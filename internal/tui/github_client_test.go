package tui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
)

// fakeGH implements githubClient with per-method hooks. Unset hooks return
// zero values, so the fake doubles as a safe no-op client for testModel.
type fakeGH struct {
	searchPRs          func(query, cursor string) (gh.PRPage, error)
	findForHead        func(head string) (gh.PR, error)
	findPreview        func(number int) (gh.PR, error)
	findChecks         func(number int) (gh.PR, error)
	loadPRDetail       func(number int, prev gh.PRDetail) gh.PRDetail
	loadLocalPRDetail  func(number int, prev gh.PRDetail) gh.PRDetail
	merge              func(number int, headOID string, method gh.MergeMethod) error
	checkout           func(number int) error
	close              func(number int) error
	setStatus          func(pr gh.PR, target string) error
	postIssueComment   func(number int, body string) error
	editIssueComment   func(id int64, body string) error
	deleteIssueComment func(id int64) error
	updateBody         func(number int, bodyFile string) error
	submitReview       func(draft gh.ReviewDraft, event gh.ReviewEvent) error
}

func (f *fakeGH) SearchPRs(query, cursor string) (gh.PRPage, error) {
	if f.searchPRs != nil {
		return f.searchPRs(query, cursor)
	}
	return gh.PRPage{}, nil
}

func (f *fakeGH) FindForHead(head string) (gh.PR, error) {
	if f.findForHead != nil {
		return f.findForHead(head)
	}
	return gh.PR{}, nil
}

func (f *fakeGH) FindPreview(number int) (gh.PR, error) {
	if f.findPreview != nil {
		return f.findPreview(number)
	}
	return gh.PR{}, nil
}

func (f *fakeGH) FindChecks(number int) (gh.PR, error) {
	if f.findChecks != nil {
		return f.findChecks(number)
	}
	return gh.PR{}, nil
}

func (f *fakeGH) LoadPRDetail(number int, prev gh.PRDetail) gh.PRDetail {
	if f.loadPRDetail != nil {
		return f.loadPRDetail(number, prev)
	}
	return gh.PRDetail{}
}

func (f *fakeGH) LoadLocalPRDetail(number int, prev gh.PRDetail) gh.PRDetail {
	if f.loadLocalPRDetail != nil {
		return f.loadLocalPRDetail(number, prev)
	}
	if f.loadPRDetail != nil {
		return f.loadPRDetail(number, prev)
	}
	return gh.PRDetail{}
}

func (f *fakeGH) Merge(number int, headOID string, method gh.MergeMethod) error {
	if f.merge != nil {
		return f.merge(number, headOID, method)
	}
	return nil
}

func (f *fakeGH) Checkout(number int) error {
	if f.checkout != nil {
		return f.checkout(number)
	}
	return nil
}

func (f *fakeGH) Close(number int) error {
	if f.close != nil {
		return f.close(number)
	}
	return nil
}

func (f *fakeGH) SetStatus(pr gh.PR, target string) error {
	if f.setStatus != nil {
		return f.setStatus(pr, target)
	}
	return nil
}

func (f *fakeGH) PostIssueComment(number int, body string) error {
	if f.postIssueComment != nil {
		return f.postIssueComment(number, body)
	}
	return nil
}

func (f *fakeGH) EditIssueComment(id int64, body string) error {
	if f.editIssueComment != nil {
		return f.editIssueComment(id, body)
	}
	return nil
}

func (f *fakeGH) DeleteIssueComment(id int64) error {
	if f.deleteIssueComment != nil {
		return f.deleteIssueComment(id)
	}
	return nil
}

func (f *fakeGH) UpdateBody(number int, bodyFile string) error {
	if f.updateBody != nil {
		return f.updateBody(number, bodyFile)
	}
	return nil
}

func (f *fakeGH) SubmitReview(draft gh.ReviewDraft, event gh.ReviewEvent) error {
	if f.submitReview != nil {
		return f.submitReview(draft, event)
	}
	return nil
}

