package cmd

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

func TestRestoreOneOrMoreThreads(t *testing.T) {
	var paths []string
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}), "restore", "12345", "67890", "12345")
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	wantPaths := []string{"/topics/12345/status/active.json", "/topics/67890/status/active.json"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("paths = %v, want %v", paths, wantPaths)
	}
	if response.Summary != "2 threads restored from Trash" {
		t.Errorf("summary = %q, want %q", response.Summary, "2 threads restored from Trash")
	}
	if response.Data != nil {
		t.Errorf("data = %#v, want it omitted", response.Data)
	}
}

func TestRestoreHelpNamesThreadIDsAndTrash(t *testing.T) {
	command, _, err := newRootCmd().Find([]string{"restore"})
	if err != nil {
		t.Fatal(err)
	}

	if command.Use != "restore <thread-id>..." {
		t.Errorf("use = %q, want thread IDs", command.Use)
	}
	for field, text := range map[string]string{
		"long help":   command.Long,
		"agent notes": command.Annotations["agent_notes"],
	} {
		for _, want := range []string{"topic_id", "hey search --in trash", "once hey thread list --in trash is available", "currently in Trash"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s = %q, want %q", field, text, want)
			}
		}
	}
	if !strings.Contains(command.Annotations["agent_notes"], "thread IDs") || !strings.Contains(command.Annotations["agent_notes"], "not box item IDs") {
		t.Errorf("agent notes do not distinguish thread IDs from box item IDs: %q", command.Annotations["agent_notes"])
	}
}

func TestRestoreRejectsMissingOrInvalidIDsBeforeRequest(t *testing.T) {
	var requests atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})

	tests := [][]string{
		{"restore"},
		{"restore", "not-an-id"},
		{"restore", "0"},
		{"restore", "--", "-12345"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := runJSONCommand(t, handler, args...)
			if err == nil {
				t.Fatalf("%v was accepted", args)
			}
			var cliErr *apierr.Error
			if len(args) > 1 && !errors.As(err, &cliErr) {
				t.Errorf("error = %v, want a structured usage error", err)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid input made %d requests", requests.Load())
	}
}

func TestRestoreStopsAndReportsAThreadFailure(t *testing.T) {
	var paths []string
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/topics/67890/status/active.json" {
			http.Error(w, "cannot restore", http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), "restore", "12345", "67890", "24680")
	if err == nil {
		t.Fatal("server failure was not reported")
	}

	wantPaths := []string{"/topics/12345/status/active.json", "/topics/67890/status/active.json"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("paths = %v, want the command to stop after the failure at %v", paths, wantPaths)
	}
}

func TestRestoreStyledOutputUsesMutationFormat(t *testing.T) {
	stdout, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "restore", "12345", "67890")
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if stdout != "2 threads restored from Trash.\n" {
		t.Errorf("styled output = %q", stdout)
	}
}
