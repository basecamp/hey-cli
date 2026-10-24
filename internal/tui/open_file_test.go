package tui

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func stubSniffFileType(t *testing.T, contentType string, err error) {
	t.Helper()
	original := sniffFileType
	sniffFileType = func(string) (string, error) { return contentType, err }
	t.Cleanup(func() { sniffFileType = original })
}

func TestCheckContentOpensInViewerFollowsTheContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")

	stubSniffFileType(t, "application/pdf", nil)
	if err := checkContentOpensInViewer(path); err != nil {
		t.Fatalf("a PDF was refused: %v", err)
	}

	stubSniffFileType(t, "application/vnd.microsoft.portable-executable", nil)
	err := checkContentOpensInViewer(path)
	if err == nil || !strings.Contains(errorNotice("Could not open attachment", err), "press s to save it") {
		t.Fatalf("a Windows program called report.pdf was not refused: %v", err)
	}

	stubSniffFileType(t, "", errors.New("xdg-mime: not found"))
	if err := checkContentOpensInViewer(path); err == nil {
		t.Fatal("a file nothing could identify was not refused")
	}
}

// A Windows program called agenda.md is what xdg-mime's file(1) backend and
// GNOME's both name by its contents, which is what xdg-open then acts on.
func TestCheckContentOpensInViewerRefusesADisguisedProgramWithXDGMime(t *testing.T) {
	if _, err := exec.LookPath("xdg-mime"); err != nil {
		t.Skip("xdg-mime is not installed")
	}
	program := make([]byte, 512)
	copy(program, "MZ")
	binary.LittleEndian.PutUint32(program[0x3c:], 0x80)
	copy(program[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(program[0x84:], 0x14c)

	directory := t.TempDir()
	disguised := filepath.Join(directory, "agenda.md")
	if err := os.WriteFile(disguised, program, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkContentOpensInViewer(disguised); err == nil {
		contentType, _ := sniffFileType(disguised)
		t.Fatalf("a Windows program called agenda.md was allowed to open (xdg-mime says %q)", contentType)
	}

	notes := filepath.Join(directory, "notes.md")
	if err := os.WriteFile(notes, []byte("# Thursday planning\n\n- Book the room\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkContentOpensInViewer(notes); err != nil {
		contentType, _ := sniffFileType(notes)
		t.Fatalf("a Markdown note was refused (xdg-mime says %q): %v", contentType, err)
	}
}

func TestOpenExternalFileChecksTheContentsBeforeLaunchingOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux picks the application by contents")
	}
	var launched []string
	original := startOpener
	startOpener = func(name string, args ...string) error {
		launched = append(launched, name)
		return nil
	}
	t.Cleanup(func() { startOpener = original })
	path := filepath.Join(t.TempDir(), "report.pdf")

	stubSniffFileType(t, "application/vnd.microsoft.portable-executable", nil)
	if err := openExternalFile(path); err == nil {
		t.Fatal("a Windows program called report.pdf was opened")
	}
	if len(launched) != 0 {
		t.Fatalf("launched %v for a Windows program called report.pdf", launched)
	}

	stubSniffFileType(t, "application/pdf", nil)
	if err := openExternalFile(path); err != nil {
		t.Fatalf("a PDF was refused: %v", err)
	}
	if len(launched) != 1 || launched[0] != "xdg-open" {
		t.Fatalf("launched %v for a PDF, want xdg-open", launched)
	}
}
