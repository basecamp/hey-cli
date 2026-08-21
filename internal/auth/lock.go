package auth

import (
	"fmt"
	"os"
	"path/filepath"
)

// lock takes the lock every process sharing these credentials waits on, so that a
// load-refresh-save is one operation across `hey tui`, `hey watch` and whatever command
// is run alongside them — with token rotation the loser of a race presents a refresh
// token the server has already consumed — and so the file store's read-modify-write of
// the whole credentials map can't drop another origin's entry.
//
// It is a file lock, held by the open file, so a process that dies while holding it
// releases it and there is no stale lock to clear. The mutex is what makes the file lock
// mean anything inside this process, where a lock belongs to an open file rather than to
// the goroutine that took it.
func (s *Store) lock() (func(), error) {
	s.lockMu.Lock()

	file, err := s.openLockFile()
	if err != nil {
		s.lockMu.Unlock()
		return nil, err
	}

	if err := acquireLock(file); err != nil {
		_ = file.Close()
		s.lockMu.Unlock()
		return nil, fmt.Errorf("could not lock %s: %w", file.Name(), err)
	}

	return func() {
		_ = releaseLock(file)
		_ = file.Close()
		s.lockMu.Unlock()
	}, nil
}

func (s *Store) openLockFile() (*os.File, error) {
	if err := os.MkdirAll(s.fallbackDir, 0700); err != nil {
		return nil, err
	}

	return os.OpenFile(s.lockPath(), os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // G304: path built from the store's own config directory
}

func (s *Store) lockPath() string {
	return filepath.Join(s.fallbackDir, "credentials.lock")
}
