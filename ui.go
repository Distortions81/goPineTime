package main

import (
	"image/color"
	"strings"
	"time"
	"unicode"

	"github.com/Distortions81/goPine/internal/gfx"
	"github.com/Distortions81/goPine/internal/timesync"
	"github.com/Distortions81/goPine/internal/uifont"
	"tinygo.org/x/tinyfont"
)

type page uint8

const (
	pageClock page = iota
	pageSettings
	pageUpdate
	pageTrial
	pageMessage
	pageTimeSettings
	pageSetTime
	pageSetDate
	pageTimeSync
	pageApps
	pageAlarms
	pageAlarmEdit
	pageAlarmRepeat
	pageStopwatch
	pageCountdown
	pageCountdownEdit
	pageAlert
	pageWeather
	pageWeatherForecast
	pageWeatherSync
	pageMusic
	pagePhone
	pageInbox
	pageNotification
	pageTransfer
	pageDisplaySettings
	pagePairing
	pagePairingSettings
	pageCharging // Frame identity for the full-screen power notice overlay.
	pageBatteryConfirm
)

type uiAction uint8

const (
	actionNone uiAction = iota
	actionStartUpdate
	actionKeep
	actionRevert
	actionInstallUpdate
)

const updateHoldDuration = 3 * time.Second

type watchUI struct {
	page          page
	expires       time.Time
	message       string
	use24         bool
	touchWake     bool
	flipScreen    bool
	lightMode     bool
	brightness    uint8
	batteryChoice batteryChoice
	phoneAuto     bool
	holding       bool
	holdSince     time.Time
	holdStep      int
	clock         watchClock
	edit          clockEdit
	sync          timesync.Session
	syncStatus    string
	timers        timerState
	alarmIndex    int
	editRepeat    alarmRepeat
	settingsNote  string
	weather       *weatherState
	phone         *phoneState
	notifications *notificationState
	transfer      *transferState
	powerNotice   bool
	pairing       *pairingState
}

func newWatchUI(state updateState) watchUI {
	u := watchUI{timers: newTimerState()}
	u.clock.approximate = true
	u.clock.initialized = initialClockInitialized()
	u.home(state)
	return u
}

func (u *watchUI) cancelHold() { u.holding, u.holdStep = false, 0 }

func (u *watchUI) home(state updateState) {
	u.cancelHold()
	u.batteryChoice = batteryNone
	u.page = pageClock
	if state == firmwareTrial {
		u.page = pageTrial
	}
}

func (u *watchUI) showMessage(message string) {
	u.cancelHold()
	u.page, u.message = pageMessage, message
}

func (u *watchUI) back(state updateState) {
	u.cancelHold()
	if u.page == pagePhone {
		u.page = pageSettings
	} else if u.page == pageNotification {
		u.page = pageInbox
	} else if u.page == pageInbox {
		u.page = pageApps
	} else if u.page == pageMusic {
		u.page = pageApps
	} else if weatherPage(u.page) {
		if u.weather != nil {
			u.weather.open = false
		}
		if u.page == pageWeather {
			u.page = pageApps
		} else {
			u.page = pageWeather
		}
	} else if u.page == pageAlarmEdit || u.page == pageAlarmRepeat {
		u.page = pageAlarms
	} else if u.page == pageCountdownEdit {
		u.page = pageCountdown
	} else if u.page == pageAlarms || u.page == pageStopwatch || u.page == pageCountdown {
		u.page = pageApps
	} else if u.page == pageSetTime || u.page == pageSetDate {
		u.page = pageTimeSettings
	} else if u.page == pageTimeSettings {
		u.page = pageSettings
	} else if (u.page == pageUpdate || u.page == pageMessage) && state == firmwareConfirmed {
		u.page = pageSettings
	} else {
		u.home(state)
	}
}

