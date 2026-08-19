package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type sentForward struct {
	Requests []string
	Subject  string
	Content  string
	To       []string
	CC       []string
	BCC      []string
}

func forwardServer(t *testing.T, entriesJSON string) (*httptest.Server, *sentForward) {
	t.Helper()
	sent := &sentForward{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Requests = append(sent.Requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/topics/7.json":
			fmt.Fprintf(w, `{"id":7,"name":"Quarterly planning","entries":%s}`, entriesJSON)
		case "/entries/12/forwards/new.json":
			fmt.Fprint(w, `{"subject":"Fwd: Quarterly planning","content":"<div>Quoted message</div>"}`)
		case "/identity.json":
			fmt.Fprint(w, `{"id":1,"senders":[{"id":42,"default":true}]}`)
		case "/messages.json":
			var body struct {
				Message struct {
					Subject string `json:"subject"`
					Content string `json:"content"`
				} `json:"message"`
				Entry struct {
					Addressed struct {
						Directly    []string `json:"directly"`
						Copied      []string `json:"copied"`
						Blindcopied []string `json:"blindcopied"`
					} `json:"addressed"`
				} `json:"entry"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent.Subject = body.Message.Subject
			sent.Content = body.Message.Content
			sent.To = body.Entry.Addressed.Directly
			sent.CC = body.Entry.Addressed.Copied
			sent.BCC = body.Entry.Addressed.Blindcopied
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, sent
}

func TestForwardSendsLatestEntryDraft(t *testing.T) {
	server, sent := forwardServer(t, `[{"id":11},{"id":12}]`)

	err := runCLI(t, server, "forward", "7",
		"--to", "alice@example.com",
		"--cc", "bob@example.org",
		"--bcc", "carol@example.com",
		"-m", "For your review\nThanks & take care",
	)
	if err != nil {
		t.Fatalf("forward failed: %v", err)
	}

	wantRequests := []string{
		"GET /topics/7.json",
		"GET /entries/12/forwards/new.json",
		"GET /identity.json",
		"POST /messages.json",
	}
	if strings.Join(sent.Requests, ",") != strings.Join(wantRequests, ",") {
		t.Errorf("requests = %v, want %v", sent.Requests, wantRequests)
	}
	if sent.Subject != "Fwd: Quarterly planning" {
		t.Errorf("subject = %q", sent.Subject)
	}
	wantContent := `<div>For your review<br>Thanks &amp; take care</div><br><div>Quoted message</div>`
	if sent.Content != wantContent {
		t.Errorf("content = %q, want %q", sent.Content, wantContent)
	}
	if len(sent.To) != 1 || sent.To[0] != "alice@example.com" {
		t.Errorf("to = %v", sent.To)
	}
	if len(sent.CC) != 1 || sent.CC[0] != "bob@example.org" {
		t.Errorf("cc = %v", sent.CC)
	}
	if len(sent.BCC) != 1 || sent.BCC[0] != "carol@example.com" {
		t.Errorf("bcc = %v", sent.BCC)
	}
}

func TestForwardRequiresRecipient(t *testing.T) {
	server, sent := forwardServer(t, `[{"id":12}]`)

	err := runCLI(t, server, "forward", "7")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("forward without a recipient should be a usage error, got %v", err)
	}
	if len(sent.Requests) != 0 {
		t.Errorf("forward made requests before validating recipients: %v", sent.Requests)
	}
}

func TestForwardRequiresThreadEntry(t *testing.T) {
	server, sent := forwardServer(t, `[]`)

	err := runCLI(t, server, "forward", "7", "--to", "alice@example.com")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "not_found" {
		t.Fatalf("forward without an entry should be a not-found error, got %v", err)
	}
	if len(sent.Requests) != 1 || sent.Requests[0] != "GET /topics/7.json" {
		t.Errorf("requests = %v, want only the topic read", sent.Requests)
	}
}
