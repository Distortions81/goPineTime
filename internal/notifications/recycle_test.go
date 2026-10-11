package notifications

import "testing"

func TestSlotsRecycleWithoutMovingLiveMessages(t *testing.T) {
	var b Inbox
	packets := [][]byte{wire("First"), wire("Second"), wire("Third"), wire("Fourth"), wire("Fifth")}
	b.Add(packets[0])
	first := b.At(0)
	id := first.ID
	for i := 1; i < Capacity; i++ {
		b.Add(packets[i])
		if b.Find(id) != first || Text(first.Body[:]) != "First" {
			t.Fatal("live message moved while inserting another")
		}
	}
	before := b
	if b.Add(wire(" \t\x00\n ")) || b != before {
		t.Fatal("empty packet recycled a live slot")
	}
	b.Add(packets[4])
	if b.At(0) != first || b.Find(id) != nil || Text(first.Body[:]) != "Fifth" {
		t.Fatal("oldest slot was not recycled")
	}
	keep, removed := b.At(2), b.At(1)
	keepID := keep.ID
	b.Dismiss(removed.ID)
	if *removed != (Message{}) || b.Find(keepID) != keep {
		t.Fatal("dismissal retained text or moved another message")
	}
	b.Add(packets[0])
	if b.At(0) != removed || b.Find(keepID) != keep {
		t.Fatal("empty slot was not reused")
	}
}

func TestMixedPeerRecyclingHasNoAllocationsOrStaleText(t *testing.T) {
	var b Inbox
	a := applePacket(42, 0, 0, "Long title", "A long message from device one")
	second := applePacket(42, 0, 0, "Device two", "Other body")
	second[147] = 2
	short := applePacket(42, 1, 0, "A", "B")
	clearFirst := applePacket(0, 3, 0, "", "")
	android := wire("Android")
	if got := testing.AllocsPerRun(1000, func() {
		b.Clear()
		b.Add(android)
		b.ApplyApple(a)
		id := b.At(0).ID
		b.Read(id)
		b.ApplyApple(second)
		b.ApplyApple(short)
		m := b.Find(id)
		if m == nil || m.Unread || Text(m.Title[:]) != "A" || Text(m.Body[:]) != "B" {
			panic("modified slot retained old text or lost read state")
		}
		b.ApplyApple(clearFirst)
		if b.Count != 2 || b.Find(id) != nil || b.At(0).AppleSource != 2 {
			panic("session clear crossed peer boundary")
		}
		b.Dismiss(b.At(0).ID)
		for i := 0; i < 20; i++ {
			b.Add(android)
		}
	}); got != 0 {
		t.Fatalf("recycled notification lifecycle allocated %g times", got)
	}
}
