package cmd

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/tui"
)

var openTopicInRunningTUI = tui.OpenTopicInRunningTUI

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
	var instance string
	var remote bool
	command := &cobra.Command{
		Use:    use,
		Short:  "Launch the interactive terminal UI",
		Hidden: hidden,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if remote && topicID == 0 {
				return apierr.ErrUsage("--remote requires --topic")
			}
			request := tui.TopicRequest{TopicID: topicID, Title: topicTitle}
			if accountID, err := strconv.ParseInt(cfg.AccountID, 10, 64); err == nil && accountID > 0 {
				request.AccountID = accountID
			}
			if remote {
				if err := openTopicInRunningTUI(instance, request); err != nil {
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
			return runTUI(rootSDK, sdk, cfg.AccountID, tuiWatchers(), tui.Options{OpenTopic: request, Instance: instance})
		},
	}
	command.Flags().Int64Var(&topicID, "topic", 0, "Open a thread by topic ID")
	command.Flags().StringVar(&topicTitle, "topic-title", "", "Set the title of a directly opened thread")
	_ = command.Flags().MarkHidden("topic-title")
	command.Flags().StringVar(&instance, "instance", "", "Name this TUI for remote topic requests")
	_ = command.Flags().MarkHidden("instance")
	command.Flags().BoolVar(&remote, "remote", false, "Send the topic to a running TUI and exit")
	return command
}
