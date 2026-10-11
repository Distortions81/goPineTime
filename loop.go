package main

import "time"

// The same event loop runs on PineTime, SDL, and the scripted virtual clock.
// Hardware actions and time are injected so tests can advance through sleeping,
// input, radio activity, and alarms without waiting for wall time to pass.
type watchLoop struct {
	display    clockDisplay
	ui         *watchUI
	sync       *timeSyncController
	renderer   *frameRenderer
	updater    *directUpdater
	now        func() time.Time
	state      func() updateState
	action     func(uiAction)
	save       func(time.Time, powerStatus) time.Time
	beforeSync func(time.Time)
}

func (l *watchLoop) run() error {
	defer l.display.SetVibration(false)
	var previous frameKey
	painted, awake := false, true
	flipped := false
	power := l.display.PowerStatus()
	nextPower := l.now().Add(powerPollInterval)
	l.display.SetTouchWake(l.ui.touchWake)
	// TinyGo treats callbacks passed through the renderer as escaping. Allocate
	// one closure for the loop, not one closure and captured timestamp per frame.
	var frameTime time.Time
	drawFrame := func(c canvas) { l.ui.drawFrame(c, frameTime, power) }
	wakeAlert := func() error {
		if err := l.display.Wake(); err != nil {
			return wrapError("wake for alert", err)
		}
		awake, painted = true, false
		power = l.display.PowerStatus()
		nextPower = l.now().Add(powerPollInterval)
		l.renderer.invalidate()
		return nil
	}
	for {
		now := l.now()
		if awake && !now.Before(nextPower) {
			power = l.display.PowerStatus()
			nextPower = now.Add(powerPollInterval)
		}
		if !l.sync.directMode && l.ui.tickTimers(now, l.state()) {
			if err := wakeAlert(); err != nil {
				return err
			}
		}
		if l.updater != nil {
			l.updater.tick(l.ui, now, power)
		}
		exclusive := l.ui.updateExclusive() || l.sync.directMode
		if !exclusive && l.beforeSync != nil {
			l.beforeSync(now)
		}
		l.sync.update(l.ui, now, power.Percent)
		if !exclusive {
			l.ui.receiveAppleNotifications(l.sync.radio, now)
		}
		if !exclusive && l.sync.updatePairing(l.ui) {
			if err := wakeAlert(); err != nil {
				return err
			}
		}
		if l.ui.pairingVisible() || l.ui.phoneSetupVisible(now) || l.ui.sync.Open || l.ui.timers.active || (l.ui.weather != nil && l.ui.weather.open) || (l.ui.page == pageTransfer && l.ui.transfer != nil && l.ui.transfer.open) {
			l.display.KeepAwake()
		}
		vibrating := false
		if !exclusive {
			notificationBuzz := l.ui.notificationVibrating(now)
			vibrating = l.ui.timers.vibrating(now) || notificationBuzz
		}
		l.display.SetVibration(vibrating)
		l.display.SetTouchWake(l.ui.touchWake)
		if backlight, ok := l.display.(interface{ SetBrightness(uint8) }); ok {
			backlight.SetBrightness(l.ui.brightness)
		}
		// Capture the deadline before rendering. A frame that crosses a minute
		// or alert boundary must service that deadline immediately afterward.
		wakeAt := now.Add(nextLoopDelay(now, l.ui, nextPower, awake))
		if !exclusive && l.save != nil {
			if due := l.save(now, power); !due.IsZero() && due.Before(wakeAt) {
				wakeAt = due
			}
		}
		if awake {
			if flipped != l.ui.flipScreen {
				if err := l.display.SetFlipped(l.ui.flipScreen); err != nil {
					return wrapError("rotate display", err)
				}
				flipped = l.ui.flipScreen
				painted = false
				l.renderer.invalidate()
			}
			key := l.ui.frameKey(now, power)
			if !painted || key != previous {
				frameTime = now
				mask := allStrips
				if painted {
					mask = changedStrips(&previous, &key)
				}
				if err := l.renderer.renderStrips(l.display, drawFrame, mask); err != nil {
					return wrapError("refresh display", err)
				}
				previous, painted = key, true
			}
		}
		event, err := l.display.Wait(max(wakeAt.Sub(l.now()), minimumLoopWait))
		if err != nil {
			return wrapError("wait for display", err)
		}
		event = orientInput(event, flipped)
		if event.Kind == inputQuit {
			return nil
		}
		now = l.now()
		if event.Kind == inputPower {
			if err := wakeAlert(); err != nil {
				return err
			}
			l.ui.showPowerNotice()
			event = inputEvent{Kind: inputRefresh}
		}
		if event.Kind == inputSleep {
			awake = false
			if p := l.ui.phone; p != nil {
				p.setupUntil, p.setupRequested = time.Time{}, false
			}
		}
		if event.Kind == inputWake {
			awake, painted = true, false
			if (l.ui.page == pagePhone || l.ui.page == pagePairingSettings) && l.ui.phone != nil {
				l.ui.phone.setupRequested, l.ui.phone.advertiseRequested = true, true
			}
			power = l.display.PowerStatus()
			nextPower = now.Add(powerPollInterval)
			l.renderer.invalidate()
		}
		// An alert can arrive during Wait while a contact began on another page.
		// Discard that contact so it cannot dismiss the new alert or finish OTA.
		if !l.sync.directMode && l.ui.tickTimers(now, l.state()) {
			if err := wakeAlert(); err != nil {
				return err
			}
			event = inputEvent{Kind: inputCancel}
		}
		if action := l.ui.handle(event, now, l.state(), power); action != actionNone {
			l.action(action)
			painted = false
		}
	}
}
