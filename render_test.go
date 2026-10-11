package main

import (
	"errors"
	"image/color"
	"math/rand"
	"testing"
	"time"

	"github.com/Distortions81/goPine/internal/gfx"
	"tinygo.org/x/drivers/pixel"
)

type memoryDisplay struct {
	pixels          [240 * 240]color.RGBA
	writes, singles int
	fail            bool
	lightMode       bool
}

func (d *memoryDisplay) SetLightMode(light bool)                { d.lightMode = light }
func (d *memoryDisplay) ThemeColor(c color.RGBA) color.RGBA     { return themeColor(d.lightMode, c) }
func (d *memoryDisplay) Size() (int16, int16)                   { return 240, 240 }
func (d *memoryDisplay) Display() error                         { return nil }
func (d *memoryDisplay) PowerStatus() powerStatus               { return powerStatus{} }
func (d *memoryDisplay) Wait(time.Duration) (inputEvent, error) { return inputEvent{}, nil }
func (d *memoryDisplay) Close() error                           { return nil }
func (d *memoryDisplay) Wake() error                            { return nil }
func (d *memoryDisplay) KeepAwake()                             {}
func (d *memoryDisplay) SetTouchWake(bool)                      {}
func (d *memoryDisplay) SetVibration(bool)                      {}
func (d *memoryDisplay) SetPixel(x, y int16, c color.RGBA) {
	d.singles++
	if x >= 0 && x < 240 && y >= 0 && y < 240 {
		c = gfx.Over(d.pixels[int(y)*240+int(x)], c)
		d.pixels[int(y)*240+int(x)] = pixel.NewColor[pixel.RGB444BE](c.R, c.G, c.B).RGBA()
	}
}
func (d *memoryDisplay) FillScreen(c color.RGBA) { _ = d.FillRectangle(0, 0, 240, 240, c) }
func (d *memoryDisplay) FillRectangle(x, y, w, h int16, c color.RGBA) error {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			d.SetPixel(xx, yy, c)
		}
	}
	return nil
}
func (d *memoryDisplay) DrawBitmap(x, y int16, b pixel.Image[pixel.RGB444BE]) error {
	if d.fail {
		return errors.New("SPI failure")
	}
	d.writes++
	w, h := b.Size()
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < w; xx++ {
			d.pixels[(int(y)+yy)*240+int(x)+xx] = b.Get(xx, yy).RGBA()
		}
	}
	return nil
}

func TestStripRendererMatchesDirectPixelsAndSkipsUnchanged(t *testing.T) {
	now := time.Unix(0, 0)
	for _, p := range []page{pageClock, pageSettings, pageUpdate, pageTrial, pageMessage, pageTimeSettings, pageSetTime, pageSetDate, pageApps, pageAlarms, pageAlarmEdit, pageAlarmRepeat, pageStopwatch, pageCountdown, pageCountdownEdit, pageAlert} {
		d, ref := &memoryDisplay{}, &memoryDisplay{}
		u := watchUI{page: p, holding: true, holdStep: 12, message: "Charge to at least 20 percent first.", timers: newTimerState(), edit: clockEdit{hour: 23, minute: 59, day: 59}}
		draw := func(c canvas) { u.drawFrame(c, now, powerStatus{Percent: 68, State: chargeCharging}) }
		ref.FillScreen(black)
		draw(ref)
		var r frameRenderer
		if err := r.render(d, draw); err != nil {
			t.Fatal(err)
		}
		if d.pixels != ref.pixels {
			t.Fatalf("strip clipping changed page %d", p)
		}
		if d.writes != 240/stripHeight || d.singles != 0 {
			t.Fatalf("got %d transfers and %d individual writes", d.writes, d.singles)
		}
		if len(r.strip.bitmap.RawBuffer()) != 2880 {
			t.Fatal("unexpected RAM footprint")
		}
		if err := r.render(d, draw); err != nil {
			t.Fatal(err)
		}
		if d.writes != 240/stripHeight {
			t.Fatal("unchanged frame sent again")
		}
		r.invalidate()
		if err := r.render(d, draw); err != nil {
			t.Fatal(err)
		}
		if d.writes != 2*240/stripHeight {
			t.Fatal("wake did not repaint")
		}
	}
}

