//go:build !darwin && !linux && !windows

package cmd

import "os"

func commitFileNoReplace(source, destination string) error {
	return os.Link(source, destination)
}
