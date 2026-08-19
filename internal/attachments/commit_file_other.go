//go:build !darwin && !linux && !windows

package attachments

import "os"

func commitFileNoReplace(source, destination string) error {
	return os.Link(source, destination)
}
