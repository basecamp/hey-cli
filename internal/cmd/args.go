package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type usageError struct {
	usage string
}

func (e usageError) Error() string {
	return "Usage: " + e.usage
}

func usageErrorf(format string, args ...any) error {
	return usageError{usage: fmt.Sprintf(format, args...)}
}

func usageExactOneArg() cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return nil
		}

		if len(args) == 0 {
			return usageErrorf("%s", cleanUseLine(cmd.UseLine()))
		}

		return fmt.Errorf("expected 1 argument, got %d", len(args))
	}
}

func usageMinOneArg() cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= 1 {
			return nil
		}

		return usageErrorf("%s", cleanUseLine(cmd.UseLine()))
	}
}

func cleanUseLine(useLine string) string {
	return strings.TrimSpace(strings.TrimSuffix(useLine, " [flags]"))
}
