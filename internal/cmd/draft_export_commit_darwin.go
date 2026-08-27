//go:build darwin

package cmd

import (
	"errors"

	"golang.org/x/sys/unix"
)

func commitDraftExportDirectoryNoReplace(source, destination string) error {
	return unix.RenamexNp(source, destination, unix.RENAME_EXCL)
}

func exchangeDraftExportDirectories(source, destination string) (bool, error) {
	err := unix.RenamexNp(source, destination, unix.RENAME_SWAP)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return false, nil
	}
	return err == nil, err
}
