package main

import (
	"testing"
	"time"
)

func TestClockMenuHintsSupportBothTapAndSwipe(t *testing.T) {
	for _, tc := range []struct {
		e    inputEvent
		want page
	}{
		{inputEvent{Kind: inputTap, X: 55, Y: 57}, pageApps},
		{inputEvent{Kind: inputSwipeRight}, pageApps},
		{inputEvent{Kind: inputTap, X: 180, Y: 57}, pageSettings},
		{inputEvent{Kind: inputSwipeLeft}, pageSettings},
		{inputEvent{Kind: inputTap, X: 120, Y: 57}, pageClock},  // Gap between hints.
		{inputEvent{Kind: inputTap, X: 120, Y: 130}, pageClock}, // Time itself.
	} {
		u := newWatchUI(firmwareConfirmed)
		u.handle(tc.e, time.Unix(100, 0), firmwareConfirmed, powerStatus{})
		if u.page != tc.want {
			t.Fatal("menu hint destination", tc.e, u.page, tc.want)
		}
	}
	u := newWatchUI(firmwareTrial)
	u.handle(inputEvent{Kind: inputTap, X: 55, Y: 57}, time.Unix(100, 0), firmwareTrial, powerStatus{})
	if u.page != pageTrial {
		t.Fatal("hint bypassed trial confirmation")
	}
}
