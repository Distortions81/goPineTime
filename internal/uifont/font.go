// Package uifont contains pre-rasterized 4-bit antialiased fonts. Font bitmaps
// are immutable strings so TinyGo can retain them in flash, not the heap.
package uifont

import (
	"image/color"

	"github.com/Distortions81/goPine/internal/gfx"
	"tinygo.org/x/drivers"
	"tinygo.org/x/tinyfont"
)

type Font struct {
	first, last           rune
	index, data           string
	lineHeight            uint8
	pixelTop, pixelBottom int16
	glyph                 glyph
}

type glyph struct {
	info       tinyfont.GlyphInfo
	pixels     string
	rows       string
	compressed bool
}

func (f *Font) GetYAdvance() uint8             { return f.lineHeight }
func (f *Font) VerticalBounds() (int16, int16) { return f.pixelTop, f.pixelBottom }

// Resolve the contiguous repertoire and its unsupported-rune fallback.
func (f *Font) glyphIndex(r rune) (rune, int) {
	index := int(r - f.first)
	if r < f.first || r > f.last {
		r = f.first
		index = 0
	}

	return r, index
}

// LineWidth preserves TinyFont's inner/outbox metrics without decoding bitmap
// packets or changing the reusable glyph just to measure text.
func (f *Font) LineWidth(text string) (inner, outer uint32) {
	if len(text) == 0 {
		return
	}
	for _, r := range text {
		_, index := f.glyphIndex(r)
		i := index * 2
		offset := int(f.index[i])<<8 | int(f.index[i+1])
		outer += uint32(f.data[offset+2])
	}
	metric := func(r rune) (width, advance uint8, offset int8) {
		_, index := f.glyphIndex(r)
		i := index * 2
		at := int(f.index[i])<<8 | int(f.index[i+1])
		return f.data[at] & 127, f.data[at+2], int8(f.data[at+3])
	}
	_, _, first := metric(rune(text[0]))
	width, advance, last := metric(rune(text[len(text)-1]))
	inner = outer - uint32(first) - uint32(advance) + uint32(last) + uint32(width)
	return
}

// Like TinyFont's const fonts, this returns one reusable glyph for the single
// display goroutine. It is not intended for concurrent rendering.
func (f *Font) GetGlyph(r rune) tinyfont.Glypher {
	r, index := f.glyphIndex(r)
	i := index * 2
	offset := int(f.index[i])<<8 | int(f.index[i+1])
	d := f.data[offset:]
	f.glyph.info = tinyfont.GlyphInfo{Rune: r, Width: d[0] & 127, Height: d[1], XAdvance: d[2], XOffset: int8(d[3]), YOffset: int8(d[4])}
	n := (int(d[0]&127)*int(d[1]) + 1) / 2
	if d[0]&128 != 0 {
		end := len(f.data)
		if i+2 < len(f.index) {
			end = int(f.index[i+2])<<8 | int(f.index[i+3])
		}
		n = end - offset - 5
	}
	f.glyph.pixels = d[5 : 5+n]
	f.glyph.compressed = d[0]&128 != 0
	f.glyph.rows = ""
	if f.glyph.compressed {
		size := (int(d[1]) + 1) * 2
		f.glyph.rows = f.glyph.pixels[:size]
		f.glyph.pixels = f.glyph.pixels[size:]
	}
	return &f.glyph
}

func (g *glyph) Info() tinyfont.GlyphInfo { return g.info }

func (g *glyph) Draw(d drivers.Displayer, x, y int16, c color.RGBA) {
	x += int16(g.info.XOffset)
	y += int16(g.info.YOffset)
	x0, y0, x1, y1 := 0, 0, int(g.info.Width), int(g.info.Height)
	if clip, ok := d.(interface{ ClipBounds() (int, int, int, int) }); ok {
		l, t, r, b := clip.ClipBounds()
		x0, y0 = max(x0, l-int(x)), max(y0, t-int(y))
		x1, y1 = min(x1, r-int(x)), min(y1, b-int(y))
	}
	if x0 >= x1 || y0 >= y1 || c.A == 0 {
		return
	}
	colors := coverageColors(c)
	g.drawPixels(d, x, y, x0, y0, x1, y1, &colors)
}

