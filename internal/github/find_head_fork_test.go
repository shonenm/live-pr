package github

import (
	"errors"
	"strings"
	"testing"
)

// gh pr list --head matches the branch name alone, so the response can carry
// fork PRs opened from a same-named branch.
func TestHeadLookupsIgnoreForkPRs(t *testing.T) {
	const fork = `{"number":40,"state":"OPEN","headRefName":"feature","isCrossRepository":true}`
	list := func(open, all string) Client {
		return Client{run: func(args ...string) ([]byte, error) {
			if strings.Contains(strings.Join(args, " "), "--state open") {
				return []byte("[" + open + "]"), nil
			}
			return []byte("[" + all + "]"), nil
		}}
	}

	t.Run("only a fork PR", func(t *testing.T) {
		client := list(fork, fork)
		if pr, err := client.FindOpen("feature"); !errors.Is(err, ErrPRNotFound) {
			t.Fatalf("FindOpen = %#v, %v", pr, err)
		}
		if pr, err := client.FindForHead("feature"); !errors.Is(err, ErrPRNotFound) {
			t.Fatalf("FindForHead = %#v, %v", pr, err)
		}
		if prs, err := client.FindAllForHead("feature"); !errors.Is(err, ErrPRNotFound) {
			t.Fatalf("FindAllForHead = %#v, %v", prs, err)
		}
	})

	t.Run("newer fork PR next to the branch's own", func(t *testing.T) {
		own := `{"number":12,"state":"OPEN","headRefName":"feature"}`
		client := list(own+","+fork, own+","+fork)
		if pr, err := client.FindOpen("feature"); err != nil || pr.Number != 12 {
			t.Fatalf("FindOpen = %#v, %v", pr, err)
		}
		if pr, err := client.FindForHead("feature"); err != nil || pr.Number != 12 {
			t.Fatalf("FindForHead = %#v, %v", pr, err)
		}
	})

	t.Run("open fork PR does not hide the branch's merged PR", func(t *testing.T) {
		merged := `{"number":12,"state":"MERGED","headRefName":"feature"}`
		client := list(fork, merged+","+fork)
		if pr, err := client.FindForHead("feature"); err != nil || pr.Number != 12 {
			t.Fatalf("FindForHead = %#v, %v", pr, err)
		}
	})
}
