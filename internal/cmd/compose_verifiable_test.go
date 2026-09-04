package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
)

type verifiableComposeState struct {
	draftWrites   int
	draftReads    int
	sendWrites    int
	messageReads  int
	requests      []string
	accountScopes map[string][]string

	sendStatus            int
	readStatus            int
	readJSON              string
	unauthorizedFirstSend bool
	refreshRequests       int

	draftBody map[string]any
	sendBody  map[string]any
}

type verifiableReadback struct {
	messageID    int64
	url          string
	senderID     int64
	subject      string
	content      string
	to           []string
	cc           []string
	bcc          []string
	bccDisclosed bool
}

func exactVerifiableReadback() verifiableReadback {
	return verifiableReadback{
		messageID:    12345,
		url:          "https://app.hey.com/topics/7742",
		senderID:     42,
		subject:      "Inovo Customer Update — Week 12",
		content:      "<p>Body.</p>",
		to:           []string{"alice@example.com"},
		cc:           []string{"bob@example.com"},
		bcc:          []string{"carol@example.org"},
		bccDisclosed: true,
	}
}

func encodeVerifiableReadback(t *testing.T, readback verifiableReadback) string {
	t.Helper()
	addressed := map[string]any{
		"directly": contactsFor(readback.to),
		"copied":   contactsFor(readback.cc),
	}
	if readback.bccDisclosed {
		addressed["blindcopied"] = contactsFor(readback.bcc)
	}
	payload, err := json.Marshal(map[string]any{
		"id":      readback.messageID,
		"subject": readback.subject,
		"content": readback.content,
		"url":     readback.url,
		"sender": map[string]any{
			"id": readback.senderID, "name": "Nova Desk", "email_address": "nova@example.com",
		},
		"addressed": addressed,
	})
	if err != nil {
		t.Fatalf("encode verifiable readback: %v", err)
	}
	return string(payload)
}

func encodeCreatedDraft(t *testing.T, body map[string]any) string {
	t.Helper()
	message, _ := body["message"].(map[string]any)
	entry, _ := body["entry"].(map[string]any)
	addressed, _ := entry["addressed"].(map[string]any)
	contacts := func(raw any) []map[string]any {
		values, _ := raw.([]any)
		out := make([]map[string]any, 0, len(values))
		for i, value := range values {
			address, _ := value.(string)
			out = append(out, map[string]any{"id": i + 1, "email_address": address})
		}
		return out
	}
	payload, err := json.Marshal(map[string]any{
		"id":      12345,
		"subject": message["subject"],
		"content": message["content"],
		"sender":  map[string]any{"id": body["acting_sender_id"]},
		"addressed": map[string]any{
			"directly":    contacts(addressed["directly"]),
			"copied":      contacts(addressed["copied"]),
			"blindcopied": contacts(addressed["blindcopied"]),
		},
	})
	if err != nil {
		t.Fatalf("encode created draft: %v", err)
	}
	return string(payload)
}

