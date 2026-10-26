package main

import (
	"time"

	"github.com/Distortions81/goPine/internal/notifications"
	"github.com/Distortions81/goPine/internal/uifont"
)

type notificationState struct {
	inbox               notifications.Inbox
	selected            uint32
	offset              int
	quiet               bool
	buzzUntil, lastBuzz time.Time
}

const notificationPulse = 150 * time.Millisecond
const notificationBuzzGap = 5 * time.Second

func (u *watchUI) openInbox() {
	if u.notifications == nil {
		u.notifications = &notificationState{}
	}
	u.notifications.buzzUntil = time.Time{}
	u.page = pageInbox
}
func (u *watchUI) unreadNotifications() int {
	if u.notifications == nil {
		return 0
	}
	return u.notifications.inbox.Unread()
}

//go:noinline
func (u *watchUI) receiveNotifications(r timeRadio, now time.Time) {
	source, ok := r.(interface {
		TakeNotification() ([notifications.MaxPacket]byte, int)
	})
	if !ok {
		return
	}
	// Match the transport's two-slot mailbox. Never drain an unbounded stream
	// before the scheduler can service input or an alarm.
	for i := 0; i < 2; i++ {
		packet, size := source.TakeNotification()
		if size <= 0 {
			break
		}
		if size > len(packet) {
			continue
		}
		if u.notifications == nil {
			u.notifications = &notificationState{}
		}
		n := u.notifications
		if !n.inbox.Add(packet[:size]) {
			continue
		}
		if !n.quiet && !u.timers.active && (n.lastBuzz.IsZero() || now.Sub(n.lastBuzz) >= notificationBuzzGap) {
			n.lastBuzz, n.buzzUntil = now, now.Add(notificationPulse)
		}
	}
}

//go:noinline
func (u *watchUI) receiveAppleNotifications(r timeRadio, now time.Time) {
	source, ok := r.(interface {
		TakeAppleNotification() ([notifications.ApplePacket]byte, int)
	})
	if !ok {
		return
	}
	for i := 0; i < 3; i++ { // Session clear plus the two newest transport records.
		packet, size := source.TakeAppleNotification()
		if size <= 0 {
			break
		}
		if size != len(packet) {
			continue
		}
		if u.notifications == nil {
			if packet[4] >= 2 {
				continue
			}
			u.notifications = &notificationState{}
		}
		n := u.notifications
		_, buzz := n.inbox.ApplyApple(packet[:])
		if buzz && !n.quiet && !u.timers.active && (n.lastBuzz.IsZero() || now.Sub(n.lastBuzz) >= notificationBuzzGap) {
			n.lastBuzz, n.buzzUntil = now, now.Add(notificationPulse)
		}
	}
}

func (u *watchUI) notificationVibrating(now time.Time) bool {
	n := u.notifications
	if n == nil {
		return false
	}
	if u.timers.active || n.quiet {
		n.buzzUntil = time.Time{}
	}
	return now.Before(n.buzzUntil)
}

func (u *watchUI) handleNotifications(e inputEvent, state updateState) {
	n := u.notifications
	if n == nil {
		u.openInbox()
		return
	}
	if e.Kind == inputSwipeRight || (e.Kind == inputTap && inRect(e, 0, 0, 60, 42)) {
		u.back(state)
		return
	}
	if u.page == pageNotification {
		m := n.inbox.Find(n.selected)
		if m == nil {
			if e.Kind == inputTap && inRect(e, 12, 188, 228, 232) {
				u.page = pageInbox
			}
			return
		}
		if e.Kind == inputTap && inRect(e, 12, 188, 126, 232) {
			n.inbox.Dismiss(n.selected)
			u.page = pageInbox
		} else if e.Kind == inputSwipeLeft || (e.Kind == inputTap && inRect(e, 138, 188, 228, 232)) {
			data, size := notificationText(m)
			text := data[:size]
			next := notificationPageEnd(text, n.offset)
			if next >= len(text) {
				next = 0
			}
			n.offset = next
		}
		return
	}
	if e.Kind != inputTap {
		return
	}
	switch {
	case inRect(e, 12, 42, 114, 74):
		if n.quiet {
			u.askBattery(batteryBuzz)
		} else {
			n.quiet = true
		}
		n.buzzUntil = time.Time{}
	case inRect(e, 126, 42, 228, 74):
		n.inbox.Clear()
		n.buzzUntil = time.Time{}
	default:
		for i := 0; i < n.inbox.Count; i++ {
			if inRect(e, 12, int16(78+i*38), 228, int16(112+i*38)) {
				n.selected, n.offset = n.inbox.At(i).ID, 0
				n.inbox.Read(n.selected)
				n.buzzUntil = time.Time{}
				u.page = pageNotification
				return
			}
		}
	}
}

