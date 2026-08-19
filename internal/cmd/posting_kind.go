package cmd

import (
	"fmt"
	"strings"

	"github.com/basecamp/hey-cli/internal/output"
)

func validateEmailPostingKind(action, kind string) error {
	if !strings.EqualFold(strings.TrimSpace(kind), "world/post") {
		return nil
	}

	return output.ErrUsageHint(
		fmt.Sprintf("hey %s cannot act on a HEY World post", action),
		"HEY World posts are published content. Use `hey world delete <token> --confirm` only when you intend to remove a published post.",
	)
}
