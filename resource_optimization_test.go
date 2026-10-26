package main

import (
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/checkpoint"
)

func TestCalendarMillisPreservesCivilFields(t *testing.T) {
	for _, seconds := range []int{-12 * 3600, -7 * 3600, 0, 37, 5*3600 + 45*60, 14 * 3600} {
		zone := time.FixedZone("test", seconds)
		for i := 0; i < 1000; i++ {
			local := time.Unix(-2208988800+int64(i)*7333333, int64(i)*9876543).In(zone)
			want := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC).UnixMilli()
			if got := calendarMillis(local); got != want {
				t.Fatalf("offset %d, %v: got %d, want %d", seconds, local, got, want)
			}
		}
	}
	zone, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	for _, month := range []time.Month{time.March, time.November} {
		for hour := 0; hour < 240; hour++ {
			local := time.Date(2026, month, 1, hour, 0, 0, 999999999, time.UTC).In(zone)
			want := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC).UnixMilli()
			if calendarMillis(local) != want {
				t.Fatalf("DST projection changed: %v", local)
			}
		}
	}
}

func TestUnstampedRuntimeDoesNotAllocateRecoveryState(t *testing.T) {
	now := time.Unix(0, 0)
	u := newWatchUI(firmwareConfirmed)
	r := checkpoint.Runtime{StopwatchRunning: true, StopwatchMillis: 12345,
		CountdownRunning: true, CountdownMillis: 5000}
	if got := testing.AllocsPerRun(20, func() { u.loadRuntime(r, now) }); got != 0 {
		t.Fatalf("unstamped duration restore allocated %g objects", got)
	}
	if u.timers.resume != nil || u.timers.watch.elapsed(now) != 12345*time.Millisecond ||
		u.timers.countdown.left(now) != 5*time.Second {
		t.Fatal("duration-only restore retained recovery state or changed timers")
	}
	r.Active, r.AlertMillis = true, 60000
	u.loadRuntime(r, now)
	if u.timers.resume != nil {
		t.Fatal("expired unstamped alert retained recovery state")
	}
	r.AlertMillis = 1234
	u.loadRuntime(r, now)
	if u.timers.resume == nil || !u.timers.resume.alertPending || u.timers.resume.alertElapsed != 1234*time.Millisecond {
		t.Fatal("live restored alert lost its remaining duration")
	}
}

var benchmarkMillis int64

func BenchmarkCalendarMillis(b *testing.B) {
	local := time.Date(2026, 10, 25, 12, 34, 56, 789123456, time.FixedZone("test", -6*3600))
	b.Run("FieldsReference", func(b *testing.B) {
		for b.Loop() {
			benchmarkMillis = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC).UnixMilli()
		}
	})
	b.Run("Offset", func(b *testing.B) {
		for b.Loop() {
			benchmarkMillis = calendarMillis(local)
		}
	})
}
