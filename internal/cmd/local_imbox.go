package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

type localImboxCommand struct {
	cmd *cobra.Command
}

// localThread is one Imbox row as the app holds it: id is the row's, topic the thread's.
type localThread struct {
	ID        int64  `json:"id"`
	Topic     int64  `json:"topic"`
	Subject   string `json:"subject"`
	Summary   string `json:"summary"`
	Sender    string `json:"sender"`
	SenderID  int64  `json:"sender_id"`
	Seen      bool   `json:"seen"`
	BubbledUp bool   `json:"bubbled_up"`
}

type localImbox struct {
	Threads []localThread `json:"threads"`
	HasMore bool          `json:"has_more"`
}

func newLocalImboxCommand() *localImboxCommand {
	imboxCommand := &localImboxCommand{}
	imboxCommand.cmd = &cobra.Command{
		Use:   "imbox",
		Short: "List the Imbox as the HEY app holds it",
		Long:  "List the threads in the Imbox, from the HEY app on this computer. Reading them marks nothing seen.",
		Annotations: map[string]string{
			"agent_notes": "Runs the app's imbox.list. Each row's seen says whether the person has seen it, and stays as it was: listing marks nothing seen. has_more in meta means the Imbox holds more than the app has loaded.",
		},
		Example: `  hey local imbox
  hey local imbox --json`,
		RunE: imboxCommand.run,
		Args: cobra.NoArgs,
	}
	return imboxCommand
}

func (c *localImboxCommand) run(cmd *cobra.Command, _ []string) error {
	result, err := callLocal(cmd, "imbox.list", nil)
	if err != nil {
		return err
	}
	imbox, err := decodeLocalAnswerAs("imbox.list", result, func(imbox localImbox) error {
		if imbox.Threads == nil {
			return errors.New("it lists no threads")
		}
		return nil
	})
	if err != nil {
		return err
	}
	notice := ""
	if imbox.HasMore {
		notice = "The Imbox holds more threads than the app has loaded."
	}

	if writer.IsStyled() {
		if len(imbox.Threads) == 0 {
			if notice != "" {
				fmt.Fprintln(cmd.OutOrStdout(), notice)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Nothing in the Imbox")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		// Both ids, as hey box view shows them: the row's is what --ids-only prints.
		table.addRow([]string{"ID", "Thread", "Sender", "Subject", ""})
		for _, thread := range imbox.Threads {
			unseen := ""
			if !thread.Seen {
				unseen = "new"
			}
			table.addRow([]string{fmt.Sprintf("%d", thread.ID), fmt.Sprintf("%d", thread.Topic), truncate(thread.Sender, 24), truncate(thread.Subject, 50), unseen})
		}
		table.print()
		if notice != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", notice)
		}
		return nil
	}
	if stderrNotice := paginationNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
	}
	return writeOK(imbox.Threads,
		output.WithSummary(fmt.Sprintf("%d %s in the Imbox", len(imbox.Threads), threadNoun(len(imbox.Threads)))),
		output.WithNotice(notice),
		output.WithMeta("has_more", imbox.HasMore),
	)
}
