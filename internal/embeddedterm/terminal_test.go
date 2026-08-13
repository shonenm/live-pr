//go:build !windows

package embeddedterm

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	portalis "github.com/Starframe/portalis"
	tea "github.com/charmbracelet/bubbletea"
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
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix process groups")
	}
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
	env := Environment("base-sha...head-ref", "main", "feature/x", "refs/live-pr/pulls/1/head", "https://example.test/pr/1", "abc123")
	want := []string{"LIVE_PR_REVIEW=1", "LIVE_PR_RANGE=base-sha...head-ref", "LIVE_PR_BASE=main", "LIVE_PR_HEAD=feature/x", "LIVE_PR_HEAD_REV=refs/live-pr/pulls/1/head", "LIVE_PR_PR_URL=https://example.test/pr/1", "LIVE_PR_SHA=abc123"}
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
	terminal := New(`printf '%s|%s' "$PWD" "$LIVE_PR_BASE"; sleep 1`, dir, Environment("main", "main", "feature", "HEAD", "", ""))
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
