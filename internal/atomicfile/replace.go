// Package atomicfile provides the final replacement step for temp-file saves.
package atomicfile

import (
	"errors"
	"os"
	"runtime"
)

// Replace renames tmp over path. Windows cannot rename over an existing file,
// so it removes the destination and retries there.
func Replace(tmp, path string) error {
	err := os.Rename(tmp, path)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmp, path)
}
