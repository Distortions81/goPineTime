package main

import (
	"encoding/binary"
	"errors"
	"github.com/Distortions81/goPine/internal/music"
	"github.com/Distortions81/goPine/internal/timesync"
	"testing"
	"time"
)

type fakePhoneRadio struct {
	fakeWeatherRadio
	value             [music.SnapshotSize]byte
	changed, draining bool
	commands          []byte
	generation        uint32
	setups            int
	commandError      error
}

type twoDeviceRadio struct {
	fakePhoneRadio
	peers, trusted int
}

func (r *twoDeviceRadio) PeerCount() int        { return r.peers }
func (r *twoDeviceRadio) TrustedPeerCount() int { return r.trusted }

func TestPhoneStatusTracksBothDevicesIndependentlyOfMusic(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneConnected)
	r := &twoDeviceRadio{peers: 1, trusted: 1}
	r.report(music.LinkMusicReady, true, 1)
	c := timeSyncController{radio: r}
	c.update(&u, now, 80)
	u.openPhone()
	r.peers = 2
	c.update(&u, now.Add(time.Second), 80)
	if u.phone.status != "Securing connection" || !u.phoneSetupActive(now.Add(time.Second)) || u.phone.music.Generation != 1 {
		t.Fatal("second pairing lost setup protection or the first music source")
	}
	r.trusted = 2
	c.update(&u, now.Add(2*time.Second), 80)
	if u.phone.status != "2 devices connected" || u.phoneSetupActive(now.Add(2*time.Second)) {
		t.Fatal("second authentication did not update connection status")
	}
	r.peers, r.trusted = 1, 1
	c.update(&u, now.Add(3*time.Second), 80)
	if u.phone.status != "Connected" || r.stops != 0 || u.phone.music.Generation != 1 {
		t.Fatal("second disconnect interrupted the first device")
	}
}

func (r *fakePhoneRadio) PhoneSetup() { r.setups++ }

func (r *fakePhoneRadio) UpdateBattery(value uint8) { r.battery = value }

func TestPhoneBatteryUsesExistingSample(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneConnected)
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r}
	c.update(&u, now, 80)
	c.update(&u, now.Add(time.Second), 79)
	if r.battery != 79 || r.starts != 1 {
		t.Fatal("battery not refreshed within existing connection")
	}
	c.update(&u, now.Add(2*time.Second), 255)
	if r.battery != 100 {
		t.Fatal("battery outside percent range")
	}
}

