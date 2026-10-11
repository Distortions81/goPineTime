package main

import "time"

const alarmCount = 5
const countdownSource = alarmCount
const alertDuration = time.Minute
const alertPulseDuration = 200 * time.Millisecond
const alertPulsePeriod = time.Second

type alarmRepeat uint8

const (
	alarmOnce alarmRepeat = iota
	alarmDaily
	alarmWeekdays
)

type alarm struct {
	hour, minute int
	repeat       alarmRepeat
	enabled      bool
	next         time.Time // Civil time, unlike duration-based timers below.
}

func (a *alarm) schedule(local time.Time) {
	a.next = time.Time{}
	if !a.enabled {
		return
	}
	due := time.Date(local.Year(), local.Month(), local.Day(), a.hour, a.minute, 0, 0, local.Location())
	for !due.After(local) || (a.repeat == alarmWeekdays && (due.Weekday() == time.Saturday || due.Weekday() == time.Sunday)) {
		due = due.AddDate(0, 0, 1)
	}
	a.next = due
}

type stopwatch struct {
	running    bool
	started    time.Time
	saved, lap time.Duration
}

func (s stopwatch) elapsed(now time.Time) time.Duration {
	if s.running {
		return s.saved + max(0, now.Sub(s.started))
	}
	return s.saved
}
func (s *stopwatch) toggle(now time.Time) {
	if s.running {
		s.saved = s.elapsed(now)
	} else {
		s.started = now
	}
	s.running = !s.running
}

type countdown struct {
	preset, remaining time.Duration
	deadline          time.Time
	running           bool
}

func (c countdown) left(now time.Time) time.Duration {
	if c.running {
		return max(0, c.deadline.Sub(now))
	}
	return c.remaining
}
func (c *countdown) toggle(now time.Time) {
	if c.running {
		c.remaining = c.left(now)
		c.running = false
		return
	}
	if c.remaining <= 0 {
		c.remaining = c.preset
	}
	if c.remaining > 0 {
		c.deadline = now.Add(c.remaining)
		c.running = true
	}
}
func (c *countdown) reset() { c.running = false; c.remaining = c.preset }

type timerState struct {
	alarms           [alarmCount]alarm
	snooze           [alarmCount]time.Time // Runtime deadlines: unaffected by clock edits.
	watch            stopwatch
	countdown        countdown
	pending          uint8
	active           bool
	source           int
	ringSince        time.Time
	clockOffset      time.Duration
	clockInitialized bool
	resume           *timerResume
}

func newTimerState() timerState {
	t := timerState{countdown: countdown{preset: 5 * time.Minute, remaining: 5 * time.Minute}}
	for i := range t.alarms {
		t.alarms[i] = alarm{hour: 7, repeat: alarmDaily}
	}
	return t
}

// Rebase wall-clock alarms after a manual time/date change. Do not ring all
// the alarms skipped by a forward clock change or move duration-based timers.
func (t *timerState) reschedule(local time.Time) {
	for i := range t.alarms {
		t.alarms[i].schedule(local)
	}
}

func (t *timerState) poll(now, local time.Time, clockInitialized bool) {
	for i := range t.alarms {
		a := &t.alarms[i]
		if clockInitialized && a.enabled && !a.next.IsZero() && !local.Before(a.next) {
			t.pending |= 1 << uint(i)
			if a.repeat == alarmOnce {
				a.enabled = false
			}
			a.schedule(local)
		}
		if !t.snooze[i].IsZero() && !now.Before(t.snooze[i]) {
			t.pending |= 1 << uint(i)
			t.snooze[i] = time.Time{}
		}
	}
	if t.countdown.running && !now.Before(t.countdown.deadline) {
		t.countdown.running = false
		t.countdown.remaining = 0
		t.pending |= 1 << countdownSource
	}
}

func (u *watchUI) tickTimers(now time.Time, state updateState) bool {
	if u.updateExclusive() {
		return false
	}
	t := &u.timers
	u.reconcileTimers(now)
	local := u.clock.Now(now)
	if t.clockOffset != u.clock.offset || t.clockInitialized != u.clock.initialized {
		t.reschedule(local)
		t.clockOffset = u.clock.offset
		t.clockInitialized = u.clock.initialized
	}
	for i := range t.alarms {
		if u.clock.initialized && t.alarms[i].enabled && t.alarms[i].next.IsZero() {
			t.alarms[i].schedule(local)
		}
	}
	t.poll(now, local, u.clock.initialized)
	if t.active && now.Sub(t.ringSince) >= alertDuration {
		t.active = false
		u.home(state)
	}
	if t.active || t.pending == 0 {
		return false
	}
	for i := 0; i <= countdownSource; i++ {
		if t.pending&(1<<uint(i)) != 0 {
			t.pending &^= 1 << uint(i)
			t.source, t.active, t.ringSince = i, true, now
			if r := t.resume; r != nil && r.alertPending && int(r.saved.Source) == i {
				t.ringSince = now.Add(-r.alertElapsed)
				r.alertPending = false
			}
			u.cancelHold()
			u.powerNotice = false
			u.sync.Cancel()
			u.page = pageAlert // Interrupt/discard drafts and any armed updater hold.
			return true
		}
	}
	return false
}

func (u *watchUI) dismissAlert(now time.Time, snooze bool, state updateState) {
	if snooze && u.timers.active && u.timers.source < alarmCount {
		u.timers.snooze[u.timers.source] = now.Add(5 * time.Minute)
	}
	u.timers.active = false
	u.home(state)
}

func (t *timerState) vibrating(now time.Time) bool {
	return t.active && now.Sub(t.ringSince) < alertDuration &&
		max(0, now.Sub(t.ringSince))%alertPulsePeriod < alertPulseDuration
}

func (u *watchUI) scheduleTimers(s *wakeSchedule, awake bool) {
	t, now := &u.timers, s.now
	for i := range t.alarms {
		if a := &t.alarms[i]; u.clock.initialized && a.enabled && !a.next.IsZero() {
			s.after(a.next.Sub(u.clock.Now(now)))
		}
		if !t.snooze[i].IsZero() {
			s.by(t.snooze[i])
		}
	}
	if t.countdown.running {
		s.by(t.countdown.deadline)
		if awake && u.page == pageCountdown {
			// The rounded-up display changes at this next remaining-second boundary.
			left := t.countdown.left(now)
			s.after((left-1)%time.Second + 1)
		}
	}
	if awake && u.page == pageStopwatch && t.watch.running {
		s.after(100*time.Millisecond - t.watch.elapsed(now)%(100*time.Millisecond))
	}
	if t.active {
		s.by(t.ringSince.Add(alertDuration))
		phase := max(0, now.Sub(t.ringSince)) % alertPulsePeriod
		if phase < alertPulseDuration {
			s.after(alertPulseDuration - phase)
		} else {
			s.after(alertPulsePeriod - phase)
		}
	}
}
