//go:build pinetime

package main

import (
	"device/nrf"
	"image/color"
	"machine"
	"runtime/volatile"
	"time"
	"unsafe"

	"tinygo.org/x/drivers"
	"tinygo.org/x/drivers/pixel"
	"tinygo.org/x/drivers/st7789"
)

type pineTimeDisplay struct {
	*st7789.DeviceOf[pixel.RGB444BE]
	batteryADC        machine.ADC
	batteryMillivolts uint32
	batteryReady      bool
	buttonPressed     bool
	button            buttonInput
	charger           chargerInput
	screenOn          bool
	lcdPower          lcdPower
	touchWake         bool
	brightness        uint8
	sleepAt           time.Time
	touch             touchController
	touchEvents       touchQueue
	vibrating         bool
	vibrationEnds     time.Time
}

const (
	chargeIndicationPin = machine.Pin(12)
	powerPresencePin    = machine.Pin(19)
	batteryVoltagePin   = machine.Pin(31)
	screenTimeout       = 15 * time.Second
	touchPollInterval   = 2 * time.Millisecond
)

func openDisplay() (clockDisplay, error) {
	// The DC/DC regulator substantially reduces CPU power consumption. Some
	// PineTime bootloaders also leave UART enabled even though the board has no
	// serial pins, so explicitly turn it off.
	nrf.POWER.DCDCEN.Set(nrf.POWER_DCDCEN_DCDCEN)
	nrf.UART0.ENABLE.Set(0)
	stopRuntimeRNG(false)

	// MCUboot leaves the low-brightness channel on. Take ownership of all
	// three active-low channels, not just the one used by the LCD driver.
	for _, pin := range [...]machine.Pin{machine.LCD_BACKLIGHT_LOW, machine.LCD_BACKLIGHT_MID, machine.LCD_BACKLIGHT_HIGH} {
		pin.High()
		pin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	}

	// Keep the external SPI flash deselected while configuring the shared bus.
	flashCS := machine.Pin(5)
	flashCS.Configure(machine.PinConfig{Mode: machine.PinOutput})
	flashCS.High()
	machine.LCD_CS.Configure(machine.PinConfig{Mode: machine.PinOutput})
	machine.LCD_CS.High()

	configureDisplaySPI()

	// The clock does not use external flash, so place it in deep power-down.
	flashCS.Low()
	_ = machine.SPI0.Tx([]byte{0xB9}, nil)
	flashCS.High()

	display := st7789.NewOf[pixel.RGB444BE](
		machine.SPI0,
		machine.LCD_RESET,
		machine.LCD_RS,
		machine.LCD_CS,
		machine.LCD_BACKLIGHT_HIGH,
	)
	display.Configure(st7789.Config{
		Width:      240,
		Height:     240,
		Rotation:   drivers.Rotation0,
		RowOffset:  80,
		FrameRate:  st7789.FRAMERATE_39,
		VSyncLines: 32,
	})
	setBacklight(true, brightnessHigh)

	chargeIndicationPin.Configure(machine.PinConfig{Mode: machine.PinInput})
	powerPresencePin.Configure(machine.PinConfig{Mode: machine.PinInput})
	machine.InitADC()
	batteryADC := machine.ADC{Pin: batteryVoltagePin}
	batteryADC.Configure(machine.ADCConfig{
		Reference:  3000,
		Resolution: 12,
		SampleTime: 40,
		Samples:    1,
	})
	nrf.SAADC.ENABLE.Set(0)

	configureInputInterrupts()
	configureIdleTimer()
	machine.VIBRATOR_PIN.High() // Active-low, off before configuring output.
	machine.VIBRATOR_PIN.Configure(machine.PinConfig{Mode: machine.PinOutput})

	d := &pineTimeDisplay{
		DeviceOf:   &display,
		batteryADC: batteryADC,
		screenOn:   true,
		sleepAt:    time.Now().Add(screenTimeout),
	}
	d.lcdPower.woke(time.Now())
	d.charger.state = readChargerState()
	d.charger.candidate = d.charger.state
	// Touch is optional at runtime so a controller fault cannot prevent the
	// side button and clock display from working.
	if configureBoardI2C() == nil {
		// Each transfer has TinyGo's bounded bus timeout. Sensor failures are
		// nonfatal and do not prevent touch, button, or clock startup.
		_, _ = shutdownUnusedSensors(boardI2C{}, time.Sleep)
		_ = d.touch.Configure()
	}
	return d, nil
}

func (d *pineTimeDisplay) FillScreen(c color.RGBA) {
	d.DeviceOf.FillScreen(c)
}

