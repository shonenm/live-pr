// Package summarize turns a session transcript into a timeline summary entry.
package summarize

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
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

const prompt = `Summarize this coding session as one pull-request timeline entry.
Output exactly:
- First line: a concise title (<= 72 chars) naming the key decision, pivot, or what was done.
- A blank line.
- 2-4 short bullet points ("- ...") covering decisions made, direction changes, and what changed.
No preamble and no closing remarks.`

// Claude summarizes via the `claude -p` headless CLI, piping the transcript on
// stdin.
type Claude struct {
	Model string // optional --model override
}

func (c Claude) Summarize(transcript string) (Summary, error) {
	args := []string{"-p", prompt}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	cmd := exec.Command("claude", args...)
	cmd.Stdin = strings.NewReader(transcript)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return Summary{}, fmt.Errorf("claude summarize: %w: %s", err, detail)
		}
		return Summary{}, fmt.Errorf("claude summarize: %w", err)
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
