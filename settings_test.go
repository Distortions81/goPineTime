package main

import (
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/checkpoint"
)

func TestSettingsWritesAreCoalescedAndPowerGated(t *testing.T) {
	f, j, _ := clockTestStorage(t)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	p := loadSettings(&u, now, j)
	p.update(&u, now.Add(time.Hour), true)
	if f.writes != 0 {
		t.Fatal("idle boot wrote flash")
	}
	u.use24 = true
	u.touchWake = true
	p.update(&u, now, true)
	u.timers.countdown.preset = 10 * time.Minute
	p.update(&u, now.Add(time.Second), true)
	p.update(&u, now.Add(2*time.Second), true)
	if f.writes != 0 {
		t.Fatal("edit burst was not coalesced")
	}
	p.update(&u, now.Add(4*time.Second), false)
	if f.writes != 0 || u.settingsNote == "" {
		t.Fatal("low-power save was not deferred")
	}
	p.update(&u, now.Add(5*time.Second), true)
	if f.writes == 0 || u.settingsNote != "" {
		t.Fatal("save did not resume")
	}
	writes := f.writes
	u.timers.watch.toggle(now.Add(6 * time.Second))
	p.update(&u, now.Add(6*time.Second), true)
	p.update(&u, now.Add(8*time.Second), true)
	if f.writes <= writes {
		t.Fatal("stopwatch start not persisted")
	}
	writes = f.writes
	for i := 10; i < 1000; i++ {
		p.update(&u, now.Add(time.Duration(i)*time.Second), true)
	}
	if f.writes != writes {
		t.Fatal("running timer wrote periodic snapshots")
	}
	loaded := newWatchUI(firmwareConfirmed)
	loadSettings(&loaded, now.Add(10*time.Second), j)
	if !loaded.use24 || !loaded.touchWake || loaded.timers.countdown.preset != 10*time.Minute || !loaded.timers.watch.running {
		t.Fatal("settings did not restore")
	}
	f.fail = true
	u.use24 = false
	p.update(&u, now.Add(time.Hour), true)
	p.update(&u, now.Add(time.Hour+settingsSaveDelay), true)
	f.fail = false
	p.update(&u, now.Add(2*time.Hour), true)
	if !p.failed || f.writes != writes {
		t.Fatal("uncertain flash save was retried")
	}
}

func TestHealthSettingsRestoreWithoutPeriodicFlashWrites(t *testing.T) {
	f, j, _ := clockTestStorage(t)
	now := time.Unix(100, 0)
	u := newWatchUI(firmwareConfirmed)
	p := loadSettings(&u, now, j)
	u.healthOptions = heartPeriodic | 8 | stepsEnabled
	u.watchGraph = graphSteps
	p.update(&u, now, true)
	p.update(&u, now.Add(settingsSaveDelay), true)
	writes := f.writes
	if writes == 0 {
		t.Fatal("health choices were not saved")
	}
	loaded := newWatchUI(firmwareConfirmed)
	loadSettings(&loaded, now.Add(time.Minute), j)
	if loaded.healthOptions != u.healthOptions || loaded.watchGraph != graphSteps || loaded.health != nil {
		t.Fatal("health options failed to restore without activating sensors")
	}
	d := &fakeHealthDevice{}
	u.health = &healthState{device: d}
	u.tickHealth(now, false)
	for i := 0; i < 100; i++ {
		d.count++
		u.tickHealth(now.Add(time.Duration(i)*time.Second), false)
		p.update(&u, now.Add(time.Duration(i)*time.Second), true)
	}
	if f.writes != writes {
		t.Fatal("sensor readings caused periodic flash writes")
	}
}

func TestSettingsCheckFreshPowerWithoutRetryDeadline(t *testing.T) {
	f, j, _ := clockTestStorage(t)
	now := time.Unix(0, 0)
	u := newWatchUI(firmwareConfirmed)
	p := loadSettings(&u, now, j)
	reads, charged := 0, false
	p.writePowerOK = func() bool { reads++; return charged }
	u.touchWake = true
	p.update(&u, now, true) // UI's cached battery says adequate power.
	if reads != 0 {
		t.Fatal("sampled before a write was due")
	}
	p.update(&u, now.Add(settingsSaveDelay), true)
	if reads != 1 || f.writes != 0 || !p.deadline(true).IsZero() || p.failed {
		t.Fatal("fresh low power was ignored or left a retry deadline", reads, f.writes)
	}
	// Sleep needs no retries. A later wake/charging event can resume the save.
	charged = true
	p.update(&u, now.Add(12*time.Hour), true)
	if reads != 2 || f.writes == 0 || !p.deadline(true).IsZero() {
		t.Fatal("deferred save did not resume after fresh power check")
	}
	p.update(&u, now.Add(13*time.Hour), true)
	if reads != 2 {
		t.Fatal("unchanged settings sampled battery")
	}
}

