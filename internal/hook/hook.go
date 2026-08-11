// Package hook implements the agent-hook entrypoints that feed the timeline.
package hook

import (
	"strings"
	"time"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/summarize"
)

const tsFormat = "2006-01-02T15:04"

// Deps are the injectable dependencies of Stop (so it is testable without a
// live summarizer or clock).
type Deps struct {
	TimelinePath string
	Summarizer   summarize.Summarizer
	Now          time.Time
	MinInterval  time.Duration // suppress a new summary within this window
}

// Stop summarizes the session transcript into a summary event. It is a no-op
// when the transcript is empty, when a summary already exists within
// MinInterval, or when the summarizer returns an empty title. Returns whether an
// event was appended.
func Stop(transcript string, d Deps) (bool, error) {
	if strings.TrimSpace(transcript) == "" {
		return false, nil
	}
	evs, err := event.Load(d.TimelinePath)
	if err != nil {
		return false, err
	}
	if d.MinInterval > 0 && recentlySummarized(evs, d.Now, d.MinInterval) {
		return false, nil
	}

	sum, err := d.Summarizer.Summarize(transcript)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(sum.Title) == "" {
		return false, nil
	}

	ev := event.Event{
		TS:     d.Now.Format(tsFormat),
		Kind:   event.Summary,
		Title:  sum.Title,
		Body:   sum.Body,
		Author: "agent",
	}
	if err := event.Append(d.TimelinePath, ev); err != nil {
		return false, err
	}
	return true, nil
}

// recentlySummarized reports whether the most recent summary event is newer than
// `within` relative to now.
func recentlySummarized(evs []event.Event, now time.Time, within time.Duration) bool {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind != event.Summary {
			continue
		}
		ts, err := time.ParseInLocation(tsFormat, evs[i].TS, time.Local)
		if err != nil {
			return false
		}
		return now.Sub(ts) < within
	}
	return false
}
