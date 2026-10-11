package main

const (
	brightnessHigh uint8 = iota // Zero preserves the default for existing settings.
	brightnessLow
	brightnessMedium
)

func validBrightness(level uint8) uint8 {
	if level > brightnessMedium {
		return brightnessHigh
	}
	return level
}

func brightnessLabel(level uint8) string {
	switch validBrightness(level) {
	case brightnessLow:
		return "Backlight: Low"
	case brightnessMedium:
		return "Backlight: Medium"
	default:
		return "Backlight: High"
	}
}

// PineTime's three active-low channels are cumulative, matching InfiniTime's
// classic levels. These fixed levels need no PWM, timer, or PPI resources.
// Bits 0, 1, 2 select LOW, MID, HIGH; sleep always disables every channel.
func backlightChannels(level uint8, awake bool) uint8 {
	if !awake {
		return 0
	}
	switch validBrightness(level) {
	case brightnessLow:
		return 1
	case brightnessMedium:
		return 3
	default:
		return 7
	}
}
