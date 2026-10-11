package main

import "image/color"

// Resolve semantic colors before text and curve antialiasing premultiplies
// them. The canvas owns its theme, so independent renders cannot affect it.
func themeColor(light bool, c color.RGBA) color.RGBA {
	if !light {
		return c
	}
	switch c {
	case black:
		return color.RGBA{244, 248, 246, 255}
	case white:
		return color.RGBA{25, 40, 50, 255}
	case muted:
		return color.RGBA{75, 96, 106, 255}
	case card:
		return color.RGBA{222, 233, 229, 255}
	case accent:
		return color.RGBA{0, 110, 78, 255}
	case positive:
		return color.RGBA{184, 225, 204, 255}
	case warning:
		return color.RGBA{154, 75, 0, 255}
	case disabled:
		return color.RGBA{118, 135, 137, 255}
	default:
		return c
	}
}

func (s *stripCanvas) SetLightMode(light bool) { s.lightMode = light }
func (s *stripCanvas) ThemeColor(c color.RGBA) color.RGBA {
	return themeColor(s.lightMode, c)
}
