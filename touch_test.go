package main

import (
	"testing"
	"time"
)

func touchData(down bool, x, y int16) [6]byte {
	data := [6]byte{0, 0, byte(x >> 8), byte(x), byte(y >> 8), byte(y)}
	if down {
		data[1] = 1
	}
	return data
}

func TestTouchHoldRequiresFreshStationaryContact(t *testing.T) {
	now := time.Unix(0, 0)
	var tracker touchTracker
	data := touchData(true, 120, 195)
	if e := tracker.decode(data, now); e.Kind != inputPress {
		t.Fatal("no press")
	}
	for i := 1; i <= 150; i++ {
		e := tracker.decode(data, now.Add(time.Duration(i)*20*time.Millisecond))
		if e.Kind != inputHold || e.Held != time.Duration(i)*20*time.Millisecond {
			t.Fatalf("bad hold %+v", e)
		}
	}
	if e := tracker.decode(touchData(false, 120, 195), now.Add(3*time.Second)); e.Kind != inputRelease || e.Tap {
		t.Fatal("hold release became tap")
	}
	tracker.decode(data, now)
	if e := tracker.decode(data, now.Add(time.Second)); e.Kind == inputHold {
		t.Fatal("missing samples counted toward hold")
	}
	if e := tracker.decode(data, now.Add(1020*time.Millisecond)); e.Kind == inputHold {
		t.Fatal("lost hold rearmed without release")
	}
	tracker.decode(touchData(false, 120, 195), now.Add(1040*time.Millisecond))
	if e := tracker.decode(data, now.Add(1060*time.Millisecond)); e.Kind != inputPress {
		t.Fatal("new contact did not rearm")
	}
	if e := tracker.decode(touchData(true, 150, 195), now.Add(1080*time.Millisecond)); e.Kind != inputCancel {
		t.Fatal("drag did not cancel hold")
	}
	if e := tracker.decode(data, now.Add(1100*time.Millisecond)); e.Kind == inputHold {
		t.Fatal("dragging back resumed hold")
	}
}

func TestTouchSwipeAndWakeSuppression(t *testing.T) {
	now := time.Unix(0, 0)
	for _, tt := range []struct {
		from, to int16
		want     inputKind
	}{{200, 40, inputSwipeLeft}, {40, 200, inputSwipeRight}, {120, 100, inputRelease}} {
		var tracker touchTracker
		tracker.decode(touchData(true, tt.from, 120), now)
		if e := tracker.decode(touchData(false, tt.to, 120), now.Add(200*time.Millisecond)); e.Kind != tt.want {
			t.Fatalf("swipe: %+v", e)
		}
		if e := tracker.decode(touchData(false, tt.to, 120), now.Add(220*time.Millisecond)); e.Kind != inputRelease {
			t.Fatal("stale swipe")
		}
		tracker.decode(touchData(true, tt.from, 120), now)
		tracker.cancel()
		if e := tracker.decode(touchData(false, tt.to, 120), now.Add(200*time.Millisecond)); e.Kind != inputRelease {
			t.Fatal("wake gesture navigated")
		}
	}
}

func TestAutomaticWakeAllowsFreshTapButSuppressesExistingContact(t *testing.T) {
	now := time.Unix(0, 0)
	var tracker touchTracker
	tracker.cancelContact()
	if e := tracker.decode(touchData(true, 120, 210), now); e.Kind != inputPress {
		t.Fatal("idle wake swallowed fresh press")
	}
	if e := tracker.decode(touchData(false, 120, 210), now.Add(100*time.Millisecond)); e.Kind != inputTap {
		t.Fatal("idle wake swallowed fresh tap")
	}
	tracker.decode(touchData(true, 120, 210), now.Add(time.Second))
	tracker.cancelContact()
	if e := tracker.decode(touchData(false, 120, 210), now.Add(1100*time.Millisecond)); e.Kind == inputTap {
		t.Fatal("held contact dismissed new alert")
	}
	tracker.cancel() // An existing input fault still requires a release.
	tracker.cancelContact()
	if e := tracker.decode(touchData(true, 120, 210), now.Add(2*time.Second)); e.Kind == inputPress {
		t.Fatal("wake rearmed a lost contact")
	}
}

func TestVerticalSwipesRequireFreshContactAndSurviveQueue(t *testing.T) {
	now := time.Unix(0, 0)
	for _, tc := range []struct {
		from, to int16
		want     inputKind
	}{{40, 200, inputSwipeDown}, {200, 40, inputSwipeUp}, {100, 125, inputRelease}} {
		var tracker touchTracker
		tracker.decode(touchData(true, 120, tc.from), now)
		e := tracker.decode(touchData(false, 120, tc.to), now.Add(200*time.Millisecond))
		if e.Kind != tc.want {
			t.Fatal("vertical swipe decoded incorrectly", e)
		}
		if tc.want != inputRelease {
			flipped := orientInput(e.inputEvent, true)
			if flipped.Kind == e.Kind || orientInput(flipped, true) != e.inputEvent {
				t.Fatal("vertical swipe did not rotate")
			}
		}
		var queue touchQueue
		queue.push(touchEvent{Activity: true, inputEvent: inputEvent{Kind: inputHold}})
		queue.push(e)
		if queue.n != 1 || queue.pop().Kind != tc.want {
			t.Fatal("vertical release retained stale hold or lost swipe")
		}
		tracker.decode(touchData(true, 120, tc.from), now)
		tracker.cancel()
		if e := tracker.decode(touchData(false, 120, tc.to), now.Add(200*time.Millisecond)); e.Kind != inputRelease {
			t.Fatal("suppressed wake contact opened controls", e)
		}
	}
}
