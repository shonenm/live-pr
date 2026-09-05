package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

// outboxTestModel builds a detail-screen model whose PR has an isolated
// outbox under a temp XDG state root.
func outboxTestModel(t *testing.T, number int) Model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.screen = detailScreen
	pr := gh.PR{Number: number, HeadRefName: "feature/x", State: "OPEN"}
	m.cache.PR = &pr
	m.refreshOutbox()
	return m
}

func queueTestComment(t *testing.T, m *Model, body string) store.OutboxEntry {
	t.Helper()
	entries, err := store.AppendOutbox(m.outboxPath, store.OutboxEntry{PR: m.cache.PR.Number, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	m.outbox = entries
	m.detailView.invalidateConversation()
	return entries[len(entries)-1]
}

func TestHandleRemoteCommentDoneQueuesNetworkFailure(t *testing.T) {
	m := outboxTestModel(t, 7)
	next, _ := m.Update(remoteCommentDone{
		generation: 0,
		number:     7,
		body:       "hello from the train",
		err:        errors.New("gh api: dial tcp: connection refused"),
	})
	model := next.(Model)
	if len(model.outbox) != 1 || model.outbox[0].Body != "hello from the train" || model.outbox[0].PR != 7 {
		t.Fatalf("outbox = %+v; want the failed comment queued", model.outbox)
	}
	entries, err := store.LoadOutbox(store.CommentOutbox(m.root, 7))
	if err != nil || len(entries) != 1 {
		t.Fatalf("outbox file = %+v, %v; want the entry persisted", entries, err)
	}
	if !strings.Contains(model.notice, "queued") {
		t.Fatalf("notice = %q; want it to say the comment was queued", model.notice)
	}
	if model.status != "" {
		t.Fatalf("status = %q; a queued comment is not an error", model.status)
	}
}

func TestHandleRemoteCommentDonePersistsStaleFailureForSourcePR(t *testing.T) {
	t.Run("refresh generation", func(t *testing.T) {
		m := outboxTestModel(t, 7)
		generation := m.targetGeneration
		m.targetGeneration++
		next, cmd := m.Update(remoteCommentDone{
			generation: generation,
			number:     7,
			body:       "preserve after refresh",
			editID:     31,
			err:        errors.New("dial tcp: connection refused"),
		})
		model := next.(Model)
		entries, err := store.LoadOutbox(store.CommentOutbox(m.root, 7))
		if err != nil || len(entries) != 1 || entries[0].Body != "preserve after refresh" || entries[0].CommentID != 31 {
			t.Fatalf("source outbox = %+v, %v", entries, err)
		}
		if cmd != nil || len(model.outbox) != 0 || model.notice != "" {
			t.Fatalf("stale completion updated display: outbox=%+v notice=%q cmd=%v", model.outbox, model.notice, cmd)
		}
	})

	t.Run("PR switch", func(t *testing.T) {
		m := outboxTestModel(t, 7)
		generation := m.targetGeneration
		sourcePath := store.CommentOutbox(m.root, 7)
		other := gh.PR{Number: 8, HeadRefName: "feature/y", HeadRefOID: "bbb", State: "OPEN"}
		_ = m.openRemote(other)
		next, cmd := m.Update(remoteCommentDone{
			generation: generation,
			number:     7,
			body:       "belongs to seven",
			err:        errors.New("dial tcp: connection refused"),
		})
		model := next.(Model)
		source, err := store.LoadOutbox(sourcePath)
		if err != nil || len(source) != 1 || source[0].PR != 7 || source[0].Body != "belongs to seven" {
			t.Fatalf("source outbox = %+v, %v", source, err)
		}
		otherEntries, err := store.LoadOutbox(store.CommentOutbox(m.root, 8))
		if err != nil || len(otherEntries) != 0 {
			t.Fatalf("current PR outbox was contaminated: %+v, %v", otherEntries, err)
		}
		if cmd != nil || model.cache.PR == nil || model.cache.PR.Number != 8 || len(model.outbox) != 0 {
			t.Fatalf("stale completion changed current PR: pr=%+v outbox=%+v cmd=%v", model.cache.PR, model.outbox, cmd)
		}
	})
}

func TestHandleRemoteCommentDoneDoesNotQueueAuthError(t *testing.T) {
	m := outboxTestModel(t, 7)
	next, _ := m.Update(remoteCommentDone{
		generation: 0,
		number:     7,
		body:       "hello",
		err:        errors.New("HTTP 401: Bad credentials"),
	})
	model := next.(Model)
	if len(model.outbox) != 0 {
		t.Fatalf("outbox = %+v; auth failures must not queue", model.outbox)
	}
	if _, err := os.Stat(store.CommentOutbox(m.root, 7)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outbox file exists for an auth failure: %v", err)
	}
	if !strings.HasPrefix(model.status, "comment: ") {
		t.Fatalf("status = %q; want the plain error path", model.status)
	}
}

func TestFlushOutboxSendsInOrderAndStopsAtFirstFailure(t *testing.T) {
	var posted []string
	var edited []int64
	client := &fakeGH{
		postIssueComment: func(number int, body string) error {
			if body == "second" {
				return errors.New("dial tcp: connection refused")
			}
			posted = append(posted, body)
			return nil
		},
		editIssueComment: func(id int64, body string) error {
			edited = append(edited, id)
			return nil
		},
	}
	entries := []store.OutboxEntry{
		{ID: "e1", Body: "an edit", CommentID: 31},
		{ID: "p1", Body: "first"},
		{ID: "p2", Body: "second"},
		{ID: "p3", Body: "third"},
	}
	msg := flushOutbox(client, 7, entries, 4)().(outboxFlushed)
	if len(edited) != 1 || edited[0] != 31 {
		t.Fatalf("EditIssueComment calls = %v; want the queued edit sent", edited)
	}
	if len(posted) != 1 || posted[0] != "first" {
		t.Fatalf("posted = %v; the flush must stop at the first failure", posted)
	}
	if msg.generation != 4 || msg.number != 7 || msg.err == nil {
		t.Fatalf("outboxFlushed = %+v", msg)
	}
	if len(msg.posted) != 2 || msg.posted[0] != "e1" || msg.posted[1] != "p1" {
		t.Fatalf("posted IDs = %v; want the delivered prefix only", msg.posted)
	}
}

func TestHandleOutboxFlushedRemovesPostedEntriesAndRefetches(t *testing.T) {
	m := outboxTestModel(t, 7)
	sent := queueTestComment(t, &m, "first")
	queueTestComment(t, &m, "second")
	m.outboxFlushing = true
	next, cmd := m.Update(outboxFlushed{
		generation: 0,
		number:     7,
		posted:     []string{sent.ID},
		err:        errors.New("dial tcp: connection refused"),
	})
	model := next.(Model)
	if model.outboxFlushing {
		t.Fatal("a completed flush must clear the in-flight flag")
	}
	if len(model.outbox) != 1 || model.outbox[0].Body != "second" {
		t.Fatalf("outbox = %+v; want only the undelivered entry", model.outbox)
	}
	entries, err := store.LoadOutbox(model.outboxPath)
	if err != nil || len(entries) != 1 || entries[0].Body != "second" {
		t.Fatalf("outbox file = %+v, %v", entries, err)
	}
	if cmd == nil || model.targetGeneration != m.targetGeneration+1 {
		t.Fatal("delivered entries must trigger a conversation refetch")
	}

	// Deliver the rest: the queue file disappears and the notice reports it.
	m2 := model
	m2.outboxFlushing = true
	next, cmd = m2.Update(outboxFlushed{generation: m2.targetGeneration, number: 7, posted: []string{m2.outbox[0].ID}})
	model = next.(Model)
	if len(model.outbox) != 0 || cmd == nil {
		t.Fatalf("outbox = %+v; want an empty queue and a refetch", model.outbox)
	}
	if _, err := os.Stat(model.outboxPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an emptied queue must remove its file: %v", err)
	}
	if !strings.Contains(model.notice, "Sent 1 queued comment") {
		t.Fatalf("notice = %q", model.notice)
	}
}

func TestHandleOutboxFlushedKeepsQueueWhileStillOffline(t *testing.T) {
	m := outboxTestModel(t, 7)
	queueTestComment(t, &m, "still waiting")
	m.outboxFlushing = true
	next, _ := m.Update(outboxFlushed{generation: 0, number: 7, err: errors.New("dial tcp: connection refused")})
	model := next.(Model)
	if len(model.outbox) != 1 || model.outboxFlushing {
		t.Fatalf("outbox = %+v flushing=%v; an offline flush must keep the queue", model.outbox, model.outboxFlushing)
	}
	if model.targetGeneration != m.targetGeneration {
		t.Fatal("a failed flush must not trigger a refetch")
	}
	if !strings.Contains(model.githubStatus, "queued") {
		t.Fatalf("githubStatus = %q; want the queue surfaced", model.githubStatus)
	}
}

func TestRefreshDispatchesOutboxFlush(t *testing.T) {
	m := outboxTestModel(t, 7)
	queueTestComment(t, &m, "hello")
	next, cmd := m.Update(keyPress("r"))
	model := next.(Model)
	if !model.outboxFlushing || cmd == nil {
		t.Fatalf("refresh must dispatch an outbox flush: flushing=%v cmd=%v", model.outboxFlushing, cmd)
	}
}

func TestDiscardQueuedCommentWithD(t *testing.T) {
	m := outboxTestModel(t, 7)
	queueTestComment(t, &m, "changed my mind")
	cursor := -1
	for i, item := range m.conversationItems() {
		if item.kind() == itemOutbox {
			cursor = i
		}
	}
	if cursor < 0 {
		t.Fatalf("queued comment missing from the conversation: %+v", m.conversationItems())
	}
	m.detailView.cursors[conversationTab] = cursor
	next, _ := m.Update(keyPress("d"))
	model := next.(Model)
	if _, ok := model.overlay.(outboxDiscardOverlay); !ok {
		t.Fatalf("overlay = %#v; want the discard confirm", model.overlay)
	}
	next, _ = model.Update(keyPress("y"))
	model = next.(Model)
	if model.overlay != nil || len(model.outbox) != 0 {
		t.Fatalf("overlay=%#v outbox=%+v; want the entry discarded", model.overlay, model.outbox)
	}
	if _, err := os.Stat(model.outboxPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("discard must remove the emptied queue file: %v", err)
	}
	if model.notice != "Queued comment discarded" {
		t.Fatalf("notice = %q", model.notice)
	}
}
