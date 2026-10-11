package main

import "time"

const (
	minimumLoopWait      = time.Millisecond
	powerPollInterval    = 5 * time.Second
	noDeadlineDelay      = time.Duration(1<<63 - 1)
	timeSyncPollInterval = 50 * time.Millisecond
)

// wakeSchedule collects deadlines from otherwise independent subsystems. This
// keeps the event loop asleep until the first subsystem needs service and gives
// timers and alarms one place to add their deadlines later.
type wakeSchedule struct {
	now  time.Time
	next time.Time
}

func newWakeSchedule(now time.Time, firstDelay time.Duration) wakeSchedule {
	return wakeSchedule{now: now, next: now.Add(firstDelay)}
}

func (s *wakeSchedule) by(deadline time.Time) {
	if deadline.Before(s.next) {
		s.next = deadline
	}
}

func (s *wakeSchedule) after(delay time.Duration) {
	s.by(s.now.Add(delay))
}

func (s wakeSchedule) delay() time.Duration {
	return max(s.next.Sub(s.now), minimumLoopWait)
}

func nextLoopDelay(now time.Time, u *watchUI, nextPower time.Time, awake bool) time.Duration {
	schedule := newWakeSchedule(now, noDeadlineDelay)
	if u.updateExclusive() {
		schedule.by(nextPower)
		schedule.by(u.transfer.expires)
		schedule.after(50 * time.Millisecond)
		return schedule.delay()
	}
	if awake {
		schedule.by(nextPower)
		schedule.after(nextMinuteDelay(u.clock.Now(now)))
	}
	if awake && u.page == pageUpdate {
		schedule.by(u.expires)
	}
	if awake && u.page == pageTimeSync {
		schedule.after(timeSyncPollInterval)
	}
	if awake && u.weather != nil && u.weather.open {
		schedule.by(u.weather.expires)
		schedule.after(timeSyncPollInterval)
	}
	if u.phone != nil && u.phone.mode == phoneSession {
		schedule.by(u.phone.expires)
	}
	if u.phone != nil && u.phone.mode != phoneOff && now.Before(u.phone.retryAt) {
		schedule.by(u.phone.retryAt)
	}
	if u.phoneSetupActive(now) {
		schedule.by(u.phone.setupUntil)
	}
	u.scheduleTimers(&schedule, awake)
	if n := u.notifications; n != nil && now.Before(n.buzzUntil) {
		schedule.by(n.buzzUntil)
	}
	return schedule.delay()
}
