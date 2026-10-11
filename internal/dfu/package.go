// Package dfu creates native goPine OTA packages and wired bootstrap images.
// This host-side package is deliberately separate from the watch's OTA code.
package dfu

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Distortions81/goPine/internal/ota"
)

// CheckTinyGoStack rejects packages built with the upstream target's unsafe
// fallback stack. Hardware setup demonstrated that 2KB overflows into globals.
// Keep this check in the packager as well as the project target configuration.
func CheckTinyGoStack(r io.ReaderAt, minimum uint32) error {
	f, err := elf.NewFile(r)
	if err != nil {
		return err
	}
	defer f.Close()
	section := f.Section(".tinygo_stacksizes")
	if section == nil {
		return errors.New("missing TinyGo task stack sizes")
	}
	data, err := section.Data()
	if err != nil {
		return err
	}
	return checkStackSizes(data, minimum)
}

func checkStackSizes(data []byte, minimum uint32) error {
	if len(data) == 0 || len(data)%4 != 0 {
		return errors.New("invalid TinyGo task stack sizes")
	}
	for offset := 0; offset < len(data); offset += 4 {
		if size := binary.LittleEndian.Uint32(data[offset:]); size < minimum {
			return fmt.Errorf("TinyGo task stack is %d bytes; goPine requires at least %d (use project targets)", size, minimum)
		}
	}
	return nil
}

// ELFBody accepts linked firmware, not a raw pinetime binary accidentally
// built at zero. RAM initializers use their flash load (physical) addresses.
func ELFBody(r io.ReaderAt) ([]byte, error) {
	f, err := elf.NewFile(r)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if f.Machine != elf.EM_ARM || f.Type != elf.ET_EXEC || f.ByteOrder != binary.LittleEndian {
		return nil, errors.New("expected little-endian ARM executable ELF")
	}
	end := uint64(ota.VectorAddress)
	start := ^uint64(0)
	// LLD may emit a read-only PT_LOAD at zero containing only ELF/program
	// headers. It is not firmware. Only segments containing allocated,
	// file-backed sections belong in the flash image.
	isFirmware := func(p *elf.Prog) bool {
		if p.Type != elf.PT_LOAD || p.Filesz == 0 {
			return false
		}
		for _, s := range f.Sections {
			if s.Flags&elf.SHF_ALLOC != 0 && s.Type != elf.SHT_NOBITS && s.Size != 0 &&
				s.Offset >= p.Off && s.Offset-p.Off < p.Filesz {
				return true
			}
		}
		return false
	}
	for _, p := range f.Progs {
		if !isFirmware(p) {
			continue
		}
		if p.Paddr < ota.VectorAddress || p.Paddr >= ota.Primary+ota.MaxImageSize-40 ||
			p.Filesz > ota.Primary+ota.MaxImageSize-40-p.Paddr {
			return nil, fmt.Errorf("ELF segment at %#x is outside the MCUboot image area", p.Paddr)
		}
		start = min(start, p.Paddr)
		end = max(end, p.Paddr+p.Filesz)
	}
	if start != ota.VectorAddress {
		return nil, errors.New("ELF vectors must start at 0x8020; use targets/pinetime-mcuboot.json")
	}
	body := bytes.Repeat([]byte{0xff}, int(end-ota.VectorAddress))
	used := make([]bool, len(body))
	for _, p := range f.Progs {
		if !isFirmware(p) {
			continue
		}
		offset := p.Paddr - ota.VectorAddress
		for i := offset; i < offset+p.Filesz; i++ {
			if used[i] {
				return nil, errors.New("overlapping ELF load segments")
			}
			used[i] = true
		}
		if _, err := io.ReadFull(p.Open(), body[offset:offset+p.Filesz]); err != nil {
			return nil, err
		}
	}
	if err := ota.ValidateVectors(body, uint32(len(body))); err != nil {
		return nil, err
	}
	return body, nil
}

// Version is MCUboot's major.minor.revision[+build] version tuple.
type Version struct {
	Major, Minor uint8
	Revision     uint16
	Build        uint32
}

func ParseVersion(s string) (Version, error) {
	var v Version
	base, build, hasBuild := strings.Cut(s, "+")
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return v, errors.New("version must be major.minor.revision[+build]")
	}
	if !hasBuild {
		build = "0"
	}
	parts = append(parts, build)
	values := [4]uint64{}
	for i, size := range []int{8, 8, 16, 32} {
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				return v, errors.New("version components must be unsigned decimal numbers")
			}
		}
		n, err := strconv.ParseUint(parts[i], 10, size)
		if err != nil {
			return v, fmt.Errorf("invalid version: %w", err)
		}
		values[i] = n
	}
	return Version{uint8(values[0]), uint8(values[1]), uint16(values[2]), uint32(values[3])}, nil
}

