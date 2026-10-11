package main

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/weather"
)

func weatherPackets(now time.Time) (current, forecast []byte) {
	current, forecast = make([]byte, 53), make([]byte, 36)
	current[1], current[48], forecast[0], forecast[10] = 1, 1, 1, 5
	for _, b := range [][]byte{current, forecast} {
		binary.LittleEndian.PutUint64(b[2:], uint64(calendarMillis(now)/1000))
	}
	binary.LittleEndian.PutUint16(current[10:], 1825)
	binary.LittleEndian.PutUint16(current[12:], 800)
	binary.LittleEndian.PutUint16(current[14:], 2300)
	copy(current[16:], "Denver")
	for i := 0; i < 5; i++ {
		binary.LittleEndian.PutUint16(forecast[11+i*5:], uint16(800+i*100))
		binary.LittleEndian.PutUint16(forecast[13+i*5:], uint16(2300+i*100))
		forecast[15+i*5] = byte(i)
	}
	return
}

func sampleWeather(now time.Time) *weatherState {
	w := &weatherState{currentReceived: now, forecastReceived: now}
	a, b := weatherPackets(now)
	w.cache.Apply(a)
	w.cache.Apply(b)
	return w
}

type fakeWeatherRadio struct {
	fakeTimeRadio
	packets [][]byte
	window  time.Duration
}

func (r *fakeWeatherRadio) StartWeather(_ [10]byte, battery uint8, window time.Duration) error {
	r.starts++
	r.battery, r.window = battery, window
	return r.err
}
func (r *fakeWeatherRadio) TakeWeather() (p [weather.MaxPacket]byte, n int, err error) {
	if r.err != nil {
		return p, 0, r.err
	}
	if len(r.packets) != 0 {
		n = copy(p[:], r.packets[0])
		r.packets = r.packets[1:]
	}
	return
}

func weatherUI(now time.Time) watchUI {
	u := newWatchUI(firmwareConfirmed)
	u.openWeather()
	// Exercise the legacy bounded receiver directly; normal navigation uses Phone.
	u.weather.open, u.weather.status, u.weather.expires = true, "Connect your phone", now.Add(weatherWindow)
	u.page = pageWeatherSync
	return u
}

func TestWeatherUpdateClosesAfterBothRecordsWithoutChangingClock(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	u := weatherUI(now)
	before := u.clock
	a, b := weatherPackets(now)
	r := &fakeWeatherRadio{packets: [][]byte{b, a}}
	c := timeSyncController{radio: r}
	c.update(&u, now, 73)
	if r.starts != 1 || r.stops != 1 || r.window != weatherWindow || c.running || u.weather.open || u.page != pageWeather {
		t.Fatal("window did not finish", r, c, u.page)
	}
	if !u.weather.cache.HasCurrent || !u.weather.cache.HasForecast || u.weather.cache.Current.City() != "Denver" || u.clock != before {
		t.Fatal("cached update changed time or lost data")
	}
	c.update(&u, now.Add(time.Hour), 73)
	if r.starts != 1 {
		t.Fatal("cached weather started background radio")
	}
}

func TestWeatherCancellationTimeoutErrorAndAlert(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, event := range []inputEvent{{Kind: inputSleep}, {Kind: inputSwipeRight}, {Kind: inputTap, X: 20, Y: 20}, {Kind: inputTap, X: 120, Y: 210}} {
		u := weatherUI(now)
		r := &fakeWeatherRadio{}
		c := timeSyncController{radio: r}
		c.update(&u, now, 60)
		u.handle(event, now, firmwareConfirmed, powerStatus{})
		c.update(&u, now, 60)
		if r.stops != 1 || c.running || u.weather.open || u.page != pageWeather {
			t.Fatal("cancel leaked weather radio", event)
		}
	}
	for _, cause := range []string{"timeout", "error", "alarm"} {
		u := weatherUI(now)
		u.weather.cache = sampleWeather(now).cache
		before := u.weather.cache
		r := &fakeWeatherRadio{}
		c := timeSyncController{radio: r}
		c.update(&u, now, 60)
		switch cause {
		case "timeout":
			now = now.Add(weatherWindow)
		case "error":
			r.err = errors.New("radio")
		case "alarm":
			u.page = pageAlert
		}
		c.update(&u, now, 60)
		if r.stops != 1 || c.running || u.weather.open || u.weather.cache != before {
			t.Fatal("failure lost cache or kept radio", cause)
		}
	}
}

func TestWeatherAgesNavigationAndFrameKeys(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	u := newWatchUI(firmwareConfirmed)
	u.openWeather()
	u.weather = sampleWeather(now)
	u.clock.initialized, u.clock.approximate = true, false
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{now, "Received 0 min ago"}, {now.Add(time.Hour), "Received 1 hr ago"},
		{now.Add(24 * time.Hour), "Over 24 hours old"}, {now.Add(-time.Hour), "Received 0 min ago"},
	} {
		if got := weatherAge(now, tc.at); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
	u.clock.approximate = true
	before := u.frameKey(now, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 210, Y: 20}, now, firmwareConfirmed, powerStatus{})
	if !u.weather.fahrenheit || u.frameKey(now, powerStatus{}) == before {
		t.Fatal("unit toggle did not repaint")
	}
	u.handle(inputEvent{Kind: inputTap, X: 60, Y: 210}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageWeatherForecast {
		t.Fatal("forecast navigation")
	}
	u.back(firmwareConfirmed)
	u.back(firmwareConfirmed)
	if u.page != pageApps {
		t.Fatal("back did not return to tools")
	}
	cache := u.weather.cache
	u.handle(inputEvent{Kind: inputTap, X: 177, Y: 197}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageApps || u.weather.open || u.weather.cache != cache {
		t.Fatal("disabled Weather card opened a session or changed cached weather")
	}
	end := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)
	if stamp := forecastDay(end, 0); stamp.Year() != 2027 || stamp.Month() != time.January || stamp.Day() != 1 {
		t.Fatal("forecast must start tomorrow across year boundary", stamp)
	}
}

func TestWeatherStripPixels(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 34, 0, 0, time.UTC)
	for _, pg := range []page{pageWeather, pageWeatherForecast, pageWeatherSync} {
		u := weatherUI(now)
		u.weather.cache = sampleWeather(now).cache
		u.page = pg
		u.clock.initialized, u.clock.approximate = true, false
		d, ref := &memoryDisplay{}, &memoryDisplay{}
		draw := func(c canvas) { u.drawFrame(c, now, powerStatus{Percent: 73}) }
		ref.FillScreen(black)
		draw(ref)
		var r frameRenderer
		if err := r.render(d, draw); err != nil || d.pixels != ref.pixels {
			t.Fatal("weather strip mismatch", pg, err)
		}
	}
}
