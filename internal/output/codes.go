package output

import "errors"

const (
	ExitOK        = 0
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
	var e *Error
	if !errors.As(err, &e) {
		return 1
	}
	switch e.Code {
	case "usage":
		return ExitUsage
	case "not_found":
		return ExitNotFound
	case "auth":
		return ExitAuth
	case "forbidden":
		return ExitForbidden
	case "rate_limit":
		return ExitRateLimit
	case "network":
		return ExitNetwork
	case "api":
		return ExitAPI
	case "ambiguous":
		return ExitAmbiguous
	default:
		return 1
	}
}
