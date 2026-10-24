//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cmd

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestContactPhotoRejectsAFIFOWithoutOpeningIt(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "jane-doe.png")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := openContactPhoto(fifo)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO was accepted as a photo")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO blocked instead of being refused")
	}
}
