//go:build !windows

package embeddedterm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	portalis "github.com/shonenm/portalis"
)

func TestWatchdogHelper(t *testing.T) {
	if os.Getenv("LIVE_PR_WATCHDOG_TEST_HELPER") != "1" {
		return
	}
	pidFile := os.Getenv("LIVE_PR_WATCHDOG_PID_FILE")
	terminal := New(fmt.Sprintf("echo $$ > %q; exec sleep 30", pidFile), t.TempDir(), nil)
	if cmd := terminal.Init(); cmd == nil {
		os.Exit(2)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestWatchdogKillsReviewerWhenParentIsSIGKILLed(t *testing.T) {
	pidFile := t.TempDir() + "/reviewer.pid"
	parent := exec.Command(os.Args[0], "-test.run=^TestWatchdogHelper$")
	parent.Env = append(os.Environ(), "LIVE_PR_WATCHDOG_TEST_HELPER=1", "LIVE_PR_WATCHDOG_PID_FILE="+pidFile)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if parent.Process != nil {
			_ = parent.Process.Kill()
		}
	}()
	var reviewerPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			reviewerPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if reviewerPID > 0 {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if reviewerPID == 0 {
		t.Fatal("reviewer did not start")
	}
	if err := parent.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_, _ = parent.Process.Wait()
	deadline = time.Now().Add(3 * time.Second)
	for processAlive(reviewerPID) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if processAlive(reviewerPID) {
		t.Fatalf("reviewer %d survived parent SIGKILL", reviewerPID)
	}
}

func TestEnvironment(t *testing.T) {
	env := Environment("base-sha...head-ref", "main", "feature/x", "refs/live-pr/pulls/1/head", "https://example.test/pr/1", "abc123", "/state/reviewed/1.json")
	want := []string{"LIVE_PR_REVIEW=1", "LIVE_PR_RANGE=base-sha...head-ref", "LIVE_PR_BASE=main", "LIVE_PR_HEAD=feature/x", "LIVE_PR_HEAD_REV=refs/live-pr/pulls/1/head", "LIVE_PR_PR_URL=https://example.test/pr/1", "LIVE_PR_SHA=abc123", "LIVE_PR_REVIEWED_FILE=/state/reviewed/1.json"}
	if strings.Join(env, "\n") != strings.Join(want, "\n") {
		t.Fatalf("env = %#v", env)
	}
}

func TestPIDFileLivesInPrivateDirectory(t *testing.T) {
	terminal := New("true", t.TempDir(), nil)
	if terminal == nil || terminal.err != nil {
		t.Fatalf("New failed: %v", terminal.Err())
	}
	dir := filepath.Dir(terminal.pidFile)
	if filepath.Clean(dir) == filepath.Clean(os.TempDir()) {
		t.Fatalf("pid file %s sits directly in the shared temp dir", terminal.pidFile)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat pid dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("pid dir mode = %o, want 700", perm)
	}
	terminal.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("pid dir not removed on Close: %v", err)
	}
}

func TestEmptyCommandIsDisabled(t *testing.T) {
	if terminal := New("  ", t.TempDir(), nil); terminal != nil {
		t.Fatal("empty command should not create a terminal")
	}
}

func TestTerminalStartsInRepoWithContextAndRendersOutput(t *testing.T) {
	dir := t.TempDir()
	terminal := New(`printf '%s|%s' "$PWD" "$LIVE_PR_BASE"; sleep 1`, dir, Environment("main", "main", "feature", "HEAD", "", "", ""))
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

func TestReviewerReadingPTYIsNotStopped(t *testing.T) {
	terminal := New(`cat`, t.TempDir(), nil)
	terminal.Resize(40, 4)
	defer terminal.Close()
	if cmd := terminal.Init(); cmd == nil {
		t.Fatal("reviewer did not start")
	}

	var watchdogPID, pid int
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(terminal.pidFile)
		if err == nil {
			watchdogPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if watchdogPID > 0 {
				out, _ := exec.Command("pgrep", "-P", strconv.Itoa(watchdogPID)).Output()
				pid, _ = strconv.Atoi(strings.TrimSpace(string(out)))
				if pid > 0 {
					break
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("reviewer PID was not found")
	}
	time.Sleep(100 * time.Millisecond)
	state, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(string(state)), "T") {
		t.Fatalf("reviewer stopped while reading PTY: pid=%d state=%q", pid, state)
	}
}

func TestTerminalShowsCursor(t *testing.T) {
	terminal := New(`printf 'x'; sleep 1`, t.TempDir(), nil)
	terminal.Resize(20, 4)
	defer terminal.Close()

	cmd := terminal.Init()
	deadline := time.Now().Add(time.Second)
	for cmd != nil && time.Now().Before(deadline) {
		msg := cmd()
		cmd = terminal.Update(msg)
		if strings.Contains(terminal.View(20, 4), "\x1b[7m") {
			return
		}
	}
	t.Fatalf("cursor was not rendered: %q", terminal.View(20, 4))
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
	deadline := time.Now().Add(time.Second)
	var msg tea.Msg
	for terminal.Available() && cmd != nil && time.Now().Before(deadline) {
		msg = cmd()
		cmd = terminal.Update(msg)
	}
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

// TestKillTreeReachesGroupEscapedDescendants reproduces the nvim leak: the
// TUI client re-spawns itself as an --embed server in its own process group,
// which a plain group SIGKILL misses. killTree must reach it via the PPID walk.
func TestKillTreeReachesGroupEscapedDescendants(t *testing.T) {
	// sh spawns a child that escapes into its own process group (like nvim's
	// embedded server), then keeps a same-group child alive too.
	cmd := exec.Command("sh", "-c", `perl -e 'setpgrp(0,0); sleep 30' & sleep 30 & wait`)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	root := cmd.Process.Pid

	var kids []int
	for range 40 { // wait for both children to appear
		kids = descendants(root)
		if len(kids) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(kids) < 2 {
		t.Fatalf("descendants(%d) = %v, want the escaped perl and the sleep", root, kids)
	}

	killTree(root)
	deadline := time.Now().Add(3 * time.Second)
	for _, pid := range kids {
		for processAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		if processAlive(pid) {
			t.Fatalf("descendant %d survived killTree", pid)
		}
	}
}
