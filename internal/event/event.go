// Package event defines the timeline event model and its append-only JSONL store.
package event

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

// Kind categorizes a timeline event. Kinds drive the visual treatment in the TUI
// (pills/colors) and how the event is rendered in an exported PR body.
type Kind string

const (
	Note     Kind = "note"     // freeform marker
	Decision Kind = "decision" // a choice that was made
	Pivot    Kind = "pivot"    // a change in direction
	Summary  Kind = "summary"  // a distilled session summary
	Commit   Kind = "commit"   // a git commit
)

// Event is a single entry on the development timeline.
type Event struct {
	TS    string `json:"ts"`             // timestamp, "2006-01-02T15:04"
	Kind  Kind   `json:"kind"`           // event category
	Title string `json:"title"`          // one-line headline
	Body  string `json:"body,omitempty"` // optional detail
	SHA   string `json:"sha,omitempty"`  // commit sha, for Commit events
}

// timeFormat is minute precision to match how a PR timeline reads; seconds add noise.
const timeFormat = "2006-01-02T15:04"

// New stamps an event with the current local time.
func New(kind Kind, title, body string) Event {
	return Event{TS: time.Now().Format(timeFormat), Kind: kind, Title: title, Body: body}
}

// Append writes one event as a JSON line to the timeline file (append-only,
// creating it if absent). O_APPEND keeps concurrent writers from interleaving lines.
func Append(path string, ev Event) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// Load reads every event from a timeline file in order. A missing file is not an
// error — it yields no events (an un-started branch has an empty timeline).
func Load(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // event bodies can be long
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, sc.Err()
}