func (r *fakePhoneRadio) StartPhone(_ [10]byte, battery uint8, window time.Duration) error {
	r.starts++
	r.battery, r.window = battery, window
	return r.err
}
func (r *fakePhoneRadio) TakeMusic() ([music.SnapshotSize]byte, bool, error) {
	changed := r.changed
	r.changed = false
	return r.value, changed, r.err
}
func (r *fakePhoneRadio) MusicCommand(command byte, generation uint32) error {
	if generation != r.generation {
		return errors.New("stale connection")
	}
	if r.commandError != nil {
		return r.commandError
	}
	r.commands = append(r.commands, command)
	return nil
}
func (r *fakePhoneRadio) Busy() bool { return r.draining }
func (r *fakePhoneRadio) report(link byte, playing bool, generation uint32) {
	r.value = [music.SnapshotSize]byte{}
	copy(r.value[:40], "A song on your phone")
	copy(r.value[40:80], "The artist")
	r.value[81], r.value[82] = link, 1
	if playing {
		r.value[80] = 1
	}
	binary.LittleEndian.PutUint32(r.value[83:], generation)
	r.changed, r.generation = true, generation
}
func samplePhone(now time.Time) *phoneState {
	p := &phoneState{mode: phoneSession, expires: now.Add(phoneSessionDuration), status: "Connected"}
	r := &fakePhoneRadio{}
	r.report(music.LinkMusicReady, true, 1)
	p.music.Apply(r.value)
	return p
}
func phoneUI(now time.Time, mode phoneMode) watchUI {
	u := newWatchUI(firmwareConfirmed)
	u.openMusic()
	u.phone.mode, u.phone.expires = mode, now.Add(phoneSessionDuration)
	return u
}
func TestPhoneDefaultsOffAndAutoConnectionSurvivesSleep(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	u := phoneUI(now, phoneOff)
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r}
	c.update(&u, now, 73)
	if r.starts != 0 {
		t.Fatal("opening music started Bluetooth")
	}
	u.openPhone()
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 110}, now, firmwareConfirmed, powerStatus{})
	if u.page == pageBatteryConfirm {
		u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
	}
	c.update(&u, now, 73)
	if r.starts != 1 || r.window != 0 || !u.phoneAuto {
		t.Fatal("auto connection not enabled")
	}
	for _, pg := range []page{pageMusic, pageWeather, pageTimeSettings, pageClock, pageAlert} {
		u.page = pg
		u.handle(inputEvent{Kind: inputSleep}, now, firmwareConfirmed, powerStatus{})
		c.update(&u, now.Add(time.Second), 73)
		if !c.running || r.stops != 0 {
			t.Fatal("screen stopped shared connection", pg)
		}
	}
	c.update(&u, now.Add(24*time.Hour), 73)
	if !c.running {
		t.Fatal("auto connection expired")
	}
	u.openPhone()
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 202}, now, firmwareConfirmed, powerStatus{})
	c.update(&u, now, 73)
	if c.running || u.phoneAuto || u.phone.mode != phoneOff {
		t.Fatal("Off did not stop shared connection")
	}
}
func TestPhoneCommandsAndPeerChange(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneConnected)
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r}
	r.report(music.LinkMusicReady, true, 1)
	c.update(&u, now, 73)
	if len(r.commands) != 1 || r.commands[0] != music.Open {
		t.Fatal("missing refresh hint")
	}
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 150}, now, firmwareConfirmed, powerStatus{})
	c.update(&u, now, 73)
	if len(r.commands) != 2 || r.commands[1] != music.Pause || !u.phone.music.Playing {
		t.Fatal("pause command or optimistic state")
	}
	// A disconnect/reconnect can finish between UI passes. Do not deliver the
	// old gesture to the new connection, even if it already subscribed again.
	u.handle(inputEvent{Kind: inputTap, X: 190, Y: 150}, now, firmwareConfirmed, powerStatus{})
	r.report(music.LinkMusicReady, false, 3)
	c.update(&u, now, 73)
	if len(r.commands) != 3 || r.commands[2] != music.Open {
		t.Fatal("replayed gesture across peers", r.commands)
	}
	r.report(music.LinkDisconnected, false, 4)
	c.update(&u, now, 73)
	if music.Text(&u.phone.music.Track) != "" || u.phone.music.Known {
		t.Fatal("stale metadata survived disconnect")
	}
	u.handle(inputEvent{Kind: inputTap, X: 190, Y: 150}, now, firmwareConfirmed, powerStatus{})
	c.update(&u, now, 73)
	if len(r.commands) != 3 {
		t.Fatal("sent without subscription")
	}
}
func TestPhoneCommandFailureAndWeatherCache(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	u := phoneUI(now, phoneConnected)
	before := u.clock
	a, b := weatherPackets(now)
	r := &fakePhoneRadio{fakeWeatherRadio: fakeWeatherRadio{packets: [][]byte{a, b}}}
	c := timeSyncController{radio: r}
	r.report(music.LinkMusicReady, false, 1)
	c.update(&u, now, 80)
	if u.weather == nil || !u.weather.cache.HasForecast || u.clock != before || !c.running {
		t.Fatal("weather interrupted phone/time")
	}
	r.commandError = errors.New("full")
	beforeFrame := u.frameKey(now, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 60, Y: 200}, now, firmwareConfirmed, powerStatus{})
	c.update(&u, now, 80)
	if u.phone.queued || u.phone.musicStatus() != "Command failed" || u.phone.status != "Connected" {
		t.Fatal("command retries not bounded")
	}
	if u.frameKey(now, powerStatus{}) == beforeFrame {
		t.Fatal("music error did not repaint")
	}
	u.openPhone()
	if u.phone.status != "Connected" || !c.running {
		t.Fatal("music command error changed shared phone status")
	}
	r.err = errors.New("radio")
	c.update(&u, now, 80)
	if c.running || u.phone.mode != phoneOff || u.phone.music.Link != music.LinkDisconnected || u.phone.musicNote != "" {
		t.Fatal("error retained connection")
	}
}

