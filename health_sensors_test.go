package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/motionconfig"
)

// Model the feature-memory address window, rather than merely checking a list
// of register writes: wrong word addressing/chunk offsets corrupt the image.
type healthBus struct {
	heart        [256]byte
	motion       [256]byte
	feature      [16384]byte
	loaded       []byte
	failRegister byte
	failOnce     bool
	rejectConfig bool
	chunks       int
}

func (b *healthBus) Tx(address uint16, w, r []byte) error {
	if len(w) == 0 {
		return errors.New("missing register")
	}
	reg := w[0]
	if b.failOnce && reg == b.failRegister {
		b.failOnce = false
		return errors.New("injected bus failure")
	}
	registers := b.motion[:]
	if address == heartRateAddress {
		registers = b.heart[:]
	}
	if address == motionAddress && reg == 0x5e {
		offset := (int(b.motion[0x5b]&15) | int(b.motion[0x5c])<<4) * 2
		if len(r) > 0 {
			copy(r, b.feature[offset:])
			return nil
		}
		copy(b.feature[offset:], w[1:])
		b.chunks++
		return nil
	}
	if len(r) > 0 {
		copy(r, registers[int(reg):])
		return nil
	}
	copy(registers[int(reg):], w[1:])
	if address == motionAddress && reg == 0x59 && w[1] == 1 {
		b.loaded = append([]byte(nil), b.feature[:]...)
		b.motion[0x2a] = 1
		if b.rejectConfig {
			b.motion[0x2a] = 2
		}
		b.motion[0x5b], b.motion[0x5c] = 3, 0x12
	}
	return nil
}

func TestHealthSensorRegistersAndStepFirmware(t *testing.T) {
	for _, id := range []byte{0x11, 0x13} {
		b := &healthBus{}
		b.heart[0] = 0x21
		b.motion[0] = id
		d := boardHealthDevice{bus: b, pause: func(time.Duration) {}}
		if err := d.SetHeart(true); err != nil {
			t.Fatal(err)
		}
		if b.heart[1] != 0xd0 || b.heart[0x0c] != 0x2f || b.heart[0x16] != 0x77 {
			t.Fatal("HRS3300 conversion not configured")
		}
		b.heart[9], b.heart[10], b.heart[15] = 0x12, 0xa3, 0xc4
		b.heart[8], b.heart[0x0d], b.heart[0x0e] = 3, 2, 1
		if raw, err := d.ReadHeart(); err != nil || raw != 0x1234 {
			t.Fatal("wrong HRS3300 sample decoding", raw, err)
		}
		if d.HeartAmbient() != 4121 || !d.HeartConfig().verified {
			t.Fatal("ambient channel/configuration readback not captured")
		}
		if err := d.SetHeart(false); err != nil || b.heart[1]&0x80 != 0 || b.heart[0x0c] != 0 {
			t.Fatal("LED/conversion did not power off", err)
		}
		if err := d.SetSteps(true); err != nil {
			t.Fatal(err)
		}
		config := motionconfig.BMA423
		if id == 0x13 {
			config = motionconfig.BMA425
		}
		if !bytes.Equal(b.loaded[:len(config)], []byte(config)) {
			t.Fatal("configuration firmware corrupted", id)
		}
		if b.chunks != len(config)/16+1 || b.motion[0x40] != 0x28 || b.motion[0x7d] != 4 || b.motion[0x7c] != 1 {
			t.Fatal("step counting power/ODR incorrect", id)
		}
		base := (0x12<<4 | 3) * 2
		if b.feature[base+0x3b]&0x18 != 0x10 {
			t.Fatal("step feature not enabled at ASIC window base")
		}
		binary.LittleEndian.PutUint32(b.motion[0x1e:0x22], 123456)
		if count, err := d.ReadSteps(); err != nil || count != 123456 {
			t.Fatal("wrong hardware counter", count, err)
		}
		if err := d.SetSteps(false); err != nil || b.motion[0x7d] != 0 || b.motion[0x7c] != 1 {
			t.Fatal("motion power down failed")
		}
	}
}

func TestHealthSensorFailureCleanup(t *testing.T) {
	for _, reg := range []byte{0x59, 0x5b, 0x5e, 0x40, 0x7d} {
		b := &healthBus{failRegister: reg, failOnce: true}
		b.motion[0] = 0x11
		d := boardHealthDevice{bus: b, pause: func(time.Duration) {}}
		if err := d.SetSteps(true); err == nil {
			t.Fatal("ignored step initialization failure", reg)
		}
		if b.motion[0x7d] != 0 {
			t.Fatal("failure left acceleration enabled", reg)
		}
	}
	for _, reg := range []byte{0x16, 0x17, 0x0c} {
		b := &healthBus{failRegister: reg, failOnce: true}
		b.heart[1], b.heart[0x0c] = 0xd0, 0x2f
		b.heart[0] = 0x21
		d := boardHealthDevice{bus: b, pause: func(time.Duration) {}}
		if err := d.SetHeart(true); err == nil {
			t.Fatal("ignored heart initialization failure", reg)
		}
		if b.heart[0x0c] != 0 || b.heart[1]&0x80 != 0 {
			t.Fatal("failure left LED/conversions enabled", reg)
		}
	}
	b := &healthBus{rejectConfig: true}
	b.motion[0] = 0x11
	d := boardHealthDevice{bus: b, pause: func(time.Duration) {}}
	if err := d.SetSteps(true); err == nil || b.motion[0x7d] != 0 {
		t.Fatal("ASIC initialization failure was ignored")
	}
	b.motion[0] = 0xff
	if err := d.SetSteps(true); err == nil {
		t.Fatal("unsupported accelerometer accepted")
	}
}
