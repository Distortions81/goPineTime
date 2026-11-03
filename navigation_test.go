package main

import (
	"testing"
	"time"
)

func TestSharedBackButtonDrawingAndNavigation(t *testing.T) {
	now := time.Unix(100, 0)
	for _, tc := range []struct{ from, to page }{
		{pageSettings, pageClock}, {pageApps, pageClock}, {pageUpdate, pageSettings}, {pageMessage, pageSettings},
		{pageTimeSettings, pageSettings}, {pageSetTime, pageTimeSettings}, {pageSetDate, pageTimeSettings}, {pageTimeSync, pageTimeSettings},
		{pageAlarms, pageApps}, {pageAlarmEdit, pageAlarms}, {pageAlarmRepeat, pageAlarms}, {pageStopwatch, pageApps}, {pageCountdown, pageApps}, {pageCountdownEdit, pageCountdown},
		{pageWeather, pageApps}, {pageWeatherForecast, pageWeather}, {pageWeatherSync, pageWeather},
		{pageMusic, pageApps}, {pagePhone, pageSettings}, {pageInbox, pageApps}, {pageNotification, pageInbox},
		{pageTransfer, pageSettings}, {pageDisplaySettings, pageSettings}, {pagePairingSettings, pagePhone}, {pageBatteryConfirm, pageDisplaySettings}, {pageHealth, pageApps},
		{pageWatchGraph, pageHealth},
		{pageHeartSignal, pageHealth},
		{pageQuickControls, pageClock}, {pageQuietHours, pageQuickControls}, {pageBatteryHistory, pageClock},
		{pageBatteryUsage, pageBatteryHistory},
	} {
		u := newWatchUI(firmwareConfirmed)
		u.page = tc.from
		u.phone = samplePhone(now)
		u.weather = sampleWeather(now)
		u.notifications = sampleNotifications()
		u.pairing = &pairingState{}
		u.batteryChoice = batteryTouch
		u.transfer = &transferState{open: true, phase: updateWaiting}
		d := &memoryDisplay{}
		u.drawFrame(d, now, powerStatus{})
		if d.pixels[24*240+16] == black {
			t.Fatal("page omitted shared back arrow", tc.from)
		}
		u.handle(inputEvent{Kind: inputTap, X: 25, Y: 24}, now, firmwareConfirmed, powerStatus{})
		if u.page != tc.to {
			t.Fatal("back button destination", tc.from, u.page, tc.to)
		}
		if tc.from == pageTransfer && u.transfer.open {
			t.Fatal("back skipped transfer cancellation")
		}
		if tc.from == pageBatteryConfirm && u.touchWake {
			t.Fatal("back applied unconfirmed battery choice")
		}
	}
}

func TestSharedBackButtonOverlayAndTrackingCleanup(t *testing.T) {
	now := time.Unix(100, 0)
	u := newWatchUI(firmwareConfirmed)
	u.openHealth()
	u.healthOptions = heartPeriodic | stepsEnabled
	u.handle(inputEvent{Kind: inputTap, X: 25, Y: 24}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageApps || u.healthOptions != heartPeriodic|stepsEnabled {
		t.Fatal("back cancelled background health tracking")
	}
	u.page = pagePhone
	u.pairing = &pairingState{code: 1}
	u.handle(inputEvent{Kind: inputTap, X: 25, Y: 24}, now, firmwareConfirmed, powerStatus{})
	if !u.pairing.cancel || u.page != pagePhone {
		t.Fatal("pairing back did not cancel overlay")
	}
	u.pairing = nil
	u.showPowerNotice()
	u.handle(inputEvent{Kind: inputTap, X: 25, Y: 24}, now, firmwareConfirmed, powerStatus{})
	if u.powerNotice || u.page != pagePhone {
		t.Fatal("charging back navigated underneath overlay")
	}
}

func TestRecentHeartReadingAgeRepaints(t *testing.T) {
	now := time.Unix(120, 0)
	u := watchUI{page: pageHealth, health: &healthState{bpm: 72, lastReading: now}}
	before := u.frameKey(now, powerStatus{})
	if before == u.frameKey(now.Add(time.Minute), powerStatus{}) {
		t.Fatal("heart reading age would remain stale on screen")
	}
}
