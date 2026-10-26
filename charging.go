package main

import (
	"github.com/Distortions81/goPine/internal/gfx"
	"github.com/Distortions81/goPine/internal/uifont"
)

// A notice overlays the current page without cancelling an editor, phone
// session or firmware transfer. Critical screens always retain priority.
func (u *watchUI) powerNoticeVisible() bool {
	return u.powerNotice && !u.timers.active && u.page != pageAlert &&
		u.page != pageTrial && u.page != pageTransfer && u.page != pageTimeSync &&
		(u.weather == nil || !u.weather.open)
}

func (u *watchUI) showPowerNotice() {
	u.cancelHold()
	u.powerNotice = true
	if !u.powerNoticeVisible() {
		u.powerNotice = false
	}
}

func (u *watchUI) handlePowerNotice(e inputEvent) bool {
	if !u.powerNotice {
		return false
	}
	if !u.powerNoticeVisible() || e.Kind == inputSleep {
		u.powerNotice = false
		return false // Preserve the underlying page's normal sleep behavior.
	}
	if e.Kind == inputRefresh {
		return false // Service underlying prompt/session expiry normally.
	}
	switch e.Kind {
	case inputTap, inputSwipeLeft, inputSwipeRight:
		u.powerNotice = false
	}
	// Consume the entire gesture, including its release, so dismissing the
	// notice cannot also press a control on the underlying page.
	return true
}

func drawCharging(d canvas, p powerStatus) {
	title, detail := "CHARGING", "Power connected"
	switch p.State {
	case chargeDischarging:
		title, detail = "UNPLUGGED", "Running on battery"
	case chargeExternalPower:
		title, detail = "CHARGING STOPPED", "Power still connected"
		if p.Percent >= 100 {
			title, detail = "CHARGED", "Power connected"
		}
	}
	c := powerColor(p)
	centered(d, &uifont.Regular18, 31, title, c)
	// Large gauge, with the percentage below it to keep 100% legible.
	gfx.Box(d, 53, 51, 128, 40, 4, c)
	gfx.FillBox(d, 181, 61, 7, 20, c)
	fill := int16(min(p.Percent, 100)) * 112 / 100
	if fill > 0 {
		gfx.FillBox(d, 61, 59, fill, 24, c)
	}
	percent, start := percentDigits(p.Percent)
	digits := string(percent[start:3])
	w, _ := lineWidth(&uifont.Clock, digits)
	suffix, _ := lineWidth(&uifont.Regular18, "%")
	x := int16((240 - w - suffix - 4) / 2)
	writeLine(d, &uifont.Clock, x, 160, digits, white)
	writeLine(d, &uifont.Regular18, x+int16(w)+4, 160, "%", white)
	centered(d, &uifont.Regular18, 192, detail, muted)
	centered(d, &uifont.Regular18, 228, "Tap to return", muted)
}
