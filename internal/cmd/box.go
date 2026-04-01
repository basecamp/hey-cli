package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/output"
)

const maxAdditionalPages = 100

type boxCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
}

func newBoxCommand() *boxCommand {
	boxCommand := &boxCommand{}
	boxCommand.cmd = &cobra.Command{
		Use:   "box <name|id>",
		Short: "List postings in a mailbox",
		Long:  "List postings in a mailbox. Accepts a box name (imbox, feedbox, etc.) or numeric ID.",
		Annotations: map[string]string{
			"agent_notes": "Accepts box name or numeric ID. Returns postings (threads). Use thread IDs with hey threads.",
		},
		Example: `  hey box imbox
  hey box imbox --limit 10
  hey box 123 --json`,
		RunE: boxCommand.run,
		Args: validateBoxArgs,
	}

	boxCommand.cmd.Flags().IntVar(&boxCommand.limit, "limit", 0, "Maximum number of postings to show")
	boxCommand.cmd.Flags().BoolVar(&boxCommand.all, "all", false, "Fetch all results (override --limit)")

	return boxCommand
}

func validateBoxArgs(cmd *cobra.Command, args []string) error {
	switch len(args) {
	case 1:
		return nil
	case 0:
		return usageErrorf("%s <name|id> (example: hey box imbox)", cmd.CommandPath())
	default:
		return fmt.Errorf("expected 1 mailbox argument, got %d", len(args))
	}
}

func (c *boxCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	ctx := cmd.Context()
	resp, err := resolveBox(ctx, args[0])
	if err != nil {
		return err
	}

	postings, finalNextURL, err := paginateBoxPostings(ctx, resp, c.limit, c.all, fetchNextBoxPage)
	if err != nil {
		return err
	}
	hasMore := finalNextURL != ""

	total := len(postings)
	if c.limit > 0 && !c.all && len(postings) > c.limit {
		postings = postings[:c.limit]
		// Clear next_history_url when client-side truncation drops postings,
		// so JSON consumers don't skip the truncated items.
		resp.NextHistoryUrl = ""
	} else {
		resp.NextHistoryUrl = finalNextURL
	}
	notice := boxTruncationNotice(len(postings), total, hasMore, c.all)

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Box: %s (%s)\n\n", resp.Name, resp.Kind)

		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Thread", "From", "Summary", "Date"})
		for _, p := range postings {
			displayID := resolvePostingTopicID(p)
			table.addRow([]string{fmt.Sprintf("%d", displayID), p.Creator.Name, truncate(p.Summary, 60), formatDate(p.CreatedAt)})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	resp.Postings = postings
	return writeOK(resp,
		output.WithSummary(fmt.Sprintf("%d postings in %s", len(postings), resp.Name)),
		output.WithNotice(notice),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "read",
				Command:     "hey threads <id>",
				Description: "Read an email thread",
			},
			output.Breadcrumb{
				Action:      "compose",
				Command:     "hey compose --to <email> --subject <subject>",
				Description: "Compose a new message",
			},
		),
	)
}

// resolveBox fetches a box by name or ID, using named SDK getters for
// well-known box names to avoid an extra List API call.
func resolveBox(ctx context.Context, nameOrID string) (*generated.BoxShowResponse, error) {

	// Numeric ID: fetch directly
	if id, err := strconv.ParseInt(nameOrID, 10, 64); err == nil {
		resp, err := sdk.Boxes().Get(ctx, id, nil)
		if err != nil {
			return nil, convertSDKError(err)
		}
		return resp, nil
	}

	// Named getter for well-known boxes (saves a List call)
	switch strings.ToLower(nameOrID) {
	case "imbox":
		resp, err := sdk.Boxes().GetImbox(ctx, nil)
		if err != nil {
			return nil, convertSDKError(err)
		}
		return resp, nil
	case "feedbox", "the feed":
		resp, err := sdk.Boxes().GetFeedbox(ctx, nil)
		if err != nil {
			return nil, convertSDKError(err)
		}
		return resp, nil
	case "trailbox", "paper trail":
		resp, err := sdk.Boxes().GetTrailbox(ctx, nil)
		if err != nil {
			return nil, convertSDKError(err)
		}
		return resp, nil
	case "asidebox", "set aside":
		resp, err := sdk.Boxes().GetAsidebox(ctx, nil)
		if err != nil {
			return nil, convertSDKError(err)
		}
		return resp, nil
	case "laterbox", "reply later":
		resp, err := sdk.Boxes().GetLaterbox(ctx, nil)
		if err != nil {
			return nil, convertSDKError(err)
		}
		return resp, nil
	case "bubblebox", "bubbled up":
		resp, err := sdk.Boxes().GetBubblebox(ctx, nil)
		if err != nil {
			return nil, convertSDKError(err)
		}
		return resp, nil
	}

	// Unknown name: list-then-filter fallback
	result, err := sdk.Boxes().List(ctx)
	if err != nil {
		return nil, convertSDKError(err)
	}

	lower := strings.ToLower(nameOrID)
	if result != nil {
		for _, b := range *result {
			if strings.ToLower(b.Kind) == lower || strings.ToLower(b.Name) == lower {
				resp, err := sdk.Boxes().Get(ctx, b.Id, nil)
				if err != nil {
					return nil, convertSDKError(err)
				}
				return resp, nil
			}
		}
	}

	return nil, output.ErrNotFound("box", nameOrID)
}

