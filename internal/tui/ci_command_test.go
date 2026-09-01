package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadWoodpeckerCIUsesPipelineForHeadSHA(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX shell script")
	}
	bin := t.TempDir()
	script := `#!/bin/sh
if [ "$1 $2" = "pipeline ls" ]; then
  printf '41\told-sha\tsuccess\n42\thead-sha\trunning\n'
elif [ "$1 $2" = "pipeline ps" ] && [ "$4" = "42" ]; then
  printf 'build\ttest\trunning\t100\t0\n'
else
  exit 2
fi
`
	if err := os.WriteFile(filepath.Join(bin, "woodpecker-cli"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := loadWoodpeckerCI(ctx, t.TempDir(), os.Environ(), nil, "acme/widget", "head-sha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Woodpecker #42 · running") || !strings.Contains(got, "◐ test · running") {
		t.Fatalf("Woodpecker output = %q", got)
	}
}

func TestFindWoodpeckerPipelineByHeadSHA(t *testing.T) {
	output := "41\told-sha\tsuccess\n42\thead-sha\trunning\n"
	number, state := findWoodpeckerPipeline(output, "head-sha")
	if number != "42" || state != "running" {
		t.Fatalf("pipeline = %q, %q", number, state)
	}
}

func TestFormatWoodpeckerCI(t *testing.T) {
	steps := "build\tlint\tsuccess\t100\t105\nbuild\ttest\tfailure\t100\t110\ndeploy\tship\trunning\t110\t0\n"
	got := formatWoodpeckerCI("42", "failure", steps)
	for _, want := range []string{
		"Woodpecker #42 · failure",
		"build\n  └─ ✓ lint · success · 5s",
		"  └─ ✗ test · failure · 10s",
		"deploy\n  └─ ◐ ship · running",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q: %q", want, got)
		}
	}
}

func TestWoodpeckerRepositoryFallsBackToPRURL(t *testing.T) {
	if got := woodpeckerRepository("", "https://github.com/acme/widget/pull/7"); got != "acme/widget" {
		t.Fatalf("repository = %q", got)
	}
	if got := woodpeckerRepository("configured/repo", "https://github.com/acme/widget/pull/7"); got != "configured/repo" {
		t.Fatalf("configured repository = %q", got)
	}
}

func TestReadCITokenDoesNotExposeFailedCommandOutput(t *testing.T) {
	t.Setenv("GO_WANT_CI_TOKEN_HELPER", "fail")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := readCIToken(ctx, t.TempDir(), []string{os.Args[0], "-test.run=TestCITokenHelper", "--"})
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("token command error = %v", err)
	}
}

func TestReadCITokenRequiresOneLine(t *testing.T) {
	t.Setenv("GO_WANT_CI_TOKEN_HELPER", "multiline")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := readCIToken(ctx, t.TempDir(), []string{os.Args[0], "-test.run=TestCITokenHelper", "--"})
	if err == nil || !strings.Contains(err.Error(), "one non-empty line") {
		t.Fatalf("token command error = %v", err)
	}
}

func TestCITokenHelper(t *testing.T) {
	switch os.Getenv("GO_WANT_CI_TOKEN_HELPER") {
	case "fail":
		_, _ = fmt.Fprintln(os.Stdout, "super-secret")
		_, _ = fmt.Fprintln(os.Stderr, "super-secret")
		os.Exit(1)
	case "multiline":
		_, _ = fmt.Fprintln(os.Stdout, "first\nsecond")
		os.Exit(0)
	}
}
