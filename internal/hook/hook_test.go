package hook

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/summarize"
)

type fakeSummarizer struct {
	sum   summarize.Summary
	calls int
}

func (f *fakeSummarizer) Summarize(string) (summarize.Summary, error) {
	f.calls++
	return f.sum, nil
}

func TestStopAppendsThenThrottles(t *testing.T) {
	tl := filepath.Join(t.TempDir(), "timeline.jsonl")
	fs := &fakeSummarizer{sum: summarize.Summary{Title: "did stuff", Body: "- a\n- b"}}
	base := time.Date(2026, 7, 24, 10, 0, 0, 0, time.Local)
	deps := func(now time.Time) Deps {
		return Deps{TimelinePath: tl, Summarizer: fs, Now: now, MinInterval: 10 * time.Minute}
	}

	// empty transcript: no-op, summarizer untouched
	if added, err := Stop("   ", deps(base)); err != nil || added {
		t.Fatalf("empty transcript: want false/nil, got %v/%v", added, err)
	}
	if fs.calls != 0 {
		t.Fatalf("summarizer must not be called on empty transcript")
	}

	// first real call appends
	added, err := Stop("user: hi\nassistant: done", deps(base))
	if err != nil || !added {
		t.Fatalf("first call: want appended, got %v/%v", added, err)
	}

	// within the interval: throttled
	added, _ = Stop("user: more", deps(base.Add(5*time.Minute)))
	if added {
		t.Fatalf("second call within interval should be throttled")
	}

	// past the interval: appends again
	added, _ = Stop("user: even more", deps(base.Add(11*time.Minute)))
	if !added {
		t.Fatalf("call past the interval should append")
	}

	evs, _ := event.Load(tl)
	if len(evs) != 2 {
		t.Fatalf("want 2 summary events, got %d", len(evs))
	}
	for _, e := range evs {
		if e.Kind != event.Summary || e.Title != "did stuff" {
			t.Errorf("bad summary event: %+v", e)
		}
	}
}
