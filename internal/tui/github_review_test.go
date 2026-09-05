package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func TestParseInlineReviewComment(t *testing.T) {
	comment, err := parseInlineReviewComment("path: internal/x.go\nline: 14\nside: RIGHT\n\nHandle the error.")
	if err != nil || comment.Path != "internal/x.go" || comment.Line != 14 || comment.Side != "RIGHT" || comment.Body != "Handle the error." {
		t.Fatalf("comment = %#v err=%v", comment, err)
	}
	if _, err := parseInlineReviewComment("path: x.go\nline: nope\nside: RIGHT\n\nbody"); err == nil {
		t.Fatal("invalid line accepted")
	}
}

func TestCommentKeysSplitConversationAndInlineReview(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "abc123"}
	m.detailView.files = []git.ChangedFile{{Path: "main.go", Status: "M"}}
	m.diffCommand, m.diffTerminal = "", nil

	// a opens a GitHub conversation comment (not the review body, which is now on v).
	u, _ := m.Update(keyPress("a"))
	if got, ok := u.(Model).overlay.(localEditOverlay); !ok || got.mode != addRemoteComment {
		t.Fatalf("a editor = %#v, want addRemoteComment", u.(Model).overlay)
	}

	// A adds an inline review comment on the selected file.
	u, _ = m.Update(keyPress("A"))
	m = u.(Model)
	if got, ok := m.overlay.(localEditOverlay); !ok || got.mode != addInlineReviewComment || !strings.Contains(m.localEditor.Value(), "path: main.go") {
		t.Fatalf("A inline editor = %#v value=%q", m.overlay, m.localEditor.Value())
	}
	m.localEditor.SetValue("path: main.go\nline: 3\nside: RIGHT\n\nFix this.")
	u, _ = m.Update(keyPress("ctrl+s"))
	m = u.(Model)

	path := store.PullRequestReviewDraft(m.root, 12)
	draft, err := gh.LoadReviewDraft(path, 12, "abc123")
	if err != nil || len(draft.Comments) != 1 || draft.Comments[0].Line != 3 {
		t.Fatalf("saved draft = %#v err=%v", draft, err)
	}
}

func TestReviewSubmitPopupAndResult(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "abc123"}
	path := store.PullRequestReviewDraft(m.root, 12)
	draft := gh.NewReviewDraft(12, "abc123")
	draft.Body = "Changes required"
	if err := gh.SaveReviewDraft(path, draft); err != nil {
		t.Fatal(err)
	}

	u, _ := m.Update(keyPress("v"))
	m = u.(Model)
	o, ok := m.overlay.(reviewSubmitOverlay)
	if !ok || o.event == "" || o.typing || !strings.Contains(o.render(m), "Request changes") {
		t.Fatalf("submit popup not opened: overlay=%#v", m.overlay)
	}
	u, _ = m.Update(keyPress("down"))
	m = u.(Model)
	u, _ = m.Update(keyPress("down"))
	m = u.(Model)
	u, _ = m.Update(keyPress("enter"))
	m = u.(Model)
	o, ok = m.overlay.(reviewSubmitOverlay)
	if !ok || !o.typing || o.event != gh.ReviewRequestChangesEvent {
		t.Fatalf("did not enter message after choosing type: overlay=%#v", m.overlay)
	}
	m.localEditor.SetValue("Please fix this before approve")
	// Enter stays in the editor as a newline; only Ctrl+S submits.
	u, _ = m.Update(keyPress("enter"))
	m = u.(Model)
	o, ok = m.overlay.(reviewSubmitOverlay)
	if !ok || !o.typing || m.reviewSubmitting || !strings.Contains(m.localEditor.Value(), "\n") {
		t.Fatalf("enter did not insert newline: overlay=%#v submitting=%v value=%q", m.overlay, m.reviewSubmitting, m.localEditor.Value())
	}
	u, cmd := m.Update(keyPress("ctrl+s"))
	m = u.(Model)
	if cmd == nil || !m.reviewSubmitting {
		t.Fatalf("changes request not scheduled: submitting=%v cmd=%v", m.reviewSubmitting, cmd)
	}
	u, _ = m.Update(reviewSubmitted{
		generation: m.targetGeneration,
		draftPath:  path,
		draft:      m.reviewDraft,
		event:      gh.ReviewRequestChangesEvent,
	})
	m = u.(Model)
	if m.reviewSubmitting || !strings.Contains(m.notice, "REQUEST_CHANGES") {
		t.Fatalf("submit result = submitting:%v notice:%q", m.reviewSubmitting, m.notice)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("submitted draft remains: %v", err)
	}
}

