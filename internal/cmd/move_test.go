package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

type recordedMove struct {
	requests   []string
	postingIDs []int64
	boxID      int64
	moveStatus int
}

func moveServer(t *testing.T) (*httptest.Server, *recordedMove) {
	t.Helper()
	recorded := &recordedMove{moveStatus: http.StatusNoContent}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.requests = append(recorded.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/boxes.json":
			_, _ = w.Write([]byte(`[
				{"id":1,"kind":"imbox","name":"Imbox"},
				{"id":2,"kind":"feedbox","name":"The Feed"},
				{"id":3,"kind":"asidebox","name":"Set Aside"},
				{"id":4,"kind":"laterbox","name":"Reply Later"},
				{"id":5,"kind":"trailbox","name":"Paper Trail"},
				{"id":6,"kind":"bubblebox","name":"Bubble Up"}
			]`))
		case "/postings/moves.json":
			var body struct {
				PostingIDs []int64 `json:"posting_ids"`
				BoxID      int64   `json:"box_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			recorded.postingIDs = body.PostingIDs
			recorded.boxID = body.BoxID
			w.WriteHeader(recorded.moveStatus)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, recorded
}

func runMove(t *testing.T, server *httptest.Server, args ...string) (output.Response, error) {
	t.Helper()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"move", "--json", "--base-url", server.URL}, args...))

	err := root.Execute()
	var resp output.Response
	if buf.Len() > 0 {
		_ = json.Unmarshal(buf.Bytes(), &resp)
	}
	return resp, err
}

func TestMovePostingsToNamedBox(t *testing.T) {
	server, recorded := moveServer(t)

	resp, err := runMove(t, server, "12345", "67890", "--to", "paper-trail")
	if err != nil {
		t.Fatalf("move failed: %v", err)
	}
	if got := strings.Join(recorded.requests, ","); got != "GET /boxes.json,POST /postings/moves.json" {
		t.Errorf("requests = %q", got)
	}
	if recorded.boxID != 5 {
		t.Errorf("box_id = %d, want 5", recorded.boxID)
	}
	if len(recorded.postingIDs) != 2 || recorded.postingIDs[0] != 12345 || recorded.postingIDs[1] != 67890 {
		t.Errorf("posting_ids = %v", recorded.postingIDs)
	}
	if resp.Summary != "2 threads moved to Paper Trail" {
		t.Errorf("summary = %q", resp.Summary)
	}
}

func TestMoveDestinationAliases(t *testing.T) {
	tests := []struct {
		to    string
		boxID int64
	}{
		{"imbox", 1},
		{"feed", 2},
		{"The Feed", 2},
		{"aside", 3},
		{"set_aside", 3},
		{"later", 4},
		{"reply later", 4},
		{"trail", 5},
		{"paper trail", 5},
		{"5", 5},
	}

	for _, tt := range tests {
		t.Run(tt.to, func(t *testing.T) {
			server, recorded := moveServer(t)
			if _, err := runMove(t, server, "12345", "--to", tt.to); err != nil {
				t.Fatalf("move failed: %v", err)
			}
			if recorded.boxID != tt.boxID {
				t.Errorf("box_id = %d, want %d", recorded.boxID, tt.boxID)
			}
		})
	}
}

func TestMoveRejectsBubbleUp(t *testing.T) {
	for _, to := range []string{"bubble", "Bubble Up", "bubblebox", "6"} {
		t.Run(to, func(t *testing.T) {
			server, recorded := moveServer(t)
			_, err := runMove(t, server, "12345", "--to", to)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
				t.Fatalf("Bubble Up should produce a usage error, got %v", err)
			}
			if len(recorded.requests) != 1 || recorded.requests[0] != "GET /boxes.json" {
				t.Errorf("requests = %v, want only box discovery", recorded.requests)
			}
		})
	}
}

func TestMoveRequiresDestination(t *testing.T) {
	server, recorded := moveServer(t)

	_, err := runMove(t, server, "12345")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("missing destination should produce a usage error, got %v", err)
	}
	if len(recorded.requests) != 0 {
		t.Errorf("missing destination made requests: %v", recorded.requests)
	}
}

func TestMoveRejectsInvalidPostingIDBeforeRequests(t *testing.T) {
	server, recorded := moveServer(t)

	_, err := runMove(t, server, "not-an-id", "--to", "feed")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("invalid posting should produce a usage error, got %v", err)
	}
	if len(recorded.requests) != 0 {
		t.Errorf("invalid posting made requests: %v", recorded.requests)
	}
}

func TestMoveRejectsUnknownDestination(t *testing.T) {
	server, recorded := moveServer(t)

	_, err := runMove(t, server, "12345", "--to", "archive")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "not_found" {
		t.Fatalf("unknown destination should produce a not-found error, got %v", err)
	}
	if len(recorded.requests) != 1 || recorded.requests[0] != "GET /boxes.json" {
		t.Errorf("requests = %v, want only box discovery", recorded.requests)
	}
}

func TestMoveReportsServerFailure(t *testing.T) {
	server, recorded := moveServer(t)
	recorded.moveStatus = http.StatusUnprocessableEntity

	_, err := runMove(t, server, "12345", "--to", "feed")
	if err == nil {
		t.Fatal("move should report the server failure")
	}
}
