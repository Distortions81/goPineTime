// Package checkpoint stores clock anchors and watch settings.
// Append-only records alternate between two flash pages, so a save never erases
// the page holding the newest committed snapshot. There are no periodic writes.
package checkpoint

import (
	"encoding/binary"
	"errors"

	"github.com/Distortions81/goPine/internal/ieeecrc"
)

const (
	PageSize     = 4096
	Size         = 2 * PageSize
	RecordSize   = 160
	headerSize   = 16
	slots        = (PageSize - headerSize) / RecordSize
	Epoch        = int64(946684800) // 2000-01-01, local calendar represented as UTC.
	MaxHours     = uint32(876600)   // Exclusive: 2100-01-01.
	headerMagic  = 0x32485247       // GRH2: clock anchor and settings snapshot.
	legacyMagic  = 0x31485247       // GRH1: 16-byte clock-only records.
	headerCommit = 0x75a1c30e
	recordCommit = 0x693ac51e
)

type Flash interface {
	ReadAt([]byte, int64) (int, error)
	WriteAt([]byte, int64) (int, error)
	ErasePage(int64) error
}

type Record struct {
	Hours       uint32 // Clock anchor only: hours since 2000-01-01.
	Sequence    uint32
	Use24       bool
	HasTime     bool
	HasSettings bool
	Settings    Settings
	HasRuntime  bool
	Runtime     Runtime
}

type Alarm struct {
	Hour, Minute, Repeat uint8
	Enabled              bool
}

type Settings struct {
	Alarms           [5]Alarm
	CountdownSeconds uint32
	TouchWake        bool
	FlipScreen       bool
	PhoneAuto        bool
	Display          uint8 // Bit 0: light theme. Bits 1–2: High(0), Low(1), Medium(2).
}

func (s Settings) LightMode() bool   { return s.Display&1 != 0 }
func (s Settings) Brightness() uint8 { return s.Display >> 1 }
func (s *Settings) SetDisplay(light bool, brightness uint8) {
	if brightness > 2 {
		brightness = 0
	}
	s.Display = brightness << 1
	if light {
		s.Display |= 1
	}
}

// Runtime timestamps are local calendar fields encoded as Unix milliseconds,
// matching the clock handoff. They are estimates when the reboot clock is.
type Runtime struct {
	StopwatchStarted                           int64
	StopwatchMillis, LapMillis                 uint64
	CountdownMillis                            uint32
	StopwatchRunning, CountdownRunning, Active bool
	ClockInitialized                           bool
	Timestamped                                uint8
	Pending, Source                            uint8
	CountdownDeadline                          int64
	Snooze                                     [5]int64
	SnoozeMillis                               [5]uint32
	RingSince                                  int64
	AlertMillis                                uint32
}

func (r Runtime) valid() bool {
	const lastStamp = int64(4102444800000) // 2100-01-01
	const maxMillis = uint64((1<<63 - 1) / 1000000)
	stampOK := func(v int64) bool { return v >= 0 && v < lastStamp }
	if !stampOK(r.StopwatchStarted) || !stampOK(r.CountdownDeadline) || !stampOK(r.RingSince) ||
		r.StopwatchMillis > maxMillis || r.LapMillis > maxMillis || r.CountdownMillis >= 24*60*60*1000 || r.Pending>>6 != 0 || r.Source > 5 {
		return false
	}
	if r.AlertMillis > 60000 {
		return false
	}
	for i, v := range r.Snooze {
		if !stampOK(v) || r.SnoozeMillis[i] > 300000 {
			return false
		}
	}
	return true
}

func (s Settings) valid() bool {
	if s.CountdownSeconds == 0 || s.CountdownSeconds >= 24*60*60 || s.Display > 5 {
		return false
	}
	for _, a := range s.Alarms {
		if a.Hour > 23 || a.Minute > 59 || a.Repeat > 2 {
			return false
		}
	}
	return true
}

type Journal struct {
	flash        Flash
	latest       Record
	found        bool
	page, slot   int
	owned, blank [2]bool
	legacy       [2]bool
	failed       bool // An uncertain I/O outcome requires reopening before retry.
}

