package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/music"
	"github.com/Distortions81/goPine/internal/notifications"
	"github.com/Distortions81/goPine/internal/uifont"
)

type fakeNotificationRadio struct {
	fakePhoneRadio
	packets [][]byte
	takes   int
}

func (r *fakeNotificationRadio) TakeNotification() (value [notifications.MaxPacket]byte, size int) {
	r.takes++
	if len(r.packets) == 0 {
		return
	}
	size = copy(value[:], r.packets[0])
	r.packets = r.packets[1:]
	return
}
func notificationPacket(text string) []byte { return append([]byte{255, 1, 0}, text...) }
func sampleNotifications() *notificationState {
	n := &notificationState{}
	n.inbox.Add(notificationPacket("Calendar\x00Design review at 2:30. Bring the prototype and notes."))
	n.inbox.Add(notificationPacket("Mail: Alice\x00Lunch at noon?"))
	n.inbox.Add(notificationPacket("Messages: Jordan\x00I'll meet you at the trailhead."))
	n.inbox.Add(notificationPacket("A very long source and sender name\x00" + strings.Repeat("W", 65)))
	n.selected = n.inbox.At(0).ID
	return n
}

func notificationPreview(kind string) *notificationState {
	n := sampleNotifications()
	switch kind {
	case "quiet":
		n.quiet = true
	case "read":
		n.selected = n.inbox.At(2).ID
		n.inbox.Read(n.selected)
	case "more":
		data, size := notificationText(n.inbox.Find(n.selected))
		n.offset = notificationPageEnd(data[:size], 0)
	case "replaced":
		n.selected = 12345
	case "call":
		n.inbox.Add([]byte{3, 1, 0, 'A', 'l', 'i', 'c', 'e'})
		n.selected = n.inbox.At(0).ID
	}
	return n
}

func TestNotificationControllerBoundedAndOptIn(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneOff)
	r := &fakeNotificationRadio{packets: [][]byte{notificationPacket("First"), notificationPacket("Second"), notificationPacket("Third")}}
	c := timeSyncController{radio: r}
	c.update(&u, now, 80)
	if r.takes != 0 || u.notifications != nil {
		t.Fatal("received while off")
	}
	u.phone.mode = phoneConnected
	r.report(music.LinkAuthenticated, false, 1) // No music subscription is required for notifications.
	c.update(&u, now, 80)
	if r.takes != 2 || u.notifications.inbox.Count != 2 || !u.notificationVibrating(now) {
		t.Fatal("bounded receipt")
	}
	until := u.notifications.buzzUntil
	c.update(&u, now.Add(time.Millisecond), 80)
	if u.notifications.inbox.Count != 3 || u.notifications.buzzUntil != until {
		t.Fatal("burst extended buzz")
	}
	if u.notificationVibrating(until) {
		t.Fatal("pulse failed to end")
	}
	u.notifications.quiet = true
	r.packets = [][]byte{notificationPacket("Quiet message")}
	c.update(&u, now.Add(time.Minute), 80)
	if u.notificationVibrating(now.Add(time.Minute)) || u.unreadNotifications() != 4 {
		t.Fatal("quiet discarded or buzzed")
	}
	u.notifications.quiet = false
	u.timers.active = true
	u.page = pageAlert
	r.packets = [][]byte{notificationPacket("During alarm")}
	c.update(&u, now.Add(2*time.Minute), 80)
	if u.page != pageAlert || u.notificationVibrating(now.Add(2*time.Minute)) || u.notifications.inbox.Count != 4 {
		t.Fatal("notification stole alarm")
	}
}

func TestNotificationNavigationAndStableSelection(t *testing.T) {
	now := time.Now()
	u := newWatchUI(firmwareConfirmed)
	u.page = pageApps
	u.handle(inputEvent{Kind: inputTap, X: 63, Y: 197}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageInbox || u.notifications == nil || u.phone != nil {
		t.Fatal("opening inbox should leave radio off")
	}
	u.handle(inputEvent{Kind: inputTap, X: 200, Y: 25}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageInbox || u.phone != nil {
		t.Fatal("Inbox must not contain a pairing entry")
	}
	n := sampleNotifications()
	u.notifications = n
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 94}, now, firmwareConfirmed, powerStatus{})
	if u.page != pageNotification || n.inbox.Unread() != 3 {
		t.Fatal("open should mark read")
	}
	selected := n.selected
	u.receiveNotifications(&fakeNotificationRadio{packets: [][]byte{notificationPacket("New")}}, now)
	if n.selected != selected || u.page != pageNotification {
		t.Fatal("new arrival changed viewed message")
	}
	u.handle(inputEvent{Kind: inputTap, X: 190, Y: 205}, now, firmwareConfirmed, powerStatus{})
	if n.offset <= 0 {
		t.Fatal("long message did not page")
	}
	for i := 0; i < 4; i++ {
		u.receiveNotifications(&fakeNotificationRadio{packets: [][]byte{notificationPacket("Newer")}}, now)
	}
	if u.page != pageNotification || n.inbox.Find(selected) != nil {
		t.Fatal("eviction silently switched pages or retained old text")
	}
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 205}, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 120, Y: 94}, now, firmwareConfirmed, powerStatus{})
	u.handle(inputEvent{Kind: inputTap, X: 65, Y: 205}, now, firmwareConfirmed, powerStatus{})
	if n.inbox.Count != 3 || u.page != pageInbox {
		t.Fatal("local dismissal")
	}
	u.handle(inputEvent{Kind: inputTap, X: 180, Y: 58}, now, firmwareConfirmed, powerStatus{})
	if n.inbox.Count != 0 {
		t.Fatal("clear")
	}
}