// ASCII has already been sanitized at receipt. Wrap on spaces when possible,
// but split long words so paging always advances. The 32-byte line cap also
// keeps temporary string conversions in Go's stack buffer. No slices grow.
func notificationLine(text []byte, start, width int) (line []byte, next int) {
	start = min(max(start, 0), len(text))
	for start < len(text) && text[start] == ' ' {
		start++
	}
	end, space := start, -1
	for end < len(text) && end-start < 32 {
		w, _ := lineWidth(&uifont.Regular18, string(text[start:end+1]))
		if int(w) > width && end > start {
			break
		}
		if text[end] == ' ' {
			space = end
		}
		end++
	}
	next = end
	if end < len(text) && space > start {
		end, next = space, space+1
	}
	return text[start:end], next
}
func notificationPageEnd(text []byte, start int) int {
	for i := 0; i < 6; i++ {
		_, start = notificationLine(text, start, 216)
	}
	return start
}

func notificationText(m *notifications.Message) (data [142]byte, size int) {
	title, body := notifications.Bytes(m.Title[:]), notifications.Bytes(m.Body[:])
	size = copy(data[:], title)
	if size > 0 && len(body) > 0 {
		size += copy(data[size:], ": ")
	}
	size += copy(data[size:], body)
	return
}

func (u *watchUI) drawNotifications(d canvas) {
	n := u.notifications
	if n == nil {
		return
	}
	if u.page == pageInbox {
		centered(d, &uifont.Regular18, 29, "INBOX", white)
		label, c := "BUZZ ON", card
		if n.quiet {
			label, c = "QUIET", positive
		}
		clockControl(d, 12, 42, 102, 32, label, c)
		clockControl(d, 126, 42, 102, 32, "CLEAR", card)
		if n.inbox.Count == 0 {
			centered(d, &uifont.Regular18, 117, "No messages", white)
			centered(d, &uifont.Regular18, 150, "Settings > Bluetooth", muted)
			centered(d, &uifont.Regular18, 179, "iPhone or Android", muted)
			centered(d, &uifont.Regular18, 222, "Cleared on restart", muted)
			return
		}
		for i := 0; i < n.inbox.Count; i++ {
			m := n.inbox.At(i)
			y := int16(78 + i*38)
			if !textVisible(d, &uifont.Regular18, y+24, "") {
				continue
			}
			label := notifications.Bytes(m.Title[:])
			if len(label) == 0 {
				label = notifications.Bytes(m.Body[:])
			}
			line, next := notificationLine(label, 0, 184)
			c := muted
			if m.Unread {
				c = white
				writeLine(d, &uifont.Regular18, 12, y+24, "*", accent)
			}
			if next < len(label) {
				line = line[:min(len(line), 29)]
				for len(line) > 0 {
					w, _ := lineWidth(&uifont.Regular18, string(line)+"...")
					if w <= 184 {
						break
					}
					line = line[:len(line)-1]
				}
				var clipped [32]byte
				size := copy(clipped[:], line)
				copy(clipped[size:], "...")
				writeLine(d, &uifont.Regular18, 30, y+24, string(clipped[:size+3]), c)
			} else {
				writeLine(d, &uifont.Regular18, 30, y+24, string(line), c)
			}
		}
		return
	}
	m := n.inbox.Find(n.selected)
	if m == nil {
		centered(d, &uifont.Regular18, 29, "MESSAGE", white)
		centered(d, &uifont.Regular18, 110, "Message replaced", white)
		centered(d, &uifont.Regular18, 142, "Newer messages arrived", muted)
		clockControl(d, 12, 188, 216, 40, "BACK TO INBOX", card)
		return
	}
	header := "MESSAGE"
	if m.Category == 3 {
		header = "INCOMING CALL"
	}
	if m.Category == 4 {
		header = "MISSED CALL"
	}
	centered(d, &uifont.Regular18, 29, header, white)
	data, size := notificationText(m)
	body := data[:size]
	pos := n.offset
	for i := 0; i < 6; i++ {
		line, next := notificationLine(body, pos, 216)
		c := white
		if pos < len(notifications.Bytes(m.Title[:])) {
			c = accent
		}
		writeLine(d, &uifont.Regular18, 12, int16(57+i*23), string(line), c)
		pos = next
	}
	clockControl(d, 12, 188, 114, 40, "DISMISS", card)
	if pos < len(body) {
		clockControl(d, 138, 188, 90, 40, "MORE", card)
	} else if n.offset > 0 {
		clockControl(d, 138, 188, 90, 40, "TOP", card)
	}
}
