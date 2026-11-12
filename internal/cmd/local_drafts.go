package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/localapp"
	"github.com/basecamp/hey-cli/internal/output"
)

type localDraftsCommand struct {
	cmd *cobra.Command
}

// localDraft is a draft as the HEY app holds it. id is the one a send takes; made_as, the id
// it was written as before HEY named it, is taken too. sendable says whether a send would
// ask the person about it now.
type localDraft struct {
	ID       int64    `json:"id"`
	MadeAs   *int64   `json:"made_as,omitempty"`
	Kind     string   `json:"kind"`
	Thread   *int64   `json:"thread,omitempty"`
	Account  string   `json:"account"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	Cc       []string `json:"cc"`
	Bcc      []string `json:"bcc"`
	Subject  string   `json:"subject"`
	Summary  string   `json:"summary"`
	SavedAt  string   `json:"saved_at"`
	SendsAt  *string  `json:"sends_at,omitempty"`
	Sendable *bool    `json:"sendable"`
}

// complete holds what a draft's zero would claim: draft 0, or one HEY would not send.
func (d localDraft) complete() error {
	if d.ID <= 0 {
		return errors.New("it gives a draft without an ID")
	}
	if d.Sendable == nil {
		return fmt.Errorf("it does not say whether draft %d can be sent", d.ID)
	}
	return nil
}

type localDrafts struct {
	Drafts []localDraft `json:"drafts"`
}

func newLocalDraftsCommand() *localDraftsCommand {
	draftsCommand := &localDraftsCommand{}
	draftsCommand.cmd = &cobra.Command{
		Use:   "drafts",
		Short: "List the drafts the HEY app holds",
		Long: `List every draft the HEY app on this computer holds, the last saved first.

A draft marked "sendable" is one hey local send would ask you to send now. One
that is scheduled goes on its own at its time; any other is yours to finish in HEY.`,
		Annotations: map[string]string{
			"agent_notes": "Runs the app's drafts.list. id is what hey local send takes, and so is made_as, the id a draft was written as before HEY named it. sendable is whether hey local send would ask the person about it now: not scheduled (sends_at), to somebody, every file uploaded, not a forward, from a sender the account still has. These ids are the app's own, not hey draft list's.",
		},
		Example: `  hey local drafts
  hey local drafts --json`,
		RunE: draftsCommand.run,
		Args: cobra.NoArgs,
	}
	return draftsCommand
}

func (c *localDraftsCommand) run(cmd *cobra.Command, _ []string) error {
	result, err := callLocal(cmd, "drafts.list", nil)
	if err != nil {
		return err
	}
	drafts, err := decodeLocalAnswerAs("drafts.list", result, func(drafts localDrafts) error {
		if drafts.Drafts == nil {
			return errors.New("it lists no drafts")
		}
		for _, draft := range drafts.Drafts {
			if incomplete := draft.complete(); incomplete != nil {
				return incomplete
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if writer.IsStyled() {
		if len(drafts.Drafts) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No drafts in the HEY app")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "To", "Subject", "Saved", ""})
		for _, draft := range drafts.Drafts {
			table.addRow([]string{fmt.Sprintf("%d", draft.ID), truncate(strings.Join(draft.To, ", "), 32), truncate(draft.Subject, 40), draft.SavedAt, localDraftState(draft)})
		}
		table.print()
		return nil
	}
	return writeOK(drafts.Drafts,
		output.WithSummary(fmt.Sprintf("%d %s in the HEY app", len(drafts.Drafts), localDraftNoun(len(drafts.Drafts)))),
	)
}

func localDraftState(draft localDraft) string {
	switch {
	case draft.SendsAt != nil:
		return "scheduled " + *draft.SendsAt
	case *draft.Sendable:
		return "sendable"
	default:
		return ""
	}
}

func localDraftNoun(count int) string {
	if count == 1 {
		return "draft"
	}
	return "drafts"
}

// writeLocalDraft answers a draft an agent wrote: saved, nothing sent, and how to ask HEY
// to send it, where HEY would.
func writeLocalDraft(cmd *cobra.Command, name string, result *localapp.Result, thread int64) error {
	kind := "new"
	if thread != 0 {
		kind = "reply"
	}
	draft, err := decodeLocalAnswerAs(name, result, func(draft localDraft) error {
		if err := draft.complete(); err != nil {
			return err
		}
		if draft.Kind != kind {
			return &localOtherwiseError{fmt.Sprintf("it saved draft %d as %s, not %s", draft.ID, draft.Kind, kind)}
		}
		if thread != 0 && (draft.Thread == nil || *draft.Thread != thread) {
			return &localOtherwiseError{fmt.Sprintf("it saved draft %d, not a reply on thread %d", draft.ID, thread)}
		}
		return nil
	})
	if err != nil {
		return err
	}

	to := "nobody"
	if len(draft.To) > 0 {
		to = strings.Join(draft.To, ", ")
	}
	saved := fmt.Sprintf("Draft %d saved, to %s. Nothing has been sent.", draft.ID, to)
	if !*draft.Sendable {
		summary := fmt.Sprintf("Draft %d saved; HEY would not send it from here now", draft.ID)
		// --ids-only and --count carry no summary; this one goes where a waiting send's does.
		if format := writer.EffectiveFormat(); format == output.FormatIDs || format == output.FormatCount {
			fmt.Fprintln(cmd.ErrOrStderr(), summary)
		}
		return writeMutationLine(cmd,
			saved+" HEY can't send it from here: finish it in HEY.",
			summary,
			localOneOf(draft),
		)
	}
	send := fmt.Sprintf("hey local %ssend %d", localSocketArgument(), draft.ID)
	return writeMutationLine(cmd,
		saved+" Ask HEY to send it with: "+send,
		fmt.Sprintf("Draft %d saved; nothing has been sent", draft.ID),
		localOneOf(draft),
		output.WithBreadcrumbs(output.Breadcrumb{Action: "send", Command: send, Description: "Ask HEY to send it: the person presses Send in the HEY window"}),
	)
}

// localMessageBody is -m's words, else stdin's when it is not a terminal.
func localMessageBody(message string) (string, error) {
	if message == "" && !stdinIsTerminal() {
		read, err := readStdin()
		if err != nil {
			return "", err
		}
		message = read
	}
	if message == "" {
		return "", apierr.ErrUsage("no message provided (use -m or --message, or pipe it to stdin)")
	}
	return message, nil
}
