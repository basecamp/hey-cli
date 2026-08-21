//go:build unix

package cmd

import (
	"sync"
	"testing"
	"time"
)

func TestOmarchyPollLockSerializesConcurrentPolls(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var mu sync.Mutex
	inside, overlapped := 0, false
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			withOmarchyPollLock(func() {
				mu.Lock()
				inside++
				if inside > 1 {
					overlapped = true
				}
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
			})
		})
	}
	wg.Wait()
	if overlapped {
		t.Error("two polls must never diff the fingerprints at once — that is the duplicate toast")
	}
}
