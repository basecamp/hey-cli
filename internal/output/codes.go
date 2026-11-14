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
	ExitPending   = 9
)

// ErrPending is what a command returns once it has written an answer that is neither done
// nor failed: a send the HEY app on this computer holds in its window until the person
// presses Send. Nothing has been sent. Execute writes nothing more for it and exits
// ExitPending, a code nothing else exits with, so a script never reads a wait as a send.
var ErrPending = errors.New("waiting in HEY for the person to press Send: nothing has been sent")

func ExitCodeFor(err error) int {
	if errors.Is(err, ErrPending) {
		return ExitPending
	}
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
	case apierr.CodeAPI, apierr.CodeNotDelivered:
		return ExitAPI
	case apierr.CodeAmbiguous:
		return ExitAmbiguous
	default:
		return ExitUsage
	}
}
