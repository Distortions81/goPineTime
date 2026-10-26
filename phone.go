package main

import (
	"github.com/Distortions81/goPine/internal/music"
	"github.com/Distortions81/goPine/internal/timesync"
	"github.com/Distortions81/goPine/internal/uifont"
	"time"
)

type phoneMode uint8

const (
	phoneOff phoneMode = iota
	phoneSession
	phoneConnected
)
const phoneSessionDuration = 10 * time.Minute
const phoneSetupDuration = 2 * time.Minute

type phoneRadio interface {
	weatherRadio
	StartPhone([10]byte, uint8, time.Duration) error
	TakeMusic() ([music.SnapshotSize]byte, bool, error)
	MusicCommand(byte, uint32) error
}
type phonePeerRadio interface {
	PeerCount() int
	TrustedPeerCount() int
}
type phoneState struct {
	mode                       phoneMode
	expires                    time.Time
	music                      music.State
	status                     string
	musicNote                  string
	revision                   uint32
	command                    byte
	queued, announced, restart bool
	retryAt                    time.Time
	setupUntil                 time.Time
	setupRequested             bool
	advertiseRequested         bool
	peers, trustedPeers        uint8
}

func (u *watchUI) openMusic() {
	if u.phone == nil {
		u.phone = &phoneState{status: "Bluetooth off"}
	}
	u.page = pageMusic
}
func (u *watchUI) openPhone() {
	if u.phone == nil {
		u.phone = &phoneState{status: "Bluetooth off"}
	}
	u.page = pagePhone
	u.phone.setupRequested, u.phone.advertiseRequested = true, true
}

// Setup keeps the screen visible for a bounded time, while the persistent
// radio remains available afterward. Do not interrupt SMP to save settings.
func (u *watchUI) phoneSetupActive(now time.Time) bool {
	p := u.phone
	return p != nil && p.mode != phoneOff && now.Before(p.setupUntil) &&
		(p.peers > p.trustedPeers || p.music.Link == music.LinkDisconnected || p.music.Link == music.LinkSecuring)
}

func (u *watchUI) phoneSetupVisible(now time.Time) bool {
	return (u.page == pagePhone || u.page == pagePairingSettings) && u.phoneSetupActive(now)
}
func (p *phoneState) clearLink() {
	if p.music != (music.State{}) || p.musicNote != "" {
		p.music = music.State{}
		p.musicNote = ""
		p.revision++
	}
	p.queued, p.announced = false, false
	p.peers, p.trustedPeers = 0, 0
}

func (p *phoneState) setMusicNote(note string) {
	if p.musicNote != note {
		p.musicNote = note
		p.revision++
	}
}

func (p *phoneState) musicStatus() string {
	if p.musicNote != "" {
		return p.musicNote
	}
	if p.music.Link == music.LinkAuthenticated {
		return "Open music in app"
	}
	return p.status
}
func (u *watchUI) handlePhone(e inputEvent, now time.Time, state updateState) {
	p := u.phone
	if p == nil {
		u.openMusic()
		return
	}
	if e.Kind == inputSwipeRight || (e.Kind == inputTap && inRect(e, 0, 0, 60, 44)) {
		u.back(state)
		return
	}
	// Both paired devices remain available while the screen sleeps.
	if e.Kind != inputTap {
		return
	}
	if u.page == pagePhone {
		switch {
		case inRect(e, 16, 88, 224, 132):
			if !u.phoneAuto && p.mode == phoneOff {
				u.askBattery(batteryBluetooth)
			} else {
				u.enablePhone(now)
			}
		case inRect(e, 16, 136, 224, 180):
			u.openPairingSettings()
		case inRect(e, 16, 184, 224, 228) && (u.phoneAuto || p.mode != phoneOff):
			u.phoneAuto, p.mode = false, phoneOff
			p.setupUntil, p.setupRequested = time.Time{}, false
			p.status = "Bluetooth off"
			p.clearLink()
		default:
			return
		}
		return
	}
	if p.mode == phoneOff || p.music.Link != music.LinkMusicReady {
		return
	}
	command := byte(0)
	switch {
	case inRect(e, 12, 66, 228, 124):
		command = music.Open
	case inRect(e, 12, 130, 78, 174):
		command = music.Previous
	case inRect(e, 84, 130, 156, 174):
		if !p.music.Known {
			return
		}
		command = music.Play
		if p.music.Playing {
			command = music.Pause
		}
	case inRect(e, 162, 130, 228, 174):
		command = music.Next
	case inRect(e, 12, 184, 114, 228):
		command = music.VolumeDown
	case inRect(e, 126, 184, 228, 228):
		command = music.VolumeUp
	default:
		return
	}
	// At most one pending gesture; never replay a command after disconnect.
	p.command, p.queued = command, true
}

