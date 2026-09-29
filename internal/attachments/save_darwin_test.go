//go:build darwin

package attachments

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSaveSetsQuarantineOnDownloadedAttachment(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "Invoice.terminal")
	if _, err := Save(context.Background(), writeDownload("<plist/>"), destination, "/rails/active_storage/blobs/invoice", false); err != nil {
		t.Fatal(err)
	}
	value := make([]byte, 256)
	size, err := unix.Getxattr(destination, "com.apple.quarantine", value)
	if err != nil {
		t.Fatalf("saved attachment has no quarantine flag: %v", err)
	}
	if !strings.HasSuffix(string(value[:size]), ";HEY CLI;") {
		t.Errorf("quarantine flag = %q", value[:size])
	}
}
