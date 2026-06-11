package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/basecamp/hey-cli/internal/output"
)

// resolveSenderID looks up a sender ID by email address from the identity endpoint.
// Returns an error if the email doesn't match any configured sender.
func resolveSenderID(ctx context.Context, email string) (int64, error) {
	identity, err := sdk.Identity().GetIdentity(ctx)
	if err != nil {
		return 0, convertSDKError(err)
	}
	if identity == nil {
		return 0, output.ErrAPI(0, "could not fetch identity")
	}

	email = strings.ToLower(strings.TrimSpace(email))
	var available []string
	for _, s := range identity.Senders {
		if strings.ToLower(s.EmailAddress) == email {
			return s.Id, nil
		}
		available = append(available, s.EmailAddress)
	}

	return 0, output.ErrUsage(fmt.Sprintf(
		"no sender matching %q (available: %s)",
		email, strings.Join(available, ", "),
	))
}

// effectiveSenderID determines the sender ID to use for a mutation.
// Priority: --from flag > config default_sender > SDK default.
func effectiveSenderID(ctx context.Context, fromFlag string) (int64, error) {
	if fromFlag != "" {
		return resolveSenderID(ctx, fromFlag)
	}
	if cfg.DefaultSender != "" {
		return resolveSenderID(ctx, cfg.DefaultSender)
	}
	id, err := sdk.DefaultSenderID(ctx)
	if err != nil {
		return 0, convertSDKError(err)
	}
	return id, nil
}
