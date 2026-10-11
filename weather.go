package main

import (
	"time"

	"github.com/Distortions81/goPine/internal/timesync"
	"github.com/Distortions81/goPine/internal/uifont"
	"github.com/Distortions81/goPine/internal/weather"
)

const weatherWindow = time.Minute

type weatherRadio interface {
	StartWeather([10]byte, uint8, time.Duration) error
	TakeWeather() ([weather.MaxPacket]byte, int, error)
}

type weatherState struct {
	cache                             weather.Cache
	expires                           time.Time
	currentReceived, forecastReceived time.Time
	status                            string
	revision                          uint32
	received                          uint8
	open                              bool
	fahrenheit                        bool
}

func (u *watchUI) openWeather() {
	if u.weather == nil {
		u.weather = &weatherState{}
	}
	u.page = pageWeather
}

func weatherPage(p page) bool {
	return p == pageWeather || p == pageWeatherForecast || p == pageWeatherSync
}

func (u *watchUI) handleWeather(e inputEvent, now time.Time, state updateState) {
	w := u.weather
	if w == nil {
		u.openWeather()
		return
	}
	if e.Kind == inputSleep || e.Kind == inputCancel || e.Kind == inputSwipeRight ||
		(e.Kind == inputTap && inRect(e, 0, 0, 60, 44)) {
		w.open = false
		if e.Kind != inputCancel && e.Kind != inputSleep {
			u.back(state)
		} else if u.page == pageWeatherSync {
			u.page = pageWeather
		}
		return
	}
	if e.Kind != inputTap {
		return
	}
	if u.page == pageWeatherSync {
		if inRect(e, 16, 188, 224, 232) {
			w.open = false
			u.page = pageWeather
		}
		return
	}
	if inRect(e, 188, 0, 240, 44) {
		w.fahrenheit = !w.fahrenheit
	} else if inRect(e, 12, 188, 114, 232) {
		if u.page == pageWeather {
			u.page = pageWeatherForecast
		} else {
			u.page = pageWeather
		}
	}
}

// Keep weather's temporary packet/cache frames out of the time-sync path.
//
//go:noinline
func (c *timeSyncController) updateWeather(u *watchUI, now time.Time, battery uint8) {
	w := u.weather
	if w == nil || !w.open || u.page != pageWeatherSync {
		if w != nil {
			w.open = false
		}
		c.close()
		return
	}
	if !now.Before(w.expires) {
		w.open, w.status = false, "Update timed out"
		c.close()
		return
	}
	radio, ok := c.radio.(weatherRadio)
	if !ok {
		w.open, w.status = false, "Bluetooth unavailable"
		c.close()
		return
	}
	if !c.running {
		if radioBusy(c.radio) {
			return
		}
		value, _ := timesync.Encode(u.clock.Now(now))
		if err := radio.StartWeather(value, battery, weatherWindow); err != nil {
			c.radio.Stop()
			w.open, w.status = false, "Bluetooth unavailable"
			return
		}
		c.running, c.weatherMode = true, true
	}
	// At most one current record and one forecast per event-loop pass.
	for i := 0; i < 2; i++ {
		packet, n, err := radio.TakeWeather()
		if err != nil {
			w.open, w.status = false, "Bluetooth error"
			c.close()
			return
		}
		if n == 0 {
			break
		}
		if n < 0 || n > len(packet) || !w.cache.Apply(packet[:n]) {
			w.status = "Invalid weather data"
			continue
		}
		w.revision++
		w.received |= 1 << packet[0]
		if packet[0] == 0 {
			w.currentReceived = now
		} else {
			w.forecastReceived = now
		}
		w.status = "Weather received"
	}
	if w.received == 3 {
		w.open = false
		u.page = pageWeather
		c.close()
	}
}

func weatherAgeText(received, now time.Time) (text [32]byte, n int) {
	// InfiniLink sends UTC seconds although the protocol says local civil
	// seconds. Report receipt age without guessing a timezone.
	age := max(0, now.Sub(received))
	if age >= 24*time.Hour {
		n = copy(text[:], "Over 24 hours old")
		return
	}
	value, suffix := int(age/time.Minute), " min ago"
	if age >= time.Hour {
		value, suffix = int(age/time.Hour), " hr ago"
	}
	n = copy(text[:], "Received ")
	if value >= 10 {
		text[n] = byte('0' + value/10)
		n++
	}
	text[n] = byte('0' + value%10)
	n++
	n += copy(text[n:], suffix)
	return
}

