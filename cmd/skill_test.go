package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestMaterializeSkillMatchesBundledCommands(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path, err := materializeSkill()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: live-pr", "live-pr comment add", "--author agent", "live-pr summary set", "live-pr pr preview"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("bundled skill missing %q", want)
		}
	}
}
