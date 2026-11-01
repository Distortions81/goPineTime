//go:build pinetime

package main

import "machine"

func newHealthDevice() healthDevice {
	// InfiniTime also leaves the unused HRS3300 interrupt line as an input.
	machine.Pin(30).Configure(machine.PinConfig{Mode: machine.PinInput})
	return &boardHealthDevice{bus: boardI2C{}, pause: pauseWithRadio}
}
