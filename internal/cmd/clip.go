package cmd

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type clipsCommand struct {
	cmd *cobra.Command
}

func newClipListCommand() *clipsCommand {
	return newClipsListingCommand("list", `  hey clip list
  hey clip list --json
  hey clip list --ids-only`)
}

func newClipsListingCommand(use, example string) *clipsCommand {
	command := &clipsCommand{}
	command.cmd = &cobra.Command{
		Use:   use,
		Short: "List the newest page of passages clipped from email",
		Annotations: map[string]string{
			"agent_notes": "Returns the newest page of clip IDs, content, source entry IDs, and source thread context. The SDK does not expose the cursor for older pages. Use an ID with hey clip delete.",
		},
		Example: example,
		RunE:    command.run,
		Args:    cobra.NoArgs,
	}
	return command
}

func (c *clipsCommand) run(cmd *cobra.Command, _ []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	clips, err := sdk.Clips().List(cmd.Context())
	if err != nil {
		return apierr.FromSDK(err)
	}
	notice := clipsPageNotice(clips)
	if stderrNotice := paginationNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
	}

	switch writer.EffectiveFormat() {
	case output.FormatStyled:
		if len(clips) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No clips found")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Content", "Entry", "Thread", "Saved"})
		for _, clip := range clips {
			table.addRow([]string{
				fmt.Sprintf("%d", clip.Id),
				truncate(terminal.SanitizeLine(clip.Content), 60),
				fmt.Sprintf("%d", clip.EntryId),
				clipTopicLabel(clip.Topic),
				formatDate(clip.CreatedAt),
			})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "Notice: "+notice)
		}
		return nil
	case output.FormatMarkdown:
		return writeClipsMarkdown(cmd, clips)
	default:
		return writeOK(clips,
			output.WithSummary(fmt.Sprintf("%d %s", len(clips), clipNoun(len(clips)))),
			output.WithNotice(notice),
			output.WithBreadcrumbs(
				output.Breadcrumb{Action: "create", Command: "hey clip create <entry-id> --content <text>", Description: "Save text from an email entry"},
				output.Breadcrumb{Action: "delete", Command: "hey clip delete <clip-id>", Description: "Delete a clip"},
			),
		)
	}
}

func clipsPageNotice(clips []generated.Clip) string {
	if len(clips) == 0 {
		return ""
	}
	return "Showing HEY's newest clips page. The SDK does not expose the cursor for older pages."
}

func clipTopicLabel(topic generated.ClipTopic) string {
	name := terminal.SanitizeLine(topic.Name)
	if name == "" {
		return fmt.Sprintf("%d", topic.Id)
	}
	return fmt.Sprintf("%s (%d)", name, topic.Id)
}

func writeClipsMarkdown(cmd *cobra.Command, clips []generated.Clip) error {
	if len(clips) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "(no results)")
		return err
	}
	var document strings.Builder
	document.WriteString("| id | content | entry_id | topic_id | topic | saved |\n")
	document.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, clip := range clips {
		fmt.Fprintf(&document, "| %d | %s | %d | %d | %s | %s |\n",
			clip.Id,
			markdownSafeText(clip.Content),
			clip.EntryId,
			clip.Topic.Id,
			markdownSafeText(clip.Topic.Name),
			formatDate(clip.CreatedAt),
		)
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), document.String())
	return err
}

func clipNoun(count int) string {
	if count == 1 {
		return "clip"
	}
	return "clips"
}

type clipCommand struct {
	cmd *cobra.Command
}

func newClipCommand() *clipCommand {
	clipCommand := &clipCommand{}
	clipCommand.cmd = &cobra.Command{
		Use:   "clip",
		Short: "List and manage passages saved from email",
		Annotations: map[string]string{
			"agent_notes": "List clips, create a clip from text carried by an email entry, or delete a clip. HEY assigns a created clip to the source entry's account and resolves deletion by identity-owned clip ID across linked accounts; --account selects list presentation. The CLI verifies that the passage is source-backed by the entry's message content before saving it, with a 64 KiB passage limit and a 1 MiB source-validation limit. Find clip IDs with hey clip list.",
		},
	}
	clipCommand.cmd.AddCommand(newClipListCommand().cmd)
	clipCommand.cmd.AddCommand(newClipCreateCommand().cmd)
	clipCommand.cmd.AddCommand(newClipDeleteCommand().cmd)
	return clipCommand
}