func erased(b []byte) bool {
	for _, v := range b {
		if v != 255 {
			return false
		}
	}
	return true
}
func read(f Flash, b []byte, off int64) error {
	n, err := f.ReadAt(b, off)
	if err != nil {
		return err
	}
	if n != len(b) {
		return errors.New("short checkpoint read")
	}
	return nil
}
func write(f Flash, b []byte, off int64) error {
	n, err := f.WriteAt(b, off)
	if err != nil {
		return err
	}
	if n != len(b) {
		return errors.New("short checkpoint write")
	}
	return nil
}
func header() [headerSize]byte {
	return versionHeader(headerMagic)
}
func versionHeader(magic uint32) [headerSize]byte {
	var b [headerSize]byte
	binary.LittleEndian.PutUint32(b[:4], magic)
	binary.LittleEndian.PutUint32(b[4:8], Size)
	binary.LittleEndian.PutUint32(b[8:12], ieeecrc.Checksum(b[:8]))
	binary.LittleEndian.PutUint32(b[12:], headerCommit)
	return b
}

func partialHeader(h, want [headerSize]byte) bool {
	gap := false
	for k := 0; k < headerSize; k += 4 {
		if erased(h[k : k+4]) {
			gap = true
		} else if gap || binary.LittleEndian.Uint32(h[k:k+4]) != binary.LittleEndian.Uint32(want[k:k+4]) {
			return false
		}
	}
	return true
}

func Open(f Flash) (*Journal, error) {
	j := &Journal{flash: f, page: -1, slot: -1, blank: [2]bool{true, true}}
	want := header()
	oldHeader := versionHeader(legacyMagic)
	partial := true
	// ReadAt may make this escape under TinyGo. Reuse one buffer instead of
	// allocating one for every record (up to 510 when opening a v1 journal).
	var b [RecordSize]byte
	for page := 0; page < 2; page++ {
		var h [headerSize]byte
		if err := read(f, h[:], int64(page*PageSize)); err != nil {
			return nil, err
		}
		j.legacy[page] = h == oldHeader
		j.owned[page] = h == want || j.legacy[page]
		// Allow a first initialization interrupted between header word writes;
		// otherwise fail closed on unrecognized data rather than erase it.
		partial = partial && (partialHeader(h, want) || partialHeader(h, oldHeader))
		if !erased(h[:]) {
			j.blank[page] = false
		}
		size := RecordSize
		if j.legacy[page] {
			size = 16
		}
		for slot := 0; slot < (PageSize-headerSize)/size; slot++ {
			if err := read(f, b[:size], int64(page*PageSize+headerSize+slot*size)); err != nil {
				return nil, err
			}
			if !erased(b[:size]) {
				j.blank[page], partial = false, false
			}
			if !j.owned[page] {
				continue
			}
			var r Record
			var ok bool
			if j.legacy[page] {
				r, ok = decodeLegacy(b[:16])
			} else {
				r, ok = decode(&b)
			}
			if ok && (!j.found || int32(r.Sequence-j.latest.Sequence) > 0) {
				j.latest, j.found, j.page, j.slot = r, true, page, slot
			}
		}
		// The v2 record size leaves a short unused tail; it still belongs to the
		// arena and must be checked before treating unowned storage as blank.
		used := headerSize + (PageSize-headerSize)/size*size
		if err := read(f, b[:PageSize-used], int64(page*PageSize+used)); err != nil {
			return nil, err
		}
		if !erased(b[:PageSize-used]) {
			j.blank[page], partial = false, false
		}
	}
	// A valid header claims this entire two-page arena, allowing reclamation
	// of the other page after interrupted rotation. No header: no foreign erase.
	if !j.owned[0] && !j.owned[1] && !(j.blank[0] && j.blank[1]) && !partial {
		return nil, errors.New("clock storage contains unrecognized data")
	}
	return j, nil
}

