package event

import (
	"path/filepath"
	"testing"
)

func TestAppendLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "timeline.jsonl")

	e1 := New(Decision, "chose X", "because Y")
	e2 := Event{TS: "2026-07-21T10:00", Kind: Commit, Title: "feat: thing", SHA: "abc123"}

	for _, e := range []Event{e1, e2} {
		if err := Append(p, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events, got %d", len(got))
	}
	if got[0].Kind != Decision || got[0].Title != "chose X" || got[0].Body != "because Y" {
		t.Errorf("event 0 mismatch: %+v", got[0])
	}
	if got[1].Kind != Commit || got[1].SHA != "abc123" {
		t.Errorf("event 1 mismatch: %+v", got[1])
	}
	// omitempty: a Commit-less event must not serialize an empty sha back as set
	if got[0].SHA != "" {
		t.Errorf("expected empty sha on event 0, got %q", got[0].SHA)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if got != nil {
		t.Errorf("missing file should yield nil events, got %v", got)
	}
}
