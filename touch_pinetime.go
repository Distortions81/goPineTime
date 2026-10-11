//go:build pinetime

package main

import (
	"device/nrf"
	"machine"
	"time"
)

const (
	touchResetPin = machine.Pin(10)
	touchIRQPin   = machine.Pin(28)
)

type touchController struct {
	tracker touchTracker
	ready   bool
	asleep  bool
}

// TinyGo leaves TWI enabled after Tx. All board sensors share this bus; gate the
// peripheral around completed transactions, including failed accesses.
type boardI2C struct{}

func (boardI2C) Tx(addr uint16, w, r []byte) error {
	machine.I2C1.Bus.ENABLE.Set(nrf.TWI_ENABLE_ENABLE_Enabled)
	err := machine.I2C1.Tx(addr, w, r)
	machine.I2C1.Bus.ENABLE.Set(0)
	return err
}

func configureBoardI2C() error {
	if err := machine.I2C1.Configure(machine.I2CConfig{
		Frequency: 100_000,
		SDA:       machine.SDA_PIN,
		SCL:       machine.SCL_PIN,
	}); err != nil {
		return err
	}
	machine.I2C1.Bus.ENABLE.Set(0)
	return nil
}

func (t *touchController) Configure() error {
	touchResetPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	resetTouchController(touchResetPin.Set, pauseWithRadio)

	if err := configureTouchRegisters(boardI2C{}, pauseWithRadio); err != nil {
		return err
	}

	touchIRQPin.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	return t.enableInterrupt()
}

func (t *touchController) enableInterrupt() error {
	enableTouchInterrupt()
	t.ready = true
	return nil
}

func (t *touchController) Sleep() {
	if t.asleep {
		return
	}
	disableTouchInterrupt()
	t.ready, t.asleep = false, true
	t.tracker = touchTracker{}
	if err := sleepTouchController(boardI2C{}, touchResetPin.Set, pauseWithRadio); err != nil {
		// A failed controller must not keep generating IRQs or wake the LCD.
		// Hold reset until the next wake can try a fresh initialization.
		touchResetPin.Low()
	}
}

func (t *touchController) Wake() {
	if !t.asleep {
		return
	}
	t.asleep = false
	resetTouchController(touchResetPin.Set, pauseWithRadio)
	if err := configureTouchRegisters(boardI2C{}, pauseWithRadio); err == nil {
		_ = t.enableInterrupt()
	}
}

func (t *touchController) Poll() touchEvent {
	if !t.ready {
		return touchEvent{}
	}
	pending := takePortInput(touchInputMask)

	return t.tracker.poll(boardI2C{}, pending, time.Now())
}

func (t *touchController) Close() {
	t.Sleep()
}
