package threadload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// testCap keeps the oversized fixtures small: the SDK refuses a declared length past the
// cap on the body's first read, so nothing here allocates a default-sized 16 MiB body.
const testCap int64 = 256

// oversizedMessageSource serves /messages/1.json with the given status and a
// Content-Length past the cap, and answers Message through a real SDK client with the cap
// set to testCap. The statuses used here — 200, 401, 404 — are ones the generated client
// does not retry with backoff; a 429 or a 500 through a real client costs seven seconds
// of sleeps, which is what TestClassifyMessageError covers those shapes without.
func oversizedMessageSource(t *testing.T, status int) (Source, func() int) {
	t.Helper()
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.FormatInt(testCap+1, 10))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"truncated":"`)
	}))
	t.Cleanup(server.Close)

	client := hey.NewClient(&hey.Config{BaseURL: server.URL, CacheEnabled: false},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxResponseBodyBytes(testCap),
		hey.WithMaxRetries(0))
	return NewSDKSource(client), func() int { return reads }
}

func TestSDKSourceMarksAnOversizedMessageOverLimit(t *testing.T) {
	source, reads := oversizedMessageSource(t, http.StatusOK)

	_, err := source.Message(context.Background(), 1)
	if !errors.Is(err, ErrOverLimit) {
		t.Errorf("err = %v, want ErrOverLimit", err)
	}
	if errors.Is(err, ErrSystemic) {
		t.Errorf("err = %v, an oversized body is about one message, not the service", err)
	}
	if reads() != 1 {
		t.Errorf("reads = %d, want 1: an oversized body is not retried", reads())
	}
}

// An oversized error response keeps its status: the SDK wraps the refusal in the *Error
// for the status, and the status is what classifies it. A 401 is the service refusing
// this client, so it is systemic, whether or not its body also blew the cap.
func TestSDKSourceKeepsAnOversizedAuthErrorSystemic(t *testing.T) {
	source, _ := oversizedMessageSource(t, http.StatusUnauthorized)

	_, err := source.Message(context.Background(), 1)
	if !errors.Is(err, ErrSystemic) {
		t.Errorf("err = %v, want ErrSystemic", err)
	}
	if errors.Is(err, ErrOverLimit) {
		t.Errorf("err = %v, an oversized 401 is the service refusing the client, not one message over the limit", err)
	}
}

func TestSDKSourceKeepsAnOversizedNotFoundAnOrdinaryFailure(t *testing.T) {
	source, _ := oversizedMessageSource(t, http.StatusNotFound)

	_, err := source.Message(context.Background(), 1)
	if err == nil {
		t.Fatal("err = nil, want a failure")
	}
	if errors.Is(err, ErrOverLimit) {
		t.Errorf("err = %v, a 404 is a message that is not there, not one over the limit", err)
	}
	if errors.Is(err, ErrSystemic) {
		t.Errorf("err = %v, a 404 is about one message, not the service", err)
	}
}

// refusal is the read error the SDK's capped body ends with, and statusRefusal is the
// *Error CheckResponse builds for an error status whose body was refused — the refusal
// as its Cause, so errors.As finds the status and errors.Is still finds
// ErrResponseTooLarge (the SDK's body_limit tests pin that shape).
func refusal() error {
	return fmt.Errorf("GET /messages/1.json: %w of %d bytes", hey.ErrResponseTooLarge, testCap)
}

func statusRefusal(code string, status int) error {
	return &hey.Error{Code: code, Message: "refused", HTTPStatus: status, Cause: refusal()}
}

// TestClassifyMessageError covers the statuses the generated client would retry with
// seconds of backoff before answering — a 429 and a 500 — against the error shapes the
// SDK hands back, plus the shapes the real-client tests above already prove end to end.
func TestClassifyMessageError(t *testing.T) {
	for _, tt := range []struct {
		name       string
		err        error
		overLimit  bool
		systemicIs bool
	}{
		{"oversized success", refusal(), true, false},
		{"oversized 500", statusRefusal(hey.CodeAPI, 500), false, true},
		{"oversized 429", statusRefusal(hey.CodeRateLimit, 429), false, true},
		{"oversized 404", statusRefusal(hey.CodeNotFound, 404), false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyMessageError(tt.err)
			if errors.Is(err, ErrOverLimit) != tt.overLimit {
				t.Errorf("errors.Is(err, ErrOverLimit) = %v, want %v (err = %v)", !tt.overLimit, tt.overLimit, err)
			}
			if errors.Is(err, ErrSystemic) != tt.systemicIs {
				t.Errorf("errors.Is(err, ErrSystemic) = %v, want %v (err = %v)", !tt.systemicIs, tt.systemicIs, err)
			}
		})
	}
}
