package main

import (
	"testing"
	"time"

	"tinygo.org/x/drivers/pixel"
)

// Compare the actual packed-strip path against full-frame compositing. This
// catches palette resolution after antialiasing and stale strips on a toggle.
func TestThemeToggleMatchesFullFrame(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 34, 0, 0, time.UTC)
	for _, p := range []page{pageClock, pageSettings, pageDisplaySettings, pageBatteryConfirm, pageApps, pagePhone, pageMusic, pagePairingSettings, pageStopwatch, pageCountdown, pageUpdate, pageTrial, pageInbox, pageNotification, pageTransfer} {
		u := newWatchUI(firmwareConfirmed)
		u.page, u.phone, u.notifications = p, samplePhone(now), sampleNotifications()
		u.batteryChoice = batteryTouch
		u.pairing = &pairingState{status: 2, count: 2}
		u.transfer = &transferState{phase: updateReceiving, percent: 58}
		d := &memoryDisplay{}
		var r frameRenderer
		power := powerStatus{Percent: 68, State: chargeCharging}
		draw := func(c canvas) { u.drawFrame(c, now, power) }
		before := u.frameKey(now, power)
		for _, light := range []bool{false, true, false} {
			u.lightMode = light
			next := u.frameKey(now, power)
			if light != before.lightMode && changedStrips(&before, &next) != allStrips {
				t.Fatal("theme change did not repaint the full screen", p)
			}
			if err := r.renderStrips(d, draw, changedStrips(&before, &next)); err != nil {
				t.Fatal(err)
			}
			ref := &memoryDisplay{}
			draw(ref)
			if d.pixels != ref.pixels {
				t.Fatalf("page %d light=%v differs from full frame", p, light)
			}
			bg := themeColor(light, black)
			if d.pixels[0] != pixel.NewColor[pixel.RGB444BE](bg.R, bg.G, bg.B).RGBA() {
				t.Fatal("background did not follow theme", p, light)
			}
			before = next
		}
	}
}

func TestThemeAppliesToOverlays(t *testing.T) {
	now := time.Unix(0, 0)
	for _, pairing := range []bool{false, true} {
		u := newWatchUI(firmwareConfirmed)
		u.powerNotice = !pairing
		if pairing {
			u.pairing = &pairingState{code: 123456}
		}
		before := u.frameKey(now, powerStatus{})
		u.lightMode = true
		after := u.frameKey(now, powerStatus{})
		if changedStrips(&before, &after) != allStrips || !after.lightMode {
			t.Fatal("overlay omitted theme from its frame identity")
		}
		d, ref := &memoryDisplay{}, &memoryDisplay{}
		draw := func(c canvas) { u.drawFrame(c, now, powerStatus{Percent: 68, State: chargeCharging}) }
		var r frameRenderer
		if err := r.render(d, draw); err != nil {
			t.Fatal(err)
		}
		draw(ref)
		if d.pixels != ref.pixels {
			t.Fatal("overlay theme differs between strip and full frame")
		}
	}
}
