package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCommentOutboxScopesByPR(t *testing.T) {
	root := t.TempDir()
	got := CommentOutbox(root, 42)
	if filepath.Base(got) != "42.json" || filepath.Base(filepath.Dir(got)) != "outbox" {
		t.Fatalf("comment outbox path = %q", got)
	}
	if got == CommentOutbox(root, 43) {
		t.Fatal("different PRs must not share an outbox file")
	}
}

func TestLoadOutboxMissingFileIsEmptyQueue(t *testing.T) {
	entries, err := LoadOutbox(filepath.Join(t.TempDir(), "outbox", "7.json"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("LoadOutbox(missing) = %v, %v; want empty, nil", entries, err)
	}
}

func TestOutboxRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := CommentOutbox(t.TempDir(), 7)
	entries, err := AppendOutbox(path, OutboxEntry{PR: 7, Body: "first"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err = AppendOutbox(path, OutboxEntry{PR: 7, Body: "second", CommentID: 31})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("append returned %d entries; want 2", len(entries))
	}
	loaded, err := LoadOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[0].Body != "first" || loaded[1].Body != "second" || loaded[1].CommentID != 31 {
		t.Fatalf("round trip lost data: %+v", loaded)
	}
	if loaded[0].ID == "" || loaded[0].ID == loaded[1].ID {
		t.Fatalf("entries must carry unique IDs: %q, %q", loaded[0].ID, loaded[1].ID)
	}
	if loaded[0].CreatedAt == "" {
		t.Fatal("append must stamp a creation time")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("outbox file mode = %o; want 0600", perm)
		}
	}
	if err := SaveOutbox(path, loaded[1:]); err != nil {
		t.Fatal(err)
	}
	remaining, err := LoadOutbox(path)
	if err != nil || len(remaining) != 1 || remaining[0].Body != "second" {
		t.Fatalf("partial save = %+v, %v", remaining, err)
	}
	if err := SaveOutbox(path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an empty queue must remove the file: %v", err)
	}
	if err := SaveOutbox(path, nil); err != nil {
		t.Fatalf("emptying an already-missing queue must be a no-op: %v", err)
	}
}

func TestLoadOutboxRejectsCorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "7.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOutbox(path); err == nil {
		t.Fatal("corrupt outbox must surface an error")
	}
}
