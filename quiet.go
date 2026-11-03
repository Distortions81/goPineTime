package main

import (
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

// Fits the journal's final two reserved bytes. No notification inbox needs to
// be allocated merely to retain a quiet preference.
const (
	quietManual uint16 = 1 << iota
	quietScheduled
)

const quietConfigured uint16 = 1 << 12

func (u *watchUI) quietStart() int { return int(u.quiet >> 2 & 31) }
func (u *watchUI) quietEnd() int   { return int(u.quiet >> 7 & 31) }

func (u *watchUI) scheduledQuiet(now time.Time) bool {
	// A build seed or approximate reboot handoff must not silently mute alerts.
	if u.quiet&quietScheduled == 0 || !u.clock.initialized || u.clock.approximate {
		return false
	}
	hour := u.clock.Now(now).Hour()
	start, end := u.quietStart(), u.quietEnd()
	if start < end {
		return hour >= start && hour < end
	}
	// Equal endpoints deliberately mean all day.
	return hour >= start || hour < end
}

func (u *watchUI) notificationQuiet(now time.Time) bool {
	return u.quiet&quietManual != 0 || u.scheduledQuiet(now)
}

func (u *watchUI) notificationBuzzDeadline(now time.Time) time.Time {
	end := now.Add(notificationPulse)
	if u.scheduledQuiet(end) {
		// Shorten the existing motor deadline when a pulse crosses the start
		// hour. This needs no separate wakeup at quiet-hour boundaries.
		local := u.clock.Now(end)
		return end.Add(-time.Duration(local.Minute())*time.Minute - time.Duration(local.Second())*time.Second - time.Duration(local.Nanosecond()))
	}
	return end
}

func (u *watchUI) toggleBuzz() {
	if u.quiet&quietManual != 0 {
		u.askBattery(batteryBuzz)
	} else {
		u.quiet |= quietManual
	}
	if u.notifications != nil {
		u.notifications.buzzUntil = time.Time{}
	}
}

func (u *watchUI) handleQuietHours(e inputEvent) {
	if e.Kind == inputSwipeRight || backButtonTapped(e) {
		u.page = pageQuickControls
		return
	}
	if e.Kind != inputTap {
		return
	}
	switch {
	case inRect(e, 12, 42, 228, 82):
		// Give the first opt-in a useful overnight default; zero old records
		// keep their original behavior until this explicit edit.
		if u.quiet&^(quietManual|quietScheduled) == 0 {
			u.quiet |= 22<<2 | 7<<7
		}
		u.quiet |= quietConfigured
		u.quiet ^= quietScheduled
	case inRect(e, 12, 88, 228, 128):
		u.quiet = u.quiet&^(31<<2) | uint16((u.quietStart()+1)%24)<<2
		u.quiet |= quietConfigured
	case inRect(e, 12, 134, 228, 174):
		u.quiet = u.quiet&^(31<<7) | uint16((u.quietEnd()+1)%24)<<7
		u.quiet |= quietConfigured
	}
}

func drawQuietHour(d canvas, y int16, prefix string, hour int) {
	if !controlVisible(d, y, 40, y+27) {
		return
	}
	var text fixedDisplayText
	text.add(prefix)
	text.add(twoDigits(hour))
	text.add(":00")
	clockControl(d, 12, y, 216, 40, string(text.data[:text.n]), card)
}

func (u *watchUI) drawQuietHours(d canvas, now time.Time) {
	centered(d, &uifont.Regular18, 29, "QUIET HOURS", white)
	label := "Schedule: Off"
	if u.quiet&quietScheduled != 0 {
		label = "Schedule: On"
	}
	clockControl(d, 12, 42, 216, 40, label, card)
	drawQuietHour(d, 88, "Start: ", u.quietStart())
	drawQuietHour(d, 134, "End: ", u.quietEnd())
	detail := "Tap time to add 1 hour"
	if u.quiet&quietScheduled != 0 {
		if !u.clock.initialized || u.clock.approximate {
			detail = "Set or sync time first"
		} else if u.quietStart() == u.quietEnd() {
			detail = "Same times: all day"
		} else if u.scheduledQuiet(now) {
			detail = "Quiet hours active"
		}
	}
	centered(d, &uifont.Regular18, 204, detail, muted)
	centered(d, &uifont.Regular18, 232, "Alarms still vibrate", accent)
}
