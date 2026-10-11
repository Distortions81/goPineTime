package main

import (
	"fmt"
	"testing"
	"time"
)

func TestTrailerFlags(t *testing.T) {
	for _, tt := range []struct {
		ok, done uint32
		want     updateState
	}{
		{1, 0xffffffff, firmwareConfirmed},
		{0xffffff01, 0xffffff01, firmwareConfirmed},
		{0xffffffff, 0xffffff01, firmwareTrial},
		{0xffffffff, 1, firmwareTrial},
		{0xffffffff, 0xffffffff, firmwareInvalid},
		{0xffffff00, 1, firmwareInvalid},
		{0xffffff02, 1, firmwareInvalid},
	} {
		if got := stateFromTrailer(tt.ok, tt.done); got != tt.want {
			t.Errorf("ok=%#x done=%#x: got %v want %v", tt.ok, tt.done, got, tt.want)
		}
	}
}

func openUpdate(t *testing.T, u *watchUI, now time.Time) {
	t.Helper()
	power := powerStatus{Percent: 80}
	u.handle(inputEvent{Kind: inputSwipeLeft}, now, firmwareConfirmed, power)
	if u.page != pageSettings {
		t.Fatal("swipe did not open settings")
	}
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 200}, now, firmwareConfirmed, power)
	if u.page != pageUpdate {
		t.Fatal("settings did not open update prompt")
	}
}

func TestUpdateRequiresFreshThreeSecondHold(t *testing.T) {
	now := time.Unix(0, 0)
	power := powerStatus{Percent: 80}
	u := newWatchUI(firmwareConfirmed)
	// Old clock-button position and wake no longer open the updater.
	for _, kind := range []inputKind{inputWake, inputTap} {
		u.handle(inputEvent{Kind: kind, X: 180, Y: 195}, now, firmwareConfirmed, power)
		if u.page != pageClock {
			t.Fatal("clock tap opened updater")
		}
	}
	openUpdate(t, &u, now)
	hold := inputEvent{Kind: inputHold, X: 120, Y: 195, Held: 3 * time.Second}
	if a := u.handle(hold, now.Add(3*time.Second), firmwareConfirmed, power); a != actionNone {
		t.Fatal("unpaired hold started update")
	}
	u.handle(inputEvent{Kind: inputPress, X: 120, Y: 195}, now, firmwareConfirmed, power)
	hold.Held = 2999 * time.Millisecond
	if a := u.handle(hold, now.Add(hold.Held), firmwareConfirmed, power); a != actionNone {
		t.Fatal("short hold started update")
	}
	hold.Held = 3 * time.Second
	if a := u.handle(hold, now.Add(hold.Held), firmwareConfirmed, power); a != actionStartUpdate {
		t.Fatal("complete hold did not start update")
	}
	if a := u.handle(hold, now.Add(4*time.Second), firmwareConfirmed, power); a != actionNone {
		t.Fatal("held finger started another update")
	}
}

func TestHoldCancellation(t *testing.T) {
	now := time.Unix(0, 0)
	power := powerStatus{Percent: 80}
	for _, kind := range []inputKind{inputRelease, inputTap, inputCancel, inputWake, inputSleep, inputSwipeRight} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			u := newWatchUI(firmwareConfirmed)
			openUpdate(t, &u, now)
			u.handle(inputEvent{Kind: inputPress, X: 120, Y: 195}, now, firmwareConfirmed, power)
			u.handle(inputEvent{Kind: kind}, now.Add(time.Second), firmwareConfirmed, power)
			if u.holding {
				t.Fatal("hold not canceled")
			}
			if a := u.handle(inputEvent{Kind: inputHold, X: 120, Y: 195, Held: 4 * time.Second}, now.Add(4*time.Second), firmwareConfirmed, power); a != actionNone {
				t.Fatal("stale hold accepted")
			}
		})
	}
	for _, invalid := range []inputEvent{
		{Kind: inputPress, X: 120, Y: 170},
		{Kind: inputHold, X: 220, Y: 195, Held: time.Second},
	} {
		u := newWatchUI(firmwareConfirmed)
		openUpdate(t, &u, now)
		u.handle(inputEvent{Kind: inputPress, X: 120, Y: 195}, now, firmwareConfirmed, power)
		u.handle(invalid, now, firmwareConfirmed, power)
		if u.holding {
			t.Fatal("outside contact retained hold")
		}
	}
}

