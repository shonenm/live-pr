// Package atomicfile provides the final replacement step for temp-file saves.
package atomicfile

import "os"

// Replace renames tmp over path without deleting the destination on failure.
// Go uses MoveFileEx(MOVEFILE_REPLACE_EXISTING) on Windows. Retrying by removing
// path would destroy the old data when tmp is missing, locked, or unreadable.
// Like os.Rename, this does not promise power-loss durability on every filesystem.
func Replace(tmp, path string) error {
	return os.Rename(tmp, path)
}
