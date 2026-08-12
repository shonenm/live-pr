//go:build !windows

package embeddedterm

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
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
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		return 1
	}
	if pidFile := os.Getenv(watchdogPIDFileEnv); pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
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
			killProcessGroup(child.Process.Pid)
			<-done
			return 0
		case <-ticker.C:
			if !processAlive(parent) || os.Getppid() != parent {
				killProcessGroup(child.Process.Pid)
				<-done
				return 0
			}
		}
	}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func killProcessGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }
