// Package summarize turns a session transcript into a timeline summary entry.
package summarize

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Summary is a distilled session entry: a headline and supporting detail.
type Summary struct {
	Title string
	Body  string
}

// Summarizer produces a Summary from a transcript. Implementations may call an
// LLM, a heuristic, or (in tests) return a canned result.
type Summarizer interface {
	Summarize(transcript string) (Summary, error)
}

const prompt = `Decide whether this coding session contains one PR-level fact a reviewer will need later.
Record only a material design decision, a change in direction, a consequential constraint/tradeoff, or a significant review finding that changed scope or behavior.
Do not record routine implementation progress, test failures/fixes, cleanup, readability edits, formatting, or a list of files changed.
If there is no qualifying fact, output nothing.
Otherwise output exactly:
- First line: a concise title (<= 72 chars) naming the decision, pivot, constraint, or finding.
- A blank line.
- 1-3 short bullets explaining rationale and reviewer-visible impact.
No preamble and no closing remarks.`

// Claude summarizes via the `claude -p` headless CLI, piping the transcript on
// stdin.
type Claude struct {
	Model   string        // optional --model override
	Timeout time.Duration // bounds the CLI call; zero means 60s
}

func (c Claude) Summarize(transcript string) (Summary, error) {
	args := []string{"-p", prompt}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	return run("claude summarize", c.Timeout, transcript, "claude", args...)
}

// Command summarizes via a user-configured shell command (run with `sh -c`),
// piping the transcript on stdin and parsing stdout like Claude's output:
// first non-empty line is the title, the rest is the body.
type Command struct {
	Command string        // shell command line
	Timeout time.Duration // bounds the call; zero means 60s
}

func (c Command) Summarize(transcript string) (Summary, error) {
	return run("summarize command", c.Timeout, transcript, "sh", "-c", c.Command)
}

// run executes a summarizer process with the transcript on stdin and parses
// its stdout. The stop hook must never block the agent, so a stuck process
// (auth prompt, network wait) is killed at the deadline rather than waited on
// forever.
func run(label string, timeout time.Duration, transcript, name string, args ...string) (Summary, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// The deadline kills only the direct child; a grandchild (sh -c spawning
	// the real tool) can keep the stdout pipe open and stall Wait forever.
	// WaitDelay caps that wait after the kill.
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(transcript)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("timed out after %s: %w", timeout, err)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return Summary{}, fmt.Errorf("%s: %w: %s", label, err, detail)
		}
		return Summary{}, fmt.Errorf("%s: %w", label, err)
	}
	return Parse(string(out)), nil
}

// Parse splits a summarizer's output into title (first non-empty line) and body.
func Parse(s string) Summary {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	title, rest := "", 0
	for i, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			title = strings.TrimSpace(ln)
			rest = i + 1
			break
		}
	}
	title = strings.TrimLeft(title, "# ")
	body := strings.TrimSpace(strings.Join(lines[rest:], "\n"))
	return Summary{Title: title, Body: body}
}
