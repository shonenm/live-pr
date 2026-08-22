package prfilter

import (
	"fmt"
	"sort"

	gh "github.com/shonenm/live-pr/internal/github"
)

// StackEntry is one PR inside a stack, at its depth in the base/head chain.
type StackEntry struct {
	PR    gh.PR
	Depth int
}

// Stack groups the PRs that chain onto one root via base/head branches.
type Stack struct {
	ID      string
	Title   string
	order   int
	Entries []StackEntry
}

// SingleStacks wraps each PR in its own stack, for lists (like closed PRs)
// where base/head grouping is meaningless.
func SingleStacks(prs []gh.PR) []Stack {
	stacks := make([]Stack, len(prs))
	for i, pr := range prs {
		stacks[i] = Stack{ID: fmt.Sprintf("pr:%d", pr.Number), order: i, Entries: []StackEntry{{PR: pr}}}
	}
	return stacks
}

// BuildStacks groups PRs whose base branch is another PR's head branch into
// stacks, walking the exact base/head graph.
func BuildStacks(prs []gh.PR) []Stack {
	if len(prs) == 0 {
		return nil
	}
	head := make(map[string]int, len(prs))
	for i, pr := range prs {
		if pr.HeadRefName == "" {
			continue
		}
		if _, exists := head[pr.HeadRefName]; exists {
			head[pr.HeadRefName] = -1 // ambiguous branch names must not invent a parent
		} else {
			head[pr.HeadRefName] = i
		}
	}
	parents := make([]int, len(prs))
	children := make([][]int, len(prs))
	for i := range parents {
		parents[i] = -1
		if parent, ok := head[prs[i].BaseRefName]; ok && parent >= 0 && parent != i {
			parents[i] = parent
			children[parent] = append(children[parent], i)
		}
	}
	visited := make([]bool, len(prs))
	stacks := make([]Stack, 0, len(prs))
	addStack := func(root int) {
		if visited[root] {
			return
		}
		stack := Stack{order: root}
		var walk func(int, int)
		walk = func(index, depth int) {
			if visited[index] {
				return
			}
			visited[index] = true
			if index < stack.order {
				stack.order = index
			}
			stack.Entries = append(stack.Entries, StackEntry{PR: prs[index], Depth: depth})
			for _, child := range children[index] {
				walk(child, depth+1)
			}
		}
		walk(root, 0)
		rootPR := stack.Entries[0].PR
		stack.ID = fmt.Sprintf("pr:%d", rootPR.Number)
		if rootPR.Number == 0 {
			stack.ID = "branch:" + rootPR.HeadRefName
		}
		if rootPR.Number > 0 {
			stack.Title = fmt.Sprintf("#%d", rootPR.Number)
		} else {
			stack.Title = "Local PR"
		}
		stacks = append(stacks, stack)
	}
	for i, parent := range parents {
		if parent == -1 {
			addStack(i)
		}
	}
	for i := range prs {
		addStack(i) // cycle/duplicate safety
	}
	sort.SliceStable(stacks, func(i, j int) bool { return stacks[i].order < stacks[j].order })
	return stacks
}
