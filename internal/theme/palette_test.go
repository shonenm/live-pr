package theme

import (
	"strconv"
	"strings"
	"testing"
)

var presets = []struct {
	name string
	p    Palette
}{
	{"primer-dark", PrimerDark()},
	{"primer-light", PrimerLight()},
	{"nord", Nord()},
	{"catppuccin-mocha", CatppuccinMocha()},
}

func TestByNameSelectsPresetsAndFallsBack(t *testing.T) {
	for _, preset := range presets {
		if got := ByName(preset.name); got != preset.p {
			t.Errorf("ByName(%q) = %+v, want the %s preset", preset.name, got, preset.name)
		}
	}
	if got := ByName(" Nord "); got != Nord() {
		t.Errorf("ByName should trim and lowercase, got %+v", got)
	}
	for _, unknown := range []string{"", "solarized", "primer"} {
		if got := ByName(unknown); got != PrimerDark() {
			t.Errorf("ByName(%q) = %+v, want the primer-dark fallback", unknown, got)
		}
	}
}

// TestPrimerDarkKeepsLegacyColors pins the default preset to the exact hex
// values live-pr shipped with, so adding themes never changes existing setups.
func TestPrimerDarkKeepsLegacyColors(t *testing.T) {
	want := Palette{
		Foreground:     "#f0f6fc",
		Muted:          "#9198a1",
		Background:     "#0d1117",
		Selected:       "#212830",
		Border:         "#3d444d",
		BorderEmphasis: "#656c76",
		Accent:         "#4493f8",
		Success:        "#3fb950",
		Attention:      "#d29922",
		Danger:         "#f85149",
		OpenEmphasis:   "#238636",
		DangerEmphasis: "#da3633",
		DoneEmphasis:   "#8957e5",
	}
	if got := PrimerDark(); got != want {
		t.Fatalf("PrimerDark() = %+v, want %+v", got, want)
	}
}

// TestPalettesKeepReadableContrast checks every preset against its own
// background with the WCAG relative-luminance math: body text meets AA
// (4.5:1) and muted text stays at least readable (3:1).
func TestPalettesKeepReadableContrast(t *testing.T) {
	for _, preset := range presets {
		if got := ContrastRatio(preset.p.Foreground, preset.p.Background); got < 4.5 {
			t.Errorf("%s: foreground/background contrast = %.2f, want >= 4.5", preset.name, got)
		}
		if got := ContrastRatio(preset.p.Muted, preset.p.Background); got < 3.0 {
			t.Errorf("%s: muted/background contrast = %.2f, want >= 3.0", preset.name, got)
		}
	}
}

// TestPalettesKeepSemanticHues verifies the state colors keep their meaning
// in every theme: open/success stays green-led, closed/danger red-led, and
// merged/done purple-led (red and blue over green).
func TestPalettesKeepSemanticHues(t *testing.T) {
	channels := func(t *testing.T, hex string) (r, g, b uint64) {
		t.Helper()
		rgb, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
		if err != nil {
			t.Fatalf("parse %q: %v", hex, err)
		}
		return rgb >> 16 & 0xff, rgb >> 8 & 0xff, rgb & 0xff
	}
	for _, preset := range presets {
		for _, green := range []string{preset.p.Success, preset.p.OpenEmphasis} {
			if r, g, b := channels(t, green); g <= r && g <= b {
				t.Errorf("%s: open color %s is not green-led", preset.name, green)
			}
		}
		for _, red := range []string{preset.p.Danger, preset.p.DangerEmphasis} {
			if r, g, _ := channels(t, red); r <= g {
				t.Errorf("%s: closed color %s is not red-led", preset.name, red)
			}
		}
		if r, g, b := channels(t, preset.p.DoneEmphasis); r <= g || b <= g {
			t.Errorf("%s: merged color %s is not purple-led", preset.name, preset.p.DoneEmphasis)
		}
	}
}
