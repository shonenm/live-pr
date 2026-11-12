package publish

import (
	"strings"
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
)

func TestRunRefusesCheckedOutForkPR(t *testing.T) {
	st := setupRepo(t)
	cache := gh.NewCache("feature")
	cache.PR = &gh.PR{Number: 40, State: "OPEN", HeadRefName: "feature", IsCrossRepository: true}
	cache.ExplicitCheckout = true
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		t.Fatal(err)
	}
	client := &fakeGitHub{missing: true, pr: gh.PR{Number: 41, URL: "https://example/pr/41", State: "OPEN"}}
	pushed := false
	_, err := run(Options{Base: "main"}, client, func(string) error { pushed = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "PR #40 comes from a fork") {
		t.Fatalf("expected fork refusal, got %v", err)
	}
	if pushed || client.created || client.updated {
		t.Fatalf("fork publish reached the remote: pushed=%v client=%#v", pushed, client)
	}
}
