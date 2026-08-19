package tui

import (
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
	m.files = []git.ChangedFile{{Path: "main.go", Status: "M"}}
	m.diffCommand, m.diffTerminal = "", nil

	// a opens a GitHub conversation comment (not the review body, which is now on v).
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if got := u.(Model).localEditMode; got != addRemoteComment {
		t.Fatalf("a editor = %v, want addRemoteComment", got)
	}

	// A adds an inline review comment on the selected file.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A")})
	m = u.(Model)
	if m.localEditMode != addInlineReviewComment || !strings.Contains(m.localEditor.Value(), "path: main.go") {
		t.Fatalf("A inline editor = %v value=%q", m.localEditMode, m.localEditor.Value())
	}
	m.localEditor.SetValue("path: main.go\nline: 3\nside: RIGHT\n\nFix this.")
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
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

	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = u.(Model)
	if m.reviewSubmitEvent == "" || m.reviewSubmitTyping || !strings.Contains(m.renderReviewSubmitPopup(), "Request changes") {
		t.Fatalf("submit popup not opened: event=%q typing=%v popup=%q", m.reviewSubmitEvent, m.reviewSubmitTyping, m.renderReviewSubmitPopup())
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if !m.reviewSubmitTyping || m.localEditMode != editReviewBody {
		t.Fatalf("did not enter message after choosing type: typing=%v mode=%v", m.reviewSubmitTyping, m.localEditMode)
	}
	m.localEditor.SetValue("Please fix this before approve")
	// Enter stays in the editor as a newline; only Ctrl+S submits.
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if !m.reviewSubmitTyping || m.reviewSubmitting || !strings.Contains(m.localEditor.Value(), "\n") {
		t.Fatalf("enter did not insert newline: typing=%v submitting=%v value=%q", m.reviewSubmitTyping, m.reviewSubmitting, m.localEditor.Value())
	}
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = u.(Model)
	if cmd == nil || !m.reviewSubmitting {
		t.Fatalf("changes request not scheduled: submitting=%v cmd=%v", m.reviewSubmitting, cmd)
	}
	u, _ = m.Update(reviewSubmitted{event: gh.ReviewRequestChangesEvent})
	m = u.(Model)
	if m.reviewSubmitting || !strings.Contains(m.notice, "REQUEST_CHANGES") {
		t.Fatalf("submit result = submitting:%v notice:%q", m.reviewSubmitting, m.notice)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("submitted draft remains: %v", err)
	}
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
	m.conversationDirty = true

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
	m.cursors[conversationTab] = idxOf(99)
	edited, _ := m.editSelectedLocalItem()
	if edited.localEditMode != editRemoteComment || edited.remoteCommentID != 99 || edited.localEditor.Value() != "mine" {
		t.Fatalf("own comment edit = mode:%v id:%d value:%q", edited.localEditMode, edited.remoteCommentID, edited.localEditor.Value())
	}

	// Someone else's comment: rejected.
	m.cursors[conversationTab] = idxOf(100)
	rejected, _ := m.editSelectedLocalItem()
	if rejected.localEditMode == editRemoteComment {
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
