package tui

import "sync"

// cacheWrites orders snapshots by dispatch, not by goroutine scheduling. Model
// copies and checkout replacements share this coordinator for the TUI lifetime.
// It stores only generations, never cache payloads.
type cacheWrites struct {
	mu    sync.Mutex
	next  uint64
	saved map[string]uint64
}

func (w *cacheWrites) prepare(path string, save func() error) func() error {
	w.mu.Lock()
	w.next++
	generation := w.next
	w.mu.Unlock()
	return func() error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if generation <= w.saved[path] {
			return nil
		}
		if err := save(); err != nil {
			return err
		}
		if w.saved == nil {
			w.saved = make(map[string]uint64)
		}
		w.saved[path] = generation
		return nil
	}
}
