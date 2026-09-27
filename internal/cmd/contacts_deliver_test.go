package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type contactDeliveryServer struct {
	server      *httptest.Server
	recorded    *recordedContacts
	contactType string
	boxes       string
}

func newContactDeliveryServer(t *testing.T) *contactDeliveryServer {
	t.Helper()
	state := &contactDeliveryServer{
		recorded:    &recordedContacts{statuses: make(map[string]int)},
		contactType: "Person",
		boxes: `[
			{"id":11,"kind":"imbox","name":"Definitely Not The Imbox"},
			{"id":22,"kind":"feedbox","name":"Newsletters"},
			{"id":33,"kind":"trailbox","name":"Receipts"}
		]`,
	}
	state.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(req.Body)
		state.recorded.mu.Lock()
		state.recorded.requests = append(state.recorded.requests, recordedContactRequest{
			Method: req.Method,
			Path:   req.URL.Path,
			Query:  req.URL.RawQuery,
			Body:   body.Bytes(),
		})
		status := state.recorded.statuses[req.Method+" "+req.URL.Path]
		state.recorded.mu.Unlock()

		if status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"request failed"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/boxes.json":
			_, _ = w.Write([]byte(state.boxes))
		case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/boxes/") && strings.HasSuffix(req.URL.Path, "/designations.json"):
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/7.json":
			_, _ = fmt.Fprintf(w, `{"id":7,"account_id":1,"name":"Jane Doe","email_address":"jane@example.com","contactable_type":%q}`, state.contactType)
		case req.Method == http.MethodPatch && req.URL.Path == "/contacts/7/clearance.json":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(state.server.Close)
	return state
}

func TestContactDeliverHelpDescribesRoutingContract(t *testing.T) {
	command := newContactsDeliverCommand().cmd
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--help"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	help := output.String()
	for _, want := range []string{
		"Pass a contact ID",
		"exact --to values: imbox, feed, papertrail, or screened-out",
		"Screened Out applies only to external email contacts",
		"Imbox removes a custom box preference",
		"does not approve a contact that is already Screened Out",
		"Selecting The Feed removes an existing bundle",
		"write-only",
		"Existing mail may move asynchronously",
		"success means HEY accepted the change",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help does not contain %q:\n%s", want, help)
		}
	}

	const wantAgentNotes = "Use a contact ID from `hey contact list`, not a clearance ID, box item ID, box ID, or email address. Pass the exact --to token: imbox, feed, papertrail, or screened-out."
	if got := command.Annotations["agent_notes"]; got != wantAgentNotes {
		t.Errorf("agent notes = %q, want %q", got, wantAgentNotes)
	}
}

func TestContactDeliverDesignatesCanonicalBoxes(t *testing.T) {
	tests := []struct {
		destination string
		boxID       int64
	}{
		{destination: "imbox", boxID: 11},
		{destination: "feed", boxID: 22},
		{destination: "papertrail", boxID: 33},
	}
	for _, tc := range tests {
		t.Run(tc.destination, func(t *testing.T) {
			state := newContactDeliveryServer(t)
			resp, err := runContacts(t, state.server, "deliver", "7", "--to", tc.destination)
			if err != nil {
				t.Fatal(err)
			}
			result := decodeContactData[contactDeliveryResult](t, resp.Data)
			if result.ID != 7 || result.Destination != tc.destination || resp.Summary != "Contact delivery changed" {
				t.Errorf("result = %+v, summary = %q", result, resp.Summary)
			}
			requests := state.recorded.snapshot()
			if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[0].Path != "/boxes.json" {
				t.Fatalf("requests = %+v", requests)
			}
			wantPath := fmt.Sprintf("/boxes/%d/designations.json", tc.boxID)
			if requests[1].Method != http.MethodPost || requests[1].Path != wantPath {
				t.Fatalf("designation request = %+v, want POST %s", requests[1], wantPath)
			}
			var body generated.CreateBoxDesignationRequestContent
			if err := json.Unmarshal(requests[1].Body, &body); err != nil {
				t.Fatal(err)
			}
			if body.ContactId != 7 {
				t.Errorf("contact_id = %d, want 7", body.ContactId)
			}
		})
	}
}

