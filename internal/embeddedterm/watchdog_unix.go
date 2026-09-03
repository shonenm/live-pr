//go:build !windows

package embeddedterm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	watchdogModeEnv     = "LIVE_PR_REVIEW_WATCHDOG"
	watchdogParentEnv   = "LIVE_PR_REVIEW_PARENT"
	watchdogCommandEnv  = "LIVE_PR_REVIEW_COMMAND"
	watchdogPIDFileEnv  = "LIVE_PR_REVIEW_PID_FILE"
	watchdogLockFileEnv = "LIVE_PR_REVIEW_LOCK_FILE"
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
	pidFile := os.Getenv(watchdogPIDFileEnv)
	lockFile := os.Getenv(watchdogLockFileEnv)
	// The reviewer must not inherit the watchdog trigger: a live-pr invocation
	// from inside the reviewer (e.g. `live-pr review add` in a nvim terminal)
	// would otherwise re-enter watchdog mode, double-spawn the reviewer, and
	// clobber the pid file.
	for _, env := range []string{watchdogModeEnv, watchdogParentEnv, watchdogCommandEnv, watchdogPIDFileEnv, watchdogLockFileEnv} {
		_ = os.Unsetenv(env)
	}
	child := exec.Command("sh", "-c", "exec "+command)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Inherit the watchdog's PTY foreground group. Creating a separate reviewer
	// group stops interactive readers with SIGTTIN unless terminal ownership is
	// transferred too; one watchdog-owned group gives cleanup the same boundary.
	if err := child.Start(); err != nil {
		return 1
	}
	group := os.Getpid()
	if pidFile != "" {
		writePIDFile(pidFile, group)
		writeReviewerLock(lockFile, pidFile)
		defer func() {
			removeReviewerLock(lockFile, pidFile)
			removePIDFile(pidFile)
		}()
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
			removeReviewerLock(lockFile, pidFile)
			removePIDFile(pidFile)
			killTree(group)
			return 0
		case <-ticker.C:
			if !processAlive(parent) || os.Getppid() != parent {
				// killTree SIGKILLs our own process group, so the deferred
				// remove never runs; delete the pid file first.
				removeReviewerLock(lockFile, pidFile)
				removePIDFile(pidFile)
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

func killTreeAndWait(root int) {
	pids := append(descendants(root), root)
	killTree(root)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range pids {
			if processRunning(pid) {
				alive = true
				break
			}
		}
		if !alive {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processRunning(pid int) bool {
	if !processAlive(pid) {
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && !strings.Contains(string(out), "Z")
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

// writePIDFile creates the pid file exclusively: the parent kills whatever
// PID the file names, so a path pre-created by someone else (file or symlink)
// must never be adopted.
func writePIDFile(path string, pid int) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	_, _ = f.WriteString(strconv.Itoa(pid))
	_ = f.Close()
}

func removePIDFile(path string) {
	if path != "" {
		_ = os.Remove(path)
		// The parent puts the pid file in a dedicated directory; drop it too
		// when the parent is gone. Remove refuses non-empty directories, so
		// this is safe even if the path ever pointed elsewhere.
		_ = os.Remove(filepath.Dir(path))
	}
}

func reviewerLockPath(cwd, command string, env []string) string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err == nil {
		cwd = abs
	}
	h := sha256.New()
	_, _ = h.Write([]byte(cwd))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(command))
	identity := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "LIVE_PR_PR_URL=") && strings.TrimPrefix(kv, "LIVE_PR_PR_URL=") != "" {
			identity = kv
			break
		}
		if strings.HasPrefix(kv, "LIVE_PR_HEAD=") && identity == "" {
			identity = kv
		}
	}
	if identity != "" {
		_, _ = h.Write([]byte("\x00"))
		_, _ = h.Write([]byte(identity))
	}
	return filepath.Join(cache, "live-pr", "reviewers", hex.EncodeToString(h.Sum(nil))+".lock")
}

func closeExistingReviewer(lockFile string) {
	if lockFile == "" {
		return
	}
	data, err := os.ReadFile(lockFile)
	if err != nil {
		return
	}
	pidFile := strings.TrimSpace(string(data))
	if pidFile == "" || filepath.Dir(pidFile) == filepath.Dir(lockFile) {
		_ = os.Remove(lockFile)
		return
	}
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		_ = os.Remove(lockFile)
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil || pid <= 1 {
		_ = os.Remove(lockFile)
		return
	}
	killTreeAndWait(pid)
	_ = os.Remove(lockFile)
}

func writeReviewerLock(lockFile, pidFile string) {
	if lockFile == "" || pidFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(lockFile), 0o700); err != nil {
		return
	}
	tmp := lockFile + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, []byte(pidFile), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, lockFile)
}

func removeReviewerLock(lockFile, pidFile string) {
	if lockFile == "" || pidFile == "" {
		return
	}
	data, err := os.ReadFile(lockFile)
	if err == nil && strings.TrimSpace(string(data)) == pidFile {
		_ = os.Remove(lockFile)
	}
}
