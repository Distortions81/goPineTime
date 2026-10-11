// This program displays the current time on the screen.
package main

import (
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() error {
	app, err := openApplication()
	if err != nil {
		return err
	}
	defer app.close()
	err = app.loop.run()
	app.flush()
	return err
}

type watchApplication struct {
	loop  watchLoop
	close func()
	flush func()
}

// Clock/journal restoration uses large temporary values. Its frame must unwind
// before the loop; closures retain only the state that actually needs to live.
//
//go:noinline
func openApplication() (*watchApplication, error) {
	if err := initializeClock(); err != nil {
		return nil, wrapError("initialize clock", err)
	}
	display, err := openDisplay()
	if err != nil {
		return nil, wrapError("open display", err)
	}

	ui := newWatchUI(firmwareState())
	syncController := timeSyncController{radio: newTimeRadio()}
	syncController.allowPhone = func() bool { return firmwareState() == firmwareConfirmed }
	activeTimeRadio = syncController.radio
	persistence := restoreClock(&ui, time.Now())
	settings := loadSettings(&ui, time.Now(), persistence.journal)
	// Cached UI power can be arbitrarily old during sleep. Sample only when
	// actually about to write, rather than waking periodically for the ADC.
	settings.writePowerOK = func() bool { return updatePowerOK(display.PowerStatus()) }
	beforeReset := func() {
		// Best effort: clock storage failure must not prevent OTA or rollback.
		allowed := updatePowerOK(display.PowerStatus())
		idle := !radioBusy(syncController.radio)
		settings.flush(&ui, time.Now(), allowed && idle)
		persistence.beforeReset(&ui, time.Now(), allowed && idle)
	}
	var renderer frameRenderer
	progress := func(title string) func(int) {
		last := -1
		current := 0
		draw := func(c canvas) {
			centered(c, &uifont.Bold18, 65, title, white)
			centered(c, &uifont.Bold24, 130, decimal(current)+"%", accent)
			centered(c, &uifont.Bold18, 175, "Keep power connected", muted)
		}
		return func(percent int) {
			if percent/5 == last {
				return
			}
			last = percent / 5
			current = percent
			_ = renderer.render(display, draw)
		}
	}
	if provisioningBuild && !updatePowerOK(display.PowerStatus()) {
		ui.showMessage("Charge to at least 20 percent. Reboot to retry setup.")
	} else if err := prepareFirmware(progress("Installing recovery")); err != nil {
		ui.showMessage("Recovery setup failed. Check power and use wired setup again.")
	} else if provisioningBuild {
		ui.showMessage("Recovery ready. Install bootstrap HEX over SWD now.")
	}

	loop := watchLoop{display: display, ui: &ui, sync: &syncController, renderer: &renderer,
		now: time.Now, state: firmwareState}
	loop.beforeSync = func(now time.Time) {
		// Keep the flash save outside the deeper phone-update call stack. Save
		// opt-in before starting BLE; low battery leaves a temporary choice.
		if ui.phoneAuto && !syncController.running && !radioBusy(syncController.radio) && !settings.saved.matches(&ui) {
			settings.flush(&ui, now, firmwareState() == firmwareConfirmed && !ui.timers.active)
		}
	}
	updater := &directUpdater{sync: &syncController, openFlash: openUpdateFlash,
		confirmed: func() bool { return firmwareState() == firmwareConfirmed },
		powerOK:   func() bool { return updatePowerOK(display.PowerStatus()) },
		reboot:    func() error { return restartUpdatedFirmware(beforeReset) }}
	loop.updater = updater
	loop.save = func(now time.Time, power powerStatus) time.Time {
		allowed := updatePowerOK(power) && !ui.timers.active && firmwareState() == firmwareConfirmed
		return syncController.saveSettings(&ui, settings, now, allowed)
	}
	loop.action = func(action uiAction) {
		switch action {
		case actionKeep:
			if err := keepFirmware(); err != nil {
				ui.showMessage("Could not confirm. Reboot can still revert this build.")
			} else {
				ui.home(firmwareState())
			}
		case actionRevert:
			if err := revertFirmware(beforeReset); err != nil {
				ui.showMessage(err.Error())
			}
		case actionStartUpdate:
			// Recheck actual power at the flash boundary, not just cached UI data.
			if !updatePowerOK(display.PowerStatus()) {
				ui.showMessage("Charge to at least 20 percent first.")
			} else if _, ok := syncController.radio.(directUpdateRadio); !ok {
				ui.showMessage("Bluetooth updater unavailable in this build.")
			} else {
				ui.beginDirectUpdate(time.Now())
			}
		case actionInstallUpdate:
			updater.install(&ui)
		}
	}
	return &watchApplication{
		loop: loop,
		close: func() {
			_ = updater.release()
			syncController.close()
			activeTimeRadio = nil
			_ = display.Close()
		},
		flush: func() {
			settings.flush(&ui, time.Now(), updatePowerOK(display.PowerStatus()) && firmwareState() == firmwareConfirmed && !radioBusy(syncController.radio))
		},
	}, nil
}

// Exclude input timestamps: 50Hz touch polling should repaint only when visible
// state changes (the hold indicator advances at 10Hz).
type frameKey struct {
	page                                     page
	message, syncStatus, settingsNote        string
	clockMinute                              int64
	timer                                    timerFrameState
	syncSecond                               int64
	edit                                     clockEdit
	step                                     int
	percent                                  uint8
	power                                    chargeState
	use24, holding, approximate, initialized bool
	syncOpen, syncPending                    bool
	touchWake, flipScreen                    bool
	weatherRevision                          uint32
	notificationRevision                     uint32
}

// Keep calendar temporaries out of the long-lived event loop frame.
//
//go:noinline
func (u *watchUI) frameKey(now time.Time, power powerStatus) frameKey {
	if u.pairingVisible() {
		return frameKey{page: pagePairing, step: int(u.pairing.code), syncSecond: int64(u.pairing.progress)}
	}
	if u.page == pagePairingSettings && u.pairing != nil {
		return frameKey{page: pagePairingSettings, step: u.pairing.status, holding: u.pairing.confirm, message: u.pairing.note, syncSecond: int64(u.pairing.diagnostic) | int64(u.pairing.progress)<<32 | int64(u.pairing.count)<<48}
	}
	if u.powerNoticeVisible() {
		return frameKey{page: pageCharging, percent: power.Percent, power: power.State}
	}
	key := frameKey{page: u.page, message: u.message, settingsNote: u.settingsNote,
		use24: u.use24, touchWake: u.touchWake, flipScreen: u.flipScreen, holding: u.holding, step: u.holdStep, edit: u.edit,
		approximate: u.clock.approximate, initialized: u.clock.initialized,
		percent: power.Percent, power: power.State, timer: u.timerFrameKey(now)}
	if u.page == pageClock || u.page == pageAlert {
		stamp := u.clock.Now(now)
		_, offset := stamp.Zone()
		key.clockMinute = (stamp.Unix() + int64(offset) - int64(stamp.Second())) / 60
	}
	if u.page == pageTimeSync {
		key.syncOpen, key.syncPending, key.syncStatus = u.sync.Open, u.sync.Pending, u.syncStatus
		if u.sync.Pending {
			key.syncSecond = u.sync.Proposed.Add(now.Sub(u.sync.Received)).Unix()
		} else if u.sync.Open {
			key.syncSecond = int64(max(0, (u.sync.Expires.Sub(now)+time.Second-1)/time.Second))
		}
	}
	if weatherPage(u.page) && u.weather != nil {
		w := u.weather
		key.weatherRevision, key.message, key.use24, key.syncOpen = w.revision, w.status, w.fahrenheit, w.open
		key.clockMinute = calendarMillis(u.clock.Now(now)) / 60000
		if w.open {
			key.syncSecond = int64(max(0, w.expires.Sub(now)+time.Second-1) / time.Second)
		}
	}
	if (u.page == pageMusic || u.page == pagePhone) && u.phone != nil {
		p := u.phone
		key.weatherRevision, key.message, key.step = p.revision, p.status, int(p.mode)
		if u.page == pagePhone {
			key.touchWake = u.phoneAuto
		}
		if p.mode == phoneSession {
			key.syncSecond = int64(max(0, p.expires.Sub(now)+time.Minute-1) / time.Minute)
		}
	}
	if u.page == pageClock {
		key.notificationRevision = uint32(u.unreadNotifications())
	}
	if (u.page == pageInbox || u.page == pageNotification) && u.notifications != nil {
		n := u.notifications
		key.notificationRevision, key.weatherRevision = n.inbox.Revision, n.selected
		key.step, key.touchWake = n.offset, n.quiet
	}
	if u.page == pageTransfer && u.transfer != nil {
		t := u.transfer
		key.step = t.percent
		key.message = t.message
		key.syncStatus = t.version
		key.weatherRevision = uint32(t.phase)
	}
	return key
}

func formatTime(t time.Time) string {
	text, start := timeDigits(t, false)
	return string(text[start:])
}

func formatMeridiem(t time.Time) string {
	if t.Hour() < 12 {
		return "AM"
	}
	return "PM"
}

func nextMinuteDelay(t time.Time) time.Duration {
	return t.Truncate(time.Minute).Add(time.Minute).Sub(t)
}
