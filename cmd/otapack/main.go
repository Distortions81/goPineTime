// otapack wraps a correctly linked TinyGo ELF in the native goPine format.
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Distortions81/goPine/internal/dfu"
)

const bootloaderSHA256 = "4d25ea801c7859069881a4e601cd25f7598ad16114b2a806be401865d255d72a"

func main() {
	input := flag.String("elf", "", "TinyGo ELF linked for 0x8020")
	version := flag.String("version", "", "major.minor.revision[+build]")
	out := flag.String("out", "build/ota", "output directory")
	boot := flag.String("bootloader", "", "also make wired bootstrap HEX using stock bootloader 1.0.1")
	flag.Parse()
	if err := pack(*input, *version, *out, *boot); err != nil {
		fmt.Fprintln(os.Stderr, "otapack:", err)
		os.Exit(1)
	}
}

func pack(input, version, out, boot string) error {
	v, err := dfu.ParseVersion(version)
	if err != nil {
		return err
	}
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := dfu.CheckTinyGoStack(f, 8192); err != nil {
		return err
	}
	body, err := dfu.ELFBody(f)
	if err != nil {
		return err
	}
	image, err := dfu.Image(body, v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	name := "gopine-app-image-" + version
	if err := os.WriteFile(filepath.Join(out, name+".bin"), image, 0o644); err != nil {
		return err
	}
	// A bootstrap image is wired-only; do not accidentally offer its recovery
	// provisioning code as a normal OTA update.
	if boot == "" {
		if err := writeFile(filepath.Join(out, "gopine-update-"+version+".zip"), func(w io.Writer) error {
			return dfu.ZIP(w, image, name)
		}); err != nil {
			return err
		}
	}
	if boot != "" {
		bootloader, err := os.ReadFile(boot)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(bootloader)) != bootloaderSHA256 {
			return fmt.Errorf("bootloader does not match official 1.0.1 SHA-256")
		}
		flash, err := dfu.Bootstrap(bootloader, image)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(out, "gopine-bootstrap-"+version+".hex"), func(w io.Writer) error {
			return dfu.HEX(w, flash, 0)
		}); err != nil {
			return err
		}
	}
	fmt.Printf("Packaged %d-byte MCUboot image (unsigned), version %s, in %s\n", len(image), version, out)
	return nil
}

func writeFile(path string, write func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = write(f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
