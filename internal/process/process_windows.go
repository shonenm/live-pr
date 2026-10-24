//go:build windows

package process

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func configureProcess(_ *exec.Cmd) {}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	// taskkill's native tree option includes Git transports and shell children.
	// Bound cleanup independently of the already-cancelled caller context.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
