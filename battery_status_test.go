package main

import (
	"testing"
	"time"
)

func TestBatteryStatusAcrossPagesAndOverlays(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 34, 0, 0, time.UTC)
	for _, light := range []bool{false, true} {
		for pg := pageClock; pg <= pageBatteryConfirm; pg++ {
			u := newWatchUI(firmwareConfirmed)
			u.page, u.lightMode = pg, light
			u.phone, u.notifications = samplePhone(now), sampleNotifications()
			u.weather, u.pairing = sampleWeather(now), &pairingState{status: 2}
			u.transfer = &transferState{phase: updateReceiving, percent: 58}
			u.batteryChoice = batteryTouch
			if pg == pagePairing {
				u.pairing.code = 123456
			}
			if pg == pageCharging {
				u.powerNotice = true
			}
			for _, power := range []powerStatus{{Percent: 9}, {Percent: 73, State: chargeCharging}, {Percent: 100, State: chargeExternalPower}} {
				d, ref := &memoryDisplay{}, &memoryDisplay{}
				d.SetLightMode(light)
				ref.SetLightMode(light)
				ref.FillScreen(themeColor(light, black))
				drawStatusBattery(ref, power)
				u.drawFrame(d, now, power)
				for y := 0; y < 13; y++ {
					for x := 190; x < 240; x++ {
						if d.pixels[y*240+x] != ref.pixels[y*240+x] {
							t.Fatal("status missing or header overlaps", pg, light, x, y)
						}
					}
				}
				changed := power
				changed.Percent++
				if u.frameKey(now, changed) == u.frameKey(now, power) {
					t.Fatal("battery change cannot repaint", pg)
				}
			}
		}
	}
}

func TestSettingsBatteryFooterRemoved(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	u.page = pageSettings
	d, ref := &memoryDisplay{}, &memoryDisplay{}
	ref.FillScreen(black)
	u.drawFrame(d, time.Time{}, powerStatus{Percent: 73, State: chargeCharging})
	for i := 225 * 240; i < len(d.pixels); i++ {
		if d.pixels[i] != ref.pixels[i] {
			t.Fatal("Settings still has a battery footer")
		}
	}
	u.settingsNote = "Settings saved"
	u.drawFrame(d, time.Time{}, powerStatus{Percent: 73})
	for i := 225 * 240; i < len(d.pixels); i++ {
		if d.pixels[i] != ref.pixels[i] {
			return
		}
	}
	t.Fatal("settings notice lost")
}

func TestChargingFrameKeyTakesPriorityOverDeviceSettings(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	u.page, u.pairing, u.powerNotice = pagePairingSettings, &pairingState{status: 2}, true
	if u.frameKey(time.Time{}, powerStatus{}).page != pageCharging {
		t.Fatal("frame identity disagrees with visible charging overlay")
	}
}

func TestBatteryStatusChargingAndBounds(t *testing.T) {
	for _, percent := range []uint8{0, 9, 73, 100, 255} {
		charging, external := &memoryDisplay{}, &memoryDisplay{}
		charging.FillScreen(black)
		external.FillScreen(black)
		drawStatusBattery(charging, powerStatus{Percent: percent, State: chargeCharging})
		drawStatusBattery(external, powerStatus{Percent: percent, State: chargeExternalPower})
		if charging.pixels == external.pixels {
			t.Fatal("charging bolt indistinguishable from plugged-in gauge", percent)
		}
		if percent == 255 {
			ref := &memoryDisplay{}
			ref.FillScreen(black)
			drawStatusBattery(ref, powerStatus{Percent: 100, State: chargeCharging})
			if charging.pixels != ref.pixels {
				t.Fatal("reading not clamped")
			}
		}
	}
}
