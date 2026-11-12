package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func TestInlineReviewCommentSurvivesUnreadableDraft(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "abc123"}
	m.detailView.files = []git.ChangedFile{{Path: "main.go", Status: "M"}}
	path := store.PullRequestReviewDraft(m.root, 12)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	u, _ := m.Update(keyPress("A"))
	m = u.(Model)
	if _, ok := m.overlay.(localEditOverlay); !ok {
		t.Fatalf("inline editor not opened: overlay=%#v status=%q", m.overlay, m.status)
	}
	if !strings.Contains(m.status, path+".corrupt-") {
		t.Fatalf("status = %q; want the moved draft path", m.status)
	}
	m.localEditor.SetValue("path: main.go\nline: 3\nside: RIGHT\n\nFix this.")
	u, _ = m.Update(keyPress("ctrl+s"))
	m = u.(Model)

	draft, err := gh.LoadReviewDraft(path, 12, "abc123")
	if err != nil || len(draft.Comments) != 1 || draft.Comments[0].Body != "Fix this." {
		t.Fatalf("saved draft = %#v err=%v", draft, err)
	}
	moved, _ := filepath.Glob(path + ".corrupt-*")
	if len(moved) != 1 {
		t.Fatalf("moved drafts = %v; want the unreadable file kept aside", moved)
	}
}

func TestReviewSubmitIncludesCommentsAddedWhilePopupOpen(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "abc123"}
	path := store.PullRequestReviewDraft(m.root, 12)

	u, _ := m.Update(keyPress("v"))
	m = u.(Model)
	u, _ = m.Update(keyPress("enter"))
	m = u.(Model)
	if o, ok := m.overlay.(reviewSubmitOverlay); !ok || !o.typing {
		t.Fatalf("review body editor not opened: overlay=%#v", m.overlay)
	}

	// Another process (`live-pr review add`) adds a comment meanwhile.
	concurrent := gh.NewReviewDraft(12, "abc123")
	concurrent.Comments = []gh.ReviewComment{{Path: "main.go", Line: 3, Side: "RIGHT", Body: "added from the CLI"}}
	if err := gh.SaveReviewDraft(path, concurrent); err != nil {
		t.Fatal(err)
	}

	m.localEditor.SetValue("Looks good overall")
	u, cmd := m.Update(keyPress("ctrl+s"))
	m = u.(Model)
	if cmd == nil || !m.reviewSubmitting {
		t.Fatalf("review not scheduled: submitting=%v status=%q", m.reviewSubmitting, m.status)
	}
	if len(m.reviewDraft.Comments) != 1 || m.reviewDraft.Body != "Looks good overall" {
		t.Fatalf("submitted draft = %#v; want the CLI comment and the typed body", m.reviewDraft)
	}
	persisted, err := gh.LoadReviewDraft(path, 12, "abc123")
	if err != nil || len(persisted.Comments) != 1 || persisted.Body != "Looks good overall" {
		t.Fatalf("persisted draft = %#v err=%v; want the CLI comment kept", persisted, err)
	}
}

func TestOfflineCommentQueuesPastUnreadableOutbox(t *testing.T) {
	m := outboxTestModel(t, 7)
	if err := os.MkdirAll(filepath.Dir(m.outboxPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.outboxPath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(remoteCommentDone{
		number: 7,
		body:   "hello from the train",
		err:    errors.New("gh api: dial tcp: connection refused"),
	})
	model := next.(Model)
	if len(model.outbox) != 1 || model.outbox[0].Body != "hello from the train" {
		t.Fatalf("outbox = %+v; want the failed comment queued", model.outbox)
	}
	if !strings.Contains(model.status, model.outboxPath+".corrupt-") {
		t.Fatalf("status = %q; want the moved outbox path", model.status)
	}
	entries, err := store.LoadOutbox(model.outboxPath)
	if err != nil || len(entries) != 1 {
		t.Fatalf("LoadOutbox = %+v, %v; want the queued comment on disk", entries, err)
	}
}