func recordOffset(page, slot int) int64   { return int64(page*PageSize + headerSize + slot*RecordSize) }
func (j *Journal) Latest() (Record, bool) { return j.latest, j.found }
func encode(r Record) [RecordSize]byte {
	var b [RecordSize]byte
	packed := r.Hours
	if r.Use24 {
		packed |= 1 << 20
	}
	if r.HasTime {
		packed |= 1 << 21
	}
	if r.HasSettings {
		packed |= 1 << 22
	}
	if r.HasRuntime {
		packed |= 1 << 23
	}
	binary.LittleEndian.PutUint32(b[:4], packed)
	binary.LittleEndian.PutUint32(b[4:8], r.Sequence)
	binary.LittleEndian.PutUint32(b[8:12], r.Settings.CountdownSeconds)
	for i, a := range r.Settings.Alarms {
		off := 12 + i*4
		b[off], b[off+1], b[off+2] = a.Hour, a.Minute, a.Repeat
		if a.Enabled {
			b[off+3] = 1
		}
	}
	rt := r.Runtime
	binary.LittleEndian.PutUint64(b[32:40], uint64(rt.StopwatchStarted))
	binary.LittleEndian.PutUint64(b[40:48], rt.StopwatchMillis)
	binary.LittleEndian.PutUint64(b[48:56], rt.LapMillis)
	binary.LittleEndian.PutUint32(b[56:60], rt.CountdownMillis)
	if rt.StopwatchRunning {
		b[60] |= 1
	}
	if rt.CountdownRunning {
		b[60] |= 2
	}
	if rt.Active {
		b[60] |= 4
	}
	if rt.ClockInitialized {
		b[60] |= 8
	}
	b[61], b[62] = rt.Pending, rt.Source
	b[63] = rt.Timestamped
	binary.LittleEndian.PutUint64(b[64:72], uint64(rt.CountdownDeadline))
	for i, v := range rt.Snooze {
		binary.LittleEndian.PutUint64(b[72+i*8:80+i*8], uint64(v))
	}
	binary.LittleEndian.PutUint64(b[112:120], uint64(rt.RingSince))
	for i, v := range rt.SnoozeMillis {
		binary.LittleEndian.PutUint32(b[120+i*4:124+i*4], v)
	}
	binary.LittleEndian.PutUint32(b[140:144], rt.AlertMillis)
	// Previously reserved zero byte: existing records default to touch wake off.
	if r.Settings.TouchWake {
		b[144] = 1
	}
	if r.Settings.FlipScreen {
		b[145] = 1
	}
	if r.Settings.PhoneAuto {
		b[146] = 1
	}
	// A reserved zero byte gives existing records dark theme and high brightness.
	b[147] = r.Settings.Display
	binary.LittleEndian.PutUint32(b[RecordSize-8:RecordSize-4], ieeecrc.Checksum(b[:RecordSize-8]))
	binary.LittleEndian.PutUint32(b[RecordSize-4:], recordCommit)
	return b
}

// Decode the reusable read buffer in place; copying this array into a function
// parameter otherwise creates another heap allocation for the CRC call.
func decode(b *[RecordSize]byte) (Record, bool) {
	packed := binary.LittleEndian.Uint32(b[:4])
	r := Record{Hours: packed & 0xfffff, Sequence: binary.LittleEndian.Uint32(b[4:8]), Use24: packed&(1<<20) != 0,
		HasTime: packed&(1<<21) != 0, HasSettings: packed&(1<<22) != 0, HasRuntime: packed&(1<<23) != 0}
	r.Settings.CountdownSeconds = binary.LittleEndian.Uint32(b[8:12])
	valid := binary.LittleEndian.Uint32(b[RecordSize-4:]) == recordCommit && binary.LittleEndian.Uint32(b[RecordSize-8:RecordSize-4]) == ieeecrc.Checksum(b[:RecordSize-8]) && packed>>24 == 0 && r.Hours < MaxHours
	for i := range r.Settings.Alarms {
		off := 12 + i*4
		r.Settings.Alarms[i] = Alarm{b[off], b[off+1], b[off+2], b[off+3] == 1}
		valid = valid && b[off+3] <= 1
	}
	r.Runtime = Runtime{StopwatchStarted: int64(binary.LittleEndian.Uint64(b[32:40])),
		StopwatchMillis: binary.LittleEndian.Uint64(b[40:48]), LapMillis: binary.LittleEndian.Uint64(b[48:56]),
		CountdownMillis: binary.LittleEndian.Uint32(b[56:60]), StopwatchRunning: b[60]&1 != 0, CountdownRunning: b[60]&2 != 0, Active: b[60]&4 != 0,
		Pending: b[61], Source: b[62], Timestamped: b[63], ClockInitialized: b[60]&8 != 0,
		CountdownDeadline: int64(binary.LittleEndian.Uint64(b[64:72])), RingSince: int64(binary.LittleEndian.Uint64(b[112:120]))}
	for i := range r.Runtime.Snooze {
		r.Runtime.Snooze[i] = int64(binary.LittleEndian.Uint64(b[72+i*8 : 80+i*8]))
	}
	for i := range r.Runtime.SnoozeMillis {
		r.Runtime.SnoozeMillis[i] = binary.LittleEndian.Uint32(b[120+i*4 : 124+i*4])
	}
	r.Runtime.AlertMillis = binary.LittleEndian.Uint32(b[140:144])
	r.Settings.TouchWake = b[144] == 1
	r.Settings.FlipScreen = b[145] == 1
	r.Settings.PhoneAuto = b[146] == 1
	r.Settings.Display = b[147]
	valid = valid && b[60]>>4 == 0 && b[144] <= 1 && b[145] <= 1 && b[146] <= 1 && b[147] <= 5
	for _, v := range b[148 : RecordSize-8] {
		valid = valid && v == 0
	}
	valid = valid && (!r.HasSettings || r.Settings.valid()) && (!r.HasRuntime || (r.HasSettings && r.Runtime.valid()))
	return r, valid
}

