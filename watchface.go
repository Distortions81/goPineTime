package main

import (
	"image/color"
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

var warning = color.RGBA{255, 170, 68, 255}

const clockBaseline = 145
const clockMeridiemGap = 4

func clockLineWidth(text, meridiem string) int16 {
	w, _ := lineWidth(&uifont.Clock, text)
	if meridiem != "" {
		mw, _ := lineWidth(&uifont.Regular18, meridiem)
		w += clockMeridiemGap + mw
	}
	return int16(w)
}

func drawLargeTime(d canvas, text, meridiem string) {
	if !textVisible(d, &uifont.Clock, clockBaseline, text) &&
		(meridiem == "" || !textVisible(d, &uifont.Regular18, clockBaseline, meridiem)) {
		return
	}
	width, _ := d.Size()
	x := (width - clockLineWidth(text, meridiem)) / 2
	writeLine(d, &uifont.Clock, x, clockBaseline, text, white)
	if meridiem != "" {
		w, _ := lineWidth(&uifont.Clock, text)
		writeLine(d, &uifont.Regular18, x+int16(w)+clockMeridiemGap, clockBaseline, meridiem, muted)
	}
}

func powerColor(p powerStatus) color.RGBA {
	if p.State != chargeDischarging {
		return accent
	}
	if p.Percent < 20 {
		return warning
	}
	return white
}

func (u *watchUI) drawFrame(d canvas, now time.Time, p powerStatus) {
	if themed, ok := d.(interface{ SetLightMode(bool) }); ok {
		themed.SetLightMode(u.lightMode)
	}
	d.FillScreen(themeColor(u.lightMode, black))
	if u.pairingVisible() {
		u.drawPairing(d)
	} else if u.powerNoticeVisible() {
		drawCharging(d, p)
	} else {
		u.draw(d, now)
		if u.page == pageSettings && u.settingsNote != "" {
			centered(d, &uifont.Regular18, 235, u.settingsNote, warning)
		}
	}
	drawStatusBattery(d, p)
}
