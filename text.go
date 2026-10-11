package main

import (
	"github.com/Distortions81/goPine/internal/gfx"
	"github.com/Distortions81/goPine/internal/uifont"
	"image/color"
	"strings"

	"tinygo.org/x/drivers"
	"tinygo.org/x/tinyfont"
)

// No-rotation text path preserving the canvas's strip clipping interface.
// TinyFont's rotation wrapper hides it, causing every AA glyph pixel to be
// rasterized again for each of the fifteen strips, delaying touch processing.
func writeLine(d drivers.Displayer, font tinyfont.Fonter, x, y int16, text string, c color.RGBA) {
	if !textVisible(d, font, y, text) {
		return
	}
	c = gfx.ResolveColor(d, c)
	if fast, ok := font.(*uifont.Font); ok {
		fast.DrawText(d, x, y, text, c)
		return
	}
	x0 := x
	for _, ch := range text {
		if ch == '\n' || ch == '\r' {
			x, y = x0, y+int16(font.GetYAdvance())
			continue
		}
		g := font.GetGlyph(ch)
		g.Draw(d, x, y, c)
		x += int16(g.Info().XAdvance)
	}
}

// Whole-line rejection avoids decoding/measuring every glyph for every strip.
// Unknown fonts and multiline text retain the general glyph clipping path.
func textVisible(d drivers.Displayer, font tinyfont.Fonter, y int16, text string) bool {
	clip, clipped := d.(interface{ ClipBounds() (int, int, int, int) })
	bounds, known := font.(interface{ VerticalBounds() (int16, int16) })
	if !clipped || !known || strings.ContainsAny(text, "\r\n") {
		return true
	}
	_, top, _, bottom := clip.ClipBounds()
	lo, hi := bounds.VerticalBounds()
	return int(y)+int(lo) < bottom && int(y)+int(hi) > top
}

func lineWidth(font tinyfont.Fonter, text string) (uint32, uint32) {
	if fast, ok := font.(*uifont.Font); ok {
		return fast.LineWidth(text)
	}
	return tinyfont.LineWidth(font, text)
}
