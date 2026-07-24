package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

// maxCollectionPages bounds the timeline walk in case pagination never
// terminates. A collection's threads plus interleaved calendar events span at
// most a few dozen pages in practice; this is a safety backstop.
const maxCollectionPages = 200

type collectionCommand struct {
	cmd *cobra.Command
}

func newCollectionCommand() *collectionCommand {
	collectionCommand := &collectionCommand{}
	collectionCommand.cmd = &cobra.Command{
		Use:   "collection <id>",
		Short: "List topics in a collection",
		Annotations: map[string]string{
			"agent_notes": "Lists all threads (topics) in a collection, walking the full timeline. Use topic IDs with hey threads to read them. Get collection IDs from hey collections.",
		},
		Example: `  hey collection 8328
  hey collection 8328 --json`,
		RunE: collectionCommand.run,
		Args: usageExactOneArg(),
	}

	return collectionCommand
}

func (c *collectionCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	collectionID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid collection ID: %s", args[0]))
	}

	// The collection detail endpoint is HTML-only (no JSON representation) and
	// renders only the first ~50 threads per page. The rest live further down
	// the timeline, reachable via the "next page" pagination link, so walk every
	// page and accumulate the threads, de-duplicating across pages.
	ctx := cmd.Context()
	path := fmt.Sprintf("/collections/%d", collectionID)
	var topics []htmlutil.CollectionTopic
	seen := map[int64]bool{}
	var truncated bool

	for page := 0; path != ""; page++ {
		if page >= maxCollectionPages {
			truncated = true
			break
		}
		resp, err := sdk.GetHTML(ctx, path)
		if err != nil {
			return convertSDKError(err)
		}
		body := string(resp.Data)
		for _, t := range htmlutil.ParseCollectionTopicsHTML(body) {
			if !seen[t.TopicID] {
				seen[t.TopicID] = true
				topics = append(topics, t)
			}
		}
		path = htmlutil.ParseCollectionNextPage(body)
	}

	var notice string
	if truncated {
		notice = fmt.Sprintf("Stopped after %d pages; some older threads may be missing.", maxCollectionPages)
	}

	if writer.IsStyled() {
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Topic ID", "Title"})
		for _, t := range topics {
			table.addRow([]string{fmt.Sprintf("%d", t.TopicID), t.Title})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(cmd.OutOrStdout(), notice)
		}
		return nil
	}

	return writeOK(topics,
		output.WithSummary(fmt.Sprintf("%d topics in collection %d", len(topics), collectionID)),
		output.WithNotice(notice),
		output.WithBreadcrumbs(output.Breadcrumb{
			Action:      "read",
			Command:     "hey threads <topic-id>",
			Description: "Read a thread in this collection",
		}),
	)
}
