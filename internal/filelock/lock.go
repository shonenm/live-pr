// Package filelock serializes file transactions across processes.
package filelock

import "os"

// Acquire holds a sidecar lock until release. The sidecar must not be removed:
// unlinking it would let another process lock a different inode at the same path.
func Acquire(path string) (release func() error, err error) {
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	unlock, err := lock(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() error {
		unlockErr := unlock()
		closeErr := file.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}
