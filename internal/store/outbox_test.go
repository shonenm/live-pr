package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	if _, err := AppendOutbox(path, OutboxEntry{PR: 7, Body: "first"}); err != nil {
		t.Fatal(err)
	}
	entries, err := AppendOutbox(path, OutboxEntry{PR: 7, Body: "second", CommentID: 31})
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
	remaining, err := RemoveOutbox(path, loaded[0].ID)
	if err != nil || len(remaining) != 1 || remaining[0].Body != "second" {
		t.Fatalf("partial remove = %+v, %v", remaining, err)
	}
	if _, err := RemoveOutbox(path, loaded[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an empty queue must remove the file: %v", err)
	}
	if _, err := RemoveOutbox(path, "already-missing"); err != nil {
		t.Fatalf("mutating an already-missing queue must be a no-op: %v", err)
	}
}

func TestOutboxMutationHelper(t *testing.T) {
	if os.Getenv("LIVE_PR_OUTBOX_HELPER") != "1" {
		return
	}
	path, operation, id := os.Getenv("LIVE_PR_OUTBOX_PATH"), os.Getenv("LIVE_PR_OUTBOX_OPERATION"), os.Getenv("LIVE_PR_OUTBOX_ID")
	var err error
	switch operation {
	case "append":
		_, err = AppendOutbox(path, OutboxEntry{ID: id, PR: 7, Body: id})
	case "remove":
		_, err = RemoveOutbox(path, id)
	default:
		t.Fatalf("unknown helper operation %q", operation)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestOutboxMutationsAcrossProcessesPreserveConcurrentAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox", "7.json")
	for _, entry := range []OutboxEntry{
		{ID: "acknowledged", PR: 7, Body: "posted"},
		{ID: "discarded", PR: 7, Body: "discard"},
		{ID: "kept", PR: 7, Body: "keep"},
	} {
		if _, err := AppendOutbox(path, entry); err != nil {
			t.Fatal(err)
		}
	}

	type child struct {
		cmd *exec.Cmd
		out bytes.Buffer
	}
	newChild := func(operation, id string) *child {
		c := &child{cmd: exec.Command(os.Args[0], "-test.run=^TestOutboxMutationHelper$")}
		c.cmd.Env = append(os.Environ(),
			"LIVE_PR_OUTBOX_HELPER=1",
			"LIVE_PR_OUTBOX_PATH="+path,
			"LIVE_PR_OUTBOX_OPERATION="+operation,
			"LIVE_PR_OUTBOX_ID="+id,
		)
		c.cmd.Stdout, c.cmd.Stderr = &c.out, &c.out
		return c
	}

	children := []*child{newChild("remove", "acknowledged"), newChild("remove", "discarded")}
	const appendCount = 24
	for i := range appendCount {
		children = append(children, newChild("append", fmt.Sprintf("concurrent-%02d", i)))
	}
	for _, child := range children {
		if err := child.cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, child := range children {
		if err := child.cmd.Wait(); err != nil {
			t.Fatalf("outbox helper failed: %v\n%s", err, child.out.String())
		}
	}

	entries, err := LoadOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(entries))
	for _, entry := range entries {
		got[entry.ID] = true
	}
	if len(entries) != appendCount+1 || !got["kept"] || got["acknowledged"] || got["discarded"] {
		t.Fatalf("mutated outbox = %+v", entries)
	}
	for i := range appendCount {
		if id := fmt.Sprintf("concurrent-%02d", i); !got[id] {
			t.Errorf("concurrent append %q was lost", id)
		}
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
