package main

import (
	"errors"
	"time"

	"github.com/Distortions81/goPine/internal/timesync"
	"github.com/Distortions81/goPine/internal/uifont"
)

type timeRadio interface {
	Start([10]byte, uint8) error
	Stop()
	Service()
	Take() ([10]byte, int, time.Duration, error)
}

// All methods run on the UI goroutine. Between strips and during input waits,
// service only controller timing; defer host callbacks to the shallow loop.
var activeTimeRadio timeRadio

func serviceTimeRadio() {
	if activeTimeRadio != nil {
		if r, ok := activeTimeRadio.(interface{ ServiceController() }); ok {
			r.ServiceController()
		} else {
			activeTimeRadio.Service()
		}
	}
}

// A closed UI window may still have an asynchronous disconnect in flight.
// Only the hardware bridge can tell us when longer sleep is safe.
func timeRadioNeedsService() bool {
	if activeTimeRadio == nil {
		return false
	}
	if radio, ok := activeTimeRadio.(interface{ Busy() bool }); ok {
		return radio.Busy()
	}
	return true
}

type timeSyncController struct {
	radio       timeRadio
	running     bool
	weatherMode bool
	phoneMode   phoneMode
	directMode  bool
	saving      bool
	allowPhone  func() bool
}

func (c *timeSyncController) close() {
	if c.running {
		c.radio.Stop()
		c.running = false
	}
	c.weatherMode = false
	c.phoneMode = phoneOff
	c.directMode = false
}

func (c *timeSyncController) update(u *watchUI, now time.Time, battery uint8) {
	// Security and peer GATT work can use deep C call chains. Run the pump
	// before entering the larger music/weather/time packet processing frames.
	c.radio.Service()
	if c.running {
		if r, ok := c.radio.(interface{ UpdateBattery(uint8) }); ok {
			r.UpdateBattery(min(battery, 100))
		}
	}
	// A completed asynchronous shutdown must wake this controller so an
	// explicitly requested replacement can start even with the screen asleep.
	if r, ok := c.radio.(interface{ AcknowledgeUpdates() }); ok {
		r.AcknowledgeUpdates()
	}
	if c.directMode {
		return
	}
	if c.saving || u.page == pageUpdate || u.page == pageTransfer || u.page == pageTrial || (c.allowPhone != nil && !c.allowPhone()) {
		c.close()
		if u.phone != nil {
			u.phone.clearLink()
		}
		return
	}
	if u.phoneAuto {
		if u.phone == nil {
			u.phone = &phoneState{status: "Waiting for phone"}
		}
		u.phone.mode = phoneConnected
	}
	weatherOpen := u.page == pageWeatherSync && u.weather != nil && u.weather.open
	if p := u.phone; p != nil {
		if p.restart || (c.running && c.phoneMode != p.mode) {
			c.close()
			p.clearLink()
			p.restart = false
		}
		if p.mode != phoneOff {
			u.sync.Cancel()
			if u.weather != nil {
				u.weather.open = false
			}
			c.updatePhone(u, now, battery)
			return
		}
	}

	if c.running && c.weatherMode != weatherOpen {
		c.close()
	}
	if weatherOpen {
		u.sync.Cancel()
		c.updateWeather(u, now, battery)
		return
	}
	if u.weather != nil {
		u.weather.open = false
	}
	if u.page != pageTimeSync {
		u.sync.Cancel()
		c.close()
		return
	}
	if u.sync.Expire(now) {
		u.syncStatus = "Sync timed out"
	}
	if !u.sync.Open || u.sync.Pending {
		c.close()
		return
	}
	if !c.running {
		if radioBusy(c.radio) {
			return
		}
		value, err := timesync.Encode(u.clock.Now(now))
		// An unset/build-seeded clock must not prevent receiving correct time.
		if err != nil {
			value = [10]byte{}
		}
		err = c.radio.Start(value, battery)
		if err != nil {
			c.radio.Stop()
			u.sync.Cancel()
			u.syncStatus = "Bluetooth unavailable"
			return
		}
		c.running = true
	}
	value, size, age, err := c.radio.Take()
	if err != nil {
		c.close()
		u.sync.Cancel()
		u.syncStatus = "Bluetooth error"
		return
	}
	if size == 0 {
		return
	}
	if size < 0 || size > len(value) || age < 0 || age >= timesync.Window {
		err = errors.New("invalid time report")
	} else {
		value, err = timesync.Normalize(value[:size])
		if err == nil {
			err = u.sync.Offer(now, value[:])
		}
		if err == nil {
			u.sync.Received = now.Add(-age)
		}
	}
	c.close()
	if err != nil {
		u.sync.Cancel()
		u.syncStatus = "Invalid time received"
	}
}

func (u *watchUI) handleTimeSync(e inputEvent, now time.Time) {
	if u.sync.Expire(now) {
		u.syncStatus = "Sync timed out"
	}
	if e.Kind == inputSleep || e.Kind == inputSwipeRight ||
		(e.Kind == inputTap && (inRect(e, 0, 0, 60, 44) || inRect(e, 12, 188, 114, 232))) {
		u.sync.Cancel()
		u.page = pageTimeSettings
		return
	}
	if e.Kind == inputTap && inRect(e, 126, 188, 228, 232) && u.sync.Pending {
		stamp, err := u.sync.Accept(now)
		if err == nil {
			u.clock.Set(now, stamp)
			u.syncStatus = "Time synchronized"
		}
	}
}

func (u *watchUI) drawTimeSync(d canvas, now time.Time) {
	centered(d, &uifont.Regular18, 29, "BLUETOOTH TIME", white)
	if u.sync.Pending {
		centered(d, &uifont.Regular18, 69, "Use received time?", muted)
		stamp := u.sync.Proposed.Add(now.Sub(u.sync.Received))
		clock, date := syncTimeDigits(stamp), syncDateDigits(stamp)
		centered(d, &uifont.Regular18, 105, string(clock[:]), accent)
		centered(d, &uifont.Regular18, 133, string(date[:]), white)
		centered(d, &uifont.Regular18, 166, "Check before accepting", muted)
		clockControl(d, 126, 188, 102, 44, "ACCEPT", positive)
	} else if u.sync.Open {
		centered(d, &uifont.Regular18, 64, "InfiniLink Developer", white)
		centered(d, &uifont.Regular18, 91, "Force ANCS: OFF", accent)
		centered(d, &uifont.Regular18, 121, "Connect to InfiniTime", white)
		centered(d, &uifont.Regular18, 150, "Then confirm here", muted)
		seconds := max(0, int((u.sync.Expires.Sub(now)+time.Second-1)/time.Second))
		centered(d, &uifont.Regular18, 177, "Waiting "+decimal(seconds)+"s", accent)
	} else {
		centered(d, &uifont.Regular18, 106, u.syncStatus, white)
		centered(d, &uifont.Regular18, 138, "Bluetooth window closed", muted)
	}
	clockControl(d, 12, 188, 102, 44, "BACK", card)
}

// Deadline access is also safe during the final interrupt-masked sleep handoff.
func timeRadioIdleDelay() time.Duration {
	if r, ok := activeTimeRadio.(interface{ IdleDelay() time.Duration }); ok {
		return r.IdleDelay()
	}
	if timeRadioNeedsService() {
		return activeRadioInterval
	}
	return idleMaxWait
}
func timeRadioHasUpdate() bool {
	if r, ok := activeTimeRadio.(interface{ HasUpdate() bool }); ok {
		return r.HasUpdate()
	}
	return false
}