func TestReviewSubmissionCompletionOwnsSubmittedDraft(t *testing.T) {
	newSubmission := func(t *testing.T, client githubClient) (Model, gh.PR, gh.ReviewDraft, string, reviewSubmitted) {
		t.Helper()
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		m := testModel()
		m.root = t.TempDir()
		pr := gh.PR{Number: 11, HeadRefOID: "aaa", HeadRefName: "a"}
		m.cache.PR = &pr
		draft := gh.NewReviewDraft(pr.Number, pr.HeadRefOID)
		draft.Body = "submitted review"
		path := store.PullRequestReviewDraft(m.root, pr.Number)
		if err := gh.SaveReviewDraft(path, draft); err != nil {
			t.Fatal(err)
		}
		m.reviewDraft, m.reviewDraftPath, m.reviewSubmitting = draft, path, true
		msg := submitReview(client, draft, gh.ReviewApproveEvent, path, m.targetGeneration)().(reviewSubmitted)
		return m, pr, draft, path, msg
	}

	t.Run("target draft is cleared", func(t *testing.T) {
		m, _, _, path, msg := newSubmission(t, &fakeGH{})
		next, _ := m.handleReviewSubmitted(msg)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("submitted draft remains: %v", err)
		}
		if next.reviewDraft.PR != 0 {
			t.Fatalf("displayed draft was not cleared: %+v", next.reviewDraft)
		}
	})

	t.Run("switching PR clears only source draft", func(t *testing.T) {
		m, _, _, sourcePath, msg := newSubmission(t, &fakeGH{})
		other := gh.PR{Number: 22, HeadRefOID: "bbb", HeadRefName: "b"}
		otherDraft := gh.NewReviewDraft(other.Number, other.HeadRefOID)
		otherDraft.Body = "unpublished other review"
		otherPath := store.PullRequestReviewDraft(m.root, other.Number)
		if err := gh.SaveReviewDraft(otherPath, otherDraft); err != nil {
			t.Fatal(err)
		}
		_ = m.openRemote(other)
		next, cmd := m.handleReviewSubmitted(msg)
		if _, err := os.Stat(sourcePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("submitted source draft remains: %v", err)
		}
		if got, err := gh.LoadReviewDraft(otherPath, other.Number, other.HeadRefOID); err != nil || !reflect.DeepEqual(got, otherDraft) {
			t.Fatalf("other PR draft changed: %+v, %v", got, err)
		}
		if cmd != nil || next.reviewDraft.PR != other.Number {
			t.Fatalf("stale completion changed current display: draft=%+v cmd=%v", next.reviewDraft, cmd)
		}
	})

	t.Run("additions while submitting are preserved", func(t *testing.T) {
		m, _, draft, path, msg := newSubmission(t, &fakeGH{})
		updated := draft
		updated.Comments = append(updated.Comments, gh.ReviewComment{Path: "new.go", Line: 4, Side: "RIGHT", Body: "added while posting"})
		if err := gh.SaveReviewDraft(path, updated); err != nil {
			t.Fatal(err)
		}
		m.reviewDraft = updated
		next, _ := m.handleReviewSubmitted(msg)
		got, err := gh.LoadReviewDraft(path, updated.PR, updated.Commit)
		if err != nil || !reflect.DeepEqual(got, updated) {
			t.Fatalf("in-flight additions were lost: %+v, %v", got, err)
		}
		if !reflect.DeepEqual(next.reviewDraft, updated) {
			t.Fatalf("displayed additions were cleared: %+v", next.reviewDraft)
		}
	})

	t.Run("failure keeps draft", func(t *testing.T) {
		failure := errors.New("review rejected")
		m, _, draft, path, msg := newSubmission(t, &fakeGH{submitReview: func(gh.ReviewDraft, gh.ReviewEvent) error { return failure }})
		next, _ := m.handleReviewSubmitted(msg)
		got, err := gh.LoadReviewDraft(path, draft.PR, draft.Commit)
		if err != nil || !reflect.DeepEqual(got, draft) {
			t.Fatalf("failed submission changed draft: %+v, %v", got, err)
		}
		if !strings.Contains(next.status, failure.Error()) {
			t.Fatalf("failure status = %q", next.status)
		}
	})
}