func TestRunPRActionMergeGuardsOnHeadCommit(t *testing.T) {
	var gotNumber int
	var gotOID string
	var gotMethod gh.MergeMethod
	client := &fakeGH{merge: func(number int, headOID string, method gh.MergeMethod) error {
		gotNumber, gotOID, gotMethod = number, headOID, method
		return nil
	}}
	pr := gh.PR{Number: 7, HeadRefOID: "abc123"}
	msg, ok := runPRAction(client, nil, mergePR, pr, gh.MergeSquash)().(prActionDone)
	if !ok {
		t.Fatal("runPRAction did not return prActionDone")
	}
	if gotNumber != 7 || gotOID != "abc123" || gotMethod != gh.MergeSquash {
		t.Fatalf("Merge(%d, %q, %q); want the PR number, its head commit, and the chosen method", gotNumber, gotOID, gotMethod)
	}
	if msg.action != mergePR || msg.number != 7 || msg.pr.Number != 7 || msg.err != nil {
		t.Fatalf("prActionDone = %+v", msg)
	}
}

func TestRunPRActionCloseSurfacesClientError(t *testing.T) {
	boom := errors.New("close refused")
	var gotNumber int
	client := &fakeGH{close: func(number int) error {
		gotNumber = number
		return boom
	}}
	msg := runPRAction(client, nil, closePR, gh.PR{Number: 12}, gh.MergeCommit)().(prActionDone)
	if gotNumber != 12 {
		t.Fatalf("Close(%d); want 12", gotNumber)
	}
	if msg.action != closePR || msg.number != 12 || !errors.Is(msg.err, boom) {
		t.Fatalf("prActionDone = %+v", msg)
	}
}

func TestRunPRActionSameRepoCheckoutUsesGit(t *testing.T) {
	ghCalled := false
	client := &fakeGH{checkout: func(int) error {
		ghCalled = true
		return nil
	}}
	var gotBranch string
	checkoutHead := func(branch string) error {
		gotBranch = branch
		return nil
	}
	pr := gh.PR{Number: 3, HeadRefName: "feature"}
	msg := runPRAction(client, checkoutHead, checkoutPR, pr, gh.MergeCommit)().(prActionDone)
	if ghCalled || gotBranch != "feature" || msg.action != checkoutPR || msg.number != 3 || msg.err != nil {
		t.Fatalf("git checkout branch=%q ghCalled=%v, prActionDone = %+v", gotBranch, ghCalled, msg)
	}
}

func TestRunPRActionForkCheckoutFallsBackToGh(t *testing.T) {
	var gotNumber int
	client := &fakeGH{checkout: func(number int) error {
		gotNumber = number
		return nil
	}}
	checkoutHead := func(branch string) error {
		t.Fatalf("fork checkout must not run plain git (branch %q)", branch)
		return nil
	}
	pr := gh.PR{Number: 3, HeadRefName: "feature", IsCrossRepository: true}
	msg := runPRAction(client, checkoutHead, checkoutPR, pr, gh.MergeCommit)().(prActionDone)
	if gotNumber != 3 || msg.action != checkoutPR || msg.err != nil {
		t.Fatalf("Checkout(%d), prActionDone = %+v", gotNumber, msg)
	}
	// A PR without a known head branch cannot be checked out by name either.
	gotNumber = 0
	msg = runPRAction(client, checkoutHead, checkoutPR, gh.PR{Number: 4}, gh.MergeCommit)().(prActionDone)
	if gotNumber != 4 || msg.err != nil {
		t.Fatalf("Checkout(%d), prActionDone = %+v", gotNumber, msg)
	}
}

func TestRunPRStatusAppliesOptimisticStateOnSuccess(t *testing.T) {
	var gotPR gh.PR
	var gotTarget string
	client := &fakeGH{setStatus: func(pr gh.PR, target string) error {
		gotPR, gotTarget = pr, target
		return nil
	}}
	pr := gh.PR{Number: 5, State: "OPEN"}
	msg := runPRStatus(client, pr, "draft")().(prStatusDone)
	if gotPR.Number != 5 || gotTarget != "draft" {
		t.Fatalf("SetStatus(%+v, %q)", gotPR, gotTarget)
	}
	if msg.err != nil || msg.target != "draft" || msg.pr.State != "OPEN" || !msg.pr.IsDraft {
		t.Fatalf("prStatusDone = %+v; want the optimistic draft state", msg)
	}
}

func TestRunPRStatusKeepsStateOnError(t *testing.T) {
	boom := errors.New("status refused")
	client := &fakeGH{setStatus: func(gh.PR, string) error { return boom }}
	pr := gh.PR{Number: 5, State: "OPEN"}
	msg := runPRStatus(client, pr, "closed")().(prStatusDone)
	if !errors.Is(msg.err, boom) || msg.pr.State != "OPEN" {
		t.Fatalf("prStatusDone = %+v; a failed status change must not mutate the PR", msg)
	}
}

