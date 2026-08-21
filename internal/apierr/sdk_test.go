package apierr

import (
	"errors"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

func TestFromSDK(t *testing.T) {
	tests := []struct {
		name           string
		sdkErr         *hey.Error
		wantCode       string
		wantMessage    string
		wantHint       string
		wantHTTPStatus int
	}{
		{
			name:           "auth carries the login instruction",
			sdkErr:         &hey.Error{Code: hey.CodeAuth, Message: "not authenticated", HTTPStatus: 401},
			wantCode:       CodeAuth,
			wantMessage:    "not authenticated",
			wantHint:       "Run: hey auth login",
			wantHTTPStatus: 401,
		},
		{
			name:           "not found keeps the server's sentence",
			sdkErr:         &hey.Error{Code: hey.CodeNotFound, Message: "topic 3011 not found", HTTPStatus: 404},
			wantCode:       CodeNotFound,
			wantMessage:    "topic 3011 not found",
			wantHTTPStatus: 404,
		},
		{
			name:           "forbidden",
			sdkErr:         &hey.Error{Code: hey.CodeForbidden, Message: "that account is not yours", HTTPStatus: 403},
			wantCode:       CodeForbidden,
			wantMessage:    "that account is not yours",
			wantHTTPStatus: 403,
		},
		{
			name:           "rate limit reads the retry-after out of the hint",
			sdkErr:         &hey.Error{Code: hey.CodeRateLimit, Message: "429", Hint: "30 seconds", HTTPStatus: 429},
			wantCode:       CodeRateLimit,
			wantMessage:    "rate limited — retry after 30 seconds",
			wantHTTPStatus: 429,
		},
		{
			name:           "rate limit without a retry-after",
			sdkErr:         &hey.Error{Code: hey.CodeRateLimit, Message: "429", HTTPStatus: 429},
			wantCode:       CodeRateLimit,
			wantMessage:    "rate limited",
			wantHTTPStatus: 429,
		},
		{
			name:        "network says so rather than showing a status",
			sdkErr:      &hey.Error{Code: hey.CodeNetwork, Message: "dial tcp: connection refused"},
			wantCode:    CodeNetwork,
			wantMessage: "network error: dial tcp: connection refused",
		},
		{
			name:           "validation keeps the server's hint",
			sdkErr:         &hey.Error{Code: hey.CodeValidation, Message: "subject can't be blank", Hint: "Give the message a subject", HTTPStatus: 422},
			wantCode:       CodeValidation,
			wantMessage:    "subject can't be blank",
			wantHint:       "Give the message a subject",
			wantHTTPStatus: 422,
		},
		{
			name:           "conflict keeps the server's hint",
			sdkErr:         &hey.Error{Code: hey.CodeConflict, Message: "that contact already exists", Hint: "hey contacts show ryan@example.com", HTTPStatus: 409},
			wantCode:       CodeConflict,
			wantMessage:    "that contact already exists",
			wantHint:       "hey contacts show ryan@example.com",
			wantHTTPStatus: 409,
		},
		{
			name:           "anything else is an API error",
			sdkErr:         &hey.Error{Code: hey.CodeAPI, Message: "internal server error", HTTPStatus: 500},
			wantCode:       CodeAPI,
			wantMessage:    "internal server error",
			wantHTTPStatus: 500,
		},
		{
			name:           "an SDK usage error is an API error too, as it always was",
			sdkErr:         &hey.Error{Code: hey.CodeUsage, Message: "page cursor is required", HTTPStatus: 400},
			wantCode:       CodeAPI,
			wantMessage:    "page cursor is required",
			wantHTTPStatus: 400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted := AsError(FromSDK(tt.sdkErr))
			if converted.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", converted.Code, tt.wantCode)
			}
			if converted.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", converted.Message, tt.wantMessage)
			}
			if converted.Hint != tt.wantHint {
				t.Errorf("hint = %q, want %q", converted.Hint, tt.wantHint)
			}
			if converted.HTTPStatus != tt.wantHTTPStatus {
				t.Errorf("http status = %d, want %d", converted.HTTPStatus, tt.wantHTTPStatus)
			}
		})
	}
}

func TestFromSDKPassesNilThrough(t *testing.T) {
	if err := FromSDK(nil); err != nil {
		t.Errorf("FromSDK(nil) = %v, want nil", err)
	}
}

func TestFromSDKWrapsAnErrorThatIsNotTheSDKs(t *testing.T) {
	converted := AsError(FromSDK(errors.New("could not open the editor")))
	if converted.Code != CodeAPI {
		t.Errorf("code = %q, want %q", converted.Code, CodeAPI)
	}
	if converted.Message != "could not open the editor" {
		t.Errorf("message = %q, want the error's own text", converted.Message)
	}
}

func TestFromSDKKeepsTheCauseReachable(t *testing.T) {
	sdkErr := &hey.Error{Code: hey.CodeValidation, Message: "subject can't be blank", HTTPStatus: 422}

	if !errors.Is(FromSDK(sdkErr), error(sdkErr)) {
		t.Error("the SDK error a validation failure came from must stay reachable through errors.Is")
	}
}
