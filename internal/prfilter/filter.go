// Package prfilter holds the Model-independent PR-list domain logic: the
// GitHub-search-like filter query DSL, the CI health it filters on, and
// base/head stack grouping.
package prfilter

import (
	"fmt"
	"strings"

	gh "github.com/shonenm/live-pr/internal/github"
)

// Split partitions a filter query into the part GitHub's search evaluates
// server-side and the part that has to be applied locally.
func Split(query string) (server, local string) {
	serverTokens, localTokens := []string{}, []string{}
	tokens := strings.Fields(query)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if strings.HasPrefix(token, "(") {
			// GitHub's issue search understands neither OR nor grouping: it
			// matches the punctuation as free text and returns nothing, so
			// groups are evaluated locally instead.
			end := groupEnd(tokens, i)
			localTokens = append(localTokens, tokens[i:end+1]...)
			i = end
			continue
		}
		key, value, structured := strings.Cut(strings.ToLower(token), ":")
		if structured && (key == "ci" || key == "merge") {
			localTokens = append(localTokens, token)
			continue
		}
		if structured && (key == "is" || key == "state") && (value == "open" || value == "closed") {
			continue
		}
		serverTokens = append(serverTokens, token)
	}
	return strings.Join(serverTokens, " "), strings.Join(localTokens, " ")
}

// groupEnd returns the index of the token closing the parenthesized group
// that starts at i, or the last token when the group is never closed.
func groupEnd(tokens []string, i int) int {
	for ; i < len(tokens); i++ {
		if strings.HasSuffix(tokens[i], ")") {
			return i
		}
	}
	return len(tokens) - 1
}

// Matches evaluates a GitHub-search-like query against one PR.
// Parenthesized alternatives — "(assignee:@me OR review-requested:@me)" —
// match when any alternative matches; everything else is ANDed.
func Matches(pr gh.PR, query, viewer string) bool {
	matcher := prFilterMatcher{pr: pr, viewer: viewer}
	tokens := strings.Fields(strings.ToLower(query))
	for i := 0; i < len(tokens); i++ {
		if !strings.HasPrefix(tokens[i], "(") {
			if !matcher.token(tokens[i]) {
				return false
			}
			continue
		}
		alternatives, next := filterGroup(tokens, i)
		i = next
		matched := false
		for _, alternative := range alternatives {
			if matcher.token(alternative) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// filterGroup collects the alternatives of a parenthesized group starting at
// tokens[i], returning them and the index of its last token. An unclosed
// group runs to the end of the query.
func filterGroup(tokens []string, i int) ([]string, int) {
	end := groupEnd(tokens, i)
	var alternatives []string
	for _, token := range tokens[i : end+1] {
		token = strings.TrimSuffix(strings.TrimPrefix(token, "("), ")")
		if token != "" && token != "or" {
			alternatives = append(alternatives, token)
		}
	}
	return alternatives, end
}

// prFilterMatcher evaluates single filter tokens, building the free-text
// haystack only when a token needs it.
type prFilterMatcher struct {
	pr            gh.PR
	viewer        string
	haystack      string
	haystackBuilt bool
}

func (f *prFilterMatcher) token(token string) bool {
	pr, viewer := f.pr, f.viewer
	key, value, structured := strings.Cut(token, ":")
	if structured {
		me := value == "@me"
		if me {
			if viewer == "" {
				return false
			}
			value = strings.ToLower(viewer)
		}
		switch key {
		case "is", "state":
			switch value {
			case "open":
				return strings.EqualFold(pr.State, "OPEN")
			case "closed":
				// Match GitHub search semantics: is:closed covers merged
				// PRs too, like matchesListState's closed bucket.
				return strings.EqualFold(pr.State, "CLOSED") || strings.EqualFold(pr.State, "MERGED")
			case "draft":
				return key == "is" && pr.IsDraft
			case "pr":
				return key == "is"
			default:
				return false
			}
		case "author":
			return strings.EqualFold(pr.Author.Login, value)
		case "assignee":
			return hasLogin(pr.Assignees, value)
		case "review-requested":
			return me && pr.ViewerReviewRequested || hasLogin(pr.ReviewRequests, value)
		case "label":
			for _, label := range pr.Labels {
				if strings.EqualFold(label.Name, value) {
					return true
				}
			}
			return false
		case "draft":
			return (value == "true") == pr.IsDraft
		case "ci":
			return CIHealth(pr) == value
		case "merge":
			conflicting := pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY"
			return value == "conflicting" && conflicting
		}
	}
	if !f.haystackBuilt {
		f.haystack = strings.ToLower(fmt.Sprintf("#%d %s %s %s %s", pr.Number, pr.Title, pr.HeadRefName, pr.BaseRefName, pr.Author.Login))
		for _, label := range pr.Labels {
			f.haystack += " " + strings.ToLower(label.Name)
		}
		f.haystackBuilt = true
	}
	return strings.Contains(f.haystack, token)
}

func hasLogin(users []gh.PRUser, login string) bool {
	if login == "" {
		return false
	}
	for _, user := range users {
		if strings.EqualFold(user.Login, login) {
			return true
		}
	}
	return false
}
