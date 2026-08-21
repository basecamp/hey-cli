package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// Every code an SDK failure can arrive as, with the exit code the shell sees and
// the envelope a script parses. The mapping used to be a switch over string
// literals on one side and a switch over string literals on the other, so a typo
// exited 1 and said nothing; this is what would catch that.
func TestExitCodeAndEnvelopeForSDKErrors(t *testing.T) {
	tests := []struct {
		name     string
		sdkErr   *hey.Error
		wantExit int
		wantCode string
		wantHint string
	}{
		{"auth", &hey.Error{Code: hey.CodeAuth, Message: "not authenticated", HTTPStatus: 401}, ExitAuth, apierr.CodeAuth, "Run: hey auth login"},
		{"not found", &hey.Error{Code: hey.CodeNotFound, Message: "topic 3011 not found", HTTPStatus: 404}, ExitNotFound, apierr.CodeNotFound, ""},
		{"forbidden", &hey.Error{Code: hey.CodeForbidden, Message: "not yours", HTTPStatus: 403}, ExitForbidden, apierr.CodeForbidden, ""},
		{"rate limit", &hey.Error{Code: hey.CodeRateLimit, Message: "429", Hint: "30 seconds", HTTPStatus: 429}, ExitRateLimit, apierr.CodeRateLimit, ""},
		{"network", &hey.Error{Code: hey.CodeNetwork, Message: "connection refused"}, ExitNetwork, apierr.CodeNetwork, ""},
		{"validation", &hey.Error{Code: hey.CodeValidation, Message: "subject can't be blank", Hint: "Give it a subject", HTTPStatus: 422}, ExitUsage, apierr.CodeValidation, "Give it a subject"},
		{"conflict", &hey.Error{Code: hey.CodeConflict, Message: "already exists", Hint: "hey contacts show", HTTPStatus: 409}, ExitUsage, apierr.CodeConflict, "hey contacts show"},
		{"api", &hey.Error{Code: hey.CodeAPI, Message: "internal server error", HTTPStatus: 500}, ExitAPI, apierr.CodeAPI, ""},
		{"an SDK usage error", &hey.Error{Code: hey.CodeUsage, Message: "page cursor is required", HTTPStatus: 400}, ExitAPI, apierr.CodeAPI, ""},
		{"an SDK ambiguous error", &hey.Error{Code: hey.CodeAmbiguous, Message: "two contacts match", HTTPStatus: 300}, ExitAPI, apierr.CodeAPI, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := apierr.FromSDK(tt.sdkErr)

			if got := ExitCodeFor(err); got != tt.wantExit {
				t.Errorf("exit code = %d, want %d", got, tt.wantExit)
			}

			var buf bytes.Buffer
			New(Options{Format: FormatJSON, Stderr: &buf}).Err(err)

			var resp ErrorResponse
			if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if resp.OK {
				t.Error("ok = true, want false")
			}
			if resp.Code != tt.wantCode {
				t.Errorf("envelope code = %q, want %q", resp.Code, tt.wantCode)
			}
			if resp.Hint != tt.wantHint {
				t.Errorf("envelope hint = %q, want %q", resp.Hint, tt.wantHint)
			}
			if resp.Error != apierr.AsError(err).Message {
				t.Errorf("envelope error = %q, want the mapped message %q", resp.Error, apierr.AsError(err).Message)
			}
		})
	}
}

func TestExitCodeForEveryCodeAnErrorCanCarry(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{apierr.ErrUsage("bad"), ExitUsage},
		{apierr.ErrUsageHint("bad", "try this"), ExitUsage},
		{apierr.ErrNotFound("topic", "3011"), ExitNotFound},
		{apierr.ErrAuth("no"), ExitAuth},
		{apierr.ErrForbidden("no"), ExitForbidden},
		{apierr.ErrRateLimit(30), ExitRateLimit},
		{apierr.ErrNetwork(nil), ExitNetwork},
		{apierr.ErrAPI(500, "oops"), ExitAPI},
		{apierr.ErrAmbiguous("contact", nil), ExitAmbiguous},
		{apierr.ErrValidation(422, "blank", "", nil), ExitUsage},
		{apierr.ErrConflict(409, "exists", "", nil), ExitUsage},
		{&apierr.Error{Code: "upgrade_required", Message: "build too old"}, ExitUsage},
		// Something that never went through apierr at all — a bare error out of the
		// standard library or a dependency still has to exit with something.
		{errors.New("a plain error nobody wrapped"), ExitUsage},
		{fmt.Errorf("wrapped: %w", apierr.ErrNotFound("topic", "3011")), ExitNotFound},
	}

	for _, tt := range tests {
		if got := ExitCodeFor(tt.err); got != tt.want {
			t.Errorf("ExitCodeFor(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}
