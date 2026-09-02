package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// messageServingAddressed answers one message whose `addressed` object is exactly the
// JSON given, so a test can say what HEY put on the wire rather than what a Go struct
// looks like after somebody built it by hand.
func messageServingAddressed(t *testing.T, addressedJSON string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/messages/") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":9101,"subject":"Inovo Customer Update — Week 12",
			"content":"<p>Hi Alice,</p>",
			"sender":{"id":42,"email_address":"nova@example.com"}%s}`, addressedJSON)
	}))
	t.Cleanup(server.Close)
	return server
}

// bcc_disclosed rests on one fact about the decode: `encoding/json` leaves a field HEY
// omitted — or served as null — nil, and makes an explicitly empty array non-nil, and
// nothing between HEY's JSON and generated.Message flattens the two together. That is
// asserted here through the real path — an HTTP response read by the SDK — because a
// struct built by hand would prove nothing about the decoder, and this is the invariant
// that would break silently if the SDK ever changed decoders.
func TestBlindcopiedPresenceSurvivesTheSDKDecode(t *testing.T) {
	tests := []struct {
		name         string
		addressed    string
		wantNonNil   bool
		wantContacts int
	}{
		{
			name:      "the addressed object itself is omitted",
			addressed: ``,
		},
		{
			name:      "blindcopied is omitted",
			addressed: `,"addressed":{"directly":[{"id":100,"email_address":"alice@example.com"}]}`,
		},
		{
			name:      "blindcopied is null",
			addressed: `,"addressed":{"directly":[{"id":100,"email_address":"alice@example.com"}],"blindcopied":null}`,
		},
		{
			name:       "blindcopied is an explicitly empty array",
			addressed:  `,"addressed":{"directly":[{"id":100,"email_address":"alice@example.com"}],"blindcopied":[]}`,
			wantNonNil: true,
		},
		{
			name:         "blindcopied carries addresses",
			addressed:    `,"addressed":{"blindcopied":[{"id":102,"email_address":"carol@example.org"}]}`,
			wantNonNil:   true,
			wantContacts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := messageServingAddressed(t, tt.addressed)
			withSDKPointedAt(t, server)

			message, err := sdk.Messages().Get(context.Background(), 9101)
			if err != nil {
				t.Fatalf("read the message: %v", err)
			}
			if got := message.Addressed.Blindcopied != nil; got != tt.wantNonNil {
				t.Errorf("blindcopied non-nil = %v, want %v", got, tt.wantNonNil)
			}
			if len(message.Addressed.Blindcopied) != tt.wantContacts {
				t.Errorf("blindcopied = %d contacts, want %d",
					len(message.Addressed.Blindcopied), tt.wantContacts)
			}
		})
	}
}

// bcc_disclosed answers "did HEY tell us the BCC line", not "was anybody on it". Those
// are different questions, and reading the second as the first is what left a caller
// unable to prove a message's exact destinations: an empty BCC that HEY served and an
// empty BCC that HEY withheld had the same shape.
func TestAddressedFromReportsWhetherHEYServedABCCLineAtAll(t *testing.T) {
	tests := []struct {
		name          string
		blindcopied   []generated.Contact
		wantDisclosed bool
		wantBCC       []string
	}{
		{
			name:          "no blindcopied field at all",
			blindcopied:   nil,
			wantDisclosed: false,
			wantBCC:       []string{},
		},
		{
			name:          "an explicitly empty blindcopied line",
			blindcopied:   []generated.Contact{},
			wantDisclosed: true,
			wantBCC:       []string{},
		},
		{
			name:          "a blindcopied line with addresses",
			blindcopied:   []generated.Contact{{Id: 102, EmailAddress: "carol@example.org"}},
			wantDisclosed: true,
			wantBCC:       []string{"carol@example.org"},
		},
		{
			// A contact HEY named without an address is still HEY answering the
			// question: the line was served, it just carries nothing this can print.
			name:          "a blindcopied line whose only contact has no address",
			blindcopied:   []generated.Contact{{Id: 102}},
			wantDisclosed: true,
			wantBCC:       []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope := addressedFrom(generated.Addressed{Blindcopied: tt.blindcopied})
			if envelope.BCCDisclosed != tt.wantDisclosed {
				t.Errorf("bcc_disclosed = %v, want %v", envelope.BCCDisclosed, tt.wantDisclosed)
			}
			if !equalStrings(envelope.BCC, tt.wantBCC) {
				t.Errorf("bcc = %v, want %v", envelope.BCC, tt.wantBCC)
			}
			if envelope.BCC == nil {
				t.Error("bcc must marshal as [] rather than null")
			}
		})
	}
}

// Disclosure and the bound are independent: a line long enough to be cut was plainly
// served, so it is disclosed and truncated at once.
func TestAddressedFromKeepsDisclosureWhenALineIsCut(t *testing.T) {
	contacts := make([]generated.Contact, 0, maxRetainedRecipients+5)
	for i := range maxRetainedRecipients + 5 {
		contacts = append(contacts, generated.Contact{
			Id: int64(200 + i), EmailAddress: fmt.Sprintf("reader%d@example.com", i),
		})
	}

	envelope := addressedFrom(generated.Addressed{Blindcopied: contacts})
	if !envelope.BCCDisclosed {
		t.Error("a line HEY served in full is disclosed however much of it is kept")
	}
	if !envelope.Truncated {
		t.Error("a cut list must say it was cut")
	}
	if len(envelope.BCC) != maxRetainedRecipients {
		t.Errorf("bcc = %d addresses, want the bound of %d", len(envelope.BCC), maxRetainedRecipients)
	}
	if envelope.BCC[0] != "reader0@example.com" {
		t.Errorf("bcc[0] = %q, want the first address verbatim", envelope.BCC[0])
	}
}
