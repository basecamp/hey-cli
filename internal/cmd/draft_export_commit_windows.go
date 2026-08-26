//go:build windows

package cmd

import "golang.org/x/sys/windows"

func commitDraftExportDirectoryNoReplace(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to)
}
