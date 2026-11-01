package main

import (
	"math"
	"testing"
	"time"
)

var pulseBenchmarkResult uint16

func BenchmarkPulseEstimate(b *testing.B) {
	var p pulseDetector
	for i := range p.values {
		p.values[i] = int16(1000 * math.Sin(float64(i)*.75))
	}
	b.ReportAllocs()
	for b.Loop() {
		pulseBenchmarkResult = p.estimate()
	}
}

func BenchmarkHealthIdlePoll(b *testing.B) {
	now := time.Unix(100, 0)
	h := healthState{device: &fakeHealthDevice{}}
	h.tick(now, heartPeriodic, false)
	b.ReportAllocs()
	for b.Loop() {
		h.tick(now.Add(time.Millisecond), heartPeriodic, false)
	}
}

type syntheticPulseDevice struct {
	fakeHealthDevice
	index int
}

func (d *syntheticPulseDevice) ReadHeart() (uint16, error) {
	d.index++
	return uint16(20000 + 1000*math.Sin(float64(d.index)*.1*2*math.Pi*1.2)), nil
}

func TestPeriodicHeartStopsAfterSuccessfulReading(t *testing.T) {
	now := time.Unix(100, 0)
	d := &syntheticPulseDevice{}
	h := healthState{device: d}
	h.tick(now, heartPeriodic, false)
	stopped := time.Duration(0)
	for elapsed := heartSamplePeriod; elapsed < heartWindow; elapsed += heartSamplePeriod {
		h.tick(now.Add(elapsed), heartPeriodic, false)
		if !h.heartOn {
			stopped = elapsed
			break
		}
	}
	if h.bpm == 0 || stopped == 0 || stopped >= 20*time.Second {
		t.Fatal("valid periodic measurement kept LED powered unnecessarily", h.bpm, stopped)
	}
	if h.next != now.Add(5*time.Minute) {
		t.Fatal("early completion changed periodic cadence", h.next)
	}
	h.tick(now.Add(time.Minute), heartPeriodic, false)
	if d.heartStarts != 1 {
		t.Fatal("successful window restarted before its interval")
	}
}

func TestHealthSettingsOnlyRestartAffectedSensor(t *testing.T) {
	now := time.Unix(100, 0)
	d := &fakeHealthDevice{}
	h := healthState{device: d}
	h.tick(now, heartPeriodic|stepsEnabled, false)
	h.tick(now.Add(time.Second), withHeartInterval(heartPeriodic|stepsEnabled, 1), false)
	if d.stepStarts != 1 || d.stepStops != 0 {
		t.Fatal("heart mode reloaded step firmware")
	}
	starts, stops := d.heartStarts, d.heartStops
	h.tick(now.Add(2*time.Second), withHeartInterval(heartPeriodic, 1), false)
	if d.heartStarts != starts || d.heartStops != stops {
		t.Fatal("step toggle restarted heart sensor")
	}
}

func TestHealthSteadyPollingAndRenderingAllocateNothing(t *testing.T) {
	now := time.Unix(100, 0)
	h := healthState{device: &fakeHealthDevice{}}
	h.tick(now, heartPeriodic, false)
	if n := testing.AllocsPerRun(100, func() { h.tick(now.Add(time.Millisecond), heartPeriodic, false) }); n != 0 {
		t.Fatal("idle sensor polling allocates", n)
	}
	for _, pg := range []page{pageHealth, pageWatchGraph, pageHeartSignal, pageClock} {
		u := newWatchUI(firmwareConfirmed)
		u.page, u.watchGraph, u.healthOptions = pg, graphHeart, heartPeriodic|stepsEnabled
		u.clock.initialized, u.clock.approximate = true, false
		u.health = sampleGraphHealth(now)
		u.health.signal = sampleHeartSignal(now).signal
		d := &benchmarkDisplay{}
		var r frameRenderer
		draw := func(c canvas) { u.drawFrame(c, now, powerStatus{Percent: 73}) }
		_ = r.render(d, draw)
		if n := testing.AllocsPerRun(20, func() { _ = r.render(d, draw) }); n != 0 {
			t.Fatal("health page rendering allocates", pg, n)
		}
	}
}

func TestUnchangedStepSamplesDoNotInvalidateHealthPage(t *testing.T) {
	now := time.Unix(100, 0)
	h := healthState{device: &fakeHealthDevice{}}
	h.tick(now, stepsEnabled, false)
	revision := h.revision
	h.tick(now.Add(10*time.Second), stepsEnabled, false)
	if revision != h.revision {
		t.Fatal("unchanged step count requested redraw")
	}
}

func TestPulseOptimizationMatchesPreviousArithmetic(t *testing.T) {
	// Golden output from the pre-optimization 64-bit implementation, commit
	// 083f801. Includes 656 accepted and 368 rejected windows, varied ring heads,
	// signal offsets, periods, and full-range signed noise.
	seed, hash := uint32(0x12345678), uint32(2166136261)
	for n := 0; n < 1024; n++ {
		var p pulseDetector
		for i := range p.values {
			seed = seed*1664525 + 1013904223
			v := int16(seed >> 16)
			if n%4 != 0 {
				period := 3 + n%13
				v = int16((i%period)*2000/period - 1000 + n%17*100)
			}
			p.values[i] = v
		}
		p.head = uint8(n & 63)
		hash = (hash ^ uint32(p.estimate())) * 16777619
	}
	if hash != 3655802313 {
		t.Fatal("correlation results changed during optimization", hash)
	}
}

func TestAllHeartSchedulesSleepBetweenBoundedWindows(t *testing.T) {
	now := time.Unix(100, 0)
	for index := uint8(0); index < 6; index++ {
		options := withHeartInterval(heartPeriodic, index)
		d := &fakeHealthDevice{}
		h := healthState{device: d}
		h.tick(now, options, false)
		interval := heartInterval(options)
		h.tick(now.Add(heartWindow), options, false)
		if h.heartOn || h.next != now.Add(interval) {
			t.Fatal("scheduled sensor did not power down until next interval", index, h.next)
		}
		h.tick(now.Add(interval-time.Millisecond), options, false)
		if d.heartStarts != 1 {
			t.Fatal("sensor restarted early", index)
		}
		h.tick(now.Add(interval), options, false)
		if !h.heartOn || d.heartStarts != 2 {
			t.Fatal("sensor missed scheduled attempt", index)
		}
	}
}
