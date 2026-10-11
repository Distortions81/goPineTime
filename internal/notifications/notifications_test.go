package notifications

import (
	"bytes"
	"testing"
)

func wire(text string) []byte { return append([]byte{255, 1, 0}, []byte(text)...) }

func TestCompanionPackets(t *testing.T) {
	for _, tc := range []struct{ text, title, body string }{
		{"Mail: Alice\x00Lunch at noon?", "Mail: Alice", "Lunch at noon?"},
		{"Old companion body", "", "Old companion body"},
		{"InfiniLink\x00Reminder\x00", "InfiniLink", "Reminder"},
		{"\x00Only a body", "", "Only a body"},
		{"Title only\x00", "", "Title only"},
		{"  A\n\tB\x00café 🔔 done", "A B", "caf? ? done"},
	} {
		var b Inbox
		if !b.Add(wire(tc.text)) {
			t.Fatal(tc.text)
		}
		m := b.At(0)
		if Text(m.Title[:]) != tc.title || Text(m.Body[:]) != tc.body || b.Unread() != 1 {
			t.Fatalf("%q: %q / %q", tc.text, Text(m.Title[:]), Text(m.Body[:]))
		}
	}
	var b Inbox
	for _, data := range [][]byte{nil, {1, 2, 3}, wire("\x00"), wire(" \t\x00 \n"), make([]byte, MaxPacket+1)} {
		if b.Add(data) || b.Count != 0 || b.Revision != 0 {
			t.Fatal("invalid packet mutated inbox")
		}
	}
	if !b.Add(wire(string(bytes.Repeat([]byte{'W'}, 50))+"\x00body")) || string(b.At(0).Title[37:]) != "..." {
		t.Fatal("title truncation")
	}
}

func TestInboxReplacementReadDismissAndClear(t *testing.T) {
	var b Inbox
	for i := 0; i < 6; i++ {
		if !b.Add(wire(string(rune('A' + i)))) {
			t.Fatal(i)
		}
	}
	if b.Count != Capacity || Text(b.At(0).Body[:]) != "F" || Text(b.At(3).Body[:]) != "C" {
		t.Fatal("replacement order")
	}
	id := b.At(2).ID
	b.Read(id)
	if b.Unread() != 3 || b.Find(id).Unread {
		t.Fatal("read")
	}
	b.Dismiss(id)
	if b.Find(id) != nil || b.Count != 3 || Text(b.At(2).Body[:]) != "C" || b.At(3) != nil {
		t.Fatal("dismiss order or retained text")
	}
	b.Dismiss(1000)
	b.Clear()
	if b.Count != 0 || b.Unread() != 0 || b.messages != ([Capacity]Message{}) {
		t.Fatal("clear retained text")
	}
}

func TestReceiptDoesNotAllocate(t *testing.T) {
	var b Inbox
	data := wire("Mail: Alice\x00Meeting in ten minutes")
	if n := testing.AllocsPerRun(100, func() { b.Add(data) }); n != 0 {
		t.Fatal(n)
	}
}

func FuzzInbox(f *testing.F) {
	f.Add(wire("Sender\x00Message"))
	f.Add([]byte{3, 1, 0, 'A', 0xff, 0xfe})
	f.Fuzz(func(t *testing.T, data []byte) {
		var b Inbox
		if !b.Add(data) {
			return
		}
		if b.Count != 1 || b.Unread() != 1 {
			t.Fatal("invalid state")
		}
		for _, text := range [][]byte{b.At(0).Title[:], b.At(0).Body[:]} {
			for _, c := range text {
				if c != 0 && (c < 32 || c > 126) {
					t.Fatal("unsupported text survived")
				}
			}
		}
	})
}