func TestUpdateCancelExpiryAndPower(t *testing.T) {
	now := time.Unix(0, 0)
	power := powerStatus{Percent: 80}
	for _, cancel := range []inputEvent{{Kind: inputSwipeRight}, {Kind: inputTap, X: 20, Y: 20}} {
		u := newWatchUI(firmwareConfirmed)
		openUpdate(t, &u, now)
		u.handle(cancel, now, firmwareConfirmed, power)
		if u.page != pageSettings {
			t.Fatal("back did not return to settings")
		}
	}
	u := newWatchUI(firmwareConfirmed)
	openUpdate(t, &u, now)
	u.handle(inputEvent{Kind: inputPress, X: 120, Y: 195}, now.Add(28*time.Second), firmwareConfirmed, power)
	if a := u.handle(inputEvent{Kind: inputHold, X: 120, Y: 195, Held: 3 * time.Second}, now.Add(31*time.Second), firmwareConfirmed, power); a != actionNone || u.page != pageSettings {
		t.Fatal("expired prompt accepted")
	}
	for _, state := range []updateState{firmwareConfirmed, firmwareTrial, firmwareInvalid, firmwareUnavailable} {
		u = newWatchUI(firmwareConfirmed)
		openUpdate(t, &u, now)
		u.handle(inputEvent{Kind: inputPress, X: 120, Y: 195}, now, firmwareConfirmed, power)
		if a := u.handle(inputEvent{Kind: inputHold, X: 120, Y: 195, Held: 3 * time.Second}, now.Add(3*time.Second), state, powerStatus{Percent: 19}); a != actionNone || u.holding {
			t.Fatal("unsafe power/state accepted")
		}
	}
	for _, state := range []updateState{firmwareUnavailable, firmwareInvalid, firmwareTrial} {
		armed := newWatchUI(firmwareConfirmed)
		openUpdate(t, &armed, now)
		armed.handle(inputEvent{Kind: inputPress, X: 120, Y: 195}, now, firmwareConfirmed, power)
		if a := armed.handle(inputEvent{Kind: inputHold, X: 120, Y: 195, Held: 3 * time.Second}, now.Add(3*time.Second), state, power); a != actionNone || armed.holding {
			t.Fatal("changed firmware state accepted with adequate battery")
		}
		u = newWatchUI(state)
		u.handle(inputEvent{Kind: inputSwipeLeft}, now, state, power)
		u.handle(inputEvent{Kind: inputTap, X: 120, Y: 200}, now, state, power)
		if u.page == pageUpdate {
			t.Fatal("unsafe state opened update")
		}
	}
}

func TestSettingsTimeFormatAndKeepLanding(t *testing.T) {
	now := time.Date(2026, 10, 4, 23, 5, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	if u.use24 || u.timeLabel(now) != "11:05" {
		t.Fatal("default is not 12-hour")
	}
	u.handle(inputEvent{Kind: inputSwipeLeft}, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 80}, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 154}, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputSwipeRight}, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputSwipeRight}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageClock || !u.use24 || u.timeLabel(now) != "23:05" {
		t.Fatal("format choice lost on back")
	}
	u.home(firmwareConfirmed)
	u.handle(inputEvent{Kind: inputTap, X: 180, Y: 195}, now, firmwareConfirmed, powerStatus{Percent: 80})
	if u.page != pageClock {
		t.Fatal("extra KEEP-position tap opened update")
	}
}

