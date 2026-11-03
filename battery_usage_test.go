package main

import (
	"testing"
	"time"
)

func usagePower(percent uint8, state chargeState) powerStatus {
	return powerStatus{Percent: percent, Millivolts: 3500 + uint16(percent)*6, State: state}
}

func learnedSleepUsage() batteryUsage {
	var b batteryUsage
	b.observe(0, true, false, 0)
	b.sample(0, usagePower(80, chargeDischarging))
	b.observe(batterySettleSeconds, false, false, 0)
	b.sample(batterySettleSeconds, usagePower(80, chargeDischarging))
	for i := uint32(1); i <= 3; i++ {
		b.sample(batterySettleSeconds+i*8*60*60, usagePower(80-uint8(i)*10, chargeDischarging))
	}
	return b
}

func sampleLearnedBatteryUsage() *batteryHistory {
	h := sampleBatteryHistory()
	h.usage = learnedSleepUsage()
	h.usage.addCycle(2*24*60*60, 3600, 75)
	h.usage.addCycle(2*24*60*60, 3600, 75)
	return h
}

func TestSleepEstimateRequiresIndependentLongAsleepSpans(t *testing.T) {
	var b batteryUsage
	b.observe(0, false, false, 0)
	b.sample(0, usagePower(80, chargeDischarging))
	b.sample(batterySettleSeconds, usagePower(80, chargeDischarging))
	for i := uint32(1); i <= 3; i++ {
		b.sample(batterySettleSeconds+i*8*60*60, usagePower(80-uint8(i)*10, chargeDischarging))
		low, high, ready := b.sleepRemaining(b.percent)
		if ready != (i == 3) || (ready && (low == 0 || high <= low)) {
			t.Fatal("sleep estimate readiness/range", i, low, high, ready)
		}
	}
	if b.sleepWindows != 3 || b.sleepSeconds != 24*60*60 || b.sleepDrop != 30 || b.awake != 0 {
		t.Fatal("sleep accounting or window independence", b)
	}
	low, high, _ := b.sleepRemaining(50)
	if low != 2400*38 || high != 3600*42 {
		t.Fatal("sleep range lost endpoint allowance or 10% reserve", low, high)
	}
	if _, _, ready := b.sleepRemaining(10); ready {
		t.Fatal("predicted runtime into the reserve")
	}
}

func TestSleepLearningRejectsAwakeUseSmallDropsAndBadSamples(t *testing.T) {
	for _, fault := range []string{"awake", "small-drop", "rebound", "invalid-voltage", "charging", "exclusive"} {
		var b batteryUsage
		b.observe(0, false, false, 0)
		b.sample(0, usagePower(80, chargeDischarging))
		b.sample(batterySettleSeconds, usagePower(80, chargeDischarging))
		p := usagePower(70, chargeDischarging)
		switch fault {
		case "awake":
			b.observe(3600, true, false, 0)
		case "small-drop":
			p = usagePower(78, chargeDischarging)
		case "rebound":
			p = usagePower(83, chargeDischarging)
		case "invalid-voltage":
			p.Millivolts = 0
		case "charging":
			p.State = chargeExternalPower
		case "exclusive":
			b.observe(3600, false, true, 0)
		}
		b.sample(12*60*60, p)
		if b.sleepWindows != 0 {
			t.Fatal("ineligible sleep data accepted", fault, b)
		}
	}
}

