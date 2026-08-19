package prfilter

import (
	"strings"

	gh "github.com/shonenm/live-pr/internal/github"
)

// CheckCounts buckets a PR's checks into pending, failed, and passed.
func CheckCounts(checks []gh.PRCheck) (pending, failed, passed int) {
	for _, check := range checks {
		conclusion := strings.ToUpper(check.Conclusion)
		state := strings.ToUpper(check.State)
		status := strings.ToUpper(check.Status)
		switch {
		case conclusion == "FAILURE" || conclusion == "CANCELLED" || conclusion == "TIMED_OUT" || conclusion == "ACTION_REQUIRED" || conclusion == "STARTUP_FAILURE" || conclusion == "STALE" || state == "FAILURE" || state == "ERROR":
			failed++
		case status != "COMPLETED" && conclusion == "" && state != "SUCCESS":
			pending++
		default:
			passed++
		}
	}
	return pending, failed, passed
}

// CheckHealth reduces a PR's checks to one health word and the count that
// decided it: any failure wins, then anything pending, then passes.
func CheckHealth(checks []gh.PRCheck) (string, int) {
	pending, failed, passed := CheckCounts(checks)
	switch {
	case failed > 0:
		return "failed", failed
	case pending > 0:
		return "pending", pending
	case passed > 0:
		return "passed", passed
	default:
		return "none", 0
	}
}

// CIHealth is the health word the ci: filter matches on, falling back to the
// list rollup state when the individual checks have not been loaded yet.
func CIHealth(pr gh.PR) string {
	if pr.PreviewLoaded || len(pr.Checks) > 0 {
		health, _ := CheckHealth(pr.Checks)
		return health
	}
	switch strings.ToUpper(pr.CheckRollupState) {
	case "SUCCESS":
		return "passed"
	case "FAILURE", "ERROR":
		return "failed"
	case "PENDING", "EXPECTED", "IN_PROGRESS":
		return "pending"
	default:
		return "unknown"
	}
}
