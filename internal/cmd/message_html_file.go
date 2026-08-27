package cmd

import (
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// readMessageHTMLFile reads the exact bytes from a user-selected local file. Empty
// files are allowed here because an attachment-only message or an intentional draft
// body replacement can be valid; each command applies its own empty-body rule.
func readMessageHTMLFile(path string) (string, error) {
	if path == "" {
		return "", apierr.ErrUsage("--message-html-file requires a path")
	}

	pathInfo, err := os.Stat(path)
	if err != nil {
		return "", apierr.ErrUsage(fmt.Sprintf("could not inspect HTML message file %q: %v", path, err))
	}
	if !pathInfo.Mode().IsRegular() {
		return "", apierr.ErrUsage(fmt.Sprintf("HTML message file %q is not a regular file", path))
	}

	file, err := os.Open(path) // #nosec G304 -- the user explicitly selected this local HTML message file
	if err != nil {
		return "", apierr.ErrUsage(fmt.Sprintf("could not open HTML message file %q: %v", path, err))
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return "", apierr.ErrUsage(fmt.Sprintf("could not inspect HTML message file %q: %v", path, err))
	}
	if !info.Mode().IsRegular() {
		return "", apierr.ErrUsage(fmt.Sprintf("HTML message file %q is not a regular file", path))
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return "", apierr.ErrUsage(fmt.Sprintf("could not read HTML message file %q: %v", path, err))
	}
	if !utf8.Valid(data) {
		return "", apierr.ErrUsage(fmt.Sprintf("HTML message file %q is not valid UTF-8", path))
	}
	return string(data), nil
}
