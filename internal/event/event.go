// Package event defines the timeline event model and its append-only JSONL store.
package event

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Kind categorizes a timeline event. Kinds drive the visual treatment in the TUI
// and how the event is rendered in an exported PR body.
type Kind string

const (
	Note     Kind = "note"
	Decision Kind = "decision"
	Pivot    Kind = "pivot"
	Summary  Kind = "summary"
	Commit   Kind = "commit"
)

// Event is one visible entry on the development timeline.
type Event struct {
	ID        string `json:"id,omitempty"`
	TS        string `json:"ts"`
	Kind      Kind   `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	SHA       string `json:"sha,omitempty"`
	Author    string `json:"author,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type record struct {
	Event
	Op     string `json:"op,omitempty"`
	Target string `json:"target,omitempty"`
}

const timeFormat = "2006-01-02T15:04"

// New stamps an event with the current local time.
func New(kind Kind, title, body string) Event {
	return Event{TS: time.Now().Format(timeFormat), Kind: kind, Title: title, Body: body}
}

// Create appends a new event and returns it with its stable ID.
func Create(path string, ev Event) (Event, error) {
	if ev.ID == "" {
		id, err := newID()
		if err != nil {
			return Event{}, err
		}
		ev.ID = id
	}
	if err := appendRecord(path, record{Event: ev}); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// Append preserves the original append-only API for commit and hook writers.
func Append(path string, ev Event) error {
	_, err := Create(path, ev)
	return err
}

// Update appends a replacement for an existing event. The original timestamp
// and ID remain stable while the latest record wins.
func Update(path, id string, replacement Event) (Event, error) {
	events, err := Load(path)
	if err != nil {
		return Event{}, err
	}
	current, ok := find(events, id)
	if !ok {
		return Event{}, fmt.Errorf("timeline event %q not found", id)
	}
	replacement.ID = id
	replacement.TS = current.TS
	if replacement.Author == "" {
		replacement.Author = current.Author
	}
	replacement.UpdatedAt = time.Now().Format(timeFormat)
	toWrite := replacement
	toWrite.ID = ""
	if err := appendRecord(path, record{Event: toWrite, Op: "update", Target: id}); err != nil {
		return Event{}, err
	}
	return replacement, nil
}

// Delete appends a tombstone for an existing event.
func Delete(path, id string) error {
	events, err := Load(path)
	if err != nil {
		return err
	}
	if _, ok := find(events, id); !ok {
		return fmt.Errorf("timeline event %q not found", id)
	}
	return appendRecord(path, record{Event: Event{UpdatedAt: time.Now().Format(timeFormat)}, Op: "delete", Target: id})
}

// Load materializes visible events from the append-only record stream. Legacy
// records without IDs receive deterministic IDs so they can also be edited.
func Load(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []Event
	positions := map[string]int{}
	deleted := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNumber := 0
	for sc.Scan() {
		lineNumber++
		line := append([]byte(nil), sc.Bytes()...)
		if len(line) == 0 {
			continue
		}
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, err
		}
		switch rec.Op {
		case "":
			if rec.ID == "" {
				rec.ID = legacyID(lineNumber, line)
			}
			if _, exists := positions[rec.ID]; exists {
				return nil, fmt.Errorf("duplicate timeline event id %q", rec.ID)
			}
			positions[rec.ID] = len(events)
			events = append(events, rec.Event)
		case "update":
			i, ok := positions[rec.Target]
			if !ok {
				return nil, fmt.Errorf("update targets unknown timeline event %q", rec.Target)
			}
			if deleted[rec.Target] {
				continue
			}
			original := events[i]
			rec.ID, rec.TS = rec.Target, original.TS
			if rec.Author == "" {
				rec.Author = original.Author
			}
			events[i] = rec.Event
		case "delete":
			if _, ok := positions[rec.Target]; !ok {
				return nil, fmt.Errorf("delete targets unknown timeline event %q", rec.Target)
			}
			deleted[rec.Target] = true
		default:
			return nil, fmt.Errorf("unsupported timeline operation %q", rec.Op)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	visible := events[:0]
	for _, ev := range events {
		if !deleted[ev.ID] {
			visible = append(visible, ev)
		}
	}
	return visible, nil
}

func appendRecord(path string, rec record) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate timeline event id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func legacyID(lineNumber int, line []byte) string {
	hash := sha256.Sum256(append([]byte(fmt.Sprintf("%d:", lineNumber)), line...))
	return hex.EncodeToString(hash[:6])
}

func find(events []Event, id string) (Event, bool) {
	for _, ev := range events {
		if ev.ID == id {
			return ev, true
		}
	}
	return Event{}, false
}
