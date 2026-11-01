package main

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/uifont"
)

type fakeHealthDevice struct {
	heart, steps                                          bool
	heartStarts, heartStops, stepStarts, stepStops, reads int
	count                                                 uint32
	fail                                                  bool
}

func (d *fakeHealthDevice) SetHeart(on bool) error {
	if d.fail {
		return errors.New("I2C failed")
	}
	d.heart = on
	if on {
		d.heartStarts++
	} else {
		d.heartStops++
	}
	return nil
}
func (d *fakeHealthDevice) ReadHeart() (uint16, error) {
	d.reads++
	if d.fail {
		return 0, errors.New("I2C failed")
	}
	return 20000, nil
}
func (d *fakeHealthDevice) SetSteps(on bool) error {
	if d.fail {
		return errors.New("I2C failed")
	}
	d.steps = on
	if on {
		d.stepStarts++
		d.count = 0
	} else {
		d.stepStops++
	}
	return nil
}
func (d *fakeHealthDevice) ReadSteps() (uint32, error) {
	if d.fail {
		return 0, errors.New("I2C failed")
	}
	return d.count, nil
}

func TestHealthModesPowerAndDeadlines(t *testing.T) {
	now := time.Unix(100, 0)
	d := &fakeHealthDevice{}
	u := newWatchUI(firmwareConfirmed)
	u.health = &healthState{device: d}
	u.tickHealth(now, false)
	if d.heartStarts != 0 || d.stepStarts != 0 || !u.health.next.IsZero() {
		t.Fatal("off mode activated sensors or polling")
	}
	u.healthOptions = heartPeriodic | stepsEnabled
	u.tickHealth(now, false)
	if !d.heart || !d.steps {
		t.Fatal("requested sensors did not start")
	}
	if delay := nextLoopDelay(now, &u, now.Add(time.Hour), false); delay != heartSamplePeriod {
		t.Fatal("sleeping loop missed heart sample", delay)
	}
	d.count = 42
	u.tickHealth(now.Add(heartWindow), false)
	if d.heart || !d.steps || u.health.steps != 42 {
		t.Fatal("periodic window failed to power off heart independently of steps")
	}
	u.tickHealth(now.Add(5*time.Minute-time.Millisecond), false)
	if d.heart {
		t.Fatal("periodic restarted too early")
	}
	u.tickHealth(now.Add(5*time.Minute), false)
	if !d.heart || d.heartStarts != 2 {
		t.Fatal("periodic window did not restart")
	}
	u.healthOptions = withHeartInterval(heartPeriodic|stepsEnabled, 4)
	u.tickHealth(now.Add(6*time.Minute), false)
	u.tickHealth(now.Add(8*time.Minute), false)
	if d.heart {
		t.Fatal("scheduled measurement exceeded its window")
	}
	u.tickHealth(now.Add(9*time.Minute), true)
	if d.heart || d.steps || !u.health.next.IsZero() {
		t.Fatal("update did not suspend sensors")
	}
	u.tickHealth(now.Add(10*time.Minute), false)
	if !d.heart || !d.steps {
		t.Fatal("sensors did not resume after update")
	}
	u.healthOptions = 0
	u.tickHealth(now.Add(11*time.Minute), false)
	if d.heart || d.steps || !u.health.next.IsZero() {
		t.Fatal("off did not restore idle power behavior")
	}
}

func TestHealthStepSessionsAndFailureRecovery(t *testing.T) {
	now := time.Unix(100, 0)
	d := &fakeHealthDevice{}
	h := healthState{device: d}
	h.tick(now, stepsEnabled, false)
	d.count = 10
	h.tick(now.Add(time.Second), 0, false)
	h.tick(now.Add(2*time.Second), stepsEnabled, false)
	d.count = 5
	h.tick(now.Add(12*time.Second), stepsEnabled, false)
	if h.steps != 15 {
		t.Fatal("lost step total across sensor restart", h.steps)
	}
	d.fail = true
	h.tick(now.Add(22*time.Second), stepsEnabled, false)
	if !h.stepsFailed || !h.stepsOn {
		t.Fatal("failed shutdown was assumed successful")
	}
	d.fail = false
	h.tick(now.Add(32*time.Second), 0, false)
	if h.stepsOn {
		t.Fatal("shutdown did not recover")
	}
	h.tick(now.Add(33*time.Second), heartPeriodic, false)
	d.fail = true
	h.tick(now.Add(34*time.Second), heartPeriodic, false)
	if !h.heartFailed {
		t.Fatal("sampling failure was ignored")
	}
	d.fail = false
	h.tick(now.Add(35*time.Second), heartPeriodic, false)
	if h.heartOn {
		t.Fatal("failed heart sensor did not power down on retry")
	}
}

