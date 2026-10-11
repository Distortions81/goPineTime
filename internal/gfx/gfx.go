// Package gfx provides small, allocation-free drawing helpers for opaque
// canvases that composite premultiplied color.RGBA pixels (source-over).
package gfx

import "image/color"

type Canvas interface {
	Size() (int16, int16)
	SetPixel(x, y int16, c color.RGBA)
	FillRectangle(x, y, width, height int16, c color.RGBA) error
}

// ResolveColor lets a canvas supply a semantic palette. Resolve before
// coverage is applied, so antialiased edges use the selected foreground.
func ResolveColor(d any, c color.RGBA) color.RGBA {
	if themed, ok := d.(interface{ ThemeColor(color.RGBA) color.RGBA }); ok {
		return themed.ThemeColor(c)
	}
	return c
}

// Over composites a premultiplied source onto an opaque destination.
func Over(dst, src color.RGBA) color.RGBA {
	if src.A == 255 {
		return src
	}
	if src.A == 0 {
		return dst
	}
	a := uint32(255 - src.A)
	return color.RGBA{
		uint8(min(255, uint32(src.R)+(uint32(dst.R)*a+127)/255)),
		uint8(min(255, uint32(src.G)+(uint32(dst.G)*a+127)/255)),
		uint8(min(255, uint32(src.B)+(uint32(dst.B)*a+127)/255)), 255,
	}
}

// Coverage scales an already premultiplied color by coverage in [0,255].
func Coverage(c color.RGBA, alpha uint8) color.RGBA {
	scale := func(v uint8) uint8 { return uint8((uint32(v)*uint32(alpha) + 127) / 255) }
	return color.RGBA{scale(c.R), scale(c.G), scale(c.B), scale(c.A)}
}

// Bounds also respects a strip canvas's current viewport. This avoids
// rasterizing the same off-screen curves once for every strip.
func Bounds(d Canvas) (int, int, int, int) {
	w, h := d.Size()
	if clipped, ok := d.(interface{ ClipBounds() (int, int, int, int) }); ok {
		return clipped.ClipBounds()
	}
	return 0, 0, int(w), int(h)
}

func FillBox(d Canvas, x, y, w, h int16, c color.RGBA) {
	fillBox(d, int(x), int(y), int(w), int(h), ResolveColor(d, c))
}

// Keep intermediate coordinates wider than the public int16 surface API.
func fillBox(d Canvas, x, y, w, h int, c color.RGBA) {
	if w <= 0 || h <= 0 {
		return
	}
	l, t, r, b := Bounds(d)
	x0, y0 := max(x, l), max(y, t)
	x1, y1 := min(x+w, r), min(y+h, b)
	if x1 > x0 && y1 > y0 {
		_ = d.FillRectangle(int16(x0), int16(y0), int16(x1-x0), int16(y1-y0), c)
	}
}

func Box(d Canvas, x, y, w, h, thickness int16, c color.RGBA) {
	c = ResolveColor(d, c)
	if w <= 0 || h <= 0 || thickness <= 0 {
		return
	}
	ax, ay, aw, ah := int(x), int(y), int(w), int(h)
	t := min(int(thickness), min(aw, ah))
	fillBox(d, ax, ay, aw, t, c)
	if ah > t {
		fillBox(d, ax, ay+max(t, ah-t), aw, min(t, ah-t), c)
	}
	if ah > 2*t {
		fillBox(d, ax, ay+t, t, ah-2*t, c)
		if aw > t {
			fillBox(d, ax+max(t, aw-t), ay+t, min(t, aw-t), ah-2*t, c)
		}
	}
}

// Line uses fixed-point coverage along the minor axis. Integer horizontal
// and vertical lines stay crisp; reversed endpoints produce identical pixels.
func Line(d Canvas, x0, y0, x1, y1 int16, c color.RGBA) {
	c = ResolveColor(d, c)
	ax, ay, bx, by := int(x0), int(y0), int(x1), int(y1)
	steep := abs(by-ay) > abs(bx-ax)
	if steep {
		ax, ay, bx, by = ay, ax, by, bx
	}
	if ax > bx {
		ax, bx, ay, by = bx, ax, by, ay
	}
	l, t, r, b := Bounds(d)
	lo, hi := l, r
	if steep {
		lo, hi = t, b
	}
	for x := max(ax, lo); x <= min(bx, hi-1); x++ {
		fixed := int64(ay) * 256
		if bx != ax {
			fixed += int64(by-ay) * 256 * int64(x-ax) / int64(bx-ax)
		}
		y := int(fixed >> 8)
		fraction := uint8(fixed - int64(y)*256)
		plot := func(yy int, alpha uint8) {
			px, py := x, yy
			if steep {
				px, py = yy, x
			}
			if alpha > 0 && px >= l && px < r && py >= t && py < b {
				d.SetPixel(int16(px), int16(py), Coverage(c, alpha))
			}
		}
		plot(y, 255-fraction)
		plot(y+1, fraction)
	}
}

