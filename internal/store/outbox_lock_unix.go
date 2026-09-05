//go:build !windows

package store

import (
	"os"
	"syscall"
)

func lockOutbox(file *os.File) (func() error, error) {
	for {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != syscall.EINTR {
			if err != nil {
				return nil, err
			}
			break
		}
	}
	return func() error { return syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }, nil
}
