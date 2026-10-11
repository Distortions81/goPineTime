package main

import (
	"github.com/Distortions81/goPine/internal/checkpoint"
	"testing"
	"time"
)

func TestFlippedTouchCoordinatesAndGestures(t *testing.T) {
	for _, kind := range []inputKind{inputTap, inputPress, inputHold, inputRelease, inputSwipeLeft, inputSwipeRight} {
		for _, point := range [][2]int16{{0, 0}, {239, 239}, {17, 205}} {
			original := inputEvent{Kind: kind, X: point[0], Y: point[1], Held: 3 * time.Second}
			got := orientInput(original, true)
			if got.X != 239-original.X || got.Y != 239-original.Y || got.Held != original.Held || orientInput(got, true) != original {
				t.Fatal("rotation does not round-trip", original, got)
			}
			if orientInput(original, false) != original {
				t.Fatal("normal orientation changed input")
			}
		}
	}
	for _, kind := range []inputKind{inputSleep, inputWake, inputPower, inputCancel, inputRefresh, inputQuit} {
		e := inputEvent{Kind: kind}
		if orientInput(e, true) != e {
			t.Fatal("rotated non-touch event")
		}
	}
}
func TestScreenOrientationSavedAndRestored(t *testing.T) {
	flash, j, _ := clockTestStorage(t)
	now := time.Unix(0, 0)
	u := newWatchUI(firmwareConfirmed)
	u.flipScreen, u.touchWake = true, true
	p := loadSettings(&u, now, j)
	if !p.flush(&u, now, true) {
		t.Fatal("save failed")
	}
	reopened, err := checkpoint.Open(flash)
	if err != nil {
		t.Fatal(err)
	}
	restored := newWatchUI(firmwareConfirmed)
	loadSettings(&restored, now, reopened)
	if !restored.flipScreen || !restored.touchWake {
		t.Fatal("orientation or touch wake lost on reboot")
	}
}

type orientationDisplay struct {
	scriptDisplay
	flips []bool
}

func (d *orientationDisplay) SetFlipped(v bool) error { d.flips = append(d.flips, v); return nil }
func TestFlippedLoopRestoresBeforeFirstFrameAndRetainsOnWake(t *testing.T) {
	d := &orientationDisplay{scriptDisplay: *newScriptDisplay()}
	u := newWatchUI(firmwareConfirmed)
	u.flipScreen = true
	u.page = pageDisplaySettings
	d.steps = []scriptStep{
		{event: inputEvent{Kind: inputRefresh}, check: func() {
			if len(d.flips) != 1 || !d.flips[0] {
				t.Fatal("saved rotation not applied before frame")
			}
		}},
		{at: time.Second, event: inputEvent{Kind: inputSleep}},
		{at: 2 * time.Second, event: inputEvent{Kind: inputWake}},
		{at: 3 * time.Second, event: inputEvent{Kind: inputTap, X: 119, Y: 39}}, // logical (120,200), flip off
		{at: 4 * time.Second, event: inputEvent{Kind: inputQuit}, check: func() {
			if u.flipScreen || len(d.flips) != 2 || d.flips[1] {
				t.Fatal("rotated controls or wake persistence failed")
			}
		}},
	}
	l := watchLoop{display: d, ui: &u, sync: &timeSyncController{radio: &fakeTimeRadio{}}, renderer: &frameRenderer{}, now: func() time.Time { return d.now }, state: func() updateState { return firmwareConfirmed }}
	if err := l.run(); err != nil {
		t.Fatal(err)
	}
}