// Editor temporaries must not enlarge the event loop's permanent stack frame.
//
//go:noinline
func (u *watchUI) handle(e inputEvent, now time.Time, state updateState, power powerStatus) uiAction {
	if u.handlePairing(e) {
		return actionNone
	}
	if u.page == pageDisplaySettings {
		u.handleDisplaySettings(e)
		return actionNone
	}
	if u.page == pagePairingSettings {
		u.handlePairingSettings(e)
		return actionNone
	}
	if u.handlePowerNotice(e) {
		return actionNone
	}
	if u.page == pageBatteryConfirm {
		u.handleBatteryConfirm(e, now)
		return actionNone
	}
	if u.page == pageTransfer {
		return u.handleTransfer(e, now)
	}
	if u.page == pageInbox || u.page == pageNotification {
		u.handleNotifications(e, state)
		return actionNone
	}
	if u.page == pageClock && state != firmwareTrial && e.Kind == inputTap && inRect(e, 12, 190, 228, 232) && u.unreadNotifications() > 0 {
		u.openInbox()
		return actionNone
	}
	if u.page == pageMusic || u.page == pagePhone {
		u.handlePhone(e, now, state)
		return actionNone
	}
	if weatherPage(u.page) {
		u.handleWeather(e, now, state)
		return actionNone
	}
	if u.page == pageAlert {
		snooze := e.Kind == inputTap && inRect(e, 16, 144, 224, 188) && u.timers.source < alarmCount
		if snooze || e.Kind == inputSleep || e.Kind == inputSwipeRight ||
			(e.Kind == inputTap && inRect(e, 16, 192, 224, 236)) {
			u.dismissAlert(now, snooze, state)
		}
		return actionNone
	}
	if u.page == pageTimeSync {
		u.handleTimeSync(e, now)
		return actionNone
	}
	if e.Kind == inputWake || e.Kind == inputSleep || e.Kind == inputCancel {
		u.cancelHold()
		if e.Kind == inputSleep && (u.page == pageSetTime || u.page == pageSetDate) {
			u.page = pageTimeSettings // Sleeping discards an unfinished edit.
		}
		if e.Kind == inputSleep && (u.page == pageAlarmEdit || u.page == pageAlarmRepeat || u.page == pageCountdownEdit) {
			u.back(state)
		}
		return actionNone
	}
	if u.page == pageUpdate && !now.Before(u.expires) {
		u.back(state)
		return actionNone
	}
	if e.Kind == inputSwipeRight && u.page == pageClock && state != firmwareTrial {
		u.page = pageApps
		return actionNone
	}
	if e.Kind == inputSwipeRight && u.page != pageTrial {
		u.back(state)
		return actionNone
	}
	if u.page == pageClock && e.Kind == inputSwipeLeft {
		if state == firmwareTrial {
			u.home(state)
		} else {
			u.page = pageSettings
		}
		return actionNone
	}
	if u.page == pageUpdate {
		if e.Kind == inputPress {
			u.cancelHold()
			if inRect(e, 24, 174, 216, 218) && state == firmwareConfirmed && updatePowerOK(power) {
				u.holding, u.holdSince = true, now
			}
		} else if e.Kind == inputHold && u.holding {
			if !inRect(e, 24, 174, 216, 218) || state != firmwareConfirmed || !updatePowerOK(power) {
				u.cancelHold()
				return actionNone
			}
			// Both the input tracker and this screen must have seen a fresh press.
			held := min(e.Held, now.Sub(u.holdSince))
			u.holdStep = int(min(held, updateHoldDuration) / (100 * time.Millisecond))
			if held >= updateHoldDuration {
				u.showMessage("Starting updater. Keep power connected.")
				return actionStartUpdate
			}
		} else if e.Kind == inputRelease || e.Kind == inputTap || e.Kind == inputSwipeLeft {
			u.cancelHold()
		}
	}
	if e.Kind != inputTap {
		return actionNone
	}
	if u.page != pageClock && u.page != pageTrial && inRect(e, 0, 0, 60, 44) {
		u.back(state)
		return actionNone
	}
	switch u.page {
	case pageTimeSettings, pageSetTime, pageSetDate, pageAlarmEdit:
		u.handleClockTap(e, now)
	case pageApps, pageAlarms, pageAlarmRepeat, pageStopwatch, pageCountdown, pageCountdownEdit:
		u.handleTimerTap(e, now)
	case pageSettings:
		if inRect(e, 16, 42, 224, 82) {
			u.page = pageTimeSettings
		} else if inRect(e, 16, 88, 224, 128) {
			u.openPhone()
		} else if inRect(e, 16, 180, 224, 220) {
			switch state {
			case firmwareUnavailable:
				u.showMessage("Wired MCUboot setup required. See OTA docs.")
			case firmwareInvalid:
				u.showMessage("Invalid boot state. Use wired recovery.")
			case firmwareTrial:
				u.home(state)
			case firmwareConfirmed:
				if !updatePowerOK(power) {
					u.showMessage("Charge to at least 20 percent first.")
				} else {
					u.cancelHold()
					u.page, u.expires = pageUpdate, now.Add(30*time.Second)
				}
			}
		} else if inRect(e, 16, 134, 224, 174) {
			u.page = pageDisplaySettings
		}
	case pageMessage:
		if inRect(e, 40, 174, 200, 218) {
			u.back(state)
		}
	case pageTrial:
		if state != firmwareTrial {
			u.home(state)
		} else if inRect(e, 12, 174, 114, 218) {
			return actionRevert
		} else if inRect(e, 126, 174, 228, 218) {
			if !updatePowerOK(power) {
				u.showMessage("Charge to at least 20 percent to keep.")
			} else {
				return actionKeep
			}
		}
	}
	return actionNone
}

