package main

import (
	"testing"
	"time"

	"tinygo.org/x/drivers/pixel"
)

func TestAllClockTimesFit(t *testing.T) {
	for hour := 0; hour < 24; hour++ {
		for minute := 0; minute < 60; minute++ {
			now := time.Date(2026, 10, 4, hour, minute, 0, 0, time.UTC)
			for _, use24 := range []bool{false, true} {
				u := watchUI{use24: use24}
				meridiem := ""
				if !use24 {
					meridiem = formatMeridiem(now)
				}
				text := u.timeLabel(now)
				if w := clockLineWidth(text, meridiem); w > 224 {
					t.Fatalf("clock time %s %s is too wide: %d", text, meridiem, w)
				}
			}
		}
	}
}

func TestClockHasNoLowerStatusOrHint(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 34, 0, 0, time.UTC)
	for _, use24 := range []bool{false, true} {
		for _, state := range []chargeState{chargeDischarging, chargeCharging, chargeExternalPower} {
			d := &memoryDisplay{}
			d.FillScreen(black)
			u := watchUI{page: pageClock, use24: use24, clock: watchClock{initialized: true}}
			u.drawFrame(d, now, powerStatus{Percent: 73, State: state})
			bg := pixel.NewColor[pixel.RGB444BE](black.R, black.G, black.B).RGBA()
			for y := clockBaseline + 1; y < 240; y++ {
				for x := 0; x < 240; x++ {
					if d.pixels[y*240+x] != bg {
						t.Fatalf("unexpected content below time at %d,%d", x, y)
					}
				}
			}
		}
	}
}
