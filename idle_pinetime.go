//go:build pinetime

package main

import (
	"device/arm"
	"device/nrf"
	"runtime/interrupt"
	"time"
)

// RTC0 belongs to NimBLE; RTC1 is TinyGo's monotonic clock. Leave both alone.
// RTC2 runs freely and supplies only a one-shot compare for the UI's idle wait.
func configureIdleTimer() {
	nrf.RTC2.TASKS_STOP.Set(1)
	nrf.RTC2.INTENCLR.Set(0xffffffff)
	nrf.RTC2.EVTENCLR.Set(0xffffffff)
	nrf.RTC2.PRESCALER.Set(0)
	nrf.RTC2.EVENTS_COMPARE[0].Set(0)
	irq := interrupt.New(nrf.IRQ_RTC2, func(interrupt.Interrupt) {
		nrf.RTC2.INTENCLR.Set(nrf.RTC_INTENCLR_COMPARE0)
		nrf.RTC2.EVENTS_COMPARE[0].Set(0)
	})
	irq.SetPriority(0xc0)
	irq.Enable()
	nrf.RTC2.TASKS_START.Set(1)
}

func enabledInterruptPending() bool {
	return arm.NVIC.ISPR[0].Get()&arm.NVIC.ISER[0].Get() != 0 ||
		arm.NVIC.ISPR[1].Get()&arm.NVIC.ISER[1].Get() != 0
}

func waitForInput(duration time.Duration) {
	duration = min(duration, idleWatchdogInterval(nrf.WDT.RUNSTATUS.Get() != 0, nrf.WDT.CRV.Get()))
	state := interrupt.Disable()
	duration = min(duration, timeRadioIdleDelay())
	if duration <= 0 || timeRadioHasUpdate() {
		interrupt.Restore(state)
		return
	}
	stopRuntimeRNG(timeRadioNeedsService())
	ticks := idleRTCTicks(duration)
	// InfiniTime's tickless port keeps IRQs pending during the sleep handoff.
	// This closes the check/sleep race: the ISR cannot consume a wake event
	// just before WFE. Unlike 0.3.4, GPIOTE stays enabled with a real PORT ISR.
	oldSCR := arm.SCB.SCR.Get()
	arm.SCB.SCR.Set(oldSCR | arm.SCB_SCR_SEVONPEND)
	nrf.RTC2.EVENTS_COMPARE[0].Set(0)
	arm.ClearPendingIRQ(nrf.IRQ_RTC2)
	nrf.RTC2.CC[0].Set((nrf.RTC2.COUNTER.Get() + ticks) & 0xffffff)
	nrf.RTC2.INTENSET.Set(nrf.RTC_INTENSET_COMPARE0)
	for portChanged.Get() == 0 && !enabledInterruptPending() && nrf.RTC2.EVENTS_COMPARE[0].Get() == 0 {
		arm.Asm("dsb")
		arm.Asm("wfe")
	}
	nrf.RTC2.INTENCLR.Set(nrf.RTC_INTENCLR_COMPARE0)
	nrf.RTC2.EVENTS_COMPARE[0].Set(0)
	arm.ClearPendingIRQ(nrf.IRQ_RTC2)
	arm.SCB.SCR.Set(oldSCR)
	interrupt.Restore(state) // dispatch button/touch/radio/runtime IRQs now
}

// This direct wait assumes the current single Go application task. Bluetooth
// queues are checked with IRQs masked; pending radio interrupts wake WFE.
// Additional Go background tasks would require scheduler integration.
