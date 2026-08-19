//go:build !windows

package cmd

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
