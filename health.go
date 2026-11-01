package main

import (
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

const (
	heartOff uint8 = iota
	heartPeriodic
)

const stepsEnabled uint8 = 16

// Match InfiniTime's 10 Hz acquisition cadence for the HRS3300 conversion mode.
const heartSamplePeriod = 100 * time.Millisecond
const heartWindow = 30 * time.Second

func heartInterval(options uint8) time.Duration {
	switch heartIntervalIndex(options) {
	case 1:
		return 15 * time.Minute
	case 2:
		return 30 * time.Minute
	case 3:
		return time.Minute
	case 4:
		return time.Hour
	case 5:
		return 3 * time.Hour
	default:
		return 5 * time.Minute
	}
}

func heartIntervalIndex(options uint8) uint8       { return options>>2&3 | (options&2)<<1 }
func withHeartInterval(options, index uint8) uint8 { return options&^14 | (index&3)<<2 | (index>>2)<<1 }
func nextHeartInterval(options uint8) uint8 {
	index := heartIntervalIndex(options)
	switch index {
	case 0:
		index = 1
	case 1:
		index = 2
	case 2:
		index = 4
	case 4:
		index = 5
	case 5:
		index = 3
	default:
		index = 0
	}
	return withHeartInterval(options, index)
}
func heartIntervalLabel(options uint8) string {
	switch heartIntervalIndex(options) {
	case 1:
		return "Every 15 minutes"
	case 2:
		return "Every 30 minutes"
	case 3:
		return "Every minute"
	case 4:
		return "Every hour"
	case 5:
		return "Every 3 hours"
	default:
		return "Every 5 minutes"
	}
}

type healthDevice interface {
	SetHeart(bool) error
	ReadHeart() (uint16, error)
	SetSteps(bool) error
	ReadSteps() (uint32, error)
}

type healthState struct {
	device                                     healthDevice
	options                                    uint8
	heartOn, stepsOn, heartFailed, stepsFailed bool
	suspended                                  bool
	next, sampleAt, stopAt, measureAt, stepsAt time.Time
	bpm                                        uint16
	attempted                                  bool
	lastReading                                time.Time
	steps, rawSteps, revision                  uint32
	pulse                                      pulseDetector
	history                                    *healthHistory
	signal                                     heartSignal
}

func (u *watchUI) openHealth() {
	if u.health == nil {
		u.health = &healthState{device: newHealthDevice()}
	}
	u.page = pageHealth
}

func (u *watchUI) handleHealth(e inputEvent) {
	if e.Kind == inputSwipeRight || backButtonTapped(e) {
		u.page = pageApps
		return
	}
	if e.Kind != inputTap {
		return
	}
	switch {
	case inRect(e, 12, 42, 228, 76):
		u.healthOptions ^= heartPeriodic
	case inRect(e, 12, 80, 228, 114) && u.healthOptions&1 == heartPeriodic:
		u.healthOptions = nextHeartInterval(u.healthOptions)
	case inRect(e, 12, 118, 228, 152):
		u.healthOptions ^= stepsEnabled
	case inRect(e, 12, 156, 228, 190):
		u.page = pageWatchGraph
	case inRect(e, 12, 192, 228, 216):
		u.page = pageHeartSignal
	}
}

func (u *watchUI) drawHealth(d canvas, now time.Time) {
	centered(d, &uifont.Regular18, 29, "HEART & STEPS", white)
	mode := "Heart rate: Off"
	switch u.healthOptions & 1 {
	case heartPeriodic:
		mode = "Heart rate: Periodic"
	}
	clockControl(d, 12, 42, 216, 34, mode, card)
	intervalColor := disabled
	if u.healthOptions&1 == heartPeriodic {
		intervalColor = white
	}
	clockControlLabel(d, 12, 80, 216, 34, heartIntervalLabel(u.healthOptions), card, intervalColor)
	steps := "Pedometer: Off"
	if u.healthOptions&stepsEnabled != 0 {
		steps = "Pedometer: On"
	}
	clockControl(d, 12, 118, 216, 34, steps, card)
	clockControl(d, 12, 156, 216, 34, "Watch face graph", card)
	if !textVisible(d, &uifont.Regular18, 207, "") && !textVisible(d, &uifont.Regular18, 231, "") {
		return
	}
	var heartBuffer, stepsBuffer fixedDisplayText
	heartText, stepsText := "No heart reading", "Steps: 0 since boot"
	if h := u.health; h != nil {
		if h.attempted {
			heartText = "No stable pulse"
		}
		if h.bpm != 0 {
			text := &heartBuffer
			text.add("Last: ")
			text.number(uint32(h.bpm))
			text.add(" BPM")
			if !h.lastReading.IsZero() {
				minutes := int(max(0, now.Sub(h.lastReading)) / time.Minute)
				text.add(" (")
				if minutes >= 24*60 {
					text.number(uint32(minutes / (24 * 60)))
					text.add("d")
				} else if minutes >= 60 {
					text.number(uint32(minutes / 60))
					text.add("h")
				} else if minutes > 0 {
					text.number(uint32(minutes))
					text.add("m")
				} else {
					text.add("now")
				}
				text.add(")")
			}
			heartText = string(text.data[:text.n])
		}
		if h.heartOn && h.bpm == 0 {
			heartText = "Measuring heart rate..."
		}
		if h.heartFailed {
			heartText = "Heart sensor error"
		}
		text := &stepsBuffer
		text.add("Steps: ")
		text.number(h.steps)
		text.add(" since boot")
		stepsText = string(text.data[:text.n])
		if h.stepsFailed {
			stepsText = "Step sensor error"
		}
		if h.suspended {
			heartText = "Paused for update"
		}
	}
	if u.healthCharging {
		heartText = "Paused on charger"
	}
	centered(d, &uifont.Regular18, 207, heartText, accent)
	centered(d, &uifont.Regular18, 231, stepsText, muted)
}

func (u *watchUI) tickHealth(now time.Time, exclusive bool) {
	paused := exclusive || u.healthCharging
	if u.health == nil {
		if u.healthOptions == 0 || paused {
			return
		}
		u.health = &healthState{device: newHealthDevice()}
	}
	u.health.tick(now, u.healthOptions, paused)
}

func (u *watchUI) stopHealth() {
	if h := u.health; h != nil {
		h.stop(time.Now())
	}
}

func (h *healthState) stop(now time.Time) {
	// Attempt both shutdowns even if a device is disconnected. Leave failed
	// shutdowns marked active so the loop retries rather than assuming power off.
	if h.heartOn {
		if h.device.SetHeart(false) == nil {
			h.heartOn = false
		} else {
			h.heartFailed = true
		}
	}
	if h.stepsOn {
		if !h.stepsFailed {
			h.readSteps(now)
		}
		if h.device.SetSteps(false) == nil {
			h.stepsOn = false
		} else {
			h.stepsFailed = true
		}
	}
}

func (h *healthState) readSteps(now time.Time) {
	count, err := h.device.ReadSteps()
	if err != nil {
		h.stepsFailed = true
		return
	}
	delta := count - h.rawSteps // Unsigned subtraction also handles counter wrap.
	h.steps += delta
	h.recordHealth(now, graphSteps, delta)
	h.rawSteps = count
}

//go:noinline
func (h *healthState) tick(now time.Time, options uint8, exclusive bool) {
	// Radio/touch events can call this much faster than sensor deadlines.
	if options == h.options && exclusive == h.suspended && (h.next.IsZero() || now.Before(h.next)) {
		return
	}
	if options != h.options || exclusive != h.suspended {
		changed := options ^ h.options
		if exclusive != h.suspended {
			h.stop(now)
			changed = 31
		} else {
			// Restart only the subsystem whose own settings changed.
			if changed&15 != 0 && h.heartOn {
				if h.device.SetHeart(false) == nil {
					h.heartOn = false
				} else {
					h.heartFailed = true
				}
			}
			if changed&stepsEnabled != 0 && h.stepsOn {
				if !h.stepsFailed {
					h.readSteps(now)
				}
				if h.device.SetSteps(false) == nil {
					h.stepsOn = false
				} else {
					h.stepsFailed = true
				}
			}
		}
		h.options, h.suspended = options, exclusive
		if changed&15 != 0 {
			h.heartFailed = h.heartOn
			h.measureAt = now
		}
		if changed&stepsEnabled != 0 {
			h.stepsFailed = h.stepsOn
			h.stepsAt = now
		}
		h.revision++
	}
	h.next = time.Time{}
	if exclusive {
		h.stop(now)
		if h.heartOn || h.stepsOn {
			h.next = now.Add(time.Second)
		}
		return
	}
	mode := options & 1
	if mode == heartOff && h.heartOn {
		if h.device.SetHeart(false) == nil {
			h.heartOn = false
		} else {
			h.heartFailed = true
		}
	}
	if mode != heartOff && !h.heartFailed && !h.heartOn && !now.Before(h.measureAt) {
		h.signal.reset()
		err := h.device.SetHeart(true)
		if err != nil {
			h.heartFailed = true
			// A failed initialization can leave the LED enabled. Retry
			// power-down until the bus confirms that it succeeded.
			h.heartOn = true
		} else {
			h.heartOn = true
			h.attempted = false
			h.pulse = pulseDetector{}
			h.sampleAt, h.stopAt = now.Add(heartSamplePeriod), now.Add(heartWindow)
			h.measureAt = now.Add(heartInterval(options))
		}
		h.revision++
	}
	if h.heartOn && mode != heartOff && !h.heartFailed && !now.Before(h.sampleAt) {
		value, err := h.device.ReadHeart()
		if err != nil {
			h.heartFailed = true
		} else {
			ambient := uint32(0)
			if source, ok := h.device.(interface{ HeartAmbient() uint32 }); ok {
				ambient = source.HeartAmbient()
			}
			h.signal.add(value, ambient, now)
			if bpm := h.pulse.sample(value, now); bpm != 0 {
				visibleChange := h.bpm != bpm || h.lastReading.IsZero() || now.Sub(h.lastReading) >= time.Minute
				h.bpm = bpm
				h.lastReading = now
				h.recordHealth(now, graphHeart, uint32(bpm))
				h.signal.accepted++
				if visibleChange {
					h.revision++
				}
			}
		}
		// Anchor to the sampling phase rather than adding work/render time
		// to every interval. Skip missed deadlines instead of burst-reading
		// duplicate ADC samples to catch up.
		h.sampleAt = h.sampleAt.Add(heartSamplePeriod)
		if !h.sampleAt.After(now) {
			h.sampleAt = now.Add(heartSamplePeriod)
		}
	}
	if h.heartOn && (h.heartFailed || mode == heartPeriodic && (h.signal.accepted > 0 || !now.Before(h.stopAt))) {
		h.attempted = true
		if h.device.SetHeart(false) == nil {
			h.heartOn = false
		} else {
			h.heartFailed = true
		}
		h.revision++
	}
	if options&stepsEnabled == 0 && h.stepsOn {
		if !h.stepsFailed {
			h.readSteps(now)
		}
		if h.device.SetSteps(false) == nil {
			h.stepsOn = false
		} else {
			h.stepsFailed = true
		}
	}
	if options&stepsEnabled != 0 && !h.stepsOn && !h.stepsFailed {
		if h.device.SetSteps(true) != nil {
			h.stepsFailed = true
			h.stepsOn = true
			h.stepsAt = now
		} else {
			h.stepsOn, h.rawSteps = true, 0
			h.stepsAt = now
		}
		h.revision++
	}
	if h.stepsOn && !now.Before(h.stepsAt) {
		before, failed := h.steps, h.stepsFailed
		if !h.stepsFailed {
			h.readSteps(now)
		}
		h.stepsAt = now.Add(10 * time.Second)
		if before != h.steps || failed != h.stepsFailed {
			h.revision++
		}
		if h.stepsFailed && h.device.SetSteps(false) == nil {
			h.stepsOn = false
		}
		if h.stepsFailed && h.stepsOn {
			h.stepsAt = now.Add(time.Second)
		}
	}
	schedule := newWakeSchedule(now, noDeadlineDelay)
	pending := false
	if h.heartOn {
		pending = true
		if mode == heartOff || h.heartFailed {
			schedule.after(time.Second)
		} else {
			schedule.by(h.sampleAt)
			if mode == heartPeriodic {
				schedule.by(h.stopAt)
			}
		}
	} else if mode != heartOff && !h.heartFailed {
		pending = true
		schedule.by(h.measureAt)
	}
	if h.stepsOn {
		pending = true
		schedule.by(h.stepsAt)
	}
	if pending {
		h.next = schedule.next
	}
}
