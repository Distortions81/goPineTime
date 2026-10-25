//go:build pinetime

package main

import (
	"device/nrf"
	_ "unsafe" // for go:linkname
)

// TinyGo 0.42 seeds the runtime from machine.GetRNG before main, leaving RNG
// running even when Bluetooth has never started. Its driver remembers START
// in this flag; stopping hardware without clearing it would hang a later read.
// Keep this bridge covered against the pinned driver's actual getRNG function.
// TinyGo needs extern for a reference to an existing global; host Go uses
// linkname. The firmware resource gate also checks linked Go RNG call sites:
// use after boot would need coordination with NimBLE's interrupt-driven RNG.
//
//go:linkname runtimeRNGStarted machine.rngStarted
//go:extern machine.rngStarted
var runtimeRNGStarted bool

// Call before radio initialization, or with IRQs masked at the idle handoff.
// NimBLE owns RNG throughout an active or draining session, including while
// its entropy pool is full. Never stop it or change TinyGo's flag in that case.
func stopRuntimeRNG(radioBusy bool) {
	if radioBusy || !runtimeRNGStarted {
		return
	}
	nrf.RNG.TASKS_STOP.Set(1)
	nrf.RNG.EVENTS_VALRDY.Set(0)
	runtimeRNGStarted = false
}
