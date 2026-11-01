package main

import (
	"github.com/Distortions81/goPine/internal/notifications"
	"testing"
	"time"
)

type appleTestRadio struct {
	fakePhoneRadio
	packets [][notifications.ApplePacket]byte
}

func (r *appleTestRadio) TakeAppleNotification() (p [notifications.ApplePacket]byte, n int) {
	if len(r.packets) == 0 {
		return
	}
	p = r.packets[0]
	r.packets = r.packets[1:]
	return p, len(p)
}
func appleTestPacket(uid uint32, event, flags byte) (p [notifications.ApplePacket]byte) {
	for i := 0; i < 4; i++ {
		p[i] = byte(uid >> uint(i*8))
	}
	p[4], p[5], p[6] = event, 4, flags
	p[147] = 1
	copy(p[7:47], "Messages")
	copy(p[47:], "Hello from iPhone")
	return
}
func TestAppleInboxBuzzAndSessionClearing(t *testing.T) {
	now := time.Now()
	u := newWatchUI(firmwareConfirmed)
	r := &appleTestRadio{packets: [][notifications.ApplePacket]byte{appleTestPacket(0, 3, 0), appleTestPacket(1, 0, 4), appleTestPacket(2, 0, 0)}}
	u.receiveAppleNotifications(r, now)
	if u.unreadNotifications() != 2 || !u.notificationVibrating(now) {
		t.Fatal("inbox or new-notification vibration missing")
	}
	r.packets = [][notifications.ApplePacket]byte{appleTestPacket(2, 2, 0), appleTestPacket(0, 3, 0)}
	u.receiveAppleNotifications(r, now.Add(time.Minute))
	if u.unreadNotifications() != 0 {
		t.Fatal("stale ANCS session data retained")
	}
}

func TestClearAllFromInboxBlocksIPhoneReplayWithoutBuzz(t *testing.T) {
	now := time.Unix(100, 0)
	u := newWatchUI(firmwareConfirmed)
	u.openInbox()
	packet := func(uid uint32, event, flags, source byte) [notifications.ApplePacket]byte {
		p := appleTestPacket(uid, event, flags)
		p[147], p[148], p[149] = source, 1, 10
		return p
	}
	r := &appleTestRadio{packets: [][notifications.ApplePacket]byte{packet(1, 0, 0, 1), packet(2, 0, 0, 1)}}
	u.receiveAppleNotifications(r, now)
	u.handle(inputEvent{Kind: inputTap, X: 180, Y: 58}, now, firmwareConfirmed, powerStatus{})
	if u.notifications.inbox.Count != 0 || u.notificationVibrating(now) {
		t.Fatal("clear all left messages or vibration")
	}
	r.packets = [][notifications.ApplePacket]byte{packet(0, 3, 0, 1), packet(1, 0, 4, 2), packet(2, 1, 0, 2)}
	u.receiveAppleNotifications(r, now.Add(time.Minute))
	if u.unreadNotifications() != 0 || u.notificationVibrating(now.Add(time.Minute)) {
		t.Fatal("iPhone replay returned cleared messages or vibration")
	}
	r.packets = [][notifications.ApplePacket]byte{packet(3, 0, 0, 2)}
	u.receiveAppleNotifications(r, now.Add(2*time.Minute))
	if u.unreadNotifications() != 1 || !u.notificationVibrating(now.Add(2*time.Minute)) {
		t.Fatal("new iPhone message was suppressed")
	}
}
