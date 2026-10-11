//go:build !baremetal

package main

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/music"
	"github.com/veandco/go-sdl2/sdl"
)

func TestDesktopTouchWakeChoice(t *testing.T) {
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	display, err := openDisplay()
	if err != nil {
		t.Fatal(err)
	}
	defer display.Close()
	d := display.(*desktopDisplay)
	click := func(kind uint32) {
		// SDL copies the full event union, so provide enough backing storage.
		e := struct {
			event   sdl.MouseButtonEvent
			padding [64]byte
		}{event: sdl.MouseButtonEvent{Type: kind, Button: sdl.BUTTON_LEFT, X: 120, Y: 195}}
		if _, err := sdl.PushEvent(&e.event); err != nil {
			t.Fatal(err)
		}
	}
	d.asleep = true
	click(sdl.MOUSEBUTTONDOWN)
	click(sdl.MOUSEBUTTONUP)
	if e, err := d.Wait(20 * time.Millisecond); err != nil || e.Kind != inputRefresh || !d.asleep {
		t.Fatal("touch woke by default", e, err)
	}
	button := struct {
		event   sdl.KeyboardEvent
		padding [64]byte
	}{event: sdl.KeyboardEvent{Type: sdl.KEYDOWN, Keysym: sdl.Keysym{Sym: sdl.K_SPACE}}}
	if _, err := sdl.PushEvent(&button.event); err != nil {
		t.Fatal(err)
	}
	if e, err := d.Wait(100 * time.Millisecond); err != nil || e.Kind != inputWake || d.asleep {
		t.Fatal("button cannot wake with touch wake off", e, err)
	}
	click(sdl.MOUSEBUTTONDOWN)
	click(sdl.MOUSEBUTTONUP)
	for _, want := range []inputKind{inputPress, inputTap} {
		if e, err := d.Wait(100 * time.Millisecond); err != nil || e.Kind != want {
			t.Fatal("touch is disabled while awake", e, err)
		}
	}
	d.asleep = true
	d.SetTouchWake(true)
	click(sdl.MOUSEBUTTONDOWN)
	click(sdl.MOUSEBUTTONUP)
	if e, err := d.Wait(100 * time.Millisecond); err != nil || e.Kind != inputWake || d.asleep {
		t.Fatal("opt-in touch did not wake", e, err)
	}
	if e, err := d.Wait(100 * time.Millisecond); err != nil || e.Kind == inputTap {
		t.Fatal("wake tap activated a control", e, err)
	}
	d.asleep = true
	if err := d.Wake(); err != nil || d.asleep {
		t.Fatal("automatic alert wake failed", err)
	}
}

