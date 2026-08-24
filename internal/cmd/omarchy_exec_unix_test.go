//go:build unix

package cmd

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestRunOmarchyCommandTimeoutKillsTheProcessGroup(t *testing.T) {
	orig := omarchyCommandTimeout
	omarchyCommandTimeout = 500 * time.Millisecond
	t.Cleanup(func() { omarchyCommandTimeout = orig })

	start := time.Now()
	_, err := runOmarchyCommand("", "sh", "-c", "sleep 60 & wait")
	if err == nil {
		t.Fatal("a timed-out command must error")
	}
	// A killed direct child whose grandchild survives would hold the pipes
	// until WaitDelay; a leaked group would hold them for the full sleep.
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the run took %v — the process group was not killed", elapsed)
	}
}

func TestRunOmarchyCommandInterruptCancelsTheRun(t *testing.T) {
	// The child sits in its own process group, outside the terminal's
	// foreground group — Ctrl-C must still stop a mutating install.
	done := make(chan error, 1)
	go func() {
		_, err := runOmarchyCommand("", "sh", "-c", "sleep 60 & wait")
		done <- err
	}()
	time.Sleep(500 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an interrupted command must error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the interrupt did not cancel the run")
	}
}

func TestRunOmarchyCommandCapsAndDrainsOutput(t *testing.T) {
	out, err := runOmarchyCommand("", "sh", "-c", "head -c 200000 /dev/zero | tr '\\0' 'a'")
	if err != nil {
		t.Fatalf("a chatty child must be drained, not blocked: %v", err)
	}
	if len(out) != omarchyOutputLimit {
		t.Fatalf("output = %d bytes, want capped at %d", len(out), omarchyOutputLimit)
	}
}
