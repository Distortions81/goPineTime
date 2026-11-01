package main

import (
	"strconv"
	"time"
)

// Only integer formatting is needed on the watch. Avoid retaining fmt's
// reflection, floating-point formatting, and generic printer machinery.
func decimal(v int) string { return strconv.Itoa(v) }

// Fixed display text avoids strconv's slice-return escape in TinyGo. Both
// methods only copy into caller-owned storage and never grow a slice.
type fixedDisplayText struct {
	data [32]byte
	n    int
}

func (t *fixedDisplayText) add(s string) { t.n += copy(t.data[t.n:], s) }
func (t *fixedDisplayText) number(value uint32) {
	var digits [10]byte
	i := len(digits)
	for {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
		if value == 0 {
			break
		}
	}
	t.n += copy(t.data[t.n:], digits[i:])
}

func twoDigits(v int) string {
	if v >= 0 && v < 100 {
		const pairs = "00010203040506070809101112131415161718192021222324252627282930313233343536373839404142434445464748495051525354555657585960616263646566676869707172737475767778798081828384858687888990919293949596979899"
		return pairs[v*2 : v*2+2]
	}
	return decimal(v)
}

func timeDigits(t time.Time, use24 bool) (text [5]byte, start int) {
	hour, minute, _ := t.Clock()
	if !use24 {
		hour %= 12
		if hour == 0 {
			hour = 12
		}
		if hour < 10 {
			start = 1
		}
	}
	text = [5]byte{byte('0' + hour/10), byte('0' + hour%10), ':', byte('0' + minute/10), byte('0' + minute%10)}
	return
}

func percentDigits(percent uint8) (text [4]byte, start int) {
	text = [4]byte{byte('0' + percent/100), byte('0' + percent/10%10), byte('0' + percent%10), '%'}
	if percent < 100 {
		start = 1
	}
	if percent < 10 {
		start = 2
	}
	return
}

type contextError struct {
	context string
	cause   error
}

func (e *contextError) Error() string             { return e.context + ": " + e.cause.Error() }
func (e *contextError) Unwrap() error             { return e.cause }
func wrapError(context string, cause error) error { return &contextError{context, cause} }

func syncTimeDigits(t time.Time) [8]byte {
	h, m, s := t.Clock()
	return [8]byte{byte('0' + h/10), byte('0' + h%10), ':', byte('0' + m/10), byte('0' + m%10), ':', byte('0' + s/10), byte('0' + s%10)}
}

func syncDateDigits(t time.Time) (text [11]byte) {
	const months = "JanFebMarAprMayJunJulAugSepOctNovDec"
	y, m, d := t.Date()
	i := (int(m) - 1) * 3
	text = [11]byte{byte('0' + d/10), byte('0' + d%10), ' ', months[i], months[i+1], months[i+2], ' ', byte('0' + y/1000%10), byte('0' + y/100%10), byte('0' + y/10%10), byte('0' + y%10)}
	return
}
