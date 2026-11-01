package main

import (
	"testing"
	"time"
)

func sampleHeartSignal(now time.Time) *healthState {
	b := &healthBus{}
	b.heart[0] = 0x21
	d := &boardHealthDevice{bus: b, pause: func(time.Duration) {}}
	_ = d.SetHeart(true)
	h := &healthState{device: d}
	for i := 0; i < 100; i++ {
		value := uint16(1400 + (i%10)*12)
		h.signal.add(value, 80, now.Add(time.Duration(i)*heartSamplePeriod))
	}
	return h
}

func TestHeartSignalRetainsRejectedSamplesAndTimingEvidence(t *testing.T) {
	var s heartSignal
	now := time.Unix(100, 0)
	s.add(0, 20, now)
	s.add(1500, 21, now.Add(100*time.Millisecond))
	s.add(1600, 22, now.Add(500*time.Millisecond))
	if s.samples != 3 || s.invalid != 1 || s.minimum != 0 || s.maximum != 1600 || s.maxGapMS != 400 || s.gaps != 1 || s.ambient != 22 {
		t.Fatal("raw acquisition evidence incorrect", s)
	}
	for i := 0; i < 100; i++ {
		s.add(uint16(1000+i), 0, now.Add(time.Second+time.Duration(i)*heartSamplePeriod))
	}
	if s.filled != 64 || s.values[(int(s.head)+63)&63] != 1099 {
		t.Fatal("waveform buffer is not bounded/latest")
	}
	s.reset()
	if s.samples != 0 || s.filled != 0 || !s.last.IsZero() {
		t.Fatal("new attempt retained old acquisition data")
	}
}

type heartReadbackBus struct {
	*healthBus
	reg, flip byte
}

func (b heartReadbackBus) Tx(address uint16, w, r []byte) error {
	err := b.healthBus.Tx(address, w, r)
	if err == nil && address == heartRateAddress && len(r) == 1 && w[0] == b.reg {
		r[0] ^= b.flip
	}
	return err
}

func TestHeartConfigurationReadbackAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		reg, flip byte
		fail      bool
	}{{0x16, 1, true}, {0x17, 3, false}} {
		b := &healthBus{}
		b.heart[0] = 0x21
		d := boardHealthDevice{bus: heartReadbackBus{b, tc.reg, tc.flip}, pause: func(time.Duration) {}}
		err := d.SetHeart(true)
		if (err != nil) != tc.fail {
			t.Fatal("readback did not distinguish control and reserved bits", tc, err)
		}
		if tc.fail && (b.heart[0x0c] != 0 || b.heart[1]&0x80 != 0) {
			t.Fatal("readback failure left sensor enabled")
		}
		if !tc.fail && (!d.HeartConfig().verified || d.HeartConfig().gain != 3) {
			t.Fatal("diagnostics did not retain actual register readback")
		}
	}
	b := &healthBus{}
	b.heart[0] = 0xff
	d := boardHealthDevice{bus: b, pause: func(time.Duration) {}}
	if d.SetHeart(true) == nil || d.HeartConfig().id != 0xff || d.HeartConfig().verified {
		t.Fatal("unknown heart sensor identity accepted")
	}
}

func TestHeartSignalNavigation(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	u.openHealth()
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 204}, time.Unix(100, 0), firmwareConfirmed, powerStatus{})
	if u.page != pageHeartSignal {
		t.Fatal("tap on BPM did not open acquisition diagnostics")
	}
	u.handle(inputEvent{Kind: inputTap, X: 25, Y: 24}, time.Unix(100, 0), firmwareConfirmed, powerStatus{})
	if u.page != pageHealth {
		t.Fatal("shared diagnostic back did not return to Health")
	}
}
