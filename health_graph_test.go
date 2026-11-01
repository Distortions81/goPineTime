package main

import (
	"testing"
	"time"
)

func sampleGraphHealth(now time.Time) *healthState {
	h := &healthState{bpm: 76, steps: 4321, lastReading: now}
	for i := 0; i < 60; i++ {
		at := now.Add(time.Duration(i-59) * time.Minute)
		h.recordHealth(at, graphHeart, uint32(65+(i%24)))
		steps := uint32(0)
		if i > 12 && i < 30 || i > 48 {
			steps = uint32(30 + i%10*5)
		}
		h.recordHealth(at, graphSteps, steps)
	}
	return h
}

func TestHealthHistoryMinuteBucketsAndExpiry(t *testing.T) {
	now := time.Unix(6000, 0)
	h := healthState{}
	h.recordHealth(now, graphHeart, 72)
	h.recordHealth(now.Add(time.Second), graphHeart, 74)
	h.recordHealth(now, graphSteps, 2)
	h.recordHealth(now.Add(10*time.Second), graphSteps, 3)
	minute := now.Unix() / 60
	if value, ok := h.history.value(minute, graphHeart); !ok || value != 74 {
		t.Fatal("heart bucket did not retain latest reading", value, ok)
	}
	if value, ok := h.history.value(minute, graphSteps); !ok || value != 5 {
		t.Fatal("step bucket did not sum actual deltas", value, ok)
	}
	if _, ok := h.history.value(minute+1, graphSteps); ok {
		t.Fatal("missing tracking minute was fabricated")
	}
	revision := h.history.revision
	h.recordHealth(now, graphHeart, 74)
	h.recordHealth(now, graphSteps, 0)
	if h.history.revision != revision {
		t.Fatal("unchanged readings force clock redraws")
	}
	h.recordHealth(now.Add(64*time.Minute), graphHeart, 80)
	if _, ok := h.history.value(minute, graphHeart); ok {
		t.Fatal("ring overwrite retained expired reading")
	}
	if _, ok := h.history.value(minute+64, graphSteps); ok {
		t.Fatal("new heart bucket inherited old step count")
	}
	h.recordHealth(now.Add(64*time.Minute), graphSteps, 0)
	if value, ok := h.history.value(minute+64, graphSteps); !ok || value != 0 {
		t.Fatal("genuine zero-step minute was discarded")
	}
}

func TestWatchGraphOptionsDoNotEnableTracking(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	u.openHealth()
	u.handleHealth(inputEvent{Kind: inputTap, X: 120, Y: 170})
	if u.page != pageWatchGraph {
		t.Fatal("fourth Health button did not open graph options")
	}
	for _, tc := range []struct {
		y    int16
		want uint8
	}{{65, graphOff}, {115, graphHeart}, {165, graphSteps}} {
		u.handleWatchGraph(inputEvent{Kind: inputTap, X: 120, Y: tc.y})
		if u.watchGraph != tc.want || u.healthOptions != 0 {
			t.Fatal("graph selection altered tracking", u.watchGraph, u.healthOptions)
		}
	}
	u.handleWatchGraph(inputEvent{Kind: inputTap, X: 25, Y: 24})
	if u.page != pageHealth {
		t.Fatal("graph back did not return to Health")
	}
}

func TestClockGraphHistoryInvalidationAndNoFlashPolling(t *testing.T) {
	now := time.Unix(6000, 0)
	u := watchUI{page: pageClock, watchGraph: graphHeart, clock: watchClock{initialized: true}, health: &healthState{}}
	before := u.frameKey(now, powerStatus{})
	u.health.recordHealth(now, graphHeart, 72)
	if before == u.frameKey(now, powerStatus{}) {
		t.Fatal("new graph data did not invalidate clock frame")
	}
	key := u.frameKey(now, powerStatus{})
	u.health.revision++ // A sensor status or unchanged BPM does not change the graph.
	if key != u.frameKey(now, powerStatus{}) {
		t.Fatal("unrelated health status repaints clock graph")
	}
	u.health.recordHealth(now, graphHeart, 72)
	if key != u.frameKey(now, powerStatus{}) {
		t.Fatal("same minute/same BPM repaints clock graph")
	}
}
