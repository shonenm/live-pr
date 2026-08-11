package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/shonenm/live-pr/internal/event"
	gh "github.com/shonenm/live-pr/internal/github"
)

func BenchmarkConversationItems(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("items=%d", size), func(b *testing.B) {
			m := testModel()
			m.events = make([]event.Event, size)
			for i := range m.events {
				m.events[i] = event.Event{TS: time.Unix(int64(i), 0).UTC().Format(time.RFC3339), Kind: event.Note, Title: fmt.Sprintf("event %d", i)}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
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

func BenchmarkPRListRows(b *testing.B) {
	for _, size := range []int{25, 250, 2500} {
		b.Run(fmt.Sprintf("rows=%d", size), func(b *testing.B) {
			m := benchmarkPRModel(size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, _ = m.buildPRListRows()
			}
		})
	}
}
