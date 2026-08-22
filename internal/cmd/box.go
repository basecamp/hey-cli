package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
)

type boxCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
	page  string
}

// boxOutput is HEY's box payload with the listing's postings over the top of it, so the
// thread ID arrives next to the box item ID without anything the box already answered
// going missing.
type boxOutput struct {
	generated.BoxShowResponse
	Postings []sourcePostingOutput `json:"postings"`
	NextPage string                `json:"next_page,omitempty"`
}

var boxListing = postingsListing{
	heading: "Box",
	summary: boxSummary,
	cursorNotice: func(shown, total int) string {
		return fmt.Sprintf("Showing %d remaining results from this cursor (%d threads read).", shown, total)
	},
	breadcrumbs: []output.Breadcrumb{
		{Action: "read", Command: "hey threads <topic-id>", Description: "Read an email thread"},
		{Action: "move", Command: "hey move <id> --to <box>", Description: "Move an email thread to another box"},
		{Action: "compose", Command: "hey compose --to <email> --subject <subject>", Description: "Compose a new message"},
	},
}

func newBoxCommand() *boxCommand {
	command := newBoxReaderCommand(
		"box <name|id>",
		"List HEY boxes and their email threads",
		"List HEY boxes or list email threads in one box.",
		`  hey box list
  hey box view imbox
  hey box view imbox --limit 10
  hey box view 123 --json`,
	)
	command.cmd.Annotations[compatibilityUsageAnnotation] = "box <name|id>"
	command.cmd.AddCommand(newBoxListCommand().cmd)
	command.cmd.AddCommand(newBoxViewCommand().cmd)
	return command
}

func newBoxViewCommand() *boxCommand {
	return newBoxReaderCommand(
		"view <name|id>",
		"List email threads in a box",
		"List email threads in a HEY box. Accepts a box name (imbox, feedbox, etc.) or numeric ID.",
		`  hey box view imbox
  hey box view imbox --limit 10
  hey box view imbox --page next-cursor
  hey box view 123 --json`,
	)
}

func newBoxReaderCommand(use, short, long, example string) *boxCommand {
	command := &boxCommand{}
	command.cmd = &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Annotations: map[string]string{
			"agent_notes": "Accepts a box name or numeric ID. Returns email threads. Use topic_id with hey threads, reply, and forward; use id with seen, unseen, and move. --page continues from the next_page cursor of an earlier listing of the same box.",
		},
		Example: example,
		RunE:    command.run,
		Args:    validateBoxArgs,
	}

	command.cmd.Flags().IntVar(&command.limit, "limit", 0, "Maximum number of threads to show")
	command.cmd.Flags().BoolVar(&command.all, "all", false, "Fetch all results (override --limit)")
	command.cmd.Flags().StringVar(&command.page, "page", "", "Continue from a next_page cursor")

	return command
}

func validateBoxArgs(cmd *cobra.Command, args []string) error {
	switch len(args) {
	case 1:
		return nil
	case 0:
		return usageErrorf("%s <name|id> (example: hey box view imbox)", cmd.CommandPath())
	default:
		return fmt.Errorf("expected 1 mailbox argument, got %d", len(args))
	}
}

func (c *boxCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	resp, err := resolveBox(cmd.Context(), args[0], boxPageArgument(c.page))
	if err != nil {
		return err
	}

	seed := pageResult[generated.Posting]{Items: resp.Postings, Cursor: resp.NextHistoryUrl}
	request := pageRequest{Limit: c.limit, All: c.all, MaxPages: maxPostingPages}

	listing := boxListing
	listing.payload = boxPayload(resp)
	return listing.write(cmd, mail.BoxSource(resp), seed, request, c.page != "")
}

func boxSummary(count int, name string) string {
	return fmt.Sprintf("%d %s in %s", count, threadNoun(count), name)
}

// boxPayload answers with the box HEY served, its postings replaced by the ones the
// listing read and its cursor by the one the next read carries on from. next_page is that
// cursor on its own, which is what --page takes; next_history_url keeps the whole URL.
func boxPayload(box *generated.BoxShowResponse) func(mail.Source, []sourcePostingOutput, string, int) any {
	return func(_ mail.Source, postings []sourcePostingOutput, nextPage string, _ int) any {
		served := *box
		served.NextHistoryUrl = nextPage
		return boxOutput{BoxShowResponse: served, Postings: postings, NextPage: boxPageCursor(nextPage)}
	}
}

// boxPageArgument reads what --page was given. A box's cursor is served inside
// next_history_url, so the URL is accepted as readily as the cursor itself — and only the
// cursor is ever sent anywhere.
func boxPageArgument(page string) string {
	if cursor := boxPageCursor(page); cursor != "" {
		return cursor
	}
	return page
}

func boxPageCursor(nextHistoryURL string) string {
	parsed, err := url.Parse(nextHistoryURL)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("page")
}

// resolveBox fetches a box by name or ID at the page cursor, using named SDK getters for
// well-known box names to avoid an extra List API call.
func resolveBox(ctx context.Context, nameOrID, page string) (*generated.BoxShowResponse, error) {
	var cursor *string
	if page != "" {
		cursor = &page
	}

	// Numeric ID: fetch directly
	if id, err := strconv.ParseInt(nameOrID, 10, 64); err == nil {
		resp, err := sdk.Boxes().Get(ctx, id, &generated.GetBoxParams{Page: cursor})
		if err != nil {
			return nil, apierr.FromSDK(err)
		}
		return resp, nil
	}

	// Named getter for well-known boxes (saves a List call)
	switch strings.ToLower(nameOrID) {
	case "imbox":
		resp, err := sdk.Boxes().GetImbox(ctx, &generated.GetImboxParams{Page: cursor})
		if err != nil {
			return nil, apierr.FromSDK(err)
		}
		return resp, nil
	case "feedbox", "the feed":
		resp, err := sdk.Boxes().GetFeedbox(ctx, &generated.GetFeedboxParams{Page: cursor})
		if err != nil {
			return nil, apierr.FromSDK(err)
		}
		return resp, nil
	case "trailbox", "paper trail":
		resp, err := sdk.Boxes().GetTrailbox(ctx, &generated.GetTrailboxParams{Page: cursor})
		if err != nil {
			return nil, apierr.FromSDK(err)
		}
		return resp, nil
	case "asidebox", "set aside":
		resp, err := sdk.Boxes().GetAsidebox(ctx, &generated.GetAsideboxParams{Page: cursor})
		if err != nil {
			return nil, apierr.FromSDK(err)
		}
		return resp, nil
	case "laterbox", "reply later":
		resp, err := sdk.Boxes().GetLaterbox(ctx, &generated.GetLaterboxParams{Page: cursor})
		if err != nil {
			return nil, apierr.FromSDK(err)
		}
		return resp, nil
	case "bubblebox", "bubbled up":
		resp, err := sdk.Boxes().GetBubblebox(ctx, &generated.GetBubbleboxParams{Page: cursor})
		if err != nil {
			return nil, apierr.FromSDK(err)
		}
		return resp, nil
	}

	// Unknown name: list-then-filter fallback
	result, err := sdk.Boxes().List(ctx)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}

	lower := strings.ToLower(nameOrID)
	if result != nil {
		for _, b := range *result {
			if strings.ToLower(b.Kind) == lower || strings.ToLower(b.Name) == lower {
				resp, err := sdk.Boxes().Get(ctx, b.Id, &generated.GetBoxParams{Page: cursor})
				if err != nil {
					return nil, apierr.FromSDK(err)
				}
				return resp, nil
			}
		}
	}

	return nil, apierr.ErrNotFound("box", nameOrID)
}
