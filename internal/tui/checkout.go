package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

// resolveCheckoutCache distinguishes an explicit checkout pin from a cached
// branch lookup. Implicit lookups follow the open/newest matching PR.
func resolveCheckoutCache(cache gh.Cache, prs []gh.PR, branch string) gh.Cache {
	if cache.ExplicitCheckout && cache.PR != nil {
		return cache
	}
	if cache.PR != nil && !isCurrentPR(*cache.PR, branch) {
		cache = gh.NewCache(branch)
	}
	candidate := currentBranchPR(prs, branch)
	if candidate == nil {
		return cache
	}
	current := cache.PR
	if current == nil ||
		matchesListState(*candidate, openPRListState) && !matchesListState(*current, openPRListState) ||
		matchesListState(*candidate, openPRListState) == matchesListState(*current, openPRListState) && candidate.Number > current.Number {
		cache = gh.NewCache(branch)
		pr := *candidate
		cache.PR = &pr
	}
	return cache
}

type prTargetPrepared struct {
	generation uint64
	branch     string
	cache      gh.Cache
	pr         gh.PR
	err        error
}

// Check Git at selection time as well as polling: a checkout can change
// between any two timer ticks. Never hydrate the previous branch's store
// from Git belonging to a different checkout.
func preparePRTarget(root string, pr gh.PR, previous gh.Cache, known []gh.PR, generation uint64) tea.Cmd {
	previous = previous.Clone()
	known = append(append([]gh.PR(nil), known...), pr)
	return func() tea.Msg {
		msg := prTargetPrepared{generation: generation, pr: pr}
		branch, err := git.CurrentBranch()
		if err != nil {
			msg.err = err
			return msg
		}
		msg.branch = branch
		cache, err := store.ForBranch(root, branch).LoadGitHubCache()
		if err != nil {
			msg.err = err
			return msg
		}
		if !cache.ExplicitCheckout && previous.Head == branch {
			cache = previous
		}
		msg.cache = resolveCheckoutCache(cache, known, branch)
		return msg
	}
}

func (m Model) handlePRTargetPrepared(msg prTargetPrepared) (Model, tea.Cmd) {
	if msg.generation != m.targetGeneration {
		return m, nil
	}
	if msg.err != nil {
		m.refreshing = false
		m.status = "checkout: " + msg.err.Error()
		return m, m.sync()
	}
	if msg.branch != m.currentBranch {
		m.localGeneration++
		m.checkoutReloading = false
	}
	m.currentBranch, m.cache, m.checkoutCache = msg.branch, msg.cache, msg.cache
	m.remote, m.localReloading = false, false
	poll := m.nextLocalPoll()
	if !m.isCurrentTargetPR(msg.pr) {
		return m, tea.Batch(m.openRemote(msg.pr), poll)
	}
	st := store.ForBranch(m.root, msg.branch)
	return m, tea.Batch(m.startLocalLoad(st, msg.cache, &msg.pr), poll, m.startSpinner())
}
