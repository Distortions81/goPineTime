package checkpoint

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
	"unsafe"
)

var errCut = errors.New("power cut")

func TestTouchWakeRecordCompatibility(t *testing.T) {
	r := Record{HasSettings: true, Settings: Settings{CountdownSeconds: 300}}
	b := encode(r)
	// Old v2 records wrote zeros in all eight reserved bytes.
	clear(b[144:152])
	binary.LittleEndian.PutUint32(b[152:156], crc32.ChecksumIEEE(b[:152]))
	if got, ok := decode(&b); !ok || got != r || got.Settings.TouchWake {
		t.Fatal("old settings no longer load with touch wake off")
	}
	r.Settings.TouchWake = true
	b = encode(r)
	if got, ok := decode(&b); !ok || got != r {
		t.Fatal("touch wake record failed to round trip")
	}
	b[144] = 2
	binary.LittleEndian.PutUint32(b[152:156], crc32.ChecksumIEEE(b[:152]))
	if _, ok := decode(&b); ok {
		t.Fatal("accepted an unsupported touch wake value")
	}
}

type memoryFlash struct {
	data           [Size]byte
	budget         int // -1 unlimited; otherwise cut before the next word write/erase.
	writes, erases int
	failRead       bool
}

func newFlash() *memoryFlash {
	f := &memoryFlash{budget: -1}
	for i := range f.data {
		f.data[i] = 255
	}
	return f
}
func (f *memoryFlash) ReadAt(b []byte, off int64) (int, error) {
	if f.failRead {
		return 0, errCut
	}
	if off < 0 || off+int64(len(b)) > Size {
		panic("read out of bounds")
	}
	return copy(b, f.data[off:]), nil
}
func (f *memoryFlash) step() error {
	if f.budget == 0 {
		return errCut
	}
	if f.budget > 0 {
		f.budget--
	}
	return nil
}
func (f *memoryFlash) WriteAt(b []byte, off int64) (int, error) {
	if off < 0 || off%4 != 0 || len(b)%4 != 0 || off+int64(len(b)) > Size {
		panic("invalid write")
	}
	for i := 0; i < len(b); i += 4 {
		if err := f.step(); err != nil {
			return i, err
		}
		for k := 0; k < 4; k++ {
			at := int(off) + i + k
			if f.data[at]&b[i+k] != b[i+k] {
				panic("programming zero to one")
			}
			f.data[at] &= b[i+k]
		}
		f.writes++
	}
	return len(b), nil
}
func (f *memoryFlash) ErasePage(off int64) error {
	if off < 0 || off%PageSize != 0 || off+PageSize > Size {
		panic("invalid erase")
	}
	if err := f.step(); err != nil {
		return err
	}
	for i := 0; i < PageSize; i++ {
		f.data[int(off)+i] = 255
	}
	f.erases++
	return nil
}
func mustOpen(t *testing.T, f Flash) *Journal {
	t.Helper()
	j, err := Open(f)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestOpenReusesRecordReadBuffer(t *testing.T) {
	for _, magic := range []uint32{legacyMagic, headerMagic} {
		f := newFlash()
		h := versionHeader(magic)
		copy(f.data[:], h[:])
		copy(f.data[PageSize:], h[:])
		// Scan 510 legacy or 50 current slots without allocating per record.
		allocs := testing.AllocsPerRun(10, func() {
			if _, err := Open(f); err != nil {
				t.Fatal(err)
			}
		})
		if allocs > 12 {
			t.Fatalf("journal %x open allocated per record: %.0f allocations", magic, allocs)
		}
	}
}

func TestAppendRotationDedupAndBackwardDate(t *testing.T) {
	f := newFlash()
	j := mustOpen(t, f)
	for i := 0; i < 600; i++ {
		hour := uint32(10000 - i) // Date going backward must not affect newest selection.
		if err := j.Save(hour, i%2 != 0); err != nil {
			t.Fatal(err)
		}
		writes := f.writes
		if err := j.Save(hour, i%2 != 0); err != nil || f.writes != writes {
			t.Fatal("identical save wrote", err)
		}
		j = mustOpen(t, f)
		r, ok := j.Latest()
		if !ok || r != (Record{Hours: hour, Sequence: uint32(i + 1), Use24: i%2 != 0, HasTime: true}) {
			t.Fatal(i, r, ok)
		}
	}
	if f.erases != (599/slots)-1 {
		t.Fatal("unexpected page wear", f.erases)
	}
	if err := j.Save(MaxHours, false); err == nil {
		t.Fatal("accepted out-of-range date")
	}
}

func TestInterruptedSaveAtEveryWord(t *testing.T) {
	for _, count := range []int{0, 1, slots, 2 * slots} {
		base := newFlash()
		j := mustOpen(t, base)
		for i := 0; i < count; i++ {
			if err := j.Save(uint32(i), false); err != nil {
				t.Fatal(err)
			}
		}
		old, hadOld := j.Latest()
		for cut := 0; cut <= RecordSize/4+6; cut++ {
			f := *base
			f.budget = cut
			j = mustOpen(t, &f)
			err := j.Save(20000, true)
			if err != nil {
				writes := f.writes
				if retry := j.Save(20001, false); retry == nil || f.writes != writes {
					t.Fatal("retried ambiguous write")
				}
			}
			f.budget = -1
			j = mustOpen(t, &f)
			got, ok := j.Latest()
			want := Record{Hours: 20000, Sequence: old.Sequence + 1, Use24: true, HasTime: true}
			if ok && got != want && (!hadOld || got != old) {
				t.Fatalf("count %d cut %d: mixed %+v", count, cut, got)
			}
			if hadOld && !ok {
				t.Fatalf("lost old checkpoint at %d/%d", count, cut)
			}
			if err := j.Save(20001, false); err != nil {
				t.Fatal("cannot resume after cut", count, cut, err)
			}
		}
	}
}

func TestForeignDataAndCorruptLatest(t *testing.T) {
	f := newFlash()
	f.data[27] = 0
	if _, err := Open(f); err == nil || f.erases != 0 || f.writes != 0 {
		t.Fatal("adopted foreign data")
	}
	f = newFlash()
	j := mustOpen(t, f)
	if err := j.Save(100, false); err != nil {
		t.Fatal(err)
	}
	if err := j.Save(101, true); err != nil {
		t.Fatal(err)
	}
	f.data[recordOffset(0, 1)] ^= 1
	j = mustOpen(t, f)
	if r, ok := j.Latest(); !ok || r.Hours != 100 {
		t.Fatal("CRC failed", r)
	}
	if err := j.Save(102, false); err != nil {
		t.Fatal("did not skip corrupt record", err)
	}
	// Failed reads are surfaced and never interpreted as erased data.
	f.failRead = true
	if _, err := Open(f); err == nil {
		t.Fatal("ignored read failure")
	}
}

func TestSequenceWrap(t *testing.T) {
	f := newFlash()
	h := header()
	copy(f.data[:], h[:])
	r := encode(Record{Hours: 100, Sequence: ^uint32(0), HasTime: true})
	copy(f.data[headerSize:], r[:])
	j := mustOpen(t, f)
	if err := j.Save(101, false); err != nil {
		t.Fatal(err)
	}
	j = mustOpen(t, f)
	if got, _ := j.Latest(); got.Hours != 101 || got.Sequence != 0 {
		t.Fatal(got)
	}
}

func TestInterruptedPageErasePreservesNewest(t *testing.T) {
	base := newFlash()
	j := mustOpen(t, base)
	for i := 0; i < 2*slots; i++ {
		if err := j.Save(uint32(i), false); err != nil {
			t.Fatal(err)
		}
	}
	// Model power loss at different points during reclamation of page zero.
	// Page one must remain the authority even if the erased page has a torn
	// header, intact old records, or no header at all.
	for _, count := range []int{1, 4, 12, 16, 29, 1024, 2048, PageSize} {
		f := *base
		for k := 0; k < count; k++ {
			f.data[k] = 255
		}
		j = mustOpen(t, &f)
		if got, ok := j.Latest(); !ok || got.Hours != uint32(2*slots-1) {
			t.Fatal("partial erase lost latest", count, got)
		}
		if err := j.Save(20000, false); err != nil {
			t.Fatal("cannot finish rotation", count, err)
		}
	}
}

type readbackFailure struct {
	*memoryFlash
	armed bool
}

func (f *readbackFailure) WriteAt(b []byte, off int64) (int, error) {
	n, err := f.memoryFlash.WriteAt(b, off)
	// First record's commit is programmed, but its verification cannot be read.
	if f.armed && off == headerSize+RecordSize-4 {
		f.failRead = true
	}
	return n, err
}

func sampleSettings() Settings {
	s := Settings{CountdownSeconds: 300, Alarms: [5]Alarm{{Hour: 7, Minute: 30, Repeat: 2, Enabled: true}}}
	s.SetDisplay(true, 2)
	return s
}

func TestDisplayRecordCompatibility(t *testing.T) {
	// Adding display choices uses existing padding, not another word in each
	// saved/observed settings snapshot on the constrained event-loop stack.
	if unsafe.Sizeof(Settings{}) != 28 || RecordSize != 160 {
		t.Fatal("display choices enlarged settings or flash records")
	}
	r := Record{HasSettings: true, HasTime: true, Hours: 1234, Use24: true,
		Settings: Settings{CountdownSeconds: 300, TouchWake: true, FlipScreen: true, PhoneAuto: true,
			Alarms: [5]Alarm{{Hour: 7, Minute: 30, Repeat: 2, Enabled: true}}}}
	b := encode(r)
	// Previous firmware wrote zeros to these reserved bytes. Preserve every
	// other setting while defaulting the newly supported choices.
	clear(b[147:152])
	binary.LittleEndian.PutUint32(b[152:156], crc32.ChecksumIEEE(b[:152]))
	if got, ok := decode(&b); !ok || got != r || got.Settings.LightMode() || got.Settings.Brightness() != 0 {
		t.Fatal("old settings did not retain dark/default-high with alarms and phone auto")
	}
	for _, light := range []bool{false, true} {
		for brightness := uint8(0); brightness < 3; brightness++ {
			r.Settings.SetDisplay(light, brightness)
			b = encode(r)
			if got, ok := decode(&b); !ok || got != r || got.Settings.LightMode() != light || got.Settings.Brightness() != brightness {
				t.Fatal("display choice did not round-trip", light, brightness)
			}
		}
	}
	for _, invalid := range []byte{6, 7, 128, 255} {
		b[147] = invalid
		binary.LittleEndian.PutUint32(b[152:156], crc32.ChecksumIEEE(b[:152]))
		if _, ok := decode(&b); ok {
			t.Fatal("accepted unsupported display choice", invalid)
		}
	}
}

func TestSettingsAndRuntimeAtomicityAtEveryWord(t *testing.T) {
	for _, count := range []int{1, slots, 2 * slots} {
		base := newFlash()
		j := mustOpen(t, base)
		for i := 0; i < count; i++ {
			if err := j.Save(uint32(i), false); err != nil {
				t.Fatal(err)
			}
		}
		old, _ := j.Latest()
		settings := sampleSettings()
		runtime := Runtime{StopwatchRunning: true, StopwatchStarted: 1791220000000, StopwatchMillis: 12345,
			CountdownRunning: true, CountdownMillis: 250000, CountdownDeadline: 1791221000000, Timestamped: 3, ClockInitialized: true}
		for cut := 0; cut <= RecordSize/4+6; cut++ {
			f := *base
			f.budget = cut
			j = mustOpen(t, &f)
			_ = j.SaveState(settings, true, runtime)
			f.budget = -1
			j = mustOpen(t, &f)
			got, ok := j.Latest()
			want := old
			want.Sequence++
			want.Use24, want.HasSettings, want.HasRuntime = true, true, true
			want.Settings, want.Runtime = settings, runtime
			if !ok || (got != old && got != want) {
				t.Fatalf("mixed or lost snapshot count %d cut %d: %+v", count, cut, got)
			}
		}
	}
}

func TestLegacyMigrationAtEveryWord(t *testing.T) {
	base := newFlash()
	h := versionHeader(legacyMagic)
	copy(base.data[:], h[:])
	var old [16]byte
	binary.LittleEndian.PutUint32(old[:4], 20000)
	binary.LittleEndian.PutUint32(old[4:8], 7)
	binary.LittleEndian.PutUint32(old[8:12], crc32.ChecksumIEEE(old[:8]))
	binary.LittleEndian.PutUint32(old[12:], recordCommit)
	copy(base.data[headerSize:], old[:])
	for cut := 0; cut <= RecordSize/4+6; cut++ {
		f := *base
		j := mustOpen(t, &f)
		r, ok := j.Latest()
		if !ok || r.Hours != 20000 || !r.HasTime || r.HasSettings {
			t.Fatal("legacy read failed", r)
		}
		f.budget = cut
		_ = j.SaveSettings(sampleSettings(), true)
		f.budget = -1
		j = mustOpen(t, &f)
		r, ok = j.Latest()
		if !ok || r.Hours != 20000 || (r.Sequence != 7 && r.Sequence != 8) {
			t.Fatal("migration lost clock", cut, r)
		}
		if r.Sequence == 8 && (!r.HasSettings || r.Settings != sampleSettings()) {
			t.Fatal("migration mixed settings")
		}
		if err := j.SaveSettings(sampleSettings(), true); err != nil {
			t.Fatal("migration cannot resume", cut, err)
		}
		if !erased(f.data[:PageSize]) {
			t.Fatal("legacy header left live after migration")
		}
	}
}

func TestStateValidationAndDedup(t *testing.T) {
	f := newFlash()
	j := mustOpen(t, f)
	s := sampleSettings()
	if err := j.SaveState(s, true, Runtime{}); err != nil {
		t.Fatal(err)
	}
	writes := f.writes
	if err := j.SaveState(s, true, Runtime{}); err != nil || f.writes != writes {
		t.Fatal("duplicate snapshot wrote")
	}
	for _, r := range []Runtime{{Source: 6}, {Pending: 64}, {CountdownDeadline: -1}, {CountdownMillis: 86400000}, {SnoozeMillis: [5]uint32{300001}}} {
		if err := j.SaveState(s, true, r); err == nil || f.writes != writes {
			t.Fatal("invalid runtime saved", r)
		}
	}
	s.Alarms[0].Minute = 60
	if err := j.SaveSettings(s, false); err == nil || f.writes != writes {
		t.Fatal("invalid alarm saved")
	}
	f = newFlash()
	f.data[PageSize-1] = 0
	if _, err := Open(f); err == nil {
		t.Fatal("foreign data in record padding accepted")
	}
}

func TestMigrationWithBothLegacyPagesOccupied(t *testing.T) {
	base := newFlash()
	for page := 0; page < 2; page++ {
		h := versionHeader(legacyMagic)
		copy(base.data[page*PageSize:], h[:])
		var b [16]byte
		binary.LittleEndian.PutUint32(b[:4], uint32(20000+page))
		binary.LittleEndian.PutUint32(b[4:8], uint32(7+page))
		binary.LittleEndian.PutUint32(b[8:12], crc32.ChecksumIEEE(b[:8]))
		binary.LittleEndian.PutUint32(b[12:], recordCommit)
		copy(base.data[page*PageSize+headerSize:], b[:])
	}
	for cut := 0; cut <= RecordSize/4+8; cut++ {
		f := *base
		j := mustOpen(t, &f)
		f.budget = cut
		_ = j.SaveSettings(sampleSettings(), true)
		f.budget = -1
		j = mustOpen(t, &f)
		got, ok := j.Latest()
		if !ok || got.Hours != 20001 || (got.Sequence != 8 && got.Sequence != 9) {
			t.Fatal("two-page migration lost newest clock", cut, got)
		}
		if err := j.SaveSettings(sampleSettings(), true); err != nil {
			t.Fatal("two-page migration could not recover", cut, err)
		}
		if got, _ = j.Latest(); !got.HasSettings {
			t.Fatal("migration lost settings")
		}
	}
}

func TestCommittedButUncertainWriteRequiresReopen(t *testing.T) {
	f := &readbackFailure{memoryFlash: newFlash()}
	j := mustOpen(t, f)
	f.armed = true
	if err := j.Save(100, false); err == nil {
		t.Fatal("missed readback error")
	}
	f.armed, f.failRead = false, false
	writes := f.writes
	if err := j.Save(200, false); err == nil || f.writes != writes {
		t.Fatal("reused uncertain sequence")
	}
	j = mustOpen(t, f)
	if r, ok := j.Latest(); !ok || r.Hours != 100 || r.Sequence != 1 {
		t.Fatal("did not resolve committed record", r)
	}
	if err := j.Save(200, false); err != nil {
		t.Fatal(err)
	}
	if r, _ := j.Latest(); r.Sequence != 2 {
		t.Fatal("sequence was reused", r)
	}
}

func TestSaveReusesInterruptedSlotBuffer(t *testing.T) {
	base := newFlash()
	original := mustOpen(t, base)
	if err := original.Save(100, false); err != nil {
		t.Fatal(err)
	}
	// Torn records can occupy every remaining slot; a retry must skip them.
	for slot := 1; slot < slots; slot++ {
		base.data[recordOffset(0, slot)] = 0
	}
	allocs := testing.AllocsPerRun(10, func() {
		f := *base
		j := *original
		j.flash = &f
		if err := j.Save(101, false); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 8 {
		t.Fatalf("allocated per interrupted slot: %.0f allocations", allocs)
	}
}

func TestFlipRecordCompatibility(t *testing.T) {
	r := Record{HasSettings: true, Settings: Settings{CountdownSeconds: 300}}
	b := encode(r)
	if got, ok := decode(&b); !ok || got.Settings.FlipScreen {
		t.Fatal("legacy zero byte must mean normal orientation")
	}
	r.Settings.FlipScreen = true
	b = encode(r)
	if got, ok := decode(&b); !ok || !got.Settings.FlipScreen {
		t.Fatal("flip did not round-trip")
	}
}

func TestPhoneAutoRecordCompatibility(t *testing.T) {
	r := Record{HasSettings: true, Settings: Settings{CountdownSeconds: 300}}
	b := encode(r)
	if got, ok := decode(&b); !ok || got.Settings.PhoneAuto {
		t.Fatal("legacy reserved byte must keep phone off")
	}
	r.Settings.PhoneAuto = true
	b = encode(r)
	if got, ok := decode(&b); !ok || !got.Settings.PhoneAuto {
		t.Fatal("auto phone choice did not round-trip")
	}
}
