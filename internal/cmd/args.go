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
	return usageExactArgs(1)
}

func usageMinOneArg() cobra.PositionalArgs {
	return usageMinArgs(1)
}

// usageExactArgs answers the bare command with its usage line — somebody who typed
// no arguments is asking what it takes — and any other wrong count with the count,
// which is the only thing they got wrong.
func usageExactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		switch {
		case len(args) == n:
			return nil
		case len(args) == 0:
			return usageErrorf("%s", cleanUseLine(cmd.UseLine()))
		default:
			return fmt.Errorf("expected %d %s, got %d", n, argNoun(n), len(args))
		}
	}
}

func usageMinArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= n {
			return nil
		}

		return usageErrorf("%s", cleanUseLine(cmd.UseLine()))
	}
}

func argNoun(count int) string {
	if count == 1 {
		return "argument"
	}
	return "arguments"
}

func cleanUseLine(useLine string) string {
	return strings.TrimSpace(strings.TrimSuffix(useLine, " [flags]"))
}
