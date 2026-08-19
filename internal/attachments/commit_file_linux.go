//go:build linux

package attachments

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func commitFileNoReplace(source, destination string) error {
	err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
	if err == nil || (!errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EOPNOTSUPP)) {
		return err
	}
	return os.Link(source, destination)
}