func TestTouchWakeSettingDefaultsOffAndRepaints(t *testing.T) {
	now := time.Unix(0, 0)
	u := newWatchUI(firmwareConfirmed)
	if u.touchWake {
		t.Fatal("touch wake must default off")
	}
	u.handle(inputEvent{Kind: inputSwipeLeft}, now, firmwareConfirmed, powerStatus{})
	before := u.frameKey(now, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 154}, now, firmwareConfirmed, powerStatus{})
	tap := inputEvent{Kind: inputTap, X: 120, Y: 154}
	u.handle(tap, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
	if !u.touchWake || u.page != pageDisplaySettings || u.frameKey(now, powerStatus{}) == before {
		t.Fatal("toggle did not enable/repaint in settings")
	}
	u.handle(tap, now, firmwareConfirmed, powerStatus{})
	if u.touchWake {
		t.Fatal("second tap did not disable touch wake")
	}
	u.handle(inputEvent{Kind: inputTap, X: 20, Y: 20}, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 20, Y: 20}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageClock {
		t.Fatal("settings back arrow stopped working")
	}
}

func TestTrialConfirmation(t *testing.T) {
	u := newWatchUI(firmwareTrial)
	now := time.Unix(0, 0)
	power := powerStatus{Percent: 80}
	if u.page != pageTrial {
		t.Fatal("trial prompt not shown")
	}
	if a := u.handle(inputEvent{Kind: inputRefresh}, now.Add(24*time.Hour), firmwareTrial, power); a != actionNone || u.page != pageTrial {
		t.Fatal("silence automatically confirmed trial")
	}
	if a := u.handle(inputEvent{Kind: inputTap, X: 50, Y: 100}, now, firmwareTrial, power); a != actionNone {
		t.Fatal("tap outside controls confirmed trial")
	}
	if a := u.handle(inputEvent{Kind: inputTap, X: 60, Y: 195}, now, firmwareTrial, power); a != actionRevert {
		t.Fatal("revert not dispatched")
	}
	if a := u.handle(inputEvent{Kind: inputTap, X: 180, Y: 195}, now, firmwareTrial, power); a != actionKeep {
		t.Fatal("keep not dispatched")
	}
	u.showMessage("confirmation failed")
	u.handle(inputEvent{Kind: inputTap, X: 100, Y: 195}, now, firmwareTrial, power)
	if u.page != pageTrial {
		t.Fatal("error bypassed trial prompt")
	}
}

func TestTouchRejectsWakeDragAndStaleRelease(t *testing.T) {
	now := time.Unix(0, 0)
	down, up := [6]byte{0, 1, 0, 180, 0, 195}, [6]byte{5, 0, 0, 180, 0, 195}
	var tracker touchTracker
	if tracker.decode(up, now).Tap {
		t.Fatal("unpaired release counted as tap")
	}
	tracker.decode(down, now)
	tracker.cancel() // first gesture woke the sleeping screen
	if tracker.decode(up, now.Add(100*time.Millisecond)).Tap {
		t.Fatal("wake gesture approved control")
	}
	tracker.decode(down, now)
	if !tracker.decode(up, now.Add(100*time.Millisecond)).Tap {
		t.Fatal("fresh tap not recognized")
	}
	if tracker.decode(up, now.Add(200*time.Millisecond)).Tap {
		t.Fatal("duplicate gesture approved next screen")
	}
	tracker.decode(down, now)
	drag := down
	drag[3] = 100
	tracker.decode(drag, now.Add(50*time.Millisecond))
	if tracker.decode(up, now.Add(100*time.Millisecond)).Tap {
		t.Fatal("drag became tap")
	}
	tracker.decode(down, now)
	if tracker.decode(up, now.Add(2*time.Second)).Tap {
		t.Fatal("long press became tap")
	}
	tracker.decode(down, now)
	invalid := up
	invalid[3] = 250
	if tracker.decode(invalid, now).Tap || tracker.decode(up, now).Tap {
		t.Fatal("invalid sample retained stale press")
	}
}
