package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

func openExternalFile(path string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
		args = []string{path}
	case "linux":
		name = "xdg-open"
		args = []string{path}
	case "windows":
		name = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", path}
	default:
		return fmt.Errorf("opening attachments is not supported on %s", runtime.GOOS)
	}

	command := exec.CommandContext(context.Background(), name, args...) // #nosec G204 -- fixed OS launcher receives the selected local path as one argument
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}
