package main

import (
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

func sampleBatteryHistory() *batteryHistory {
	h := &batteryHistory{}
	for i := 0; i < batteryHistoryCapacity; i++ {
		state := chargeDischarging
		if i < 3 {
			state = chargeCharging
		}
		h.add(batterySample{minute: uint32(100 + i*15), millivolts: uint16(4200 - i*13), features: uint8(i / 8), state: state})
	}
	return h
}

func TestBatteryHistoryBoundsCadenceAndChanges(t *testing.T) {
	var h batteryHistory
	h.add(batterySample{minute: 100, millivolts: 4000})
	for minute := uint32(101); minute < 115; minute++ {
		h.add(batterySample{minute: minute, millivolts: 3990})
	}
	if h.count != 1 || h.latest.minute != 114 || h.latest.millivolts != 3990 {
		t.Fatal("ordinary samples were not coalesced while retaining latest reading")
	}
	h.add(batterySample{minute: 115, millivolts: 3980})
	if h.count != 2 {
		t.Fatal("15-minute sample missing")
	}
	h.add(batterySample{minute: 115, millivolts: 3980, features: 4})
	if h.count != 3 || h.at(2).features != 4 {
		t.Fatal("power-related setting change missing")
	}
	for i := uint32(0); i < 1000; i++ {
		h.add(batterySample{minute: 130 + i*15, millivolts: 3900})
	}
	if h.count != batteryHistoryCapacity || h.at(0).minute != 130+(1000-batteryHistoryCapacity)*15 || h.at(31).minute != 130+999*15 {
		t.Fatal("ring grew or lost chronological order")
	}
}

func TestBatteryUnplugTracksObservedTransitionsAndIgnoresCivilEdits(t *testing.T) {
	now := time.Unix(6000, 0)
	u := newWatchUI(firmwareConfirmed)
	u.recordBattery(now, powerStatus{Millivolts: 4000})
	if u.battery.unplugKnown {
		t.Fatal("boot invented an unplug time")
	}
	u.recordBattery(now.Add(time.Minute), powerStatus{Millivolts: 4100, State: chargeCharging})
	u.recordBattery(now.Add(2*time.Minute), powerStatus{Millivolts: 4200, State: chargeExternalPower})
	if u.battery.unplugKnown {
		t.Fatal("full battery on charger counted as unplugged")
	}
	u.recordBattery(now.Add(3*time.Minute), powerStatus{Millivolts: 4190})
	if !u.battery.unplugKnown || u.battery.unplugMinute != 103 {
		t.Fatal("unplug event not recorded")
	}
	u.clock.Set(now, now.Add(-200*24*time.Hour))
	u.recordBattery(now.Add(4*time.Minute), powerStatus{Millivolts: 4180})
	if u.battery.unplugMinute != 103 || u.battery.latest.minute != 104 {
		t.Fatal("civil clock edit changed battery elapsed time")
	}
	u.recordBattery(now.Add(5*time.Minute), powerStatus{Millivolts: 4200, State: chargeExternalPower})
	if u.battery.unplugKnown {
		t.Fatal("charger connection retained discharging session")
	}
}

func TestDailyFeatureHotPathsAndRenderingDoNotAllocate(t *testing.T) {
	now := time.Date(2026, 11, 3, 23, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.clock.Set(now, now)
	u.quiet = quietScheduled | 22<<2 | 7<<7
	u.battery = sampleBatteryHistory()
	if n := testing.AllocsPerRun(100, func() {
		u.recordBattery(now, powerStatus{Millivolts: 3900})
		u.notificationQuiet(now)
	}); n != 0 {
		t.Fatal("steady battery/quiet bookkeeping allocates", n)
	}
	for _, p := range []page{pageQuickControls, pageQuietHours, pageBatteryHistory, pageBatteryUsage} {
		u.page = p
		if p == pageBatteryUsage {
			u.battery = sampleLearnedBatteryUsage()
		}
		d := &benchmarkDisplay{}
		var r frameRenderer
		draw := func(c canvas) { u.drawFrame(c, now, powerStatus{Percent: 73, Millivolts: 3900}) }
		_ = r.render(d, draw)
		if n := testing.AllocsPerRun(20, func() { _ = r.render(d, draw) }); n != 0 {
			t.Fatal("daily feature rendering allocates", p, n)
		}
	}
	for _, label := range []string{"Buzz: Quiet hours", "Tap time to add 1 hour", "Set or sync time first", "Same times: all day", "Alarms still vibrate", "Unplug time unknown", "Gaps during sleep"} {
		if width, _ := lineWidth(&uifont.Regular18, label); width > 216 {
			t.Fatal("daily feature label too wide", label, width)
		}
	}
}

func BenchmarkBatteryHistorySample(b *testing.B) {
	u := newWatchUI(firmwareConfirmed)
	u.battery = sampleBatteryHistory()
	now := time.Unix(60*1000, 0)
	power := usagePower(66, chargeDischarging)
	u.recordBattery(now, power)
	now = now.Add(31 * time.Minute)
	u.recordBattery(now, power)
	b.ReportAllocs()
	for b.Loop() {
		u.recordBattery(now, power)
	}
}

func BenchmarkQuietHoursNotification(b *testing.B) {
	now := time.Date(2026, 11, 3, 23, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.clock.Set(now, now)
	u.quiet = quietScheduled | 22<<2 | 7<<7
	b.ReportAllocs()
	for b.Loop() {
		u.notificationQuiet(now)
	}
}
