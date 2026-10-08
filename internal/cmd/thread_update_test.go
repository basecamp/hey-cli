package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

func threadRenameHandler(t *testing.T, gotName *string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/topics/4471829.json" {
			t.Errorf("request = %s %s, want PATCH /topics/4471829.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body struct {
			Topic struct {
				Name *string `json:"name"`
			} `json:"topic"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Topic.Name == nil {
			t.Fatal("body carries no topic.name")
		}
		*gotName = *body.Topic.Name
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestThreadUpdateRenamesTheTopic(t *testing.T) {
	for _, verb := range []string{"update", "rename", "edit"} {
		t.Run(verb, func(t *testing.T) {
			var gotName string
			response, err := runJSONCommand(t, threadRenameHandler(t, &gotName),
				"thread", verb, "4471829", "--name", "  Kitchen renovation quotes  ")
			if err != nil {
				t.Fatalf("thread %s: %v", verb, err)
			}
			if gotName != "Kitchen renovation quotes" {
				t.Errorf("name sent = %q, want it trimmed", gotName)
			}
			if response.Summary != `Thread 4471829 renamed to "Kitchen renovation quotes"` || response.Data != nil {
				t.Errorf("response = %#v", response)
			}
		})
	}
}

func TestThreadUpdateKeepsTheNameAsTyped(t *testing.T) {
	var gotName string
	name := `Café "Lisboa" — 14 May ✈`
	if _, err := runJSONCommand(t, threadRenameHandler(t, &gotName), "thread", "update", "4471829", "--name", name); err != nil {
		t.Fatalf("thread update: %v", err)
	}
	if gotName != name {
		t.Errorf("name sent = %q, want %q", gotName, name)
	}
}

func TestThreadUpdateStyledConfirmation(t *testing.T) {
	var gotName string
	out, err := runStyledCommand(t, threadRenameHandler(t, &gotName),
		"thread", "update", "4471829", "--name", "Flights to Lisbon\x1b[31m")
	if err != nil {
		t.Fatalf("thread update: %v", err)
	}
	if strings.Contains(out, "\x1b[31m") {
		t.Errorf("styled output carries the escape sequence: %q", out)
	}
	if !strings.Contains(out, "Thread 4471829 renamed to") {
		t.Errorf("styled output = %q", out)
	}
}

func TestThreadUpdateRefusesBeforeWriting(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no name", args: []string{"thread", "update", "4471829"}, want: "thread name is required"},
		{name: "blank name", args: []string{"thread", "update", "4471829", "--name", "   "}, want: "thread name is required"},
		{name: "too long", args: []string{"thread", "update", "4471829", "--name", strings.Repeat("é", 513)}, want: "1026 bytes, at most 1024"},
		{name: "bad id", args: []string{"thread", "update", "abc", "--name", "Quotes"}, want: "invalid thread ID: abc"},
		{name: "zero id", args: []string{"thread", "update", "0", "--name", "Quotes"}, want: "invalid thread ID: 0"},
		{name: "no id", args: []string{"thread", "update", "--name", "Quotes"}, want: "Usage: hey thread update <thread-id>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}), tt.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.want)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want none", requests.Load())
			}
		})
	}
}

func TestThreadUpdateAcceptsTheLongestName(t *testing.T) {
	var gotName string
	name := strings.Repeat("é", 512)
	if _, err := runJSONCommand(t, threadRenameHandler(t, &gotName), "thread", "update", "4471829", "--name", name); err != nil {
		t.Fatalf("thread update: %v", err)
	}
	if gotName != name {
		t.Errorf("name sent is %d bytes, want %d", len(gotName), len(name))
	}
}

func TestThreadUpdateNotFound(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}), "thread", "update", "4471829", "--name", "Quotes")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeNotFound {
		t.Fatalf("error = %v, want not_found", err)
	}
}

// A merged thread redirects to the one it was merged into, and net/http follows that
// PATCH as a GET. The read that answers is not a rename, so it must not be reported as one.
func TestThreadUpdateMergedThreadIsNotReportedRenamed(t *testing.T) {
	var patches atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/topics/4471829.json" && r.Method == http.MethodPatch:
			patches.Add(1)
			http.Redirect(w, r, "/topics/4471830", http.StatusFound)
		case r.URL.Path == "/topics/4471830" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":4471830}`))
		default:
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}), "thread", "update", "4471829", "--name", "Quotes")
	if err == nil {
		t.Fatal("a merged thread was reported renamed")
	}
	if patches.Load() != 1 {
		t.Errorf("patches = %d, want 1", patches.Load())
	}
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || !strings.Contains(cliErr.Message, "was not renamed") ||
		!strings.Contains(cliErr.Message, "merged") || !strings.Contains(cliErr.Hint, "merged into") {
		t.Errorf("error = %#v, want it to say the thread was not renamed and may have been merged", err)
	}
}
