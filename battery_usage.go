package main

import "time"

const (
	batteryReserve       = 10
	sleepWindowMinimum   = 4 * 60 * 60
	sleepWindowMaximum   = 7 * 24 * 60 * 60
	batterySettleSeconds = 30 * 60
	// Brightness invalidates a candidate window without changing the sleeping
	// load. Background radio/touch/sensor/notification changes discard learning.
	sleepModeMask = uint32((1<<23 - 1) &^ 3)
)

// All times are runtime seconds, never the editable civil clock. This is a
// bounded, RAM-only measurement model, not a battery capacity measurement.
type batteryUsage struct {
	at, asleep, awake                                uint32
	windowAt, windowSleep, windowAwake               uint32
	cycleAt, cycleAwake                              uint32
	sleepSeconds, sleepFast, sleepSlow               uint32
	cycleSeconds, cycleAwakeTotal                    uint32
	cycleFast, cycleSlow, settleUntil                uint32
	mode                                             uint32
	sleepDrop, cycleDrop                             uint16
	windowMV                                         uint16
	windowPercent, cyclePercent, chargePeak, percent uint8
	sleepWindows, cycles                             uint8
	initialized, awakeNow, sampled, onBattery        bool
	windowValid, cycleValid, excluded                bool
	validSample                                      bool
}

func (b *batteryUsage) account(second uint32) {
	if !b.initialized {
		b.at, b.initialized, b.awakeNow = second, true, true
		return
	}
	elapsed := second - b.at
	if int32(elapsed) < 0 {
		return // A loop timestamp can predate a just-completed ADC sample.
	}
	if b.awakeNow {
		b.awake += elapsed
	} else {
		b.asleep += elapsed
	}
	b.at = second
}

func (b *batteryUsage) observe(second uint32, awake, excluded bool, mode uint32) {
	b.account(second)
	if mode != b.mode {
		if (mode^b.mode)&sleepModeMask != 0 {
			b.sleepSeconds, b.sleepDrop, b.sleepWindows = 0, 0, 0
			b.sleepFast, b.sleepSlow = 0, 0
		}
		b.mode, b.windowValid, b.settleUntil = mode, false, second+batterySettleSeconds
	}
	if excluded || b.excluded != excluded {
		b.windowValid, b.cycleValid = false, false
		b.settleUntil = second + batterySettleSeconds
	}
	b.awakeNow, b.excluded = awake, excluded
}

func (b *batteryUsage) anchor(second uint32, p powerStatus) {
	b.windowAt, b.windowSleep, b.windowAwake = second, b.asleep, b.awake
	b.windowMV, b.windowPercent, b.windowValid = p.Millivolts, p.Percent, true
}

//go:noinline
func (b *batteryUsage) sample(second uint32, p powerStatus) {
	p.Percent = min(p.Percent, 100)
	b.percent = p.Percent
	b.account(second)
	b.validSample = p.Millivolts >= 3300 && p.Millivolts <= 4400
	if !b.validSample {
		b.windowValid, b.cycleValid = false, false
		b.settleUntil = second + batterySettleSeconds
		return
	}
	discharging := p.State == chargeDischarging
	if !b.sampled {
		b.sampled, b.onBattery = true, discharging
		b.settleUntil = second + batterySettleSeconds
		if !discharging {
			b.chargePeak = p.Percent
		}
		return
	}
	if !discharging {
		if b.onBattery {
			b.chargePeak = 0
		}
		b.chargePeak = max(b.chargePeak, p.Percent)
		b.onBattery, b.windowValid, b.cycleValid = false, false, false
		return
	}
	if !b.onBattery {
		b.onBattery, b.windowValid = true, false
		b.settleUntil = second + batterySettleSeconds
		b.cycleAt, b.cycleAwake, b.cyclePercent = second, b.awake, p.Percent
		b.cycleValid = b.chargePeak >= 90 && p.Percent >= 90 && !b.excluded
		b.chargePeak = 0
	}
	if b.excluded {
		return
	}
	// A typical-use cycle requires an observed near-full unplug and a deep
	// discharge. It includes ordinary awake use, BLE and sensor work.
	if b.cycleValid && p.Percent <= 25 && b.cyclePercent-p.Percent >= 60 {
		elapsed, drop := second-b.cycleAt, uint32(b.cyclePercent-p.Percent)
		if elapsed >= 8*60*60 && elapsed <= 180*24*60*60 {
			b.addCycle(elapsed, b.awake-b.cycleAwake, drop)
		}
		b.cycleValid = false // At most one observation per charge.
	}
	if int32(second-b.settleUntil) < 0 || p.Percent < 15 || p.Percent > 90 || p.Millivolts == 0 {
		b.windowValid = false
		return
	}
	if !b.windowValid {
		b.anchor(second, p)
		return
	}
	elapsed, awake := second-b.windowAt, b.awake-b.windowAwake
	if awake > 30 || elapsed > sleepWindowMaximum {
		b.anchor(second, p)
		return
	}
	if elapsed < sleepWindowMinimum || p.Percent >= b.windowPercent || p.Millivolts >= b.windowMV {
		return
	}
	drop := uint32(b.windowPercent - p.Percent)
	if drop >= 5 {
		b.addSleep(b.asleep-b.windowSleep, drop)
		b.anchor(second, p)
	}
}

