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

// viewableContentTypes are the media types, as a content sniffer names them,
// of the same documents, images and media. Some systems pick the application
// for a file from what is in it rather than from its name, so a Windows
// program called report.pdf reaches whatever runs Windows programs. Both
// xdg-mime backends' spellings are here: file(1)'s and shared-mime-info's.
var viewableContentTypes = map[string]bool{
	// Documents and text
	"application/pdf": true, "text/plain": true, "text/markdown": true, "text/x-markdown": true,
	"text/csv": true, "application/rtf": true, "text/rtf": true, "text/calendar": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
	"application/vnd.oasis.opendocument.text":                                   true,
	"application/vnd.oasis.opendocument.spreadsheet":                            true,
	"application/vnd.oasis.opendocument.presentation":                           true,
	"application/vnd.apple.pages":                                               true, "application/vnd.apple.numbers": true, "application/vnd.apple.keynote": true,

	// Images
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"image/heic": true, "image/heif": true, "image/bmp": true, "image/x-ms-bmp": true, "image/tiff": true,

	// Audio and video
	"audio/mpeg": true, "audio/mp4": true, "audio/x-m4a": true, "audio/wav": true, "audio/x-wav": true,
	"audio/vnd.wave": true, "audio/aac": true, "audio/x-aac": true, "audio/x-hx-aac-adts": true,
	"audio/flac": true, "audio/x-flac": true, "audio/ogg": true, "audio/x-vorbis+ogg": true,
	"audio/x-opus+ogg": true, "application/ogg": true,
	"video/mp4": true, "video/x-m4v": true, "video/quicktime": true, "video/webm": true,
}

// zipDocumentExtensions are documents stored as zip archives. A sniffer that
// cannot tell one from a plain archive calls it application/zip, which then
// opens in an archive manager rather than anything that runs it.
var zipDocumentExtensions = map[string]bool{
	".docx": true, ".xlsx": true, ".pptx": true,
	".odt": true, ".ods": true, ".odp": true,
	".pages": true, ".numbers": true, ".key": true,
}

// OpensInViewer reports whether an attachment called filename is a kind of
// file the system shows in a viewer, so it is safe to open directly. Other
// attachments can still be saved and opened deliberately.
func OpensInViewer(filename string) bool {
	return viewableExtensions[portableExtension(filename)]
}

// ContentOpensInViewer reports whether a file called filename whose contents
// a sniffer named contentType is still a document, image or media file, so an
// opener that chooses by contents shows it rather than runs it.
func ContentOpensInViewer(filename, contentType string) bool {
	if !OpensInViewer(filename) {
		return false
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if contentType == "application/zip" {
		return zipDocumentExtensions[portableExtension(filename)]
	}
	return viewableContentTypes[contentType]
}

func portableExtension(filename string) string {
	portable, err := PortableFilename(filename)
	if err != nil {
		return ""
	}
	return strings.ToLower(filepath.Ext(portable))
}