func TestEditRemoteCommentOwnershipGate(t *testing.T) {
	m := testModel()
	m.viewerLogin = "me"
	m.cache.PR = &gh.PR{Number: 12, URL: "u"}
	own := gh.Comment{ID: 99, Body: "mine"}
	own.User.Login = "me"
	other := gh.Comment{ID: 100, Body: "theirs"}
	other.User.Login = "you"
	m.cache.Comments = []gh.Comment{own, other}
	m.detailView.conversationDirty = true

	items := m.conversationItems()
	idxOf := func(id int64) int {
		for i, it := range items {
			if it.comment != nil && it.comment.ID == id {
				return i
			}
		}
		return -1
	}

	// Own comment: e opens the remote editor targeting its id.
	m.detailView.cursors[conversationTab] = idxOf(99)
	edited, _ := m.editSelectedLocalItem()
	o, ok := edited.overlay.(localEditOverlay)
	if !ok || o.mode != editRemoteComment || o.remoteCommentID != 99 || edited.localEditor.Value() != "mine" {
		t.Fatalf("own comment edit = overlay:%#v value:%q", edited.overlay, edited.localEditor.Value())
	}

	// Someone else's comment: rejected.
	m.detailView.cursors[conversationTab] = idxOf(100)
	rejected, _ := m.editSelectedLocalItem()
	if o, ok := rejected.overlay.(localEditOverlay); ok && o.mode == editRemoteComment {
		t.Fatal("edited someone else's comment")
	}
}

// A comment posted, edited, or deleted on a remote PR (opened from the list)
// must refetch through fetchRemotePR. The fetchGitHub path resolves by branch,
// fails isCurrentTargetPR for a PR whose head is not the current branch, and
// wiped the detail into "Local only · no GitHub PR".
func TestRemoteCommentDoneRefetchesViaRemotePath(t *testing.T) {
	m := testModel()
	m.screen, m.remote = detailScreen, true
	m.currentBranch = "main"
	pr := gh.PR{Number: 14, HeadRefName: "feature", URL: "u"}
	m.cache = gh.NewCache("feature")
	m.cache.PR = &pr

	generation := m.targetGeneration
	u, cmd := m.Update(remoteCommentDone{generation: generation})
	m = u.(Model)
	if cmd == nil || m.targetGeneration != generation+1 {
		t.Fatalf("comment done did not schedule a refetch: generation=%d cmd=%v", m.targetGeneration, cmd)
	}
	// Identify the scheduled fetch by closure symbol name without invoking
	// it: running it would hit real git/gh. tea.Batch drops nil cmds, so the
	// refetch may arrive bare or wrapped in a batch.
	cmdName := func(c tea.Cmd) string {
		return runtime.FuncForPC(reflect.ValueOf(c).Pointer()).Name()
	}
	cmds := []tea.Cmd{cmd}
	if !strings.Contains(cmdName(cmd), "fetch") {
		batch, ok := cmd().(tea.BatchMsg)
		if !ok {
			t.Fatalf("scheduled cmd = %s, want a fetch or a batch", cmdName(cmd))
		}
		cmds = batch
	}
	sawRemote := false
	for _, sub := range cmds {
		name := cmdName(sub)
		if strings.Contains(name, "fetchGitHub") {
			t.Fatal("remote comment refetch dispatched fetchGitHub, whose result fails isCurrentTargetPR and wipes the remote detail")
		}
		if strings.Contains(name, "fetchRemotePR") {
			sawRemote = true
		}
	}
	if !sawRemote {
		t.Fatal("remote comment refetch did not dispatch fetchRemotePR")
	}
}

func TestCommentKeysOutsideConversationTabExplainInsteadOfSilence(t *testing.T) {
	m := testModel()
	m.cache.PR = &gh.PR{Number: 12, HeadRefOID: "abc123"}
	m.detailView.active = commitsTab

	u, _ := m.Update(keyPress("a"))
	m = u.(Model)
	if m.overlay != nil {
		t.Fatalf("a outside the conversation tab opened an editor: overlay=%#v", m.overlay)
	}
	if !strings.Contains(m.status, "Conversation tab") {
		t.Fatalf("a outside the conversation tab left status = %q, want a pointer to the Conversation tab", m.status)
	}
}

