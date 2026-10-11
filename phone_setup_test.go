package main

import (
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/music"
)

// Apply the hardware's 15-second inactivity policy to the production loop.
type setupDisplay struct {
	*scriptDisplay
	sleepAt time.Time
}

func (d *setupDisplay) KeepAwake() {
	if !d.asleep {
		d.sleepAt = d.now.Add(15 * time.Second)
	}
}

func (d *setupDisplay) Wait(delay time.Duration) (inputEvent, error) {
	if !d.asleep {
		delay = min(delay, max(0, d.sleepAt.Sub(d.now)))
	}
	e, err := d.scriptDisplay.Wait(delay)
	if e.Kind == inputWake {
		d.sleepAt = d.now.Add(15 * time.Second)
	}
	if err == nil && e.Kind == inputRefresh && !d.asleep && !d.now.Before(d.sleepAt) {
		d.asleep = true
		e.Kind = inputSleep
	}
	return e, err
}

func TestWakingPhoneSetupRestartsVisiblePairingWindow(t *testing.T) {
	d := &setupDisplay{scriptDisplay: newScriptDisplay()}
	d.sleepAt = d.now.Add(15 * time.Second)
	u := newWatchUI(firmwareConfirmed)
	u.openPhone()
	u.phoneAuto = true
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r}
	d.steps = []scriptStep{
		{at: 20 * time.Second, event: inputEvent{Kind: inputSleep}},
		{at: 25 * time.Second, event: inputEvent{Kind: inputWake}},
		{at: time.Minute, event: inputEvent{Kind: inputQuit}, check: func() {
			if d.asleep || !c.running || r.stops != 0 {
				t.Fatal("waking Phone lost the setup hold and slept after 15 seconds")
			}
		}},
	}
	l := watchLoop{display: d, ui: &u, sync: &c, renderer: &frameRenderer{},
		now: func() time.Time { return d.now }, state: func() updateState { return firmwareConfirmed }}
	if err := l.run(); err != nil {
		t.Fatal(err)
	}
}

func TestPhoneSetupStaysAwakeThenResumesNormalSleep(t *testing.T) {
	for _, link := range []byte{music.LinkDisconnected, music.LinkSecuring} {
		d := &setupDisplay{scriptDisplay: newScriptDisplay()}
		d.sleepAt = d.now.Add(15 * time.Second)
		u := newWatchUI(firmwareConfirmed)
		u.openPhone()
		u.handle(inputEvent{Kind: inputTap, X: 120, Y: 110}, d.now, firmwareConfirmed, powerStatus{})
		if u.page == pageBatteryConfirm {
			u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, d.now, firmwareConfirmed, powerStatus{})
		}
		r := &fakePhoneRadio{}
		r.report(link, false, 1)
		c := timeSyncController{radio: r}
		d.steps = []scriptStep{
			{at: time.Minute, check: func() {
				if d.asleep || !c.running || r.stops != 0 {
					t.Fatal("setup slept or closed the radio before a pairing code")
				}
			}},
			{at: phoneSetupDuration + 16*time.Second, event: inputEvent{Kind: inputQuit}, check: func() {
				if !d.asleep || !c.running || r.stops != 0 || !u.phoneAuto {
					t.Fatal("setup left the screen on forever or stopped the persistent radio")
				}
			}},
		}
		l := watchLoop{display: d, ui: &u, sync: &c, renderer: &frameRenderer{},
			now: func() time.Time { return d.now }, state: func() updateState { return firmwareConfirmed }}
		if err := l.run(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPhoneSetupDefersSettingsSaveBeforeAndDuringSecurity(t *testing.T) {
	f, j, _ := clockTestStorage(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	p := loadSettings(&u, now, j)
	u.openPhone()
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 110}, now, firmwareConfirmed, powerStatus{})
	if u.page == pageBatteryConfirm {
		u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
	}
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r}
	c.update(&u, now, 80)
	c.saveSettings(&u, p, now, true)
	if due := c.saveSettings(&u, p, now.Add(settingsSaveDelay), true); !due.IsZero() || !c.running || r.stops != 0 || f.writes != 0 {
		t.Fatal("saving CONNECT PHONE interrupted discovery before the PIN")
	}
	r.report(music.LinkSecuring, false, 1)
	c.update(&u, now.Add(3*time.Second), 80)
	u.page = pageClock // SMP must also survive leaving Phone or screen sleep.
	c.saveSettings(&u, p, now.Add(phoneSetupDuration+time.Second), true)
	if !c.running || r.stops != 0 || f.writes != 0 {
		t.Fatal("pending settings save interrupted active security")
	}
	r.report(music.LinkAuthenticated, false, 1)
	c.update(&u, now.Add(phoneSetupDuration+2*time.Second), 80)
	c.saveSettings(&u, p, now.Add(phoneSetupDuration+2*time.Second), true)
	if f.writes == 0 || r.stops != 1 || c.running {
		t.Fatal("settings did not resume after authentication")
	}
	c.update(&u, now.Add(phoneSetupDuration+3*time.Second), 80)
	if !c.running || r.starts != 2 || !u.phoneAuto {
		t.Fatal("saved persistent connection did not resume")
	}
}

