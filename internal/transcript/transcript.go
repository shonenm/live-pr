// Package transcript extracts a compact natural-language transcript from a
// Claude Code session log (JSONL), dropping tool traffic.
package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"unicode/utf8"
)

type entry struct {
	Type    string `json:"type"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Text returns the user/assistant natural-language turns of a transcript file as
// "role: text" blocks. Tool calls/results and other entry types are skipped. If
// the result exceeds maxBytes (>0), only the newest tail is kept.
func Text(path string, maxBytes int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	chunks := make([]string, 0, 128)
	start, retained := 0, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e entry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if e.Type != "user" && e.Type != "assistant" {
			continue
		}
		text := extractText(e.Message.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		role := e.Message.Role
		if role == "" {
			role = e.Type
		}
		chunk := role + ": " + strings.TrimSpace(text) + "\n\n"
		chunks = append(chunks, chunk)
		retained += len(chunk)
		if maxBytes > 0 {
			for start < len(chunks) && retained-len(chunks[start]) >= maxBytes {
				retained -= len(chunks[start])
				chunks[start] = ""
				start++
			}
			if start >= 1024 && start*2 >= len(chunks) {
				chunks = append([]string(nil), chunks[start:]...)
				start = 0
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}

	out := strings.Join(chunks[start:], "")
	if maxBytes > 0 && len(out) > maxBytes {
		start := len(out) - maxBytes
		for start < len(out) && !utf8.RuneStart(out[start]) {
			start++
		}
		out = out[start:]
	}
	return out, nil
}

// extractText handles content that is either a plain string or an array of
// typed blocks, returning only the text blocks joined.
func extractText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []block
	if json.Unmarshal(content, &blocks) == nil {
		var parts []string
		for _, bl := range blocks {
			if bl.Type == "text" && bl.Text != "" {
				parts = append(parts, bl.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
