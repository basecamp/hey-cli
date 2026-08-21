package attachments

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type downloadFunc func(context.Context, string, io.Writer) (int64, http.Header, error)

func (f downloadFunc) DownloadBlob(ctx context.Context, sourceURL string, destination io.Writer) (int64, http.Header, error) {
	return f(ctx, sourceURL, destination)
}

func writeDownload(data string) downloadFunc {
	return func(_ context.Context, _ string, destination io.Writer) (int64, http.Header, error) {
		written, err := io.WriteString(destination, data)
		return int64(written), nil, err
	}
}

func TestDestinationPreservesPortableFilenameBehavior(t *testing.T) {
	directory := t.TempDir()
	for filename, want := range map[string]string{
		"../../quarterly-report.pdf": "quarterly-report.pdf",
		`..\..\project-notes.txt`:    "project-notes.txt",
		"CON.pdf":                    "_CON.pdf",
		"CONIN$":                     "_CONIN$",
		"LPT².log":                   "_LPT².log",
		"notes?.txt":                 "notes_.txt",
		"line\nbreak.txt":            "line_break.txt",
		"quarterly-report. ":         "quarterly-report",
	} {
		destination, err := Destination(directory, filename)
		if err != nil {
			t.Errorf("Destination(%q): %v", filename, err)
			continue
		}
		if destination != filepath.Join(directory, want) {
			t.Errorf("Destination(%q) = %q, want %q", filename, destination, filepath.Join(directory, want))
		}
	}

	for _, filename := range []string{"", ".", ".."} {
		if _, err := Destination("", filename); err == nil {
			t.Errorf("unsafe filename %q was accepted", filename)
		}
	}

	explicit := filepath.Join(t.TempDir(), "chosen-name.pdf")
	if destination, err := Destination(explicit, ".."); err != nil || destination != explicit {
		t.Errorf("explicit destination = %q, %v", destination, err)
	}
}

func TestSaveBytesWritesContentSafely(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "tracked-time.csv")
	written, err := SaveBytes(destination, []byte("Start,End\n09:00,10:00\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(len("Start,End\n09:00,10:00\n")) {
		t.Errorf("written = %d", written)
	}
	assertFileContent(t, destination, "Start,End\n09:00,10:00\n")
}

func TestSavePreservesExistingFileUnlessForced(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "quarterly-report.pdf")
	if err := os.WriteFile(destination, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Save(context.Background(), writeDownload("new report"), destination, "/report.pdf", false)
	var saveErr *apierr.Error
	if !errors.As(err, &saveErr) || saveErr.Code != "usage" || !strings.Contains(saveErr.Message, "use --force") {
		t.Fatalf("existing destination error = %v", err)
	}
	assertFileContent(t, destination, "keep me")

	written, err := Save(context.Background(), writeDownload("new report"), destination, "/report.pdf", true)
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(len("new report")) {
		t.Errorf("written = %d, want %d", written, len("new report"))
	}
	assertFileContent(t, destination, "new report")
}

func TestSaveDoesNotReplaceFileCreatedDuringDownload(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "quarterly-report.pdf")
	downloader := func(_ context.Context, _ string, temporary io.Writer) (int64, http.Header, error) {
		written, err := io.WriteString(temporary, "downloaded report")
		if err != nil {
			return int64(written), nil, err
		}
		if err := os.WriteFile(destination, []byte("created concurrently"), 0o600); err != nil {
			return int64(written), nil, err
		}
		return int64(written), nil, nil
	}

	_, err := Save(context.Background(), downloadFunc(downloader), destination, "/report.pdf", false)
	var saveErr *apierr.Error
	if !errors.As(err, &saveErr) || saveErr.Code != "usage" {
		t.Fatalf("concurrent destination error = %v", err)
	}
	assertFileContent(t, destination, "created concurrently")
	assertNoTemporaryFiles(t, directory)
}

func TestSaveRemovesPartialFileAndPreservesDownloadError(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "quarterly-report.pdf")
	downloadErr := errors.New("download stopped")
	downloader := func(_ context.Context, _ string, temporary io.Writer) (int64, http.Header, error) {
		written, err := io.WriteString(temporary, "partial")
		if err != nil {
			return int64(written), nil, err
		}
		return int64(written), nil, downloadErr
	}

	written, err := Save(context.Background(), downloadFunc(downloader), destination, "/report.pdf", false)
	if !errors.Is(err, downloadErr) {
		t.Fatalf("download error = %v, want %v", err, downloadErr)
	}
	if written != int64(len("partial")) {
		t.Errorf("written = %d, want %d", written, len("partial"))
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Errorf("failed download left destination behind: %v", err)
	}
	assertNoTemporaryFiles(t, directory)
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Errorf("file content = %q, want %q", data, want)
	}
}

func assertNoTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".hey-file-") {
			t.Errorf("temporary file was not removed: %s", entry.Name())
		}
	}
}
