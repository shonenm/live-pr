// Package diffview runs an optional non-interactive right-pane diff filter.
package diffview

import (
	"context"
	"fmt"
	"github.com/shonenm/live-pr/internal/process"
	"os"
	"strings"
	"time"
)

const (
	defaultTimeout = 2 * time.Second
	maxLines       = 800
	maxStdoutBytes = 1 << 20
	maxStderrBytes = 8 << 10
)

// Render pipes raw diff to command and returns its bounded stdout.
func Render(ctx context.Context, command, raw string, width int) (string, error) {
	return render(ctx, command, raw, width, defaultTimeout)
}

func render(ctx context.Context, command, raw string, width int, timeout time.Duration) (string, error) {
	if strings.TrimSpace(command) == "" {
		return raw, nil
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := process.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = append(os.Environ(), fmt.Sprintf("COLUMNS=%d", width), fmt.Sprintf("LIVE_PR_DIFF_WIDTH=%d", width))
	cmd.Stdin = strings.NewReader(raw)
	stdout := &boundedBuffer{limit: maxStdoutBytes}
	stderr := &boundedBuffer{limit: maxStderrBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("diff display timed out: %w", ctx.Err())
		}
		if msg := firstLine(stderr.String(), 300); msg != "" {
			return "", fmt.Errorf("diff display: %w: %s", err, msg)
		}
		return "", fmt.Errorf("diff display: %w", err)
	}
	return truncate(stdout.String(), maxLines), nil
}

type boundedBuffer struct {
	buf   []byte
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if remaining := b.limit - len(b.buf); remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		b.buf = append(b.buf, p[:remaining]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string { return string(b.buf) }

func firstLine(s string, limit int) string {
	line := strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if len(line) > limit {
		return line[:limit] + "…"
	}
	return line
}

func truncate(s string, limit int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > limit {
		lines = append(lines[:limit], "… (truncated)")
	}
	return strings.Join(lines, "\n")
}
