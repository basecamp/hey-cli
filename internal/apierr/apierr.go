package apierr

import (
	"errors"
	"fmt"
)

// The codes an Error carries. output.ExitCodeFor maps them onto exit codes and
// they reach a scripting caller as the envelope's "code", so both sides read
// them from here rather than spelling them out and hoping.
const (
	CodeUsage      = "usage"
	CodeNotFound   = "not_found"
	CodeAuth       = "auth"
	CodeForbidden  = "forbidden"
	CodeRateLimit  = "rate_limit"
	CodeNetwork    = "network"
	CodeAPI        = "api"
	CodeAmbiguous  = "ambiguous"
	CodeValidation = "validation"
	CodeConflict   = "conflict"
	CodeUnknown    = "unknown"
)

// Error is a typed error carrying a machine-readable code, human message,
// optional hint, and metadata used for exit-code decisions.
type Error struct {
	Code       string
	Message    string
	Hint       string
	HTTPStatus int
	Cause      error

	// Meta carries structured context into the JSON error envelope — e.g. the
	// per-step results of a partially failed setup, which a scripting caller
	// needs to know what did land.
	Meta map[string]any
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func ErrUsage(msg string) *Error {
	return &Error{Code: CodeUsage, Message: msg}
}

func ErrUsageHint(msg, hint string) *Error {
	return &Error{Code: CodeUsage, Message: msg, Hint: hint}
}

func ErrNotFound(resource, identifier string) *Error {
	return &Error{
		Code:       CodeNotFound,
		Message:    fmt.Sprintf("%s %q not found", resource, identifier),
		HTTPStatus: 404,
	}
}

func ErrAuth(msg string) *Error {
	return &Error{
		Code:       CodeAuth,
		Message:    msg,
		Hint:       "Run: hey auth login",
		HTTPStatus: 401,
	}
}

func ErrForbidden(msg string) *Error {
	return &Error{
		Code:       CodeForbidden,
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
		Code:       CodeRateLimit,
		Message:    msg,
		HTTPStatus: 429,
	}
}

func ErrNetwork(cause error) *Error {
	return &Error{
		Code:    CodeNetwork,
		Message: fmt.Sprintf("network error: %v", cause),
		Cause:   cause,
	}
}

func ErrAPI(status int, msg string) *Error {
	return &Error{
		Code:       CodeAPI,
		Message:    msg,
		HTTPStatus: status,
	}
}

func ErrValidation(status int, msg, hint string, cause error) *Error {
	return &Error{
		Code:       CodeValidation,
		Message:    msg,
		Hint:       hint,
		HTTPStatus: status,
		Cause:      cause,
	}
}

func ErrConflict(status int, msg, hint string, cause error) *Error {
	return &Error{
		Code:       CodeConflict,
		Message:    msg,
		Hint:       hint,
		HTTPStatus: status,
		Cause:      cause,
	}
}

func ErrAmbiguous(resource string, matches []string) *Error {
	return &Error{
		Code:    CodeAmbiguous,
		Message: fmt.Sprintf("ambiguous %s — multiple matches found", resource),
		Hint:    fmt.Sprintf("Matches: %v", matches),
	}
}

func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeUnknown, Message: err.Error()}
}
