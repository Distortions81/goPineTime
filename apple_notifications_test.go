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
