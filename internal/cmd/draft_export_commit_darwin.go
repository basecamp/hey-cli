//go:build darwin

package cmd

import "golang.org/x/sys/unix"

func commitDraftExportDirectoryNoReplace(source, destination string) error {
	return unix.RenamexNp(source, destination, unix.RENAME_EXCL)
}
