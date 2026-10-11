package main

import (
	"github.com/Distortions81/goPine/internal/uifont"
	"time"
)

type pairingRadio interface {
	PairingCode() uint32
	BondStatus() int
	CancelPairing() error
	ForgetPhone() error
}

const pairingStageBaseline = 174

type pairingState struct {
	code                    uint32 // code + 1, so 000000 remains valid
	status                  int
	count                   int
	cancel, forget, confirm bool
	note                    string
	diagnostic              uint32
	progress                uint16
}

func forgettableBond(status int) bool {
	// A storage failure can be recovered by clearing the bond journal. Unknown,
	// absent, or still-saving bonds must never expose an erase action.
	return status == 2 || status == -1 || status == -3
}

func (u *watchUI) pairingVisible() bool {
	return !u.updateExclusive() && u.pairing != nil && u.pairing.code != 0 && !u.timers.active && u.page != pageTrial
}
func (u *watchUI) openPairingSettings() {
	if u.pairing == nil {
		u.pairing = &pairingState{status: -2}
	}
	u.pairing.confirm = false
	u.page = pagePairingSettings
}

// Runs outside drawing and C callbacks. A new code wakes the display once.
func (c *timeSyncController) updatePairing(u *watchUI) bool {
	r, ok := c.radio.(pairingRadio)
	if !ok {
		return false
	}
	code := r.PairingCode()
	p := u.pairing
	if p == nil {
		if code == 0 {
			return false
		}
		p = &pairingState{}
		u.pairing = p
	}
	previousStatus := p.status
	p.status = r.BondStatus()
	if counts, ok := c.radio.(interface{ BondCount() int }); ok {
		p.count = counts.BondCount()
	}
	if diagnostic, ok := c.radio.(interface{ PairingDiagnostics() uint32 }); ok {
		p.diagnostic = diagnostic.PairingDiagnostics()
	}
	if progress, ok := c.radio.(interface{ PairingProgress() uint16 }); ok {
		p.progress = progress.PairingProgress()
	}
	if p.status == 2 && previousStatus != 2 {
		p.note = ""
	}
	if !forgettableBond(p.status) {
		p.confirm = false
		if p.forget {
			p.forget = false
			p.note = ""
		}
	}
	if p.cancel {
		p.cancel = false
		if r.CancelPairing() != nil {
			p.note = "Could not cancel pairing"
		} else {
			p.code = 0
		}
		return false
	}
	if p.forget {
		u.phoneAuto = false
		c.close()
		u.sync.Cancel()
		if u.weather != nil {
			u.weather.open = false
		}
		if u.phone != nil {
			u.phone.mode = phoneOff
			u.phone.status = "Bluetooth off"
			u.phone.clearLink()
		}
		p.code = 0
		if p.forget && !radioBusy(c.radio) {
			if err := r.ForgetPhone(); err != nil {
				p.note = "Could not clear pairings"
			} else {
				p.note = "Forget on devices too"
			}
			p.forget = false
			p.status = r.BondStatus()
		}
		return false
	}
	changed := p.code != code && code != 0
	p.code = code
	if changed {
		u.cancelHold()
		u.powerNotice = false
	}
	return changed
}
func (u *watchUI) handlePairing(e inputEvent) bool {
	if !u.pairingVisible() {
		return false
	}
	if e.Kind == inputSleep || e.Kind == inputSwipeRight ||
		(e.Kind == inputTap && inRect(e, 16, 188, 224, 232)) {
		u.pairing.cancel = true
	}
	return true
}
func (u *watchUI) handlePairingSettings(e inputEvent) {
	p := u.pairing
	if e.Kind == inputSwipeRight || (e.Kind == inputTap && inRect(e, 0, 0, 60, 44)) {
		p.confirm = false
		u.openPhone()
		return
	}
	if e.Kind == inputTap && inRect(e, 16, 170, 224, 220) && forgettableBond(p.status) {
		if p.confirm {
			p.forget = true
			p.confirm = false
			p.note = "Disconnecting..."
		} else {
			p.confirm = true
		}
	}
}
func (u *watchUI) drawPairing(d canvas) {
	centered(d, &uifont.Bold18, 31, "PAIR DEVICE", white)
	centered(d, &uifont.Regular18, 70, "Enter this code", muted)
	centered(d, &uifont.Regular18, 95, "on your device", muted)
	var digits [6]byte
	code := u.pairing.code - 1
	for i := 5; i >= 0; i-- {
		digits[i] = byte(code%10) + '0'
		code /= 10
	}
	centered(d, &uifont.Bold24, 143, string(digits[:]), accent)
	centered(d, &uifont.Regular18, pairingStageBaseline, pairingStageText(u.pairing.progress), muted)
	clockControl(d, 16, 188, 208, 44, "CANCEL", card)
}
func (u *watchUI) drawPairingSettings(d canvas, now time.Time) {
	_ = now
	p := u.pairing
	centered(d, &uifont.Bold18, 29, "DEVICES", white)
	pairAction := "Tap Pair a device"
	if !u.phoneAuto && (u.phone == nil || u.phone.mode == phoneOff) {
		pairAction = "Turn on Bluetooth"
	}
	status := "Pairing not checked"
	switch p.status {
	case -3:
		status = "Pairing needs reset"
	case -1:
		status = "Pairing storage error"
	case 0:
		status = "No saved devices"
	case 1:
		status = "Saving pairing..."
	case 2:
		status = decimal(p.count) + "/2 devices saved"
	}
	centered(d, &uifont.Regular18, 80, status, accent)
	if p.diagnostic != 0 {
		centered(d, &uifont.Regular18, 115, pairingFailureText(p.diagnostic), muted)
		centered(d, &uifont.Regular18, 147, pairingDiagnosticCodes(p.diagnostic), muted)
		if !forgettableBond(p.status) {
			centered(d, &uifont.Regular18, 185, pairingStageText(p.progress), muted)
			centered(d, &uifont.Regular18, 212, pairAction, muted)
			return
		}
	} else {
		if !forgettableBond(p.status) {
			if p.status == 1 {
				centered(d, &uifont.Regular18, 120, "Please wait", muted)
			} else if p.note == "Forget on devices too" {
				centered(d, &uifont.Regular18, 120, "Forget this watch in", muted)
				centered(d, &uifont.Regular18, 147, "each device's settings", muted)
				centered(d, &uifont.Regular18, 185, pairAction, muted)
				centered(d, &uifont.Regular18, 212, "to pair again", muted)
			} else {
				text := pairAction
				if p.progress != 0 {
					text = pairingStageText(p.progress)
				}
				centered(d, &uifont.Regular18, 120, text, muted)
				centered(d, &uifont.Regular18, 147, "in Bluetooth settings", muted)
				note := p.note
				if note == "" {
					note = "Then pair in your app"
				}
				centered(d, &uifont.Regular18, 185, note, muted)
			}
			return
		}
		note := p.note
		if note == "" && p.status == 2 {
			note = "Two devices supported"
		}
		if note == "" && p.status == -3 {
			note = "Saved key cannot be used"
		}
		centered(d, &uifont.Regular18, 115, note, muted)
	}
	label := "FORGET ALL"
	if p.status == -1 || p.status == -3 {
		label = "RESET PAIRING"
	}
	if p.confirm {
		label = "TAP TO CONFIRM"
	}
	clockControl(d, 16, 170, 208, 50, label, card)
}