func inRect(e inputEvent, left, top, right, bottom int16) bool {
	return e.X >= left && e.X < right && e.Y >= top && e.Y < bottom
}

var (
	white    = color.RGBA{238, 244, 250, 255}
	muted    = color.RGBA{153, 170, 187, 255}
	black    = color.RGBA{0, 0, 0, 255}
	card     = color.RGBA{17, 17, 17, 255}
	accent   = color.RGBA{68, 221, 170, 255}
	positive = color.RGBA{0, 51, 34, 255}
	disabled = color.RGBA{88, 101, 111, 255}
)

func centered(d canvas, font tinyfont.Fonter, y int16, text string, c color.RGBA) {
	if !textVisible(d, font, y, text) {
		return
	}
	width, _ := d.Size()
	w, _ := lineWidth(font, text)
	writeLine(d, font, width/2-int16(w)/2, y, text, c)
}

func drawButton(d canvas, x, width int16, label string, confirm bool) {
	if !controlVisible(d, 174, 44, 202) {
		return
	}
	c := card
	if confirm {
		c = positive
	}
	gfx.RoundBox(d, x, 174, width, 44, 6, c)
	w, _ := lineWidth(&uifont.Bold18, label)
	writeLine(d, &uifont.Bold18, x+width/2-int16(w)/2, 202, label, white)
}

// Split without allocating a []string or concatenating progressively longer
// strings. Line measurement and drawing both normalize whitespace to one space.
func nextWord(text string) (word, rest string) {
	text = strings.TrimLeftFunc(text, unicode.IsSpace)
	i := strings.IndexFunc(text, unicode.IsSpace)
	if i < 0 {
		return text, ""
	}
	return text[:i], text[i:]
}

func drawWordLine(d canvas, text string, y int16, width uint32) {
	if !textVisible(d, &uifont.Regular18, y, text) {
		return
	}
	screen, _ := d.Size()
	x := screen/2 - int16(width)/2
	space, _ := lineWidth(&uifont.Regular18, " ")
	for text != "" {
		word, rest := nextWord(text)
		if word == "" {
			break
		}
		writeLine(d, &uifont.Regular18, x, y, word, muted)
		w, _ := lineWidth(&uifont.Regular18, word)
		x += int16(w + space)
		text = rest
	}
}

func drawLines(d canvas, text string) {
	space, _ := lineWidth(&uifont.Regular18, " ")
	start, rest := text, text
	width, y := uint32(0), int16(92)
	for rest != "" {
		word, tail := nextWord(rest)
		if word == "" {
			break
		}
		w, _ := lineWidth(&uifont.Regular18, word)
		if width != 0 && width+space+w > 208 {
			drawWordLine(d, start[:len(start)-len(rest)], y, width)
			start, width, y = rest, 0, y+21
		}
		if width != 0 {
			width += space
		}
		width += w
		rest = tail
	}
	if width != 0 {
		drawWordLine(d, start, y, width)
	}
}

