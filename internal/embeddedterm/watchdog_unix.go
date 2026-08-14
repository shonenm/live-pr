//go:build !windows

package embeddedterm

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	watchdogModeEnv    = "LIVE_PR_REVIEW_WATCHDOG"
	watchdogParentEnv  = "LIVE_PR_REVIEW_PARENT"
	watchdogCommandEnv = "LIVE_PR_REVIEW_COMMAND"
	watchdogPIDFileEnv = "LIVE_PR_REVIEW_PID_FILE"
)

func init() {
	if os.Getenv(watchdogModeEnv) != "1" {
		return
	}
	parent, err := strconv.Atoi(os.Getenv(watchdogParentEnv))
	if err != nil || parent <= 1 || os.Getenv(watchdogCommandEnv) == "" {
		os.Exit(2)
	}
	os.Exit(runWatchdog(parent, os.Getenv(watchdogCommandEnv)))
}

func runWatchdog(parent int, command string) int {
	child := exec.Command("sh", "-c", "exec "+command)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Inherit the watchdog's PTY foreground group. Creating a separate reviewer
	// group stops interactive readers with SIGTTIN unless terminal ownership is
	// transferred too; one watchdog-owned group gives cleanup the same boundary.
	if err := child.Start(); err != nil {
		return 1
	}
	group := os.Getpid()
	if pidFile := os.Getenv(watchdogPIDFileEnv); pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(group)), 0o600)
		defer os.Remove(pidFile)
	}

	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopping)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			if err == nil {
				return 0
			}
			return 1
		case <-stopping:
			killTree(group)
			return 0
		case <-ticker.C:
			if !processAlive(parent) || os.Getppid() != parent {
				killTree(group)
				return 0
			}
		}
	}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// killTree kills every descendant of root, then root's process group. A group
// kill alone is not enough: Neovim's TUI client re-spawns itself as an
// `nvim --embed` server in its own process group (with LSP servers under
// that), so those descendants survive a group SIGKILL and leak as orphans.
// Walking the PPID tree while the parents are still alive reaches them
// regardless of their group or session.
func killTree(root int) {
	for _, pid := range descendants(root) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	_ = syscall.Kill(-root, syscall.SIGKILL)
}

// descendants returns every transitive child of root, from a single ps
// snapshot (portable across macOS and Linux).
func descendants(root int) []int {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}
	var result []int
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range children[pid] {
			result = append(result, c)
			queue = append(queue, c)
		}
	}
	return result
}
