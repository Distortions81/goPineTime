package main

import (
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

func (u *watchUI) cycleBrightness() {
	next := (validBrightness(u.brightness) + 1) % 3
	if backlightChannels(next, true) > backlightChannels(u.brightness, true) {
		u.askBattery(batteryHigh + batteryChoice(next))
	} else {
		u.brightness = next
	}
}

func (u *watchUI) toggleTouchWake() {
	if u.touchWake {
		u.touchWake = false
	} else {
		u.askBattery(batteryTouch)
	}
}

func (u *watchUI) handleQuickControls(e inputEvent) {
	if e.Kind == inputSwipeUp || e.Kind == inputSwipeRight || e.Kind == inputSleep || backButtonTapped(e) {
		u.page = pageClock
		return
	}
	if e.Kind != inputTap {
		return
	}
	switch {
	case inRect(e, 184, 0, 240, 36):
		u.batteryBack, u.page = pageQuickControls, pageBatteryHistory
	case inRect(e, 12, 42, 228, 76):
		u.cycleBrightness()
	case inRect(e, 12, 80, 228, 114):
		u.toggleTouchWake()
	case inRect(e, 12, 118, 228, 152):
		u.toggleBuzz()
	case inRect(e, 12, 156, 228, 190):
		u.openPhone()
	case inRect(e, 12, 194, 228, 228):
		u.page = pageQuietHours
	}
}

func (u *watchUI) drawQuickControls(d canvas, now time.Time) {
	centered(d, &uifont.Regular18, 29, "CONTROLS", white)
	clockControl(d, 12, 42, 216, 34, brightnessLabel(u.brightness), card)
	wake := "Touch wake: Off"
	if u.touchWake {
		wake = "Touch wake: On"
	}
	clockControl(d, 12, 80, 216, 34, wake, card)
	buzz := "Buzz: On"
	if u.quiet&quietManual != 0 {
		buzz = "Buzz: Quiet"
	} else if u.scheduledQuiet(now) {
		buzz = "Buzz: Quiet hours"
	}
	clockControl(d, 12, 118, 216, 34, buzz, card)
	phone := "Bluetooth: Off"
	if u.phoneAuto || (u.phone != nil && u.phone.mode != phoneOff) {
		phone = "Bluetooth: On"
	}
	clockControl(d, 12, 156, 216, 34, phone, card)
	clockControl(d, 12, 194, 216, 34, "Quiet hours", card)
}
