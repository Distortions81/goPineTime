//go:build !baremetal

package main

import (
	"image/color"
	"runtime"
	"time"

	"github.com/Distortions81/goPine/internal/gfx"
	"github.com/veandco/go-sdl2/sdl"
	"tinygo.org/x/drivers/pixel"
)

type desktopDisplay struct {
	window             *sdl.Window
	surface            *sdl.Surface
	touch              touchTracker
	pointerDown        bool
	pointerX, pointerY int16
	asleep             bool
	touchWake          bool
	brightness         uint8
	flipped            bool
	vibrating          bool
	chargeState        chargeState
}

func openDisplay() (clockDisplay, error) {
	// SDL requires its video calls and event loop to stay on one OS thread.
	runtime.LockOSThread()
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		runtime.UnlockOSThread()
		return nil, err
	}

	window, err := sdl.CreateWindow(
		"goPine",
		sdl.WINDOWPOS_UNDEFINED,
		sdl.WINDOWPOS_UNDEFINED,
		240,
		240,
		sdl.WINDOW_SHOWN,
	)
	if err != nil {
		sdl.Quit()
		runtime.UnlockOSThread()
		return nil, err
	}

	surface, err := window.GetSurface()
	if err != nil {
		window.Destroy()
		sdl.Quit()
		runtime.UnlockOSThread()
		return nil, err
	}

	return &desktopDisplay{window: window, surface: surface, chargeState: chargeExternalPower}, nil
}

func (d *desktopDisplay) Size() (int16, int16) {
	return int16(d.surface.W), int16(d.surface.H)
}

func (d *desktopDisplay) SetPixel(x, y int16, c color.RGBA) {
	if x < 0 || y < 0 || int32(x) >= d.surface.W || int32(y) >= d.surface.H {
		return
	}
	if d.flipped {
		x, y = 239-x, 239-y
	}
	if c.A != 255 {
		c = gfx.Over(color.RGBAModel.Convert(d.surface.At(int(x), int(y))).(color.RGBA), c)
	}
	d.surface.Set(int(x), int(y), c)
}

func (d *desktopDisplay) FillScreen(c color.RGBA) {
	pixel := sdl.MapRGBA(d.surface.Format, c.R, c.G, c.B, c.A)
	_ = d.surface.FillRect(nil, pixel)
}

func (d *desktopDisplay) FillRectangle(x, y, width, height int16, c color.RGBA) error {
	if c.A != 255 {
		for yy := y; yy < y+height; yy++ {
			for xx := x; xx < x+width; xx++ {
				d.SetPixel(xx, yy, c)
			}
		}
		return nil
	}
	if d.flipped {
		x, y = 240-x-width, 240-y-height
	}
	return d.surface.FillRect(&sdl.Rect{X: int32(x), Y: int32(y), W: int32(width), H: int32(height)}, sdl.MapRGBA(d.surface.Format, c.R, c.G, c.B, c.A))
}

func (d *desktopDisplay) DrawBitmap(x, y int16, bitmap pixel.Image[pixel.RGB444BE]) error {
	w, h := bitmap.Size()
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < w; xx++ {
			d.SetPixel(x+int16(xx), y+int16(yy), bitmap.Get(xx, yy).RGBA())
		}
	}
	return nil
}

func (d *desktopDisplay) pointerEvent() inputEvent {
	var data [6]byte
	if d.pointerDown {
		data[1] = 1
	}
	data[2], data[3] = byte(d.pointerX>>8), byte(d.pointerX)
	data[4], data[5] = byte(d.pointerY>>8), byte(d.pointerY)
	return d.touch.decode(data, time.Now()).inputEvent
}

func (d *desktopDisplay) Display() error {
	return d.window.UpdateSurface()
}

func (d *desktopDisplay) PowerStatus() powerStatus {
	return powerStatus{Percent: 100, State: d.chargeState}
}

func (d *desktopDisplay) Wake() error {
	d.asleep, d.pointerDown = false, false
	d.touch.cancelContact()
	return d.Display()
}

func (d *desktopDisplay) KeepAwake() {}

func (d *desktopDisplay) SetTouchWake(enabled bool) { d.touchWake = enabled }

func (d *desktopDisplay) SetBrightness(level uint8) {
	level = validBrightness(level)
	if d.brightness == level {
		return
	}
	d.brightness = level
	value := float32(1)
	if level == brightnessLow {
		value = 0.35
	} else if level == brightnessMedium {
		value = 0.65
	}
	// Some SDL drivers (including headless tests) cannot change brightness.
	// Retain the selected level; the watch uses its physical backlight pins.
	_ = d.window.SetBrightness(value)
}

func (d *desktopDisplay) SetVibration(on bool) {
	if d.vibrating == on {
		return
	}
	d.vibrating = on
	label := "goPine"
	if on {
		label += " - vibrating"
	}
	d.window.SetTitle(label)
}

func (d *desktopDisplay) Wait(duration time.Duration) (inputEvent, error) {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			switch e := event.(type) {
			case *sdl.KeyboardEvent:
				if e.Type == sdl.KEYDOWN && e.Repeat == 0 && e.Keysym.Sym == sdl.K_c {
					d.chargeState = (d.chargeState + 1) % 3
					return inputEvent{Kind: inputPower}, nil
				}
				if e.Type == sdl.KEYDOWN && e.Repeat == 0 && e.Keysym.Sym == sdl.K_SPACE {
					d.pointerDown = false
					d.touch.cancelContact()
					d.asleep = !d.asleep
					if d.asleep {
						d.FillScreen(black)
						_ = d.Display()
						return inputEvent{Kind: inputSleep}, nil
					}
					return inputEvent{Kind: inputWake}, nil
				}
			case *sdl.QuitEvent:
				return inputEvent{Kind: inputQuit}, nil
			case *sdl.WindowEvent:
				_ = d.window.UpdateSurface()
				if e.Event == sdl.WINDOWEVENT_FOCUS_LOST {
					d.pointerDown = false
					d.touch.cancel()
					return inputEvent{Kind: inputCancel}, nil
				}
			case *sdl.MouseButtonEvent:
				if e.Button != sdl.BUTTON_LEFT {
					continue
				}
				if d.asleep {
					if d.touchWake && e.Type == sdl.MOUSEBUTTONDOWN {
						_ = d.Wake()
						return inputEvent{Kind: inputWake}, nil
					}
					continue
				}
				d.pointerDown = e.Type == sdl.MOUSEBUTTONDOWN
				d.pointerX, d.pointerY = int16(e.X), int16(e.Y)
				return d.pointerEvent(), nil
			case *sdl.MouseMotionEvent:
				if d.pointerDown {
					d.pointerX, d.pointerY = int16(e.X), int16(e.Y)
					return d.pointerEvent(), nil
				}
			}
		}
		if d.pointerDown {
			time.Sleep(20 * time.Millisecond)
			return d.pointerEvent(), nil
		}

		remaining := time.Until(deadline)
		if remaining > 16*time.Millisecond {
			remaining = 16 * time.Millisecond
		}
		if remaining > 0 {
			time.Sleep(remaining)
		}
	}
	return inputEvent{Kind: inputRefresh}, nil
}

func (d *desktopDisplay) Close() error {
	err := d.window.Destroy()
	sdl.Quit()
	runtime.UnlockOSThread()
	return err
}

func (d *desktopDisplay) SetFlipped(flipped bool) error {
	d.flipped = flipped
	d.pointerDown = false
	d.touch.cancelContact()
	return nil
}