func (b *batteryUsage) addSleep(seconds, drop uint32) {
	if b.sleepWindows == 255 {
		b.sleepSeconds, b.sleepDrop, b.sleepWindows = b.sleepSeconds/2, b.sleepDrop/2, 127
	}
	b.sleepSeconds += seconds
	b.sleepDrop += uint16(drop)
	b.sleepWindows++
	fast, slow := seconds/(drop+2), seconds/(drop-2)
	if b.sleepFast == 0 || fast < b.sleepFast {
		b.sleepFast = fast
	}
	b.sleepSlow = max(b.sleepSlow, slow)
}

func (b *batteryUsage) addCycle(seconds, awake, drop uint32) {
	if b.cycles == 255 {
		b.cycleSeconds, b.cycleAwakeTotal, b.cycleDrop, b.cycles = b.cycleSeconds/2, b.cycleAwakeTotal/2, b.cycleDrop/2, 127
	}
	b.cycleSeconds += seconds
	b.cycleAwakeTotal += awake
	b.cycleDrop += uint16(drop)
	b.cycles++
	fast, slow := seconds/(drop+2), seconds/(drop-2)
	if b.cycleFast == 0 || fast < b.cycleFast {
		b.cycleFast = fast
	}
	b.cycleSlow = max(b.cycleSlow, slow)
}

// The range includes observed rate variation and +/-2 percentage-point endpoint
// tolerance. It is deliberately not called a statistical confidence interval:
// voltage-derived percentages have additional uncalibrated systematic error.
func (b *batteryUsage) sleepRemaining(percent uint8) (low, high uint32, ready bool) {
	percent = min(percent, 100)
	if b.sleepWindows < 3 || b.sleepSeconds < 24*60*60 || b.sleepDrop < 15 || percent <= batteryReserve || !b.onBattery || b.excluded || !b.validSample {
		return
	}
	usable := uint32(percent - batteryReserve)
	return b.sleepFast * (usable - min(usable, 2)), b.sleepSlow * (usable + 2), true
}

func (b *batteryUsage) typicalCharge() (low, high uint32, ready bool) {
	if b.cycles < 2 || b.cycleSeconds < 16*60*60 {
		return
	}
	return b.cycleFast * (100 - batteryReserve), b.cycleSlow * (100 - batteryReserve), true
}

//go:noinline
func (u *watchUI) observeBatteryUsage(now time.Time, awake, excluded bool) {
	if u.battery != nil {
		mode := u.batteryUsageMode(u.batteryFeatures())
		u.battery.usage.observe(uint32(now.Unix()), awake, excluded, mode)
	}
}

func (u *watchUI) batteryUsageMode(features uint8) uint32 {
	mode := uint32(features) | uint32(u.healthOptions)<<8
	if u.quiet&quietScheduled != 0 {
		mode |= uint32(u.quiet>>2&1023) << 13
	}
	return mode
}
