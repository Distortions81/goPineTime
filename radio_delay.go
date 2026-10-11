package main

import "time"

// Hardware settling delays may run while the phone is connected. IRQs alone
// do not schedule the next NimBLE connection event: its controller queue must
// also run. Keep host callbacks deferred until the ordinary application loop.
func pauseWithRadio(duration time.Duration) {
	pauseRadioController(duration, time.Now, time.Sleep)
}

func pauseRadioController(duration time.Duration, now func() time.Time, pause func(time.Duration)) {
	if duration <= 0 {
		return
	}
	r, ok := activeTimeRadio.(interface{ ServiceController() })
	if !ok {
		pause(duration)
		return
	}
	if busy, ok := activeTimeRadio.(interface{ Busy() bool }); ok && !busy.Busy() {
		pause(duration)
		return
	}
	deadline := now().Add(duration)
	for {
		r.ServiceController()
		remaining := deadline.Sub(now())
		if remaining <= 0 {
			return
		}
		pause(min(remaining, time.Millisecond))
	}
}
