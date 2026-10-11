package main

import "github.com/Distortions81/goPine/internal/uifont"

// Rotate decoded touch gestures to match the LCD's hardware orientation.
func orientInput(e inputEvent, flipped bool) inputEvent {
	if !flipped {
		return e
	}
	switch e.Kind {
	case inputTap, inputPress, inputHold, inputRelease:
		e.X, e.Y = 239-e.X, 239-e.Y
	case inputSwipeLeft:
		e.Kind = inputSwipeRight
		e.X, e.Y = 239-e.X, 239-e.Y
	case inputSwipeRight:
		e.Kind = inputSwipeLeft
		e.X, e.Y = 239-e.X, 239-e.Y
	}
	return e
}
func (u *watchUI) handleDisplaySettings(e inputEvent) {
	if e.Kind == inputSwipeRight || (e.Kind == inputTap && inRect(e, 0, 0, 60, 44)) {
		u.page = pageSettings
		return
	}
	if e.Kind != inputTap {
		return
	}
	switch {
	case inRect(e, 16, 42, 224, 82):
		u.lightMode = !u.lightMode
	case inRect(e, 16, 88, 224, 128):
		u.askBattery(batteryHigh + batteryChoice((validBrightness(u.brightness)+1)%3))
	case inRect(e, 16, 134, 224, 174):
		if u.touchWake {
			u.touchWake = false
		} else {
			u.askBattery(batteryTouch)
		}
	case inRect(e, 16, 180, 224, 220):
		u.flipScreen = !u.flipScreen
	}
}
func (u *watchUI) drawDisplaySettings(d canvas) {
	centered(d, &uifont.Bold18, 29, "Display", white)
	theme, wake, flip := "Theme: Dark", "Touch wake: Off", "Rotation: Normal"
	if u.lightMode {
		theme = "Theme: Light"
	}
	if u.touchWake {
		wake = "Touch wake: On"
	}
	if u.flipScreen {
		flip = "Rotation: Flipped"
	}
	clockControl(d, 16, 42, 208, 40, theme, card)
	clockControl(d, 16, 88, 208, 40, brightnessLabel(u.brightness), card)
	clockControl(d, 16, 134, 208, 40, wake, card)
	clockControl(d, 16, 180, 208, 40, flip, card)
}
