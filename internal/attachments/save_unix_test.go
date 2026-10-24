//go:build unix

package attachments

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSavedAttachmentIsNeverExecutable(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "install.sh")
	if _, err := Save(context.Background(), writeDownload("#!/bin/sh\n"), destination, "/rails/active_storage/blobs/install", false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Errorf("saved attachment mode = %v, want no execute bits", info.Mode().Perm())
	}
}
