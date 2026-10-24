package apierr

import (
	"errors"
	"fmt"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// FromSDK maps an SDK error onto the CLI's error shape, which is what decides
// the exit code, the JSON envelope and the sentence somebody reads. It lives
// here rather than next to the commands because the TUI hits the same API and
// wants the same sentence: half the callers could not reach it in internal/cmd.
func FromSDK(err error) error {
	if err == nil {
		return nil
	}

	// Not every error that comes back from an SDK call was made by the SDK. The
	// auth strategy is ours, and the SDK returns what it hands back untouched, so
	// a credential failure arrives here already classified. hey.AsError only
	// recognizes the SDK's own type and would flatten it to "api" — losing the
	// auth exit code and the hint that says how to fix it.
	var cliErr *Error
	if errors.As(err, &cliErr) {
		return cliErr
	}

	sdkErr := hey.AsError(err)
	switch sdkErr.Code {
	case hey.CodeAuth:
		return ErrAuth(sdkErr.Message)
	case hey.CodeNotFound:
		return &Error{Code: CodeNotFound, Message: sdkErr.Message, HTTPStatus: 404}
	case hey.CodeForbidden:
		return ErrForbidden(sdkErr.Message)
	case hey.CodeRateLimit:
		var retryAfter int
		_, _ = fmt.Sscanf(sdkErr.Hint, "%d", &retryAfter)
		return ErrRateLimit(retryAfter)
	case hey.CodeNetwork:
		return ErrNetwork(err)
	case hey.CodeValidation:
		return ErrValidation(sdkErr.HTTPStatus, sdkErr.Message, sdkErr.Hint, err)
	case hey.CodeConflict:
		return ErrConflict(sdkErr.HTTPStatus, sdkErr.Message, sdkErr.Hint, err)
	default:
		return ErrAPI(sdkErr.HTTPStatus, sdkErr.Message)
	}
}
