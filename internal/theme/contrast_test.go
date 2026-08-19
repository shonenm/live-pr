package theme

import "testing"

func TestLabelForegroundChoosesHigherContrast(t *testing.T) {
	if got := ContrastingLabelForeground(0x00aa00); got != "#0d1117" {
		t.Fatalf("green foreground = %s", got)
	}
	if got := ContrastingLabelForeground(0x000080); got != "#ffffff" {
		t.Fatalf("navy foreground = %s", got)
	}
}