func verifiableComposeServer(t *testing.T, state *verifiableComposeState) *httptest.Server {
	t.Helper()
	if state.accountScopes == nil {
		state.accountScopes = make(map[string][]string)
	}
	defaultReadback := encodeVerifiableReadback(t, exactVerifiableReadback())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.requests = append(state.requests, r.Method+" "+r.URL.Path)
		state.accountScopes[r.Method+" "+r.URL.Path] = append(
			state.accountScopes[r.Method+" "+r.URL.Path], r.URL.Query().Get("filtered_account_id"))

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/identity.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":1,"accounts":[{"id":840304,"status":"active"}],"senders":[{"id":42,"account_id":840304,"default":true}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/messages.json":
			state.draftWrites++
			_ = json.NewDecoder(r.Body).Decode(&state.draftBody)
			w.Header().Set("Location", "https://app.hey.com/messages/12345")
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/messages/12345/edit.json":
			state.draftReads++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, encodeCreatedDraft(t, state.draftBody))
		case r.Method == http.MethodPut && r.URL.Path == "/messages/12345.json":
			state.sendWrites++
			_ = json.NewDecoder(r.Body).Decode(&state.sendBody)
			if state.unauthorizedFirstSend && state.sendWrites == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			status := state.sendStatus
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
		case r.Method == http.MethodPost && r.URL.Path == "/oauth/tokens":
			state.refreshRequests++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/messages/12345.json":
			state.messageReads++
			status := state.readStatus
			if status == 0 {
				status = http.StatusOK
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if state.readJSON != "" {
				fmt.Fprint(w, state.readJSON)
				return
			}
			fmt.Fprint(w, defaultReadback)
		default:
			t.Errorf("unexpected request (no search or discovery is allowed): %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func runVerifiableCompose(t *testing.T, server *httptest.Server) (stdout, stderr string, err error) {
	t.Helper()
	return runCLIRaw(t, server, "--json", "--account", "840304", "compose", "--verifiable",
		"--to", "alice@example.com", "--cc", "bob@example.com", "--bcc", "carol@example.org",
		"--subject", "Inovo Customer Update — Week 12", "-m", "Body.")
}

func assertVerifiableRequestLedger(t *testing.T, state *verifiableComposeState) {
	t.Helper()
	if state.draftWrites != 1 || state.draftReads != 1 || state.sendWrites != 1 || state.messageReads != 1 {
		t.Fatalf("draft writes/reads = %d/%d sends = %d message reads = %d, want 1/1/1/1",
			state.draftWrites, state.draftReads, state.sendWrites, state.messageReads)
	}
	if got := strings.Join(state.requests, ", "); got != "GET /identity.json, POST /messages.json, GET /messages/12345/edit.json, PUT /messages/12345.json, GET /messages/12345.json" {
		t.Fatalf("requests = %s, want only identity, known draft create, one draft send, and exact message read", got)
	}
	for route, scopes := range state.accountScopes {
		for _, scope := range scopes {
			if route == "GET /identity.json" {
				if scope != "" {
					t.Errorf("identity scope = %q, want unscoped account validation", scope)
				}
				continue
			}
			if scope != "840304" {
				t.Errorf("%s scope = %q, want account 840304", route, scope)
			}
		}
	}
}

func allVerifiableChecks(value bool) map[string]bool {
	return map[string]bool{
		"readable":            value,
		"message_id":          value,
		"delivery_topic":      value,
		"verification_status": value,
		"sender":              value,
		"subject":             value,
		"to":                  value,
		"cc":                  value,
		"bcc_disclosed":       value,
		"bcc":                 value,
		"body":                value,
	}
}

func verifiableChecksWith(overrides map[string]bool) map[string]bool {
	checks := allVerifiableChecks(true)
	for name, value := range overrides {
		checks[name] = value
	}
	return checks
}

func assertUnknownVerifiableCompose(t *testing.T, stdout string, err error, state *verifiableComposeState, wantChecks map[string]bool) {
	t.Helper()
	if stdout != "" {
		t.Errorf("stdout = %q, want no success envelope", stdout)
	}
	assertAmbiguousSend(t, err)

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("error = %v, want the CLI's typed error", err)
	}
	if len(cliErr.Meta) != 2 {
		t.Errorf("metadata keys = %d (%#v), want only message_id and boolean reconciliation", len(cliErr.Meta), cliErr.Meta)
	}
	if cliErr.Meta["message_id"] != int64(12345) {
		t.Errorf("metadata = %#v, want only the known message ID and checks", cliErr.Meta)
	}

	encodedChecks, marshalErr := json.Marshal(cliErr.Meta["reconciliation"])
	if marshalErr != nil {
		t.Fatalf("marshal reconciliation checks: %v", marshalErr)
	}
	var checks map[string]any
	if unmarshalErr := json.Unmarshal(encodedChecks, &checks); unmarshalErr != nil {
		t.Fatalf("decode reconciliation checks: %v", unmarshalErr)
	}
	if len(checks) != len(wantChecks) {
		t.Errorf("reconciliation = %s, want exactly %d boolean checks", encodedChecks, len(wantChecks))
	}
	for name, want := range wantChecks {
		got, exists := checks[name]
		if !exists {
			t.Errorf("reconciliation missing %q: %s", name, encodedChecks)
			continue
		}
		gotBool, boolean := got.(bool)
		if !boolean {
			t.Errorf("reconciliation[%q] = %T, want bool", name, got)
			continue
		}
		if gotBool != want {
			t.Errorf("reconciliation[%q] = %v, want %v", name, gotBool, want)
		}
	}
	for name, value := range checks {
		if _, boolean := value.(bool); !boolean {
			t.Errorf("reconciliation[%q] = %T, want bounded boolean-only metadata", name, value)
		}
	}

	encodedMeta, marshalErr := json.Marshal(cliErr.Meta)
	if marshalErr != nil {
		t.Fatalf("marshal ambiguity metadata: %v", marshalErr)
	}
	if len(encodedMeta) > 1024 {
		t.Errorf("ambiguity metadata is %d bytes, want a bounded diagnostic", len(encodedMeta))
	}
	for _, privateValue := range []string{"alice@example.com", "bob@example.com", "carol@example.org", "Inovo Customer Update", "<p>Body.</p>"} {
		if strings.Contains(string(encodedMeta), privateValue) {
			t.Errorf("ambiguity metadata leaked message data %q: %s", privateValue, encodedMeta)
		}
	}
	assertVerifiableRequestLedger(t, state)
}

