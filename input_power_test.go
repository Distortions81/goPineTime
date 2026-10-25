package main

import (
	"testing"
	"time"
)

// A minimal GPIO electrical model: SENSE latches a matching level, and a
// still-matching level prevents clearing LATCH. Edges can arrive during rearm.
type modeledSensePin struct {
	level, enabled, sense, latched bool
	onRead, onArm                  func(*modeledSensePin)
}

func (p *modeledSensePin) drive(high bool) {
	p.level = high
	if p.enabled && high == p.sense {
		p.latched = true
	}
}
func (p *modeledSensePin) senseHigh() bool { return p.sense }
func (p *modeledSensePin) disableSense()   { p.enabled = false }
func (p *modeledSensePin) clearLatch() {
	p.latched = p.enabled && p.level == p.sense
}
func (p *modeledSensePin) high() bool {
	if p.onRead != nil {
		p.onRead(p)
	}
	return p.level
}
func (p *modeledSensePin) armSense(high bool) {
	if p.onArm != nil {
		p.onArm(p)
	}
	p.enabled, p.sense = true, high
	p.drive(p.level)
}

func TestPORTShortPulseSurvivesUntilHandler(t *testing.T) {
	for _, activeHigh := range []bool{false, true} {
		p := &modeledSensePin{level: !activeHigh}
		p.armSense(activeHigh)
		p.drive(activeHigh)
		p.drive(!activeHigh) // release before the CPU/handler runs
		if !p.latched || !acknowledgeSense(p, activeHigh) {
			t.Fatal("short button/touch pulse lost", activeHigh)
		}
		if p.latched || p.sense != activeHigh {
			t.Fatal("released source not ready for next pulse", activeHigh)
		}
	}
}

func TestPORTHeldInputDoesNotRetrigger(t *testing.T) {
	for _, activeHigh := range []bool{false, true} {
		p := &modeledSensePin{level: activeHigh}
		p.armSense(activeHigh)
		if !acknowledgeSense(p, activeHigh) || p.latched {
			t.Fatal("held source was lost or keeps waking the CPU", activeHigh)
		}
		p.drive(!activeHigh)
		if !p.latched || acknowledgeSense(p, activeHigh) || p.latched {
			t.Fatal("release incorrectly treated as another press", activeHigh)
		}
		p.drive(activeHigh)
		if !p.latched || !acknowledgeSense(p, activeHigh) {
			t.Fatal("second press lost", activeHigh)
		}
	}
}

func TestPORTTransitionDuringRearmRemainsPending(t *testing.T) {
	for _, activeHigh := range []bool{false, true} {
		p := &modeledSensePin{level: activeHigh}
		p.armSense(activeHigh)
		p.onArm = func(p *modeledSensePin) { p.drive(!activeHigh) }
		if !acknowledgeSense(p, activeHigh) || !p.latched {
			t.Fatal("transition between level read and SENSE write lost", activeHigh)
		}
		p.onArm = nil
		if acknowledgeSense(p, activeHigh) || p.latched {
			t.Fatal("follow-up release failed to clear", activeHigh)
		}
	}
}

func TestPORTRepressDuringReleaseHandling(t *testing.T) {
	p := &modeledSensePin{level: true}
	p.armSense(true)
	acknowledgeSense(p, true)
	p.drive(false)
	p.onRead = func(p *modeledSensePin) { p.drive(true) }
	if !acknowledgeSense(p, true) || p.latched {
		t.Fatal("repress during release handling lost or retriggered")
	}
}

func TestButtonBounceAndLatchedShortPress(t *testing.T) {
	start := time.Unix(0, 0)
	var b buttonInput
	for _, tc := range []struct {
		ms              int
		high, irq, down bool
	}{
		{0, false, false, false},
		{10, false, true, true}, // short press ended before processing
		{20, true, true, true},  // contact bounce must not make a new press
		{25, false, false, true},
		{44, false, false, true},
		{45, false, false, false}, // stable release completes at its deadline
		{100, true, true, true},
		{10000, true, false, true}, // held button remains pressed indefinitely
		{10001, false, false, true},
		{10021, false, false, false},
	} {
		if got := b.update(tc.high, tc.irq, start.Add(time.Duration(tc.ms)*time.Millisecond)); got != tc.down {
			t.Fatalf("at %d ms: down=%v, want %v", tc.ms, got, tc.down)
		}
	}
}

func TestIdleInputHasNoButtonPollDeadline(t *testing.T) {
	now := time.Unix(0, 0)
	zero := time.Time{}
	if got := inputIdleDelay(now, now.Add(noDeadlineDelay), zero, zero, zero, false, false); got != noDeadlineDelay {
		t.Fatal("unnecessary idle polling", got)
	}
	for _, tc := range []struct {
		refresh, screen, release, vibration time.Time
		touch, radio                        bool
		want                                time.Duration
	}{
		{now.Add(time.Hour), zero, zero, zero, false, false, time.Hour},
		{now.Add(time.Hour), now.Add(300 * time.Millisecond), zero, zero, false, false, 300 * time.Millisecond},
		{now.Add(time.Hour), zero, now.Add(20 * time.Millisecond), zero, false, false, 20 * time.Millisecond},
		{now.Add(time.Hour), zero, zero, now.Add(80 * time.Millisecond), false, false, 80 * time.Millisecond},
		{now.Add(time.Hour), zero, zero, zero, true, false, 2 * time.Millisecond},
		{now.Add(time.Hour), zero, zero, zero, false, true, 20 * time.Millisecond},
		{now.Add(time.Millisecond), zero, zero, zero, false, true, time.Millisecond},
	} {
		if got := inputIdleDelay(now, tc.refresh, tc.screen, tc.release, tc.vibration, tc.touch, tc.radio); got != tc.want {
			t.Fatalf("wait %v, want %v", got, tc.want)
		}
	}
}

func TestIdleTimerBoundsAndWatchdog(t *testing.T) {
	for _, tc := range []struct {
		delay time.Duration
		ticks uint32
	}{{0, 3}, {time.Millisecond, 33}, {time.Second, 32768}, {noDeadlineDelay, 4 * 60 * 32768}} {
		if got := idleRTCTicks(tc.delay); got != tc.ticks {
			t.Fatal("RTC conversion", tc.delay, got, tc.ticks)
		}
		// A deadline crossing the 24-bit counter wrap remains in the future.
		const nearWrap = uint32(0xfffff0)
		compare := (nearWrap + tc.ticks) & 0xffffff
		if (compare-nearWrap)&0xffffff != tc.ticks {
			t.Fatal("RTC wrap changed delay")
		}
	}
	for _, reload := range []uint32{3276, 32767, 5*32768 - 1, 0xffffffff} {
		interval := idleWatchdogInterval(true, reload)
		period := time.Duration(uint64(reload)+1) * time.Second / 32768
		if interval > idleMaxWait || interval > period/4 || interval < minimumLoopWait {
			t.Fatal("unsafe watchdog service interval", reload, interval, period)
		}
	}
	if got := idleWatchdogInterval(true, 5*32768-1); got != 1250*time.Millisecond {
		t.Fatal("five-second watchdog still wakes earlier than needed", got)
	}
	if got := idleWatchdogInterval(true, 0xffffffff); got != idleMaxWait {
		t.Fatal("long watchdog exceeds the RTC wait bound", got)
	}
	if idleWatchdogInterval(false, 0) != idleMaxWait {
		t.Fatal("watchdog disabled still adds wakeups")
	}
}
