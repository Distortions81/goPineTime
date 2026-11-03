package main

import (
	"testing"
	"time"
)

func TestQuickControlsConfirmOnlyPowerIncreasesAndReturnToOrigin(t *testing.T) {
	now := time.Unix(100, 0)
	u := newWatchUI(firmwareConfirmed)
	u.handle(inputEvent{Kind: inputSwipeDown}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageQuickControls {
		t.Fatal("swipe down did not open controls")
	}
	tap := func(y int16) {
		u.handle(inputEvent{Kind: inputTap, X: 120, Y: y}, now, firmwareConfirmed, powerStatus{})
	}
	tap(59) // High -> Low applies immediately.
	if u.brightness != brightnessLow || u.page != pageQuickControls {
		t.Fatal("lowering brightness opened a warning")
	}
	tap(59) // Low -> Medium waits for confirmation.
	if u.page != pageBatteryConfirm || u.brightness != brightnessLow {
		t.Fatal("raising brightness did not wait")
	}
	u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageQuickControls || u.brightness != brightnessMedium {
		t.Fatal("confirmation lost controls origin")
	}
	u.touchWake = true
	tap(97)
	if u.touchWake || u.page != pageQuickControls {
		t.Fatal("disabling touch wake opened a warning")
	}
	tap(135)
	if u.quiet&quietManual == 0 || u.notifications != nil || u.page != pageQuickControls {
		t.Fatal("quiet failed to apply immediately without allocating inbox")
	}
	tap(135)
	if u.page != pageBatteryConfirm || u.quiet&quietManual == 0 {
		t.Fatal("buzz enabled before confirmation")
	}
	u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageQuickControls || u.quiet&quietManual != 0 || u.notifications != nil {
		t.Fatal("buzz confirmation lost origin or allocated inbox")
	}
	tap(97)
	u.handle(inputEvent{Kind: inputSleep}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageClock || u.touchWake {
		t.Fatal("sleep retained or applied quick-controls proposal")
	}
}

func TestNewPagesRespectOverlaysAndTrial(t *testing.T) {
	now := time.Unix(100, 0)
	for _, p := range []page{pageClock, pageQuickControls, pageQuietHours, pageBatteryHistory} {
		u := newWatchUI(firmwareConfirmed)
		u.page, u.powerNotice = p, true
		u.handle(inputEvent{Kind: inputTap, X: 210, Y: 15}, now, firmwareConfirmed, powerStatus{})
		if u.page != p || u.powerNotice {
			t.Fatal("power overlay let tap navigate underlying page", p)
		}
		u.pairing = &pairingState{code: 123456}
		u.handle(inputEvent{Kind: inputSwipeDown}, now, firmwareConfirmed, powerStatus{})
		if u.page != p {
			t.Fatal("pairing overlay let swipe navigate underlying page", p)
		}
	}
	u := newWatchUI(firmwareTrial)
	u.handle(inputEvent{Kind: inputSwipeDown}, now, firmwareTrial, powerStatus{})
	if u.page != pageTrial {
		t.Fatal("controls bypassed trial confirmation")
	}
}

func TestBatteryHistoryNavigation(t *testing.T) {
	for _, p := range []page{pageClock, pageQuickControls} {
		u := newWatchUI(firmwareConfirmed)
		u.page = p
		u.handle(inputEvent{Kind: inputTap, X: 218, Y: 12}, time.Time{}, firmwareConfirmed, powerStatus{})
		if u.page != pageBatteryHistory || u.batteryBack != p {
			t.Fatal("battery status tap lost origin", p)
		}
		u.handle(inputEvent{Kind: inputSwipeRight}, time.Time{}, firmwareConfirmed, powerStatus{})
		if u.page != p {
			t.Fatal("battery back lost origin", p)
		}
	}
}

func TestClockControlHintAndQuietHoursNavigation(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	now := time.Unix(100, 0)
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 24}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageQuickControls {
		t.Fatal("clock controls cue is not tappable")
	}
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 210}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageQuietHours {
		t.Fatal("quiet-hours control did not navigate")
	}
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 62}, now, firmwareConfirmed, powerStatus{})
	if u.quiet&quietScheduled == 0 || u.quietStart() != 22 || u.quietEnd() != 7 {
		t.Fatal("first schedule opt-in did not initialize overnight defaults")
	}
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 108}, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 154}, now, firmwareConfirmed, powerStatus{})
	if u.quietStart() != 23 || u.quietEnd() != 8 {
		t.Fatal("quiet-hour controls did not advance independently")
	}
	u.handle(inputEvent{Kind: inputTap, X: 25, Y: 24}, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputSwipeUp}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageClock {
		t.Fatal("controls did not close back to clock")
	}
}