func weatherAge(received, now time.Time) string {
	text, n := weatherAgeText(received, now)
	return string(text[:n])
}

func drawWeatherAge(d canvas, received, now time.Time, y int16) {
	if !textVisible(d, &uifont.Regular18, y, "") {
		return
	}
	text, n := weatherAgeText(received, now)
	centered(d, &uifont.Regular18, y, string(text[:n]), muted)
}

func forecastDay(local time.Time, index int) time.Time {
	// Companion forecasts exclude today. Use the local receipt date rather
	// than interpreting the companion's ambiguous timestamp timezone.
	return local.AddDate(0, 0, index+1)
}

func (u *watchUI) drawWeather(d canvas, now time.Time) {
	w := u.weather
	centered(d, &uifont.Bold18, 29, "WEATHER", white)
	if w == nil {
		return
	}
	unit := "C"
	if w.fahrenheit {
		unit = "F"
	}
	if u.page != pageWeatherSync {
		writeLine(d, &uifont.Bold18, 203, 29, unit, accent)
	}
	if u.page == pageWeatherSync {
		centered(d, &uifont.Regular18, 68, "Connect to InfiniTime", white)
		centered(d, &uifont.Regular18, 100, w.status, accent)
		if w.open {
			centered(d, &uifont.Regular18, 134, decimal(int(max(0, w.expires.Sub(now)+time.Second-1)/time.Second))+" seconds left", muted)
		}
		centered(d, &uifont.Regular18, 167, "Send weather in app", muted)
		clockControl(d, 16, 188, 208, 44, "DONE", card)
		return
	}
	if u.page == pageWeather {
		if !w.cache.HasCurrent {
			centered(d, &uifont.Regular18, 94, "No weather yet", white)
			centered(d, &uifont.Regular18, 129, "Settings > Bluetooth", muted)
		} else {
			v := &w.cache.Current
			if textVisible(d, &uifont.Regular18, 60, "") {
				city := v.City()
				for len(city) > 0 {
					// Bound to the actual font width, rather than a character count.
					width, _ := lineWidth(&uifont.Regular18, city)
					if width <= 216 {
						break
					}
					city = city[:len(city)-1]
				}
				centered(d, &uifont.Regular18, 60, city, muted)
			}
			centered(d, &uifont.Bold18, 93, decimal(weather.Degrees(v.Temp, w.fahrenheit))+" "+unit, accent)
			centered(d, &uifont.Regular18, 121, weather.Condition(v.Icon), white)
			centered(d, &uifont.Regular18, 148, "Lo "+decimal(weather.Degrees(v.Low, w.fahrenheit))+" / Hi "+decimal(weather.Degrees(v.High, w.fahrenheit)), muted)
			drawWeatherAge(d, w.currentReceived, now, 176)
		}
		clockControl(d, 12, 188, 102, 44, "5 DAYS", card)
	} else {
		v := &w.cache.Forecast
		if !w.cache.HasForecast || v.Count == 0 {
			centered(d, &uifont.Regular18, 105, "No forecast yet", muted)
		} else {
			for i := 0; i < int(v.Count); i++ {
				y := int16(61 + i*25)
				if !textVisible(d, &uifont.Regular18, y, "") {
					continue
				}
				label := "Day " + decimal(i+1)
				if u.clock.initialized && !u.clock.approximate {
					stamp := forecastDay(u.clock.Now(w.forecastReceived), i)
					label = stamp.Weekday().String()[:3] + " " + decimal(stamp.Day())
				}
				day := v.Days[i]
				writeLine(d, &uifont.Regular18, 12, y, label, muted)
				writeLine(d, &uifont.Regular18, 100, y, decimal(weather.Degrees(day.Low, w.fahrenheit))+" / "+decimal(weather.Degrees(day.High, w.fahrenheit))+" "+unit, white)
			}
			drawWeatherAge(d, w.forecastReceived, now, 182)
		}
		clockControl(d, 12, 188, 102, 44, "NOW", card)
	}
}
