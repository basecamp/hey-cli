package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// threadCommentCommand adds a note to a thread through the SDK's Topics().CreateComment
// (POST /topics/{id}/comments.json), and with --dry-run asks Topics().GetCommentAudience
// (GET /topics/{id}/comments/new.json) who the note would reach without adding it.
type threadCommentCommand struct {
	cmd         *cobra.Command
	message     string
	messageHTML string
	dryRun      bool
}

// threadCommentResult is what the command answers: the note HEY created (absent on a dry
// run) and who sees it besides its author. VisibleTo is empty for a note to self, and
// absent only when HEY did not say.
type threadCommentResult struct {
	ID             int64           `json:"id,omitempty"`
	ThreadID       int64           `json:"thread_id"`
	VisibleTo      []threadContact `json:"visible_to"`
	CollectionOnly bool            `json:"collection_only"`
	DryRun         bool            `json:"dry_run,omitempty"`
}

func newThreadCommentCommand() *threadCommentCommand {
	commentCommand := &threadCommentCommand{}
	commentCommand.cmd = &cobra.Command{
		Use:   "comment <thread-id>",
		Short: "Add a note to a thread",
		Long: "Add a note to a thread. A note is internal: HEY never emails it to anyone, but " +
			"everyone with access to the thread sees it — on a HEY for Domains account that " +
			"includes your teammates on it. On a thread no one else can reach, it is a note to " +
			"self. The note is Markdown; use --message-html to send raw HTML instead. " +
			"--dry-run shows who would see the note without adding it. " +
			"Use hey reply to send a mailed reply instead.",
		Example: `  hey thread comment 12345 -m "Following up with accounting on this."
  hey thread comment 12345 --dry-run`,
		Annotations: map[string]string{
			"agent_notes": "Adds a note (an entry of kind \"comment\") to the thread by its topic_id, from hey box view, hey label view or hey search; it is not a mailed reply — use hey reply for that. " +
				"The note is never emailed, but it is not private: everyone with access to the thread sees it, including HEY for Domains teammates. " +
				"The answer names the note's id and visible_to, the other users who see it (empty for a note to self); collection_only is true on a collection's own thread. " +
				"--dry-run needs no message (one given is not sent), adds nothing, and answers visible_to and collection_only for the thread — run it first when the audience matters. " +
				"The message is Markdown via -m, or raw HTML via --message-html; a blank message is refused.",
		},
		RunE: commentCommand.run,
		Args: usageExactOneArg(),
	}
	commentCommand.cmd.Flags().StringVarP(&commentCommand.message, "message", "m", "", "Note as Markdown (required unless --dry-run)")
	commentCommand.cmd.Flags().StringVar(&commentCommand.messageHTML, "message-html", "", "Note as raw HTML instead of Markdown")
	commentCommand.cmd.Flags().BoolVar(&commentCommand.dryRun, "dry-run", false, "Show who would see the note without adding it")
	commentCommand.cmd.MarkFlagsMutuallyExclusive("message", "message-html")

	return commentCommand
}

func (c *threadCommentCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := parsePositiveID(args[0], "thread")
	if err != nil {
		return err
	}

	if c.dryRun {
		audience, audienceErr := sdk.Topics().GetCommentAudience(cmd.Context(), threadID)
		if audienceErr != nil {
			return apierr.FromSDK(audienceErr)
		}
		result := threadCommentResult{
			ThreadID:       threadID,
			VisibleTo:      threadContacts(audience.VisibleTo),
			CollectionOnly: audience.CollectionOnly,
			DryRun:         true,
		}
		return writeMutationLine(cmd,
			fmt.Sprintf("A note on thread %d would be %s.", threadID, describeNoteAudience(audience.VisibleTo, audience.CollectionOnly)),
			fmt.Sprintf("Dry run: a note on thread %d would reach %s", threadID, countNoteAudience(audience.VisibleTo)),
			result,
		)
	}

	content := threadCommentContent(c.message, c.messageHTML)
	if content == "" {
		return apierr.ErrUsage("a note is required (use -m <text> or --message-html <html>)")
	}

	note, err := sdk.Topics().CreateComment(cmd.Context(), threadID, content)
	if err != nil {
		return apierr.FromSDK(err)
	}

	result := threadCommentResult{
		ID:             note.Id,
		ThreadID:       threadID,
		CollectionOnly: note.CollectionOnly,
	}
	line := fmt.Sprintf("Note %d added to thread %d.", note.Id, threadID)
	if note.VisibleTo != nil {
		result.VisibleTo = threadContacts(note.VisibleTo)
		line = fmt.Sprintf("Note %d added to thread %d, %s.", note.Id, threadID, describeNoteAudience(note.VisibleTo, note.CollectionOnly))
	}
	return writeMutationLine(cmd, line,
		fmt.Sprintf("Note %d added to thread %d", note.Id, threadID),
		result,
		output.WithBreadcrumbs(output.Breadcrumb{Action: "read", Command: fmt.Sprintf("hey thread read %d", threadID), Description: "Read this thread"}),
	)
}

// threadCommentContent is the note's HTML, which is what HEY stores: Markdown converted
// the way every other -m in this CLI is, so text such as "a < b & c" arrives escaped, or
// raw HTML as given. Blank either way is "".
func threadCommentContent(markdown, rawHTML string) string {
	if strings.TrimSpace(rawHTML) != "" {
		return strings.TrimSpace(rawHTML)
	}
	if strings.TrimSpace(markdown) == "" {
		return ""
	}
	return htmlutil.FromMarkdown(strings.TrimSpace(markdown))
}

func describeNoteAudience(visibleTo []generated.Contact, collectionOnly bool) string {
	switch {
	case collectionOnly && len(visibleTo) == 0:
		return "seen only on its collection"
	case collectionOnly:
		return "seen only on its collection, by " + joinNoteAudience(visibleTo)
	case len(visibleTo) == 0:
		return "visible only to you"
	default:
		return "visible to you and " + joinNoteAudience(visibleTo)
	}
}

func joinNoteAudience(visibleTo []generated.Contact) string {
	names := make([]string, len(visibleTo))
	for i, contact := range visibleTo {
		name, address := terminal.SanitizeLine(contact.Name), terminal.SanitizeLine(contact.EmailAddress)
		switch {
		case name != "" && address != "":
			names[i] = fmt.Sprintf("%s <%s>", name, address)
		case name != "":
			names[i] = name
		default:
			names[i] = address
		}
	}
	return strings.Join(names, ", ")
}

func countNoteAudience(visibleTo []generated.Contact) string {
	switch len(visibleTo) {
	case 0:
		return "only you"
	case 1:
		return "you and 1 other person"
	default:
		return fmt.Sprintf("you and %d other people", len(visibleTo))
	}
}
