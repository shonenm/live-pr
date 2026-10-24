package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
)

func TestCloseCancelsGitAndGitHub(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix fake executable fixture; process cancellation is tested natively on Windows")
	}
	for _, name := range []string{"git", "gh"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "started")
			t.Setenv("LIVE_PR_CANCEL_MARKER", marker)
			script := "#!/bin/sh\nprintf ready > \"$LIVE_PR_CANCEL_MARKER\"\nexec sleep 30\n"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			m := testModel()
			m.ctx, m.cancel = context.WithCancel(t.Context())
			defer m.close()
			m.client = gh.New(m.ctx)
			done := make(chan error, 1)
			go func() {
				if name == "git" {
					_, err := git.Show(m.commandContext(), "HEAD")
					done <- err
				} else {
					_, err := m.client.FindChecks(1)
					done <- err
				}
			}()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
		started:
			for {
				if _, err := os.Stat(marker); err == nil {
					break started
				}
				select {
				case <-ticker.C:
				case err := <-done:
					t.Fatalf("exited before cancellation: %v", err)
				case <-deadline.C:
					t.Fatal("child failed to start")
				}
			}
			m.close()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation cause lost: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("child survived close")
			}
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
			if name == "git" {
				_, _ = git.Show(m.commandContext(), "HEAD")
			} else {
				_, _ = m.client.FindChecks(1)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("new child started after close")
			}
			if cmd := m.Init(); cmd != nil {
				t.Fatal("closed model scheduled startup work")
			}
			next, cmd := m.Update(ciPolled{err: context.Canceled})
			if cmd != nil || strings.Contains(next.(Model).status, "canceled") {
				t.Fatal("cancellation became a UI error")
			}
		})
	}
}

func TestModelReplacementKeepsLifetimeUntilFinalClose(t *testing.T) {
	previous := testModel()
	previous.ctx, previous.cancel = context.WithCancel(t.Context())
	defer previous.close()
	next := testModel()
	previous.closeView()
	next.advanceAsyncGenerations(previous)
	if next.commandContext().Err() != nil {
		t.Fatal("model replacement canceled the TUI")
	}
	next.close()
	if !errors.Is(previous.commandContext().Err(), context.Canceled) {
		t.Fatal("final close failed to cancel shared lifetime")
	}
}