func (u *watchUI) timeLabel(now time.Time) string {
	text, start := timeDigits(now, u.use24)
	return string(text[start:])
}

const updateCounterBaseline = 122
const updateProgressY = 134

func (u *watchUI) draw(d canvas, now time.Time) {
	if u.page == pageBatteryConfirm {
		u.drawBatteryConfirm(d)
		return
	}
	if u.page == pageTransfer {
		u.drawTransfer(d)
		return
	}
	if u.page == pageTimeSync {
		u.drawTimeSync(d, now)
		return
	}
	if u.page == pageClock {
		now = u.clock.Now(now)
		writeLine(d, &uifont.Bold18, 18, 31, "goPine", accent)
		meridiem := ""
		if !u.use24 {
			meridiem = formatMeridiem(now)
		}
		text, start := timeDigits(now, u.use24)
		drawLargeTime(d, string(text[start:]), meridiem)
		if !u.clock.initialized || u.clock.approximate {
			centered(d, &uifont.Regular18, 181, "Set or sync time", warning)
		}
		if count := u.unreadNotifications(); count > 0 {
			centered(d, &uifont.Regular18, 216, "Messages: "+decimal(count), accent)
		}
		return
	}
	if u.page != pageTrial && u.page != pageAlert {
		gfx.Line(d, 25, 16, 16, 24, accent)
		gfx.Line(d, 16, 24, 25, 32, accent)
	}
	if u.page == pageTimeSettings || u.page == pageSetTime || u.page == pageSetDate || u.page == pageAlarmEdit {
		u.drawClockSettings(d, now)
		return
	}
	if u.page == pageMusic || u.page == pagePhone {
		u.drawPhone(d, now)
		return
	}
	if u.page == pageInbox || u.page == pageNotification {
		u.drawNotifications(d)
		return
	}
	if u.page == pageDisplaySettings {
		u.drawDisplaySettings(d)
		return
	}
	if u.page == pagePairingSettings {
		u.drawPairingSettings(d, now)
		return
	}
	if u.page >= pageApps {
		if weatherPage(u.page) {
			u.drawWeather(d, now)
			return
		}
		u.drawTimers(d, now)
		return
	}
	if u.page == pageSettings {
		centered(d, &uifont.Bold18, 29, "Settings", white)
		clockControl(d, 16, 42, 208, 40, "Time & date", card)
		clockControl(d, 16, 88, 208, 40, "Bluetooth", card)
		clockControl(d, 16, 134, 208, 40, "Display", card)
		clockControl(d, 16, 180, 208, 40, "Firmware update", card)
		return
	}
	centered(d, &uifont.Regular18, 29, "goPine "+firmwareVersion, muted)
	switch u.page {
	case pageUpdate:
		centered(d, &uifont.Bold24, 64, "Install update", white)
		centered(d, &uifont.Regular18, 88, "Hold to connect", muted)
		remaining := 30 - u.holdStep
		countdown := decimal(remaining/10) + "." + decimal(remaining%10) + "s"
		centered(d, &uifont.Bold24, updateCounterBaseline, countdown, accent)
		gfx.FillBox(d, 30, updateProgressY, 180, 3, card)
		gfx.FillBox(d, 30, updateProgressY, int16(u.holdStep)*180/30, 3, accent)
		centered(d, &uifont.Regular18, 159, "Swipe back to cancel", muted)
		drawButton(d, 24, 192, "PRESS AND HOLD", true)
	case pageTrial:
		centered(d, &uifont.Bold24, 64, "Keep this build?", white)
		drawLines(d, "Keep this version or restart to go back.")
		drawButton(d, 12, 102, "REVERT", false)
		drawButton(d, 126, 102, "KEEP", true)
	case pageMessage:
		centered(d, &uifont.Bold24, 64, "Firmware update", white)
		drawLines(d, u.message)
		drawButton(d, 40, 160, "BACK", false)
	}
}
