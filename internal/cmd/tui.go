package cmd

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/tui"
)

var openInRunningTUI = tui.OpenInRunningTUI

type tuiCommand struct {
	cmd *cobra.Command
}

func newTuiCommand() *tuiCommand {
	return &tuiCommand{cmd: newTuiRunner("tui", false)}
}

func newHeyCommand() *tuiCommand {
	return &tuiCommand{cmd: newTuiRunner("hey", true)}
}

func newTuiRunner(use string, hidden bool) *cobra.Command {
	var topicID int64
	var topicTitle string
	var screener bool
	var instance string
	var remote bool
	command := &cobra.Command{
		Use:    use + " [classic|spacious]",
		Short:  "Launch the interactive terminal UI",
		Hidden: hidden,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			layout := tui.LayoutClassic
			if len(args) == 1 {
				var err error
				layout, err = tui.ParseLayout(args[0])
				if err != nil {
					return apierr.ErrUsage(err.Error())
				}
			}
			topicSet := cmd.Flags().Changed("topic")
			topicTitleSet := cmd.Flags().Changed("topic-title")
			if topicSet && topicID <= 0 {
				return apierr.ErrUsage("topic ID must be positive")
			}
			if topicSet && screener {
				return apierr.ErrUsage("--topic and --screener cannot be used together")
			}
			if topicTitleSet && !topicSet {
				return apierr.ErrUsage("--topic-title requires --topic")
			}
			if remote && !topicSet && !screener {
				return apierr.ErrUsage("--remote requires --topic or --screener")
			}
			if remote && len(args) == 1 {
				return apierr.ErrUsage("a layout cannot be selected when opening an existing TUI with --remote")
			}
			request := tui.OpenRequest{TopicID: topicID, Title: topicTitle, Screener: screener}
			if accountID, err := strconv.ParseInt(cfg.AccountID, 10, 64); err == nil && accountID > 0 {
				request.AccountID = accountID
			}
			if remote {
				if err := openInRunningTUI(instance, request); err != nil {
					if errors.Is(err, tui.ErrNoRunningTUI) {
						return fmt.Errorf("no running HEY TUI")
					}
					return err
				}
				return nil
			}
			if err := requireAuth(); err != nil {
				return err
			}
			return runTUI(rootSDK, sdk, cfg.AccountID, tuiWatchers(), tui.Options{Open: request, Instance: instance, Layout: layout})
		},
	}
	command.Flags().Int64Var(&topicID, "topic", 0, "Open a thread by topic ID")
	command.Flags().StringVar(&topicTitle, "topic-title", "", "Set the title of a directly opened thread")
	_ = command.Flags().MarkHidden("topic-title")
	command.Flags().BoolVar(&screener, "screener", false, "Open The Screener")
	command.Flags().StringVar(&instance, "instance", "", "Name this TUI for remote open requests")
	_ = command.Flags().MarkHidden("instance")
	command.Flags().BoolVar(&remote, "remote", false, "Open the destination in a running TUI and exit")
	return command
}
