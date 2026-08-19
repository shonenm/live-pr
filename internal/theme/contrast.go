package theme

import (
	"math"
	"strconv"
	"strings"
)

// ContrastingLabelForeground picks the foreground — GitHub's dark ink or
// white — with the higher WCAG contrast ratio against the given background.
func ContrastingLabelForeground(background uint64) string {
	const dark uint64 = 0x0d1117
	bgLuminance := relativeLuminance(background)
	whiteContrast := (1.0 + 0.05) / (bgLuminance + 0.05)
	darkLuminance := relativeLuminance(dark)
	darkContrast := (math.Max(bgLuminance, darkLuminance) + 0.05) / (math.Min(bgLuminance, darkLuminance) + 0.05)
	if darkContrast > whiteContrast {
		return "#0d1117"
	}
	return "#ffffff"
}

// ContrastRatio returns the WCAG contrast ratio between two "#rrggbb"
// colors, from 1 (identical) to 21 (black on white).
func ContrastRatio(a, b string) float64 {
	la, lb := hexLuminance(a), hexLuminance(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// hexLuminance parses "#rrggbb" and returns its relative luminance; malformed
// values count as black, the conservative end of the scale.
func hexLuminance(hex string) float64 {
	hex = strings.TrimPrefix(hex, "#")
	rgb, err := strconv.ParseUint(hex, 16, 32)
	if err != nil || len(hex) != 6 {
		return 0
	}
	return relativeLuminance(rgb)
}

func relativeLuminance(rgb uint64) float64 {
	channel := func(value uint64) float64 {
		v := float64(value) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	r := channel((rgb >> 16) & 0xff)
	g := channel((rgb >> 8) & 0xff)
	b := channel(rgb & 0xff)
	return 0.2126*r + 0.7152*g + 0.0722*b
}
