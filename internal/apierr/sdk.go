package apierr

import (
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