func TestHealthUIControlsAndFrameInvalidation(t *testing.T) {
	u := newWatchUI(firmwareConfirmed)
	u.openHealth()
	now := time.Unix(100, 0)
	key := u.frameKey(now, powerStatus{})
	u.handleHealth(inputEvent{Kind: inputTap, X: 120, Y: 65})
	if u.healthOptions&3 != heartPeriodic || key == u.frameKey(now, powerStatus{}) {
		t.Fatal("mode control did not select periodic/redraw")
	}
	for _, want := range []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, 3 * time.Hour, time.Minute, 5 * time.Minute} {
		u.handleHealth(inputEvent{Kind: inputTap, X: 120, Y: 95})
		if heartInterval(u.healthOptions) != want {
			t.Fatal("wrong periodic interval")
		}
	}
	u.handleHealth(inputEvent{Kind: inputTap, X: 120, Y: 135})
	if u.healthOptions&stepsEnabled == 0 {
		t.Fatal("pedometer toggle did not enable")
	}
	u.handleHealth(inputEvent{Kind: inputTap, X: 120, Y: 65})
	u.handleHealth(inputEvent{Kind: inputTap, X: 120, Y: 95})
	if u.healthOptions&1 != heartOff || heartInterval(u.healthOptions) != 5*time.Minute {
		t.Fatal("disabled interval changed while Off")
	}
	u.handleHealth(inputEvent{Kind: inputTap, X: 120, Y: 65})
	u.handleHealth(inputEvent{Kind: inputSleep})
	if u.healthOptions&1 != heartPeriodic {
		t.Fatal("screen sleep cancelled tracking")
	}
	u.handleHealth(inputEvent{Kind: inputSwipeRight})
	if u.page != pageApps {
		t.Fatal("back did not return to Apps")
	}
	for _, label := range []string{"Heart rate: Periodic", "Heart sensor error", "Step sensor error", "Paused for update", "Steps: 99999 since boot"} {
		w, _ := lineWidth(&uifont.Regular18, label)
		if w > 216 {
			t.Fatal("health text does not fit", label, w)
		}
	}
}

func TestPulseEstimatorSignalsAndGaps(t *testing.T) {
	for _, rate := range []float64{45, 60, 90, 120, 180} {
		var p pulseDetector
		var bpm uint16
		now := time.Unix(100, 0)
		for i := 0; i < 600; i++ {
			v := uint16(20000 + 1000*math.Sin(2*math.Pi*rate/60*float64(i)*heartSamplePeriod.Seconds()))
			if value := p.sample(v, now.Add(time.Duration(i)*heartSamplePeriod)); value != 0 {
				bpm = value
			}
		}
		if math.Abs(float64(bpm)-rate) > 5 {
			t.Fatal("synthetic pulse estimate", rate, bpm)
		}
		if p.sample(20000, now.Add(2*time.Minute)) != 0 || p.count != 0 {
			t.Fatal("sample gap retained a pulse")
		}
	}
	var p pulseDetector
	for i := 0; i < 600; i++ {
		if p.sample(20000, time.Unix(100, 0).Add(time.Duration(i)*heartSamplePeriod)) != 0 {
			t.Fatal("flat signal reported BPM")
		}
	}
}

func TestPulseWeakSignalsAndDriftingBaseline(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		base, amplitude, drift float64
	}{
		{"low ADC level", 50, 5, 0},
		{"small optical pulse", 20000, 2, 0},
		{"slow baseline movement", 20000, 40, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p pulseDetector
			var bpm uint16
			now := time.Unix(100, 0)
			for i := 0; i < 300; i++ {
				seconds := float64(i) * heartSamplePeriod.Seconds()
				value := tc.base + tc.amplitude*math.Sin(2*math.Pi*1.2*seconds) + tc.drift*math.Sin(2*math.Pi*seconds/30)
				if reading := p.sample(uint16(value), now.Add(time.Duration(i)*heartSamplePeriod)); reading != 0 {
					bpm = reading
				}
			}
			if bpm < 68 || bpm > 76 {
				t.Fatal("did not acquire weak 72 BPM pulse", bpm)
			}
		})
	}
}

func TestPulseRejectsNoiseDriftAndClippedData(t *testing.T) {
	for _, kind := range []string{"noise", "drift", "clipped"} {
		t.Run(kind, func(t *testing.T) {
			var p pulseDetector
			seed := uint32(42)
			for i := 0; i < 1000; i++ {
				seed = seed*1664525 + 1013904223
				value := uint16(20000 + int(seed>>24) - 128)
				if kind == "drift" {
					value = uint16(20000 + i*2)
				}
				if kind == "clipped" {
					value = 65535
				}
				if bpm := p.sample(value, time.Unix(100, 0).Add(time.Duration(i)*heartSamplePeriod)); bpm != 0 {
					t.Fatal("invalid optical data reported BPM", bpm)
				}
			}
		})
	}
}

func TestHeartSamplingKeepsPhaseAfterLateLoop(t *testing.T) {
	now := time.Unix(100, 0)
	h := healthState{device: &fakeHealthDevice{}}
	h.tick(now, heartPeriodic, false)
	h.tick(now.Add(heartSamplePeriod+20*time.Millisecond), heartPeriodic, false)
	if h.sampleAt != now.Add(2*heartSamplePeriod) {
		t.Fatal("loop delay shifted sampling phase", h.sampleAt)
	}
	h.tick(now.Add(2*heartSamplePeriod+10*time.Millisecond), heartPeriodic, false)
	if h.sampleAt != now.Add(3*heartSamplePeriod) {
		t.Fatal("sampling drift accumulated")
	}
}
