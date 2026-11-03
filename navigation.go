package main

import (
	"github.com/Distortions81/goPine/internal/gfx"
	"github.com/Distortions81/goPine/internal/uifont"
)

// Every navigable screen shares this icon and the same 60x44 touch target.
// Page handlers retain their own back/cancel behavior and cleanup.
func backButtonTapped(e inputEvent) bool {
	return e.Kind == inputTap && inRect(e, 0, 0, 60, 44)
}

func (u *watchUI) backButtonVisible() bool {
	return u.pairingVisible() || u.powerNoticeVisible() ||
		(u.page != pageClock && u.page != pageTrial && u.page != pageAlert)
}

func drawBackButton(d canvas) {
	gfx.Line(d, 25, 16, 16, 24, accent)
	gfx.Line(d, 16, 24, 25, 32, accent)
}

// Static, muted hints above the clock leave the graph/message area free and
// make the two opposite swipe destinations visible without an overlay.
func drawClockMenuHints(d canvas) {
	// Tap or swipe down for controls. Keep the cue between branding and the
	// battery status so the clock and graph areas stay available.
	gfx.Line(d, 114, 22, 120, 28, muted)
	gfx.Line(d, 120, 28, 126, 22, muted)
	writeLine(d, &uifont.Regular18, 16, 61, "Apps", muted)
	appsWidth, _ := lineWidth(&uifont.Regular18, "Apps")
	x := 16 + int16(appsWidth) + 9
	gfx.Line(d, x, 51, x+5, 56, muted)
	gfx.Line(d, x+5, 56, x, 61, muted)
	settingsWidth, _ := lineWidth(&uifont.Regular18, "Settings")
	x = 224 - int16(settingsWidth)
	writeLine(d, &uifont.Regular18, x, 61, "Settings", muted)
	gfx.Line(d, x-7, 51, x-12, 56, muted)
	gfx.Line(d, x-12, 56, x-7, 61, muted)
}
