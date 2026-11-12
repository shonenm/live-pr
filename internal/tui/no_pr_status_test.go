package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	gh "github.com/shonenm/live-pr/v2/internal/github"
	"github.com/shonenm/live-pr/v2/internal/store"
)

func noPRModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	st := store.ForBranch(root, "feature")
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.screen, m.refreshing = detailScreen, true
	m.root, m.currentBranch, m.defaultBranch = root, "feature", "main"
	m.timelinePath, m.cachePath = st.Timeline(), st.GitHubCache()
	m.cache = gh.NewCache("feature")
	m.githubStatus = "Local only · checking for PR…"
	return m, st
}

// A branch without a pull request is the ordinary local state. Whichever
// half of the split refresh lands last, it must not read as a fetch failure.
func TestBranchWithoutPRReportsLocalOnlyInEitherArrivalOrder(t *testing.T) {
	metadata := githubMetadataRefreshed{err: gh.ErrPRNotFound}
	conversation := githubConversationRefreshed{err: gh.ErrPRNotFound}
	for name, order := range map[string][]tea.Msg{
		"metadata first":     {metadata, conversation},
		"conversation first": {conversation, metadata},
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := noPRModel(t)
			for _, msg := range order {
				u, _ := m.Update(msg)
				m = u.(Model)
			}
			m.cancelPollTimers()
			if m.githubStatus != noPRStatus || !m.localAvailable || m.refreshing {
				t.Fatalf("status=%q localAvailable=%v refreshing=%v", m.githubStatus, m.localAvailable, m.refreshing)
			}
		})
	}
}

// The metadata result triggers a local reload, which lands after both GitHub
// halves. It used to put "checking for PR…" back with nothing left to clear it.
func TestLocalReloadKeepsRefreshedGitHubStatus(t *testing.T) {
	m, st := noPRModel(t)
	u, _ := m.Update(githubMetadataRefreshed{err: gh.ErrPRNotFound})
	m = u.(Model)
	data := localData{cache: gh.NewCache("feature"), base: "main", diffBase: "main", headRev: "HEAD"}
	u, _ = m.Update(baseResolved{generation: m.targetGeneration, local: &data, localStore: st})
	m = u.(Model)
	m.close()
	if m.githubStatus != noPRStatus || m.refreshing {
		t.Fatalf("reload replaced the status: %q refreshing=%v", m.githubStatus, m.refreshing)
	}
}

func TestConversationFailureStillReportsOffline(t *testing.T) {
	m, _ := noPRModel(t)
	u, _ := m.Update(githubConversationRefreshed{err: errors.New("network unavailable")})
	if got := u.(Model).githubStatus; got == noPRStatus {
		t.Fatalf("a fetch failure was reported as a missing PR: %q", got)
	}
}
