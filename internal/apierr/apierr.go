package apierr

import (
	"errors"
	"fmt"
)

// Error is a typed error carrying a machine-readable code, human message,
// optional hint, and metadata used for retry and exit-code decisions.
type Error struct {
	Code       string
	Message    string
	Hint       string
	HTTPStatus int
	Retryable  bool
	Cause      error
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func ErrUsage(msg string) *Error {
	return &Error{Code: "usage", Message: msg}
}

func ErrUsageHint(msg, hint string) *Error {
	return &Error{Code: "usage", Message: msg, Hint: hint}
}

func ErrNotFound(resource, identifier string) *Error {
	return &Error{
		Code:       "not_found",
		Message:    fmt.Sprintf("%s %q not found", resource, identifier),
		HTTPStatus: 404,
	}
}

func ErrAuth(msg string) *Error {
	return &Error{
		Code:       "auth",
		Message:    msg,
		Hint:       "Run: hey auth login",
		HTTPStatus: 401,
	}
}

func ErrForbidden(msg string) *Error {
	return &Error{
		Code:       "forbidden",
		Message:    msg,
		HTTPStatus: 403,
	}
}

func ErrRateLimit(retryAfter int) *Error {
	msg := "rate limited"
	if retryAfter > 0 {
		msg = fmt.Sprintf("rate limited — retry after %d seconds", retryAfter)
	}
	return &Error{
		Code:       "rate_limit",
		Message:    msg,
		HTTPStatus: 429,
		Retryable:  true,
	}
}

func ErrNetwork(cause error) *Error {
	return &Error{
		Code:      "network",
		Message:   fmt.Sprintf("network error: %v", cause),
		Retryable: true,
		Cause:     cause,
	}
}

func ErrAPI(status int, msg string) *Error {
	return &Error{
		Code:       "api",
		Message:    msg,
		HTTPStatus: status,
		Retryable:  status >= 500,
	}
}

func ErrAmbiguous(resource string, matches []string) *Error {
	return &Error{
		Code:    "ambiguous",
		Message: fmt.Sprintf("ambiguous %s — multiple matches found", resource),
		Hint:    fmt.Sprintf("Matches: %v", matches),
	}
}

func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: "unknown", Message: err.Error()}
}
