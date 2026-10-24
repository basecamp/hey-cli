//go:build !darwin && !windows

package attachments

// markDownloaded does nothing where the system keeps no record of where a
// file came from. Saved attachments are still never executable.
func markDownloaded(string) error {
	return nil
}