// pageFetcher fetches the next page of box postings given a URL.
type pageFetcher func(ctx context.Context, url string) (*generated.BoxShowResponse, error)

// fetchNextBoxPage fetches a pagination URL and decodes it as a BoxShowResponse.
// It validates that the URL shares the same origin as the SDK base URL to prevent
// leaking auth credentials to a different host.
func fetchNextBoxPage(ctx context.Context, nextURL string) (*generated.BoxShowResponse, error) {
	if err := validateSameOrigin(sdk.Config().BaseURL, nextURL); err != nil {
		return nil, err
	}
	resp, err := sdk.Get(ctx, nextURL)
	if err != nil {
		return nil, convertSDKError(err)
	}
	var page generated.BoxShowResponse
	if err := resp.UnmarshalData(&page); err != nil {
		return nil, fmt.Errorf("failed to parse pagination response: %w", err)
	}
	return &page, nil
}

// validateSameOrigin checks that targetURL has the same scheme and host as baseURL.
func validateSameOrigin(baseURL, targetURL string) error {
	base, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}
	target, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("invalid pagination URL: %w", err)
	}
	if base.Scheme != target.Scheme || base.Host != target.Host {
		return fmt.Errorf("pagination URL origin %s://%s does not match base %s://%s",
			target.Scheme, target.Host, base.Scheme, base.Host)
	}
	return nil
}

// paginateBoxPostings follows next_history_url links to collect additional postings
// beyond the first page. When neither all nor a limit exceeding the first page is
// requested, it returns the first page only (preserving default behavior).
func paginateBoxPostings(ctx context.Context, firstPage *generated.BoxShowResponse, limit int, all bool, fetch pageFetcher) ([]generated.Posting, string, error) {
	postings := firstPage.Postings
	nextURL := firstPage.NextHistoryUrl

	needMore := all || (limit > 0 && len(postings) < limit)
	if !needMore || nextURL == "" {
		return postings, nextURL, nil
	}
	if fetch == nil {
		return nil, "", fmt.Errorf("paginateBoxPostings: fetch function is nil while pagination is required")
	}

	for page := 1; page <= maxAdditionalPages && nextURL != ""; page++ {
		resp, err := fetch(ctx, nextURL)
		if err != nil {
			return nil, "", err
		}
		nextURL = resp.NextHistoryUrl
		if len(resp.Postings) == 0 {
			break
		}
		postings = append(postings, resp.Postings...)

		if !all && limit > 0 && len(postings) >= limit {
			break
		}
	}

	return postings, nextURL, nil
}

// boxTruncationNotice returns a user-facing notice about truncated or paginated results.
func boxTruncationNotice(shown, fetched int, hasMore, all bool) string {
	if shown < fetched {
		return fmt.Sprintf("Showing %d of %d results. Use --all to see everything.", shown, fetched)
	}
	if hasMore && all {
		return fmt.Sprintf("Showing %d results. Pagination limit reached; not all results could be fetched.", shown)
	}
	if hasMore {
		return fmt.Sprintf("Showing %d results. More available; use --all to fetch all.", shown)
	}
	return ""
}
