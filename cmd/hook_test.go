package cmd

import (
	"testing"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/summarize"
)

func TestSummarizerDefaultsToClaude(t *testing.T) {
	s := summarizer(config.Config{SummarizeModel: "haiku"})
	claude, ok := s.(summarize.Claude)
	if !ok {
		t.Fatalf("summarizer = %T, want summarize.Claude", s)
	}
	if claude.Model != "haiku" {
		t.Fatalf("model = %q, want haiku", claude.Model)
	}
}

func TestSummarizerUsesConfiguredCommand(t *testing.T) {
	s := summarizer(config.Config{SummarizeCommand: "my-agent summarize", SummarizeModel: "haiku"})
	cmd, ok := s.(summarize.Command)
	if !ok {
		t.Fatalf("summarizer = %T, want summarize.Command", s)
	}
	if cmd.Command != "my-agent summarize" {
		t.Fatalf("command = %q", cmd.Command)
	}
}
