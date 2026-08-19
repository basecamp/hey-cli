//go:build !windows

package attachments

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