func coverageColors(c color.RGBA) (colors [16]color.RGBA) {
	for i := 1; i < len(colors); i++ {
		colors[i] = gfx.Coverage(c, uint8(i)*17)
	}
	return
}

// DrawText shares one coverage palette across a line instead of rebuilding
// sixteen premultiplied colors for every glyph. No palette is retained in RAM.
func (f *Font) DrawText(d drivers.Displayer, x, y int16, text string, c color.RGBA) {
	if c.A == 0 || text == "" {
		return
	}
	colors := coverageColors(c)
	l, t, r, b := 0, 0, 0, 0
	clipped := false
	if clip, ok := d.(interface{ ClipBounds() (int, int, int, int) }); ok {
		l, t, r, b = clip.ClipBounds()
		clipped = true
	}
	start := x
	for _, ch := range text {
		if ch == '\n' || ch == '\r' {
			x, y = start, y+int16(f.lineHeight)
			continue
		}
		f.GetGlyph(ch)
		g := &f.glyph
		gx, gy := x+int16(g.info.XOffset), y+int16(g.info.YOffset)
		x0, y0, x1, y1 := 0, 0, int(g.info.Width), int(g.info.Height)
		if clipped {
			x0, y0 = max(0, l-int(gx)), max(0, t-int(gy))
			x1, y1 = min(x1, r-int(gx)), min(y1, b-int(gy))
		}
		if x0 < x1 && y0 < y1 {
			g.drawPixels(d, gx, gy, x0, y0, x1, y1, &colors)
		}
		x += int16(g.info.XAdvance)
	}
}

func (g *glyph) drawPixels(d drivers.Displayer, x, y int16, x0, y0, x1, y1 int, colors *[16]color.RGBA) {
	if g.compressed {
		g.drawPackets(d, x, y, x0, y0, x1, y1, colors)
		return
	}
	for yy := y0; yy < y1; yy++ {
		for xx := x0; xx < x1; xx++ {
			i := yy*int(g.info.Width) + xx
			a := g.pixels[i/2]
			if i%2 == 0 {
				a >>= 4
			} else {
				a &= 15
			}
			if a != 0 {
				d.SetPixel(x+int16(xx), y+int16(yy), colors[a])
			}
		}
	}
}

// Packet headers: 0xxxxxxx = 1..128 packed literal pixels, 10xxxxxx =
// 1..64 transparent pixels, 11xxxxxx = 1..64 opaque pixels. Flash-resident
// row offsets let strips jump to visible rows without replaying prior packets.
func (g *glyph) drawPackets(d drivers.Displayer, x, y int16, x0, y0, x1, y1 int, colors *[16]color.RGBA) {
	fill, batched := d.(interface {
		FillRectangle(int16, int16, int16, int16, color.RGBA) error
	})
	for yy := y0; yy < y1; yy++ {
		pos := int(g.rows[yy*2]) | int(g.rows[yy*2+1])<<8
		xx := 0
		for xx < x1 {
			header := g.pixels[pos]
			pos++
			count := int(header&63) + 1
			literals := ""
			if header < 128 {
				count = int(header) + 1
				n := (count + 1) / 2
				literals = g.pixels[pos : pos+n]
				pos += n
			}
			end := xx + count
			left, right := max(xx, x0), min(end, x1)
			if left < right && (header < 128 || header >= 192) {
				if header >= 192 && batched {
					_ = fill.FillRectangle(x+int16(left), y+int16(yy), int16(right-left), 1, colors[15])
				} else {
					for px := left; px < right; px++ {
						a := byte(15)
						if header < 128 {
							i := px - xx
							a = literals[i/2]
							if i%2 == 0 {
								a >>= 4
							} else {
								a &= 15
							}
						}
						if a != 0 {
							d.SetPixel(x+int16(px), y+int16(yy), colors[a])
						}
					}
				}
			}
			xx = end
		}
	}
}