func TestComposeVerifiableCreatesAKnownDraftThenSendsAndReadsOnlyThatID(t *testing.T) {
	state := &verifiableComposeState{}
	server := verifiableComposeServer(t, state)

	stdout, _, err := runVerifiableCompose(t, server)
	if err != nil {
		t.Fatalf("compose --verifiable: %v", err)
	}

	envelope := composeJSON(t, stdout)
	if !envelope.OK || !envelope.Data.Sent {
		t.Fatalf("envelope = %+v, want a verified send", envelope)
	}
	if envelope.Data.MessageID != 12345 || envelope.Data.TopicID != 7742 || envelope.Data.AppURL != "https://app.hey.com/topics/7742" {
		t.Errorf("handle = message %d topic %d url %q", envelope.Data.MessageID, envelope.Data.TopicID, envelope.Data.AppURL)
	}
	if envelope.Data.Verification.Status != verificationVerified {
		t.Errorf("verification = %+v, want verified", envelope.Data.Verification)
	}
	if !envelope.Data.Verification.Recipients.BCCDisclosed {
		t.Error("the exact BCC readback must be disclosed")
	}
	assertVerifiableRequestLedger(t, state)

	draftEntry, _ := state.draftBody["entry"].(map[string]any)
	if draftEntry["status"] != "drafted" {
		t.Errorf("draft entry status = %v, want drafted", draftEntry["status"])
	}
	sendEntry, _ := state.sendBody["entry"].(map[string]any)
	if _, drafted := sendEntry["status"]; drafted {
		t.Errorf("send entry status = %v, want omitted to deliver", sendEntry["status"])
	}
	if state.draftBody["acting_sender_id"] != float64(42) || state.sendBody["acting_sender_id"] != float64(42) {
		t.Errorf("acting sender changed: draft=%v send=%v", state.draftBody["acting_sender_id"], state.sendBody["acting_sender_id"])
	}
}

