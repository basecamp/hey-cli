//go:build darwin

package attachments

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func commitFileNoReplace(source, destination string) error {
	err := unix.RenamexNp(source, destination, unix.RENAME_EXCL)
	if err == nil || (!errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EOPNOTSUPP)) {
		return err
	}
	return os.Link(source, destination)
}
