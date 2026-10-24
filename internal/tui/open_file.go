package tui

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
	attachmentfiles "github.com/basecamp/hey-cli/internal/attachments"
)

// sniffFileType asks xdg-mime what a file is. xdg-open asks the same question,
// through the same desktop-specific backend, and picks the application from
// the answer rather than from the file's name.
var sniffFileType = func(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "xdg-mime", "query", "filetype", path).Output() // #nosec G204 -- fixed command receives the downloaded local path as one argument
	if err != nil {
		return "", err
	}
	contentType, _, _ := strings.Cut(string(out), ";")
	return strings.TrimSpace(contentType), nil
}

// checkContentOpensInViewer refuses a file whose contents an opener that
// chooses by contents would hand to something other than a viewer.
func checkContentOpensInViewer(path string) error {
	contentType, err := sniffFileType(path)
	if err != nil || !attachmentfiles.ContentOpensInViewer(filepath.Base(path), contentType) {
		return apierr.ErrUsageHint("its contents could run a program", "press s to save it")
	}
	return nil
}

func openExternalFile(path string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
		args = []string{path}
	case "linux":
		if err := checkContentOpensInViewer(path); err != nil {
			return err
		}
		name = "xdg-open"
		args = []string{path}
	case "windows":
		name = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", path}
	default:
		return fmt.Errorf("opening attachments is not supported on %s", runtime.GOOS)
	}

	return startOpener(name, args...)
}

var startOpener = func(name string, args ...string) error {
	command := exec.CommandContext(context.Background(), name, args...) // #nosec G204 -- fixed OS launcher receives the selected local path as one argument
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}