func TestComposeVerifiableDoesNotRefreshAndResendARejectedDelivery(t *testing.T) {
	state := &verifiableComposeState{unauthorizedFirstSend: true}
	server := verifiableComposeServer(t, state)
	configHome := t.TempDir()
	t.Setenv("HEY_NO_KEYRING", "1")
	manager := auth.NewManager(server.URL, server.Client(), filepath.Join(configHome, "hey-cli"))
	if err := manager.GetStore().Save(manager.CredentialKey(), &auth.Credentials{
		AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}

	stdout, _, err := runAuthCommand(t, configHome, server.URL, "", true,
		"--account", "840304", "compose", "--verifiable",
		"--to", "alice@example.com", "--cc", "bob@example.com", "--bcc", "carol@example.org",
		"--subject", "Inovo Customer Update — Week 12", "-m", "Body.")
	if err == nil {
		t.Fatal("compose --verifiable accepted a rejected delivery")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want no success envelope", stdout)
	}
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code == apierr.CodeAmbiguous {
		t.Fatalf("error = %v, want a terminal non-success result", err)
	}
	if state.sendWrites != 1 || state.refreshRequests != 0 || state.messageReads != 0 {
		t.Fatalf("send writes/refreshes/readbacks = %d/%d/%d, want 1/0/0",
			state.sendWrites, state.refreshRequests, state.messageReads)
	}
	if got := strings.Join(state.requests, ", "); strings.Contains(got, "search") {
		t.Fatalf("requests = %s, want no search or fallback", got)
	}
}

func TestComposeVerifiableAcceptsAnExplicitlyEmptyExactBCC(t *testing.T) {
	readback := exactVerifiableReadback()
	readback.bcc = nil
	state := &verifiableComposeState{readJSON: encodeVerifiableReadback(t, readback)}
	server := verifiableComposeServer(t, state)

	stdout, _, err := runCLIRaw(t, server, "--json", "--account", "840304", "compose", "--verifiable",
		"--to", "alice@example.com", "--cc", "bob@example.com",
		"--subject", "Inovo Customer Update — Week 12", "-m", "Body.")
	if err != nil {
		t.Fatalf("compose --verifiable with an explicitly empty BCC readback: %v", err)
	}
	verification := composeJSON(t, stdout).Data.Verification
	if verification.Status != verificationVerified || !verification.Recipients.BCCDisclosed || len(verification.Recipients.BCC) != 0 {
		t.Errorf("verification = %+v, want a verified, explicitly empty BCC", verification)
	}
	assertVerifiableRequestLedger(t, state)
}

func TestComposeVerifiableReconcilesAnAmbiguousSendByItsKnownID(t *testing.T) {
	state := &verifiableComposeState{sendStatus: http.StatusInternalServerError}
	server := verifiableComposeServer(t, state)

	stdout, _, err := runVerifiableCompose(t, server)
	if err != nil {
		t.Fatalf("the exact known-ID readback should reconcile the ambiguous send: %v", err)
	}
	envelope := composeJSON(t, stdout)
	if envelope.Data.MessageID != 12345 || envelope.Data.Verification.Status != verificationVerified {
		t.Errorf("result = %+v, want the verified draft ID 12345", envelope.Data)
	}
	assertVerifiableRequestLedger(t, state)
}

func TestComposeVerifiableLeavesAnUnreadableKnownIDUnknownWithoutRetrying(t *testing.T) {
	state := &verifiableComposeState{readStatus: http.StatusNotFound, readJSON: `{"message":"not found"}`}
	server := verifiableComposeServer(t, state)

	stdout, _, err := runVerifiableCompose(t, server)
	assertUnknownVerifiableCompose(t, stdout, err, state, allVerifiableChecks(false))
}

func TestComposeVerifiableRequiresEveryExactReadbackIdentityCheck(t *testing.T) {
	tests := []struct {
		name       string
		change     func(*verifiableReadback)
		wantChecks map[string]bool
	}{
		{
			name: "wrong message id",
			change: func(readback *verifiableReadback) {
				readback.messageID = 99999
			},
			wantChecks: verifiableChecksWith(map[string]bool{"message_id": false}),
		},
		{
			name: "no delivered topic",
			change: func(readback *verifiableReadback) {
				readback.url = "https://app.hey.com/messages/12345/edit"
			},
			wantChecks: verifiableChecksWith(map[string]bool{"delivery_topic": false}),
		},
		{
			name: "wrong sender",
			change: func(readback *verifiableReadback) {
				readback.senderID = 99
			},
			wantChecks: verifiableChecksWith(map[string]bool{"sender": false}),
		},
		{
			name: "wrong subject",
			change: func(readback *verifiableReadback) {
				readback.subject = "A different subject"
			},
			wantChecks: verifiableChecksWith(map[string]bool{"verification_status": false, "subject": false}),
		},
		{
			name: "wrong To",
			change: func(readback *verifiableReadback) {
				readback.to = []string{"mallory@example.com"}
			},
			wantChecks: verifiableChecksWith(map[string]bool{"verification_status": false, "to": false}),
		},
		{
			name: "wrong CC",
			change: func(readback *verifiableReadback) {
				readback.cc = []string{"dana@example.org"}
			},
			wantChecks: verifiableChecksWith(map[string]bool{"verification_status": false, "cc": false}),
		},
		{
			name: "undisclosed BCC",
			change: func(readback *verifiableReadback) {
				readback.bccDisclosed = false
			},
			wantChecks: verifiableChecksWith(map[string]bool{"bcc_disclosed": false, "bcc": false}),
		},
		{
			name: "changed BCC",
			change: func(readback *verifiableReadback) {
				readback.bcc = []string{"dana@example.org"}
			},
			wantChecks: verifiableChecksWith(map[string]bool{"verification_status": false, "bcc": false}),
		},
		{
			name: "changed body",
			change: func(readback *verifiableReadback) {
				readback.content = "<p>Something else.</p>"
			},
			wantChecks: verifiableChecksWith(map[string]bool{"verification_status": false, "body": false}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readback := exactVerifiableReadback()
			test.change(&readback)
			state := &verifiableComposeState{readJSON: encodeVerifiableReadback(t, readback)}
			server := verifiableComposeServer(t, state)

			stdout, _, err := runVerifiableCompose(t, server)
			assertUnknownVerifiableCompose(t, stdout, err, state, test.wantChecks)
		})
	}
}

func TestComposeVerifiableRejectsDuplicateRecipientSubstitution(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		change      func(*verifiableReadback)
		failedCheck string
	}{
		{
			name:        "To",
			args:        []string{"--to", "alice@example.com,dana@example.com", "--cc", "bob@example.com", "--bcc", "carol@example.org"},
			change:      func(r *verifiableReadback) { r.to = []string{"alice@example.com", "alice@example.com"} },
			failedCheck: "to",
		},
		{
			name:        "CC",
			args:        []string{"--to", "alice@example.com", "--cc", "bob@example.com,dana@example.com", "--bcc", "carol@example.org"},
			change:      func(r *verifiableReadback) { r.cc = []string{"bob@example.com", "bob@example.com"} },
			failedCheck: "cc",
		},
		{
			name:        "BCC",
			args:        []string{"--to", "alice@example.com", "--cc", "bob@example.com", "--bcc", "carol@example.org,dana@example.com"},
			change:      func(r *verifiableReadback) { r.bcc = []string{"carol@example.org", "carol@example.org"} },
			failedCheck: "bcc",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readback := exactVerifiableReadback()
			test.change(&readback)
			state := &verifiableComposeState{readJSON: encodeVerifiableReadback(t, readback)}
			server := verifiableComposeServer(t, state)
			args := []string{"--json", "--account", "840304", "compose", "--verifiable"}
			args = append(args, test.args...)
			args = append(args, "--subject", "Inovo Customer Update — Week 12", "-m", "Body.")
			stdout, _, err := runCLIRaw(t, server, args...)
			want := map[string]bool{test.failedCheck: false}
			if test.name != "BCC" {
				want["verification_status"] = false
			}
			assertUnknownVerifiableCompose(t, stdout, err, state, verifiableChecksWith(want))
		})
	}
}

