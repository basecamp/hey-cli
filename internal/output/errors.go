package output

import "hey-cli/internal/apierr"

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
