package main

import "time"

const (
	chargerDebounce       = 150 * time.Millisecond
	buttonReleaseDebounce = 20 * time.Millisecond
	idleMaxWait           = 4 * time.Minute // below the 24-bit RTC wrap period
	activeRadioInterval   = 20 * time.Millisecond
)

// A GPIO edge starts one settling deadline, not a periodic battery poll.
// Contact bounce and short charger pulses cannot repeatedly wake the screen.
type chargerInput struct {
	state, candidate chargeState
	due              time.Time
}

func (c *chargerInput) update(state chargeState, edge bool, now time.Time) bool {
	if edge || state != c.candidate {
		c.candidate = state
		c.due = now.Add(chargerDebounce)
	}
	if c.due.IsZero() || now.Before(c.due) {
		return false
	}
	c.due = time.Time{}
	changed := c.state != c.candidate
	c.state = c.candidate
	return changed
}

// Keep the first press responsive, but require a stable release before another
// press can toggle the display. A latched pulse still counts after release.
type buttonInput struct {
	down      bool
	releaseAt time.Time
}

func (b *buttonInput) update(high, pressLatched bool, now time.Time) bool {
	if !b.down && (high || pressLatched) {
		b.down = true
	}
	if high {
		b.releaseAt = time.Time{}
	} else if b.down {
		if b.releaseAt.IsZero() || pressLatched {
			b.releaseAt = now.Add(buttonReleaseDebounce)
		} else if !now.Before(b.releaseAt) {
			b.down = false
			b.releaseAt = time.Time{}
		}
	}
	return b.down
}

// Operations needed to acknowledge one latched PORT source. The host model
// tests the same ordering as the hardware adapter, including short pulses.
type senseInput interface {
	senseHigh() bool
	disableSense()
	clearLatch()
	high() bool
	armSense(high bool)
}

func acknowledgeSense(pin senseInput, activeHigh bool) bool {
	triggeredHigh := pin.senseHigh()
	// Clear the source before LATCH: a still-active SENSE condition prevents
	// clearing it and can continually retrigger PORT in latched detect mode.
	pin.disableSense()
	pin.clearLatch()
	high := pin.high()
	pin.armSense(!high) // sense the opposite level, including release edges
	return triggeredHigh == activeHigh || high == activeHigh
}

func idleWatchdogInterval(running bool, reload uint32) time.Duration {
	if !running {
		return idleMaxWait
	}
	quarter := time.Duration(uint64(reload)+1) * time.Second / (32768 * 4)
	// Keep the same three-quarter-period margin without waking every second
	// when the inherited watchdog permits a longer interval.
	return max(minimumLoopWait, min(idleMaxWait, quarter))
}

func idleRTCTicks(duration time.Duration) uint32 {
	// Clamp before multiplication, even for the application's "no deadline".
	duration = max(0, min(duration, idleMaxWait))
	return uint32(max(3, (duration.Nanoseconds()*32768+999999999)/1000000000))
}

func inputIdleDelay(now, refreshAt, screenOffAt, releaseAt, vibrationEnd time.Time, touchDown, radioBusy bool) time.Duration {
	schedule := newWakeSchedule(now, noDeadlineDelay)
	for _, due := range [...]time.Time{refreshAt, screenOffAt, releaseAt, vibrationEnd} {
		if !due.IsZero() {
			schedule.by(due)
		}
	}
	if touchDown {
		schedule.after(2 * time.Millisecond)
	}
	if radioBusy {
		schedule.after(activeRadioInterval)
	}
	return schedule.delay()
}
