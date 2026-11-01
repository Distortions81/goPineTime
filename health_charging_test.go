package main

import (
	"testing"
	"time"
)

func TestChargingPausesBothHealthSensorsAndPreservesPreferences(t *testing.T) {
	now := time.Unix(100, 0)
	d := &fakeHealthDevice{}
	u := newWatchUI(firmwareConfirmed)
	u.healthOptions = withHeartInterval(heartPeriodic|stepsEnabled, 5)
	u.health = &healthState{device: d}
	u.tickHealth(now, false)
	d.count = 42
	settings := u.settings()
	u.healthCharging = true
	u.tickHealth(now.Add(time.Second), false)
	if d.heart || d.steps || !u.health.next.IsZero() || u.health.steps != 42 {
		t.Fatal("charger did not shut down sensors/retain steps")
	}
	if u.settings() != settings {
		t.Fatal("charger changed saved tracking preferences")
	}
	for _, exclusive := range []bool{false, true, false} {
		u.tickHealth(now.Add(time.Hour), exclusive)
	}
	if d.heart || d.steps || d.heartStarts != 1 || d.stepStarts != 1 {
		t.Fatal("sensors restarted while connected to power")
	}
	u.healthCharging = false
	u.tickHealth(now.Add(2*time.Hour), false)
	if !d.heart || !d.steps || u.health.steps != 42 || u.settings() != settings {
		t.Fatal("unplug failed to resume tracking")
	}
}

func TestChargingBootDoesNotInitializeHealthDevices(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	u.healthOptions = heartPeriodic | stepsEnabled
	u.healthCharging = true
	u.tickHealth(time.Unix(100, 0), false)
	if u.health != nil {
		t.Fatal("charging boot initialized sensors")
	}
}

func TestLoopPausesHealthForChargingAndFullyChargedPower(t *testing.T) {
	for _, state := range []chargeState{chargeCharging, chargeExternalPower} {
		display := newScriptDisplay()
		power := powerStatus{Percent: 100, State: chargeDischarging}
		display.power = &power
		device := &fakeHealthDevice{}
		u := newWatchUI(firmwareConfirmed)
		u.healthOptions = heartPeriodic | stepsEnabled
		u.health = &healthState{device: device}
		display.steps = []scriptStep{
			{at: time.Second, event: inputEvent{Kind: inputPower}, check: func() { power.State = state }},
			{at: 2 * time.Second, event: inputEvent{Kind: inputSleep}, check: func() {
				if device.heart || device.steps {
					t.Fatal("power event failed to pause sensors", state)
				}
			}},
			{at: time.Minute, event: inputEvent{Kind: inputPower}, check: func() { power.State = chargeDischarging }},
			{at: time.Minute + time.Second, event: inputEvent{Kind: inputQuit}, check: func() {
				if !device.heart || !device.steps {
					t.Fatal("unplug failed to resume sensors", state)
				}
			}},
		}
		sync := timeSyncController{radio: &fakePhoneRadio{}}
		l := watchLoop{display: display, ui: &u, sync: &sync, renderer: &frameRenderer{}, now: func() time.Time { return display.now }, state: func() updateState { return firmwareConfirmed }}
		if err := l.run(); err != nil {
			t.Fatal(err)
		}
	}
}
