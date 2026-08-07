//go:build !windows

package embeddedterm

import (
	"strings"
	"testing"
	"time"

	portalis "github.com/Starframe/portalis"
)

func TestEnvironment(t *testing.T) {
	env := Environment("main", "feature/x", "https://example.test/pr/1")
	want := []string{"LIVE_PR_BASE=main", "LIVE_PR_HEAD=feature/x", "LIVE_PR_PR_URL=https://example.test/pr/1"}
	if strings.Join(env, "\n") != strings.Join(want, "\n") {
		t.Fatalf("env = %#v", env)
	}
}

func TestEmptyCommandIsDisabled(t *testing.T) {
	if terminal := New("  ", t.TempDir(), nil); terminal != nil {
		t.Fatal("empty command should not create a terminal")
	}
}

func TestTerminalStartsInRepoWithContextAndRendersOutput(t *testing.T) {
	dir := t.TempDir()
	terminal := New(`printf '%s|%s' "$PWD" "$LIVE_PR_BASE"; sleep 1`, dir, Environment("main", "feature", ""))
	terminal.Resize(80, 10)
	defer terminal.Close()

	cmd := terminal.Init()
	deadline := time.Now().Add(time.Second)
	for cmd != nil && time.Now().Before(deadline) {
		msg := cmd()
		cmd = terminal.Update(msg)
		if view := terminal.View(80, 10); strings.Contains(view, dir[:40]) && strings.Contains(view, "main") {
			return
		}
	}
	t.Fatalf("terminal output missing: %q", terminal.View(80, 10))
}

func TestCloseBeforeInitPreventsLateStart(t *testing.T) {
	terminal := New("sleep 10", t.TempDir(), nil)
	terminal.Close()
	if cmd := terminal.Init(); cmd != nil || terminal.Available() {
		t.Fatal("closed terminal restarted")
	}
	terminal.Close() // idempotent
}

func TestNaturalExitIsClosedAndReaped(t *testing.T) {
	terminal := New("true", t.TempDir(), nil)
	cmd := terminal.Init()
	if cmd == nil {
		t.Fatal("missing PTY listener")
	}
	msg := cmd()
	terminal.Update(msg)
	if terminal.Available() || !terminal.closed {
		t.Fatalf("terminal remained available after %T", msg)
	}
	terminal.Close() // idempotent after exit cleanup
}

func TestHandlesOnlyOwnPTYMessages(t *testing.T) {
	terminal := New("cat", t.TempDir(), nil)
	if !terminal.Handles(portalis.PtyReadyMsg{SessionID: terminal.sessionID}) {
		t.Fatal("own message not handled")
	}
	if terminal.Handles(portalis.PtyReadyMsg{SessionID: "other"}) {
		t.Fatal("foreign message handled")
	}
}

func TestReplacementRejectsOldTerminalMessages(t *testing.T) {
	old := New("cat", t.TempDir(), nil)
	replacement := New("cat", t.TempDir(), nil)
	if old.sessionID == replacement.sessionID {
		t.Fatal("terminal generations share a session ID")
	}
	late := portalis.PtyExitMsg{SessionID: old.sessionID}
	if replacement.Handles(late) {
		t.Fatal("replacement accepted old terminal exit")
	}
}
