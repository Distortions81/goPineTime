package main

import (
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

const (
	heartOff uint8 = iota
	heartPeriodic
	heartContinuous
)

const stepsEnabled uint8 = 16

// Match InfiniTime's 10 Hz acquisition cadence for the HRS3300 conversion mode.
const heartSamplePeriod = 100 * time.Millisecond
const heartWindow = 30 * time.Second

func heartInterval(options uint8) time.Duration {
	switch options >> 2 & 3 {
	case 1:
		return 15 * time.Minute
	case 2:
		return 30 * time.Minute
	default:
		return 5 * time.Minute
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
		u.healthOptions = u.healthOptions&^3 | ((u.healthOptions&3)+1)%3
	case inRect(e, 12, 80, 228, 114) && u.healthOptions&3 == heartPeriodic:
		u.healthOptions = u.healthOptions&^12 | (((u.healthOptions>>2&3)+1)%3)<<2
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
	switch u.healthOptions & 3 {
	case heartPeriodic:
		mode = "Heart rate: Periodic"
	case heartContinuous:
		mode = "Heart rate: Continuous"
	}
	clockControl(d, 12, 42, 216, 34, mode, card)
	intervalColor := disabled
	if u.healthOptions&3 == heartPeriodic {
		intervalColor = white
	}
	clockControlLabel(d, 12, 80, 216, 34, "Every "+decimal(int(heartInterval(u.healthOptions)/time.Minute))+" minutes", card, intervalColor)
	steps := "Pedometer: Off"
	if u.healthOptions&stepsEnabled != 0 {
		steps = "Pedometer: On"
	}
	clockControl(d, 12, 118, 216, 34, steps, card)
	clockControl(d, 12, 156, 216, 34, "Watch face graph", card)
	heartText, stepsText := "No heart reading", "Steps: 0 since boot"
	if h := u.health; h != nil {
		if h.attempted {
			heartText = "No stable pulse"
		}
		if h.bpm != 0 {
			heartText = "Last: " + decimal(int(h.bpm)) + " BPM"
			if !h.lastReading.IsZero() {
				minutes := int(max(0, now.Sub(h.lastReading)) / time.Minute)
				age := "now"
				if minutes >= 24*60 {
					age = decimal(minutes/(24*60)) + "d"
				} else if minutes >= 60 {
					age = decimal(minutes/60) + "h"
				} else if minutes > 0 {
					age = decimal(minutes) + "m"
				}
				heartText += " (" + age + ")"
			}
		}
		if h.heartOn && h.bpm == 0 {
			heartText = "Measuring heart rate..."
		}
		if h.heartFailed {
			heartText = "Heart sensor error"
		}
		stepsText = "Steps: " + decimal(int(h.steps)) + " since boot"
		if h.stepsFailed {
			stepsText = "Step sensor error"
		}
		if h.suspended {
			heartText = "Paused for update"
		}
	}
	centered(d, &uifont.Regular18, 207, heartText, accent)
	centered(d, &uifont.Regular18, 231, stepsText, muted)
}

func (u *watchUI) tickHealth(now time.Time, exclusive bool) {
	if u.health == nil {
		if u.healthOptions == 0 || exclusive {
			return
		}
		u.health = &healthState{device: newHealthDevice()}
	}
	u.health.tick(now, u.healthOptions, exclusive)
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
	if options != h.options || exclusive != h.suspended {
		h.stop(now)
		h.options, h.suspended = options, exclusive
		h.heartFailed, h.stepsFailed = h.heartOn, h.stepsOn
		h.measureAt, h.stepsAt = now, now
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
	mode := options & 3
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
				h.bpm = bpm
				h.lastReading = now
				h.recordHealth(now, graphHeart, uint32(bpm))
				h.signal.accepted++
				h.revision++
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
	if h.heartOn && (h.heartFailed || mode == heartPeriodic && !now.Before(h.stopAt)) {
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
		if !h.stepsFailed {
			h.readSteps(now)
		}
		h.stepsAt = now.Add(10 * time.Second)
		h.revision++
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
