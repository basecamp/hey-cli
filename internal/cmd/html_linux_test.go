package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// openPTY opens a pseudo-terminal pair and returns its slave side as a file.
func openPTY(t *testing.T) *os.File {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })

	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock pty: %v", err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pty number: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pty slave: %v", err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return slave
}

// withStdout points os.Stdout at f for the test, with the real terminal check in
// place rather than the seam, so what is exercised is what the binary does.
func withStdout(t *testing.T, f *os.File) {
	t.Helper()
	previousStdout, previousCheck := os.Stdout, stdoutIsTerminal
	os.Stdout = f
	stdoutIsTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }
	t.Cleanup(func() {
		os.Stdout = previousStdout
		stdoutIsTerminal = previousCheck
	})
}

// On a real terminal --html is refused before anything is written to it.
func TestHTMLIsRefusedOnARealTerminal(t *testing.T) {
	withStdout(t, openPTY(t))
	server, _ := threadEntriesServer(t, nil, nil)

	_, _, err := runCLIRaw(t, server, "threads", "7", "--html")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierr.CodeUsage || !strings.Contains(apiErr.Message, "terminal") {
		t.Fatalf("error = %v, want the terminal refusal", err)
	}
}

// On a real pipe --html writes the HTML.
func TestHTMLWritesToARealPipe(t *testing.T) {
	reader, pipeWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	withStdout(t, pipeWriter)
	server, _ := threadEntriesServer(t, [][]int64{{11}}, map[int64]string{11: "<p>piped</p>"})

	// The root command writes to os.Stdout when no writer is set on it.
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)
	root := newRootCmd()
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--base-url", server.URL, "threads", "7", "--html"})
	runErr := root.Execute()
	_ = pipeWriter.Close()

	var out bytes.Buffer
	if _, err := out.ReadFrom(reader); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("unexpected error: %v", runErr)
	}
	if !strings.HasPrefix(out.String(), "<!doctype html>\n") || !strings.Contains(out.String(), "<p>piped</p>") {
		t.Errorf("pipe carried %q, want the HTML document", out.String())
	}
}
