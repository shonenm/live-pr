package event

import (
	"os"
	"path/filepath"
	"strings"
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

func TestUpdateAndDeleteRemainAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timeline.jsonl")
	created, err := Create(path, Event{TS: "2026-07-21T10:00", Kind: Decision, Title: "Use REST", Author: "agent"})
	if err != nil || created.ID == "" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	updated, err := Update(path, created.ID, Event{Kind: Pivot, Title: "Use GraphQL", Body: "One round trip"})
	if err != nil || updated.TS != created.TS || updated.Author != "agent" || updated.UpdatedAt == "" {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	events, err := Load(path)
	if err != nil || len(events) != 1 || events[0].Title != "Use GraphQL" {
		t.Fatalf("load after update = %+v, %v", events, err)
	}
	if err := Delete(path, created.ID); err != nil {
		t.Fatal(err)
	}
	events, err = Load(path)
	if err != nil || len(events) != 0 {
		t.Fatalf("load after delete = %+v, %v", events, err)
	}
	raw, _ := os.ReadFile(path)
	if lines := strings.Count(strings.TrimSpace(string(raw)), "\n") + 1; lines != 3 {
		t.Fatalf("record lines = %d, want create/update/delete", lines)
	}
}

func TestLegacyEventGetsStableEditableID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timeline.jsonl")
	if err := os.WriteFile(path, []byte(`{"ts":"2026-07-21T10:00","kind":"decision","title":"legacy"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Load(path)
	if err != nil || len(first) != 1 || first[0].ID == "" {
		t.Fatalf("legacy load = %+v, %v", first, err)
	}
	second, _ := Load(path)
	if second[0].ID != first[0].ID {
		t.Fatalf("legacy ID changed: %q != %q", first[0].ID, second[0].ID)
	}
	if _, err := Update(path, first[0].ID, Event{Kind: Decision, Title: "edited legacy"}); err != nil {
		t.Fatal(err)
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
