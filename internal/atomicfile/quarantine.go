package atomicfile

import (
	"fmt"
	"os"
	"time"
)

// QuarantinedError reports that a state file could not be decoded and was
// moved to Path, so the caller can carry on with empty state while the
// damaged data stays recoverable.
type QuarantinedError struct {
	Path string
	Err  error
}

func (e *QuarantinedError) Error() string {
	return fmt.Sprintf("%v; moved the unreadable file to %s", e.Err, e.Path)
}

func (e *QuarantinedError) Unwrap() error { return e.Err }

// Quarantine moves the undecodable file at path aside and returns cause as a
// *QuarantinedError naming the new location. When the move itself fails the
// file is still in the way, so the result is a plain error instead.
func Quarantine(path string, cause error) error {
	moved := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(path, moved); err != nil {
		return fmt.Errorf("%w (could not move %s aside: %w)", cause, path, err)
	}
	return &QuarantinedError{Path: moved, Err: cause}
}
