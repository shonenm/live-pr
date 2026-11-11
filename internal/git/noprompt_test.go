//go:build !windows

package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGit puts a git on PATH that records, for fetch and push, whether
// terminal prompts are allowed, whether /dev/tty can be opened, and whether
// the process leads its own group, as it does at the head of a new session.
func fakeGit(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	script := `#!/bin/sh
case "$1" in
fetch|push)
	if (: </dev/tty) 2>/dev/null; then tty=yes; else tty=no; fi
	if [ "$(ps -o pgid= -p $$ | tr -d ' ')" = "$$" ]; then leader=yes; else leader=no; fi
	echo "prompt=$GIT_TERMINAL_PROMPT tty=$tty leader=$leader" >>"$FAKE_GIT_LOG"
	;;
esac
echo 0123456789abcdef0123456789abcdef01234567
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GIT_LOG", log)
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	return log
}

func TestBackgroundNetworkCommandsCannotPrompt(t *testing.T) {
	for name, call := range map[string]func() error{
		"FetchPull":        func() error { _, _, err := FetchPull(7, "main"); return err },
		"CheckoutPullHead": func() error { return CheckoutPullHead("feature") },
		"PushNoPrompt":     func() error { return PushNoPrompt("feature") },
	} {
		t.Run(name, func(t *testing.T) {
			log := fakeGit(t)
			if err := call(); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if want := "prompt=0 tty=no leader=yes\n"; string(got) != want {
				t.Fatalf("network git ran with %q, want %q", got, want)
			}
		})
	}
}

func TestPushKeepsTerminalForCLI(t *testing.T) {
	log := fakeGit(t)
	if err := Push("feature"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "prompt=1 ") || !strings.HasSuffix(string(got), "leader=no\n") {
		t.Fatalf("interactive push ran with %q, want the inherited terminal", got)
	}
}