func decodeLegacy(b []byte) (Record, bool) {
	packed := binary.LittleEndian.Uint32(b[:4])
	r := Record{Hours: packed & 0xfffff, Sequence: binary.LittleEndian.Uint32(b[4:8]), Use24: packed&(1<<20) != 0, HasTime: true}
	return r, binary.LittleEndian.Uint32(b[12:]) == recordCommit && binary.LittleEndian.Uint32(b[8:12]) == ieeecrc.Checksum(b[:8]) && packed>>21 == 0 && r.Hours < MaxHours
}

// Save skips an identical hour/format, assigning sequence independently of
// civil time (which can move backward). Clear paired registers before calling.
// Errors stop writes on this instance: only Open can resolve an uncertain save.
func (j *Journal) Save(hours uint32, use24 bool) error {
	if hours >= MaxHours {
		return errors.New("invalid checkpoint hour")
	}
	r := j.latest
	r.Hours, r.Use24, r.HasTime = hours, use24, true
	return j.save(r)
}

func (j *Journal) SaveSettings(settings Settings, use24 bool) error {
	if !settings.valid() {
		return errors.New("invalid settings")
	}
	r := j.latest
	r.Settings, r.Use24, r.HasSettings = settings, use24, true
	return j.save(r)
}

func (j *Journal) SaveState(settings Settings, use24 bool, runtime Runtime) error {
	if !settings.valid() || !runtime.valid() {
		return errors.New("invalid watch state")
	}
	r := j.latest
	r.Settings, r.Use24, r.HasSettings = settings, use24, true
	r.Runtime, r.HasRuntime = runtime, true
	return j.save(r)
}

// Once a v2 snapshot is committed, retire legacy headers. Old firmware then
// fails closed instead of pairing stale v1 anchors with the two-bit retained
// sequence after many settings saves. An interrupted migration keeps at least
// one full snapshot, and cannot advance the sequence again before cleanup.
func (j *Journal) retireLegacy() error {
	for page := range j.legacy {
		if j.legacy[page] {
			if err := j.flash.ErasePage(int64(page * PageSize)); err != nil {
				return err
			}
			j.legacy[page], j.owned[page], j.blank[page] = false, false, true
		}
	}
	return nil
}

func (j *Journal) save(r Record) error {
	if j.failed {
		return errors.New("clock storage needs reopen")
	}
	if j.found && r == j.latest && !j.legacy[j.page] {
		j.failed = true
		if err := j.retireLegacy(); err != nil {
			return err
		}
		j.failed = false
		return nil
	}
	j.failed = true
	page, slot := j.page, j.slot+1
	if page < 0 {
		page, slot = 0, 0
	} else if j.legacy[page] {
		page, slot = 1-page, 0
	}
	// Reuse the scan buffer for readback, including interrupted-record skips.
	var check [RecordSize]byte
	for j.owned[page] && !j.legacy[page] && slot < slots {
		if err := read(j.flash, check[:], recordOffset(page, slot)); err != nil {
			return err
		}
		if erased(check[:]) {
			break
		}
		slot++ // Skip interrupted records; never program over them.
	}
	initialize := !j.owned[page] || j.legacy[page]
	if slot >= slots {
		page, slot = 1-page, 0
		initialize = true
	}
	if initialize {
		if !j.blank[page] {
			if err := j.flash.ErasePage(int64(page * PageSize)); err != nil {
				return err
			}
		}
		h := header()
		if err := write(j.flash, h[:12], int64(page*PageSize)); err != nil {
			return err
		}
		if err := write(j.flash, h[12:], int64(page*PageSize+12)); err != nil {
			return err
		}
		var check [headerSize]byte
		if err := read(j.flash, check[:], int64(page*PageSize)); err != nil {
			return err
		}
		if check != h {
			return errors.New("clock header readback failed")
		}
		j.owned[page] = true
		j.legacy[page] = false
	}
	j.blank[page] = false
	r.Sequence = j.latest.Sequence + 1
	b := encode(r)
	off := recordOffset(page, slot)
	if err := write(j.flash, b[:RecordSize-4], off); err != nil {
		return err
	}
	if err := write(j.flash, b[RecordSize-4:], off+RecordSize-4); err != nil {
		return err
	}
	if err := read(j.flash, check[:], off); err != nil {
		return err
	}
	if check != b {
		return errors.New("clock record readback failed")
	}
	j.latest, j.found, j.page, j.slot = r, true, page, slot
	if err := j.retireLegacy(); err != nil {
		return err
	}
	j.failed = false
	return nil
}
