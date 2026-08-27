//go:build linux

package cmd

import (
	"errors"

	"golang.org/x/sys/unix"
)

func commitDraftExportDirectoryNoReplace(source, destination string) error {
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
}

func exchangeDraftExportDirectories(source, destination string) (bool, error) {
	err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_EXCHANGE)
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return false, nil
	}
	return err == nil, err
}
