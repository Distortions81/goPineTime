package dfu

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Distortions81/goPine/internal/ota"
)

func testBody(size int) []byte {
	body := make([]byte, size)
	binary.LittleEndian.PutUint32(body[:4], 0x20010000)
	binary.LittleEndian.PutUint32(body[4:8], ota.VectorAddress+9)
	return body
}

func TestTaskStackGuard(t *testing.T) {
	for _, data := range [][]byte{nil, {0, 8}, {0, 8, 0, 0}, {0, 32, 0, 0, 0, 8, 0, 0}} {
		if err := checkStackSizes(data, 8192); err == nil {
			t.Fatalf("accepted missing, malformed, or 2KB stack: %x", data)
		}
	}
	if err := checkStackSizes([]byte{0, 32, 0, 0}, 8192); err != nil {
		t.Fatal(err)
	}
}

// Minimal ARM ELF reproducing LLD's extra header-only LOAD at address zero.
// The real image is a different LOAD with an allocated .text section.
func TestELFExcludesHeadersButRejectsWrongOrigin(t *testing.T) {
	data := make([]byte, 0x200)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 1, 1, 1})
	le := binary.LittleEndian
	put16 := func(pos int, value uint16) { le.PutUint16(data[pos:], value) }
	put32 := func(pos int, value uint32) { le.PutUint32(data[pos:], value) }
	put16(16, 2)  // ET_EXEC
	put16(18, 40) // EM_ARM
	put32(20, 1)
	put32(24, ota.VectorAddress+9)
	put32(28, 52)    // program headers
	put32(32, 0x180) // section headers
	put16(40, 52)
	put16(42, 32)
	put16(44, 2)
	put16(46, 40)
	put16(48, 2)
	put32(52, 1) // ELF header-only LOAD
	put32(52+16, 116)
	put32(52+20, 116)
	put32(52+24, 4)
	put32(52+28, 4)
	app := 84
	put32(app, 1)
	put32(app+4, 0x100)
	put32(app+8, ota.VectorAddress)
	put32(app+12, ota.VectorAddress)
	put32(app+16, 64)
	put32(app+20, 64)
	put32(app+24, 5)
	put32(app+28, 4)
	copy(data[0x100:], testBody(64))
	section := 0x180 + 40
	put32(section+4, 1) // PROGBITS
	put32(section+8, 6) // SHF_ALLOC | SHF_EXECINSTR
	put32(section+12, ota.VectorAddress)
	put32(section+16, 0x100)
	put32(section+20, 64)
	put32(section+32, 4)
	body, err := ELFBody(bytes.NewReader(data))
	if err != nil || !bytes.Equal(body, testBody(64)) {
		t.Fatalf("ELF headers entered image: %v", err)
	}
	put32(app+8, 0)
	put32(app+12, 0)
	put32(section+12, 0)
	if _, err := ELFBody(bytes.NewReader(data)); err == nil {
		t.Fatal("accepted executable linked at zero")
	}
}

func TestImageAndZIP(t *testing.T) {
	v, err := ParseVersion("1.2.345+6789")
	if err != nil {
		t.Fatal(err)
	}
	image, err := Image(testBody(128), v) // 128 + 32 + 40 == 200: exact image length
	if err != nil {
		t.Fatal(err)
	}
	if len(image) != 200 {
		t.Fatal("unexpected compatibility padding")
	}
	if image[20] != 1 || image[21] != 2 || binary.LittleEndian.Uint16(image[22:24]) != 345 ||
		binary.LittleEndian.Uint32(image[24:28]) != 6789 {
		t.Fatal("incorrect MCUboot version fields")
	}
	if _, err := ota.Validate(bytes.NewReader(image), 0, int64(len(image))); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := ZIP(&out, image, "test-image"); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = data
	}
	if len(files) != 2 || !bytes.Equal(files["test-image.bin"], image) {
		t.Fatal("wrong zip contents")
	}
	var manifest struct {
		Format   string `json:"format"`
		Protocol int    `json:"protocol"`
		Image    string `json:"image"`
		SHA256   string `json:"sha256"`
	}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != "gopine-update" || manifest.Protocol != 2 || manifest.Image != "test-image.bin" ||
		manifest.SHA256 != fmt.Sprintf("%x", sha256.Sum256(image)) {
		t.Fatal("incorrect native manifest")
	}

}

func TestVersionAndBounds(t *testing.T) {
	for _, s := range []string{"", "1", "1.2", "v1.2.3", "1.2.3-rc1", "1.2.3+", "256.0.0", "1.256.0", "1.0.65536", "1.0.0+4294967296", "1.0.-1", "1.0.0/../../x"} {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("accepted invalid version %q", s)
		}
	}
	for _, s := range []string{"0.0.0", "255.255.65535+4294967295", "1.2.3+4"} {
		if _, err := ParseVersion(s); err != nil {
			t.Errorf("rejected %q: %v", s, err)
		}
	}

	body := testBody(ota.MaxImageSize)
	if _, err := Image(body, Version{}); err == nil {
		t.Fatal("accepted oversized image")
	}
	body = testBody(128)
	binary.LittleEndian.PutUint32(body[4:8], 0x101)
	if _, err := Image(body, Version{}); err == nil {
		t.Fatal("accepted image linked for address zero")
	}
	if _, err := ELFBody(bytes.NewReader(body)); err == nil {
		t.Fatal("accepted raw binary as ELF")
	}
}

func TestBootstrapAndHEX(t *testing.T) {
	image, err := Image(testBody(128), Version{})
	if err != nil {
		t.Fatal(err)
	}
	boot := []byte{1, 2, 3, 4}
	flash, err := Bootstrap(boot, image)
	if err != nil {
		t.Fatal(err)
	}
	if len(flash) != 0x7d000 || !bytes.Equal(flash[:4], boot) ||
		!bytes.Equal(flash[ota.Primary:ota.Primary+len(image)], image) ||
		binary.LittleEndian.Uint32(flash[ota.Primary+ota.ImageOK:]) != 1 ||
		!bytes.Equal(flash[ota.Primary+ota.MagicOffset:ota.Primary+ota.SlotSize], ota.BootMagic[:]) {
		t.Fatal("bad bootstrap layout")
	}
	if !bytes.Equal(flash[ota.Primary+ota.SlotSize:], bytes.Repeat([]byte{255}, ota.SectorSize)) {
		t.Fatal("scratch not erased")
	}
	var out bytes.Buffer
	if err := HEX(&out, flash, 0); err != nil {
		t.Fatal(err)
	}
	decoded := make([]byte, len(flash))
	address, count, eof := uint32(0), 0, false
	for _, line := range strings.Fields(out.String()) {
		r, err := hex.DecodeString(line[1:])
		if err != nil {
			t.Fatal(err)
		}
		var sum byte
		for _, b := range r {
			sum += b
		}
		if sum != 0 || int(r[0])+5 != len(r) {
			t.Fatal("bad HEX checksum or length")
		}
		switch r[3] {
		case 4:
			address = uint32(binary.BigEndian.Uint16(r[4:6])) << 16
		case 0:
			offset := address + uint32(binary.BigEndian.Uint16(r[1:3]))
			count += copy(decoded[offset:], r[4:len(r)-1])
		case 1:
			eof = true
		default:
			t.Fatal("unexpected HEX record")
		}
	}
	if !eof || count != len(flash) || !bytes.Equal(decoded, flash) {
		t.Fatal("HEX did not round trip")
	}
}