// Exercise SDL's actual input and rendering paths without a desktop session.
// Optional screenshots are useful when changing the 240x240 layout.
func TestDesktopControlsAndRendering(t *testing.T) {
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	display, err := openDisplay()
	if err != nil {
		t.Fatal(err)
	}
	defer display.Close()
	d := display.(*desktopDisplay)
	now := time.Date(2026, 10, 4, 12, 34, 0, 0, time.UTC)
	for _, screen := range []struct {
		name  string
		ui    watchUI
		power powerStatus
	}{
		{"battery-touch", watchUI{page: pageBatteryConfirm, batteryChoice: batteryTouch, lightMode: false}, powerStatus{Percent: 73}},
		{"battery-bluetooth", watchUI{page: pageBatteryConfirm, batteryChoice: batteryBluetooth, lightMode: false}, powerStatus{Percent: 73}},
		{"battery-buzz", watchUI{page: pageBatteryConfirm, batteryChoice: batteryBuzz, lightMode: false}, powerStatus{Percent: 73}},
		{"battery-high", watchUI{page: pageBatteryConfirm, batteryChoice: batteryHigh, lightMode: false}, powerStatus{Percent: 73}},
		{"battery-medium", watchUI{page: pageBatteryConfirm, batteryChoice: batteryMedium, lightMode: false}, powerStatus{Percent: 73}},
		{"battery-low-light", watchUI{page: pageBatteryConfirm, batteryChoice: batteryLow, lightMode: true}, powerStatus{Percent: 73}},
		{"pairing-code", watchUI{page: pagePhone, pairing: &pairingState{code: 12346}}, powerStatus{Percent: 73}},
		{"pairing-code-waiting", watchUI{page: pagePhone, pairing: &pairingState{code: 12346, progress: 0x200}}, powerStatus{Percent: 73}},
		{"pairing-code-checking", watchUI{page: pagePhone, pairing: &pairingState{code: 12346, progress: 0x285}}, powerStatus{Percent: 73}},
		{"pairing-identity", watchUI{page: pagePhone, pairing: &pairingState{code: 12346, progress: 0x514}}, powerStatus{Percent: 73}},
		{"pairing-stage-timeout", watchUI{page: pagePairingSettings, pairing: &pairingState{status: 0, diagnostic: 13, progress: 0x200}}, powerStatus{Percent: 73}},
		{"pairing-saved", watchUI{page: pagePairingSettings, pairing: &pairingState{status: 2, count: 1}}, powerStatus{Percent: 73}},
		{"pairing-two-saved", watchUI{page: pagePairingSettings, pairing: &pairingState{status: 2, count: 2}}, powerStatus{Percent: 73}},
		{"pairing-unknown", watchUI{page: pagePairingSettings, pairing: &pairingState{status: -2}}, powerStatus{Percent: 73}},
		{"pairing-empty", watchUI{page: pagePairingSettings, pairing: &pairingState{status: 0}}, powerStatus{Percent: 73}},
		{"pairing-forgotten", watchUI{page: pagePairingSettings, pairing: &pairingState{status: 0, note: "Forget on devices too"}}, powerStatus{Percent: 73}},
		{"pairing-saving", watchUI{page: pagePairingSettings, pairing: &pairingState{status: 1}}, powerStatus{Percent: 73}},
		{"pairing-error", watchUI{page: pagePairingSettings, pairing: &pairingState{status: -1}}, powerStatus{Percent: 73}},
		{"pairing-timeout", watchUI{page: pagePairingSettings, pairing: &pairingState{status: 0, diagnostic: 0x2080000}}, powerStatus{Percent: 73}},
		{"pairing-wrong-code", watchUI{page: pagePairingSettings, pairing: &pairingState{status: 2, diagnostic: 0x2130404}}, powerStatus{Percent: 73}},
		{"display-flipped", watchUI{page: pageDisplaySettings, flipScreen: true}, powerStatus{Percent: 73}},
		{"clock", watchUI{page: pageClock}, powerStatus{Percent: 73}},
		{"clock-charging", watchUI{page: pageClock}, powerStatus{Percent: 68, State: chargeCharging}},
		{"clock-powered", watchUI{page: pageClock}, powerStatus{Percent: 100, State: chargeExternalPower}},
		{"clock-low", watchUI{page: pageClock}, powerStatus{Percent: 9}},
		{"charging", watchUI{page: pageClock, powerNotice: true}, powerStatus{Percent: 68, State: chargeCharging}},
		{"charging-full", watchUI{page: pageClock, powerNotice: true}, powerStatus{Percent: 100, State: chargeExternalPower}},
		{"charging-stopped", watchUI{page: pageClock, powerNotice: true}, powerStatus{Percent: 93, State: chargeExternalPower}},
		{"unplugged", watchUI{page: pageClock, powerNotice: true}, powerStatus{Percent: 68}},
		{"clock-24h", watchUI{page: pageClock, use24: true}, powerStatus{Percent: 73}},
		{"settings", watchUI{page: pageSettings}, powerStatus{Percent: 73}},
		{"settings-light", watchUI{page: pageSettings, lightMode: true}, powerStatus{Percent: 73}},
		{"display-dark", watchUI{page: pageDisplaySettings}, powerStatus{Percent: 73}},
		{"display-light", watchUI{page: pageDisplaySettings, lightMode: true, brightness: brightnessMedium, touchWake: true}, powerStatus{Percent: 73}},
		{"clock-light", watchUI{page: pageClock, lightMode: true}, powerStatus{Percent: 73}},
		{"clock-light-charging", watchUI{page: pageClock, lightMode: true}, powerStatus{Percent: 68, State: chargeCharging}},
		{"charging-light", watchUI{page: pageClock, lightMode: true, powerNotice: true}, powerStatus{Percent: 68, State: chargeCharging}},
		{"pairing-light", watchUI{page: pagePhone, lightMode: true, pairing: &pairingState{code: 12346}}, powerStatus{Percent: 73}},
		{"bluetooth-light", watchUI{page: pagePhone, lightMode: true, phoneAuto: true, phone: &phoneState{mode: phoneConnected, status: "Connected", music: music.State{Link: music.LinkAuthenticated}}}, powerStatus{Percent: 73}},
		{"apps-light", watchUI{page: pageApps, lightMode: true}, powerStatus{Percent: 73}},
		{"settings-touch-on", watchUI{page: pageDisplaySettings, touchWake: true}, powerStatus{Percent: 73}},
		{"settings-pending", watchUI{page: pageSettings, settingsNote: "Save pending"}, powerStatus{Percent: 73}},
		{"time-settings", watchUI{page: pageTimeSettings}, powerStatus{Percent: 73}},
		{"set-time", watchUI{page: pageSetTime, edit: clockEdit{hour: 12, minute: 34}}, powerStatus{Percent: 73}},
		{"set-time-24h", watchUI{page: pageSetTime, use24: true, edit: clockEdit{hour: 23, minute: 59}}, powerStatus{Percent: 73}},
		{"set-date", watchUI{page: pageSetDate, edit: clockEdit{year: 2026, month: 10, day: 4}}, powerStatus{Percent: 73}},
		{"update", watchUI{page: pageUpdate}, powerStatus{Percent: 73}},
		{"holding", watchUI{page: pageUpdate, holding: true, holdStep: 15}, powerStatus{Percent: 73}},
		{"trial", watchUI{page: pageTrial}, powerStatus{Percent: 73}},
		{"error", watchUI{page: pageMessage, message: "Recovery setup failed. Check power and use wired setup again."}, powerStatus{Percent: 73}},
		{"apps", watchUI{page: pageApps}, powerStatus{Percent: 73}},
		{"music", watchUI{page: pageMusic, phone: samplePhone(now)}, powerStatus{Percent: 73}},
		{"phone", watchUI{page: pagePhone, phoneAuto: true, phone: &phoneState{mode: phoneConnected, status: "Connected", music: music.State{Link: music.LinkAuthenticated}}}, powerStatus{Percent: 73}},
		{"phone-securing", watchUI{page: pagePhone, phoneAuto: true, phone: &phoneState{mode: phoneConnected, status: "Securing connection", music: music.State{Link: music.LinkSecuring}}}, powerStatus{Percent: 73}},
		{"phone-off", watchUI{page: pagePhone, phone: &phoneState{status: "Bluetooth off"}}, powerStatus{Percent: 73}},
		{"music-off", watchUI{page: pageMusic, phone: &phoneState{status: "Bluetooth off"}}, powerStatus{Percent: 73}},
		{"music-unsubscribed", watchUI{page: pageMusic, phone: &phoneState{mode: phoneConnected, status: "Connected", music: music.State{Link: music.LinkAuthenticated}}}, powerStatus{Percent: 73}},
		{"music-error", watchUI{page: pageMusic, phone: &phoneState{mode: phoneConnected, status: "Connected", musicNote: "Command failed", music: music.State{Link: music.LinkMusicReady}}}, powerStatus{Percent: 73}},
		{"inbox", watchUI{page: pageInbox, notifications: sampleNotifications()}, powerStatus{Percent: 73}},
		{"inbox-empty", watchUI{page: pageInbox, notifications: &notificationState{}}, powerStatus{Percent: 73}},
		{"notification", watchUI{page: pageNotification, notifications: sampleNotifications()}, powerStatus{Percent: 73}},
		{"update-waiting", watchUI{page: pageTransfer, transfer: &transferState{phase: updateWaiting, open: true}}, powerStatus{Percent: 73}},
		{"update-receiving", watchUI{page: pageTransfer, transfer: &transferState{phase: updateReceiving, open: true, percent: 58}}, powerStatus{Percent: 73}},
		{"update-verifying", watchUI{page: pageTransfer, transfer: &transferState{phase: updateVerifying, open: true, percent: 100}}, powerStatus{Percent: 73}},
		{"update-ready", watchUI{page: pageTransfer, transfer: &transferState{phase: updateReady, open: true, percent: 100, version: "0.3.14"}}, powerStatus{Percent: 73}},
		{"update-failed", watchUI{page: pageTransfer, transfer: &transferState{phase: updateFailed, open: true, message: "Connection interrupted"}}, powerStatus{Percent: 73}},
		{"notification-read", watchUI{page: pageNotification, notifications: notificationPreview("read")}, powerStatus{Percent: 73}},
		{"notification-more", watchUI{page: pageNotification, notifications: notificationPreview("more")}, powerStatus{Percent: 73}},
		{"notification-replaced", watchUI{page: pageNotification, notifications: notificationPreview("replaced")}, powerStatus{Percent: 73}},
		{"notification-call", watchUI{page: pageNotification, notifications: notificationPreview("call")}, powerStatus{Percent: 73}},
		{"inbox-quiet", watchUI{page: pageInbox, notifications: notificationPreview("quiet")}, powerStatus{Percent: 73}},
		{"clock-unread", watchUI{page: pageClock, notifications: sampleNotifications()}, powerStatus{Percent: 73}},
		{"weather", watchUI{page: pageWeather, weather: sampleWeather(now), clock: watchClock{initialized: true}}, powerStatus{Percent: 73}},
		{"weather-forecast", watchUI{page: pageWeatherForecast, weather: sampleWeather(now), clock: watchClock{initialized: true}}, powerStatus{Percent: 73}},
		{"weather-empty", watchUI{page: pageWeather, weather: &weatherState{}}, powerStatus{Percent: 73}},
		{"weather-update", watchUI{page: pageWeatherSync, weather: &weatherState{open: true, expires: now.Add(weatherWindow), status: "Connect your phone"}}, powerStatus{Percent: 73}},
		{"alarms", watchUI{page: pageAlarms, timers: newTimerState(), clock: watchClock{initialized: true}}, powerStatus{Percent: 73}},
		{"alarms-unset", watchUI{page: pageAlarms, timers: newTimerState()}, powerStatus{Percent: 73}},
		{"alarm-edit", watchUI{page: pageAlarmEdit, edit: clockEdit{hour: 7, minute: 30}}, powerStatus{Percent: 73}},
		{"alarm-repeat", watchUI{page: pageAlarmRepeat, editRepeat: alarmWeekdays}, powerStatus{Percent: 73}},
		{"stopwatch", watchUI{page: pageStopwatch, timers: timerState{watch: stopwatch{saved: time.Hour + 23*time.Minute + 45600*time.Millisecond, lap: 62 * time.Second}}}, powerStatus{Percent: 73}},
		{"countdown", watchUI{page: pageCountdown, timers: newTimerState()}, powerStatus{Percent: 73}},
		{"countdown-edit", watchUI{page: pageCountdownEdit, edit: clockEdit{hour: 1, minute: 23, day: 45}}, powerStatus{Percent: 73}},
		{"alarm-alert", watchUI{page: pageAlert, timers: timerState{active: true, source: 2}}, powerStatus{Percent: 73}},
		{"timer-alert", watchUI{page: pageAlert, timers: timerState{active: true, source: countdownSource}}, powerStatus{Percent: 73}},
	} {
		var renderer frameRenderer
		if err := d.SetFlipped(screen.ui.flipScreen); err != nil {
			t.Fatal(err)
		}
		if err := renderer.render(d, func(c canvas) {
			screen.ui.drawFrame(c, now, screen.power)
		}); err != nil {
			t.Fatal(err)
		}
		if dir := os.Getenv("GOPINE_TEST_SCREENSHOTS"); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(filepath.Join(dir, screen.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, d.surface)
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("save screenshot: %v, %v", err, closeErr)
			}
		}
	}
	// An automatic alert wake with no contact must accept the first fresh tap.
	if err := d.Wake(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []uint32{sdl.MOUSEBUTTONDOWN, sdl.MOUSEBUTTONUP} {
		// go-sdl2 casts the concrete Go event to the larger SDL_Event union.
		// Back it with a full-size allocation so C cannot overread a small Go
		// event (and Go's race/checkptr build can validate the conversion).
		e := struct {
			event   sdl.MouseButtonEvent
			padding [64]byte
		}{event: sdl.MouseButtonEvent{Type: kind, Button: sdl.BUTTON_LEFT, X: 180, Y: 195}}
		if _, err := sdl.PushEvent(&e.event); err != nil {
			t.Fatal(err)
		}
	}
	event, err := d.Wait(100 * time.Millisecond)
	if err != nil || event.Kind != inputPress {
		t.Fatalf("press not delivered: %+v, %v", event, err)
	}
	event, err = d.Wait(100 * time.Millisecond)
	if err != nil || event.Kind != inputTap || event.X != 180 || event.Y != 195 {
		t.Fatalf("click not delivered: %+v, %v", event, err)
	}
	down := struct {
		event   sdl.MouseButtonEvent
		padding [64]byte
	}{event: sdl.MouseButtonEvent{Type: sdl.MOUSEBUTTONDOWN, Button: sdl.BUTTON_LEFT, X: 200, Y: 120}}
	motion := struct {
		event   sdl.MouseMotionEvent
		padding [64]byte
	}{event: sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, X: 40, Y: 120}}
	up := struct {
		event   sdl.MouseButtonEvent
		padding [64]byte
	}{event: sdl.MouseButtonEvent{Type: sdl.MOUSEBUTTONUP, Button: sdl.BUTTON_LEFT, X: 40, Y: 120}}
	for _, e := range []sdl.Event{&down.event, &motion.event, &up.event} {
		if _, err := sdl.PushEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []inputKind{inputPress, inputCancel, inputSwipeLeft} {
		got, err := d.Wait(100 * time.Millisecond)
		if err != nil || got.Kind != want {
			t.Fatalf("drag: %+v, %v; want %v", got, err, want)
		}
	}
	quit := struct {
		event   sdl.QuitEvent
		padding [64]byte
	}{event: sdl.QuitEvent{Type: sdl.QUIT}}
	if _, err := sdl.PushEvent(&quit.event); err != nil {
		t.Fatal(err)
	}
	event, err = d.Wait(100 * time.Millisecond)
	if err != nil || event.Kind != inputQuit {
		t.Fatalf("quit not delivered: %+v, %v", event, err)
	}
}

func TestDesktopFlippedPixels(t *testing.T) {
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	display, err := openDisplay()
	if err != nil {
		t.Fatal(err)
	}
	defer display.Close()
	d := display.(*desktopDisplay)
	u := newWatchUI(firmwareConfirmed)
	now := time.Unix(0, 0)
	var r frameRenderer
	draw := func(c canvas) { u.drawFrame(c, now, powerStatus{Percent: 73}) }
	if err := r.render(d, draw); err != nil {
		t.Fatal(err)
	}
	pixels := make([]color.RGBA, 240*240)
	for y := 0; y < 240; y++ {
		for x := 0; x < 240; x++ {
			pixels[y*240+x] = color.RGBAModel.Convert(d.surface.At(x, y)).(color.RGBA)
		}
	}
	d.SetFlipped(true)
	r.invalidate()
	if err := r.render(d, draw); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 240; y++ {
		for x := 0; x < 240; x++ {
			if color.RGBAModel.Convert(d.surface.At(239-x, 239-y)).(color.RGBA) != pixels[y*240+x] {
				t.Fatalf("rotated pixel mismatch at %d,%d", x, y)
			}
		}
	}
}
