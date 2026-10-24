package tui

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"unicode"
)

type urlCommandStarter func(string, ...string) error

type urlProcess interface {
	Start() error
	Wait() error
}

func openExternalURL(destination string) error {
	return openURLWith(runtime.GOOS, destination, startURLCommand)
}

func openURLWith(goos, destination string, start urlCommandStarter) error {
	name, args, err := openURLCommand(goos, destination)
	if err != nil {
		return err
	}
	return start(name, args...)
}

func openURLCommand(goos, destination string) (string, []string, error) {
	if err := validateURLDestination(destination); err != nil {
		return "", nil, err
	}

	switch goos {
	case "darwin":
		return "open", []string{destination}, nil
	case "linux":
		return "xdg-open", []string{destination}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", destination}, nil
	default:
		return "", nil, fmt.Errorf("opening URLs is not supported on %s", goos)
	}
}

func validateURLDestination(destination string) error {
	if destination == "" || strings.IndexFunc(destination, func(r rune) bool {
		return unicode.IsControl(r)
	}) >= 0 {
		return fmt.Errorf("invalid URL destination")
	}

	parsed, err := url.Parse(destination)
	if err != nil {
		return fmt.Errorf("invalid URL destination: %w", err)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if !parsed.IsAbs() || parsed.User != nil || parsed.Host == "" || parsed.Hostname() == "" {
			return fmt.Errorf("invalid URL destination")
		}
	case "mailto":
		if parsed.Opaque == "" {
			return fmt.Errorf("invalid URL destination")
		}
	default:
		return fmt.Errorf("invalid URL destination")
	}
	return nil
}

func startURLCommand(name string, args ...string) error {
	command := exec.CommandContext(context.Background(), name, args...) // #nosec G204 -- fixed OS launcher receives the validated destination as one argument
	return startURLProcess(command)
}

func startURLProcess(process urlProcess) error {
	if err := process.Start(); err != nil {
		return err
	}
	go func() { _ = process.Wait() }()
	return nil
}
