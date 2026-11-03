package main

import (
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/checkpoint"
	"github.com/Distortions81/goPine/internal/notifications"
)

func TestQuietHoursCivilClockBoundaries(t *testing.T) {
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("local", -7*3600)} {
		for start := 0; start < 24; start++ {
			for end := 0; end < 24; end++ {
				u := newWatchUI(firmwareConfirmed)
				u.quiet = quietScheduled | uint16(start)<<2 | uint16(end)<<7
				now := time.Date(2026, 11, 3, 0, 0, 0, 0, zone)
				// Runtime and civil clock deliberately differ by seven hours.
				u.clock.Set(now, now.Add(7*time.Hour))
				for minute := 0; minute < 24*60; minute++ {
					stamp := now.Add(time.Duration(minute) * time.Minute)
					civil := (minute + 7*60) % (24 * 60)
					distance := (civil - start*60 + 24*60) % (24 * 60)
					duration := (end - start + 24) % 24 * 60
					want := duration == 0 || distance < duration
					if u.notificationQuiet(stamp) != want {
						t.Fatalf("zone %v hours %d-%d civil minute %d", zone, start, end, civil)
					}
				}
			}
		}
	}
}

func TestQuietHoursNeedsTrustedClockAndManualAlwaysWorks(t *testing.T) {
	now := time.Date(2026, 11, 3, 23, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.quiet = quietScheduled | 22<<2 | 7<<7
	u.clock = watchClock{}
	if u.notificationQuiet(now) {
		t.Fatal("unset clock muted notifications")
	}
	u.clock = watchClock{initialized: true, approximate: true}
	if u.notificationQuiet(now) {
		t.Fatal("build seed or reboot estimate muted notifications")
	}
	u.quiet |= quietManual
	if !u.notificationQuiet(now) {
		t.Fatal("manual quiet depends on clock validity")
	}
}

func TestQuietHoursBothTransportsPreserveMessagesAndAlarm(t *testing.T) {
	for _, apple := range []bool{false, true} {
		now := time.Date(2026, 11, 3, 23, 0, 0, 0, time.UTC)
		u := newWatchUI(firmwareConfirmed)
		u.clock.Set(now, now)
		u.quiet = quietScheduled | 22<<2 | 7<<7
		if apple {
			r := &appleTestRadio{packets: [][notifications.ApplePacket]byte{appleTestPacket(1, 0, 0)}}
			u.receiveAppleNotifications(r, now)
		} else {
			r := &fakeNotificationRadio{packets: [][]byte{notificationPacket("Quiet message")}}
			u.receiveNotifications(r, now)
		}
		if u.unreadNotifications() != 1 || u.notificationVibrating(now) {
			t.Fatal("quiet hours discarded or buzzed a message", apple)
		}
		end := now.Add(8 * time.Hour)
		if u.notificationVibrating(end) {
			t.Fatal("quiet backlog buzzed at end of schedule")
		}
		u.timers.alarms[0] = alarm{hour: 23, minute: 1, repeat: alarmDaily, enabled: true}
		u.tickTimers(now, firmwareConfirmed)
		if !u.tickTimers(now.Add(time.Minute), firmwareConfirmed) || !u.timers.vibrating(now.Add(time.Minute)) {
			t.Fatal("quiet hours silenced alarm")
		}
	}
}

func TestQuietPreferencePersistsWithoutInboxOrPeriodicWrites(t *testing.T) {
	f, j, _ := clockTestStorage(t)
	now := time.Date(2026, 11, 3, 21, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.clock.Set(now, now)
	p := loadSettings(&u, now, j)
	u.quiet = quietManual | quietScheduled | 22<<2 | 7<<7
	if !p.flush(&u, now, true) {
		t.Fatal("quiet preference did not save")
	}
	reopened, err := checkpoint.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	v := newWatchUI(firmwareConfirmed)
	loadSettings(&v, now, reopened)
	if v.quiet != u.quiet || v.notifications != nil || v.phone != nil || v.health != nil {
		t.Fatal("quiet restore lost preference or allocated optional features")
	}
	writes := f.writes
	for i := 0; i <= 48*60; i++ {
		stamp := now.Add(time.Duration(i) * time.Minute)
		u.notificationQuiet(stamp)
		u.recordBattery(stamp, powerStatus{Millivolts: 3900})
		p.update(&u, stamp, true)
		if got := nextLoopDelay(stamp, &u, stamp.Add(time.Second), false); got != noDeadlineDelay {
			t.Fatal("quiet hours or battery history added sleeping wakeups", got)
		}
	}
	if f.writes != writes {
		t.Fatal("quiet transitions or battery samples wrote flash")
	}
}

func TestNotificationPulseStopsAtQuietStartWithoutBoundaryPolling(t *testing.T) {
	now := time.Date(2026, 11, 3, 21, 59, 59, 950000000, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.clock.Set(now, now)
	u.quiet = quietScheduled | 22<<2 | 7<<7
	r := &fakeNotificationRadio{packets: [][]byte{notificationPacket("Just before quiet hours")}}
	u.receiveNotifications(r, now)
	start := now.Add(50 * time.Millisecond)
	if !u.notificationVibrating(now) || u.notifications.buzzUntil != start {
		t.Fatal("notification pulse crossed quiet-hour boundary")
	}
	if got := nextLoopDelay(now, &u, now.Add(time.Second), false); got != 50*time.Millisecond {
		t.Fatal("sleeping motor deadline missed quiet start", got)
	}
	if u.notificationVibrating(start) {
		t.Fatal("motor remained on at quiet start")
	}
	if got := nextLoopDelay(start, &u, start.Add(time.Second), false); got != noDeadlineDelay {
		t.Fatal("quiet schedule retained a sleeping boundary deadline", got)
	}
}

func TestQuietPagesRepaintOnlyWhenVisibleStatusChanges(t *testing.T) {
	now := time.Date(2026, 11, 3, 21, 58, 0, 0, time.UTC)
	for _, p := range []page{pageQuickControls, pageQuietHours, pageInbox} {
		u := newWatchUI(firmwareConfirmed)
		u.openInbox()
		u.page = p
		u.clock.Set(now, now)
		u.quiet = quietScheduled | 22<<2 | 7<<7
		before := u.frameKey(now, powerStatus{})
		if before != u.frameKey(now.Add(time.Minute), powerStatus{}) {
			t.Fatal("unchanged quiet status caused a minute redraw", p)
		}
		if before == u.frameKey(now.Add(2*time.Minute), powerStatus{}) {
			t.Fatal("quiet boundary did not repaint visible status", p)
		}
		u.quiet |= quietManual
		before = u.frameKey(now, powerStatus{})
		changed := before != u.frameKey(now.Add(2*time.Minute), powerStatus{})
		if changed != (p == pageQuietHours) {
			t.Fatal("manual quiet hid schedule status or caused an unnecessary redraw", p)
		}
	}
}

func TestQuietMidnightAllDayChoiceSurvivesScheduleToggle(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	u.quiet = quietScheduled | quietConfigured // Explicit all-day 00:00–00:00.
	tap := inputEvent{Kind: inputTap, X: 120, Y: 62}
	u.handleQuietHours(tap)
	u.handleQuietHours(tap)
	if u.quiet != quietScheduled|quietConfigured || u.quietStart() != 0 || u.quietEnd() != 0 {
		t.Fatal("re-enabling explicit midnight schedule replaced it with overnight defaults")
	}
}
