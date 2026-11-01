package notifications

import "testing"

func identifiedApple(peer, source byte, uid uint32, event, flags byte, body string) []byte {
	p := applePacket(uid, event, flags, "Messages", body)
	p[147], p[148], p[149] = source, 1, peer
	return p
}

func TestDismissedAppleReplaysStayClearedAcrossReconnect(t *testing.T) {
	var b Inbox
	p := identifiedApple(10, 1, 42, 0, 0, "Original")
	b.ApplyApple(p)
	b.Dismiss(b.At(0).ID)
	for _, event := range []byte{0, 1} {
		p[4] = event
		if changed, buzz := b.ApplyApple(p); changed || buzz || b.Count != 0 {
			t.Fatal("same-session replay resurrected a dismissed item", event)
		}
	}
	b.ApplyApple(identifiedApple(10, 1, 0, 3, 0, ""))
	p = identifiedApple(10, 2, 42, 0, 4, "Original") // Same phone, another slot after reconnect.
	if changed, buzz := b.ApplyApple(p); changed || buzz || b.Count != 0 {
		t.Fatal("reconnect replay resurrected cleared message")
	}
	// A different phone with the same UID and content is independent.
	if changed, _ := b.ApplyApple(identifiedApple(20, 1, 42, 0, 0, "Original")); !changed || b.Count != 1 {
		t.Fatal("dismissal crossed iPhone identity")
	}
	if changed, _ := b.ApplyApple(identifiedApple(10, 2, 43, 0, 0, "Original")); !changed || b.Count != 2 {
		t.Fatal("new UID with same text was suppressed")
	}
}

func TestClearAllRejectsOldBacklogButAcceptsNewMessages(t *testing.T) {
	var b Inbox
	for uid := uint32(0); uid < 4; uid++ {
		b.ApplyApple(identifiedApple(10, 1, uid, 0, 0, "Old"))
	}
	b.Clear()
	if b.Count != 0 || b.messages != ([Capacity]Message{}) {
		t.Fatal("clear all retained messages")
	}
	for uid := uint32(0); uid < 6; uid++ {
		if changed, buzz := b.ApplyApple(identifiedApple(10, 2, uid, 0, 4, "Old")); changed || buzz {
			t.Fatal("clear all allowed pre-existing backlog", uid)
		}
		if changed, _ := b.ApplyApple(identifiedApple(10, 2, uid, 1, 0, "Old")); changed {
			t.Fatal("backlog modified event resurrected old item", uid)
		}
	}
	if b.Count != 0 {
		t.Fatal("old messages returned after clear all")
	}
	if changed, buzz := b.ApplyApple(identifiedApple(10, 2, 100, 0, 0, "New")); !changed || !buzz || b.Count != 1 {
		t.Fatal("genuinely new message was hidden")
	}
}

func TestRemovedAndReusedAppleUIDCanReturn(t *testing.T) {
	var b Inbox
	p := identifiedApple(10, 1, 42, 0, 0, "Old")
	b.ApplyApple(p)
	b.Dismiss(b.At(0).ID)
	b.ApplyApple(identifiedApple(10, 1, 42, 2, 0, "")) // iPhone confirms notification gone.
	if changed, _ := b.ApplyApple(p); !changed {
		t.Fatal("remote UID reused after removal stayed hidden")
	}
	b.Dismiss(b.At(0).ID)
	if changed, _ := b.ApplyApple(identifiedApple(10, 2, 42, 0, 0, "New content")); !changed {
		t.Fatal("UID reused with new content stayed hidden")
	}
	if Text(b.At(0).Body[:]) != "New content" {
		t.Fatal("wrong replacement message")
	}
}

func TestDisconnectDoesNotDismissLiveAppleMessages(t *testing.T) {
	var b Inbox
	b.ApplyApple(identifiedApple(10, 1, 42, 0, 0, "Live"))
	b.ApplyApple(identifiedApple(10, 1, 0, 3, 0, ""))
	if changed, _ := b.ApplyApple(identifiedApple(10, 2, 42, 0, 4, "Live")); !changed {
		t.Fatal("session cleanup was remembered as user dismissal")
	}
}

func TestDismissalHistoryBoundedAndAllocationFree(t *testing.T) {
	var b Inbox
	p := identifiedApple(10, 1, 0, 0, 0, "Old")
	if n := testing.AllocsPerRun(100, func() {
		b = Inbox{}
		for uid := uint32(0); uid < DismissedCapacity+1; uid++ {
			p[0] = byte(uid)
			b.ApplyApple(p)
			b.Dismiss(b.At(0).ID)
		}
	}); n != 0 {
		t.Fatal("dismissal allocated", n)
	}
	p[0] = DismissedCapacity
	if changed, _ := b.ApplyApple(p); changed {
		t.Fatal("newest dismissal evicted prematurely")
	}
	p[0] = 0
	if changed, _ := b.ApplyApple(p); !changed {
		t.Fatal("history was not bounded")
	}
}
