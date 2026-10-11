package main

import (
	"encoding/binary"
	"github.com/Distortions81/goPine/internal/ota"
	"github.com/Distortions81/goPine/internal/uifont"
	"strconv"
	"time"
)

const (
	updateWaiting byte = 1 + iota
	updateReceiving
	updateVerifying
	updateReady
	updateFailed
)
const directUpdateWindow = 10 * time.Minute

type directUpdateRadio interface {
	timeRadio
	StartUpdate(uint8) error
	TakeUpdate(*[244]byte) (int, int)
	UpdateStatus([16]byte)
}
type updateFlash interface {
	ota.Flash
	Close()
}
type transferState struct {
	phase            byte
	open, retry      bool
	expires          time.Time
	percent          int
	version, message string
}
type directUpdater struct {
	sync      *timeSyncController
	flash     updateFlash
	receiver  ota.Receiver
	packet    [244]byte
	openFlash func() (updateFlash, error)
	confirmed func() bool
	powerOK   func() bool
	reboot    func() error
}

func (u *watchUI) beginDirectUpdate(now time.Time) {
	u.cancelHold()
	if u.transfer == nil {
		u.transfer = &transferState{}
	}
	*u.transfer = transferState{phase: updateWaiting, open: true, retry: true, expires: now.Add(directUpdateWindow)}
	u.page = pageTransfer
}
func (d *directUpdater) release() error {
	if err := d.receiver.Cancel(); err != nil {
		return err
	}
	if d.flash != nil {
		d.flash.Close()
		d.flash = nil
	}
	if d.sync.directMode {
		d.sync.close()
	}
	return nil
}
func (d *directUpdater) fail(u *watchUI, message string) {
	u.transfer.phase = updateFailed
	u.transfer.message = message
}
func (d *directUpdater) publish(u *watchUI, r directUpdateRadio) {
	var status [16]byte
	status[0], status[1] = 2, u.transfer.phase
	if u.transfer.phase == updateFailed {
		status[2] = 1
	}
	binary.LittleEndian.PutUint32(status[4:], d.receiver.Total)
	binary.LittleEndian.PutUint32(status[8:], d.receiver.Offset)
	binary.LittleEndian.PutUint32(status[12:], d.receiver.Session)
	r.UpdateStatus(status)
}

