package output

import (
	"fmt"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// Error is a typed error with a code, message, and optional fields.
// Alias for apierr.Error so that cmd-layer code can use output.Error
// without importing the domain package directly.
type Error = apierr.Error

var (
	ErrUsage     = apierr.ErrUsage
	ErrUsageHint = apierr.ErrUsageHint
	ErrNotFound  = apierr.ErrNotFound
	ErrAuth      = apierr.ErrAuth
	ErrForbidden = apierr.ErrForbidden
	ErrRateLimit = apierr.ErrRateLimit
	ErrNetwork   = apierr.ErrNetwork
	ErrAPI       = apierr.ErrAPI
	ErrAmbiguous = apierr.ErrAmbiguous
	AsError      = apierr.AsError
)

// ErrJQValidation reports an invalid built-in jq expression.
func ErrJQValidation(cause error) *Error {
	return &Error{
		Code:    "usage",
		Message: fmt.Sprintf("invalid --jq expression: %s", cause),
		Cause:   cause,
	}
}

// ErrJQNotSupported reports a command that produces a dedicated raw format.
func ErrJQNotSupported(command string) *Error {
	return &Error{
		Code:    "usage",
		Message: fmt.Sprintf("--jq is not supported by %s", command),
	}
}

// ErrJQConflict reports an output flag that cannot be combined with --jq.
func ErrJQConflict(flag string) *Error {
	return &Error{
		Code:    "usage",
		Message: fmt.Sprintf("cannot use --jq with %s", flag),
	}
}

// ErrJQRuntime reports a failure while evaluating a built-in jq expression.
func ErrJQRuntime(cause error) *Error {
	return &Error{
		Code:    "usage",
		Message: fmt.Sprintf("jq filter error: %s", cause),
		Cause:   cause,
	}
}
