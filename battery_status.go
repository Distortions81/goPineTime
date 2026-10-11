package main

import "github.com/Distortions81/goPine/internal/gfx"

// Five columns per 7-pixel glyph, low bit at the top: %, then 0 through 9.
// An immutable 55-byte string keeps this small status font in flash.
const batteryGlyphs = "\x63\x13\x08\x64\x63" +
	"\x3e\x51\x49\x45\x3e\x00\x42\x7f\x40\x00" +
	"\x42\x61\x51\x49\x46\x21\x41\x45\x4b\x31" +
	"\x18\x14\x12\x7f\x10\x27\x45\x45\x45\x39" +
	"\x3c\x4a\x49\x49\x30\x01\x71\x09\x05\x03" +
	"\x36\x49\x49\x49\x36\x06\x49\x49\x29\x1e"

// Reserve only the top 13 pixels. Existing page titles start below this band,
// so even long headings, pairing and update prompts retain their full width.
func drawStatusBattery(d canvas, p powerStatus) {
	_, top, _, bottom := gfx.Bounds(d)
	if top >= 13 || bottom <= 3 {
		return
	}
	p.Percent = min(p.Percent, 100)
	c := powerColor(p)
	resolved := gfx.ResolveColor(d, c)
	percent, start := percentDigits(p.Percent)
	x := int16(214 - ((4-start)*6 - 1))
	for _, ch := range percent[start:] {
		index := 0
		if ch != '%' {
			index = int(ch-'0'+1) * 5
		}
		for col := 0; col < 5; col++ {
			bits := batteryGlyphs[index+col]
			for row := 0; row < 7; row++ {
				if bits&(1<<uint(row)) != 0 {
					d.SetPixel(x+int16(col), 4+int16(row), resolved)
				}
			}
		}
		x += 6
	}
	gfx.Box(d, 218, 3, 14, 9, 1, c)
	gfx.FillBox(d, 232, 6, 2, 3, c)
	if p.State == chargeCharging {
		// A bolt replaces the fill during active charging. Plugged-in without
		// charging retains the ordinary gauge and uses the external-power color.
		const bolt = "\x10\x38\x1c\x0e\x04"
		for col := 0; col < len(bolt); col++ {
			for row := 0; row < 7; row++ {
				if bolt[col]&(1<<uint(row)) != 0 {
					d.SetPixel(222+int16(col), 4+int16(row), resolved)
				}
			}
		}
	} else if fill := int16(p.Percent) * 10 / 100; fill > 0 {
		gfx.FillBox(d, 220, 5, fill, 5, c)
	}
}
