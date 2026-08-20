package cmd

import "github.com/spf13/cobra"

// newLogoutCommand is the top-level `hey logout` shortcut for `hey auth logout`.
func newLogoutCommand() *cobra.Command {
	return buildLogoutCommand("hey logout")
}
