package review

import "testing"

func TestExpandPlaceholders(t *testing.T) {
	got := expand(`nvim -c "CodeDiff {sha}~1 {sha}"`, "abc123", "main", "feat/x")
	want := `nvim -c "CodeDiff abc123~1 abc123"`
	if got != want {
		t.Errorf("expand = %q, want %q", got, want)
	}

	got = expand("git range-diff {base}...{head}", "", "origin/main", "feat/x")
	want = "git range-diff origin/main...feat/x"
	if got != want {
		t.Errorf("expand = %q, want %q", got, want)
	}
}

func TestCommandRunsThroughShell(t *testing.T) {
	c := Command("echo {sha}", "deadbeef", "main", "head")
	if c.Args[0] != "sh" || c.Args[1] != "-c" {
		t.Fatalf("expected sh -c, got %v", c.Args)
	}
	if c.Args[2] != "echo deadbeef" {
		t.Errorf("command line = %q", c.Args[2])
	}
}