func TestNotificationWrappingPreservesText(t *testing.T) {
	for _, text := range []string{"", "Hello world", strings.Repeat("W", 100), "A  string with several words and enough content to span multiple lines."} {
		pos, reconstructed := 0, ""
		for i := 0; pos < len(text) && i < 150; i++ {
			line, next := notificationLine([]byte(text), pos, 216)
			w, _ := lineWidth(&uifont.Regular18, string(line))
			if w > 216 {
				t.Fatal("line exceeds viewport")
			}
			if next <= pos || next > len(text) {
				t.Fatal("wrap did not advance")
			}
			reconstructed += string(line)
			pos = next
		}
		if strings.ReplaceAll(reconstructed, " ", "") != strings.ReplaceAll(text, " ", "") {
			t.Fatal("wrap lost text")
		}
	}
}

func TestNotificationFramesAndAllocations(t *testing.T) {
	now := time.Now()
	for _, pg := range []page{pageInbox, pageNotification, pageClock} {
		u := newWatchUI(firmwareConfirmed)
		u.page = pg
		u.notifications = sampleNotifications()
		d, ref := &memoryDisplay{}, &memoryDisplay{}
		draw := func(c canvas) { u.drawFrame(c, now, powerStatus{Percent: 80}) }
		ref.FillScreen(black)
		draw(ref)
		var r frameRenderer
		if err := r.render(d, draw); err != nil {
			t.Fatal(err)
		}
		if d.pixels != ref.pixels {
			t.Fatal("strip clipping changed notifications", pg)
		}
		if n := testing.AllocsPerRun(20, func() { u.frameKey(now, powerStatus{}); r.render(d, draw) }); n != 0 {
			t.Fatal("draw allocations", pg, n)
		}
	}
}

func TestNotificationBuzzWhileAsleepAndAlarmPriority(t *testing.T) {
	d := newScriptDisplay()
	u := phoneUI(d.now, phoneConnected)
	u.page = pageClock
	r := &fakeNotificationRadio{}
	u.timers.countdown = countdown{running: true, deadline: d.now.Add(3 * time.Second)}
	d.steps = []scriptStep{
		{at: time.Second, event: inputEvent{Kind: inputSleep}},
		{at: 2 * time.Second, check: func() { r.packets = [][]byte{notificationPacket("Asleep")} }},
		{at: 2050 * time.Millisecond, check: func() {
			if !d.asleep || !d.vibrating || d.wakes != 0 {
				t.Fatal("notification woke display or did not buzz")
			}
		}},
		{at: 2200 * time.Millisecond, check: func() {
			if d.vibrating || !d.asleep {
				t.Fatal("pulse did not stop asleep")
			}
		}},
		{at: 3 * time.Second, check: func() { r.packets = [][]byte{notificationPacket("Alarm wins")} }},
		{at: 3050 * time.Millisecond, check: func() {
			if u.page != pageAlert || d.wakes != 1 || !d.vibrating {
				t.Fatal("alarm priority")
			}
		}},
		{at: 3300 * time.Millisecond, check: func() {
			if d.vibrating {
				t.Fatal("notification extended alarm vibration")
			}
		}},
		{at: 3500 * time.Millisecond, event: inputEvent{Kind: inputQuit}},
	}
	if err := runScript(t, d, &u, r); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationDeadlineAndSuppression(t *testing.T) {
	now := time.Now()
	u := phoneUI(now, phoneConnected)
	r := &fakeNotificationRadio{packets: [][]byte{notificationPacket("Pulse")}}
	u.receiveNotifications(r, now)
	if got := nextLoopDelay(now, &u, now.Add(time.Second), false); got != notificationPulse {
		t.Fatal("missing asleep motor cutoff", got)
	}
	u.timers.active = true
	if u.notificationVibrating(now.Add(20 * time.Millisecond)) {
		t.Fatal("alarm did not cancel pulse")
	}
	u.timers.active = false
	if u.notificationVibrating(now.Add(30 * time.Millisecond)) {
		t.Fatal("notification resumed after alarm")
	}
	u.notifications.quiet = true
	r.packets = [][]byte{notificationPacket("Silent")}
	u.receiveNotifications(r, now.Add(time.Minute))
	if got := nextLoopDelay(now.Add(time.Minute), &u, now.Add(2*time.Minute), false); got != noDeadlineDelay {
		t.Fatal("quiet notification added polling", got)
	}
}
