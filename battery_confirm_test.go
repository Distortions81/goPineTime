package main

import (
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

func TestBatteryConfirmationDefersChangesAndCancelsOnSleep(t *testing.T) {
	now := time.Unix(0, 0)
	for _, choice := range []batteryChoice{batteryTouch, batteryBluetooth, batteryBuzz, batteryLow, batteryMedium, batteryHigh} {
		for _, cancel := range []inputEvent{{Kind: inputTap, X: 60, Y: 210}, {Kind: inputSwipeRight}, {Kind: inputSleep}, {Kind: inputCancel}} {
			u := newWatchUI(firmwareConfirmed)
			u.openPhone()
			u.openInbox()
			u.notifications.quiet = true
			before := u.settings()
			u.askBattery(choice)
			u.handle(inputEvent{Kind: inputTap, X: 120, Y: 210}, now, firmwareConfirmed, powerStatus{}) // Inert button gap.
			if u.page != pageBatteryConfirm || u.settings() != before || !u.notifications.quiet || u.phone.mode != phoneOff {
				t.Fatal("proposal changed settings or started Bluetooth", choice)
			}
			u.handle(cancel, now, firmwareConfirmed, powerStatus{})
			if u.page == pageBatteryConfirm || u.batteryChoice != batteryNone || u.settings() != before || !u.notifications.quiet || u.phone.mode != phoneOff {
				t.Fatal("cancel applied the proposal", choice, cancel)
			}
			u.askBattery(choice)
			u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
			switch choice {
			case batteryTouch:
				if !u.touchWake {
					t.Fatal("touch confirmation lost")
				}
			case batteryBluetooth:
				if !u.phoneAuto || u.phone.mode != phoneConnected {
					t.Fatal("Bluetooth confirmation lost")
				}
			case batteryBuzz:
				if u.notifications.quiet {
					t.Fatal("buzz confirmation lost")
				}
			default:
				if u.brightness != uint8(choice-batteryHigh) {
					t.Fatal("brightness confirmation lost")
				}
			}
		}
	}
}

func TestNotificationBuzzEnableRequiresConfirmation(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	u.openInbox()
	now := time.Unix(0, 0)
	tap := inputEvent{Kind: inputTap, X: 60, Y: 58}
	u.handle(tap, now, firmwareConfirmed, powerStatus{})
	if !u.notifications.quiet || u.page != pageInbox {
		t.Fatal("quiet must apply immediately")
	}
	u.handle(tap, now, firmwareConfirmed, powerStatus{})
	if !u.notifications.quiet || u.page != pageBatteryConfirm {
		t.Fatal("buzz bypassed confirmation")
	}
}

func TestBatteryConfirmationCannotApplyBehindAnOverlay(t *testing.T) {
	for _, pairing := range []bool{false, true} {
		u := newWatchUI(firmwareConfirmed)
		u.askBattery(batteryTouch)
		u.powerNotice = !pairing
		if pairing {
			u.pairing = &pairingState{code: 123456}
		}
		u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, time.Time{}, firmwareConfirmed, powerStatus{})
		if u.touchWake {
			t.Fatal("hidden confirmation applied behind overlay", pairing)
		}
	}
}

func TestBatteryCopyFitsAndRenderingDoesNotAllocate(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	d := &benchmarkDisplay{}
	var renderer frameRenderer
	for choice := batteryTouch; choice <= batteryMedium; choice++ {
		u.askBattery(choice)
		title, lines := batteryCopy(choice)
		if width, _ := lineWidth(&uifont.Bold18, title); width > 216 {
			t.Fatal("title too wide", title, width)
		}
		for _, line := range lines {
			if width, _ := lineWidth(&uifont.Regular18, line); width > 216 {
				t.Fatal("line too wide", line, width)
			}
		}
		draw := func(c canvas) { u.drawFrame(c, time.Time{}, powerStatus{}) }
		if err := renderer.render(d, draw); err != nil {
			t.Fatal(err)
		}
		if n := testing.AllocsPerRun(20, func() { _ = renderer.render(d, draw) }); n != 0 {
			t.Fatal("confirmation draw allocates", n)
		}
	}
}
