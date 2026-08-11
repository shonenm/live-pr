package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
)

func benchmarkConversationModel(size int) Model {
	m := testModel()
	m.events = make([]event.Event, size)
	for i := range m.events {
		m.events[i] = event.Event{TS: time.Unix(int64(i), 0).UTC().Format(time.RFC3339), Kind: event.Note, Title: fmt.Sprintf("event %d", i)}
	}
	return m
}

func BenchmarkConversationItems(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("items=%d/cached", size), func(b *testing.B) {
			m := benchmarkConversationModel(size)
			_ = m.conversationItems()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = m.conversationItems()
			}
		})
		b.Run(fmt.Sprintf("items=%d/derive", size), func(b *testing.B) {
			m := benchmarkConversationModel(size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.conversationDirty = true
				_ = m.conversationItems()
			}
		})
	}
}

func benchmarkPRModel(size int) Model {
	m := testModel()
	m.viewerLogin = "viewer"
	m.navigator.PRs = make([]gh.PR, size)
	for i := range m.navigator.PRs {
		m.navigator.PRs[i] = gh.PR{Number: i + 1, State: "OPEN", Title: fmt.Sprintf("PR %d", i), Assignees: []gh.PRUser{{Login: "viewer"}}}
	}
	m.prView = assignedView
	m.applyPRFilters(0)
	return m
}

func BenchmarkViewCounts(b *testing.B) {
	for _, size := range []int{25, 250, 2500} {
		b.Run(fmt.Sprintf("rows=%d", size), func(b *testing.B) {
			m := benchmarkPRModel(size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for view := assignedView; view < prViewCount; view++ {
					_ = m.viewCount(view)
				}
			}
		})
	}
}

func BenchmarkCommitRows(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("rows=%d", size), func(b *testing.B) {
			m := testModel()
			m.commits = make([]git.Commit, size)
			m.cache.PR = &gh.PR{Commits: make([]gh.PRCommit, size)}
			for i := range size {
				short := fmt.Sprintf("%07x", i)
				m.commits[i] = git.Commit{SHA: short, Subject: "commit"}
				m.cache.PR.Commits[i] = gh.PRCommit{OID: short + "000000000000000000000000000000000", CheckRollupState: "SUCCESS"}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, _ = m.buildCommits()
			}
		})
	}
}

func BenchmarkApplyPRPage(b *testing.B) {
	for _, size := range []int{25, 250, 2500} {
		b.Run(fmt.Sprintf("rows=%d", size), func(b *testing.B) {
			m := benchmarkPRModel(size)
			m.prView, m.prListState = allPRsView, openPRListState
			m.activePRPage = prPageKey(m.prView, m.prListState, "")
			m.prPages = map[string]prPageState{m.activePRPage: {prs: m.navigator.PRs, total: size, loaded: true, fresh: true}}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.applyPRFilters(0)
			}
		})
	}
}

func BenchmarkPRListRows(b *testing.B) {
	for _, size := range []int{25, 250, 2500} {
		b.Run(fmt.Sprintf("rows=%d/cached", size), func(b *testing.B) {
			m := benchmarkPRModel(size)
			_, _ = m.buildPRListRows()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, _ = m.buildPRListRows()
			}
		})
		b.Run(fmt.Sprintf("rows=%d/cold", size), func(b *testing.B) {
			m := benchmarkPRModel(size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				clear(m.prRowCache)
				_, _ = m.buildPRListRows()
			}
		})
	}
}
