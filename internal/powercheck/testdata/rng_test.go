package main

import (
	"device/nrf"
	"machine"
	"testing"
)

func resetRNG() {
	nrf.Reset()
	runtimeRNGStarted = false
}

func TestBootSeedsLeaveRNGRunningUntilCleanup(t *testing.T) {
	resetRNG()
	// runtime.initRand calls the real machine.GetRNG twice before main.
	first, _ := machine.GetRNG()
	second, _ := machine.GetRNG()
	if !nrf.Running || !runtimeRNGStarted || nrf.Starts != 1 || nrf.Reads != 8 || first == second {
		t.Fatal("startup behavior changed; review the bridge")
	}
	stopRuntimeRNG(false)
	if nrf.Running || runtimeRNGStarted || nrf.Stops != 1 || nrf.RNG.EVENTS_VALRDY.Get() != 0 {
		t.Fatal("Bluetooth-never-started sleep retained runtime RNG activity")
	}
}

func TestLaterReadsRestartAcrossRepeatedSleep(t *testing.T) {
	resetRNG()
	for i := uint32(1); i <= 20; i++ {
		_, err := machine.GetRNG()
		if err != nil || !nrf.Running || nrf.Starts != i || nrf.Reads != i*4 || nrf.Bias != 1 {
			t.Fatal("runtime RNG failed to restart", i, err)
		}
		stopRuntimeRNG(false)
		stopRuntimeRNG(false)
		if nrf.Running || runtimeRNGStarted || nrf.Stops != i {
			t.Fatal("cleanup failed or repeated an already completed stop", i)
		}
	}
}

func TestActiveOrDrainingRadioRetainsRNGOwnership(t *testing.T) {
	resetRNG()
	_, _ = machine.GetRNG()
	// Busy includes a stopped UI session whose asynchronous disconnect remains
	// in flight. It must protect both the hardware and TinyGo's remembered flag.
	stopRuntimeRNG(true)
	if !nrf.Running || !runtimeRNGStarted || nrf.Stops != 0 {
		t.Fatal("idle cleanup interrupted Bluetooth entropy collection")
	}
	_, _ = machine.GetRNG()
	if nrf.Starts != 1 {
		t.Fatal("busy cleanup changed TinyGo's driver state")
	}
	stopRuntimeRNG(false)
	// Model NimBLE starting RNG later, without using TinyGo's driver.
	nrf.RNG.TASKS_START.Set(1)
	stopRuntimeRNG(true)
	if !nrf.Running || nrf.Stops != 1 {
		t.Fatal("Bluetooth RNG was stopped after taking ownership")
	}
}