func (u *watchUI) enablePhone(now time.Time) {
	p := u.phone
	u.phoneAuto, p.mode = true, phoneConnected
	p.setupUntil, p.setupRequested = now.Add(phoneSetupDuration), false
	p.retryAt = time.Time{}
	p.advertiseRequested = true
	if p.music.Link == music.LinkDisconnected {
		p.status = "Waiting for device"
	}
}
func radioBusy(r timeRadio) bool {
	if busy, ok := r.(interface{ Busy() bool }); ok {
		return busy.Busy()
	}
	return false
}

//go:noinline
func (c *timeSyncController) updatePhone(u *watchUI, now time.Time, battery uint8) {
	p := u.phone
	if p.setupRequested {
		p.setupUntil, p.setupRequested = now.Add(phoneSetupDuration), false
	}
	if p.mode == phoneSession && !now.Before(p.expires) {
		p.mode, p.status = phoneOff, "Session ended"
		p.clearLink()
		c.close()
		return
	}
	r, ok := c.radio.(phoneRadio)
	if !ok {
		u.phoneAuto = false
		p.mode, p.status = phoneOff, "Bluetooth unavailable"
		p.clearLink()
		c.close()
		return
	}
	if !c.running {
		if now.Before(p.retryAt) {
			return
		}
		if radioBusy(c.radio) {
			p.status = "Finishing disconnect"
			return
		}
		value, _ := timesync.Encode(u.clock.Now(now))
		duration := time.Duration(0)
		if p.mode == phoneSession {
			duration = p.expires.Sub(now)
		}
		if err := r.StartPhone(value, battery, duration); err != nil {
			c.radio.Stop()
			c.phoneError(u, now)
			return
		}
		c.running, c.phoneMode = true, p.mode
	}
	if p.advertiseRequested {
		p.advertiseRequested = false
		if setup, ok := c.radio.(interface{ PhoneSetup() }); ok {
			setup.PhoneSetup()
		}
	}
	counts, haveCounts := r.(phonePeerRadio)
	if haveCounts {
		p.peers, p.trustedPeers = uint8(counts.PeerCount()), uint8(counts.TrustedPeerCount())
	}
	value, changed, err := r.TakeMusic()
	if err != nil {
		c.phoneError(u, now)
		return
	}
	oldGeneration := p.music.Generation
	if changed && p.music.Apply(value) {
		p.musicNote = ""
		if oldGeneration != p.music.Generation {
			p.queued, p.announced = false, false
		}
		p.revision++
		if p.music.Link != music.LinkMusicReady {
			p.queued, p.announced = false, false
		}
		switch p.music.Link {
		case music.LinkDisconnected:
			p.status = "Waiting for device"
		case music.LinkSecuring:
			p.status = "Securing connection"
		case music.LinkMusicReady, music.LinkAuthenticated:
			p.status = "Connected"
			if !haveCounts || p.peers == p.trustedPeers {
				p.setupUntil = time.Time{}
			}
		}
	}
	if haveCounts {
		switch {
		case p.peers > p.trustedPeers:
			p.status = "Securing connection"
		case p.trustedPeers > 1:
			p.status = "2 devices connected"
		case p.trustedPeers == 1:
			p.status = "Connected"
		default:
			p.status = "Waiting for device"
		}
	}
	if p.music.Link == music.LinkMusicReady && !p.announced {
		// InfiniTime's refresh hint asks the companion for current metadata.
		// Send once per subscription; a full transmit queue must not turn into
		// an unbounded retry loop or replay on a later connection.
		p.announced = true
		if r.MusicCommand(music.Open, p.music.Generation) != nil {
			p.setMusicNote("Refresh failed")
		}
	}
	if p.queued {
		p.queued = false
		if p.music.Link != music.LinkMusicReady || r.MusicCommand(p.command, p.music.Generation) != nil {
			p.setMusicNote("Command failed")
		} else {
			p.setMusicNote("")
		}
		// Playing state is only changed by a report from the phone.
	}
	u.receiveNotifications(c.radio, now)
	c.receivePhoneTime(u, now)
	for i := 0; i < 2; i++ {
		packet, n, err := r.TakeWeather()
		if err != nil || n <= 0 || n > len(packet) {
			break
		}
		if u.weather == nil {
			u.weather = &weatherState{}
		}
		if u.weather.cache.Apply(packet[:n]) {
			u.weather.revision++
			u.weather.status = "Weather received"
			if packet[0] == 0 {
				u.weather.currentReceived = now
			} else {
				u.weather.forecastReceived = now
			}
		}
	}
}