func TestTouchWakePersistsBothChoices(t *testing.T) {
	f, j, _ := clockTestStorage(t)
	now := time.Unix(0, 0)
	u := newWatchUI(firmwareConfirmed)
	p := loadSettings(&u, now, j)
	for _, enabled := range []bool{true, false} {
		u.touchWake = enabled
		p.update(&u, now, true)
		if p.deadline(true) != now.Add(settingsSaveDelay) || !p.deadline(false).IsZero() {
			t.Fatal("save deadline ignores delay or power gate")
		}
		p.update(&u, now.Add(settingsSaveDelay), true)
		if !p.deadline(true).IsZero() {
			t.Fatal("saved settings keep waking the loop")
		}
		reopened, err := checkpoint.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		v := newWatchUI(firmwareConfirmed)
		loadSettings(&v, now, reopened)
		if v.touchWake != enabled {
			t.Fatalf("touch wake after reboot = %v, want %v", v.touchWake, enabled)
		}
		now = now.Add(time.Minute)
	}
}

func TestRuntimeRestoresFromTimestampsAndReconcilesSync(t *testing.T) {
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("local", -6*3600)} {
		_, j, _ := clockTestStorage(t)
		now := time.Date(2026, 10, 5, 12, 0, 0, 0, zone)
		u := newWatchUI(firmwareConfirmed)
		u.clock.Set(now, now)
		u.timers.watch.toggle(now)
		u.timers.countdown.toggle(now)
		u.timers.snooze[0] = now.Add(5 * time.Minute)
		p := loadSettings(&u, now, j)
		if !p.flush(&u, now.Add(time.Minute), true) {
			t.Fatal("snapshot failed")
		}
		// Reboot's approximate clock is 30 seconds behind the eventual sync.
		boot := now.Add(90 * time.Second)
		v := newWatchUI(firmwareConfirmed)
		loadSettings(&v, boot, j)
		if v.timers.watch.elapsed(boot) != 90*time.Second || v.timers.countdown.left(boot) != 210*time.Second {
			t.Fatal("timestamp restore failed")
		}
		v.clock.Set(boot.Add(10*time.Second), now.Add(130*time.Second))
		v.tickTimers(boot.Add(10*time.Second), firmwareConfirmed)
		if v.timers.watch.elapsed(boot.Add(10*time.Second)) != 130*time.Second || v.timers.countdown.left(boot.Add(10*time.Second)) != 170*time.Second || v.timers.snooze[0].Sub(boot.Add(10*time.Second)) != 170*time.Second {
			t.Fatal("resync did not reconcile original timestamps")
		}
		before := v.timers.countdown.deadline
		v.clock.Set(boot.Add(11*time.Second), now.Add(24*time.Hour))
		v.tickTimers(boot.Add(11*time.Second), firmwareConfirmed)
		if v.timers.countdown.deadline != before {
			t.Fatal("later civil edit changed a live timer")
		}
	}
}

func TestUnsetClockRestoresDurationsWithoutEpochArithmetic(t *testing.T) {
	_, j, _ := clockTestStorage(t)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.timers.watch.toggle(now)
	u.timers.countdown.toggle(now)
	u.timers.snooze[0] = now.Add(5 * time.Minute)
	u.timers.alarms[1] = alarm{hour: 0, minute: 1, enabled: true, repeat: alarmDaily}
	p := loadSettings(&u, now, j)
	if !p.flush(&u, now.Add(time.Minute), true) {
		t.Fatal("save failed")
	}
	boot := time.Unix(0, 0).UTC()
	v := newWatchUI(firmwareConfirmed)
	v.clock = watchClock{approximate: true} // Explicitly no usable calendar.
	loadSettings(&v, boot, j)
	if v.timers.watch.elapsed(boot) != time.Minute || v.timers.countdown.left(boot) != 4*time.Minute || v.timers.snooze[0].Sub(boot) != 4*time.Minute {
		t.Fatal("used epoch as a calendar")
	}
	v.tickTimers(boot.Add(time.Minute), firmwareConfirmed)
	if v.timers.active {
		t.Fatal("uninitialized calendar fired an alarm")
	}
	// A second cold boot must retain the original anchors, not replace them
	// with 1970 values. A later sync can then account for the whole gap.
	r := v.runtimeSnapshot(boot.Add(time.Minute))
	if r.ClockInitialized || r.StopwatchStarted != calendarMillis(now) || r.CountdownDeadline != calendarMillis(now.Add(5*time.Minute)) {
		t.Fatal("lost original timestamps with unset clock")
	}
	v.clock.Set(boot.Add(time.Minute), now.Add(3*time.Minute))
	v.tickTimers(boot.Add(time.Minute), firmwareConfirmed)
	if v.timers.watch.elapsed(boot.Add(time.Minute)) != 3*time.Minute || v.timers.countdown.left(boot.Add(time.Minute)) != 2*time.Minute {
		t.Fatal("first usable clock did not reconcile")
	}
}