func TestPhoneSetupWaitingSaveResumesAtBoundedDeadline(t *testing.T) {
	f, j, _ := clockTestStorage(t)
	now := time.Now()
	u := newWatchUI(firmwareConfirmed)
	p := loadSettings(&u, now, j)
	u.openPhone()
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 110}, now, firmwareConfirmed, powerStatus{})
	if u.page == pageBatteryConfirm {
		u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
	}
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r}
	c.update(&u, now, 80)
	c.saveSettings(&u, p, now, true)
	if due := nextLoopDelay(now, &u, now.Add(time.Hour), false); due != phoneSetupDuration {
		t.Fatal("sleeping save lost setup expiry", due)
	}
	c.saveSettings(&u, p, now.Add(phoneSetupDuration), true)
	if f.writes == 0 || r.stops != 1 {
		t.Fatal("waiting for an absent phone blocked settings indefinitely")
	}
}

func TestSecondDevicePairingDefersSettingsSaveWithFirstDevicePlaying(t *testing.T) {
	f, j, _ := clockTestStorage(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	p := loadSettings(&u, now, j)
	u.openPhone()
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 110}, now, firmwareConfirmed, powerStatus{})
	if u.page == pageBatteryConfirm {
		u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
	}
	r := &twoDeviceRadio{peers: 2, trusted: 1}
	r.report(music.LinkMusicReady, true, 1)
	c := timeSyncController{radio: r}
	c.update(&u, now, 80)
	c.saveSettings(&u, p, now, true)
	u.page = pageClock
	c.saveSettings(&u, p, now.Add(phoneSetupDuration+time.Second), true)
	if !c.running || r.stops != 0 || f.writes != 0 {
		t.Fatal("first device's music state hid second device security from settings persistence")
	}
	r.trusted = 2
	c.update(&u, now.Add(phoneSetupDuration+2*time.Second), 80)
	c.saveSettings(&u, p, now.Add(phoneSetupDuration+2*time.Second), true)
	if f.writes == 0 || r.stops != 1 || c.running {
		t.Fatal("settings save failed to resume after both devices authenticated")
	}
}

func TestPhoneSetupAllowsButtonSleepAndStopsKeepingAwakeAfterAuthentication(t *testing.T) {
	for _, manual := range []bool{false, true} {
		d := &setupDisplay{scriptDisplay: newScriptDisplay()}
		d.sleepAt = d.now.Add(15 * time.Second)
		u := newWatchUI(firmwareConfirmed)
		u.openPhone()
		u.phoneAuto = true
		r := &fakePhoneRadio{}
		c := timeSyncController{radio: r}
		first := scriptStep{at: 20 * time.Second, event: inputEvent{Kind: inputRefresh}, check: func() {
			r.report(music.LinkAuthenticated, false, 1)
		}}
		if manual {
			first.event.Kind = inputSleep
			first.check = nil
		}
		d.steps = []scriptStep{first, {at: 40 * time.Second, event: inputEvent{Kind: inputQuit}, check: func() {
			if !d.asleep || !c.running || r.stops != 0 || !u.phoneAuto {
				t.Fatal("setup prevented normal sleep or shut off the shared radio", manual)
			}
		}}}
		l := watchLoop{display: d, ui: &u, sync: &c, renderer: &frameRenderer{},
			now: func() time.Time { return d.now }, state: func() updateState { return firmwareConfirmed }}
		if err := l.run(); err != nil {
			t.Fatal(err)
		}
	}
}
