package main

import (
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/checkpoint"
	"github.com/Distortions81/goPine/internal/uifont"
)

func TestBacklightLevelsAndSleep(t *testing.T) {
	for _, tc := range []struct {
		level, pins uint8
	}{{brightnessHigh, 7}, {brightnessLow, 1}, {brightnessMedium, 3}, {255, 7}} {
		if got := backlightChannels(tc.level, true); got != tc.pins {
			t.Fatal("unexpected active-low backlight channels", tc.level, got)
		}
		if got := backlightChannels(tc.level, false); got != 0 {
			t.Fatal("sleep left a backlight channel on", tc.level, got)
		}
	}
}

func TestDisplayControlsHaveSeparateRowsAndRepaint(t *testing.T) {
	now := time.Unix(0, 0)
	u := newWatchUI(firmwareConfirmed)
	u.page = pageDisplaySettings
	for _, y := range []int16{84, 130, 176, 225} {
		before := u.frameKey(now, powerStatus{})
		u.handle(inputEvent{Kind: inputTap, X: 120, Y: y}, now, firmwareConfirmed, powerStatus{})
		if u.frameKey(now, powerStatus{}) != before {
			t.Fatal("gap between display rows activated a setting", y)
		}
	}
	tap := func(y int16) {
		before := u.frameKey(now, powerStatus{})
		u.handle(inputEvent{Kind: inputTap, X: 120, Y: y}, now, firmwareConfirmed, powerStatus{})
		if u.frameKey(now, powerStatus{}) == before {
			t.Fatal("display choice did not repaint", y)
		}
	}
	tap(62)
	if !u.lightMode || u.brightness != brightnessHigh || u.touchWake || u.flipScreen {
		t.Fatal("theme row changed another setting")
	}
	for _, want := range []uint8{brightnessLow, brightnessMedium, brightnessHigh} {
		tap(108)
		u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
		if u.brightness != want || !u.lightMode || u.touchWake || u.flipScreen {
			t.Fatal("brightness did not cycle independently", want)
		}
	}
	tap(154)
	u.handle(inputEvent{Kind: inputTap, X: 177, Y: 210}, now, firmwareConfirmed, powerStatus{})
	tap(200)
	if !u.touchWake || !u.flipScreen || !u.lightMode || u.brightness != brightnessHigh {
		t.Fatal("touch and rotation rows changed unrelated settings")
	}
}

func TestDisplaySettingsRenderBudget(t *testing.T) {
	now := time.Unix(0, 0)
	u := newWatchUI(firmwareConfirmed)
	u.page = pageDisplaySettings
	d := &benchmarkDisplay{}
	var renderer frameRenderer
	draw := func(c canvas) { u.drawFrame(c, now, powerStatus{}) }
	for _, light := range []bool{false, true} {
		for _, level := range []uint8{brightnessHigh, brightnessLow, brightnessMedium} {
			u.lightMode, u.brightness = light, level
			if width, _ := lineWidth(&uifont.Regular18, brightnessLabel(level)); width > 196 {
				t.Fatal("backlight label does not leave card padding", level, width)
			}
			if err := renderer.render(d, draw); err != nil {
				t.Fatal(err)
			}
			if allocations := testing.AllocsPerRun(20, func() {
				u.frameKey(now, powerStatus{})
				if err := renderer.render(d, draw); err != nil {
					panic(err)
				}
			}); allocations != 0 {
				t.Fatal("display settings rendering allocated", light, level, allocations)
			}
		}
	}
}

func TestDisplayChoicesPersistWithTimersAndPhone(t *testing.T) {
	f, j, _ := clockTestStorage(t)
	now := time.Unix(0, 0)
	u := newWatchUI(firmwareConfirmed)
	u.use24, u.phoneAuto, u.touchWake, u.flipScreen = true, true, true, true
	u.timers.alarms[0] = alarm{hour: 7, minute: 30, repeat: alarmWeekdays, enabled: true}
	u.timers.countdown.preset = 10 * time.Minute
	u.timers.watch.toggle(now)
	p := loadSettings(&u, now, j)
	for _, light := range []bool{true, false} {
		for _, level := range []uint8{brightnessLow, brightnessMedium, brightnessHigh} {
			u.lightMode, u.brightness = light, level
			p.update(&u, now, true)
			p.update(&u, now.Add(settingsSaveDelay), true)
			reopened, err := checkpoint.Open(f)
			if err != nil {
				t.Fatal(err)
			}
			v := newWatchUI(firmwareConfirmed)
			loadSettings(&v, now.Add(settingsSaveDelay), reopened)
			if v.lightMode != light || v.brightness != level || !v.use24 || !v.phoneAuto || !v.touchWake || !v.flipScreen ||
				v.timers.alarms[0] != u.timers.alarms[0] || v.timers.countdown.preset != 10*time.Minute || !v.timers.watch.running {
				t.Fatal("display save lost an existing setting or timer", light, level)
			}
			now = now.Add(time.Minute)
		}
	}
}

type brightnessDisplay struct {
	scriptDisplay
	level uint8
}

func (d *brightnessDisplay) SetBrightness(level uint8) { d.level = level }

func TestLoopAppliesSavedBrightnessAndRetainsItAcrossSleep(t *testing.T) {
	d := &brightnessDisplay{scriptDisplay: *newScriptDisplay()}
	u := newWatchUI(firmwareConfirmed)
	u.page, u.brightness = pageDisplaySettings, brightnessLow
	d.steps = []scriptStep{
		{event: inputEvent{Kind: inputTap, X: 120, Y: 108}, check: func() {
			if d.level != brightnessLow {
				t.Fatal("saved brightness was not applied before the first frame")
			}
		}},
		{at: 100 * time.Millisecond, event: inputEvent{Kind: inputTap, X: 177, Y: 210}},
		{at: time.Second, event: inputEvent{Kind: inputSleep}, check: func() {
			if d.level != brightnessMedium {
				t.Fatal("brightness change was not applied by the loop")
			}
		}},
		{at: 2 * time.Second, event: inputEvent{Kind: inputRefresh}, check: func() {
			if !d.asleep || backlightChannels(d.level, !d.asleep) != 0 {
				t.Fatal("sleep retained a backlight channel")
			}
			u.brightness = brightnessHigh
		}},
		{at: 3 * time.Second, event: inputEvent{Kind: inputWake}, check: func() {
			if d.level != brightnessHigh || !d.asleep || backlightChannels(d.level, !d.asleep) != 0 {
				t.Fatal("updating brightness while asleep lit the screen")
			}
		}},
		{at: 4 * time.Second, event: inputEvent{Kind: inputQuit}, check: func() {
			if d.asleep || d.level != brightnessHigh || backlightChannels(d.level, !d.asleep) != 7 {
				t.Fatal("wake lost selected brightness")
			}
		}},
	}
	l := watchLoop{display: d, ui: &u, sync: &timeSyncController{radio: &fakeTimeRadio{}}, renderer: &frameRenderer{},
		now: func() time.Time { return d.now }, state: func() updateState { return firmwareConfirmed }}
	if err := l.run(); err != nil {
		t.Fatal(err)
	}
}
