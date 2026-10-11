// Package notifications implements the bounded InfiniTime ANS inbox.
package notifications

import "unicode/utf8"

const (
	MaxPacket   = 103 // Three-byte companion header and up to 100 text bytes.
	Capacity    = 4
	ApplePacket = 148 // ANCS record plus the connection's source slot.
)

type Message struct {
	ID          uint32
	Title       [40]byte
	Body        [100]byte
	Category    byte
	Unread      bool
	Apple       bool
	AppleSource byte
	RemoteID    uint32
}

// ApplyApple handles bounded ANCS records from authenticated iPhone links.
// Remote IDs and session clears are local to their source slot.
func (b *Inbox) ApplyApple(packet []byte) (changed, buzz bool) {
	if len(packet) != ApplePacket || packet[4] > 3 || packet[147] < 1 || packet[147] > 2 {
		return false, false
	}
	if packet[4] == 3 {
		for i := b.Count - 1; i >= 0; i-- {
			if b.At(i).Apple && b.At(i).AppleSource == packet[147] {
				b.Dismiss(b.At(i).ID)
				changed = true
			}
		}
		return changed, false
	}
	uid := uint32(packet[0]) | uint32(packet[1])<<8 | uint32(packet[2])<<16 | uint32(packet[3])<<24
	existing := -1
	for i := 0; i < b.Count; i++ {
		if b.At(i).Apple && b.At(i).AppleSource == packet[147] && b.At(i).RemoteID == uid {
			existing = i
			break
		}
	}
	if packet[4] == 2 {
		if existing >= 0 {
			b.Dismiss(b.At(existing).ID)
			return true, false
		}
		return false, false
	}
	if !hasText(packet[7:47]) && !hasText(packet[47:147]) {
		return false, false
	}
	var m *Message
	if existing >= 0 {
		m = b.At(existing)
		b.Revision++
	} else {
		m = b.insert()
	}
	m.Apple, m.AppleSource, m.RemoteID, m.Category = true, packet[147], uid, appleCategory(packet[5])
	clean(m.Title[:], packet[7:47])
	clean(m.Body[:], packet[47:147])

	// Silent and pre-existing notifications populate the inbox without buzzing.
	return true, packet[4] == 0 && existing < 0 && packet[6]&5 == 0
}
func appleCategory(category byte) byte {
	switch category {
	case 1:
		return 3
	case 2:
		return 4
	case 3:
		return 6
	case 4:
		return 5
	case 5:
		return 7
	case 6:
		return 1
	}
	return 0
}

type Inbox struct {
	messages [Capacity]Message // Stable slots, recycled on dismissal or eviction.
	order    [Capacity]uint8   // Newest first; only indices move.
	Count    int
	Revision uint32
}

// Add accepts InfiniTime's three-byte header, then either plain text or
// title NUL body. Companions combine source and sender in the title; no source
// or remote notification ID is inferred. Returns false for empty/malformed text.
func (b *Inbox) Add(packet []byte) bool {
	if len(packet) < 4 || len(packet) > MaxPacket {
		return false
	}
	text := packet[3:]
	split := -1
	for i, c := range text {
		if c == 0 {
			split = i
			break
		}
	}
	var title, body []byte
	if split >= 0 && split < len(text)-1 {
		title, body = text[:split], text[split+1:]
	} else {
		body = text
	}
	// Validate before recycling a live slot: an empty packet must not evict
	// the oldest message or destroy an existing ANCS notification.
	if !hasText(title) && !hasText(body) {
		return false
	}
	m := b.insert()
	m.Category = packet[0]
	clean(m.Title[:], title)
	clean(m.Body[:], body)
	return true
}

// At returns a borrowed slot. It stays put while other messages are inserted
// or dismissed, but is invalid after this message is removed or evicted.
func (b *Inbox) At(index int) *Message {
	if index < 0 || index >= b.Count {
		return nil
	}
	return &b.messages[b.order[index]]
}

func (b *Inbox) insert() *Message {
	slot := uint8(0)
	if b.Count == Capacity {
		slot = b.order[b.Count-1]
	} else {
		for b.messages[slot].ID != 0 {
			slot++
		}
		b.Count++
	}
	copy(b.order[1:b.Count], b.order[:b.Count-1])
	b.order[0] = slot
	b.Revision++
	if b.Revision == 0 {
		b.Revision++
	}
	m := &b.messages[slot]
	*m = Message{ID: b.Revision, Unread: true}
	return m
}

func hasText(data []byte) bool {
	for len(data) > 0 && data[0] != 0 {
		r, size := utf8.DecodeRune(data)
		if r > 32 && r != 127 {
			return true
		}
		data = data[size:]
	}
	return false
}

func clean(out, data []byte) {
	clear(out)
	n := 0
	for len(data) > 0 && data[0] != 0 {
		r, size := utf8.DecodeRune(data)
		data = data[size:]
		c := byte('?')
		if r >= 32 && r <= 126 {
			c = byte(r)
		}
		if r < 32 || r == 127 {
			c = ' '
		}
		if c == ' ' && (n == 0 || out[n-1] == ' ') {
			continue
		}
		if n == len(out) {
			copy(out[len(out)-3:], "...")
			return
		}
		out[n] = c
		n++
	}
	for n > 0 && out[n-1] == ' ' {
		n--
		out[n] = 0
	}
}

func Text(data []byte) string {
	return string(Bytes(data))
}
func Bytes(data []byte) []byte {
	n := 0
	for n < len(data) && data[n] != 0 {
		n++
	}
	return data[:n]
}

func CategoryLabel(category byte) string {
	switch category {
	case 1:
		return "Email"
	case 3:
		return "Incoming call"
	case 4:
		return "Missed call"
	case 5:
		return "Message"
	case 6:
		return "Voicemail"
	case 7:
		return "Calendar"
	default:
		return "Notification"
	}
}

func (b *Inbox) Find(id uint32) *Message {
	for i := 0; i < b.Count; i++ {
		if b.At(i).ID == id {
			return b.At(i)
		}
	}
	return nil
}
func (b *Inbox) Read(id uint32) {
	if m := b.Find(id); m != nil && m.Unread {
		m.Unread = false
		b.Revision++
	}
}
func (b *Inbox) Unread() int {
	n := 0
	for i := 0; i < b.Count; i++ {
		if b.At(i).Unread {
			n++
		}
	}
	return n
}
func (b *Inbox) Dismiss(id uint32) {
	for i := 0; i < b.Count; i++ {
		if b.At(i).ID != id {
			continue
		}
		*b.At(i) = Message{}
		copy(b.order[i:], b.order[i+1:b.Count])
		b.Count--
		b.order[b.Count] = 0
		b.Revision++
		return
	}
}
func (b *Inbox) Clear() {
	for i := range b.messages {
		b.messages[i] = Message{}
	}
	clear(b.order[:])
	b.Count = 0
	b.Revision++
}