// Tick owns all flash writes on the UI goroutine. BLE callbacks only queue a
// bounded packet. Flash transactions finish before display or radio callbacks.
//
//go:noinline
func (d *directUpdater) tick(u *watchUI, now time.Time, power powerStatus) {
	t := u.transfer
	if t == nil {
		return
	}
	if !t.open || u.page != pageTransfer {
		t.open = false
		if err := d.release(); err != nil {
			t.open = true
			u.page = pageTransfer
			d.fail(u, "Cancel failed. Retry.")
		}
		return
	}
	if !now.Before(t.expires) {
		t.open = false
		if err := d.release(); err != nil {
			t.open = true
			t.expires = now.Add(directUpdateWindow)
			d.fail(u, "Cancel failed. Retry.")
			return
		}
		d.fail(u, "Session expired")
		return
	}
	if !updatePowerOK(power) {
		d.fail(u, "Charge to 20 percent")
	}
	if u.phone != nil {
		u.phone.mode = phoneOff
		u.phone.status = "Connection stopped"
		u.phone.clearLink()
	}
	u.sync.Cancel()
	if u.weather != nil {
		u.weather.open = false
	}
	if t.retry {
		if err := d.release(); err != nil {
			d.fail(u, "Could not clear update")
			return
		}
		d.sync.close()
		t.retry = false
	}
	r, ok := d.sync.radio.(directUpdateRadio)
	if !ok {
		t.open = false
		d.fail(u, "Bluetooth unavailable")
		return
	}
	if !d.sync.directMode {
		if radioBusy(d.sync.radio) {
			return
		}
		if err := r.StartUpdate(power.Percent); err != nil {
			r.Stop()
			t.open = false
			d.fail(u, "Bluetooth unavailable")
			return
		}
		d.sync.running, d.sync.directMode = true, true
	}
	// The main loop pumps host callbacks through sync.update after this frame
	// returns. Connection/discovery must not run beneath OTA packet/flash work.
	if _, _, _, err := r.Take(); err != nil {
		t.open = false
		if releaseErr := d.release(); releaseErr != nil {
			t.open = true
		}
		d.fail(u, "Bluetooth error")
		return
	}
	if t.phase == updateVerifying {
		if err := d.receiver.Verify(d.receiver.Session); err != nil {
			d.fail(u, "Image check failed")
		} else {
			a, b, c, build := d.receiver.VersionParts()
			t.version = decimal(int(a)) + "." + decimal(int(b)) + "." + decimal(int(c))
			if build != 0 {
				t.version += "+" + strconv.FormatUint(uint64(build), 10)
			}
			t.phase = updateReady
		}
		d.publish(u, r)
		return
	}
	// Drain at most the negotiated two-packet window. Each publish releases its
	// transport reservation after readback; notifications coalesce until the
	// shallow host pump runs. Then return to input and alarm processing.
	for drained := 0; drained < 2 && t.open && t.phase != updateFailed && t.phase != updateVerifying; drained++ {
		kind, size := r.TakeUpdate(&d.packet)
		if kind == 0 {
			break
		}
		data := d.packet[:]
		if kind != 0 && size > 0 && size <= len(data) && t.phase != updateFailed {
			if kind == 1 && size == 9 && data[0] == 1 && t.phase == updateWaiting {
				if !d.confirmed() || !d.powerOK() {
					d.fail(u, "Check power and KEEP")
				} else {
					f, err := d.openFlash()
					if err != nil {
						d.fail(u, "Flash unavailable")
					} else {
						d.flash = f
						if err := d.receiver.Begin(f, true, binary.LittleEndian.Uint32(data[5:]), binary.LittleEndian.Uint32(data[1:])); err != nil {
							d.fail(u, "Update request failed")
						} else {
							t.phase = updateReceiving
						}
					}
				}
			} else if kind == 2 && size > 8 && t.phase == updateReceiving {
				if err := d.receiver.Write(binary.LittleEndian.Uint32(data[:]), binary.LittleEndian.Uint32(data[4:]), data[8:size]); err != nil {
					d.fail(u, "Transfer interrupted")
				}
			} else if kind == 1 && size == 5 && binary.LittleEndian.Uint32(data[1:]) == d.receiver.Session {
				switch data[0] {
				case 2:
					if t.phase == updateReceiving && d.receiver.Offset == d.receiver.Total {
						t.phase = updateVerifying
					} else {
						d.fail(u, "Update incomplete")
					}
				case 3:
					t.open = false
				}
			} else {
				d.fail(u, "Unexpected update data")
			}
			t.expires = now.Add(directUpdateWindow)
		}
		d.publish(u, r)
	}
	if d.receiver.Total > 0 {
		t.percent = int(d.receiver.Offset * 100 / d.receiver.Total)
	}
	d.publish(u, r)
}
func (d *directUpdater) install(u *watchUI) {
	if u.transfer == nil || !u.transfer.open || u.transfer.phase != updateReady {
		return
	}
	if !d.powerOK() {
		d.fail(u, "Charge to 20 percent")
		return
	}
	if !d.confirmed() {
		d.fail(u, "Tap KEEP before update")
		return
	}
	if err := d.receiver.Commit(true); err != nil {
		d.fail(u, "Install failed. Cancel.")
		return
	}
	if err := d.reboot(); err != nil {
		d.fail(u, "Restart failed. Cancel.")
	}
}
func (u *watchUI) handleTransfer(e inputEvent, now time.Time) uiAction {
	t := u.transfer
	if t == nil {
		return actionNone
	}
	if e.Kind == inputSleep || e.Kind == inputSwipeRight || (e.Kind == inputTap && inRect(e, 0, 0, 60, 44)) || (e.Kind == inputTap && inRect(e, 12, 184, 114, 232)) {
		t.open = false
		u.page = pageSettings
		return actionNone
	}
	if e.Kind == inputTap && inRect(e, 126, 184, 228, 232) {
		if t.phase == updateReady {
			return actionInstallUpdate
		}
		if t.phase == updateFailed {
			u.beginDirectUpdate(now)
		}
	}
	return actionNone
}
func (u *watchUI) drawTransfer(d canvas) {
	t := u.transfer
	if t == nil {
		return
	}
	centered(d, &uifont.Bold18, 29, "WATCH UPDATE", white)
	title, note := "Ready to connect", "Open updater on PC"
	switch t.phase {
	case updateReceiving:
		title, note = "Receiving firmware", "Keep watch nearby"
	case updateVerifying:
		title, note = "Checking firmware", "Please wait"
	case updateReady:
		title, note = "Ready to install", "Install, then tap KEEP"
	case updateFailed:
		title, note = "Update stopped", t.message
	}
	centered(d, &uifont.Regular18, 72, title, white)
	if t.phase == updateReady {
		centered(d, &uifont.Regular18, 114, "goPine "+t.version, accent)
	} else if t.phase == updateReceiving || t.phase == updateVerifying {
		centered(d, &uifont.Bold24, 120, decimal(t.percent)+"%", accent)
	} else {
		centered(d, &uifont.Regular18, 114, "Stay on this screen", muted)
	}
	centered(d, &uifont.Regular18, 154, note, muted)
	clockControl(d, 12, 184, 102, 44, "CANCEL", card)
	if t.phase == updateReady {
		clockControl(d, 126, 184, 102, 44, "INSTALL", positive)
	}
	if t.phase == updateFailed {
		clockControl(d, 126, 184, 102, 44, "RETRY", positive)
	}
}
