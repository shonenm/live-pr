package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextExtractsTurnsAndDropsTools(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"build the thing"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"on it"},{"type":"tool_use","name":"Bash","input":{}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"system","subtype":"x","content":"noise"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Text(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"user: build the thing", "assistant: on it", "assistant: done"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// tool traffic and system noise must be dropped
	for _, no := range []string{"Bash", "tool_result", "noise", "ok"} {
		if strings.Contains(got, no) {
			t.Errorf("did not expect %q in transcript:\n%s", no, got)
		}
	}
}

func TestTextTailCap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, `{"type":"user","message":{"role":"user","content":"message number `+string(rune('A'+i%26))+`"}}`)
	}
	os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644)

	got, err := Text(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 100 {
		t.Errorf("expected tail capped to 100 bytes, got %d", len(got))
	}
}
