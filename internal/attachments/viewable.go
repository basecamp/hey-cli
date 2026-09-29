package attachments

import (
	"path/filepath"
	"strings"
)

// viewableExtensions are the kinds of file the system hands to a document,
// image or media viewer. Anything else might be run rather than shown: a
// Terminal profile, a script, a shortcut, an installer. The list is closed on
// purpose, because the kinds of file an operating system will run keep growing.
var viewableExtensions = map[string]bool{
	// Documents and text
	".pdf": true, ".txt": true, ".md": true, ".csv": true, ".rtf": true,
	".docx": true, ".xlsx": true, ".pptx": true,
	".odt": true, ".ods": true, ".odp": true,
	".pages": true, ".numbers": true, ".key": true,
	".ics": true,

	// Images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".heic": true, ".heif": true, ".bmp": true, ".tif": true, ".tiff": true,

	// Audio and video
	".mp3": true, ".m4a": true, ".wav": true, ".aac": true, ".flac": true, ".ogg": true,
	".mp4": true, ".m4v": true, ".mov": true, ".webm": true,
}

// OpensInViewer reports whether an attachment called filename is a kind of
// file the system shows in a viewer, so it is safe to open directly. Other
// attachments can still be saved and opened deliberately.
func OpensInViewer(filename string) bool {
	portable, err := PortableFilename(filename)
	if err != nil {
		return false
	}
	return viewableExtensions[strings.ToLower(filepath.Ext(portable))]
}
