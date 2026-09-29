//go:build darwin

package attachments

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// markDownloaded sets the quarantine flag a browser sets on a download, so
// macOS asks before opening a file that can run a program.
func markDownloaded(path string) error {
	value := fmt.Sprintf("0081;%08x;HEY CLI;", time.Now().Unix())
	return unix.Setxattr(path, "com.apple.quarantine", []byte(value), 0)
}