func TestComposeVerifiableRejectsUnverifiableInputsBeforeAnyWrite(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "attachment", args: []string{"-m", "Body.", "--attach", "report.pdf"}},
		{name: "raw HTML", args: []string{"--message-html", "<p>Body.</p>"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := &verifiableComposeState{}
			server := verifiableComposeServer(t, state)
			args := []string{"--json", "--account", "840304", "compose", "--verifiable",
				"--to", "alice@example.com", "--subject", "Test"}
			args = append(args, test.args...)
			_, _, err := runCLIRaw(t, server, args...)
			if err == nil || !strings.Contains(err.Error(), "--verifiable") {
				t.Fatalf("error = %v, want a --verifiable incompatibility", err)
			}
			if got := strings.Join(state.requests, ", "); got != "GET /identity.json" {
				t.Fatalf("requests = %v, want only read-only account identity before failure", state.requests)
			}
		})
	}
}

func TestComposeExplicitFalseFlagsDoNotConflict(t *testing.T) {
	state := &verifiableComposeState{}
	server := verifiableComposeServer(t, state)
	_, _, err := runCLIRaw(t, server, "--json", "--account", "840304", "compose",
		"--verifiable=false", "--draft=false", "--to", "alice@example.com",
		"--cc", "bob@example.com", "--bcc", "carol@example.org",
		"--subject", "Inovo Customer Update — Week 12", "-m", "Body.")
	if err != nil {
		t.Fatalf("explicitly false flags must leave direct compose enabled: %v", err)
	}
}

func TestComposeVerifiableRejectsHTMLMarkupPassedAsMarkdown(t *testing.T) {
	for _, message := range []string{
		`<p data-private="secret">Body.</p>`,
		`<action-text-attachment sgid="original-sgid" content-type="application/pdf" filename="report.pdf" filesize="7"></action-text-attachment>`,
	} {
		state := &verifiableComposeState{}
		server := verifiableComposeServer(t, state)
		_, _, err := runCLIRaw(t, server, "--json", "--account", "840304", "compose", "--verifiable",
			"--to", "alice@example.com", "--subject", "Test", "-m", message)
		if err == nil {
			t.Fatal("verifiable compose accepted HTML markup through the Markdown input")
		}
		if state.draftWrites != 0 || state.sendWrites != 0 {
			t.Fatalf("draft/send writes = %d/%d, want 0/0", state.draftWrites, state.sendWrites)
		}
	}
}