const (
	maxClipContentBytes = 64 << 10
	maxClipSourceBytes  = 1 << 20
)

type clipCreateCommand struct {
	cmd     *cobra.Command
	content string
}

func newClipCreateCommand() *clipCreateCommand {
	createCommand := &clipCreateCommand{}
	createCommand.cmd = &cobra.Command{
		Use:     "create <entry-id>",
		Aliases: []string{"add"},
		Short:   "Save text from an email entry",
		Long:    "Save a passage from an email entry. HEY assigns the clip to the source entry's account. The content must be present in the entry's message text; whitespace differences are accepted. Passages are limited to 64 KiB and source entries to 1 MiB for validation.",
		Example: `  hey clip create 987 --content "The launch moves to Wednesday."`,
		RunE:    createCommand.run,
		Args:    usageExactOneArg(),
	}
	createCommand.cmd.Flags().StringVar(&createCommand.content, "content", "", "Text selected from the email entry (required)")
	return createCommand
}

func (c *clipCreateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	entryID, err := parsePositiveID(args[0], "entry")
	if err != nil {
		return err
	}
	if strings.TrimSpace(c.content) == "" {
		return apierr.ErrUsage("--content is required")
	}
	if len(c.content) > maxClipContentBytes {
		return apierr.ErrUsage(fmt.Sprintf("--content exceeds the %d KiB clip limit", maxClipContentBytes>>10))
	}
	message, err := sdk.Messages().Get(cmd.Context(), entryID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if message == nil {
		return apierr.ErrNotFound("message", fmt.Sprintf("%d", entryID))
	}
	if len(message.Content) > maxClipSourceBytes {
		return apierr.ErrAPI(0, fmt.Sprintf("entry %d content exceeds the %d MiB clip validation limit", entryID, maxClipSourceBytes>>20))
	}
	if !clipContentMatches(c.content, message.Content) {
		return apierr.ErrUsageHint(
			fmt.Sprintf("--content does not match text in entry %d", entryID),
			"Copy an exact passage from the entry; whitespace differences are allowed.",
		)
	}
	if err := sdk.Clips().Create(cmd.Context(), entryID, c.content); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Clip from entry %d created", entryID), map[string]any{"entry_id": entryID},
		output.WithBreadcrumbs(output.Breadcrumb{Action: "list", Command: "hey clip list", Description: "Find the new clip ID"}),
	)
}

func clipContentMatches(content, entryHTML string) bool {
	selected := normalizeClipText(content)
	entry := normalizeClipText(htmlutil.MessageSourceText(entryHTML))
	return selected != "" && strings.Contains(entry, selected)
}

func normalizeClipText(text string) string {
	var normalized strings.Builder
	normalized.Grow(len(text))
	pendingSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			pendingSpace = normalized.Len() > 0
			continue
		}
		if pendingSpace {
			normalized.WriteByte(' ')
			pendingSpace = false
		}
		normalized.WriteRune(r)
	}
	return normalized.String()
}

type clipDeleteCommand struct {
	cmd *cobra.Command
}

func newClipDeleteCommand() *clipDeleteCommand {
	deleteCommand := &clipDeleteCommand{}
	deleteCommand.cmd = &cobra.Command{
		Use:     "delete <clip-id>",
		Aliases: []string{"remove", "rm"},
		Short:   "Delete a saved clip",
		Long:    "Delete an identity-owned clip by ID across linked accounts.",
		Example: `  hey clip delete 44`,
		RunE:    deleteCommand.run,
		Args:    usageExactOneArg(),
	}
	return deleteCommand
}

func (c *clipDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	clipID, err := parsePositiveID(args[0], "clip")
	if err != nil {
		return err
	}
	if err := sdk.Clips().Delete(cmd.Context(), clipID); err != nil {
		return apierr.FromSDK(err)
	}
	return writeMutation(cmd, fmt.Sprintf("Clip %d deleted", clipID), map[string]any{"id": clipID})
}
