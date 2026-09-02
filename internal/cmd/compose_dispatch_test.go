package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

// lostResponseServer answers everything a send needs and then, on the POST itself,
// reads the whole request body and drops the connection without writing a status —
// the shape of an accepted send whose answer never came back. sendPath selects which
// send is sabotaged, so the same server serves the new-message and the reply paths.
//
// The body is consumed on purpose: that is what makes this the dangerous case rather
// than a dial failure. HEY has the request. Whether it acted on it is unknowable from
// here, which is the whole point.
type lostResponse struct {
	mu        sync.Mutex
	posts     int
	bodyBytes int
	// truncate2xx writes a 200 and a Content-Length it does not honour, so the client
	// sees the status and then loses the connection mid-body.
	truncate2xx bool
}

func (l *lostResponse) counts() (posts, bodyBytes int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.posts, l.bodyBytes
}

func lostResponseServer(t *testing.T, sendPath string) (*httptest.Server, *lostResponse) {
	t.Helper()
	state := &lostResponse{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == sendPath:
			body, _ := io.ReadAll(r.Body)
			state.mu.Lock()
			state.posts++
			state.bodyBytes += len(body)
			truncate := state.truncate2xx
			state.mu.Unlock()

			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("the test server cannot hijack, so the lost-response case cannot be staged")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			if truncate {
				// A status the client will read, then a body that stops short of the
				// length promised for it.
				_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 64\r\n\r\n{\"id\":")
			}
			_ = conn.Close()
		case strings.Contains(r.URL.Path, "identity"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":1,"accounts":[{"id":8,"status":"active"},{"id":9,"status":"active"}],"senders":[{"id":42,"account_id":9,"default":true},{"id":43,"account_id":8,"default":true}]}`)
		case r.URL.Path == "/topics/7.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":7,"account_id":9,"entries":[{"id":11},{"id":12}]}`)
		case strings.HasSuffix(r.URL.Path, "/replies/new.json"):
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		case strings.HasPrefix(r.URL.Path, "/messages/"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, messageAddressedToJane)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, state
}

// assertAmbiguousSend is the contract an indeterminate send answers with: the ambiguous
// code, exit 8, a sentence saying the message may already have gone out, and a hint
// telling the caller to reconcile rather than retry.
func assertAmbiguousSend(t *testing.T, err error) {
	t.Helper()
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("error = %v (%T), want the CLI's typed error", err, err)
	}
	if cliErr.Code != apierr.CodeAmbiguous {
		t.Errorf("code = %q, want %q", cliErr.Code, apierr.CodeAmbiguous)
	}
	if got := output.ExitCodeFor(err); got != output.ExitAmbiguous {
		t.Errorf("exit = %d, want %d", got, output.ExitAmbiguous)
	}
	if !strings.Contains(cliErr.Message, "may have been sent") {
		t.Errorf("message = %q, want it to say the message may have gone out", cliErr.Message)
	}
	if cliErr.Hint == "" {
		t.Fatal("an ambiguous send must carry a reconciliation hint")
	}
	if !strings.Contains(strings.ToLower(cliErr.Hint), "retry") {
		t.Errorf("hint = %q, want it to warn against retrying", cliErr.Hint)
	}
}

// The reviewer's probe, committed. The server consumed one complete POST and then went
// away without answering. Reporting that as a network failure invites a retry, and a
// retry on an endpoint with no idempotency key delivers the message twice.
func TestComposeReportsAnAcceptedSendWithNoAnswerAsAmbiguous(t *testing.T) {
	server, state := lostResponseServer(t, "/messages.json")

	_, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12",
		"-m", "Body.")

	assertAmbiguousSend(t, err)
	posts, bodyBytes := state.counts()
	if posts != 1 {
		t.Errorf("the server saw %d POSTs, want exactly one — nothing here may retry a send", posts)
	}
	if bodyBytes == 0 {
		t.Error("the server read no request body, so this is not the accepted-send case")
	}
}

// The reply path is the same non-idempotent send and gets the same answer.
func TestComposeReplyReportsAnAcceptedSendWithNoAnswerAsAmbiguous(t *testing.T) {
	server, state := lostResponseServer(t, "/entries/12/replies.json")

	_, _, err := runCLIRaw(t, server, "--json", "--account", "8", "compose",
		"--thread-id", "7", "-m", "Body.")

	assertAmbiguousSend(t, err)
	posts, bodyBytes := state.counts()
	if posts != 1 {
		t.Errorf("the server saw %d POSTs, want exactly one", posts)
	}
	if bodyBytes == 0 {
		t.Error("the server read no request body, so this is not the accepted-send case")
	}
}

// A 2xx the client saw and then lost mid-body is the strongest form of this: HEY said
// yes, and the answer naming what it made is gone.
func TestComposeReportsATruncatedSuccessResponseAsAmbiguous(t *testing.T) {
	server, state := lostResponseServer(t, "/messages.json")
	state.truncate2xx = true

	_, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12",
		"-m", "Body.")

	assertAmbiguousSend(t, err)
	if posts, _ := state.counts(); posts != 1 {
		t.Errorf("the server saw %d POSTs, want exactly one", posts)
	}
}

// classifySendFailure is the whole decision, so it is tested as one: everything that
// proves HEY refused the request before acting keeps its own taxonomy, and everything
// that cannot prove it is ambiguous. The default is ambiguous on purpose — an outcome
// this cannot classify is one it cannot rule out.
func TestClassifySendFailureKeepsProvableRejectionsAndAmbiguatesTheRest(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		// Provably never dispatched: the failure happened resolving the sender, which
		// is a read that runs before the send.
		{
			name: "an auth failure before the send was dispatched",
			err:  notDispatched(hey.ErrAuth("Authentication failed")),
			want: apierr.CodeAuth,
		},
		{
			name: "a network failure before the send was dispatched",
			err:  notDispatched(hey.ErrNetwork(errors.New("dial tcp: connection refused"))),
			want: apierr.CodeNetwork,
		},

		// HEY answered, and its answer is authoritative that nothing was created.
		// FromSDK carries an SDK usage error through as an API error, which is this
		// repo's standing decision (see apierr's own tests). What matters here is that
		// it is terminal: HEY was never asked to do anything.
		{name: "the SDK's own usage refusal", err: hey.ErrUsage("a message needs a recipient"), want: apierr.CodeAPI},
		{name: "401", err: hey.ErrAuth("Authentication failed"), want: apierr.CodeAuth},
		{name: "403", err: hey.ErrForbiddenScope(), want: apierr.CodeForbidden},
		{name: "404", err: hey.ErrNotFound("Entry", "12"), want: apierr.CodeNotFound},
		{name: "429", err: hey.ErrRateLimit(30), want: apierr.CodeRateLimit},
		{name: "422", err: hey.ErrValidation("Subject can't be blank"), want: apierr.CodeValidation},
		{name: "409", err: hey.ErrConflict("already sent"), want: apierr.CodeConflict},
		{name: "a plain 400", err: hey.ErrAPI(400, "Bad request"), want: apierr.CodeAPI},
		// A 4xx the SDK did not give a code of its own keeps the API code and its
		// status; the point is that it does not become an ambiguous send.
		{name: "a plain 404 carried as an API error", err: hey.ErrAPI(404, "Not found"), want: apierr.CodeAPI},
		{
			// The status is what it means, size or no size: HEY refused the request.
			name: "an oversized 422 body still carries its status",
			err:  &hey.Error{Code: hey.CodeValidation, HTTPStatus: 422, Message: "invalid", Cause: fmt.Errorf("%w of 16 bytes", hey.ErrResponseTooLarge)},
			want: apierr.CodeValidation,
		},

		// Nothing here proves HEY did not act.
		{name: "a transport failure", err: hey.ErrNetwork(errors.New("EOF")), want: apierr.CodeAmbiguous},
		{name: "500", err: hey.ErrAPI(500, "Server error (500)"), want: apierr.CodeAmbiguous},
		{name: "502", err: &hey.Error{Code: hey.CodeAPI, HTTPStatus: 502, Message: "Gateway error (502)", Retryable: true}, want: apierr.CodeAmbiguous},
		{name: "503", err: &hey.Error{Code: hey.CodeAPI, HTTPStatus: 503, Message: "Gateway error (503)", Retryable: true}, want: apierr.CodeAmbiguous},
		{name: "504", err: &hey.Error{Code: hey.CodeAPI, HTTPStatus: 504, Message: "Gateway error (504)", Retryable: true}, want: apierr.CodeAmbiguous},
		{
			name: "a response that could not be read after HEY accepted it",
			err:  fmt.Errorf("failed to read response: %w", io.ErrUnexpectedEOF),
			want: apierr.CodeAmbiguous,
		},
		{
			name: "a success body past the response cap",
			err:  fmt.Errorf("failed to read response: %w of 16777216 bytes", hey.ErrResponseTooLarge),
			want: apierr.CodeAmbiguous,
		},
		{name: "a deadline that passed mid-request", err: context.DeadlineExceeded, want: apierr.CodeAmbiguous},
		{name: "a cancelled request", err: context.Canceled, want: apierr.CodeAmbiguous},
		{name: "an error nothing here recognises", err: errors.New("something else entirely"), want: apierr.CodeAmbiguous},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifySendFailure(tt.err)
			var cliErr *apierr.Error
			if !errors.As(got, &cliErr) {
				t.Fatalf("error = %v (%T), want the CLI's typed error", got, got)
			}
			if cliErr.Code != tt.want {
				t.Fatalf("code = %q, want %q (message %q)", cliErr.Code, tt.want, cliErr.Message)
			}
			if tt.want == apierr.CodeAmbiguous {
				assertAmbiguousSend(t, got)
				return
			}
			// A rejection keeps its own words rather than being dressed up as a
			// possible send.
			if strings.Contains(cliErr.Message, "may have been sent") {
				t.Errorf("message = %q, want a plain rejection", cliErr.Message)
			}
		})
	}
}

// The taxonomy survives end to end, not only in the classifier: a status HEY answers
// with reaches the caller as the code that status means.
func TestComposeKeepsEachAnsweredStatusInItsOwnLane(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantCode string
		wantExit int
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, wantCode: apierr.CodeRateLimit, wantExit: output.ExitRateLimit},
		{name: "forbidden", status: http.StatusForbidden, wantCode: apierr.CodeForbidden, wantExit: output.ExitForbidden},
		{name: "not found", status: http.StatusNotFound, wantCode: apierr.CodeNotFound, wantExit: output.ExitNotFound},
		{name: "unprocessable", status: http.StatusUnprocessableEntity, wantCode: apierr.CodeAPI, wantExit: output.ExitAPI},
		{name: "server error", status: http.StatusInternalServerError, wantCode: apierr.CodeAmbiguous, wantExit: output.ExitAmbiguous},
		{name: "bad gateway", status: http.StatusBadGateway, wantCode: apierr.CodeAmbiguous, wantExit: output.ExitAmbiguous},
		{name: "service unavailable", status: http.StatusServiceUnavailable, wantCode: apierr.CodeAmbiguous, wantExit: output.ExitAmbiguous},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, sent := composeSendServer(t)
			sent.SendStatus = tt.status
			sent.SendLocation = ""

			_, _, err := runCLIRaw(t, server, "--json", "compose",
				"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12",
				"-m", "Body.")

			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("error = %v, want the CLI's typed error", err)
			}
			if cliErr.Code != tt.wantCode {
				t.Errorf("code = %q, want %q (message %q)", cliErr.Code, tt.wantCode, cliErr.Message)
			}
			if got := output.ExitCodeFor(err); got != tt.wantExit {
				t.Errorf("exit = %d, want %d", got, tt.wantExit)
			}
			if sent.Reads != 0 {
				t.Errorf("readbacks = %d, want none — no send was confirmed", sent.Reads)
			}
		})
	}
}

// A failure before the send goes out keeps its own taxonomy, and the proof is that the
// server never saw a POST at all.
func TestComposeKeepsAPreDispatchFailureOutOfTheAmbiguousLane(t *testing.T) {
	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			t.Errorf("nothing should have been posted: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	_, _, err := runCLIRaw(t, server, "--json", "compose",
		"--to", "alice@example.com", "--subject", "Inovo Customer Update — Week 12",
		"-m", "Body.")

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("error = %v, want the CLI's typed error", err)
	}
	if cliErr.Code != apierr.CodeAuth {
		t.Errorf("code = %q, want %q — the sender could not be resolved, so nothing was sent",
			cliErr.Code, apierr.CodeAuth)
	}
	if posts != 0 {
		t.Errorf("the server saw %d POSTs, want none", posts)
	}
}

// The ambiguity belongs to the send, not to the CLI. A read that cannot reach HEY —
// the same transport failure that makes a send indeterminate — keeps whatever taxonomy
// it always had, because reading again costs nothing and delivers nothing. The
// classifier is wired to the two send call sites and nowhere else, and this is what
// says so.
func TestAFailedReadIsNeverAnAmbiguousSend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close() // nothing is listening, so every request fails to connect

	_, _, err := runCLIRaw(t, server, "--json", "thread", "read", "7")
	if err == nil {
		t.Fatal("a read against nothing must fail")
	}

	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("error = %v, want the CLI's typed error", err)
	}
	if cliErr.Code == apierr.CodeAmbiguous {
		t.Errorf("code = %q, want a read to keep its own taxonomy", cliErr.Code)
	}
	if got := output.ExitCodeFor(err); got == output.ExitAmbiguous {
		t.Errorf("exit = %d, want anything but the ambiguous-send code", got)
	}
	if strings.Contains(cliErr.Message, "may have been sent") {
		t.Errorf("message = %q, want nothing about sending", cliErr.Message)
	}
}
