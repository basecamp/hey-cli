//go:build windows

package attachments

import "os"

// markDownloaded records that the file came from the internet, as a browser
// does, so Windows warns before running it. Volumes without alternate data
// streams, such as FAT drives, can't carry the mark; the file is saved anyway,
// as a browser would save it.
func markDownloaded(path string) error {
	_ = os.WriteFile(path+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0o600)
	return nil
}
