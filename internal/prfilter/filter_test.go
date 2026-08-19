package prfilter

import (
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
)

func TestPRFilterSupportsGitHubTermsAndFreeText(t *testing.T) {
	failed := []gh.PRCheck{{Status: "COMPLETED", Conclusion: "FAILURE"}}
	pr := gh.PR{Number: 7, Title: "Fix Login", State: "OPEN", HeadRefName: "auth/fix", Author: gh.PRUser{Login: "alice"}, Assignees: []gh.PRUser{{Login: "me"}}, ReviewRequests: []gh.PRUser{{Login: "reviewer"}}, Labels: []gh.PRLabel{{Name: "bug"}}, Checks: failed, Mergeable: "CONFLICTING", IsDraft: false}
	for _, query := range []string{"login", "#7", "is:open", "state:open", "author:alice", "assignee:@me", "review-requested:reviewer", "label:bug", "draft:false", "ci:failed", "merge:conflicting", "label:bug ci:failed auth"} {
		if !Matches(pr, query, "me") {
			t.Fatalf("filter %q did not match", query)
		}
	}
	teamRequest := pr
	teamRequest.ReviewRequests = nil
	teamRequest.ViewerReviewRequested = true
	if !Matches(teamRequest, "review-requested:@me", "me") {
		t.Fatal("team review request did not match @me")
	}
	for _, query := range []string{"is:closed", "state:closed", "author:bob", "assignee:bob", "review-requested:@me", "label:docs", "draft:true", "ci:passed"} {
		if Matches(pr, query, "me") {
			t.Fatalf("filter %q unexpectedly matched", query)
		}
	}
}

func TestPRFilterOrGroups(t *testing.T) {
	assigned := gh.PR{Number: 1, State: "OPEN", Assignees: []gh.PRUser{{Login: "me"}}}
	requested := gh.PR{Number: 2, State: "OPEN", ViewerReviewRequested: true}
	neither := gh.PR{Number: 3, State: "OPEN", Author: gh.PRUser{Login: "someone"}}

	const needsMe = "(assignee:@me OR review-requested:@me)"
	for _, tc := range []struct {
		pr   gh.PR
		want bool
	}{{assigned, true}, {requested, true}, {neither, false}} {
		if got := Matches(tc.pr, needsMe, "me"); got != tc.want {
			t.Fatalf("#%d needs-me = %v, want %v", tc.pr.Number, got, tc.want)
		}
	}

	// Groups AND with the rest of the query.
	if Matches(assigned, needsMe+" is:closed", "me") {
		t.Fatal("group ignored the trailing is:closed term")
	}
	if !Matches(assigned, "is:open "+needsMe, "me") {
		t.Fatal("leading term broke the group")
	}
	// An unclosed group still evaluates its alternatives.
	if !Matches(requested, "(assignee:@me OR review-requested:@me", "me") {
		t.Fatal("unclosed group rejected a matching PR")
	}
	// Plain queries keep working, free text included.
	if !Matches(neither, "someone", "me") || Matches(neither, "nobody", "me") {
		t.Fatal("free-text matching regressed")
	}
}

func TestSplitSeparatesServerAndLocalTerms(t *testing.T) {
	server, local := Split("is:closed author:me ci:failed merge:conflicting")
	if server != "author:me" || local != "ci:failed merge:conflicting" {
		t.Fatalf("split filter = server:%q local:%q", server, local)
	}
	server, local = Split("(review-requested:@me OR assignee:@me OR author:@me) label:bug")
	if server != "label:bug" || local != "(review-requested:@me OR assignee:@me OR author:@me)" {
		t.Fatalf("group split = server:%q local:%q", server, local)
	}
	// An unclosed group still lands on the local side rather than leaking.
	if server, local := Split("(a OR b"); server != "" || local != "(a OR b" {
		t.Fatalf("unclosed group = server:%q local:%q", server, local)
	}
}
