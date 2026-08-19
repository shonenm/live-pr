package theme

import "math"

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
