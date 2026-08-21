package output

import (
	"fmt"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// ErrJQValidation reports an invalid built-in jq expression.
func ErrJQValidation(cause error) *apierr.Error {
	return &apierr.Error{
		Code:    apierr.CodeUsage,
		Message: fmt.Sprintf("invalid --jq expression: %s", cause),
		Cause:   cause,
	}
}

// ErrJQNotSupported reports a command that produces a dedicated raw format.
func ErrJQNotSupported(command string) *apierr.Error {
	return &apierr.Error{
		Code:    apierr.CodeUsage,
		Message: fmt.Sprintf("--jq is not supported by %s", command),
	}
}

// ErrJQConflict reports an output flag that cannot be combined with --jq.
func ErrJQConflict(flag string) *apierr.Error {
	return &apierr.Error{
		Code:    apierr.CodeUsage,
		Message: fmt.Sprintf("cannot use --jq with %s", flag),
	}
}

// ErrJQRuntime reports a failure while evaluating a built-in jq expression.
func ErrJQRuntime(cause error) *apierr.Error {
	return &apierr.Error{
		Code:    apierr.CodeUsage,
		Message: fmt.Sprintf("jq filter error: %s", cause),
		Cause:   cause,
	}
}