func TestPostRemoteCommentPostsNewComment(t *testing.T) {
	var gotNumber int
	var gotBody string
	client := &fakeGH{postIssueComment: func(number int, body string) error {
		gotNumber, gotBody = number, body
		return nil
	}}
	msg := postRemoteComment(client, 9, "hello", 0, 4)().(remoteCommentDone)
	if gotNumber != 9 || gotBody != "hello" {
		t.Fatalf("PostIssueComment(%d, %q)", gotNumber, gotBody)
	}
	if msg.generation != 4 || msg.edited || msg.deleted || msg.err != nil {
		t.Fatalf("remoteCommentDone = %+v", msg)
	}
}

func TestPostRemoteCommentEditsExistingComment(t *testing.T) {
	boom := errors.New("edit refused")
	var gotID int64
	var gotBody string
	client := &fakeGH{editIssueComment: func(id int64, body string) error {
		gotID, gotBody = id, body
		return boom
	}}
	msg := postRemoteComment(client, 9, "revised", 31, 4)().(remoteCommentDone)
	if gotID != 31 || gotBody != "revised" {
		t.Fatalf("EditIssueComment(%d, %q)", gotID, gotBody)
	}
	if msg.generation != 4 || !msg.edited || !errors.Is(msg.err, boom) {
		t.Fatalf("remoteCommentDone = %+v", msg)
	}
}

func TestDeleteRemoteCommentStampsGenerationAndError(t *testing.T) {
	boom := errors.New("delete refused")
	var gotID int64
	client := &fakeGH{deleteIssueComment: func(id int64) error {
		gotID = id
		return boom
	}}
	msg := deleteRemoteComment(client, 44, 6)().(remoteCommentDone)
	if gotID != 44 {
		t.Fatalf("DeleteIssueComment(%d); want 44", gotID)
	}
	if msg.generation != 6 || !msg.deleted || !errors.Is(msg.err, boom) {
		t.Fatalf("remoteCommentDone = %+v", msg)
	}
}

func TestHandleRemoteCommentDoneIgnoresStaleGeneration(t *testing.T) {
	m := testModel()
	m.targetGeneration = 5
	m.remoteCommentBusy = true
	m.status = "before"
	next, cmd := m.handleRemoteCommentDone(remoteCommentDone{generation: 4, err: errors.New("stale")})
	if next.remoteCommentBusy {
		t.Fatal("a stale completion must still clear the busy flag")
	}
	if next.status != "before" || cmd != nil {
		t.Fatalf("stale completion mutated the model: status=%q cmd=%v", next.status, cmd)
	}
	next, _ = m.handleRemoteCommentDone(remoteCommentDone{generation: 5, err: errors.New("boom")})
	if next.status != "comment: boom" {
		t.Fatalf("current-generation error status = %q", next.status)
	}
}

func TestPollCIStampsGeneration(t *testing.T) {
	var gotNumber int
	client := &fakeGH{findChecks: func(number int) (gh.PR, error) {
		gotNumber = number
		return gh.PR{Number: number, HeadRefOID: "head"}, nil
	}}
	msg := pollCI(client, 42, 7)().(ciPolled)
	if gotNumber != 7 {
		t.Fatalf("FindChecks(%d); want 7", gotNumber)
	}
	if msg.generation != 42 || msg.pr.Number != 7 {
		t.Fatalf("ciPolled = %+v", msg)
	}

	boom := errors.New("checks unavailable")
	client = &fakeGH{findChecks: func(int) (gh.PR, error) { return gh.PR{}, boom }}
	msg = pollCI(client, 43, 7)().(ciPolled)
	if msg.generation != 43 || !errors.Is(msg.err, boom) {
		t.Fatalf("ciPolled = %+v", msg)
	}
}

func TestCIPollDelayBackoffCapsAtTwoMinutes(t *testing.T) {
	for failures, want := range map[int]time.Duration{0: 15 * time.Second, 1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute, 8: 2 * time.Minute} {
		if got := ciPollDelay(failures); got != want {
			t.Fatalf("ciPollDelay(%d) = %s, want %s", failures, got, want)
		}
	}
}

