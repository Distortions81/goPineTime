package main

import (
	"testing"
	"time"
)

// Like NimBLE's LL event-end path, an interrupt queues work and the next
// connection event cannot be scheduled until the controller consumes it.
type delayedControllerRadio struct {
	fakeTimeRadio
	now, nextIRQ time.Time
	queuedAt     time.Time
	maxLatency   time.Duration
	completed    int
	hosts        int
	busy         bool
}

func (r *delayedControllerRadio) Busy() bool { return r.busy }
func (r *delayedControllerRadio) Service()   { r.hosts++ }
func (r *delayedControllerRadio) ServiceController() {
	if r.queuedAt.IsZero() {
		return
	}
	r.maxLatency = max(r.maxLatency, r.now.Sub(r.queuedAt))
	r.completed++
	r.queuedAt = time.Time{}
	r.nextIRQ = r.now.Add(7500 * time.Microsecond)
}
func (r *delayedControllerRadio) pause(duration time.Duration) {
	r.now = r.now.Add(duration)
	if r.queuedAt.IsZero() && !r.now.Before(r.nextIRQ) {
		r.queuedAt = r.nextIRQ
	}
}

type hostOnlyDelayRadio struct {
	fakeTimeRadio
	hosts int
}

func (r *hostOnlyDelayRadio) Service() { r.hosts++ }

func TestHardwareSettlingServicesControllerWithoutHostReentry(t *testing.T) {
	previous := activeTimeRadio
	defer func() { activeTimeRadio = previous }()
	for _, operation := range []string{"touch-wake", "touch-sleep", "rapid-lcd-sleep", "flash-wait"} {
		t.Run(operation, func(t *testing.T) {
			start := time.Unix(1000, 0)
			r := &delayedControllerRadio{now: start, nextIRQ: start.Add(7500 * time.Microsecond), busy: true}
			activeTimeRadio = r
			now := func() time.Time { return r.now }
			pause := func(d time.Duration) { pauseRadioController(d, now, r.pause) }
			bus := touchBusFunc(func(uint16, []byte, []byte) error { return nil })
			var minimum time.Duration
			switch operation {
			case "touch-wake":
				resetTouchController(func(bool) {}, pause)
				if err := configureTouchRegisters(bus, pause); err != nil {
					t.Fatal(err)
				}
				minimum = 65 * time.Millisecond
			case "touch-sleep":
				if err := sleepTouchController(bus, func(bool) {}, pause); err != nil {
					t.Fatal(err)
				}
				minimum = 55 * time.Millisecond
			case "rapid-lcd-sleep":
				var panel lcdPower
				panel.woke(start)
				if err := panel.sleep(now, pause, func(bool) error { return nil }); err != nil {
					t.Fatal(err)
				}
				minimum = lcdWakeSettleTime
			case "flash-wait":
				for range 200 {
					pause(time.Millisecond)
				}
				minimum = 200 * time.Millisecond
			}
			if r.now.Sub(start) < minimum {
				t.Fatal("controller service shortened required hardware settling")
			}
			if r.completed < int(minimum/(10*time.Millisecond)) || r.maxLatency > time.Millisecond || !r.queuedAt.IsZero() {
				t.Fatalf("controller work stalled: completed=%d max latency=%v queued=%v", r.completed, r.maxLatency, !r.queuedAt.IsZero())
			}
			if r.hosts != 0 {
				t.Fatal("hardware delay entered a host callback")
			}
		})
	}
}

func TestHardwareDelayWithoutControllerDoesNotDispatchHost(t *testing.T) {
	previous := activeTimeRadio
	defer func() { activeTimeRadio = previous }()
	for _, radio := range []timeRadio{nil, &controllerTestRadio{}, &delayedControllerRadio{busy: false}, &hostOnlyDelayRadio{}} {
		activeTimeRadio = radio
		now := time.Unix(1000, 0)
		calls := 0
		pauseRadioController(0, func() time.Time { return now }, func(d time.Duration) { calls++ })
		if calls != 0 {
			t.Fatal("zero hardware delay slept")
		}
		pauseRadioController(5*time.Millisecond, func() time.Time { return now }, func(d time.Duration) {
			calls++
			now = now.Add(d)
		})
		if r, ok := radio.(*controllerTestRadio); ok {
			if r.controllers == 0 || r.hosts != 0 {
				t.Fatal("optional Busy method changed controller-only service")
			}
		} else if calls != 1 {
			t.Fatal("inactive/absent controller gained periodic wakeups")
		}
		if r, ok := radio.(*hostOnlyDelayRadio); ok && r.hosts != 0 {
			t.Fatal("missing controller method fell back to host callbacks")
		}
	}
}
