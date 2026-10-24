package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

// refusingDeliveries answers every delivery the way HEY does when it refuses one — the
// account has reached its sending limit — in front of a test server that would have
// delivered it: a 302 to the draft's edit page, whatever the format asked for. That page
// serves the draft (messages/_message.jbuilder: an id and no thread), and so does its
// JSON, which is how the command confirms the message is still a draft.
func refusingDeliveries(inner http.Handler, draftID int64) http.Handler {
	edit := fmt.Sprintf("/messages/%d/edit", draftID)
	draft := fmt.Sprintf(`{"id":%d,"subject":"Board update","content":"<div>Numbers to follow.</div>",`+
		`"addressed":{"directly":[{"id":7,"name":"Maria Delgado","email_address":"maria@example.com"}]}}`, draftID)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case delivering(r):
			w.Header().Set("Location", edit)
			w.WriteHeader(http.StatusFound)
		case r.Method == http.MethodGet && (r.URL.Path == edit || r.URL.Path == edit+".json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, draft)
		default:
			inner.ServeHTTP(w, r)
		}
	})
}

// delivering reports whether r asks HEY to deliver a message: a new one, a reply, or a
// draft sent. A draft saved asks for status drafted, and is not one.
func delivering(r *http.Request) bool {
	switch {
	case r.Method == http.MethodPost && (r.URL.Path == "/messages.json" || strings.HasSuffix(r.URL.Path, "/replies.json")):
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/messages/"):
	default:
		return false
	}
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	return !strings.Contains(string(body), `"drafted"`)
}

// assertNotDelivered checks that a refused send is reported as one: not_delivered, exit 7,
// the draft HEY kept named in the error and in the command that sends it.
func assertNotDelivered(t *testing.T, err error, draftID int64) {
	t.Helper()
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("err = %v, want the refusal reported", err)
	}
	if cliErr.Code != apierr.CodeNotDelivered {
		t.Errorf("code = %q, want %q", cliErr.Code, apierr.CodeNotDelivered)
	}
	if code := output.ExitCodeFor(err); code != output.ExitAPI {
		t.Errorf("exit code = %d, want %d", code, output.ExitAPI)
	}
	if want := fmt.Sprintf("hey draft send %d", draftID); cliErr.Hint != want {
		t.Errorf("hint = %q, want %q", cliErr.Hint, want)
	}
	if cliErr.Meta["draft_id"] != draftID {
		t.Errorf("meta = %#v, want draft_id %d", cliErr.Meta, draftID)
	}
	if !strings.Contains(cliErr.Message, fmt.Sprintf("draft %d", draftID)) {
		t.Errorf("message = %q, want it to name the draft", cliErr.Message)
	}
}

// HEY refuses a delivery by keeping the message as a draft and redirecting to it. Every
// command that delivers a message used to follow that redirect and report the draft as
// a message sent; each now says it was not sent and which draft holds it.
func TestARefusedDeliveryIsNotReportedSent(t *testing.T) {
	t.Run("compose", func(t *testing.T) {
		var writes []draftWrite
		_, err := runJSONCommand(t, refusingDeliveries(draftLifecycleServer(t, draftEditJSON, &writes), 2201),
			"compose", "--to", "maria@example.com", "--subject", "Board update", "-m", "Numbers to follow.")
		assertNotDelivered(t, err, 2201)
	})

	t.Run("compose --from", func(t *testing.T) {
		var writes []draftWrite
		_, err := runJSONCommand(t, refusingDeliveries(senderServer(t, senderIdentity, &writes, nil), 2201),
			"compose", "--from", "billing@example.org", "--to", "maria@example.com", "--subject", "Board update", "-m", "Numbers.")
		assertNotDelivered(t, err, 2201)
	})

	t.Run("draft send", func(t *testing.T) {
		var writes []draftWrite
		_, err := runJSONCommand(t, refusingDeliveries(draftLifecycleServer(t, draftEditJSON, &writes), 12345),
			"draft", "send", "12345")
		assertNotDelivered(t, err, 12345)
	})

	for _, command := range [][]string{
		{"reply", "7", "-m", "Thursday works."},
		{"compose", "--thread-id", "7", "-m", "Thursday works."},
	} {
		t.Run(strings.Join(command[:2], " "), func(t *testing.T) {
			server, _ := threadReplyServer(t, messageAddressedToJane, 11, 12)
			server.Config.Handler = refusingDeliveries(server.Config.Handler, 2202)
			_, err := runCLIOutput(t, server, append([]string{"--account", "8"}, command...)...)
			assertNotDelivered(t, err, 2202)
		})
	}

	t.Run("forward", func(t *testing.T) {
		server, _ := forwardServer(t, `[{"id":11},{"id":12}]`)
		server.Config.Handler = refusingDeliveries(server.Config.Handler, 2203)
		_, err := runCLIOutput(t, server, "--account", "8", "forward", "7", "--to", "alice@example.com")
		assertNotDelivered(t, err, 2203)
	})
}

// An answer that names an entry and no thread is only taken for a refusal once HEY says
// the entry is still a draft. One it answers 422 for went out — Undo Send's held ones
// included — and one whose edit page cannot be read is taken as sent too: reporting a
// sent message as unsent would have somebody send it twice.
func TestADeliveryIsReportedSentUnlessHEYConfirmsTheDraft(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
	}{
		{"went out", http.StatusUnprocessableEntity},
		{"could not be checked", http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var writes []draftWrite
			inner := draftLifecycleServer(t, draftEditJSON, &writes)
			server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/messages/2201/edit.json" {
					w.WriteHeader(tt.status)
					return
				}
				inner.ServeHTTP(w, r)
			})
			response, err := runJSONCommand(t, answeringDeliveries(server, `{"id":2201,"subject":"Board update"}`),
				"compose", "--to", "maria@example.com", "--subject", "Board update", "-m", "Numbers to follow.")
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			if response.Summary != "Message sent" {
				t.Errorf("summary = %q, want the send reported", response.Summary)
			}
		})
	}
}