func TestHoldFeedbackUpdatesAboveButton(t *testing.T) {
	d := &memoryDisplay{}
	u := watchUI{page: pageUpdate, holding: true, holdStep: 10}
	draw := func(c canvas) { u.draw(c, time.Unix(0, 0)) }
	var r frameRenderer
	if err := r.render(d, draw); err != nil {
		t.Fatal(err)
	}
	before := d.pixels
	u.holdStep = 11
	d.writes = 0
	if err := r.render(d, draw); err != nil {
		t.Fatal(err)
	}
	if d.writes < 1 || d.writes > (44+stripHeight-1)/stripHeight+1 {
		t.Fatalf("hold feedback redrew %d strips", d.writes)
	}
	for i, pixel := range d.pixels {
		if pixel != before[i] && i/240 >= 174 {
			t.Fatal("countdown feedback is still under the finger on the button")
		}
	}
}

func TestAntialiasedShapesAcrossStripBoundaries(t *testing.T) {
	draw := func(c canvas) {
		gfx.FillBox(c, 0, 0, 240, 240, card)
		gfx.RoundBox(c, 12, 7, 216, 97, 18, accent)
		gfx.Line(c, -10, 15, 250, 213, white)
		gfx.Circle(c, 120, 120, 63, warning)
		gfx.FillCircle(c, 120, 190, 33, gfx.Coverage(white, 128))
	}
	d, ref := &memoryDisplay{}, &memoryDisplay{}
	ref.FillScreen(black)
	draw(ref)
	var r frameRenderer
	if err := r.render(d, draw); err != nil {
		t.Fatal(err)
	}
	if d.pixels != ref.pixels {
		t.Fatal("strip boundary changed antialiased geometry")
	}
}

func TestRendererRetriesFailedTransfers(t *testing.T) {
	d := &memoryDisplay{fail: true}
	var r frameRenderer
	draw := func(c canvas) { c.FillScreen(accent) }
	if err := r.render(d, draw); err == nil {
		t.Fatal("SPI failure hidden")
	}
	d.fail = false
	if err := r.render(d, draw); err != nil {
		t.Fatal(err)
	}
	if d.writes != 240/stripHeight {
		t.Fatal("failed strip marked clean")
	}
}

func TestFrameKeyIgnoresSubminuteTimeAndContactTimestamps(t *testing.T) {
	u := watchUI{page: pageUpdate, holding: true, holdStep: 12}
	now := time.Unix(0, 0)
	a := u.frameKey(now, powerStatus{})
	u.holdSince = now.Add(time.Second)
	if a != u.frameKey(now.Add(time.Second), powerStatus{}) {
		t.Fatal("unchanged view invalidated")
	}
	u.holdStep++
	if a == u.frameKey(now, powerStatus{}) {
		t.Fatal("hold progress not invalidated")
	}
}

func TestPackedRectanglesMatchPixelReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := stripCanvas{bitmap: pixel.NewImage[pixel.RGB444BE](240, stripHeight), y: 16}
	for n := 0; n < 1000; n++ {
		s.FillScreen(accent)
		x, y := int16(rng.Intn(300)-30), int16(rng.Intn(40))
		w, h := int16(rng.Intn(260)-5), int16(rng.Intn(35)-5)
		if n%20 == 0 {
			x, w = 230, 32767
		}
		if n%21 == 0 {
			y, h = 20, 32767
		}
		alpha := []uint8{0, 17, 128, 255}[n%4]
		c := gfx.Coverage(color.RGBA{byte(n), 153, 34, 255}, alpha)
		s.FillRectangle(x, y, w, h, c)
		for yy := int(s.y); yy < int(s.y)+stripHeight; yy++ {
			for xx := 0; xx < 240; xx++ {
				want := pixel.NewColor[pixel.RGB444BE](accent.R, accent.G, accent.B).RGBA()
				if xx >= int(x) && xx < int(x)+int(w) && yy >= int(y) && yy < int(y)+int(h) {
					want = gfx.Over(want, c)
					want = pixel.NewColor[pixel.RGB444BE](want.R, want.G, want.B).RGBA()
				}
				if got := s.bitmap.Get(xx, yy-int(s.y)).RGBA(); got != want {
					t.Fatalf("case %d at %d,%d: got %v want %v", n, xx, yy, got, want)
				}
			}
		}
	}
}

func (d *memoryDisplay) SetFlipped(bool) error { return nil }