func TestSleepBackgroundConfigurationInvalidatesModelButCivilEditsDoNot(t *testing.T) {
	now := time.Unix(90000, 0)
	u := newWatchUI(firmwareConfirmed)
	u.battery = &batteryHistory{usage: learnedSleepUsage()}
	u.clock.Set(now, now.Add(-200*24*time.Hour))
	u.observeBatteryUsage(now, false, false)
	if _, _, ready := u.battery.usage.sleepRemaining(50); !ready {
		t.Fatal("civil clock edit invalidated runtime measurement")
	}
	u.brightness = brightnessLow
	u.observeBatteryUsage(now.Add(time.Second), false, false)
	if u.battery.usage.sleepWindows != 3 || u.battery.usage.windowValid {
		t.Fatal("brightness discarded learned asleep data or kept mixed candidate")
	}
	u.phoneAuto = true
	u.observeBatteryUsage(now.Add(2*time.Second), false, false)
	if _, _, ready := u.battery.usage.sleepRemaining(50); ready {
		t.Fatal("Bluetooth change reused incompatible sleep rates")
	}
	u.phoneAuto = false
	u.battery.usage = learnedSleepUsage()
	u.quiet = quietManual
	u.observeBatteryUsage(now.Add(3*time.Second), false, false)
	if _, _, ready := u.battery.usage.sleepRemaining(50); ready {
		t.Fatal("notification policy change reused incompatible sleep rates")
	}
	u.quiet = quietScheduled | 22<<2 | 7<<7
	u.battery.usage = learnedSleepUsage()
	u.battery.usage.mode = u.batteryUsageMode(u.batteryFeatures())
	u.quiet = quietScheduled | 22<<2 | 8<<7
	u.observeBatteryUsage(now.Add(4*time.Second), false, false)
	if _, _, ready := u.battery.usage.sleepRemaining(50); ready {
		t.Fatal("quiet schedule edit reused incompatible sleep rates")
	}
}

func TestTypicalUseLearnsObservedDeepChargeCyclesAndScreenTime(t *testing.T) {
	var b batteryUsage
	for cycle := uint32(0); cycle < 2; cycle++ {
		start := cycle * 4 * 24 * 60 * 60
		b.observe(start, true, false, 0)
		b.sample(start, usagePower(100, chargeCharging))
		b.sample(start+60, usagePower(99, chargeDischarging))
		b.observe(start+60+60*60, false, false, 0) // One hour of screen-on time.
		b.sample(start+60+3*24*60*60, usagePower(24, chargeDischarging))
		low, high, ready := b.typicalCharge()
		if ready != (cycle == 1) || (ready && (low == 0 || high <= low)) {
			t.Fatal("typical charge readiness or range", cycle, low, high, ready)
		}
		if b.cycles != uint8(cycle+1) || b.cycleAwakeTotal != (cycle+1)*60*60 {
			t.Fatal("cycle or active use counted incorrectly", b)
		}
		b.sample(start+60+3*24*60*60+3600, usagePower(20, chargeDischarging))
		if b.cycles != uint8(cycle+1) {
			t.Fatal("same discharge counted twice")
		}
	}
}

func TestUsageDoesNotInventCyclesAtBootOrAfterUpdateAndInvalidADC(t *testing.T) {
	for _, fault := range []string{"boot", "update", "adc"} {
		var b batteryUsage
		b.observe(0, false, false, 0)
		if fault != "boot" {
			b.sample(0, usagePower(100, chargeCharging))
		}
		b.sample(60, usagePower(99, chargeDischarging))
		if fault == "update" {
			b.observe(3600, false, true, 0)
			b.observe(7200, false, false, 0)
		} else if fault == "adc" {
			b.sample(3600, powerStatus{})
		}
		b.sample(3*24*60*60, usagePower(20, chargeDischarging))
		if b.cycles != 0 {
			t.Fatal("invalid full-cycle evidence accepted", fault)
		}
	}
}

func TestUsageStaleLoopTimestampAndCountersStayBounded(t *testing.T) {
	var b batteryUsage
	b.observe(100, false, false, 0)
	b.sample(110, usagePower(80, chargeDischarging))
	b.observe(109, false, false, 0) // ADC completed after the loop timestamp.
	b.observe(120, true, false, 0)
	if b.asleep != 20 || b.awake != 0 {
		t.Fatal("stale timestamp manufactured elapsed time", b)
	}
	for i := 0; i < 2000; i++ {
		b.addSleep(8*60*60, 10)
		b.addCycle(3*24*60*60, 3600, 75)
	}
	if b.sleepWindows < 3 || b.cycles < 2 || b.sleepDrop < 15 || b.sleepSeconds == 0 || b.cycleSeconds == 0 {
		t.Fatal("bounded weighting overflowed or lost readiness", b)
	}
}
