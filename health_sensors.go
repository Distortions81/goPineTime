package main

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/Distortions81/goPine/internal/motionconfig"
)

// Board register sequences follow the pinned InfiniTime HRS3300 and Bosch
// BMA42x drivers. The sensor's feature engine counts steps while the MCU sleeps.
type boardHealthDevice struct {
	bus         touchBus
	pause       func(time.Duration)
	packet      [17]byte
	heartConfig heartSensorConfig
	ambient     uint32
}

type heartSensorConfig struct {
	id, enable, driver, resolution, gain byte
	verified                             bool
}

func (d *boardHealthDevice) HeartConfig() heartSensorConfig { return d.heartConfig }
func (d *boardHealthDevice) HeartAmbient() uint32           { return d.ambient }

func (d *boardHealthDevice) write(address uint16, reg, value byte) error {
	d.packet[0], d.packet[1] = reg, value
	return d.bus.Tx(address, d.packet[:2], nil)
}

func (d *boardHealthDevice) SetHeart(enabled bool) (err error) {
	if !enabled {
		return shutdownHeartRate(d.bus)
	}
	d.heartConfig = heartSensorConfig{}
	defer func() {
		if err != nil {
			_ = shutdownHeartRate(d.bus)
		}
	}()
	if err = shutdownHeartRate(d.bus); err != nil {
		return err
	}
	var id [1]byte
	if err = d.bus.Tx(heartRateAddress, []byte{0}, id[:]); err != nil {
		return err
	}
	d.heartConfig.id = id[0]
	if id[0] != 0x21 {
		return errors.New("unknown heart sensor")
	}
	// InfiniTime lets the previous conversion settle after disabling HRS.
	d.pause(100 * time.Millisecond)
	for _, setting := range [...][2]byte{{0x01, 0x50}, {0x16, 0x77}, {0x17, 0x00}, {0x0c, 0x2f}, {0x01, 0xd0}} {
		if err = d.write(heartRateAddress, setting[0], setting[1]); err != nil {
			return err
		}
	}
	// Verify actual register readback, rather than treating an I2C ACK or a
	// lit LED as proof that acquisition was configured correctly.
	var value [1]byte
	for _, setting := range [...][3]byte{{0x01, 0xd0, 0xf8}, {0x0c, 0x20, 0x60}, {0x16, 0x07, 0x0f}, {0x17, 0x00, 0x1c}} {
		if err = d.bus.Tx(heartRateAddress, setting[:1], value[:]); err != nil {
			return err
		}
		switch setting[0] {
		case 0x01:
			d.heartConfig.enable = value[0]
		case 0x0c:
			d.heartConfig.driver = value[0]
		case 0x16:
			d.heartConfig.resolution = value[0]
		case 0x17:
			d.heartConfig.gain = value[0]
		}
		if value[0]&setting[2] != setting[1] {
			return errors.New("heart configuration readback failed")
		}
	}
	d.heartConfig.verified = true
	return nil
}

func (d *boardHealthDevice) ReadHeart() (uint16, error) {
	// Read both channels atomically, matching InfiniTime's HRS3300 driver.
	var data [8]byte
	if err := d.bus.Tx(heartRateAddress, []byte{0x08}, data[:]); err != nil {
		return 0, err
	}
	d.ambient = uint32(data[5]&63)<<11 | uint32(data[0])<<3 | uint32(data[6]&7)
	return uint16(data[1])<<8 | uint16(data[2]&15)<<4 | uint16(data[7]&15), nil
}

func (d *boardHealthDevice) SetSteps(enabled bool) (err error) {
	if !enabled {
		return shutdownMotion(d.bus, d.pause)
	}
	defer func() {
		if err != nil {
			_ = shutdownMotion(d.bus, d.pause)
		}
	}()
	if err = shutdownMotion(d.bus, d.pause); err != nil {
		return err
	}
	var id [1]byte
	if err = d.bus.Tx(motionAddress, []byte{0}, id[:]); err != nil {
		return err
	}
	config := motionconfig.BMA423
	if id[0] == 0x13 {
		config = motionconfig.BMA425
	}
	if err = d.write(motionAddress, 0x7c, 0); err != nil {
		return err
	}
	d.pause(time.Millisecond)
	if err = d.write(motionAddress, 0x59, 0); err != nil {
		return err
	}
	// 16-byte chunks bound the bus buffers and allow radio IRQ work between
	// transfers. Firmware lives in flash as strings, never a RAM-sized copy.
	for offset := 0; offset < len(config); offset += 16 {
		if err = d.featureAddress(uint16(offset / 2)); err != nil {
			return err
		}
		d.packet[0] = 0x5e
		copy(d.packet[1:], config[offset:offset+16])
		if err = d.bus.Tx(motionAddress, d.packet[:], nil); err != nil {
			return err
		}
		d.pause(time.Microsecond)
	}
	if err = d.write(motionAddress, 0x59, 1); err != nil {
		return err
	}
	d.pause(150 * time.Millisecond)
	var status [1]byte
	if err = d.bus.Tx(motionAddress, []byte{0x2a}, status[:]); err != nil {
		return err
	}
	if status[0]&15 != 1 {
		return errors.New("step configuration rejected")
	}
	// The ASIC supplies the feature window base after initialization. Read
	// it, then update byte 0x3b without disturbing neighboring feature bits.
	var base [2]byte
	if err = d.bus.Tx(motionAddress, []byte{0x5b}, base[:]); err != nil {
		return err
	}
	start := uint16(base[0]&15) | uint16(base[1])<<4
	if err = d.featureAddress(start + 0x3a/2); err != nil {
		return err
	}
	var feature [2]byte
	if err = d.bus.Tx(motionAddress, []byte{0x5e}, feature[:]); err != nil {
		return err
	}
	feature[1] = (feature[1] &^ 0x08) | 0x10 // Counter enabled, per-step IRQ disabled.
	if err = d.featureAddress(start + 0x3a/2); err != nil {
		return err
	}
	d.packet[0], d.packet[1], d.packet[2] = 0x5e, feature[0], feature[1]
	if err = d.bus.Tx(motionAddress, d.packet[:3], nil); err != nil {
		return err
	}
	if err = d.featureAddress(start); err != nil {
		return err
	}
	for _, setting := range [...][2]byte{{0x40, 0x28}, {0x41, 0x00}, {0x7d, 0x04}, {0x7c, 0x01}} {
		if err = d.write(motionAddress, setting[0], setting[1]); err != nil {
			return err
		}
	}
	d.pause(time.Millisecond)
	return nil
}

func (d *boardHealthDevice) featureAddress(word uint16) error {
	if err := d.write(motionAddress, 0x5b, byte(word&15)); err != nil {
		return err
	}
	return d.write(motionAddress, 0x5c, byte(word>>4))
}

func (d *boardHealthDevice) ReadSteps() (uint32, error) {
	var data [4]byte
	if err := d.bus.Tx(motionAddress, []byte{0x1e}, data[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}
