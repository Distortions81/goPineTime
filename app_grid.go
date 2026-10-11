package main

import (
	"image/color"

	"github.com/Distortions81/goPine/internal/gfx"
	"github.com/Distortions81/goPine/internal/uifont"
)

const appCardWidth, appCardHeight int16 = 102, 58

func appCardPosition(index int) (int16, int16) {
	return 12 + int16(index%2)*114, 44 + int16(index/2)*62
}

func appCardLabel(index int) string {
	switch index {
	case 0:
		return "Alarms"
	case 1:
		return "Stopwatch"
	case 2:
		return "Timer"
	case 3:
		return "Music"
	case 4:
		return "Messages"
	default:
		return "Weather"
	}
}

func (u *watchUI) handleAppsTap(e inputEvent) {
	for index := 0; index < 6; index++ {
		x, y := appCardPosition(index)
		if !inRect(e, x, y, x+appCardWidth, y+appCardHeight) {
			continue
		}
		switch index {
		case 0:
			u.page = pageAlarms
		case 1:
			u.page = pageStopwatch
		case 2:
			u.page = pageCountdown
		case 3:
			u.openMusic()
		case 4:
			u.openInbox()
			// Weather remains visible but unavailable until companion delivery works.
		}
		return
	}
}

func drawApps(d canvas) {
	centered(d, &uifont.Bold18, 29, "APPS", white)
	for index := 0; index < 6; index++ {
		x, y := appCardPosition(index)
		if !controlVisible(d, y, appCardHeight, y+48) {
			continue
		}
		gfx.RoundBox(d, x, y, appCardWidth, appCardHeight, 6, card)
		foreground, icon := white, accent
		if index == 5 {
			foreground, icon = disabled, disabled
		}
		drawAppIcon(d, index, x+appCardWidth/2, y+17, icon)
		label := appCardLabel(index)
		width, _ := lineWidth(&uifont.Regular18, label)
		writeLine(d, &uifont.Regular18, x+(appCardWidth-int16(width))/2, y+48, label, foreground)
	}
}

// Icons use the same clipped drawing primitives as the rest of the interface;
// they need neither a bitmap allocation nor a separate asset buffer.
func drawAppIcon(d canvas, index int, x, y int16, c color.RGBA) {
	switch index {
	case 0: // Alarm clock.
		gfx.Circle(d, x, y+1, 8, c)
		gfx.Line(d, x, y+1, x, y-4, c)
		gfx.Line(d, x, y+1, x+4, y+3, c)
		gfx.Line(d, x-10, y-5, x-5, y-9, c)
		gfx.Line(d, x+5, y-9, x+10, y-5, c)
		gfx.Line(d, x-5, y+8, x-7, y+11, c)
		gfx.Line(d, x+5, y+8, x+7, y+11, c)
	case 1: // Stopwatch.
		gfx.Circle(d, x, y+2, 9, c)
		gfx.Line(d, x, y+2, x+4, y-3, c)
		gfx.FillBox(d, x-3, y-11, 7, 2, c)
		gfx.Line(d, x, y-9, x, y-7, c)
		gfx.Line(d, x+7, y-6, x+10, y-9, c)
	case 2: // Hourglass.
		gfx.Line(d, x-8, y-10, x+8, y-10, c)
		gfx.Line(d, x-8, y+10, x+8, y+10, c)
		gfx.Line(d, x-7, y-9, x-7, y-6, c)
		gfx.Line(d, x+7, y-9, x+7, y-6, c)
		gfx.Line(d, x-7, y-6, x+7, y+6, c)
		gfx.Line(d, x+7, y-6, x-7, y+6, c)
		gfx.Line(d, x-7, y+6, x-7, y+9, c)
		gfx.Line(d, x+7, y+6, x+7, y+9, c)
	case 3: // Paired musical notes.
		gfx.FillCircle(d, x-7, y+7, 3, c)
		gfx.FillCircle(d, x+6, y+4, 3, c)
		gfx.Line(d, x-4, y+7, x-4, y-7, c)
		gfx.Line(d, x+9, y+4, x+9, y-10, c)
		gfx.Line(d, x-4, y-7, x+9, y-10, c)
		gfx.Line(d, x-4, y-5, x+9, y-8, c)
	case 4: // Message bubble.
		gfx.Box(d, x-11, y-9, 23, 16, 1, c)
		gfx.Line(d, x-6, y+6, x-6, y+11, c)
		gfx.Line(d, x-6, y+11, x, y+6, c)
		gfx.Line(d, x-7, y-4, x+7, y-4, c)
		gfx.Line(d, x-7, y, x+3, y, c)
	case 5: // Sun behind a cloud.
		gfx.Circle(d, x+5, y-4, 6, c)
		gfx.FillCircle(d, x-5, y+2, 5, c)
		gfx.FillCircle(d, x+1, y+1, 6, c)
		gfx.FillCircle(d, x+7, y+4, 4, c)
		gfx.FillBox(d, x-7, y+3, 17, 6, c)
	}
}
