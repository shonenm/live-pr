package embeddedterm

import (
	"fmt"
	"sync/atomic"
)

var sessionSequence atomic.Uint64

func nextSessionID() string {
	return fmt.Sprintf("live-pr-diff-%d", sessionSequence.Add(1))
}

// StateMsg asks the host to redraw after a terminal state change.
type StateMsg struct{ SessionID string }

// Environment builds the context exposed to an embedded diff command.
func Environment(reviewRange, base, head, headRev, prURL, sha string) []string {
	return []string{
		"LIVE_PR_RANGE=" + reviewRange,
		"LIVE_PR_BASE=" + base,
		"LIVE_PR_HEAD=" + head,
		"LIVE_PR_HEAD_REV=" + headRev,
		"LIVE_PR_PR_URL=" + prURL,
		"LIVE_PR_SHA=" + sha,
	}
}