func TestTimersCreatedBeforeClockInitialization(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	u := newWatchUI(firmwareConfirmed)
	u.clock = watchClock{approximate: true}
	u.timers.watch.toggle(now)
	u.timers.countdown.toggle(now)
	r := u.runtimeSnapshot(now.Add(time.Minute))
	if r.ClockInitialized || r.Timestamped != 0 {
		t.Fatal("unset clock created timestamps")
	}
	v := newWatchUI(firmwareConfirmed)
	v.clock = u.clock
	boot := now.Add(time.Second)
	v.loadRuntime(r, boot)
	v.clock.Set(boot.Add(time.Minute), time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
	v.tickTimers(boot.Add(time.Minute), firmwareConfirmed)
	if v.timers.watch.elapsed(boot.Add(time.Minute)) != 2*time.Minute || v.timers.countdown.left(boot.Add(time.Minute)) != 3*time.Minute {
		t.Fatal("initializing clock reset relative timers")
	}
}

func TestResumeDoesNotUndoPauseOrRestart(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.timers.watch.toggle(now)
	u.timers.countdown.toggle(now)
	r := u.runtimeSnapshot(now.Add(time.Minute))
	v := newWatchUI(firmwareConfirmed)
	v.loadRuntime(r, now.Add(2*time.Minute))
	v.timers.watch.toggle(now.Add(130 * time.Second))
	v.timers.countdown.reset()
	v.clock.Set(now.Add(140*time.Second), now.Add(time.Hour))
	v.tickTimers(now.Add(140*time.Second), firmwareConfirmed)
	if v.timers.watch.running || v.timers.watch.saved != 130*time.Second || v.timers.countdown.running || v.timers.countdown.remaining != 5*time.Minute {
		t.Fatal("sync undid user's timer changes")
	}
}

func TestExpiredCountdownRestoresOneAlert(t *testing.T) {
	_, j, _ := clockTestStorage(t)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.timers.countdown.toggle(now)
	p := loadSettings(&u, now, j)
	p.flush(&u, now, true)
	boot := now.Add(10 * time.Minute)
	v := newWatchUI(firmwareConfirmed)
	loadSettings(&v, boot, j)
	if !v.tickTimers(boot, firmwareConfirmed) || v.timers.source != countdownSource {
		t.Fatal("missed expired persisted countdown")
	}
	v.dismissAlert(boot, false, firmwareConfirmed)
	v.clock.Set(boot, now.Add(time.Hour))
	if v.tickTimers(boot, firmwareConfirmed) {
		t.Fatal("sync resurrected dismissed timer")
	}
}

func TestSettingsAndClockShareOneSnapshot(t *testing.T) {
	f, j, regs := clockTestStorage(t)
	now := time.Date(2026, 10, 5, 12, 34, 56, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.use24 = true
	u.timers.watch.toggle(now)
	cp := loadSavedClock(&u, now, j, regs)
	sp := loadSettings(&u, now, j)
	if !sp.flush(&u, now.Add(time.Minute), true) || !cp.beforeReset(&u, now.Add(time.Minute), true) {
		t.Fatal("handoff failed")
	}
	j, err := checkpoint.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	boot := time.Unix(0, 0)
	v := newWatchUI(firmwareConfirmed)
	v.clock = watchClock{approximate: true}
	loadSavedClock(&v, boot, j, regs)
	loadSettings(&v, boot, j)
	if !v.clock.initialized || !v.use24 || v.timers.watch.elapsed(boot) != time.Minute {
		t.Fatal("clock and timer handoff disagreed")
	}
}

func TestRestoredAlertKeepsItsTimeout(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.timers.active, u.timers.source, u.timers.ringSince = true, countdownSource, now
	r := u.runtimeSnapshot(now.Add(20 * time.Second))
	v := newWatchUI(firmwareConfirmed)
	v.loadRuntime(r, now.Add(40*time.Second))
	if !v.tickTimers(now.Add(40*time.Second), firmwareConfirmed) || v.timers.ringSince != now {
		t.Fatal("restored alert restarted full timeout")
	}
	v.tickTimers(now.Add(alertDuration), firmwareConfirmed)
	if v.timers.active {
		t.Fatal("restored alert outlived deadline")
	}
}

func TestUninitializedClockCannotCreateResetHandoff(t *testing.T) {
	_, j, regs := clockTestStorage(t)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.clock.initialized = false // Flag, not the incidental runtime year, is authority.
	p := loadSavedClock(&u, now, j, regs)
	if p.beforeReset(&u, now, true) {
		t.Fatal("uninitialized calendar made handoff")
	}
	u.clock.Set(now, time.Unix(0, 0))
	if u.clock.initialized {
		t.Fatal("epoch-zero clock marked initialized")
	}
	u.clock.Set(now, now)
	if !u.clock.initialized || u.clock.approximate {
		t.Fatal("valid calendar was not initialized")
	}
}
