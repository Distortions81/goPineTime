package main

import (
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

// Only the proposed choice is stored. Settings and hardware remain unchanged
// until confirmation; leaving or sleeping discards the proposal.
type batteryChoice uint8

const (
	batteryNone batteryChoice = iota
	batteryTouch
	batteryBluetooth
	batteryBuzz
	batteryHigh // Same contiguous order as brightnessHigh/Low/Medium.
	batteryLow
	batteryMedium
)

func (u *watchUI) askBattery(choice batteryChoice) {
	u.cancelHold()
	u.batteryChoice, u.page = choice, pageBatteryConfirm
}

func (u *watchUI) handleBatteryConfirm(e inputEvent, now time.Time) {
	confirm := e.Kind == inputTap && inRect(e, 126, 188, 228, 232)
	cancel := e.Kind == inputSleep || e.Kind == inputCancel || e.Kind == inputSwipeRight ||
		(e.Kind == inputTap && inRect(e, 12, 188, 114, 232))
	if !confirm && !cancel {
		return
	}
	choice := u.batteryChoice
	u.batteryChoice, u.page = batteryNone, pageDisplaySettings
	switch choice {
	case batteryBluetooth:
		u.page = pagePhone
		if confirm && u.phone != nil {
			u.enablePhone(now)
		}
	case batteryBuzz:
		u.page = pageInbox
		if confirm && u.notifications != nil {
			u.notifications.quiet = false
		}
	case batteryTouch:
		if confirm {
			u.touchWake = true
		}
	case batteryHigh, batteryLow, batteryMedium:
		if confirm {
			u.brightness = uint8(choice - batteryHigh)
		}
	}
}

func batteryCopy(choice batteryChoice) (title string, lines [4]string) {
	switch choice {
	case batteryTouch:
		return "Touch wake", [4]string{"Touch stays active", "while the screen sleeps.", "Accidental wakes use", "extra backlight power."}
	case batteryBluetooth:
		return "Bluetooth", [4]string{"Stays on during sleep.", "Connections and traffic", "use extra battery.", "Includes notifications."}
	case batteryBuzz:
		return "Notification buzz", [4]string{"Each alert uses the", "vibration motor.", "Quiet still receives", "Bluetooth messages."}
	case batteryHigh:
		return "High backlight", [4]string{"Brightest setting.", "Uses the most backlight", "power while awake.", "Turns off during sleep."}
	case batteryMedium:
		return "Medium backlight", [4]string{"Between Low and High.", "Uses less backlight", "power than High.", "Turns off during sleep."}
	case batteryLow:
		return "Low backlight", [4]string{"Dimmest setting.", "Uses the least backlight", "power while awake.", "Turns off during sleep."}
	}
	return "Battery use", [4]string{}
}

func (u *watchUI) drawBatteryConfirm(d canvas) {
	title, lines := batteryCopy(u.batteryChoice)
	centered(d, &uifont.Regular18, 29, title, white)
	centered(d, &uifont.Regular18, 57, "Battery use", accent)
	for i, line := range lines {
		centered(d, &uifont.Regular18, int16(89+i*23), line, muted)
	}
	clockControl(d, 12, 188, 102, 44, "Cancel", card)
	clockControl(d, 126, 188, 102, 44, "Confirm", positive)
}
