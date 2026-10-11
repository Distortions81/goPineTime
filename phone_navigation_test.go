package main

import (
	"testing"
	"time"
)

func TestPairingHasOnlyOneEntryInSettings(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		origin page
		x, y   int16
	}{
		{"settings", pageSettings, 120, 108}, {"time", pageTimeSettings, 120, 200},
		{"weather", pageWeather, 180, 210}, {"forecast", pageWeatherForecast, 180, 210},
		{"music header", pageMusic, 200, 25}, {"music status", pageMusic, 120, 55}, {"inbox", pageInbox, 200, 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := newWatchUI(firmwareConfirmed)
			u.openMusic()
			u.openInbox()
			u.weather = sampleWeather(now)
			u.page = tc.origin
			p, cached := u.phone, u.weather.cache
			u.handle(inputEvent{Kind: inputTap, X: tc.x, Y: tc.y}, now, firmwareConfirmed, powerStatus{})
			want := tc.origin
			if tc.origin == pageSettings {
				want = pagePhone
			}
			if u.page != want || u.phone != p {
				t.Fatal("unexpected pairing navigation", u.page)
			}
			if u.phoneAuto || u.phone.mode != phoneOff || u.sync.Open || u.weather.open || u.weather.cache != cached {
				t.Fatal("feature navigation changed connection or cache")
			}
			if tc.origin == pageSettings {
				u.back(firmwareConfirmed)
				if u.page != pageSettings {
					t.Fatal("Phone must return to Settings")
				}
			}
		})
	}
}

func TestFeatureNavigationPreservesSharedConnection(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	u := phoneUI(now, phoneConnected)
	u.phoneAuto = true
	u.phone.announced = true
	u.weather = sampleWeather(now)
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r, running: true, phoneMode: phoneConnected}
	for _, pg := range []page{pageWeather, pageWeatherForecast, pageMusic, pageInbox, pageTimeSettings, pageClock} {
		u.page = pg
		c.update(&u, now, 80)
		if r.starts != 0 || r.stops != 0 || !c.running || !u.phoneAuto || !u.phone.announced {
			t.Fatal("feature navigation disturbed the connection", pg)
		}
	}
}

func TestSettingsRowsRemainIndependentlyReachable(t *testing.T) {
	now := time.Unix(0, 0)
	for _, tc := range []struct {
		y    int16
		want page
	}{{62, pageTimeSettings}, {108, pagePhone}, {154, pageDisplaySettings}, {200, pageUpdate}} {
		u := newWatchUI(firmwareConfirmed)
		u.page = pageSettings
		u.handle(inputEvent{Kind: inputTap, X: 120, Y: tc.y}, now, firmwareConfirmed, powerStatus{Percent: 80})
		if u.page != tc.want {
			t.Fatalf("settings row at y=%d opened %d, want %d", tc.y, u.page, tc.want)
		}
	}
}

func TestPhoneInfoAndBackPreserveSharedConnection(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneConnected)
	u.phoneAuto = true
	u.openPhone()
	r := &fakePhoneRadio{}
	c := timeSyncController{radio: r, running: true, phoneMode: phoneConnected}
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 158}, now, firmwareConfirmed, powerStatus{})
	if u.page != pagePairingSettings {
		t.Fatal("Paired devices did not open pairing information")
	}
	c.update(&u, now, 80)
	u.handle(inputEvent{Kind: inputSwipeRight}, now, firmwareConfirmed, powerStatus{})
	if u.page != pagePhone {
		t.Fatal("pairing info back skipped Phone")
	}
	u.handle(inputEvent{Kind: inputSwipeRight}, now, firmwareConfirmed, powerStatus{})
	c.update(&u, now, 80)
	if u.page != pageSettings || !c.running || !u.phoneAuto || r.starts != 0 || r.stops != 0 {
		t.Fatal("info navigation disturbed the shared connection or original feature")
	}
}
