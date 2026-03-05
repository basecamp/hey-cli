package editor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Open launches $EDITOR with a temp file containing initialContent,
// waits for the editor to close, and returns the edited content.
func Open(initialContent string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	tmpFile, err := os.CreateTemp("", "hey-*.txt")
	if err != nil {
		return "", fmt.Errorf("could not create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:gosec // G703: path from os.CreateTemp

	if _, err = tmpFile.WriteString(initialContent); err != nil {
		_ = tmpFile.Close()
		return "", fmt.Errorf("could not write temp file: %w", err)
	}
	_ = tmpFile.Close()

	cmd := exec.CommandContext(context.Background(), editor, tmpFile.Name()) //nolint:gosec // G204: intentional — launches user's $EDITOR
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err = cmd.Run(); err != nil {
		return "", fmt.Errorf("editor exited with error: %w", err)
	}

	data, err := os.ReadFile(tmpFile.Name()) //nolint:gosec // G703: path from os.CreateTemp
	if err != nil {
		return "", fmt.Errorf("could not read edited file: %w", err)
	}

	return string(data), nil
}
