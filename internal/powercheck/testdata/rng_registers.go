// Test-only nRF RNG register model, copied into device/nrf by the harness.
package nrf

const RNG_CONFIG_DERCEN_Enabled = 1

var Running bool
var Starts, Stops, Reads, Bias uint32
var polls int
var ready uint32

type register struct{ stop bool }

func (r register) Set(value uint32) {
	if value != 1 {
		panic("unexpected RNG task value")
	}
	if r.stop {
		Running = false
		Stops++
	} else {
		Running = true
		Starts++
	}
}

type event struct{}

func (event) Set(value uint32) { ready = value }
func (event) Get() uint32 {
	if Running {
		ready = 1
		polls = 0
	} else if ready == 0 {
		polls++
		if polls > 100 {
			panic("RNG read stalled: hardware stopped without resetting the driver flag")
		}
	}
	return ready
}

type rng struct {
	TASKS_START, TASKS_STOP register
	EVENTS_VALRDY           event
}

func (*rng) SetCONFIG_DERCEN(value uint32) { Bias = value }
func (*rng) GetVALUE() uint32 {
	Reads++
	return Reads & 0xff
}

var RNG = &rng{TASKS_STOP: register{stop: true}}

func Reset() {
	Running = false
	Starts, Stops, Reads, Bias = 0, 0, 0, 0
	polls, ready = 0, 0
}
