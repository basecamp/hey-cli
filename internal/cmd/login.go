package cmd

import "github.com/spf13/cobra"

// newLoginCommand is the top-level `hey login` shortcut for `hey auth login`.
func newLoginCommand() *cobra.Command {
	return buildLoginCommand("hey login")
}
