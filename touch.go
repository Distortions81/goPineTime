package main

import "time"

const touchSampleTimeout = 250 * time.Millisecond

type touchEvent struct {
	Activity bool
	Tap      bool
	inputEvent
}

// A control needs a complete press/release, not just an interrupt or a stale
// gesture register. Holds require fresh, stationary contact samples; a timer
// alone must never approve an update after a missed release or I2C error.
type touchTracker struct {
	down, moved, suppress bool
	x, y                  int16
	since                 time.Time
	last                  time.Time
}

func (t *touchTracker) cancel() {
	t.down = false
	t.suppress = true
}

// A button/automatic wake only needs to suppress an existing contact. Marking
// an idle tracker suppressed would swallow the first fresh tap on the alert.
func (t *touchTracker) cancelContact() {
	if t.down {
		t.cancel()
	}
}

func (t *touchTracker) decode(data [6]byte, now time.Time) touchEvent {
	x := int16(data[2]&0xf)<<8 | int16(data[3])
	y := int16(data[4]&0xf)<<8 | int16(data[5])
	points := data[1] & 0xf
	event := touchEvent{Activity: true, inputEvent: inputEvent{Kind: inputCancel, X: x, Y: y}}
	gesture := data[0]
	if points == 0 && (x >= 240 || y >= 240) {
		// Even an unusable release position ends contact, but cannot tap.
		t.cancel()
		t.suppress = false
		return event
	}
	if x >= 240 || y >= 240 || points > 1 || (gesture > 5 && gesture != 0x0b && gesture != 0x0c) {
		return t.missingSample(now)
	}
	if points == 1 {
		if t.down && now.Sub(t.last) > touchSampleTimeout {
			t.cancel() // Lost contact history; require release before rearming.
		}
		t.last = now
		if !t.down {
			t.down, t.moved, t.x, t.y, t.since = true, false, x, y, now
			if !t.suppress {
				event.Kind = inputPress
			}
		} else if abs16(x-t.x) > 16 || abs16(y-t.y) > 16 {
			t.moved = true
		} else if !t.moved && !t.suppress {
			event.Kind, event.Held = inputHold, now.Sub(t.since)
		}
		return event
	}
	event.Kind = inputRelease
	event.Tap = t.down && !t.moved && !t.suppress &&
		abs16(x-t.x) <= 16 && abs16(y-t.y) <= 16 &&
		now.Sub(t.since) < time.Second && (data[0] == 0 || data[0] == 5)
	if event.Tap {
		event.Kind = inputTap
	} else if t.down && !t.suppress && now.Sub(t.since) < time.Second {
		dx, dy := x-t.x, y-t.y
		if abs16(dx) >= 50 && abs16(dx) > 2*abs16(dy) {
			if dx < 0 {
				event.Kind = inputSwipeLeft
			} else {
				event.Kind = inputSwipeRight
			}
		} else if abs16(dy) >= 50 && abs16(dy) > 2*abs16(dx) {
			if dy < 0 {
				event.Kind = inputSwipeUp
			} else {
				event.Kind = inputSwipeDown
			}
		}
	}
	t.down, t.suppress = false, false
	return event
}

// Match InfiniTime's IRQ-driven reads with EnTouch enabled (periodic contact
// interrupts). Do not depend on out-of-event I2C availability or treat a cached
// register read as a new sample. PineTime drivers also filter sporadic invalid
// reports: these neither advance a hold nor invent a release. Bound that grace
// period so lost release events cannot leave contact latched indefinitely.
func (t *touchTracker) poll(bus touchBus, pending bool, now time.Time) touchEvent {
	if !pending {
		return t.missingSample(now)
	}
	var data [6]byte
	if err := bus.Tx(touchAddress, []byte{0x01}, data[:]); err != nil {
		return t.missingSample(now)
	}
	return t.decode(data, now)
}

func (t *touchTracker) missingSample(now time.Time) touchEvent {
	if t.down && now.Sub(t.last) > touchSampleTimeout {
		t.cancel()
		return touchEvent{Activity: true, inputEvent: inputEvent{Kind: inputCancel}}
	}
	return touchEvent{}
}

func abs16(n int16) int16 {
	if n < 0 {
		return -n
	}
	return n
}