func (d *pineTimeDisplay) PowerStatus() powerStatus {
	nrf.SAADC.ENABLE.Set(nrf.SAADC_ENABLE_ENABLE_Enabled)
	raw := d.batteryADC.Get()
	// Get waits for STOPPED but leaves the peripheral enabled.
	nrf.SAADC.ENABLE.Set(0)
	millivolts := uint32(adcToBatteryMillivolts(raw))
	if d.batteryReady {
		// Smooth occasional ADC noise without hiding meaningful changes for long.
		d.batteryMillivolts = (d.batteryMillivolts*3 + millivolts) / 4
	} else {
		d.batteryMillivolts = millivolts
		d.batteryReady = true
	}

	return powerStatus{
		Percent:    estimateBatteryPercent(uint16(d.batteryMillivolts)),
		Millivolts: uint16(d.batteryMillivolts),
		State:      readChargerState(),
	}
}

func readChargerState() chargeState {
	return chargerState(!powerPresencePin.Get(), !chargeIndicationPin.Get())
}

func (d *pineTimeDisplay) Wait(duration time.Duration) (inputEvent, error) {
	refreshAt := time.Now().Add(duration)

	for {
		now := time.Now()
		// Service the side button/watchdog even during continuous touch events.
		pressed := d.readButton(now)
		if pressed && !d.buttonPressed {
			d.buttonPressed = true
			d.touchEvents.clear()
			d.touch.tracker.cancelContact()
			if d.screenOn {
				if err := d.setScreen(false); err != nil {
					return inputEvent{}, err
				}
				return inputEvent{Kind: inputSleep}, nil
			} else {
				if err := d.setScreen(true); err != nil {
					return inputEvent{}, err
				}
				d.sleepAt = now.Add(screenTimeout)
				d.buttonPressed = pressed
				return inputEvent{Kind: inputWake}, nil
			}
		}
		d.buttonPressed = pressed
		if d.charger.update(readChargerState(), takeChargerInput(), now) {
			return inputEvent{Kind: inputPower}, nil
		}
		d.serviceInput()
		if timeRadioHasUpdate() {
			return inputEvent{Kind: inputRefresh}, nil
		}
		if touch := d.touchEvents.pop(); touch.Activity {
			if !d.screenOn && !d.touchWake {
				continue
			}
			d.sleepAt = now.Add(screenTimeout)
			if !d.screenOn {
				d.touchEvents.clear()
				d.touch.tracker.cancel()
				if err := d.setScreen(true); err != nil {
					return inputEvent{}, err
				}
				return inputEvent{Kind: inputWake}, nil
			}
			// Bound event rate without sleeping through the next touch IRQ.
			pauseWithRadio(touchPollInterval)
			return touch.inputEvent, nil
		}

		if d.screenOn && !now.Before(d.sleepAt) {
			if err := d.setScreen(false); err != nil {
				return inputEvent{}, err
			}
			d.touch.tracker.cancelContact()
			d.touchEvents.clear()
			return inputEvent{Kind: inputSleep}, nil
		}
		if !now.Before(refreshAt) {
			return inputEvent{Kind: inputRefresh}, nil
		}

		var screenOffAt, vibrationEnd time.Time
		if d.screenOn {
			screenOffAt = d.sleepAt
		}
		if d.vibrating {
			vibrationEnd = d.vibrationEnds
		}
		delay := inputIdleDelay(time.Now(), refreshAt, screenOffAt, d.button.releaseAt, vibrationEnd, d.touch.tracker.down, false)
		if !d.charger.due.IsZero() {
			delay = min(delay, max(minimumLoopWait, d.charger.due.Sub(time.Now())))
		}
		waitForInput(delay)
	}
}

// Called between raster strips as well as from Wait. Never dispatch UI actions
// recursively from a draw; queue observations for the normal input loop.
func (d *pineTimeDisplay) serviceInput() {
	// Also bound the pulse during lengthy frame rendering or a slow UI action.
	if d.vibrating && !time.Now().Before(d.vibrationEnds) {
		d.SetVibration(false)
	}
	serviceTimeRadio()
	if !d.screenOn && !d.touchWake {
		return
	}
	if !d.touchEvents.push(d.touch.Poll()) {
		d.touch.tracker.cancel()
	}
}

// Keep the short sync/confirmation window visible while the phone connects.
// The physical button still sleeps immediately and cancels the sync window.
func (d *pineTimeDisplay) KeepAwake() {
	if d.screenOn {
		d.sleepAt = time.Now().Add(screenTimeout)
	}
}

func (d *pineTimeDisplay) SetTouchWake(enabled bool) {
	if d.touchWake == enabled {
		return
	}
	d.touchWake = enabled
	if !d.screenOn {
		d.touchEvents.clear()
		if enabled {
			d.touch.Wake()
		} else {
			d.touch.Sleep()
		}
	}
}

func (d *pineTimeDisplay) SetBrightness(level uint8) {
	level = validBrightness(level)
	if d.brightness == level {
		return
	}
	d.brightness = level
	setBacklight(d.screenOn, level)
}

func (d *pineTimeDisplay) Wake() error {
	d.touchEvents.clear()
	d.touch.tracker.cancelContact()
	if err := d.setScreen(true); err != nil {
		return err
	}
	d.KeepAwake()
	return nil
}

