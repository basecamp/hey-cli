//go:build !darwin && !linux && !windows

package cmd

import (
	"io/fs"
	"os"
)

func commitDraftExportDirectoryNoReplace(source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fs.ErrExist
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, destination)
}

func exchangeDraftExportDirectories(_, _ string) (bool, error) {
	return false, nil
}
