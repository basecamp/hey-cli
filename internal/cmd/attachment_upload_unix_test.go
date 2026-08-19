//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cmd

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrepareAttachmentsRejectsFIFOWithoutOpeningIt(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "attachment.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareAttachments([]string{fifo}); err == nil {
		t.Fatal("FIFO attachment should be rejected")
	}
}
