package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// HEY keeps the account user as creator and the selected send-as contact as sender.
// Sending from an alias must not make a later thread read look like it came from
// the account user's primary address.
func TestComposeFromIsVisibleOnThreadRead(t *testing.T) {
	var actingSenderID atomic.Int64
	var messageReads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "identity"):
			fmt.Fprint(w, senderIdentity)
		case r.Method == http.MethodPost && r.URL.Path == "/messages.json":
			var request struct {
				ActingSenderID int64 `json:"acting_sender_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			actingSenderID.Store(request.ActingSenderID)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/topics/7/entries.json":
			fmt.Fprint(w, `[{"id":11,"kind":"message","creator":{"id":77,"name":"Personal","email_address":"personal@example.org"}}]`)
		case r.URL.Path == "/messages/11.json":
			messageReads.Add(1)
			fmt.Fprint(w, `{"id":11,"content":"<p>Numbers.</p>","creator":{"id":77,"name":"Personal","email_address":"personal@example.org"},"sender":{"id":88,"name":"Billing","email_address":"billing@example.org"}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	stdoutTerminal(t, false)

	_, _, err := runCLIRaw(t, server, "--json", "compose", "--from", "billing@example.org", "--to", "maria@example.com", "--subject", "Board update", "-m", "Numbers.")
	if err != nil {
		t.Fatal(err)
	}
	if actingSenderID.Load() != 88 {
		t.Fatalf("sent with sender id %d, want 88", actingSenderID.Load())
	}

	stdout, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Data []struct {
			Creator struct {
				EmailAddress string `json:"email_address"`
			} `json:"creator"`
			Sender struct {
				EmailAddress string `json:"email_address"`
			} `json:"sender"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil || len(response.Data) != 1 {
		t.Fatalf("invalid thread response: %v %s", err, stdout)
	}
	entry := response.Data[0]
	if entry.Creator.EmailAddress != "personal@example.org" || entry.Sender.EmailAddress != "billing@example.org" {
		t.Fatalf("read back creator %q and sender %q, want the account user and selected From address", entry.Creator.EmailAddress, entry.Sender.EmailAddress)
	}

	styled, _, err := runCLIRaw(t, server, "--styled", "thread", "read", "7")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled, "From: Billing <billing@example.org>") {
		t.Fatalf("thread displays the wrong From: %s", styled)
	}
	markdown, _, err := runCLIRaw(t, server, "--markdown", "thread", "read", "7")
	if err != nil || !strings.Contains(markdown, "Billing") || !strings.Contains(markdown, `billing\@example\.org`) {
		t.Fatalf("Markdown thread hides From: %v %s", err, markdown)
	}
	html, _, err := runCLIRaw(t, server, "--html", "thread", "read", "7")
	if err != nil || !strings.Contains(html, "Billing &lt;billing@example.org&gt;") {
		t.Fatalf("HTML thread hides From: %v %s", err, html)
	}

	before := messageReads.Load()
	for _, format := range []string{"--count", "--ids-only"} {
		if _, _, err := runCLIRaw(t, server, format, "thread", "read", "7"); err != nil {
			t.Fatal(err)
		}
	}
	if messageReads.Load() != before {
		t.Fatalf("index-only formats read %d message bodies, want none", messageReads.Load()-before)
	}
}