func Image(body []byte, version Version) ([]byte, error) {
	if err := ota.ValidateVectors(body, uint32(len(body))); err != nil {
		return nil, err
	}
	bodySize := (len(body) + 3) &^ 3
	if ota.HeaderSize+bodySize+40 > ota.MaxImageSize {
		return nil, errors.New("image overlaps MCUboot trailer")
	}
	image := make([]byte, ota.HeaderSize+bodySize+40)
	le := binary.LittleEndian
	le.PutUint32(image[:4], ota.ImageMagic)
	le.PutUint16(image[8:10], ota.HeaderSize)
	le.PutUint32(image[12:16], uint32(bodySize))
	image[20], image[21] = version.Major, version.Minor
	le.PutUint16(image[22:24], version.Revision)
	le.PutUint32(image[24:28], version.Build)
	copy(image[ota.HeaderSize:], body)
	for i := ota.HeaderSize + len(body); i < ota.HeaderSize+bodySize; i++ {
		image[i] = 0xff
	}
	digest := sha256.Sum256(image[:ota.HeaderSize+bodySize])
	tlv := image[ota.HeaderSize+bodySize:]
	le.PutUint16(tlv[:2], 0x6907)
	le.PutUint16(tlv[2:4], 40)
	tlv[4] = 0x10
	le.PutUint16(tlv[6:8], 32)
	copy(tlv[8:], digest[:])
	return image, nil
}

func ZIP(w io.Writer, image []byte, name string) error {
	validated, err := ota.Validate(bytes.NewReader(image), 0, int64(len(image)))
	if err != nil {
		return err
	}
	if validated.Size != int64(len(image)) {
		return errors.New("unexpected image tail")
	}
	if name == "" || strings.ContainsAny(name, "/\\") {
		return errors.New("invalid package basename")
	}
	manifest := map[string]any{
		"format": "gopine-update", "protocol": 2,
		"image": name + ".bin", "sha256": fmt.Sprintf("%x", sha256.Sum256(image)),
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	z := zip.NewWriter(w)
	for _, file := range []struct {
		name string
		data []byte
	}{{name + ".bin", image}, {"manifest.json", manifestJSON}} {
		entry, err := z.Create(file.name)
		if err != nil {
			return err
		}
		if _, err := entry.Write(file.data); err != nil {
			return err
		}
	}
	return z.Close()
}

// Bootstrap contains only internal flash: bootloader, relocated-vector page,
// confirmed app slot, and cleared scratch. External recovery is installed by
// the separate standalone setup firmware BEFORE this HEX. UICR is not touched.
func Bootstrap(bootloader, image []byte) ([]byte, error) {
	if len(bootloader) == 0 || len(bootloader) > ota.Primary-ota.SectorSize {
		return nil, errors.New("bootloader exceeds its reserved region")
	}
	if len(image) > ota.MaxImageSize {
		return nil, errors.New("image too large")
	}
	if _, err := ota.Validate(bytes.NewReader(image), 0, int64(len(image))); err != nil {
		return nil, err
	}
	flash := bytes.Repeat([]byte{0xff}, ota.Primary+ota.SlotSize+ota.SectorSize)
	copy(flash, bootloader)
	copy(flash[ota.Primary:], image)
	copy(flash[ota.Primary+ota.ImageOK:], []byte{1, 0, 0, 0})
	copy(flash[ota.Primary+ota.MagicOffset:], ota.BootMagic[:])
	return flash, nil
}

// HEX writes Intel HEX with extended linear addresses (not a raw binary whose
// address a programmer would have to guess).
func HEX(w io.Writer, data []byte, base uint32) error {
	record := func(kind byte, address uint16, payload []byte) error {
		b := []byte{byte(len(payload)), byte(address >> 8), byte(address), kind}
		b = append(b, payload...)
		var sum byte
		for _, v := range b {
			sum += v
		}
		b = append(b, -sum)
		_, err := fmt.Fprintf(w, ":%X\n", b)
		return err
	}
	upper := ^uint32(0)
	for pos := 0; pos < len(data); {
		address := base + uint32(pos)
		if address>>16 != upper {
			upper = address >> 16
			if err := record(4, 0, []byte{byte(upper >> 8), byte(upper)}); err != nil {
				return err
			}
		}
		n := min(16, len(data)-pos, 0x10000-int(address&0xffff))
		if err := record(0, uint16(address), data[pos:pos+n]); err != nil {
			return err
		}
		pos += n
	}
	return record(1, 0, nil)
}