func TestComposeVerifiableRejectsTopicOutsideCanonicalPath(t *testing.T) {
	for _, rawURL := range []string{
		"https://app.hey.com/messages/12345/edit?return_to=/topics/7742",
		"https://app.hey.com/messages/12345/topics/7742",
		"https://app.hey.com/topics/7742/",
		"https://app.hey.com/topics/+7742",
		"topics/7742",
	} {
		readback := exactVerifiableReadback()
		readback.url = rawURL
		state := &verifiableComposeState{readJSON: encodeVerifiableReadback(t, readback)}
		server := verifiableComposeServer(t, state)
		stdout, _, err := runVerifiableCompose(t, server)
		assertUnknownVerifiableCompose(t, stdout, err, state,
			verifiableChecksWith(map[string]bool{"delivery_topic": false}))
	}
}

func TestComposeVerifiableDoesNotReplaceExplicitSenderIDWithCreator(t *testing.T) {
	state := &verifiableComposeState{readJSON: `{
		"id":12345,"url":"https://app.hey.com/topics/7742",
		"subject":"Inovo Customer Update — Week 12","content":"<p>Body.</p>",
		"sender":{"id":99},
		"creator":{"id":42,"name":"Nova Desk","email_address":"nova@example.com"},
		"addressed":{
			"directly":[{"id":1,"email_address":"alice@example.com"}],
			"copied":[{"id":2,"email_address":"bob@example.com"}],
			"blindcopied":[{"id":3,"email_address":"carol@example.org"}]
		}
	}`}
	server := verifiableComposeServer(t, state)
	stdout, _, err := runVerifiableCompose(t, server)
	assertUnknownVerifiableCompose(t, stdout, err, state,
		verifiableChecksWith(map[string]bool{"sender": false}))
}

func TestComposeVerifiableRequiresExplicitSenderInReadback(t *testing.T) {
	state := &verifiableComposeState{readJSON: `{
		"id":12345,"url":"https://app.hey.com/topics/7742",
		"subject":"Inovo Customer Update — Week 12","content":"<p>Body.</p>",
		"creator":{"id":42,"name":"Nova Desk","email_address":"nova@example.com"},
		"addressed":{
			"directly":[{"id":1,"email_address":"alice@example.com"}],
			"copied":[{"id":2,"email_address":"bob@example.com"}],
			"blindcopied":[{"id":3,"email_address":"carol@example.org"}]
		}
	}`}
	server := verifiableComposeServer(t, state)
	stdout, _, err := runVerifiableCompose(t, server)
	assertUnknownVerifiableCompose(t, stdout, err, state,
		verifiableChecksWith(map[string]bool{"sender": false}))
}

func TestComposeVerifiableRejectsRecipientContactWithoutAddress(t *testing.T) {
	state := &verifiableComposeState{readJSON: `{
		"id":12345,"url":"https://app.hey.com/topics/7742",
		"subject":"Inovo Customer Update — Week 12","content":"<p>Body.</p>",
		"sender":{"id":42,"name":"Nova Desk","email_address":"nova@example.com"},
		"addressed":{
			"directly":[{"id":1,"email_address":"alice@example.com"},{"id":999,"name":"Undisclosed recipient"}],
			"copied":[{"id":2,"email_address":"bob@example.com"}],
			"blindcopied":[{"id":3,"email_address":"carol@example.org"}]
		}
	}`}
	server := verifiableComposeServer(t, state)
	_, _, err := runVerifiableCompose(t, server)
	if err == nil {
		t.Fatal("verifiable compose accepted a recipient contact with no address")
	}
	assertVerifiableRequestLedger(t, state)
}

func TestComposeVerifiableRejectsNumericNonMessageDraftLocation(t *testing.T) {
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/identity.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":1,"accounts":[{"id":840304,"status":"active"}],"senders":[{"id":42,"account_id":840304,"default":true}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/messages.json":
			w.Header().Set("Location", "https://app.hey.com/topics/12345")
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/messages/12345/edit.json":
			http.NotFound(w, r)
		case r.Method == http.MethodPut && r.URL.Path == "/messages/12345.json":
			puts++
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	_, _, err := runVerifiableCompose(t, server)
	if err == nil || puts != 0 {
		t.Fatalf("numeric /topics Location was accepted as a draft message id: err=%v PUTs=%d", err, puts)
	}
}
