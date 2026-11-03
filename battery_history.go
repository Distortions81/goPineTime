package main

import (
	"time"

	"github.com/Distortions81/goPine/internal/gfx"
	"github.com/Distortions81/goPine/internal/uifont"
)

const batteryHistoryCapacity = 32

type batterySample struct {
	minute     uint32 // Runtime clock, independent of user edits and phone sync.
	millivolts uint16
	features   uint8
	state      chargeState
}

// A fixed ring, allocated once on the first existing power sample. No flash
// writes, new ADC reads, wake deadlines, or storage per sample.
type batteryHistory struct {
	usage        batteryUsage
	points       [batteryHistoryCapacity]batterySample
	latest       batterySample
	revision     uint32
	unplugMinute uint32
	head, count  uint8
	unplugKnown  bool
}

func (u *watchUI) batteryFeatures() uint8 {
	flags := validBrightness(u.brightness)
	if u.touchWake {
		flags |= 1 << 2
	}
	if u.phoneAuto || (u.phone != nil && u.phone.mode != phoneOff) {
		flags |= 1 << 3
	}
	flags |= (u.healthOptions & 1) << 4
	flags |= (u.healthOptions & stepsEnabled) << 1
	flags |= uint8(u.quiet&3) << 6
	return flags
}

// Keep sample/calendar temporaries out of the loop's permanent stack frame.
//
//go:noinline
func (u *watchUI) recordBattery(now time.Time, power powerStatus) {
	if u.battery == nil {
		u.battery = &batteryHistory{}
	}
	features := u.batteryFeatures()
	usage := &u.battery.usage
	usage.observe(uint32(now.Unix()), !usage.initialized || usage.awakeNow, usage.excluded, u.batteryUsageMode(features))
	usage.sample(uint32(now.Unix()), power)
	u.battery.add(batterySample{uint32(now.Unix() / 60), power.Millivolts, features, power.State})
}

func (h *batteryHistory) at(index int) batterySample {
	return h.points[(int(h.head)-int(h.count)+index+batteryHistoryCapacity)%batteryHistoryCapacity]
}

func (h *batteryHistory) add(point batterySample) {
	if h.count == 0 || point != h.latest {
		h.latest = point
		h.revision++
	}
	if h.count != 0 {
		last := h.at(int(h.count) - 1)
		if last.state != chargeDischarging && point.state == chargeDischarging {
			h.unplugMinute, h.unplugKnown = point.minute, true
		} else if point.state != chargeDischarging {
			h.unplugKnown = false
		}
		if point.minute-last.minute < 15 && point.state == last.state && point.features == last.features {
			return
		}
	}
	h.points[h.head] = point
	h.head = (h.head + 1) % batteryHistoryCapacity
	if h.count < batteryHistoryCapacity {
		h.count++
	}
}

func (u *watchUI) drawBatteryHistory(d canvas) {
	centered(d, &uifont.Regular18, 29, "BATTERY", white)
	h := u.battery
	if h == nil || h.count == 0 {
		centered(d, &uifont.Regular18, 112, "No samples yet", muted)
		return
	}
	last := h.at(int(h.count) - 1)
	if textVisible(d, &uifont.Regular18, 58, "") {
		var text fixedDisplayText
		text.add("Now: ")
		text.number(uint32(h.latest.millivolts))
		text.add(" mV")
		centered(d, &uifont.Regular18, 58, string(text.data[:text.n]), white)
	}
	// Fixed 3.4–4.3 V scale; a small voltage wobble must not look like a
	// complete discharge. Long sleep gaps remain disconnected.
	const left, right, top, bottom int16 = 42, 224, 78, 154
	if controlVisible(d, top, bottom-top, bottom) {
		gfx.Line(d, left, top, left, bottom, card)
		gfx.Line(d, left, bottom, right, bottom, card)
		first := h.at(0)
		span := max(uint32(1), last.minute-first.minute)
		var px, py int16
		for i := 0; i < int(h.count); i++ {
			p := h.at(i)
			x := left + int16(uint64(p.minute-first.minute)*uint64(right-left)/uint64(span))
			y := bottom - int16(uint32(max(3400, min(4300, p.millivolts))-3400)*uint32(bottom-top)/900)
			if i > 0 {
				previous := h.at(i - 1)
				if p.minute-previous.minute <= 30 && p.millivolts != 0 && previous.millivolts != 0 {
					gfx.Line(d, px, py, x, y, accent)
				}
				if p.features != previous.features || p.state != previous.state {
					gfx.Line(d, x, bottom-4, x, bottom, warning)
				}
			}
			if p.millivolts != 0 {
				gfx.FillBox(d, x-1, y-1, 3, 3, accent)
			}
			px, py = x, y
		}
	}
	writeLine(d, &uifont.Regular18, 2, 89, "4.3", muted)
	writeLine(d, &uifont.Regular18, 2, 156, "3.4", muted)
	span := last.minute - h.at(0).minute
	drawNumberPair(d, 181, "Span ", span/60, "h ", span%60, "m", muted)
	if h.unplugKnown && h.latest.state == chargeDischarging {
		elapsed := h.latest.minute - h.unplugMinute
		drawNumberPair(d, 207, "Unplugged ", elapsed/60, "h ", elapsed%60, "m", white)
	} else {
		centered(d, &uifont.Regular18, 207, "Unplug time unknown", muted)
	}
	centered(d, &uifont.Regular18, 234, "Swipe for usage", muted)
}
