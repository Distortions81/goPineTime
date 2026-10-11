//go:build pinetime && (mcuboot || provision)

package main

import (
	"device/nrf"
	"errors"
	"machine"
	"time"

	"github.com/Distortions81/goPine/internal/ota"
)

// The display and flash share SPI0. All access is on the UI goroutine; touch
// interrupts only set a flag. Never draw while a flash transaction holds CS.
type externalFlash struct{}

func wakeFlash() error {
	err := flashTransaction([]byte{0xab}, nil, nil)
	pauseWithRadio(time.Millisecond)
	return err
}

func openFlash() (*externalFlash, error) {
	if err := wakeFlash(); err != nil {
		return nil, err
	}
	var id [3]byte
	if err := flashTransaction([]byte{0x9f}, nil, id[:]); err != nil {
		return nil, err
	}
	// The stock bootloader supports multiple PineTime vendors. All must be
	// 32Mbit chips with the 0x40 memory type and 0x16 JEDEC capacity code.
	if id[0] == 0 || id[0] == 0xff || id[1] != 0x40 || id[2] != 0x16 {
		_ = flashTransaction([]byte{0xb9}, nil, nil)
		return nil, errors.New("unsupported external flash")
	}
	f := &externalFlash{}
	if err := f.waitReady(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *externalFlash) Close() {
	_ = flashTransaction([]byte{0xb9}, nil, nil)
}

func flashTransaction(command, data, read []byte) error {
	cs := machine.Pin(5)
	cs.Low()
	defer cs.High()
	if err := machine.SPI0.Tx(command, nil); err != nil {
		return err
	}
	if len(data) != 0 {
		return machine.SPI0.Tx(data, nil)
	}
	if len(read) != 0 {
		return machine.SPI0.Tx(nil, read)
	}
	return nil
}

func flashCommand(op byte, offset int64) []byte {
	return []byte{op, byte(offset >> 16), byte(offset >> 8), byte(offset)}
}

func flashBounds(offset int64, size int) bool {
	// No code in goPine's updater may touch InfiniTime's filesystem.
	return offset >= 0 && offset <= ota.Secondary+ota.SlotSize &&
		size >= 0 && int64(size) <= ota.Secondary+ota.SlotSize-offset
}

func (f *externalFlash) ReadAt(p []byte, offset int64) (int, error) {
	if !flashBounds(offset, len(p)) {
		return 0, errors.New("flash read out of bounds")
	}
	nrf.WDT.RR[0].Set(0x6E524635)
	if err := flashTransaction(flashCommand(0x03, offset), nil, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (f *externalFlash) WriteAt(p []byte, offset int64) (int, error) {
	if !flashBounds(offset, len(p)) {
		return 0, errors.New("flash write out of bounds")
	}
	written := 0
	for len(p) > 0 {
		n := min(len(p), 256-int(offset%256))
		if err := f.writeEnable(); err != nil {
			return written, err
		}
		if err := flashTransaction(flashCommand(0x02, offset), p[:n], nil); err != nil {
			return written, err
		}
		if err := f.waitReady(); err != nil {
			return written, err
		}
		written += n
		offset += int64(n)
		p = p[n:]
	}
	return written, nil
}

func (f *externalFlash) EraseSector(offset int64) error {
	if !flashBounds(offset, ota.SectorSize) || offset%ota.SectorSize != 0 {
		return errors.New("flash erase out of bounds")
	}
	if err := f.writeEnable(); err != nil {
		return err
	}
	if err := flashTransaction(flashCommand(0x20, offset), nil, nil); err != nil {
		return err
	}
	return f.waitReady()
}

func (f *externalFlash) status() (byte, error) {
	var status [1]byte
	err := flashTransaction([]byte{0x05}, nil, status[:])
	return status[0], err
}

func (f *externalFlash) waitReady() error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		nrf.WDT.RR[0].Set(0x6E524635)
		status, err := f.status()
		if err != nil {
			return err
		}
		if status&1 == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return errors.New("external flash timed out")
		}
		pauseWithRadio(time.Millisecond)
	}
}

func (f *externalFlash) writeEnable() error {
	if err := f.waitReady(); err != nil {
		return err
	}
	if err := flashTransaction([]byte{0x06}, nil, nil); err != nil {
		return err
	}
	status, err := f.status()
	if err != nil {
		return err
	}
	if status&2 == 0 {
		return errors.New("external flash is write protected")
	}
	return nil
}
