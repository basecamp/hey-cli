//go:build unix

package cmd

import (
	"os"
	"path/filepath"
	"syscall"
)

// withOmarchyPollLock serializes the fingerprint diff and toast across
// concurrent polls. The Omarchy bar is built once per monitor, so two plugin
// instances can poll at the same moment; without the lock both would read the
// same fingerprints, both find the same thread new, and toast it twice. The
// lock is advisory and best-effort: if it cannot be taken the poll still
// notifies, because a missed lock is rarer than a missed toast is bad.
func withOmarchyPollLock(fn func()) {
	path := omarchyPollLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		fn()
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: fixed path under the state dir
	if err != nil {
		fn()
		return
	}
	defer file.Close()                                                     //nolint:errcheck // read-only handle, nothing to flush
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // G115: a descriptor fits in an int
		fn()
		return
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN) //nolint:errcheck,gosec // released with the descriptor anyway; G115 as above
	fn()
}
