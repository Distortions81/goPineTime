package main

import (
	"testing"
	"time"
)

type pairingTestRadio struct {
	fakeTimeRadio
	code             uint32
	status           int
	busy             bool
	forgot           int
	diagnostic       uint32
	progress         uint16
	count, cancelled int
}

func (r *pairingTestRadio) PairingCode() uint32        { return r.code }
func (r *pairingTestRadio) PairingDiagnostics() uint32 { return r.diagnostic }
func (r *pairingTestRadio) PairingProgress() uint16    { return r.progress }
func (r *pairingTestRadio) BondStatus() int            { return r.status }
func (r *pairingTestRadio) BondCount() int             { return r.count }
func (r *pairingTestRadio) CancelPairing() error       { r.code = 0; r.cancelled++; return nil }
func (r *pairingTestRadio) Busy() bool                 { return r.busy }
func (r *pairingTestRadio) ForgetPhone() error         { r.forgot++; r.status = 0; r.count = 0; return nil }
func (r *pairingTestRadio) Stop()                      { r.code = 0; r.stops++ }
func TestPairingProgressRedrawsCodeAndInfo(t *testing.T) {
	r := &pairingTestRadio{code: 123457, progress: 0x200}
	c := timeSyncController{radio: r}
	u := newWatchUI(firmwareConfirmed)
	c.updatePairing(&u)
	before := u.frameKey(time.Now(), powerStatus{})
	r.progress = 0x280 // first confirm arrived; no verified rounds yet
	c.updatePairing(&u)
	if u.frameKey(time.Now(), powerStatus{}) == before || pairingStageText(u.pairing.progress) != "Checking code 0/20" {
		t.Fatal("first code-check message did not redraw the visible PIN")
	}
	r.code, r.diagnostic = 0, 0x2130404
	u.openPairingSettings()
	c.updatePairing(&u)
	before = u.frameKey(time.Now(), powerStatus{})
	r.progress = 0x205
	c.updatePairing(&u)
	if u.frameKey(time.Now(), powerStatus{}) == before || u.pairing.diagnostic != r.diagnostic || pairingStageText(u.pairing.progress) != "Checking code 5/20" {
		t.Fatal("INFO lost progress or the retained security error")
	}
}

func TestSavedDeviceCountRedrawsPairingInfo(t *testing.T) {
	r := &pairingTestRadio{status: 2, count: 1}
	c := timeSyncController{radio: r}
	u := newWatchUI(firmwareConfirmed)
	u.openPairingSettings()
	c.updatePairing(&u)
	before := u.frameKey(time.Now(), powerStatus{})
	r.count = 2
	c.updatePairing(&u)
	if u.pairing.count != 2 || before == u.frameKey(time.Now(), powerStatus{}) {
		t.Fatal("second saved device did not redraw pairing info")
	}
}

func TestPairingStageUsesNarrowRedrawWithoutLeavingOldPixels(t *testing.T) {
	now := time.Now()
	u := newWatchUI(firmwareConfirmed)
	u.pairing = &pairingState{code: 123457}
	d := &memoryDisplay{}
	var renderer frameRenderer
	draw := func(c canvas) { u.drawFrame(c, now, powerStatus{}) }
	if err := renderer.render(d, draw); err != nil {
		t.Fatal(err)
	}
	previous := u.frameKey(now, powerStatus{})
	for _, stage := range []uint16{0x200, 0x280, 0x201, 0x293, 0x314, 0x414, 0x514, 0x600} {
		u.pairing.progress = stage
		next := u.frameKey(now, powerStatus{})
		mask := changedStrips(&previous, &next)
		if mask == allStrips || mask == 0 {
			t.Fatal("stage update did not use narrow redraw", stage)
		}
		if err := renderer.renderStrips(d, draw, mask); err != nil {
			t.Fatal(err)
		}
		ref := &memoryDisplay{}
		var full frameRenderer
		if err := full.render(ref, draw); err != nil {
			t.Fatal(err)
		}
		if d.pixels != ref.pixels {
			t.Fatal("stage change left stale pixels", stage)
		}
		previous = next
	}
	u.pairing.code++
	next := u.frameKey(now, powerStatus{})
	if changedStrips(&previous, &next) != allStrips {
		t.Fatal("new PIN used a stage-only redraw")
	}
}
func TestPhoneInfoRetainsAndRedrawsPairingFailure(t *testing.T) {
	r := &pairingTestRadio{status: 0}
	c := timeSyncController{radio: r}
	u := newWatchUI(firmwareConfirmed)
	u.openPairingSettings()
	c.updatePairing(&u)
	before := u.frameKey(time.Now(), powerStatus{})
	r.diagnostic = 0x2080000
	c.updatePairing(&u)
	if u.frameKey(time.Now(), powerStatus{}) == before || pairingFailureText(u.pairing.diagnostic) != "Connection timed out" {
		t.Fatal("connection failure hidden behind unchanged Waiting for phone UI")
	}
	r.diagnostic = 0x2130404
	c.updatePairing(&u)
	if pairingFailureText(u.pairing.diagnostic) != "Code did not match" || pairingDiagnosticCodes(u.pairing.diagnostic) != "S 1028 / D 531" {
		t.Fatal("disconnect overwrote the more specific pairing error")
	}
	r.diagnostic = 0
	c.updatePairing(&u)
	if u.pairing.diagnostic != 0 {
		t.Fatal("successful pairing retained stale failure")
	}
}
func TestPairingCodeAndCancel(t *testing.T) {
	r := &pairingTestRadio{code: 1}
	c := timeSyncController{radio: r, running: true}
	u := newWatchUI(firmwareConfirmed)
	u.openWeather()
	u.weather.open = true
	u.page = pageWeatherSync
	u.phoneAuto = true
	if !c.updatePairing(&u) || !u.pairingVisible() {
		t.Fatal("000000 must display and wake")
	}
	if c.updatePairing(&u) {
		t.Fatal("same code repeatedly wakes watch")
	}
	key := u.frameKey(time.Now(), powerStatus{})
	if key.page != pagePairing || key.step != 1 {
		t.Fatal("pairing redraw identity")
	}
	u.handle(inputEvent{Kind: inputTap, X: 100, Y: 200}, time.Now(), firmwareConfirmed, powerStatus{})
	c.updatePairing(&u)
	if u.pairingVisible() || !c.running || !u.weather.open || !u.phoneAuto || r.stops != 0 || r.cancelled != 1 {
		t.Fatal("cancel must only end the pending pairing and preserve other features")
	}
}
func TestForgetWaitsForDisconnectAndRequiresConfirmation(t *testing.T) {
	r := &pairingTestRadio{busy: true, status: 2}
	c := timeSyncController{radio: r, running: true}
	u := newWatchUI(firmwareConfirmed)
	u.openMusic()
	u.phone.mode = phoneConnected
	u.openPairingSettings()
	c.updatePairing(&u)
	tap := inputEvent{Kind: inputTap, X: 100, Y: 190}
	u.handlePairingSettings(tap)
	c.updatePairing(&u)
	if r.forgot != 0 || !u.pairing.confirm {
		t.Fatal("first tap forgot phone")
	}
	u.handlePairingSettings(tap)
	c.updatePairing(&u)
	if r.forgot != 0 || !u.pairing.forget || u.phone.mode != phoneOff {
		t.Fatal("forgot before disconnect")
	}
	r.busy = false
	c.updatePairing(&u)
	c.updatePairing(&u)
	if r.forgot != 1 || u.pairing.forget || u.pairing.status != 0 {
		t.Fatal("forget did not finish exactly once")
	}
}

