package prfilter

import (
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
)

func TestBuildPRStacksUsesExactBaseHeadGraph(t *testing.T) {
	prs := []gh.PR{
		{Number: 3, Title: "UI", BaseRefName: "stack/api", HeadRefName: "stack/ui"},
		{Number: 2, Title: "API", BaseRefName: "stack/model", HeadRefName: "stack/api"},
		{Number: 1, Title: "Model", BaseRefName: "main", HeadRefName: "stack/model"},
		{Number: 4, Title: "Independent", BaseRefName: "main", HeadRefName: "other"},
	}
	stacks := BuildStacks(prs)
	if len(stacks) != 2 || len(stacks[0].Entries) != 3 || len(stacks[1].Entries) != 1 {
		t.Fatalf("stacks = %#v", stacks)
	}
	for i, want := range []int{1, 2, 3} {
		if stacks[0].Entries[i].PR.Number != want || stacks[0].Entries[i].Depth != i {
			t.Fatalf("chain[%d] = %#v", i, stacks[0].Entries[i])
		}
	}
	if stacks[1].Entries[0].PR.Number != 4 {
		t.Fatalf("independent stack = %#v", stacks[1])
	}
}

func TestBuildPRStacksSupportsBranchesWithoutTitleHeuristics(t *testing.T) {
	prs := []gh.PR{
		{Number: 2, Title: "same", BaseRefName: "root", HeadRefName: "child-a"},
		{Number: 3, Title: "same", BaseRefName: "root", HeadRefName: "child-b"},
		{Number: 1, Title: "different", BaseRefName: "main", HeadRefName: "root"},
		{Number: 4, Title: "same", BaseRefName: "main", HeadRefName: "unrelated"},
	}
	stacks := BuildStacks(prs)
	if len(stacks) != 2 || len(stacks[0].Entries) != 3 || stacks[0].Entries[1].Depth != 1 || stacks[0].Entries[2].Depth != 1 || len(stacks[1].Entries) != 1 {
		t.Fatalf("branched stacks = %#v", stacks)
	}
}

func TestDuplicateHeadBranchesDoNotInventStackParent(t *testing.T) {
	prs := []gh.PR{{Number: 1, HeadRefName: "same"}, {Number: 2, HeadRefName: "same"}, {Number: 3, BaseRefName: "same", HeadRefName: "child"}}
	stacks := BuildStacks(prs)
	if len(stacks) != 3 {
		t.Fatalf("ambiguous head created stack: %#v", stacks)
	}
}
