package main

import (
	"time"

	"github.com/Distortions81/goPine/internal/gfx"
	"github.com/Distortions81/goPine/internal/uifont"
)

const (
	graphOff uint8 = iota
	graphHeart
	graphSteps
)

// Minute buckets share timestamps but keep independent heart/step validity.
// Only the most recent hour is rendered. No samples are written to flash.
type healthHistory struct {
	minutes    [64]int64
	heart      [64]uint8
	steps      [64]uint32
	stepsValid uint64
	revision   uint32
}

func (h *healthState) recordHealth(now time.Time, kind uint8, value uint32) {
	if kind != graphHeart && kind != graphSteps {
		return
	}
	if kind == graphHeart && (value < 40 || value > 200) {
		return
	}
	if h.history == nil {
		h.history = &healthHistory{}
	}
	history := h.history
	minute := now.Unix() / 60
	i := uint(minute) & 63
	bit := uint64(1) << i
	if history.minutes[i] != minute {
		history.minutes[i] = minute
		history.heart[i], history.steps[i] = 0, 0
		history.stepsValid &^= bit
	}
	if kind == graphHeart {
		if history.heart[i] == uint8(value) {
			return
		}
		history.heart[i] = uint8(value)
	} else {
		if history.stepsValid&bit != 0 && value == 0 {
			return
		}
		history.steps[i] += min(value, ^uint32(0)-history.steps[i])
		history.stepsValid |= bit
	}
	history.revision++
}

func (h *healthHistory) value(minute int64, kind uint8) (uint32, bool) {
	i := uint(minute) & 63
	if h.minutes[i] != minute {
		return 0, false
	}
	if kind == graphHeart {
		return uint32(h.heart[i]), h.heart[i] != 0
	}
	return h.steps[i], h.stepsValid&(uint64(1)<<i) != 0
}

func (u *watchUI) handleWatchGraph(e inputEvent) {
	if e.Kind == inputSwipeRight || backButtonTapped(e) {
		u.page = pageHealth
		return
	}
	if e.Kind != inputTap {
		return
	}
	for option := uint8(0); option < 3; option++ {
		y := int16(48 + int(option)*48)
		if inRect(e, 12, y, 228, y+42) {
			u.watchGraph = option
			return
		}
	}
}

func graphLabel(option uint8) string {
	switch option {
	case graphHeart:
		return "Heart rate"
	case graphSteps:
		return "Steps"
	default:
		return "Off"
	}
}

func (u *watchUI) drawWatchGraphOptions(d canvas) {
	centered(d, &uifont.Regular18, 29, "WATCH GRAPH", white)
	for option := uint8(0); option < 3; option++ {
		background := card
		label := graphLabel(option)
		if u.watchGraph == option {
			background = positive
			label += ": Selected"
		}
		clockControl(d, 12, int16(48+int(option)*48), 216, 42, label, background)
	}
	centered(d, &uifont.Regular18, 210, "Last hour, since boot", muted)
	centered(d, &uifont.Regular18, 234, "Tracking set in Health", muted)
}

func (u *watchUI) watchGraphVisible() bool {
	return u.watchGraph != graphOff && u.clock.initialized && !u.clock.approximate
}

func drawGraphNumber(d canvas, x, y int16, value uint32) {
	text := decimal(int(value))
	c := gfx.ResolveColor(d, muted)
	for _, ch := range text {
		if ch < '0' || ch > '9' {
			continue
		}
		for col := 0; col < 5; col++ {
			bits := batteryGlyphs[int(ch-'0')*5+col]
			for row := 0; row < 7; row++ {
				if bits&(1<<uint(row)) != 0 {
					d.SetPixel(x+int16(col), y+int16(row), c)
				}
			}
		}
		x += 6
	}
}

func (u *watchUI) drawWatchGraph(d canvas, now time.Time) {
	label := "Heart BPM - last hour"
	if u.watchGraph == graphSteps {
		label = "Steps/min - last hour"
	}
	centered(d, &uifont.Regular18, 174, label, muted)
	const left, right, top int16 = 32, 228, 184
	bottom := int16(231)
	if u.unreadNotifications() > 0 {
		bottom = 210
	}
	if !controlVisible(d, top, bottom-top, bottom) {
		return
	}
	var history *healthHistory
	if u.health != nil {
		history = u.health.history
	}
	latest := now.Unix() / 60
	low, high := uint32(200), uint32(0)
	count := 0
	if history != nil {
		for minute := latest - 59; minute <= latest; minute++ {
			if value, ok := history.value(minute, u.watchGraph); ok {
				low = min(low, value)
				high = max(high, value)
				count++
			}
		}
	}
	if count == 0 {
		centered(d, &uifont.Regular18, 211, "No readings yet", disabled)
		return
	}
	if u.watchGraph == graphHeart {
		low = max(uint32(30), (low-5)/10*10)
		high = max(low+20, (high+14)/10*10)
	} else {
		low = 0
		high = max(uint32(1), high)
	}
	// Numeric scale stays visible even in the compact layout with messages.
	drawGraphNumber(d, 8, top, high)
	drawGraphNumber(d, 8, bottom-6, low)
	gfx.Line(d, left, top, right, top, card)
	gfx.Line(d, left, bottom, right, bottom, card)
	gfx.Line(d, left, top, left, bottom, card)
	var previousX, previousY int16
	previousMinute := latest - 100
	for minute := latest - 59; minute <= latest; minute++ {
		value, ok := history.value(minute, u.watchGraph)
		if !ok {
			continue
		}
		x := left + int16((minute-(latest-59))*int64(right-left)/59)
		y := bottom - int16(int64(value-low)*int64(bottom-top)/int64(high-low))
		if u.watchGraph == graphSteps {
			gfx.FillBox(d, x, bottom-max(int16(1), bottom-y), 2, max(int16(1), bottom-y), accent)
		} else {
			if minute-previousMinute <= 2 {
				gfx.Line(d, previousX, previousY, x, y, accent)
			}
			gfx.FillCircle(d, x, y, 2, accent)
		}
		previousX, previousY, previousMinute = x, y, minute
	}
}