func TestContactDeliverScreensOutExternalContact(t *testing.T) {
	state := newContactDeliveryServer(t)
	resp, err := runContacts(t, state.server, "deliver", "7", "--to", "screened-out")
	if err != nil {
		t.Fatal(err)
	}
	result := decodeContactData[contactDeliveryResult](t, resp.Data)
	if result.ID != 7 || result.Destination != "screened-out" {
		t.Errorf("result = %+v", result)
	}
	requests := state.recorded.snapshot()
	if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[0].Path != "/contacts/7.json" || requests[1].Method != http.MethodPatch || requests[1].Path != "/contacts/7/clearance.json" {
		t.Fatalf("requests = %+v", requests)
	}
	var body generated.UpdateContactClearanceRequestContent
	if err := json.Unmarshal(requests[1].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "denied" {
		t.Errorf("status = %q, want denied", body.Status)
	}
}

func TestContactDeliverRejectsUnscreenableContactTypes(t *testing.T) {
	for _, contactType := range []string{"User", "Extenzion", "Tombstone", "Calendar::ExternalUser", "", "Unexpected"} {
		t.Run(contactType, func(t *testing.T) {
			state := newContactDeliveryServer(t)
			state.contactType = contactType
			_, err := runContacts(t, state.server, "deliver", "7", "--to", "screened-out")
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeValidation {
				t.Fatalf("error = %v, want validation error", err)
			}
			requests := state.recorded.snapshot()
			if len(requests) != 1 || requests[0].Method != http.MethodGet || requests[0].Path != "/contacts/7.json" {
				t.Fatalf("requests = %+v, want only contact read", requests)
			}
		})
	}
}

func TestContactDeliverAcceptsEveryExternalContactType(t *testing.T) {
	for _, contactType := range []string{"Alias", "Person", "Service"} {
		t.Run(contactType, func(t *testing.T) {
			state := newContactDeliveryServer(t)
			state.contactType = contactType
			if _, err := runContacts(t, state.server, "deliver", "7", "--to", "screened-out"); err != nil {
				t.Fatal(err)
			}
			if requests := state.recorded.snapshot(); len(requests) != 2 || requests[1].Path != "/contacts/7/clearance.json" {
				t.Fatalf("requests = %+v", requests)
			}
		})
	}
}

func TestContactDeliverValidationPrecedesEveryHTTPRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing destination", args: []string{"deliver", "7"}},
		{name: "empty destination", args: []string{"deliver", "7", "--to="}},
		{name: "unsupported destination", args: []string{"deliver", "7", "--to", "later"}},
		{name: "display name", args: []string{"deliver", "7", "--to", "The Feed"}},
		{name: "sdk box kind", args: []string{"deliver", "7", "--to", "feedbox"}},
		{name: "malformed id", args: []string{"deliver", "seven", "--to", "imbox"}},
		{name: "zero id", args: []string{"deliver", "0", "--to", "imbox"}},
		{name: "negative id", args: []string{"deliver", "--account", "2", "--to", "imbox", "--", "-7"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := newContactDeliveryServer(t)
			args := tc.args
			if tc.name != "negative id" {
				args = append(args, "--account", "2")
			}
			_, err := runContacts(t, state.server, args...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage {
				t.Fatalf("error = %v, want usage error", err)
			}
			if requests := state.recorded.snapshot(); len(requests) != 0 {
				t.Fatalf("validation sent HTTP requests: %+v", requests)
			}
		})
	}
}

func TestContactDeliverStopsAfterReadOrWriteFailure(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		statusKey string
		status    int
		boxes     string
		wantPaths []string
	}{
		{name: "box list", args: []string{"deliver", "7", "--to", "feed"}, statusKey: "GET /boxes.json", status: http.StatusInternalServerError, wantPaths: []string{"/boxes.json"}},
		{name: "missing canonical box", args: []string{"deliver", "7", "--to", "feed"}, boxes: `[{"id":11,"kind":"imbox","name":"The Feed"}]`, wantPaths: []string{"/boxes.json"}},
		{name: "designation", args: []string{"deliver", "7", "--to", "feed"}, statusKey: "POST /boxes/22/designations.json", status: http.StatusForbidden, wantPaths: []string{"/boxes.json", "/boxes/22/designations.json"}},
		{name: "contact read", args: []string{"deliver", "7", "--to", "screened-out"}, statusKey: "GET /contacts/7.json", status: http.StatusInternalServerError, wantPaths: []string{"/contacts/7.json"}},
		{name: "contact missing", args: []string{"deliver", "7", "--to", "screened-out"}, statusKey: "GET /contacts/7.json", status: http.StatusNoContent, wantPaths: []string{"/contacts/7.json"}},
		{name: "clearance", args: []string{"deliver", "7", "--to", "screened-out"}, statusKey: "PATCH /contacts/7/clearance.json", status: http.StatusForbidden, wantPaths: []string{"/contacts/7.json", "/contacts/7/clearance.json"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := newContactDeliveryServer(t)
			if tc.statusKey != "" {
				state.recorded.statuses[tc.statusKey] = tc.status
			}
			if tc.boxes != "" {
				state.boxes = tc.boxes
			}
			resp, err := runContacts(t, state.server, tc.args...)
			if err == nil {
				t.Fatal("expected error")
			}
			if resp.OK || resp.Data != nil {
				t.Errorf("failure response = %+v, want no success data", resp)
			}
			requests := state.recorded.snapshot()
			if len(requests) != len(tc.wantPaths) {
				t.Fatalf("requests = %+v", requests)
			}
			for i, want := range tc.wantPaths {
				if requests[i].Path != want {
					t.Errorf("request %d path = %q, want %q", i, requests[i].Path, want)
				}
			}
		})
	}
}

func TestContactDeliverStyledOutput(t *testing.T) {
	state := newContactDeliveryServer(t)
	got, err := runStyledCommand(t, state.server.Config.Handler, "contact", "deliver", "7", "--to", "feed")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Delivery changed for contact 7: The Feed.\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
