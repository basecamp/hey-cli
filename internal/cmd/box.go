package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/basecamp/hey-sdk/go/pkg/hey"

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
		{Action: "read", Command: "hey thread read <thread-id>", Description: "Read an email thread"},
		{Action: "bundle", Command: "hey bundle view <box-item-id>", Description: "List the unseen threads a bundle row groups"},
		{Action: "move", Command: "hey move <box-item-id> --to <box>", Description: "Move an email thread to another box"},
		{Action: "compose", Command: "hey compose --to <email> --subject <subject>", Description: "Compose a new message"},
	},
}

func newBoxCommand() *boxCommand {
	command := newBoxReaderCommand(
		"box",
		"List HEY boxes and their email threads",
		"List HEY boxes or list email threads in one box.",
		`  hey box list
  hey box view imbox
  hey box view papertrail
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
		"List email threads in a HEY box. Accepts a box's short name (imbox, feed, papertrail, setaside, replylater, bubbleup), its kind (feedbox, trailbox, …), its display name (The Feed, Paper Trail) or its numeric ID.",
		`  hey box view imbox
  hey box view papertrail
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
			"agent_notes": "Accepts a box's short name (imbox, feed, papertrail, setaside, replylater, bubbleup), kind (feedbox, trailbox, …) or display name in any case — the spellings hey search --in and hey move --to take — or a numeric ID. Trash is not a box: use hey search --in trash. Returns email threads. Use topic_id with hey thread read, reply, and forward; use id with seen, unseen, and move. A row with kind \"bundle\" groups one sender's unseen threads and has no topic_id: list them with hey bundle view <id>, and every thread with that sender via hey contact threads <contact-id>. --page continues from the next_page cursor of an earlier listing of the same box.",
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

func validateBoxArgs(_ *cobra.Command, args []string) error {
	switch len(args) {
	case 0, 1:
		return nil
	default:
		return fmt.Errorf("expected 1 mailbox argument, got %d", len(args))
	}
}

func (c *boxCommand) run(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
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

// resolveBox fetches a box by name or ID at the page cursor. A name is any spelling
// boxKindFor knows — short name, kind or display name — and the same ones hey search --in
// and hey move --to take.
func resolveBox(ctx context.Context, nameOrID, page string) (*generated.BoxShowResponse, error) {
	var cursor *string
	if page != "" {
		cursor = &page
	}

	if id, err := strconv.ParseInt(nameOrID, 10, 64); err == nil {
		return resolveBoxByID(ctx, id, cursor)
	}

	kind := boxKindFor(nameOrID)
	if resp, named, err := readNamedBox(ctx, kind, cursor); named {
		return resp, err
	}

	// Any other box is found in the list, by the kind or the name HEY serves for it.
	result, err := sdk.Boxes().List(ctx)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}

	if result != nil {
		for _, b := range *result {
			if boxKindFor(b.Kind) == kind || boxKindFor(b.Name) == kind {
				resp, err := sdk.Boxes().Get(ctx, b.Id, &generated.GetBoxParams{Page: cursor})
				if err != nil {
					return nil, apierr.FromSDK(err)
				}
				return resp, nil
			}
		}
	}

	return nil, errBoxNotFound(nameOrID)
}

// resolveBoxByID reads a box HEY names on its own route, like a box given by name. The
// pages after the first are read there whatever the first came from (mail.ReadPage
// dispatches on the kind), and /boxes/{id} orders the Feed, the Paper Trail and Bubble Up
// differently, so a first page read by ID would repeat or skip threads on the second.
func resolveBoxByID(ctx context.Context, id int64, cursor *string) (*generated.BoxShowResponse, error) {
	result, err := sdk.Boxes().List(ctx)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}

	if result != nil {
		for _, b := range *result {
			if b.Id == id {
				if resp, named, readErr := readNamedBox(ctx, b.Kind, cursor); named {
					return resp, readErr
				}
				break
			}
		}
	}

	resp, err := sdk.Boxes().Get(ctx, id, &generated.GetBoxParams{Page: cursor})
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	return resp, nil
}

// readNamedBox reads a box HEY has a route of its own for, which pages the box in its own
// order. named is false for any other kind, which only /boxes/{id} serves.
func readNamedBox(ctx context.Context, kind string, cursor *string) (resp *generated.BoxShowResponse, named bool, err error) {
	switch kind {
	case hey.BoxKindImbox:
		resp, err = sdk.Boxes().GetImbox(ctx, &generated.GetImboxParams{Page: cursor})
	case hey.BoxKindFeed:
		resp, err = sdk.Boxes().GetFeedbox(ctx, &generated.GetFeedboxParams{Page: cursor})
	case hey.BoxKindTrail:
		resp, err = sdk.Boxes().GetTrailbox(ctx, &generated.GetTrailboxParams{Page: cursor})
	case hey.BoxKindSetAside:
		resp, err = sdk.Boxes().GetAsidebox(ctx, &generated.GetAsideboxParams{Page: cursor})
	case hey.BoxKindLater:
		resp, err = sdk.Boxes().GetLaterbox(ctx, &generated.GetLaterboxParams{Page: cursor})
	case hey.BoxKindBubbleUp:
		resp, err = sdk.Boxes().GetBubblebox(ctx, &generated.GetBubbleboxParams{Page: cursor})
	default:
		return nil, false, nil
	}
	if err != nil {
		return nil, true, apierr.FromSDK(err)
	}
	return resp, true, nil
}
