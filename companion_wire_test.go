package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/music"
	"github.com/Distortions81/goPine/internal/notifications"
)

// Shared with the real-host ATT test. These are synthetic source-derived
// InfiniLink payloads, not a captured phone session or proof of iOS behavior.
func companionWireFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	data, err := os.ReadFile("testdata/infinilink_wire.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceRevision string            `json:"source_revision"`
		Packets        map[string]string `json:"packets"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SourceRevision != "60abe2d2c67aff67726855374385a499d9353a94" {
		t.Fatal("unexpected companion reference")
	}
	packets := make(map[string][]byte)
	for name, encoded := range fixture.Packets {
		packets[name], err = hex.DecodeString(encoded)
		if err != nil {
			t.Fatal(name, err)
		}
	}
	return packets
}

func TestInfiniLinkWireValuesReachSharedPhoneFeatures(t *testing.T) {
	packets := companionWireFixtures(t)
	for _, calendar := range []string{"time_9", "time_10"} {
		t.Run(calendar, func(t *testing.T) {
			now := time.Date(2026, 10, 5, 11, 0, 0, 0, time.UTC)
			u := phoneUI(now, phoneConnected)
			u.page = pageWeather
			r := &fakeNotificationRadio{}
			r.size = copy(r.fakeTimeRadio.value[:], packets[calendar])
			r.fakeWeatherRadio.packets = [][]byte{packets["current_v0"], packets["current_v1"], packets["forecast"]}
			r.packets = [][]byte{packets["alert"]}
			copy(r.value[:40], packets["track"])
			copy(r.value[40:80], packets["artist"])
			r.value[80], r.value[81], r.value[82] = packets["status"][0], music.LinkMusicReady, 1
			r.generation = 42
			binary.LittleEndian.PutUint32(r.value[83:], r.generation)
			r.changed = true
			c := timeSyncController{radio: r}
			c.update(&u, now, 80)
			stamp := time.Date(2026, 10, 5, 12, 34, 56, 0, time.UTC)
			if !u.clock.Now(now).Equal(stamp) || u.clock.approximate || u.syncStatus != "Time synchronized" {
				t.Fatal("InfiniLink calendar did not set clock", u.clock.Now(now), u.syncStatus)
			}
			if u.weather == nil || !u.weather.cache.HasCurrent || u.weather.cache.Current.City() != "Denver" ||
				u.weather.cache.Current.Temp != 1200 || u.weather.cache.Current.Low != -200 || u.weather.cache.Current.High != 1800 {
				t.Fatal("InfiniLink current weather did not reach cache", u.weather)
			}
			if u.phone.status != "Connected" || music.Text(&u.phone.music.Track) != "Test track" ||
				music.Text(&u.phone.music.Artist) != "Test artist" || !u.phone.music.Playing {
				t.Fatal("InfiniLink metadata did not reach music", u.phone)
			}
			if u.notifications == nil || u.notifications.inbox.Count != 1 ||
				notifications.Text(u.notifications.inbox.At(0).Title[:]) != "Calendar" ||
				notifications.Text(u.notifications.inbox.At(0).Body[:]) != "Review at noon" {
				t.Fatal("InfiniLink alert did not reach inbox", u.notifications)
			}
			c.update(&u, now.Add(time.Second), 80)
			forecast := u.weather.cache.Forecast
			if !u.weather.cache.HasForecast || forecast.Count != 5 || forecast.Days[0].Low != -200 ||
				forecast.Days[4].High != 1500 || forecast.Days[4].Icon != 2 {
				t.Fatal("InfiniLink forecast did not reach cache", forecast)
			}
			if !c.running || r.starts != 1 || r.stops != 0 || u.sync.Open || u.page != pageWeather {
				t.Fatal("feature delivery replaced shared connection", c.running, r.starts, r.stops, u.page)
			}
			u.openMusic()
			u.handlePhone(inputEvent{Kind: inputTap, X: 120, Y: 150}, now.Add(2*time.Second), firmwareConfirmed)
			c.update(&u, now.Add(2*time.Second), 80)
			if len(r.commands) != 2 || r.commands[0] != music.Open || r.commands[1] != music.Pause ||
				r.starts != 1 || r.stops != 0 {
				t.Fatal("music command did not use shared connection", r.commands, r.starts, r.stops)
			}
		})
	}
}
