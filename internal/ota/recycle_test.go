package ota_test

import (
	"bytes"
	"testing"

	"github.com/Distortions81/goPine/internal/ota"
)

func TestReceiverReusesScratchThroughWholeSession(t *testing.T) {
	image := testImage(t)
	f := newFlash(image)
	r := &ota.Receiver{}
	if got := testing.AllocsPerRun(100, func() {
		if err := r.Begin(f, true, uint32(len(image)), 42); err != nil {
			panic(err)
		}
		for r.Offset < r.Total {
			start := r.Offset
			if err := r.Write(42, start, image[start:min(start+236, r.Total)]); err != nil {
				panic(err)
			}
		}
		if err := r.Verify(42); err != nil {
			panic(err)
		}
		if err := r.Commit(true); err != nil {
			panic(err)
		}
		if err := r.Cancel(); err != nil {
			panic(err)
		}
		if r.Total != 0 || r.Offset != 0 || r.Session != 0 || r.Version != [8]byte{} {
			panic("stale receiver metadata")
		}
	}); got != 0 {
		t.Fatalf("begin/write/verify/commit/cancel allocated %g times", got)
	}
}

func TestValidateBorrowedScratchBoundsAndReuse(t *testing.T) {
	image := testImage(t)
	reader := bytes.NewReader(image)
	want, err := ota.Validate(reader, 0, int64(len(image)))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{40, 41, 64, 236, 256, 1024} {
		storage := bytes.Repeat([]byte{0xa5}, size+2)
		got, err := ota.ValidateWithScratch(reader, 0, int64(len(image)), storage[1:size+1])
		if err != nil || got != want || storage[0] != 0xa5 || storage[size+1] != 0xa5 {
			t.Fatal("scratch size changed validation or its guards", size, err)
		}
		image[500] ^= 1
		if _, err := ota.ValidateWithScratch(reader, 0, int64(len(image)), storage[1:size+1]); err == nil {
			t.Fatal("reused scratch accepted corrupted image")
		}
		image[500] ^= 1
	}
	if _, err := ota.ValidateWithScratch(reader, 0, int64(len(image)), make([]byte, 39)); err == nil {
		t.Fatal("accepted undersized scratch")
	}
}