func (d *pineTimeDisplay) SetVibration(on bool) {
	if on && !d.vibrating {
		d.vibrationEnds = time.Now().Add(alertPulseDuration)
	}
	d.vibrating = on
	machine.VIBRATOR_PIN.Set(!on)
}

func (d *pineTimeDisplay) readButton(now time.Time) bool {
	pressLatched := takePortInput(buttonInputMask)
	high := machine.BUTTON_IN.Get()
	// Factory bootloaders may leave the watchdog running. Do not feed it during
	// a long press, preserving the button-held reset/bootloader escape route.
	if !high {
		nrf.WDT.RR[0].Set(0x6E524635)
	}
	return d.button.update(high, pressLatched, now)
}

func (d *pineTimeDisplay) setScreen(on bool) error {
	if on == d.screenOn {
		return nil
	}
	if on {
		configureDisplaySPI()
		machine.LCD_RS.Configure(machine.PinConfig{Mode: machine.PinOutput})
		if err := d.DeviceOf.Sleep(false); err != nil {
			return err
		}
		d.lcdPower.woke(time.Now())
		d.touch.Wake()
		// SLPOUT also requires 5 ms before another LCD command, including
		// when touch-to-wake is on and touch.Wake returns immediately.
		pauseWithRadio(6 * time.Millisecond)
		setBacklight(true, d.brightness)
	} else {
		setBacklight(false, d.brightness)
		if err := d.lcdPower.sleep(time.Now, pauseWithRadio, d.DeviceOf.Sleep); err != nil {
			return err
		}
		// TinyGo's driver waits 5 ms after SLPIN. Add a tick-rounding margin
		// before releasing the bus and D/C pin, as InfiniTime does.
		pauseWithRadio(time.Millisecond)
		suspendDisplaySPI()
		disconnectInput(machine.LCD_RS)
		if !d.touchWake {
			d.touch.Sleep()
		}
	}
	d.screenOn = on
	return nil
}

func configureDisplaySPI() {
	// The sleep workaround resets SPIM0, so restore its full configuration.
	machine.SPI0_SCK_PIN.High()
	machine.SPI0_SCK_PIN.Configure(machine.PinConfig{Mode: machine.PinOutput})
	machine.SPI0_SDO_PIN.Low()
	machine.SPI0_SDO_PIN.Configure(machine.PinConfig{Mode: machine.PinOutput})
	machine.SPI0_SDI_PIN.Configure(machine.PinConfig{Mode: machine.PinInput})
	machine.SPI0.Configure(machine.SPIConfig{
		Frequency: 8_000_000,
		SCK:       machine.SPI0_SCK_PIN,
		SDO:       machine.SPI0_SDO_PIN,
		SDI:       machine.SPI0_SDI_PIN,
		Mode:      3,
	})
}

func suspendDisplaySPI() {
	machine.SPI0.Bus.ENABLE.Set(0)
	// nRF52832 anomaly 89: disabling SPIM after EasyDMA + GPIOTE use can
	// leave a 400–450 uA draw. Nordic requires a peripheral POWER cycle,
	// including the readback, then full reconfiguration before reuse.
	// SPIM0 shares this block with TWI0; the board's I2C uses TWI1.
	power := (*volatile.Register32)(unsafe.Pointer(uintptr(0x40003ffc)))
	power.Set(0)
	_ = power.Get()
	power.Set(1)
	// Match InfiniTime's SpiMaster::Sleep: release the SPI pins and their
	// digital input buffers. CS stays high and LCD reset stays deasserted.
	disconnectInput(machine.SPI0_SCK_PIN)
	disconnectInput(machine.SPI0_SDO_PIN)
	disconnectInput(machine.SPI0_SDI_PIN)
}

func disconnectInput(pin machine.Pin) {
	nrf.P0.PIN_CNF[pin].Set(nrf.GPIO_PIN_CNF_INPUT_Disconnect << nrf.GPIO_PIN_CNF_INPUT_Pos)
}

func setBacklight(on bool, level uint8) {
	channels := backlightChannels(level, on)
	machine.LCD_BACKLIGHT_LOW.Set(channels&1 == 0)
	machine.LCD_BACKLIGHT_MID.Set(channels&2 == 0)
	machine.LCD_BACKLIGHT_HIGH.Set(channels&4 == 0)
}

func (d *pineTimeDisplay) Close() error {
	d.SetVibration(false)
	d.touch.Close()
	return d.setScreen(false)
}

// The controller rotates the pixels without a frame buffer or CPU copy.
func (d *pineTimeDisplay) SetFlipped(flipped bool) error {
	rotation := st7789.Rotation(drivers.Rotation0)
	if flipped {
		rotation = drivers.Rotation180
	}
	if err := d.SetRotation(rotation); err != nil {
		return err
	}
	d.touchEvents.clear()
	d.touch.tracker.cancelContact()
	return nil
}