func TestAuthenticatedPhoneWithoutMusicSubscriptionStillReceivesTimeAndWeather(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	u := phoneUI(now, phoneConnected)
	a, b := weatherPackets(now)
	r := &fakePhoneRadio{fakeWeatherRadio: fakeWeatherRadio{packets: [][]byte{a, b}}}
	r.report(music.LinkAuthenticated, false, 1)
	r.value[82] = 0
	r.fakeTimeRadio.value, _ = timesync.Encode(now.Add(time.Hour))
	r.size = 10
	c := timeSyncController{radio: r}
	c.update(&u, now, 80)
	if u.phone.status != "Connected" || !c.running || !u.weather.cache.HasCurrent || !u.weather.cache.HasForecast {
		t.Fatal("shared connection depends on music subscription")
	}
	if !u.clock.Now(now).Equal(now.Add(time.Hour)) || u.phone.musicStatus() != "Open music in app" {
		t.Fatal("time or music-specific availability incorrect")
	}
	u.handle(inputEvent{Kind: inputTap, X: 190, Y: 150}, now, firmwareConfirmed, powerStatus{})
	c.update(&u, now, 80)
	if len(r.commands) != 0 || u.phone.queued {
		t.Fatal("sent a media command without subscription")
	}
}

func TestSecuringPhoneDoesNotClaimConnectedOrEnableMusic(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneConnected)
	r := &fakePhoneRadio{}
	r.report(music.LinkSecuring, true, 1)
	c := timeSyncController{radio: r}
	c.update(&u, now, 80)
	u.handle(inputEvent{Kind: inputTap, X: 190, Y: 150}, now, firmwareConfirmed, powerStatus{})
	c.update(&u, now, 80)
	if u.phone.status != "Securing connection" || u.phone.queued || len(r.commands) != 0 || u.phone.music.Known {
		t.Fatal("unsecured connection appears ready")
	}
}

func TestMusicUnsubscribePreservesPhoneAndDropsPendingCommand(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneConnected)
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r}
	r.report(music.LinkMusicReady, true, 1)
	c.update(&u, now, 80)
	u.handle(inputEvent{Kind: inputTap, X: 190, Y: 150}, now, firmwareConfirmed, powerStatus{})
	r.report(music.LinkAuthenticated, true, 1)
	c.update(&u, now, 80)
	if !c.running || u.phone.status != "Connected" || u.phone.queued || u.phone.announced || len(r.commands) != 1 {
		t.Fatal("unsubscribe disconnected phone or replayed queued music command")
	}
	r.report(music.LinkMusicReady, true, 1)
	c.update(&u, now, 80)
	if len(r.commands) != 2 || r.commands[1] != music.Open || r.starts != 1 || r.stops != 0 {
		t.Fatal("resubscribe did not refresh over the same phone connection")
	}
}

func TestMusicRefreshFailureStaysLocalAndDoesNotRetryContinuously(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneConnected)
	r := &fakePhoneRadio{commandError: errors.New("queue full")}
	r.report(music.LinkMusicReady, false, 1)
	c := timeSyncController{radio: r}
	c.update(&u, now, 80)
	if u.phone.status != "Connected" || u.phone.musicStatus() != "Refresh failed" || !u.phone.announced {
		t.Fatal("refresh failure changed shared connection status")
	}
	r.commandError = nil
	c.update(&u, now.Add(time.Second), 80)
	if len(r.commands) != 0 {
		t.Fatal("automatically retried failed refresh")
	}
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 85}, now, firmwareConfirmed, powerStatus{})
	c.update(&u, now, 80)
	if len(r.commands) != 1 || r.commands[0] != music.Open || u.phone.musicNote != "" || u.phone.status != "Connected" {
		t.Fatal("manual retry did not clear local error")
	}
}
func TestPhoneSwitchWaitsForDrainAndWeatherSharesConnection(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneSession)
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r}
	c.update(&u, now, 73)
	u.phone.mode = phoneConnected
	r.draining = true
	c.update(&u, now, 73)
	if r.starts != 1 || c.running || r.stops != 1 {
		t.Fatal("started over asynchronous teardown")
	}
	r.draining = false
	c.update(&u, now, 73)
	if r.starts != 2 || !c.running {
		t.Fatal("did not resume after teardown")
	}
	u.openWeather()
	u.handle(inputEvent{Kind: inputTap, X: 180, Y: 210}, now, firmwareConfirmed, powerStatus{})
	c.update(&u, now, 73)
	if u.phone.mode != phoneConnected || c.weatherMode || r.starts != 2 || r.stops != 1 || u.page != pageWeather {
		t.Fatal("weather restarted shared connection")
	}
}
func TestPhoneSessionExpiresInSleepingProductionLoop(t *testing.T) {
	d := newScriptDisplay()
	u := phoneUI(d.now, phoneSession)
	r := &fakePhoneRadio{}
	d.steps = []scriptStep{
		{at: time.Second, event: inputEvent{Kind: inputSleep}},
		{at: phoneSessionDuration + time.Second, event: inputEvent{Kind: inputQuit}, check: func() {
			if u.phone.mode != phoneOff || r.stops != 1 || !d.asleep || d.wakes != 0 {
				t.Fatal("session leaked or woke screen")
			}
		}},
	}
	if err := runScript(t, d, &u, r); err != nil {
		t.Fatal(err)
	}
	if d.waits > 5 {
		t.Fatal("phone session polled while asleep", d.waits)
	}
	if d.powerReads != 1 {
		t.Fatal("phone battery reporting added asleep ADC samples", d.powerReads)
	}
}
func TestPhoneStripPixelsAndFrameKeys(t *testing.T) {
	now := time.Now()
	for _, pg := range []page{pageMusic, pagePhone} {
		u := phoneUI(now, phoneConnected)
		u.phone = samplePhone(now)
		u.page = pg
		d, ref := &memoryDisplay{}, &memoryDisplay{}
		draw := func(c canvas) { u.drawFrame(c, now, powerStatus{Percent: 73}) }
		ref.FillScreen(black)
		draw(ref)
		var r frameRenderer
		if err := r.render(d, draw); err != nil || d.pixels != ref.pixels {
			t.Fatal("strip mismatch", pg, err)
		}
		before := u.frameKey(now, powerStatus{})
		u.phone.revision++
		if u.frameKey(now, powerStatus{}) == before {
			t.Fatal("metadata did not repaint")
		}
	}
}

