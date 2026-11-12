//go:build windows

package git

import "os/exec"

func detachTerminal(_ *exec.Cmd) {}