func pairingStageText(progress uint16) string {
	switch progress >> 8 {
	case 1:
		return "Starting pairing"
	case 2:
		if progress&255 == 0 {
			return "Waiting for device code"
		}
		return "Checking code " + decimal(int(progress&31)) + "/20"
	case 3:
		return "Verifying secure key"
	case 4:
		return "Enabling encryption"
	case 5:
		return "Exchanging identity"
	case 6:
		return "Pairing completed"
	default:
		return "Waiting for device"
	}
}

// NimBLE keeps local SMP, peer SMP and HCI errors in distinct namespaces.
// Retain their numbers alongside a short explanation for hardware diagnosis.
func pairingFailureText(value uint32) string {
	security, disconnected := uint16(value), uint16(value>>16)
	if security != 0 {
		switch security {
		case 0x404, 0x504:
			return "Code did not match"
		case 0x405, 0x505:
			return "Device rejected pairing"
		case 19: // BLE_HS_ETIMEOUT_HCI
			return "BLE command timed out"
		case 13: // BLE_HS_ETIMEOUT
			return "Pairing timed out"
		case 0x207:
			return "Bluetooth setup failed"
		default:
			return "Pairing failed"
		}
	}
	switch disconnected {
	case 0x208:
		return "Connection timed out"
	case 0x205, 0x206:
		return "Saved key rejected"
	case 0x213:
		return "Device disconnected"
	default:
		return "Connection interrupted"
	}
}

func pairingDiagnosticCodes(value uint32) string {
	return "S " + decimal(int(uint16(value))) + " / D " + decimal(int(value>>16))
}
