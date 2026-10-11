package ota

import (
	"encoding/binary"
	"errors"
)

// Receiver stages an application in external secondary flash. It never writes
// recovery, the running application, settings or the bootloader. Callers must
// only create it while the running image is confirmed.
type Receiver struct {
	flash                     Flash
	Total, Offset, Session    uint32
	erased                    uint32
	verified, commitAttempted bool
	Version                   [8]byte
	readback                  [236]byte
}

func (r *Receiver) Begin(f Flash, confirmed bool, total, session uint32) error {
	if !confirmed || total < 80 || total > MaxImageSize || session == 0 {
		return errors.New("invalid update request")
	}
	*r = Receiver{}
	// Invalidate any old swap request before modifying a single image byte.
	if err := f.EraseSector(Secondary + SlotSize - SectorSize); err != nil {
		return err
	}
	if err := checkErasedTrailer(f); err != nil {
		return err
	}
	*r = Receiver{flash: f, Total: total, Session: session}
	return nil
}

func (r *Receiver) Write(session, offset uint32, data []byte) error {
	if r.flash == nil || r.verified || session != r.Session || offset != r.Offset || len(data) == 0 || len(data) > 236 || uint32(len(data)) > r.Total-r.Offset {
		return errors.New("unexpected update chunk")
	}
	for len(data) > 0 {
		if r.Offset == r.erased {
			if err := r.flash.EraseSector(int64(Secondary) + int64(r.erased)); err != nil {
				return err
			}
			r.erased += SectorSize
		}
		n := min(len(data), int(r.erased-r.Offset))
		if err := writeAt(r.flash, data[:n], int64(Secondary)+int64(r.Offset)); err != nil {
			return err
		}
		// An acknowledged offset means the flash bytes were read back, not just
		// accepted by the radio. A reconnect can resume at precisely this byte.
		if err := readAt(r.flash, r.readback[:n], int64(Secondary)+int64(r.Offset)); err != nil {
			return err
		}
		for i, b := range data[:n] {
			if r.readback[i] != b {
				return errors.New("update readback failed")
			}
		}
		r.Offset += uint32(n)
		data = data[n:]
	}
	return nil
}

func (r *Receiver) Verify(session uint32) error {
	if r.flash == nil || session != r.Session || r.Offset != r.Total {
		return errors.New("update incomplete")
	}
	image, err := Validate(r.flash, Secondary, int64(r.Total))
	if err != nil {
		return err
	}
	if int64(r.Total) != image.Size {
		return errors.New("unexpected image tail")
	}
	if err := readAt(r.flash, r.Version[:], Secondary+20); err != nil {
		return err
	}
	r.verified = true
	return nil
}

// Commit is called only by an explicit on-watch Install action with a fresh
// power check. image_ok stays erased, so MCUboot tests the image and reverts it
// unless the new application receives KEEP. Magic is written last.
func (r *Receiver) Commit(confirmed bool) error {
	if !confirmed || !r.verified {
		return errors.New("update not verified")
	}
	if err := r.Verify(r.Session); err != nil {
		return err
	}
	r.commitAttempted = true
	if err := writeAt(r.flash, BootMagic[:], Secondary+MagicOffset); err != nil {
		return err
	}
	var magic [16]byte
	if err := readAt(r.flash, magic[:], Secondary+MagicOffset); err != nil {
		return err
	}
	if magic != BootMagic {
		return errors.New("update commit failed")
	}
	return nil
}

func (r *Receiver) Cancel() error {
	// Normally the trailer is already erased. If Install had an uncertain
	// result, erase its request before allowing cancellation to be reported.
	if r.commitAttempted {
		if err := r.flash.EraseSector(Secondary + SlotSize - SectorSize); err != nil {
			return err
		}
		if err := checkErasedTrailer(r.flash); err != nil {
			return err
		}
	}
	*r = Receiver{}
	return nil
}

func checkErasedTrailer(f Flash) error {
	var data [256]byte
	for offset := int64(Secondary + SlotSize - SectorSize); offset < Secondary+SlotSize; offset += int64(len(data)) {
		if err := readAt(f, data[:], offset); err != nil {
			return err
		}
		for _, b := range data {
			if b != 255 {
				return errors.New("update trailer not erased")
			}
		}
	}
	return nil
}

func (r *Receiver) VersionParts() (uint8, uint8, uint16, uint32) {
	return r.Version[0], r.Version[1], binary.LittleEndian.Uint16(r.Version[2:]), binary.LittleEndian.Uint32(r.Version[4:])
}
