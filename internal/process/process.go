// Package process configures cancellable child processes and bounded pipe waits.
package process

import (
	"context"
	"os/exec"
	"time"
)

// CommandContext cancels the process group on Unix, including shell/SSH children.
// Windows uses taskkill's tree option. WaitDelay bounds inherited output pipes.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureProcess(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd
}
