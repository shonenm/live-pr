package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
)

// The head lookup ignores fork PRs, so refreshing a checked-out fork PR must
// go by number: losing the pin would let publish open a duplicate PR.
func TestLoadStatusRefreshKeepsCheckedOutForkPR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gh shim is a POSIX shell script")
	}
	st := cmdRepo(t)
	const fork = `{"number":40,"state":"OPEN","title":"refreshed","headRefName":"main","isCrossRepository":true}`
	binDir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1 $2\" = \"pr view\" ]; then echo '" + fork + "'; else echo '[" + fork + "]'; fi\n"
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cache := gh.NewCache(st.Branch)
	cache.PR = &gh.PR{Number: 40, State: "OPEN", HeadRefName: st.Branch, IsCrossRepository: true}
	cache.ExplicitCheckout = true
	if err := gh.SaveCache(st.GitHubCache(), cache); err != nil {
		t.Fatal(err)
	}

	status, err := loadStatus(true)
	if err != nil {
		t.Fatal(err)
	}
	if status.PR == nil || status.PR.Number != 40 || status.PR.Title != "refreshed" {
		t.Fatalf("status PR = %#v", status.PR)
	}
	saved, err := gh.LoadCache(st.GitHubCache(), st.Branch)
	if err != nil || saved.PR == nil || saved.PR.Number != 40 || !saved.PR.IsCrossRepository || !saved.ExplicitCheckout {
		t.Fatalf("fork pin not kept: %#v err=%v", saved, err)
	}
}