// scheduleCIPoll itself wraps tea.Tick, whose command blocks for the full
// delay; the tick payload is exercised through the ciPollTick route instead.
func TestPollingTimersCancelOnRescheduleAndClose(t *testing.T) {
	m := testModel()
	m.screen, m.remote, m.pollTimers = detailScreen, false, &pollTimers{}
	first := m.nextLocalPoll()
	second := m.nextLocalPoll()
	if msg := first(); msg != nil {
		t.Fatalf("superseded timer returned %#v", msg)
	}
	m.close()
	if msg := second(); msg != nil {
		t.Fatalf("closed timer returned %#v", msg)
	}
	// Cancellation remains safe when copied Models share the timer registry.
	m.close()
}

func TestCIPollTickDispatchesPollForCurrentPR(t *testing.T) {
	var gotNumber int
	m := testModel()
	m.client = &fakeGH{findChecks: func(number int) (gh.PR, error) {
		gotNumber = number
		return gh.PR{Number: number}, nil
	}}
	m.screen = detailScreen
	m.targetGeneration = 8
	m.localHeadOID, m.revisionRelation = "head", git.RevisionSynced
	m.cache.PR = &gh.PR{Number: 21, State: "OPEN", HeadRefOID: "head", CheckRollupState: "PENDING"}
	updated, cmd := m.Update(ciPollTick{generation: 8, number: 21})
	if cmd == nil {
		t.Fatal("a live tick must dispatch a poll")
	}
	msg, ok := cmd().(ciPolled)
	if !ok || gotNumber != 21 || msg.generation != 8 || msg.pr.Number != 21 {
		t.Fatalf("tick poll = %+v (FindChecks(%d))", msg, gotNumber)
	}
	if _, cmd := updated.(Model).Update(ciPollTick{generation: 7, number: 21}); cmd != nil {
		t.Fatal("a stale-generation tick must die instead of polling")
	}
}

func TestFetchGitHubUsesLocalMetadataAndPassesCachedDetail(t *testing.T) {
	var got gh.PRDetail
	remoteCalled := false
	client := &fakeGH{
		loadLocalPRDetail: func(number int, prev gh.PRDetail) gh.PRDetail {
			got = prev
			return gh.PRDetail{}
		},
		loadPRDetail: func(number int, prev gh.PRDetail) gh.PRDetail {
			remoteCalled = true
			return gh.PRDetail{}
		},
	}
	m := testModel()
	m.client = client
	pr := gh.PR{Number: 15}
	m.cache.PR = &pr
	m.cache.Comments = []gh.Comment{{ID: 7, UpdatedAt: "2026-08-01T10:00:00Z"}}
	batch := fetchGitHub(m.client, "feature", 15, 3, m.cachedDetail())().(tea.BatchMsg)
	for _, cmd := range batch {
		cmd()
	}
	if remoteCalled || got.PR.Number != 15 || len(got.Comments) != 1 || got.Comments[0].ID != 7 {
		t.Fatalf("local detail route = remote:%v cached:%#v", remoteCalled, got)
	}
}

func TestRemoteSnapshotErrorRequiresMatchingHead(t *testing.T) {
	if err := remoteSnapshotError("abc123456789", "abc123456789"); err != nil {
		t.Fatal(err)
	}
	if err := remoteSnapshotError("abc123456789", "def123456789"); err == nil || !strings.Contains(err.Error(), "abc1234 != def1234") {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestFetchRemotePRPropagatesClientError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake executable")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	boom := errors.New("preview unavailable")
	var gotNumber int
	client := &fakeGH{loadPRDetail: func(number int, prev gh.PRDetail) gh.PRDetail {
		gotNumber = number
		return gh.PRDetail{PreviewErr: boom}
	}}
	pr := gh.PR{Number: 15, BaseRefName: "main", HeadRefOID: "head"}
	msg := fetchRemotePR(client, pr, 9, gh.PRDetail{})().(remoteLoaded)
	if gotNumber != 15 {
		t.Fatalf("LoadPRDetail(%d); want 15", gotNumber)
	}
	if msg.generation != 9 || !errors.Is(msg.previewErr, boom) {
		t.Fatalf("remoteLoaded = generation %d previewErr %v", msg.generation, msg.previewErr)
	}
	if msg.pr.Number != 15 {
		t.Fatalf("a failed preview must keep the original PR, got %+v", msg.pr)
	}
	if msg.refErr == nil {
		t.Fatal("a failed git fetch must surface refErr")
	}
}
