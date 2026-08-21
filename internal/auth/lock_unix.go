//go:build unix

package auth

import (
	"os"
	"syscall"
)

func acquireLock(file *os.File) error {
	return syscall.Flock(descriptor(file), syscall.LOCK_EX)
}

func releaseLock(file *os.File) error {
	return syscall.Flock(descriptor(file), syscall.LOCK_UN)
}

func descriptor(file *os.File) int {
	return int(file.Fd()) //nolint:gosec // G115: a file descriptor is an int everywhere flock exists
}
