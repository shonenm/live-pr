package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shonenm/live-pr/internal/atomicfile"
)

func TestAppendOutboxQueuesPastUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox", "7.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := AppendOutbox(path, OutboxEntry{PR: 7, Body: "typed while offline"})
	var quarantined *atomicfile.QuarantinedError
	if !errors.As(err, &quarantined) {
		t.Fatalf("err = %v; want a QuarantinedError", err)
	}
	if len(entries) != 1 || entries[0].Body != "typed while offline" {
		t.Fatalf("entries = %+v; want the new comment queued", entries)
	}
	if got, err := os.ReadFile(quarantined.Path); err != nil || string(got) != "not json" {
		t.Fatalf("moved file = %q, %v; want the original content", got, err)
	}
	loaded, err := LoadOutbox(path)
	if err != nil || len(loaded) != 1 || loaded[0].Body != "typed while offline" {
		t.Fatalf("LoadOutbox = %+v, %v; want the queued comment", loaded, err)
	}
}
