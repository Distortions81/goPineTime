package main

import (
	"image/color"
	"time"

	"tinygo.org/x/drivers"
	"tinygo.org/x/drivers/pixel"
)

type canvas interface {
	drivers.Displayer
	FillScreen(color.RGBA)
	FillRectangle(x, y, width, height int16, c color.RGBA) error
}

type clockDisplay interface {
	drivers.Displayer
	FillScreen(color.RGBA)
	DrawBitmap(x, y int16, bitmap pixel.Image[pixel.RGB444BE]) error
	PowerStatus() powerStatus
	Wait(time.Duration) (inputEvent, error)
	Wake() error
	KeepAwake()
	SetTouchWake(bool)
	SetFlipped(bool) error
	SetVibration(bool)
	Close() error
}

type inputKind uint8

const (
	inputRefresh inputKind = iota
	inputWake
	inputTap
	inputQuit
	inputPress
	inputHold
	inputRelease
	inputCancel
	inputSwipeLeft
	inputSwipeRight
	inputSleep
	inputPower
	inputSwipeUp
	inputSwipeDown
)

type inputEvent struct {
	Kind inputKind
	X, Y int16
	Held time.Duration
}
