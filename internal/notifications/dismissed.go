package notifications

// Bounded boot-local dismissal history. Keep hashes/identities, not cleared
// message text. Clear All also rejects pre-existing ANCS replay backlogs.
const DismissedCapacity = 32

type dismissedApple struct {
	fingerprint uint64
	uid         uint32
	peer        [7]byte
	source      byte // Zero denotes a free entry; real source slots are 1 or 2.
}

func sameApplePeer(a [7]byte, sourceA byte, b [7]byte, sourceB byte) bool {
	if a != ([7]byte{}) && b != ([7]byte{}) {
		return a == b
	}
	return a == b && sourceA == sourceB
}

func appleFingerprint(packet []byte) uint64 {
	// ANCS supplies fixed, zero-padded title/body fields. Event/flags may
	// change on a replay and are deliberately excluded from content identity.
	h := uint64(14695981039346656037)
	h = (h ^ uint64(packet[5])) * 1099511628211
	for _, v := range packet[7:147] {
		h = (h ^ uint64(v)) * 1099511628211
	}
	return h
}

func (b *Inbox) rememberDismissed(peer [7]byte, source byte, uid uint32, fingerprint uint64) {
	if source < 1 || source > 2 {
		return
	}
	for i := range b.dismissed {
		d := &b.dismissed[i]
		if d.source != 0 && d.uid == uid && sameApplePeer(d.peer, d.source, peer, source) {
			d.fingerprint = fingerprint
			return
		}
	}
	b.dismissed[b.dismissedNext] = dismissedApple{fingerprint, uid, peer, source}
	b.dismissedNext = (b.dismissedNext + 1) % DismissedCapacity
}

func (b *Inbox) wasDismissed(peer [7]byte, source byte, uid uint32, fingerprint uint64) bool {
	for i := range b.dismissed {
		d := &b.dismissed[i]
		if d.source != 0 && d.uid == uid && sameApplePeer(d.peer, d.source, peer, source) {
			if d.fingerprint == fingerprint {
				return true
			}
			// A reused UID with changed content is a new message, not a replay.
			*d = dismissedApple{}
			return false
		}
	}
	return false
}

func (b *Inbox) forgetDismissed(peer [7]byte, source byte, uid uint32) {
	for i := range b.dismissed {
		d := &b.dismissed[i]
		if d.source != 0 && d.uid == uid && sameApplePeer(d.peer, d.source, peer, source) {
			*d = dismissedApple{}
		}
	}
}
