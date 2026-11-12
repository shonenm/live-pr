package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	gh "github.com/shonenm/live-pr/internal/github"
	md "github.com/shonenm/live-pr/internal/markdown"
)

// warmTestModel returns a sized detail screen whose conversation is far
// longer than the viewport. The markdown cache is process-wide, so each test
// passes its own tag to keep its bodies cold.
func warmTestModel(t *testing.T, tag string) (Model, []string) {
	t.Helper()
	m := testModel()
	m.screen, m.detailView.active = detailScreen, conversationTab
	bodies := make([]string, 60)
	for i := range bodies {
		bodies[i] = fmt.Sprintf("%s body %d with **bold** text", tag, i)
		m.cache.Comments = append(m.cache.Comments, gh.Comment{
			ID: int64(i + 1), Body: bodies[i], CreatedAt: fmt.Sprintf("2026-07-22T10:%02d:00Z", i),
		})
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return updated.(Model), bodies
}

func TestConversationDefersColdMarkdownOutsideViewport(t *testing.T) {
	m, bodies := warmTestModel(t, "defer")
	width := m.list.Width() - 8
	last := bodies[len(bodies)-1]

	if !md.Cached(bodies[0], width) {
		t.Fatal("card inside the viewport was not rendered")
	}
	if md.Cached(last, width) {
		t.Fatal("first sync rendered markdown for a card far below the viewport")
	}
	warm := m.detailView.convWarm
	if warm == nil || !warm.running {
		t.Fatal("sync did not dispatch the conversation warm-up")
	}
	content, _ := m.buildConversation()
	if !strings.Contains(ansi.Strip(content), last) {
		t.Fatal("deferred card has no plain-text stand-in")
	}

	// Re-derive the dispatched command: sync batches it with unrelated ones.
	warm.running = false
	m.detailView.conversationRenderValid = false
	m.buildConversation()
	cmd := m.warmConversation()
	if cmd == nil {
		t.Fatal("no warm-up command for the deferred cards")
	}
	msg, ok := cmd().(conversationWarmed)
	if !ok || len(msg.jobs) == 0 {
		t.Fatalf("warm-up message = %#v", msg)
	}
	if !md.Cached(last, width) {
		t.Fatal("warm-up did not render the deferred body")
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	content, _ = m.buildConversation()
	if plain := ansi.Strip(content); strings.Contains(plain, "**") {
		t.Fatal("stand-in survived the warm-up rebuild")
	}
	if warm.running || len(warm.pending) != 0 || m.warmConversation() != nil {
		t.Fatal("warm-up rebuild queued another warm-up")
	}
}

func TestConversationRendersSelectedCardBelowViewport(t *testing.T) {
	m, bodies := warmTestModel(t, "selected")
	width := m.list.Width() - 8
	target := len(m.conversationItems()) - 1
	m.detailView.cursors[conversationTab] = target
	m.buildConversation()
	if !md.Cached(bodies[len(bodies)-1], width) {
		t.Fatal("selected card was deferred")
	}
}

// A body the markdown cache refuses to keep must render inline after one
// warm-up instead of being deferred again on every rebuild.
func TestConversationWarmedBodyIsNotDeferredAgain(t *testing.T) {
	m, bodies := warmTestModel(t, "again")
	job := markdownJob{text: bodies[len(bodies)-1], width: m.list.Width() - 8}
	if md.Cached(job.text, job.width) {
		t.Fatal("setup: body is already rendered")
	}
	updated, _ := m.Update(conversationWarmed{jobs: []markdownJob{job}})
	m = updated.(Model)
	if !md.Cached(job.text, job.width) {
		t.Fatal("warmed body was deferred again")
	}
}
