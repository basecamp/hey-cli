package cmd

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTimetrackExportWritesServerCSVToStdout(t *testing.T) {
	csv := timeTrackCSVFixture(t)
	var requests atomic.Int32
	stdout, err := runFormattedCommand(t, timeTrackExportHandler(t, &requests), nil, "timetrack", "export")
	if err != nil {
		t.Fatalf("execute timetrack export: %v", err)
	}
	if stdout != csv {
		t.Errorf("stdout = %q, want server CSV", stdout)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", requests.Load())
	}
}

func TestTimetrackExportSavesCSV(t *testing.T) {
	csv := timeTrackCSVFixture(t)
	destination := filepath.Join(t.TempDir(), "tracked-time.csv")
	var requests atomic.Int32
	response, err := runJSONCommand(t, timeTrackExportHandler(t, &requests), "timetrack", "export", "--output", destination)
	if err != nil {
		t.Fatalf("execute timetrack export: %v", err)
	}

	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want export metadata", response.Data)
	}
	if data["path"] != destination || data["byte_size"] != float64(len(csv)) {
		t.Errorf("data = %#v", data)
	}
	if response.Summary != "Time tracks exported to "+destination {
		t.Errorf("summary = %q", response.Summary)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != csv {
		t.Errorf("saved CSV = %q", contents)
	}
}

func TestTimetrackExportPreservesExistingFileWithoutRequest(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "tracked-time.csv")
	if err := os.WriteFile(destination, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	_, err := runJSONCommand(t, timeTrackExportHandler(t, &requests), "timetrack", "export", "--output", destination)
	if err == nil || !strings.Contains(err.Error(), "use --force") {
		t.Fatalf("error = %v, want existing destination error", err)
	}
	if requests.Load() != 0 {
		t.Errorf("requests = %d, want 0", requests.Load())
	}
	contents, readErr := os.ReadFile(destination)
	if readErr != nil || string(contents) != "keep me" {
		t.Errorf("existing file = %q, %v", contents, readErr)
	}
}

func TestTimetrackExportForceReplacesExistingFile(t *testing.T) {
	csv := timeTrackCSVFixture(t)
	destination := filepath.Join(t.TempDir(), "tracked-time.csv")
	if err := os.WriteFile(destination, []byte("old export"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	if _, err := runJSONCommand(t, timeTrackExportHandler(t, &requests), "timetrack", "export", "--output", destination, "--force"); err != nil {
		t.Fatalf("execute timetrack export --force: %v", err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != csv {
		t.Errorf("replaced file = %q, %v", contents, err)
	}
}

func TestTimetrackExportFetchFailurePreservesExistingFile(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "tracked-time.csv")
	if err := os.WriteFile(destination, []byte("old export"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "export unavailable", http.StatusBadRequest)
	}), "timetrack", "export", "--output", destination, "--force")
	if err == nil {
		t.Fatal("expected export error")
	}
	contents, readErr := os.ReadFile(destination)
	if readErr != nil || string(contents) != "old export" {
		t.Errorf("existing file = %q, %v", contents, readErr)
	}
}

func TestTimetrackExportValidatesRawOutputFlagsWithoutRequest(t *testing.T) {
	for _, args := range [][]string{
		{"timetrack", "export", "--force"},
		{"timetrack", "export"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, timeTrackExportHandler(t, &requests), args...)
			if err == nil {
				t.Fatal("expected usage error")
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want 0", requests.Load())
			}
		})
	}

	t.Run("styled", func(t *testing.T) {
		var requests atomic.Int32
		_, err := runFormattedCommand(t, timeTrackExportHandler(t, &requests), []string{"--styled"}, "timetrack", "export")
		if err == nil {
			t.Fatal("expected usage error")
		}
		if requests.Load() != 0 {
			t.Errorf("requests = %d, want 0", requests.Load())
		}
	})
}

func timeTrackExportHandler(t *testing.T, requests *atomic.Int32) http.Handler {
	t.Helper()
	csv := timeTrackCSVFixture(t)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/calendar/time_tracks/exports" {
			t.Errorf("request = %s %s, want GET /calendar/time_tracks/exports", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if accept := r.Header.Get("Accept"); accept != "text/csv" {
			t.Errorf("Accept = %q, want text/csv", accept)
		}
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.WriteString(w, csv)
	})
}

func timeTrackCSVFixture(t *testing.T) string {
	t.Helper()
	contents, err := os.ReadFile("testdata/time-tracking-export.csv")
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
