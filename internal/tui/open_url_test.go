package tui

import (
	"errors"
	"os/exec"
	"testing"
)

func TestOpenURLCommand(t *testing.T) {
	tests := []struct {
		name string
		goos string
		want string
		args []string
	}{
		{name: "darwin", goos: "darwin", want: "open", args: []string{"https://example.com/a?x=1&y=2"}},
		{name: "linux", goos: "linux", want: "xdg-open", args: []string{"mailto:alex@example.com?subject=Hello"}},
		{name: "windows", goos: "windows", want: "rundll32", args: []string{"url.dll,FileProtocolHandler", "https://example.com/a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, args, err := openURLCommand(tt.goos, tt.args[len(tt.args)-1])
			if err != nil {
				t.Fatalf("openURLCommand() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("executable = %q, want %q", got, tt.want)
			}
			if len(args) != len(tt.args) {
				t.Fatalf("args = %#v, want %#v", args, tt.args)
			}
			for i := range args {
				if args[i] != tt.args[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tt.args[i])
				}
			}
		})
	}
}

func TestOpenExternalURLReturnsLauncherFailure(t *testing.T) {
	launcherErr := errors.New("launcher failed")
	var executable string
	var arguments []string
	err := openURLWith("linux", "https://example.com/report", func(name string, args ...string) error {
		executable = name
		arguments = append(arguments, args...)
		return launcherErr
	})
	if !errors.Is(err, launcherErr) {
		t.Fatalf("openURLWith error = %v, want launcher failure", err)
	}
	if executable != "xdg-open" || len(arguments) != 1 || arguments[0] != "https://example.com/report" {
		t.Errorf("launcher = %q %#v", executable, arguments)
	}
}

func TestRunURLCommandReturnsExitFailure(t *testing.T) {
	command, err := exec.LookPath("false")
	if err != nil {
		t.Skip("false command is unavailable")
	}
	if err := runURLCommand(command); err == nil {
		t.Fatal("runURLCommand returned success for a failed launcher")
	}
}

func TestOpenURLCommandValidation(t *testing.T) {
	valid := []string{"http://example.com", "https://example.com/path", "mailto:alex@example.com"}
	invalid := []string{"", "http:/example.com", "http:///path", "http://", "https://?x", "https://trusted.example@evil.example/path", "ftp://example.com", "file:///tmp/a", "mailto:", "mailto://", "https://example.com/line\nbreak", "https://example.com/\u0085", "https://example.com/%zz"}

	for _, goos := range []string{"darwin", "linux", "windows", "freebsd", ""} {
		t.Run(goos, func(t *testing.T) {
			for _, destination := range valid {
				if _, _, err := openURLCommand(goos, destination); goos == "freebsd" || goos == "" {
					if err == nil {
						t.Errorf("openURLCommand(%q, %q) accepted unsupported OS", goos, destination)
					}
				} else if err != nil {
					t.Errorf("openURLCommand(%q, %q) error = %v", goos, destination, err)
				}
			}
			for _, destination := range invalid {
				if _, _, err := openURLCommand(goos, destination); err == nil {
					t.Errorf("openURLCommand(%q, %q) accepted invalid destination", goos, destination)
				}
			}
		})
	}
}
