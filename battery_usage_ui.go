package main

import "github.com/Distortions81/goPine/internal/uifont"

func drawUsageRange(d canvas, y int16, prefix string, low, high uint32, ready bool) {
	if !textVisible(d, &uifont.Regular18, y, "") {
		return
	}
	var text fixedDisplayText
	text.add(prefix)
	if !ready {
		text.add("Learning")
	} else {
		for i, seconds := range [2]uint32{low, high} {
			tenths := seconds / 8640
			if i == 1 {
				text.add("-")
				tenths = (seconds + 8639) / 8640
			}
			text.number(tenths / 10)
			text.add(".")
			text.number(tenths % 10)
		}
		text.add("d")
	}
	centered(d, &uifont.Regular18, y, string(text.data[:text.n]), white)
}

func (u *watchUI) drawBatteryUsage(d canvas) {
	centered(d, &uifont.Regular18, 29, "BATTERY USE", white)
	if u.battery == nil {
		centered(d, &uifont.Regular18, 112, "No samples yet", muted)
		return
	}
	b := &u.battery.usage
	low, high, ready := b.sleepRemaining(b.percent)
	if u.battery.latest.state != chargeDischarging {
		centered(d, &uifont.Regular18, 62, "Sleep: On charger", white)
	} else if b.validSample && b.percent <= batteryReserve {
		centered(d, &uifont.Regular18, 62, "Sleep: Reserve", white)
	} else {
		drawUsageRange(d, 62, "Sleep: ", low, high, ready)
	}
	drawNumberPair(d, 88, "Data: ", b.sleepSeconds/3600, "h / ", uint32(b.sleepWindows), " spans", muted)
	low, high, ready = b.typicalCharge()
	drawUsageRange(d, 124, "Typical: ", low, high, ready)
	drawNumberPair(d, 150, "Cycles: ", uint32(b.cycles), " / need ", 2, "", muted)
	if textVisible(d, &uifont.Regular18, 188, "") {
		awake, elapsed := b.awake, b.awake+b.asleep
		if b.cycleSeconds != 0 {
			awake, elapsed = b.cycleAwakeTotal, b.cycleSeconds
		}
		var text fixedDisplayText
		text.add("Screen: ")
		if elapsed == 0 {
			text.add("Learning")
		} else {
			text.number(uint32(uint64(awake) * (24 * 60) / uint64(elapsed)))
			text.add(" min/day")
		}
		centered(d, &uifont.Regular18, 188, string(text.data[:text.n]), accent)
	}
	centered(d, &uifont.Regular18, 212, "Since boot; RAM only", muted)
	centered(d, &uifont.Regular18, 235, "Rough; 10% reserve", muted)
}
