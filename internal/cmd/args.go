package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func usageExactArgs(expected int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == expected {
			return nil
		}

		if len(args) < expected {
			return fmt.Errorf("Usage: %s", cleanUseLine(cmd.UseLine()))
		}

		argumentLabel := "arguments"
		if expected == 1 {
			argumentLabel = "argument"
		}
		return fmt.Errorf("expected %d %s, got %d", expected, argumentLabel, len(args))
	}
}

func cleanUseLine(useLine string) string {
	return strings.TrimSpace(strings.TrimSuffix(useLine, " [flags]"))
}
