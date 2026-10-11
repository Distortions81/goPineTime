// Package ota implements the SHA-256 MCUboot image layout used by InfiniTime's
// stock bootloader. SHA-256 detects damage; it does not authenticate the author.
package ota

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/Distortions81/goPine/internal/imagesha256"
)

const (
	Primary       = 0x8000
	HeaderSize    = 32
	VectorAddress = Primary + HeaderSize
	SlotSize      = 0x74000
	SectorSize    = 0x1000
	RecoverySize  = 0x40000
	Secondary     = 0x40000
	// Reserve a whole sector for MCUboot's swap status and trailer.
	MaxImageSize = SlotSize - SectorSize
	ImageOK      = SlotSize - 24
	MagicOffset  = SlotSize - 16
	ImageMagic   = 0x96f3b83d
)

var BootMagic = [16]byte{0x77, 0xc2, 0x95, 0xf3, 0x60, 0xd2, 0xef, 0x7f, 0x35, 0x52, 0x50, 0x0f, 0x2c, 0xb6, 0x79, 0x80}

type Image struct {
	Size   int64
	Digest [32]byte
}

// Validate streams the image through a small buffer; the watch has only 64KB
// of RAM. Only the stock, unencrypted, SHA-256-only image format is accepted.
func Validate(r io.ReaderAt, offset, limit int64) (Image, error) {
	var scratch [256]byte
	return ValidateWithScratch(r, offset, limit, scratch[:])
}

// ValidateWithScratch borrows caller-owned storage for the duration of the
// synchronous read. Header, TLV and hash reads never overlap, so they share it.
// A receiver can validate repeatedly without allocating new DMA buffers.
func ValidateWithScratch(r io.ReaderAt, offset, limit int64, scratch []byte) (Image, error) {
	var result Image
	if len(scratch) < HeaderSize+8 {
		return result, errors.New("image scratch too small")
	}
	header := scratch[:HeaderSize+8]
	if limit < int64(len(header)+40) {
		return result, errors.New("image too short")
	}
	if err := readAt(r, header[:], offset); err != nil {
		return result, err
	}
	le := binary.LittleEndian
	bodySize := int64(le.Uint32(header[12:16]))
	if le.Uint32(header[:4]) != ImageMagic || le.Uint32(header[4:8]) != 0 ||
		le.Uint16(header[8:10]) != HeaderSize || le.Uint16(header[10:12]) != 0 ||
		le.Uint32(header[16:20]) != 0 || bodySize < 8 || bodySize > limit-HeaderSize-40 {
		return result, errors.New("unsupported MCUboot image")
	}
	if err := ValidateVectors(header[HeaderSize:], uint32(bodySize)); err != nil {
		return result, err
	}
	tlv := scratch[:40]
	if err := readAt(r, tlv[:], offset+HeaderSize+bodySize); err != nil {
		return result, err
	}
	if le.Uint16(tlv[:2]) != 0x6907 || le.Uint16(tlv[2:4]) != 40 ||
		tlv[4] != 0x10 || tlv[5] != 0 || le.Uint16(tlv[6:8]) != 32 {
		return result, errors.New("missing SHA-256 image TLV")
	}
	var expected [32]byte
	copy(expected[:], tlv[8:])
	var h imagesha256.Digest
	h.Reset()
	buffer := scratch
	for pos := int64(0); pos < HeaderSize+bodySize; {
		n := min(int64(len(buffer)), HeaderSize+bodySize-pos)
		if err := readAt(r, buffer[:n], offset+pos); err != nil {
			return result, err
		}
		_, _ = h.Write(buffer[:n])
		pos += n
	}
	result.Digest = h.Sum()
	for i, b := range result.Digest {
		if b != expected[i] {
			return Image{}, errors.New("image SHA-256 mismatch")
		}
	}
	result.Size = HeaderSize + bodySize + int64(len(tlv))
	return result, nil
}

func ValidateVectors(vectors []byte, bodySize uint32) error {
	if len(vectors) < 8 || bodySize > MaxImageSize-HeaderSize-40 {
		return errors.New("invalid vector table size")
	}
	sp := binary.LittleEndian.Uint32(vectors[:4])
	reset := binary.LittleEndian.Uint32(vectors[4:8])
	if sp <= 0x20000000 || sp > 0x20010000 || sp%8 != 0 || reset&1 == 0 ||
		reset&^1 < VectorAddress || reset&^1 >= VectorAddress+bodySize {
		return errors.New("image is not linked for PineTime MCUboot at 0x8020")
	}
	return nil
}

func readAt(r io.ReaderAt, p []byte, offset int64) error {
	n, err := r.ReadAt(p, offset)
	if n != len(p) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}
