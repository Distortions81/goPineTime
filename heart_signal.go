package main

import (
	"image/color"
	"time"

	"github.com/Distortions81/goPine/internal/gfx"
	"github.com/Distortions81/goPine/internal/uifont"
)

// Keep acquisition evidence independent of the BPM estimator. Even rejected
// samples must remain observable, and snapshots survive a periodic shutdown.
type heartSignal struct {
	values                                     [64]uint16
	minimum, maximum, latest                   uint16
	ambient                                    uint32
	samples, accepted, invalid, gaps, maxGapMS uint32
	last                                       time.Time
	head, filled                               uint8
}

//go:noinline
func (s *heartSignal) reset() { *s = heartSignal{} }

func (s *heartSignal) add(raw uint16, ambient uint32, now time.Time) {
	if s.samples == 0 {
		s.minimum = raw
	}
	s.minimum, s.maximum = min(s.minimum, raw), max(s.maximum, raw)
	s.latest, s.ambient = raw, ambient
	if !s.last.IsZero() {
		gap := max(time.Duration(0), now.Sub(s.last))
		s.maxGapMS = max(s.maxGapMS, uint32(gap/time.Millisecond))
		if gap > 5*heartSamplePeriod/2 {
			s.gaps++
		}
	}
	s.last = now
	s.samples++
	if raw == 0 || raw == 65535 {
		s.invalid++
	}
	s.values[s.head] = raw
	s.head = (s.head + 1) & 63
	if s.filled < 64 {
		s.filled++
	}
}

func hexByte(value byte) string {
	const digits = "0123456789ABCDEF"
	text := [2]byte{digits[value>>4], digits[value&15]}
	return string(text[:])
}

func drawNumberPair(d canvas, y int16, prefix string, a uint32, separator string, b uint32, suffix string, c color.RGBA) {
	if !textVisible(d, &uifont.Regular18, y, "") {
		return
	}
	var text fixedDisplayText
	text.add(prefix)
	text.number(a)
	text.add(separator)
	text.number(b)
	text.add(suffix)
	centered(d, &uifont.Regular18, y, string(text.data[:text.n]), c)
}

func (u *watchUI) drawHeartSignal(d canvas) {
	centered(d, &uifont.Regular18, 29, "HEART SIGNAL", white)
	if u.health == nil || u.health.signal.samples == 0 {
		centered(d, &uifont.Regular18, 108, "No optical samples", white)
		centered(d, &uifont.Regular18, 138, "Enable heart tracking", muted)
		if u.health != nil && u.health.heartFailed {
			centered(d, &uifont.Regular18, 185, "Sensor setup failed", warning)
		}
	} else {
		s := &u.health.signal
		drawNumberPair(d, 60, "ADC ", uint32(s.latest), " ALS ", s.ambient, "", white)
		const left, right, top, bottom int16 = 16, 224, 82, 132
		if controlVisible(d, top, bottom-top, bottom) {
			gfx.Line(d, left, bottom, right, bottom, card)
			lo, hi := s.minimum, max(s.minimum+1, s.maximum)
			// Avoid uint16 wrap when the ADC is saturated at its maximum.
			if s.minimum == 65535 {
				lo, hi = 65534, 65535
			}
			var px, py int16
			for i := 0; i < int(s.filled); i++ {
				index := (int(s.head) - int(s.filled) + i + 64) & 63
				x := left + int16(i)*(right-left)/63
				y := bottom - int16(uint32(s.values[index]-lo)*uint32(bottom-top)/uint32(hi-lo))
				if i > 0 {
					gfx.Line(d, px, py, x, y, accent)
				}
				px, py = x, y
			}
		}
		drawNumberPair(d, 158, "Range ", uint32(s.minimum), "-", uint32(s.maximum), "", muted)
		drawNumberPair(d, 182, "Samples ", s.samples, " Good ", s.accepted, "", white)
		drawNumberPair(d, 206, "Gap ", s.maxGapMS, "ms / ", s.gaps, "", muted)
	}
	if u.health != nil {
		if source, ok := u.health.device.(interface{ HeartConfig() heartSensorConfig }); ok {
			c := source.HeartConfig()
			label := "ID " + hexByte(c.id) + " not verified"
			if c.verified {
				label = "ID " + hexByte(c.id) + " " + hexByte(c.enable) + "/" + hexByte(c.driver) + "/" + hexByte(c.resolution) + "/" + hexByte(c.gain)
			}
			centered(d, &uifont.Regular18, 232, label, muted)
		}
	}
}
