package publish

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prbody"
	"github.com/shonenm/live-pr/internal/store"
)

func setupRepo(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file.txt")
	runGit("commit", "-m", "base")
	runGit("switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file.txt")
	runGit("commit", "-m", "feature commit")

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	st, err := store.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Conclusion(), []byte("# Preview title\n\nConclusion."), 0o644); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestBuildPreview(t *testing.T) {
	setupRepo(t)
	preview, err := BuildPreview("main")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Title != "Preview title" || preview.Head != "feature" || preview.Base != "main" {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	for _, want := range []string{"feature commit", prbody.ManagedStart, prbody.ManagedEnd} {
		if !strings.Contains(preview.Body, want) {
			t.Fatalf("preview body missing %q:\n%s", want, preview.Body)
		}
	}
}

type fakeGitHub struct {
	pr          gh.PR
	missing     bool
	created     bool
	updated     bool
	draft       bool
	body        string
	createCalls int
	updateErr   error
	createErr   error
}

func (f *fakeGitHub) FindOpen(string) (gh.PR, error) {
	if f.missing && f.createCalls == 0 {
		return gh.PR{}, gh.ErrPRNotFound
	}
	return f.pr, nil
}
func (f *fakeGitHub) Update(_, _ string, bodyFile string) error {
	body, err := os.ReadFile(bodyFile)
	f.updated, f.body = true, string(body)
	if err != nil {
		return err
	}
	return f.updateErr
}
func (f *fakeGitHub) Create(_, _, _ string, bodyFile string, draft bool) (string, error) {
	body, err := os.ReadFile(bodyFile)
	f.created, f.draft, f.body = true, draft, string(body)
	f.createCalls++
	if err != nil {
		return "", err
	}
	return f.pr.URL, f.createErr
}

func TestRunCreatesAndCachesPublishedBody(t *testing.T) {
	st := setupRepo(t)
	client := &fakeGitHub{missing: true, pr: gh.PR{Number: 12, URL: "https://example/pr/12", State: "OPEN"}}
	pushed := ""
	result, err := run(Options{Base: "main", Draft: true}, client, func(branch string) error {
		pushed = branch
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || !client.created || !client.draft || pushed != "feature" || !strings.Contains(client.body, prbody.ManagedStart) {
		t.Fatalf("unexpected publish: result=%#v client=%#v pushed=%q", result, client, pushed)
	}
	cache, err := gh.LoadCache(st.GitHubCache(), "feature")
	if err != nil {
		t.Fatal(err)
	}
	if cache.PR == nil || cache.PR.Number != 12 || cache.LastPublishedManagedBodyHash == "" {
		t.Fatalf("publish cache not updated: %#v", cache)
	}
}

func TestRunUpdatesExistingPRAndCache(t *testing.T) {
	st := setupRepo(t)
	old := prbody.Render("# Old", nil)
	cache := gh.NewCache("feature")
	cache.LastPublishedManagedBodyHash = prbody.ManagedHash(old)
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		t.Fatal(err)
	}
	client := &fakeGitHub{pr: gh.PR{Number: 12, URL: "https://example/pr/12", Body: old}}
	pushed := false
	result, err := run(Options{Base: "main"}, client, func(string) error { pushed = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Created || !client.updated || client.created || !pushed || !strings.Contains(client.body, "Preview title") {
		t.Fatalf("unexpected update: result=%#v client=%#v pushed=%v", result, client, pushed)
	}
	got, err := gh.LoadCache(st.GitHubCache(), "feature")
	if err != nil || got.PR == nil || got.PR.Number != 12 || got.LastPublishedManagedBodyHash != prbody.ManagedHash(client.body) {
		t.Fatalf("update cache mismatch: cache=%#v err=%v", got, err)
	}
}

func TestRunStopsWhenPushFails(t *testing.T) {
	setupRepo(t)
	client := &fakeGitHub{missing: true, pr: gh.PR{URL: "https://example/pr/12"}}
	pushErr := errors.New("push failed")
	_, err := run(Options{Base: "main"}, client, func(string) error { return pushErr })
	if !errors.Is(err, pushErr) || client.created || client.updated {
		t.Fatalf("remote mutation occurred after push failure: err=%v client=%#v", err, client)
	}
}

func TestRunStopsOnRemoteMutationFailure(t *testing.T) {
	st := setupRepo(t)
	old := prbody.Render("# Old", nil)
	cache := gh.NewCache("feature")
	cache.LastPublishedManagedBodyHash = prbody.ManagedHash(old)
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		t.Fatal(err)
	}
	updateErr := errors.New("update failed")
	client := &fakeGitHub{pr: gh.PR{Number: 12, Body: old}, updateErr: updateErr}
	_, err := run(Options{Base: "main"}, client, func(string) error { return nil })
	if !errors.Is(err, updateErr) || !client.updated {
		t.Fatalf("expected update failure, got err=%v client=%#v", err, client)
	}
}

func TestRunStopsBeforePushOnManagedBodyConflict(t *testing.T) {
	st := setupRepo(t)
	old := prbody.Render("# Old", nil)
	cache := gh.NewCache("feature")
	cache.LastPublishedManagedBodyHash = prbody.ManagedHash(old)
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		t.Fatal(err)
	}
	remote := strings.Replace(prbody.Render("# Remote edit", nil), "Remote edit", "Edited on GitHub", 1)
	client := &fakeGitHub{pr: gh.PR{Number: 12, Body: remote}}
	pushed := false
	_, err := run(Options{Base: "main"}, client, func(string) error { pushed = true; return nil })
	if !errors.Is(err, prbody.ErrManagedConflict) || pushed || client.updated {
		t.Fatalf("conflict was not fail-closed: err=%v pushed=%v updated=%v", err, pushed, client.updated)
	}
}