// A submitted review used to only clear the draft and post a notice: the
// review itself did not appear in the conversation until a manual refresh.
// Success must refetch through the same remote/local split as
// handleRemoteCommentDone — fetchRemotePR for a PR opened from the list,
// fetchGitHub for the current branch.
func TestReviewSubmittedRefetchesConversation(t *testing.T) {
	cmdName := func(c tea.Cmd) string {
		return runtime.FuncForPC(reflect.ValueOf(c).Pointer()).Name()
	}
	dispatched := func(t *testing.T, cmd tea.Cmd) []string {
		t.Helper()
		if cmd == nil {
			t.Fatal("review submitted did not schedule a refetch")
		}
		// Identify the scheduled fetch by closure symbol name without
		// invoking it: running it would hit real git/gh. tea.Batch drops nil
		// cmds, so the refetch may arrive bare or wrapped in a batch.
		cmds := []tea.Cmd{cmd}
		if !strings.Contains(cmdName(cmd), "fetch") {
			batch, ok := cmd().(tea.BatchMsg)
			if !ok {
				t.Fatalf("scheduled cmd = %s, want a fetch or a batch", cmdName(cmd))
			}
			cmds = batch
		}
		names := make([]string, 0, len(cmds))
		for _, sub := range cmds {
			names = append(names, cmdName(sub))
		}
		return names
	}

	t.Run("remote", func(t *testing.T) {
		m := testModel()
		m.screen, m.remote = detailScreen, true
		m.currentBranch = "main"
		m.cache = gh.NewCache("feature")
		m.cache.PR = &gh.PR{Number: 14, HeadRefName: "feature", URL: "u"}
		generation := m.targetGeneration
		path := filepath.Join(t.TempDir(), "draft.json")
		draft := gh.NewReviewDraft(14, "remote-head")
		if err := gh.SaveReviewDraft(path, draft); err != nil {
			t.Fatal(err)
		}
		u, cmd := m.Update(reviewSubmitted{generation: generation, draftPath: path, draft: draft, event: gh.ReviewApproveEvent})
		m = u.(Model)
		if m.targetGeneration != generation+1 {
			t.Fatalf("generation = %d, want %d", m.targetGeneration, generation+1)
		}
		sawRemote := false
		names := dispatched(t, cmd)
		for _, name := range names {
			if strings.Contains(name, "fetchGitHub") {
				t.Fatal("remote review refetch dispatched fetchGitHub, whose result fails isCurrentTargetPR and wipes the remote detail")
			}
			if strings.Contains(name, "fetchRemotePR") {
				sawRemote = true
			}
		}
		if !sawRemote {
			t.Fatalf("remote review refetch did not dispatch fetchRemotePR: %v", names)
		}
	})

	t.Run("local", func(t *testing.T) {
		m := testModel()
		m.screen, m.remote = detailScreen, false
		m.cache = gh.NewCache("feature/x")
		m.cache.PR = &gh.PR{Number: 14, HeadRefName: "feature/x"}
		generation := m.targetGeneration
		path := filepath.Join(t.TempDir(), "draft.json")
		draft := gh.NewReviewDraft(14, "local-head")
		if err := gh.SaveReviewDraft(path, draft); err != nil {
			t.Fatal(err)
		}
		u, cmd := m.Update(reviewSubmitted{generation: generation, draftPath: path, draft: draft, event: gh.ReviewCommentEvent})
		m = u.(Model)
		if m.targetGeneration != generation+1 {
			t.Fatalf("generation = %d, want %d", m.targetGeneration, generation+1)
		}
		sawLocal := false
		names := dispatched(t, cmd)
		for _, name := range names {
			if strings.Contains(name, "fetchRemotePR") {
				t.Fatal("local review refetch went through fetchRemotePR instead of the branch path")
			}
			if strings.Contains(name, "fetchGitHub") {
				sawLocal = true
			}
		}
		if !sawLocal {
			t.Fatalf("local review refetch did not dispatch fetchGitHub: %v", names)
		}
	})
}
