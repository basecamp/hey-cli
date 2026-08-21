package output

import (
	"errors"

	"github.com/basecamp/hey-cli/internal/apierr"
)

const (
	ExitUsage     = 1
	ExitNotFound  = 2
	ExitAuth      = 3
	ExitForbidden = 4
	ExitRateLimit = 5
	ExitNetwork   = 6
	ExitAPI       = 7
	ExitAmbiguous = 8
)

func ExitCodeFor(err error) int {
	var e *apierr.Error
	if !errors.As(err, &e) {
		return ExitUsage
	}
	switch e.Code {
	case apierr.CodeUsage:
		return ExitUsage
	case apierr.CodeNotFound:
		return ExitNotFound
	case apierr.CodeAuth:
		return ExitAuth
	case apierr.CodeForbidden:
		return ExitForbidden
	case apierr.CodeRateLimit:
		return ExitRateLimit
	case apierr.CodeNetwork:
		return ExitNetwork
	case apierr.CodeAPI:
		return ExitAPI
	case apierr.CodeAmbiguous:
		return ExitAmbiguous
	default:
		return ExitUsage
	}
}
