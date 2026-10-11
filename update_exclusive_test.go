package main

import (
	"testing"
	"time"
)

type quietUpdateRadio struct {
	fakeUpdateRadio
	appleReads, pairingReads int
}

func (r *quietUpdateRadio) TakeAppleNotification() ([148]byte, int) {
	r.appleReads++
	return [148]byte{}, 0
}
func (r *quietUpdateRadio) PairingCode() uint32 { r.pairingReads++; return 0 }
func (*quietUpdateRadio) BondStatus() int       { return 2 }
func (*quietUpdateRadio) CancelPairing() error  { return nil }
func (*quietUpdateRadio) ForgetPhone() error    { return nil }

func TestOTAExclusiveLoopDefersBackgroundWorkAndResumesAfterCancel(t *testing.T) {
	display := newScriptDisplay()
	u, updater, _, _, _ := directFixture()
	u.beginDirectUpdate(display.now)
	u.notifications = &notificationState{buzzUntil: display.now.Add(time.Second)}
	u.pairing = &pairingState{code: 123456}
	u.timers.countdown = countdown{running: true, deadline: display.now.Add(time.Second)}
	radio := &quietUpdateRadio{}
	updater.sync.radio = radio
	preflight, saves := 0, 0
	display.steps = []scriptStep{
		{at: 6 * time.Second, event: inputEvent{Kind: inputTap, X: 60, Y: 205}, check: func() {
			if u.page != pageTransfer || u.timers.active || display.vibrating || u.pairingVisible() {
				t.Fatal("background UI or vibration interrupted OTA")
			}
			if radio.appleReads != 0 || radio.pairingReads != 0 || preflight != 0 || saves != 0 {
				t.Fatal("background notification, pairing or settings work ran during OTA")
			}
			if display.powerReads < 2 || radio.hostPumps < 2 || display.waits > 140 {
				t.Fatal("essential power/host work stopped or overdue timer caused busy polling")
			}
			if display.writes != 30 {
				t.Fatal("unchanged OTA screen kept repainting")
			}
		}},
		{at: 6100 * time.Millisecond, event: inputEvent{Kind: inputQuit}, check: func() {
			if u.page != pageAlert || !u.timers.active || !display.vibrating || updater.sync.directMode {
				t.Fatal("cancellation did not resume the overdue timer")
			}
			if saves == 0 || preflight == 0 || radio.appleReads == 0 || radio.pairingReads == 0 {
				t.Fatal("normal services did not resume after cancellation")
			}
		}},
	}
	l := watchLoop{display: display, ui: u, sync: updater.sync, updater: updater,
		renderer: &frameRenderer{}, now: func() time.Time { return display.now },
		state:      func() updateState { return firmwareConfirmed },
		beforeSync: func(time.Time) { preflight++ },
		save:       func(time.Time, powerStatus) time.Time { saves++; return time.Time{} },
	}
	if err := l.run(); err != nil {
		t.Fatal(err)
	}
}

func TestOTAExclusivityCoversVerifyReadyAndRetry(t *testing.T) {
	now := time.Unix(0, 0)
	for _, phase := range []byte{updateWaiting, updateReceiving, updateVerifying, updateReady, updateFailed} {
		u := newWatchUI(firmwareConfirmed)
		u.beginDirectUpdate(now)
		u.transfer.phase = phase
		u.timers.countdown = countdown{running: true, deadline: now.Add(-time.Second)}
		if u.tickTimers(now, firmwareConfirmed) || u.page != pageTransfer || !u.timers.countdown.running {
			t.Fatal("timer preempted an OTA phase", phase)
		}
		if got := nextLoopDelay(now, &u, now.Add(time.Second), true); got != 50*time.Millisecond {
			t.Fatal("paused timer still supplied an OTA deadline", phase, got)
		}
	}
}
