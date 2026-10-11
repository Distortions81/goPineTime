//go:build pinetime && bletime && !provision

package main

import (
	"errors"
	"time"

	ble "github.com/Distortions81/goPine/internal/ble_nimble"
	"github.com/Distortions81/goPine/internal/timesync"
	"github.com/Distortions81/goPine/internal/weather"
)

type pineTimeRadio struct{}

func (pineTimeRadio) StartWeather(value [10]byte, battery uint8, window time.Duration) error {
	if rc := ble.StartWeather(value, battery, uint32(window/time.Millisecond)); rc != 0 {
		return errors.New("BLE weather start: " + decimal(rc))
	}
	return nil
}
func (pineTimeRadio) TakeWeather() ([weather.MaxPacket]byte, int, error) {
	value, size := ble.TakeWeather()
	var err error
	if rc := ble.Error(); rc != 0 {
		err = errors.New("BLE weather: " + decimal(rc))
	}
	return value, size, err
}

func newTimeRadio() timeRadio {
	ble.SetFirmwareVersion(firmwareVersion)
	return pineTimeRadio{}
}
func (pineTimeRadio) Start(value [10]byte, battery uint8) error {
	if rc := ble.Start(value, battery, uint32(timesync.Window/time.Millisecond)); rc != 0 {
		return errors.New("BLE start: " + decimal(int(rc)))
	}
	return nil
}
func (pineTimeRadio) Stop()              { ble.Stop() }
func (pineTimeRadio) Service()           { ble.Service() }
func (pineTimeRadio) ServiceController() { ble.ServiceController() }
func (pineTimeRadio) Busy() bool         { return ble.Busy() }
func (pineTimeRadio) Take() ([10]byte, int, time.Duration, error) {
	value, size, age := ble.Take()
	var err error
	if rc := ble.Error(); rc != 0 {
		err = errors.New("BLE: " + decimal(int(rc)))
	}
	return value, size, time.Duration(age) * time.Millisecond, err
}

func (pineTimeRadio) StartPhone(value [10]byte, battery uint8, window time.Duration) error {
	if rc := ble.StartPhone(value, battery, uint32(window/time.Millisecond)); rc != 0 {
		return errors.New("BLE phone: " + decimal(rc))
	}
	return nil
}
func (pineTimeRadio) TakeMusic() ([87]byte, bool, error) {
	value, changed := ble.TakeMusic()
	var err error
	if rc := ble.Error(); rc != 0 {
		err = errors.New("BLE music: " + decimal(rc))
	}
	return value, changed, err
}
func (pineTimeRadio) TakeNotification() ([103]byte, int)      { return ble.TakeNotification() }
func (pineTimeRadio) TakeAppleNotification() ([148]byte, int) { return ble.TakeAppleNotification() }
func (pineTimeRadio) UpdateBattery(value uint8)               { ble.UpdateBattery(value) }
func (pineTimeRadio) StartUpdate(battery uint8) error {
	if rc := ble.StartUpdate(battery); rc != 0 {
		return errors.New("update Bluetooth failed")
	}
	return nil
}
func (pineTimeRadio) TakeUpdate(data *[244]byte) (int, int) { return ble.TakeUpdate(data) }
func (pineTimeRadio) UpdateStatus(status [16]byte)          { ble.UpdateStatus(status) }
func (pineTimeRadio) MusicCommand(command byte, generation uint32) error {
	if rc := ble.MusicCommand(command, generation); rc != 0 {
		return errors.New("BLE command: " + decimal(rc))
	}
	return nil
}
func (pineTimeRadio) HasUpdate() bool            { return ble.HasUpdate() }
func (pineTimeRadio) AcknowledgeUpdates()        { ble.AcknowledgeUpdates() }
func (pineTimeRadio) IdleDelay() time.Duration   { return time.Duration(ble.IdleMS()) * time.Millisecond }
func (pineTimeRadio) PhoneSetup()                { ble.PhoneSetup() }
func (pineTimeRadio) PairingCode() uint32        { return ble.PairingCode() }
func (pineTimeRadio) PairingDiagnostics() uint32 { return ble.PairingDiagnostics() }
func (pineTimeRadio) PairingProgress() uint16    { return ble.PairingProgress() }
func (pineTimeRadio) BondStatus() int            { return ble.BondStatus() }
func (pineTimeRadio) BondCount() int             { return ble.BondCount() }
func (pineTimeRadio) PeerCount() int             { return ble.PeerCount() }
func (pineTimeRadio) TrustedPeerCount() int      { return ble.TrustedPeerCount() }
func (pineTimeRadio) CancelPairing() error {
	if ble.CancelPairing() != 0 {
		return errors.New("Could not cancel pairing")
	}
	return nil
}
func (pineTimeRadio) ForgetPhone() error {
	if ble.ForgetPhone() != 0 {
		return errors.New("Could not clear pairings")
	}
	return nil
}