// Circle draws a one-pixel antialiased outline; FillCircle draws a solid disc.
// Integer center coordinates refer to pixel centers, matching Line.
func Circle(d Canvas, x, y, radius int16, c color.RGBA)     { circle(d, x, y, radius, false, c) }
func FillCircle(d Canvas, x, y, radius int16, c color.RGBA) { circle(d, x, y, radius, true, c) }
func circle(d Canvas, x, y, radius int16, filled bool, c color.RGBA) {
	c = ResolveColor(d, c)
	if radius < 0 {
		return
	}
	l, t, r, b := Bounds(d)
	cx, cy := int(x)*8+4, int(y)*8+4
	outer, inner := int64(radius)*8+4, int64(radius)*8-4
	for yy := max(int(y)-int(radius), t); yy <= min(int(y)+int(radius), b-1); yy++ {
		for xx := max(int(x)-int(radius), l); xx <= min(int(x)+int(radius), r-1); xx++ {
			count := 0
			for dy := 1; dy < 8; dy += 2 {
				for dx := 1; dx < 8; dx += 2 {
					a, b := int64(xx*8+dx-cx), int64(yy*8+dy-cy)
					dist := a*a + b*b
					if dist <= outer*outer && (filled || inner <= 0 || dist >= inner*inner) {
						count++
					}
				}
			}
			if count > 0 {
				d.SetPixel(int16(xx), int16(yy), Coverage(c, uint8((count*255+8)/16)))
			}
		}
	}
}

const roundCorner6 = "\x00\x00\x00\x07\x0d\x10\x00\x01\x0d\x10\x10\x10\x00\x0d\x10\x10\x10\x10\x07\x10\x10\x10\x10\x10\x0d\x10\x10\x10\x10\x10\x10\x10\x10\x10\x10\x10"

// RoundBox fills a rounded rectangle. Only corner pixels use 4x4 coverage;
// the straight interior goes through the canvas's batched rectangle path.
func RoundBox(d Canvas, x, y, w, h, radius int16, c color.RGBA) {
	c = ResolveColor(d, c)
	if w <= 0 || h <= 0 {
		return
	}
	radius = max(0, min(radius, min(w, h)/2))
	if radius == 0 {
		FillBox(d, x, y, w, h, c)
		return
	}
	ax, ay, aw, ah, ar := int(x), int(y), int(w), int(h), int(radius)
	l, t, r, b := Bounds(d)
	if ax >= r || ay >= b || ax+aw <= l || ay+ah <= t {
		return
	}
	fillBox(d, ax+ar, ay, aw-2*ar, ah, c)
	fillBox(d, ax, ay+ar, ar, ah-2*ar, c)
	fillBox(d, ax+aw-ar, ay+ar, ar, ah-2*ar, c)
	for _, right := range []bool{false, true} {
		for _, bottom := range []bool{false, true} {
			sx, sy := ax, ay
			cx, cy := (ax+ar)*8, (ay+ar)*8
			if right {
				sx = ax + aw - ar
				cx = sx * 8
			}
			if bottom {
				sy = ay + ah - ar
				cy = sy * 8
			}
			for yy := max(sy, t); yy < min(sy+int(radius), b); yy++ {
				for xx := max(sx, l); xx < min(sx+int(radius), r); xx++ {
					count := 0
					if radius == 6 {
						// All current UI cards use this radius. Retain the exact
						// 4x4 sample counts in flash instead of resampling per frame.
						px, py := xx-sx, yy-sy
						if right {
							px = 5 - px
						}
						if bottom {
							py = 5 - py
						}
						count = int(roundCorner6[py*6+px])
					} else {
						for dy := 1; dy < 8; dy += 2 {
							for dx := 1; dx < 8; dx += 2 {
								a, b := int64(xx*8+dx-cx), int64(yy*8+dy-cy)
								if a*a+b*b <= int64(radius)*int64(radius)*64 {
									count++
								}
							}
						}
					}
					if count > 0 {
						d.SetPixel(int16(xx), int16(yy), Coverage(c, uint8((count*255+8)/16)))
					}
				}
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
