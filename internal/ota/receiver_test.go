package ota_test

import (
	"bytes"
	"testing"

	"github.com/Distortions81/goPine/internal/ota"
)

func receive(t *testing.T, f *memoryFlash, image []byte) *ota.Receiver {
	t.Helper()
	r := &ota.Receiver{}
	if err := r.Begin(f, true, uint32(len(image)), 42); err != nil {
		t.Fatal(err)
	}
	for r.Offset < uint32(len(image)) {
		start := r.Offset
		end := min(start+236, uint32(len(image)))
		if err := r.Write(42, start, image[start:end]); err != nil {
			t.Fatal(err)
		}
	}
	return r
}
func TestDirectReceiverTrialAndCancel(t *testing.T) {
	image := testImage(t)
	f := newFlash(image)
	original := bytes.Clone(f.data[:ota.Secondary])
	r := receive(t, f, image)
	if bytes.Equal(f.data[ota.Secondary+ota.MagicOffset:ota.Secondary+ota.SlotSize], ota.BootMagic[:]) {
		t.Fatal("transfer scheduled a reboot")
	}
	if err := r.Commit(true); err == nil {
		t.Fatal("unverified commit")
	}
	if err := r.Verify(42); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(false); err == nil {
		t.Fatal("trial overwrote rollback")
	}
	if err := r.Commit(true); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.data[ota.Secondary+ota.MagicOffset:ota.Secondary+ota.SlotSize], ota.BootMagic[:]) || f.data[ota.Secondary+ota.ImageOK] != 255 {
		t.Fatal("not a trial")
	}
	if !bytes.Equal(original, f.data[:ota.Secondary]) {
		t.Fatal("changed recovery")
	}
	if err := r.Cancel(); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(f.data[ota.Secondary+ota.MagicOffset:ota.Secondary+ota.SlotSize], ota.BootMagic[:]) {
		t.Fatal("cancel retained commit")
	}
}
func TestDirectReceiverRejectsWrongChunksAndCorruption(t *testing.T) {
	image := testImage(t)
	f := newFlash(image)
	var r ota.Receiver
	if err := r.Begin(f, false, uint32(len(image)), 42); err == nil || f.mutations != 0 {
		t.Fatal("trial modified flash")
	}
	if err := r.Begin(f, true, uint32(len(image)), 42); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, offset uint32
		data       []byte
	}{{41, 0, image[:20]}, {42, 1, image[:20]}, {42, 0, nil}, {42, 0, image[:237]}} {
		if err := r.Write(tc.id, tc.offset, tc.data); err == nil || r.Offset != 0 {
			t.Fatal("accepted bad chunk")
		}
	}
	rp := receive(t, f, image)
	f.data[ota.Secondary+100] ^= 1
	if err := rp.Verify(42); err == nil {
		t.Fatal("corruption accepted")
	}
}
func TestDirectReceiverPowerCutsNeverCommitIncompleteImage(t *testing.T) {
	image := testImage(t)
	f := newFlash(image)
	r := receive(t, f, image)
	r.Verify(42)
	r.Commit(true)
	for cut := 1; cut <= f.mutations; cut++ {
		broken := newFlash(image)
		broken.failAt = cut
		var rx ota.Receiver
		err := rx.Begin(broken, true, uint32(len(image)), 42)
		for err == nil && rx.Offset < uint32(len(image)) {
			start := rx.Offset
			end := min(start+236, uint32(len(image)))
			err = rx.Write(42, start, image[start:end])
		}
		if err == nil {
			err = rx.Verify(42)
		}
		if err == nil {
			err = rx.Commit(true)
		}
		if err == nil {
			t.Fatal("ignored power cut", cut)
		}
		// At cut 1, the old valid slot/trailer has not been changed at all.
		if cut > 1 && bytes.Equal(broken.data[ota.Secondary+ota.MagicOffset:ota.Secondary+ota.SlotSize], ota.BootMagic[:]) {
			t.Fatal("partial image committed", cut)
		}
	}
}

type failedEraseFlash struct{ *memoryFlash }

func (f failedEraseFlash) EraseSector(int64) error { return nil }

func TestDirectReceiverRequiresEraseAndWriteReadback(t *testing.T) {
	image := testImage(t)
	f := newFlash(image)
	var r ota.Receiver
	if err := r.Begin(failedEraseFlash{f}, true, uint32(len(image)), 42); err == nil || r.Session != 0 {
		t.Fatal("accepted unerased trailer")
	}
	if err := r.Begin(f, true, uint32(len(image)), 42); err != nil {
		t.Fatal(err)
	}
	f.corrupt = true
	if err := r.Write(42, 0, image[:192]); err == nil || r.Offset != 0 {
		t.Fatal("acknowledged corrupt write")
	}
}
