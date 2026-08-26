//go:build linux

package cmd

import "golang.org/x/sys/unix"

func commitDraftExportDirectoryNoReplace(source, destination string) error {
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
}
