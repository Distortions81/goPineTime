package main

import (
	"image/color"
	"time"

	"github.com/Distortions81/goPine/internal/gfx"
	"github.com/Distortions81/goPine/internal/uifont"
)

// Hours stay in 24-hour form internally; the editor presents the selected
// display format and rolls AM/PM naturally when stepping through noon.
type clockEdit struct{ year, month, day, hour, minute int }

func daysInMonth(year, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func (u *watchUI) beginClockEdit(page page, now time.Time) {
	now = u.clock.Now(now)
	u.edit = clockEdit{now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute()}
	if page == pageSetDate {
		u.edit.year = max(2000, min(2099, u.edit.year))
		u.edit.day = min(u.edit.day, daysInMonth(u.edit.year, u.edit.month))
	}
	u.page = page
}

func (u *watchUI) handleClockTap(e inputEvent, now time.Time) {
	if u.page == pageTimeSettings {
		switch {
		case inRect(e, 16, 42, 224, 82):
			u.beginClockEdit(pageSetTime, now)
		case inRect(e, 16, 88, 224, 128):
			u.beginClockEdit(pageSetDate, now)
		case inRect(e, 16, 134, 224, 174):
			u.use24 = !u.use24
		}
		return
	}
	if inRect(e, 12, 188, 114, 232) {
		u.back(firmwareConfirmed)
		return
	}
	if inRect(e, 126, 188, 228, 232) {
		if u.page == pageAlarmEdit {
			u.page = pageAlarmRepeat
			return
		}
		current := u.clock.Now(now)
		y, m, day := current.Date()
		hour, minute, second := current.Clock()
		if u.page == pageSetTime {
			hour, minute, second = u.edit.hour, u.edit.minute, 0
		} else {
			y, m, day = u.edit.year, time.Month(u.edit.month), u.edit.day
		}
		u.clock.Set(now, time.Date(y, m, day, hour, minute, second, 0, current.Location()))
		u.page = pageTimeSettings
		return
	}
	delta := 0
	if e.Y >= 48 && e.Y < 92 {
		delta = 1
	}
	if e.Y >= 136 && e.Y < 180 {
		delta = -1
	}
	if delta == 0 {
		return
	}
	if u.page == pageSetTime || u.page == pageAlarmEdit {
		if e.X >= 16 && e.X < 104 {
			u.edit.hour = (u.edit.hour + delta + 24) % 24
		}
		if e.X >= 136 && e.X < 224 {
			u.edit.minute = (u.edit.minute + delta + 60) % 60
		}
		return
	}
	switch {
	case e.X >= 16 && e.X < 80:
		u.edit.year = max(2000, min(2099, u.edit.year+delta))
	case e.X >= 88 && e.X < 152:
		u.edit.month = (u.edit.month-1+delta+12)%12 + 1
	case e.X >= 160 && e.X < 224:
		days := daysInMonth(u.edit.year, u.edit.month)
		u.edit.day = (u.edit.day-1+delta+days)%days + 1
	}
	u.edit.day = min(u.edit.day, daysInMonth(u.edit.year, u.edit.month))
}

func clockControl(d canvas, x, y, w, h int16, label string, c color.RGBA) {
	clockControlLabel(d, x, y, w, h, label, c, white)
}

func clockControlLabel(d canvas, x, y, w, h int16, label string, c, foreground color.RGBA) {
	if !controlVisible(d, y, h, y+h/2+7) {
		return
	}
	gfx.RoundBox(d, x, y, w, h, 6, c)
	tw, _ := lineWidth(&uifont.Regular18, label)
	writeLine(d, &uifont.Regular18, x+(w-int16(tw))/2, y+h/2+7, label, foreground)
}

func controlVisible(d canvas, y, height, baseline int16) bool {
	_, top, _, bottom := gfx.Bounds(d)
	lo, hi := uifont.Regular18.VerticalBounds()
	return min(int(y), int(baseline)+int(lo)) < bottom &&
		max(int(y)+int(height), int(baseline)+int(hi)) > top
}

func (u *watchUI) drawClockSettings(d canvas, now time.Time) {
	if u.page == pageTimeSettings {
		centered(d, &uifont.Regular18, 29, "TIME & DATE", white)
		clockControl(d, 16, 42, 208, 40, "SET TIME", card)
		clockControl(d, 16, 88, 208, 40, "SET DATE", card)
		label := "FORMAT: 12 HOUR"
		if u.use24 {
			label = "FORMAT: 24 HOUR"
		}
		clockControl(d, 16, 134, 208, 40, label, card)
		centered(d, &uifont.Regular18, 207, "Auto sync with phone", muted)
		return
	}
	title := "SET TIME"
	if u.page == pageAlarmEdit {
		title = "SET ALARM"
	}
	if u.page == pageSetDate {
		title = "SET DATE"
	}
	centered(d, &uifont.Regular18, 29, title, white)
	if u.page == pageSetTime || u.page == pageAlarmEdit {
		hour := u.edit.hour
		if !u.use24 {
			hour %= 12
			if hour == 0 {
				hour = 12
			}
		}
		for i, v := range []int{hour, u.edit.minute} {
			x := int16(16 + i*120)
			clockControl(d, x, 48, 88, 44, "+", card)
			clockControl(d, x, 136, 88, 44, "-", card)
			label := twoDigits(v)
			w, _ := lineWidth(&uifont.Regular18, label)
			writeLine(d, &uifont.Regular18, x+(88-int16(w))/2, 114, label, white)
			caption := "MINUTE"
			if i == 0 {
				caption = "HOUR"
			}
			if i == 0 && !u.use24 {
				caption = "AM"
				if u.edit.hour >= 12 {
					caption = "PM"
				}
			}
			w, _ = lineWidth(&uifont.Regular18, caption)
			writeLine(d, &uifont.Regular18, x+(88-int16(w))/2, 132, caption, muted)
		}
		centered(d, &uifont.Regular18, 114, ":", muted)
	} else {
		for i, v := range []int{u.edit.year, u.edit.month, u.edit.day} {
			x := int16(16 + i*72)
			clockControl(d, x, 48, 64, 44, "+", card)
			clockControl(d, x, 136, 64, 44, "-", card)
			label := twoDigits(v)
			w, _ := lineWidth(&uifont.Regular18, label)
			writeLine(d, &uifont.Regular18, x+(64-int16(w))/2, 114, label, white)
			labels := [...]string{"YEAR", "MON", "DAY"}
			w, _ = lineWidth(&uifont.Regular18, labels[i])
			writeLine(d, &uifont.Regular18, x+(64-int16(w))/2, 132, labels[i], muted)
		}
	}
	clockControl(d, 12, 188, 102, 44, "CANCEL", card)
	label := "SAVE"
	if u.page == pageAlarmEdit {
		label = "NEXT"
	}
	clockControl(d, 126, 188, 102, 44, label, positive)
}
