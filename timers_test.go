package main

import (
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

func TestAppsGridNavigationAndGaps(t *testing.T) {
	now := time.Unix(0, 0)
	for _, tc := range []struct {
		name string
		x, y int16
		want page
	}{
		{"Alarms", 63, 73, pageAlarms},
		{"Stopwatch", 177, 73, pageStopwatch},
		{"Timer", 63, 135, pageCountdown},
		{"Music", 177, 135, pageMusic},
		{"Messages", 63, 197, pageInbox},
		{"Weather disabled", 177, 197, pageApps},
		{"column gap", 120, 73, pageApps},
		{"first row gap", 63, 104, pageApps},
		{"second row gap", 177, 166, pageApps},
		{"left margin", 11, 73, pageApps},
		{"right margin", 228, 73, pageApps},
		{"bottom margin", 63, 226, pageApps},
		{"old inbox shortcut", 195, 25, pageApps},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := newWatchUI(firmwareConfirmed)
			u.page = pageApps
			u.handle(inputEvent{Kind: inputTap, X: tc.x, Y: tc.y}, now, firmwareConfirmed, powerStatus{})
			if u.page != tc.want {
				t.Fatalf("opened page %d, want %d", u.page, tc.want)
			}
			if u.weather != nil || u.phoneAuto || u.sync.Open || (u.phone != nil && u.phone.mode != phoneOff) {
				t.Fatal("Apps navigation started a connection or weather session")
			}
			if tc.want != pageApps {
				u.handle(inputEvent{Kind: inputSwipeRight}, now, firmwareConfirmed, powerStatus{})
				if u.page != pageApps {
					t.Fatal("app back gesture did not return to grid")
				}
			}
		})
	}
}

func TestAppsGridLabelsFitAndDrawingDoesNotAllocate(t *testing.T) {
	for index := 0; index < 6; index++ {
		width, _ := lineWidth(&uifont.Regular18, appCardLabel(index))
		if width > uint32(appCardWidth-8) {
			t.Fatalf("%s label width %d leaves less than four pixels padding", appCardLabel(index), width)
		}
	}
	u := newWatchUI(firmwareConfirmed)
	u.page = pageApps
	now := time.Unix(0, 0)
	d := &benchmarkDisplay{}
	var renderer frameRenderer
	draw := func(c canvas) { u.drawFrame(c, now, powerStatus{}) }
	if err := renderer.render(d, draw); err != nil {
		t.Fatal(err)
	}
	if got := testing.AllocsPerRun(20, func() {
		u.frameKey(now, powerStatus{})
		if err := renderer.render(d, draw); err != nil {
			panic(err)
		}
	}); got != 0 {
		t.Fatalf("Apps grid allocated %g times per frame", got)
	}
}

func TestStopwatchPauseLapAndResume(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	u.page = pageStopwatch
	now := time.Unix(0, 0)
	tap := func(x int16, at time.Duration) { u.handleTimerTap(inputEvent{X: x, Y: 205}, now.Add(at)) }
	tap(60, 0)
	tap(180, 1250*time.Millisecond)
	tap(60, 2*time.Second)
	if u.timers.watch.elapsed(now.Add(time.Hour)) != 2*time.Second || u.timers.watch.lap != 1250*time.Millisecond {
		t.Fatal("pause/lap lost elapsed time")
	}
	tap(60, time.Hour)
	tap(60, time.Hour+time.Second)
	if u.timers.watch.saved != 3*time.Second {
		t.Fatal("resume counted paused time")
	}
	tap(180, time.Hour+time.Second)
	if u.timers.watch != (stopwatch{}) {
		t.Fatal("reset retained state")
	}
}