func TestPairingInfoCannotForgetUnknownMissingOrUnsavedBond(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{{"unknown", -2}, {"missing", 0}, {"saving", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			r := &pairingTestRadio{busy: true, status: tc.status}
			c := timeSyncController{radio: r, running: true}
			u := newWatchUI(firmwareConfirmed)
			u.phoneAuto = true
			u.openPhone()
			u.phone.mode = phoneConnected
			u.openPairingSettings()
			c.updatePairing(&u)
			for i := 0; i < 3; i++ {
				u.handlePairingSettings(inputEvent{Kind: inputTap, X: 100, Y: 190})
				c.updatePairing(&u)
			}
			if r.forgot != 0 || r.stops != 0 || !c.running || !u.phoneAuto || u.pairing.confirm || u.pairing.forget {
				t.Fatal("nonexistent or unknown bond exposed a destructive action")
			}
		})
	}
}

func TestForgetRechecksBondBeforeStoppingSharedConnection(t *testing.T) {
	r := &pairingTestRadio{status: 2}
	c := timeSyncController{radio: r, running: true}
	u := newWatchUI(firmwareConfirmed)
	u.phoneAuto = true
	u.openPhone()
	u.phone.mode = phoneConnected
	u.openPairingSettings()
	c.updatePairing(&u)
	tap := inputEvent{Kind: inputTap, X: 100, Y: 190}
	u.handlePairingSettings(tap)
	u.handlePairingSettings(tap)
	// A status change after the displayed confirmation must be rechecked at the
	// mutation boundary, before disabling the shared connection or erasing flash.
	r.status = 0
	c.updatePairing(&u)
	if r.forgot != 0 || r.stops != 0 || !c.running || !u.phoneAuto || u.pairing.confirm || u.pairing.forget {
		t.Fatal("stale confirmation erased a nonexistent bond")
	}
}

func TestUnusablePairingAllowsOnlyConfirmedReset(t *testing.T) {
	for _, status := range []int{-1, -3} {
		r := &pairingTestRadio{busy: true, status: status}
		c := timeSyncController{radio: r, running: true}
		u := newWatchUI(firmwareConfirmed)
		u.phoneAuto = true
		u.openPhone()
		u.phone.mode = phoneConnected
		u.openPairingSettings()
		c.updatePairing(&u)
		tap := inputEvent{Kind: inputTap, X: 100, Y: 190}
		u.handlePairingSettings(tap)
		c.updatePairing(&u)
		if !u.pairing.confirm || r.stops != 0 || r.forgot != 0 {
			t.Fatal("storage recovery skipped confirmation")
		}
		u.handlePairingSettings(tap)
		c.updatePairing(&u)
		if !u.pairing.forget || r.forgot != 0 || c.running || u.phoneAuto {
			t.Fatal("storage recovery did not wait for disconnect")
		}
		r.busy = false
		c.updatePairing(&u)
		if r.forgot != 1 || u.pairing.status != 0 || u.pairing.note != "Forget on devices too" {
			t.Fatal("storage recovery did not clear journal or explain phone-side cleanup")
		}
	}
}
