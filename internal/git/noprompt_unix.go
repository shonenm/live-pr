//go:build !windows

package git

import (
	"os/exec"
	"syscall"
)

// detachTerminal starts cmd in a new session. ssh ignores
// GIT_TERMINAL_PROMPT and opens /dev/tty itself; without a controlling
// terminal it cannot, and fails instead of prompting.
func detachTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
