package tui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	tea "charm.land/bubbletea/v2"

	gh "github.com/shonenm/live-pr/v2/internal/github"
)

// A branch refresh without a known PR number resolves the head once; the
// metadata and conversation halves both follow from that single lookup.
func TestFetchGitHubResolvesHeadOnce(t *testing.T) {
	lookups, conversationNumber := 0, 0
	client := &fakeGH{
		findForHead: func(string) (gh.PR, error) {
			lookups++
			return gh.PR{Number: 15}, nil
		},
		loadConversation: func(number int) gh.ConversationDetail {
			conversationNumber = number
			return gh.ConversationDetail{}
		},
	}
	batch := fetchGitHub(client, "feature", 0, 3)().(tea.BatchMsg)
	metadata := batch[0]().(githubMetadataRefreshed)
	conversation := batch[1]().(githubConversationRefreshed)
	if lookups != 1 || metadata.pr.Number != 15 || conversation.number != 15 || conversationNumber != 15 {
		t.Fatalf("lookups=%d metadata=%#v conversation=%#v", lookups, metadata, conversation)
	}

	lookups = 0
	client.findForHead = func(string) (gh.PR, error) {
		lookups++
		return gh.PR{}, gh.ErrPRNotFound
	}
	batch = fetchGitHub(client, "feature", 0, 4)().(tea.BatchMsg)
	metadata = batch[0]().(githubMetadataRefreshed)
	conversation = batch[1]().(githubConversationRefreshed)
	if lookups != 1 || !errors.Is(metadata.err, gh.ErrPRNotFound) || !errors.Is(conversation.err, gh.ErrPRNotFound) || metadata.generation != 4 || conversation.generation != 4 {
		t.Fatalf("lookups=%d metadata=%#v conversation=%#v", lookups, metadata, conversation)
	}
}

// The preview already carries statusCheckRollup, so opening a remote PR must
// not fetch the checks a second time.
func TestFetchRemotePRDoesNotPollCI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake executable")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	previews, checks := 0, 0
	client := &fakeGH{
		findPreview: func(number int) (gh.PR, error) { previews++; return gh.PR{Number: number}, nil },
		findChecks:  func(number int) (gh.PR, error) { checks++; return gh.PR{Number: number}, nil },
	}
	for _, cmd := range fetchRemotePR(client, gh.PR{Number: 15, BaseRefName: "main"}, 9)().(tea.BatchMsg) {
		if _, polled := cmd().(ciPolled); polled {
			t.Fatal("remote open dispatched a CI poll")
		}
	}
	if previews != 1 || checks != 0 {
		t.Fatalf("previews=%d checks=%d; want 1 and 0", previews, checks)
	}
}

func previewListModel() Model {
	m := testModel()
	m.screen = prListScreen
	m.prList.open = []gh.PR{{Number: 1}, {Number: 2}}
	return m
}

// Rows the cursor only passes over must not start a preview fetch: the fetch
// waits for the selection to rest.
func TestPRPreviewWaitsForTheSelectionToRest(t *testing.T) {
	m := previewListModel()
	if cmd := m.ensureSelectedPRPreview(); cmd == nil || len(m.prList.previewLoading) != 0 {
		t.Fatalf("selection did not arm the debounce: cmd=%v loading=%v", cmd, m.prList.previewLoading)
	}
	if cmd := m.ensureSelectedPRPreview(); cmd != nil {
		t.Fatal("a repeated sync armed a second timer for the same row")
	}

	// The cursor moves on before the first row's timer fires.
	m.prList.cursor = 1
	if cmd := m.ensureSelectedPRPreview(); cmd == nil {
		t.Fatal("the new selection did not arm its own timer")
	}
	m, cmd := m.handlePRPreviewDue(prPreviewDue{number: 1})
	if cmd != nil || len(m.prList.previewLoading) != 0 {
		t.Fatalf("a passed-over row was fetched: loading=%v", m.prList.previewLoading)
	}

	m, cmd = m.handlePRPreviewDue(prPreviewDue{number: 2})
	if cmd == nil || !m.prList.previewLoading[2] {
		t.Fatalf("the resting row was not fetched: loading=%v", m.prList.previewLoading)
	}
	if cmd := m.ensureSelectedPRPreview(); cmd != nil {
		t.Fatal("an in-flight preview was requested again")
	}
}

func TestPRPreviewFetchesAreBounded(t *testing.T) {
	m := previewListModel()
	m.prList.previewLoading = map[int]bool{7: true, 8: true, 9: true}
	if cmd := m.ensureSelectedPRPreview(); cmd != nil {
		t.Fatal("a preview was armed beyond the in-flight limit")
	}
	if m, cmd := m.handlePRPreviewDue(prPreviewDue{number: 1}); cmd != nil || m.prList.previewLoading[1] {
		t.Fatal("a preview was fetched beyond the in-flight limit")
	}
	// A finished fetch frees a slot for the row still selected.
	delete(m.prList.previewLoading, 7)
	if cmd := m.ensureSelectedPRPreview(); cmd == nil {
		t.Fatal("a freed slot did not re-arm the selected row")
	}
}

func TestListAvatarColorsOnlyDropRowsWhenAColorChanges(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.avatarColors = map[string]string{"alice": "#112233"}
	key := prRowCacheKey{number: 1}
	m.prList.rowCache = map[prRowCacheKey][]string{key: {"row"}}

	u, cmd := m.Update(listAvatarColorsLoaded{generation: m.prList.generation, colors: map[string]string{"alice": "#112233"}})
	m = u.(Model)
	if _, ok := m.prList.rowCache[key]; !ok || cmd != nil {
		t.Fatalf("repeated colors dropped the row cache: cmd=%v", cmd)
	}

	u, _ = m.Update(listAvatarColorsLoaded{generation: m.prList.generation, colors: map[string]string{"bob": "#445566"}})
	m = u.(Model)
	if _, ok := m.prList.rowCache[key]; ok || m.avatarColors["bob"] != "#445566" {
		t.Fatal("a new color kept stale rows")
	}
}

// A range paged through in an earlier session must not become the range every
// later refresh has to refetch page by page.
func TestSeedPRPagesBoundsTheRestoredRange(t *testing.T) {
	m := testModel()
	m.prList.pages = nil
	m.navigator = gh.NewNavigatorCache()
	for number := 1; number <= 5*gh.PRPageSize; number++ {
		m.navigator.PRs = append(m.navigator.PRs, gh.PR{Number: number, State: "OPEN"})
	}
	m.navigator.SetView(m.viewName(allPRsView), m.navigator.PRs, len(m.navigator.PRs), "")
	m.seedPRPages()
	page := m.prList.pages[prPageKey(allPRsView, m.standardPRListState(allPRsView), "")]
	if len(page.prs) != restoredPRRange || page.prs[0].Number != 1 || page.total != 5*gh.PRPageSize {
		t.Fatalf("restored %d rows (total %d); want %d", len(page.prs), page.total, restoredPRRange)
	}
}
