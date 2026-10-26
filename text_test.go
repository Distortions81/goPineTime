package main

import (
	"image/color"
	"testing"

	"github.com/Distortions81/goPine/internal/uifont"
	"tinygo.org/x/tinyfont"
)

type countingStrip struct {
	stripCanvas
	calls int
}

func (s *countingStrip) SetPixel(x, y int16, c color.RGBA) {
	s.calls++
	s.stripCanvas.SetPixel(x, y, c)
}

func TestDirectTextPreservesTinyFontPixels(t *testing.T) {
	for _, font := range []tinyfont.Fonter{&uifont.Regular18, &uifont.Clock, &tinyfont.Picopixel} {
		for _, at := range [][2]int16{{18, 31}, {-5, 2}, {100, 234}} {
			ref, got := &memoryDisplay{}, &memoryDisplay{}
			ref.FillScreen(black)
			got.FillScreen(black)
			tinyfont.WriteLine(ref, font, at[0], at[1], "12:34\n5", white)
			writeLine(got, font, at[0], at[1], "12:34\n5", white)
			if ref.pixels != got.pixels {
				t.Fatal("direct writer changed text pixels", at)
			}
		}
	}
}

func TestAATextSkipsOffStripGlyphPixels(t *testing.T) {
	s := &countingStrip{}
	// This text is entirely below the top strip; no bitmap allocation is
	// necessary because clipping should prevent EVERY SetPixel call.
	writeLine(s, &uifont.Regular18, 24, 202, "HOLD 3 SECONDS", white)
	if s.calls != 0 {
		t.Fatal("off-strip text was rasterized", s.calls)
	}
}

type inputServicingDisplay struct {
	memoryDisplay
	samples int
}

func (d *inputServicingDisplay) serviceInput() { d.samples++ }
func TestRendererServicesInputEvenForUnchangedStrips(t *testing.T) {
	d := &inputServicingDisplay{}
	var r frameRenderer
	for i := 0; i < 2; i++ {
		if err := r.render(d, func(c canvas) { c.FillScreen(black) }); err != nil {
			t.Fatal(err)
		}
	}
	if d.samples != 2*(240/stripHeight+1) || d.writes != 240/stripHeight {
		t.Fatal("input starved on unchanged frame", d.samples, d.writes)
	}
}

type countedFont struct {
	*uifont.Font
	lookups int
}

func (f *countedFont) GetGlyph(r rune) tinyfont.Glypher {
	f.lookups++
	return f.Font.GetGlyph(r)
}

func TestOffStripLineSkipsLayout(t *testing.T) {
	font := countedFont{Font: &uifont.Regular18}
	s := &countingStrip{}
	centered(s, &font, 202, "HOLD 3 SECONDS", white)
	if font.lookups != 0 {
		t.Fatalf("off-strip label performed %d glyph lookups", font.lookups)
	}
}

func TestMultilineTextRemainsVisibleAcrossStrips(t *testing.T) {
	draw := func(c canvas) { writeLine(c, &uifont.Clock, -5, -15, "12:34\n5\r6", white) }
	ref, got := &memoryDisplay{}, &memoryDisplay{}
	ref.FillScreen(black)
	draw(ref)
	var r frameRenderer
	if err := r.render(got, draw); err != nil {
		t.Fatal(err)
	}
	if got.pixels != ref.pixels {
		t.Fatal("whole-line clipping hid visible multiline text")
	}
}