func TestRepeatedConnectDoesNotRestartSharedRadio(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneOff)
	u.openPhone()
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r}
	for i := 0; i < 3; i++ {
		u.handle(inputEvent{Kind: inputTap, X: 120, Y: 110}, now.Add(time.Duration(i)*time.Minute), firmwareConfirmed, powerStatus{})
		if u.page == pageBatteryConfirm {
			u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now.Add(time.Duration(i)*time.Minute), firmwareConfirmed, powerStatus{})
		}
		c.update(&u, now.Add(time.Duration(i)*time.Minute), 73)
	}
	if r.starts != 1 || r.stops != 0 || r.window != 0 || !u.phoneAuto {
		t.Fatal("repeated Connect reset shared connection")
	}
}

func TestBluetoothControlsKeepNavigationSeparateFromConnection(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneOff)
	u.openPhone()
	before := *u.phone
	// The old header action and the disabled off button must be inert. The
	// gaps between the new rows must not activate neighboring controls either.
	for _, tap := range []inputEvent{
		{Kind: inputTap, X: 200, Y: 25},
		{Kind: inputTap, X: 120, Y: 206},
		{Kind: inputTap, X: 120, Y: 134},
		{Kind: inputTap, X: 120, Y: 182},
	} {
		u.handle(tap, now, firmwareConfirmed, powerStatus{})
		if u.page != pagePhone || u.phoneAuto || *u.phone != before {
			t.Fatal("inactive Bluetooth control changed connection state", tap)
		}
	}
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 158}, now, firmwareConfirmed, powerStatus{})
	if u.page != pagePairingSettings || u.phoneAuto || *u.phone != before {
		t.Fatal("Paired devices changed connection state")
	}
	u.handle(inputEvent{Kind: inputSwipeRight}, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 110}, now, firmwareConfirmed, powerStatus{})
	if u.page == pageBatteryConfirm {
		u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
	}
	if u.page != pagePhone || !u.phoneAuto || u.phone.mode != phoneConnected ||
		!u.phone.advertiseRequested || u.phone.setupUntil != now.Add(phoneSetupDuration) {
		t.Fatal("Turn on did not open the existing persistent pairing window")
	}
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 206}, now, firmwareConfirmed, powerStatus{})
	if u.phoneAuto || u.phone.mode != phoneOff || !u.phone.setupUntil.IsZero() {
		t.Fatal("Turn off did not disable the connection preference")
	}
}

func TestPhoneSetupRefreshesExistingAdvertisingWithoutRestart(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneConnected)
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r}
	c.update(&u, now, 80)
	u.openPhone()
	c.update(&u, now.Add(time.Second), 80)
	if r.setups != 1 || r.starts != 1 || r.stops != 0 {
		t.Fatal("opening Phone did not renew advertising in place", r)
	}
	u.handlePhone(inputEvent{Kind: inputTap, X: 120, Y: 110}, now.Add(2*time.Second), firmwareConfirmed)
	c.update(&u, now.Add(2*time.Second), 80)
	if r.setups != 2 || r.starts != 1 || r.stops != 0 {
		t.Fatal("CONNECT PHONE restarted the security exchange", r)
	}
	c.update(&u, now.Add(3*time.Second), 80)
	if r.setups != 2 {
		t.Fatal("setup refresh repeated every tick")
	}
}