// Retry exceptional radio failures with a deadline, never a fast awake/sleep
// polling loop. Ordinary peer disconnects are handled by BLE advertising.
func (c *timeSyncController) phoneError(u *watchUI, now time.Time) {
	p := u.phone
	c.close()
	p.clearLink()
	if u.phoneAuto {
		p.mode, p.status = phoneConnected, "Retrying connection"
		p.retryAt = now.Add(time.Minute)
	} else {
		p.mode, p.status = phoneOff, "Bluetooth error"
	}
}

// Phone-mode Take exposes only authenticated time from the paired connection.
// Legacy one-shot time sync retains its separate physical confirmation flow.
//
//go:noinline
func (c *timeSyncController) receivePhoneTime(u *watchUI, now time.Time) {
	value, size, age, err := c.radio.Take()
	if err != nil || size <= 0 || size > len(value) || age < 0 || age >= timesync.Window {
		return
	}
	value, err = timesync.Normalize(value[:size])
	if err != nil {
		return
	}
	stamp, err := timesync.Decode(value[:])
	if err != nil {
		return
	}
	stamp = stamp.Add(age)
	drift := stamp.Sub(u.clock.Now(now))
	// Discovery can resend CTS after every reconnect. Avoid a tiny clock edit,
	// flash save and reconnect cycle when the clock is already synchronized.
	if !u.clock.initialized || u.clock.approximate || drift > 2*time.Second || drift < -2*time.Second {
		u.clock.Set(now, stamp)
	}
	u.syncStatus = "Time synchronized"
}
func fitMusicText(text string) string {
	for len(text) > 0 {
		width, _ := lineWidth(&uifont.Regular18, text)
		if width <= 216 {
			break
		}
		text = text[:len(text)-1]
	}
	return text
}
func (u *watchUI) drawPhone(d canvas, now time.Time) {
	p := u.phone
	if p == nil {
		return
	}
	if u.page == pagePhone {
		centered(d, &uifont.Regular18, 29, "BLUETOOTH", white)
		centered(d, &uifont.Regular18, 62, p.status, accent)
		label := "Turn on"
		on := u.phoneAuto || p.mode != phoneOff
		if on {
			label = "Pair a device"
		}
		clockControl(d, 16, 88, 208, 44, label, positive)
		clockControl(d, 16, 136, 208, 44, "Paired devices", card)
		if on {
			clockControl(d, 16, 184, 208, 44, "Turn off", card)
		} else {
			clockControlLabel(d, 16, 184, 208, 44, "Turn off", card, disabled)
		}
		return
	}
	centered(d, &uifont.Regular18, 29, "MUSIC", white)
	status := p.musicStatus()
	if p.mode == phoneOff {
		status = "Bluetooth is off"
	}
	centered(d, &uifont.Regular18, 57, status, accent)
	if textVisible(d, &uifont.Regular18, 85, "") {
		track := music.Text(&p.music.Track)
		if p.mode == phoneOff {
			track = "Settings > Bluetooth"
		} else if track == "" {
			track = "No track information"
		}
		centered(d, &uifont.Regular18, 85, fitMusicText(track), white)
	}
	if textVisible(d, &uifont.Regular18, 112, "") {
		artist := music.Text(&p.music.Artist)
		if p.mode == phoneOff {
			artist = "to connect a device"
		} else if p.music.Track[0] == 0 && p.music.Link == music.LinkMusicReady {
			artist = "Tap here to refresh"
		}
		centered(d, &uifont.Regular18, 112, fitMusicText(artist), muted)
	}
	c := card
	if p.mode != phoneOff && p.music.Link == music.LinkMusicReady {
		c = positive
	}
	label := "--"
	if p.music.Known {
		label = "PLAY"
		if p.music.Playing {
			label = "PAUSE"
		}
	}
	clockControl(d, 12, 130, 66, 44, "PREV", c)
	clockControl(d, 84, 130, 72, 44, label, c)
	clockControl(d, 162, 130, 66, 44, "NEXT", c)
	clockControl(d, 12, 184, 102, 44, "VOL -", c)
	clockControl(d, 126, 184, 102, 44, "VOL +", c)
}
