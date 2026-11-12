package github

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFindForHeadPrefersOpenFromOneListing(t *testing.T) {
	calls := 0
	client := Client{run: func(args ...string) ([]byte, error) {
		calls++
		if !strings.Contains(strings.Join(args, " "), "--state all") {
			t.Errorf("unexpected command: %v", args)
		}
		return []byte(`[{"number":14,"state":"CLOSED"},{"number":12,"state":"OPEN"},{"number":9,"state":"OPEN"}]`), nil
	}}
	pr, err := client.FindForHead("feature")
	if err != nil || pr.Number != 12 || calls != 1 {
		t.Fatalf("FindForHead = #%d err=%v calls=%d; want the newest open PR from one call", pr.Number, err, calls)
	}
}

func TestFindForHeadReportsMissingAfterOneListing(t *testing.T) {
	calls := 0
	client := Client{run: func(...string) ([]byte, error) {
		calls++
		return []byte(`[]`), nil
	}}
	if _, err := client.FindForHead("feature"); !errors.Is(err, ErrPRNotFound) || calls != 1 {
		t.Fatalf("err=%v calls=%d; want ErrPRNotFound from one call", err, calls)
	}
}

// The preview and the commit rollups are independent round trips.
func TestFindPreviewRunsItsRequestsConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	client := Client{repo: &repositoryIdentity{nameWithOwner: "acme/repo"}, run: func(args ...string) ([]byte, error) {
		started <- struct{}{}
		<-release
		if args[0] == "pr" {
			return []byte(`{"number":12}`), nil
		}
		return []byte(`{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"oid":"aaaa"}}],"pageInfo":{"hasNextPage":false}}}}}}`), nil
	}}
	done := make(chan PR, 1)
	go func() {
		pr, _ := client.FindPreview(12)
		done <- pr
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("preview and commit rollups did not start concurrently")
		}
	}
	close(release)
	select {
	case pr := <-done:
		if pr.Number != 12 || len(pr.Commits) != 1 || !pr.PreviewLoaded {
			t.Fatalf("preview = %#v", pr)
		}
	case <-time.After(time.Second):
		t.Fatal("preview did not finish")
	}
}
