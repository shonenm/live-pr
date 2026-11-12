package tui

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	gh "github.com/shonenm/live-pr/v2/internal/github"
)

// remoteWriteResult runs cmd, descending into batches, and returns the
// remoteCommentDone it produces.
func remoteWriteResult(t *testing.T, cmd tea.Cmd) remoteCommentDone {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command was dispatched")
	}
	var find func(msg tea.Msg) (remoteCommentDone, bool)
	find = func(msg tea.Msg) (remoteCommentDone, bool) {
		switch msg := msg.(type) {
		case remoteCommentDone:
			return msg, true
		case tea.BatchMsg:
			for _, c := range msg {
				if c == nil {
					continue
				}
				if done, ok := find(c()); ok {
					return done, true
				}
			}
		}
		return remoteCommentDone{}, false
	}
	done, ok := find(cmd())
	if !ok {
		t.Fatal("command produced no remoteCommentDone")
	}
	return done
}

func selectPRDescription(t *testing.T, m *Model) {
	t.Helper()
	m.detailView.conversationDirty = true
	for i, it := range m.conversationItems() {
		if it.kind() == itemPRDescription {
			m.detailView.cursors[conversationTab] = i
			return
		}
	}
	t.Fatal("conversation has no PR description item")
}

// Editing the description card replaces the PR body on GitHub. It must go
// through UpdateBody for the current PR, never the issue-comment edit API.
func TestEditPRDescriptionSendsBodyToUpdateBody(t *testing.T) {
	var gotNumber int
	var gotBody string
	commentEdits := 0
	m := testModel()
	m.client = &fakeGH{
		updateBody: func(number int, bodyFile string) error {
			data, err := os.ReadFile(bodyFile)
			if err != nil {
				return err
			}
			gotNumber, gotBody = number, string(data)
			return nil
		},
		editIssueComment: func(int64, string) error {
			commentEdits++
			return nil
		},
	}
	m.targetGeneration = 4
	// Keep the spinner out of the batch so the test does not wait on a tick.
	m.spinnerRunning = true
	m.cache.PR = &gh.PR{Number: 12, URL: "u", Body: "old description"}
	selectPRDescription(t, &m)

	opened, _ := m.editSelectedLocalItem()
	o, ok := opened.overlay.(localEditOverlay)
	if !ok || o.mode != editRemoteComment || o.target != "pr-description" || opened.localEditor.Value() != "old description" {
		t.Fatalf("description edit = overlay:%#v value:%q", opened.overlay, opened.localEditor.Value())
	}

	opened.localEditor.SetValue("new description\n")
	u, cmd := opened.Update(keyPress("ctrl+s"))
	sent := u.(Model)
	if sent.overlay != nil || !sent.remoteCommentBusy {
		t.Fatalf("ctrl+s did not start the update: overlay=%#v busy=%v", sent.overlay, sent.remoteCommentBusy)
	}
	done := remoteWriteResult(t, cmd)
	if gotNumber != 12 || gotBody != "new description\n" {
		t.Fatalf("UpdateBody(%d, %q)", gotNumber, gotBody)
	}
	if commentEdits != 0 {
		t.Fatal("description edit went through the issue-comment API")
	}
	if done.generation != 4 || !done.edited || done.err != nil {
		t.Fatalf("remoteCommentDone = %+v", done)
	}
}

// Unlike a comment, a description may be cleared: an empty body is sent.
func TestEditPRDescriptionAllowsEmptyBody(t *testing.T) {
	calls := 0
	gotBody := "unset"
	m := testModel()
	m.client = &fakeGH{updateBody: func(_ int, bodyFile string) error {
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return err
		}
		calls++
		gotBody = string(data)
		return nil
	}}
	m.spinnerRunning = true
	m.cache.PR = &gh.PR{Number: 12, URL: "u", Body: "old description"}
	selectPRDescription(t, &m)

	opened, _ := m.editSelectedLocalItem()
	opened.localEditor.SetValue("")
	u, cmd := opened.Update(keyPress("ctrl+s"))
	if got := u.(Model); got.overlay != nil {
		t.Fatalf("empty description was rejected: %#v", got.overlay)
	}
	remoteWriteResult(t, cmd)
	if calls != 1 || gotBody != "" {
		t.Fatalf("UpdateBody calls=%d body=%q", calls, gotBody)
	}
}

// The PR can vanish from the cache while the editor is open. Saving then
// keeps the editor up with an error instead of dispatching a write.
func TestEditPRDescriptionWithoutPRKeepsEditorOpen(t *testing.T) {
	m := testModel()
	m.cache.PR = &gh.PR{Number: 12, URL: "u", Body: "old description"}
	selectPRDescription(t, &m)

	opened, _ := m.editSelectedLocalItem()
	opened.cache.PR = nil
	u, cmd := opened.Update(keyPress("ctrl+s"))
	got := u.(Model)
	o, ok := got.overlay.(localEditOverlay)
	if !ok || o.errText != "no pull request" {
		t.Fatalf("overlay = %#v", got.overlay)
	}
	if cmd != nil || got.remoteCommentBusy {
		t.Fatalf("save without a PR dispatched a write: busy=%v", got.remoteCommentBusy)
	}
}

func TestRemoteDeleteConfirmDeletesOnlyOnYes(t *testing.T) {
	var deleted []int64
	m := testModel()
	m.client = &fakeGH{deleteIssueComment: func(id int64) error {
		deleted = append(deleted, id)
		return nil
	}}
	m.targetGeneration = 6
	m.spinnerRunning = true
	m.overlay = remoteDeleteOverlay{id: 44, title: "comment"}

	for _, key := range []string{"n", "esc", "q"} {
		u, cmd := m.Update(keyPress(key))
		got := u.(Model)
		if got.overlay != nil {
			t.Fatalf("%s left the confirm open: %#v", key, got.overlay)
		}
		if cmd != nil || got.remoteCommentBusy {
			t.Fatalf("%s dispatched a delete: busy=%v", key, got.remoteCommentBusy)
		}
	}

	// Any other key neither confirms nor dismisses.
	u, cmd := m.Update(keyPress("x"))
	if got := u.(Model); got.overlay == nil || cmd != nil || got.remoteCommentBusy {
		t.Fatalf("unrelated key acted on the confirm: overlay=%#v busy=%v", got.overlay, got.remoteCommentBusy)
	}
	if len(deleted) != 0 {
		t.Fatalf("delete ran without confirmation: %v", deleted)
	}

	u, cmd = m.Update(keyPress("y"))
	got := u.(Model)
	if got.overlay != nil || !got.remoteCommentBusy {
		t.Fatalf("y did not start the delete: overlay=%#v busy=%v", got.overlay, got.remoteCommentBusy)
	}
	done := remoteWriteResult(t, cmd)
	if len(deleted) != 1 || deleted[0] != 44 {
		t.Fatalf("DeleteIssueComment calls = %v; want [44]", deleted)
	}
	if done.generation != 6 || !done.deleted || done.err != nil {
		t.Fatalf("remoteCommentDone = %+v", done)
	}
}