func TestCountdownPauseEditAndReset(t *testing.T) {
	now := time.Unix(0, 0)
	c := countdown{preset: 5 * time.Minute, remaining: 5 * time.Minute}
	c.toggle(now)
	c.toggle(now.Add(time.Minute))
	if c.left(now.Add(time.Hour)) != 4*time.Minute {
		t.Fatal("paused countdown advanced")
	}
	c.toggle(now.Add(time.Hour))
	if c.left(now.Add(time.Hour+time.Minute)) != 3*time.Minute {
		t.Fatal("resume lost remainder")
	}
	c.reset()
	if c.running || c.remaining != 5*time.Minute {
		t.Fatal("reset failed")
	}
	u := newWatchUI(firmwareConfirmed)
	u.page = pageCountdownEdit
	u.handleTimerTap(inputEvent{X: 180, Y: 210}, now)
	if u.page != pageCountdownEdit {
		t.Fatal("accepted zero countdown")
	}
	u.edit = clockEdit{hour: 1, minute: 2, day: 3}
	u.handleTimerTap(inputEvent{X: 180, Y: 210}, now)
	if u.timers.countdown.preset != time.Hour+2*time.Minute+3*time.Second || u.page != pageCountdown {
		t.Fatal("duration save failed")
	}
}

func TestAlarmCalendarAndClockEdits(t *testing.T) {
	friday := time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC)
	a := alarm{hour: 7, enabled: true, repeat: alarmWeekdays}
	a.schedule(friday)
	if a.next.Weekday() != time.Monday || a.next.Day() != 12 {
		t.Fatal("weekday alarm did not skip weekend", a.next)
	}
	a.repeat = alarmDaily
	a.schedule(friday)
	if a.next.Day() != 10 {
		t.Fatal("daily alarm skipped a day")
	}
	u := newWatchUI(firmwareConfirmed)
	u.timers.alarms[0] = alarm{hour: 8, enabled: true, repeat: alarmDaily}
	u.timers.countdown = countdown{running: true, deadline: friday.Add(5 * time.Minute)}
	u.timers.watch.toggle(friday)
	u.tickTimers(friday, firmwareConfirmed)
	u.clock.Set(friday.Add(time.Second), friday.Add(2*time.Hour))
	u.tickTimers(friday.Add(time.Second), firmwareConfirmed)
	if u.timers.active || u.timers.alarms[0].next.Day() != 10 {
		t.Fatal("clock jump rang skipped alarm")
	}
	if u.timers.countdown.left(friday.Add(time.Second)) != 299*time.Second || u.timers.watch.elapsed(friday.Add(time.Second)) != time.Second {
		t.Fatal("civil edit changed duration timers")
	}
}

func TestQueuedAlarmsAndTimeout(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	now := time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC)
	u.timers.pending = 1 | 1<<countdownSource
	if !u.tickTimers(now, firmwareConfirmed) || u.timers.source != 0 {
		t.Fatal("first alert missing")
	}
	if !u.tickTimers(now.Add(alertDuration), firmwareConfirmed) || u.timers.source != countdownSource {
		t.Fatal("pending timer was lost at timeout")
	}
	u.tickTimers(now.Add(2*alertDuration), firmwareConfirmed)
	if u.timers.active || u.page != pageClock || u.timers.vibrating(now.Add(2*alertDuration)) {
		t.Fatal("alert failed to stop")
	}
}

func TestAlarmEditorAndDraftCancellation(t *testing.T) {
	now := time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.page = pageAlarms
	tap := func(x, y int16) {
		u.handle(inputEvent{Kind: inputTap, X: x, Y: y}, now, firmwareConfirmed, powerStatus{})
	}
	tap(60, 150)  // Edit alarm.
	tap(60, 70)   // Increment hour.
	tap(180, 210) // Next.
	tap(120, 100) // Daily -> weekdays.
	tap(180, 210) // Save.
	a := u.timers.alarms[0]
	if u.page != pageAlarms || !a.enabled || a.hour != 8 || a.repeat != alarmWeekdays || a.next.Hour() != 8 {
		t.Fatal("alarm editor did not save", a)
	}
	for _, p := range []page{pageAlarmEdit, pageAlarmRepeat, pageCountdownEdit} {
		u.page = p
		u.handle(inputEvent{Kind: inputSleep}, now, firmwareConfirmed, powerStatus{})
		if u.page == p || u.timers.alarms[0] != a {
			t.Fatal("sleep applied draft")
		}
	}
}
