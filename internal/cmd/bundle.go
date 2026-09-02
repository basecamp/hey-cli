package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type bundleCommand struct {
	cmd   *cobra.Command
	limit int
	all   bool
	page  string
}

// bundleOutput is what `hey bundle view --json` answers with: the bundled contact next
// to the unseen postings, because the contact's id is what reads the rest of the
// bundle's mail once these threads are seen (hey contact threads <contact-id>).
type bundleOutput struct {
	ID       int64                 `json:"id"`
	Contact  generated.Contact     `json:"contact"`
	Postings []sourcePostingOutput `json:"postings"`
	NextPage string                `json:"next_page,omitempty"`
}

var bundleListing = postingsListing{
	heading: "Bundle",
	summary: func(count int, name string) string {
		return fmt.Sprintf("%d unseen %s bundled from %s", count, threadNoun(count), name)
	},
	cursorNotice: func(shown, total int) string {
		return fmt.Sprintf("Showing %d remaining results from this cursor (%d unseen threads read).", shown, total)
	},
}

func newBundleCommand() *bundleCommand {
	command := newBundleReaderCommand(
		"bundle",
		"List the unseen threads a bundle groups",
		`  hey bundle view 12345
  hey bundle view 12345 --all
  hey bundle view 12345 --json`,
	)
	command.cmd.Annotations[compatibilityUsageAnnotation] = "bundle <box-item-id>"
	command.cmd.Args = cobra.MaximumNArgs(1)
	command.cmd.AddCommand(newBundleViewCommand().cmd)
	return command
}

func newBundleViewCommand() *bundleCommand {
	return newBundleReaderCommand(
		"view <box-item-id>",
		"List the unseen threads a bundle groups",
		`  hey bundle view 12345
  hey bundle view 12345 --page next-cursor
  hey bundle view 12345 --all
  hey bundle view 12345 --json`,
	)
}

func newBundleReaderCommand(use, short, example string) *bundleCommand {
	command := &bundleCommand{}
	command.cmd = &cobra.Command{
		Use:   use,
		Short: short,
		Long:  "List the unseen email threads a bundle groups. A bundle is a hey box view row with kind \"bundle\": one sender's mail rolled into a single row instead of a thread apiece.",
		Annotations: map[string]string{
			"agent_notes": "The ID is a bundle row's own id from hey box view — a row with kind \"bundle\" and no topic_id. Returns the unseen threads the bundle groups, each with topic_id for hey thread read. A bundle read through has no unseen threads; every thread with its sender, seen and unseen, is listed by hey contact threads <contact-id>.",
		},
		Example: example,
		RunE:    command.run,
		Args:    usageExactOneArg(),
	}

	command.cmd.Flags().IntVar(&command.limit, "limit", 0, "Maximum number of threads to show")
	command.cmd.Flags().BoolVar(&command.all, "all", false, "Fetch all results (override --limit)")
	command.cmd.Flags().StringVar(&command.page, "page", "", "Continue from a next_page cursor")
	return command
}

func (c *bundleCommand) run(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if err := requireAuth(); err != nil {
		return err
	}

	postingID, err := parsePositiveID(args[0], "bundle")
	if err != nil {
		return err
	}

	first, err := sdk.Postings().BundleUnseenPage(cmd.Context(), postingID, c.page)
	if err != nil {
		return bundleNotFound(args[0], apierr.FromSDK(err))
	}
	if first == nil {
		return apierr.ErrNotFound("bundle", args[0])
	}

	contact := first.Contact
	seed := pageResult[generated.Posting]{Items: first.Postings, Cursor: first.NextPage}
	request := pageRequest{Limit: c.limit, All: c.all, MaxPages: maxPostingPages}

	listing := bundleListing
	listing.emptyNotice = fmt.Sprintf(
		"This bundle has no unseen threads — everything in it has been read. List every thread with %s: hey contact threads %d",
		terminal.SanitizeLine(contact.Name), contact.Id)
	listing.breadcrumbs = []output.Breadcrumb{
		{Action: "read", Command: "hey thread read <thread-id>", Description: "Read an email thread"},
		{Action: "contact_threads", Command: fmt.Sprintf("hey contact threads %d", contact.Id),
			Description: "List every thread with this bundle's sender, seen and unseen"},
	}
	listing.payload = func(_ mail.Source, postings []sourcePostingOutput, nextPage string, _ int) any {
		return bundleOutput{ID: postingID, Contact: contact, Postings: postings, NextPage: nextPage}
	}
	return listing.write(cmd, mail.BundleSource(postingID, contact), seed, request, c.page != "")
}

// bundleNotFound says what a 404 on the bundle route means: the ID was not a bundle
// row's. The route answers only for postings that are bundles, so a plain thread's box
// item id and a topic id both 404 here.
func bundleNotFound(identifier string, err error) error {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) && apiErr.Code == apierr.CodeNotFound {
		return apierr.ErrNotFoundHint("bundle", identifier,
			"The ID must be a bundle row's own id — a hey box view row with kind \"bundle\".")
	}
	return err
}

// bundleProbeConcurrency bounds the pre-flight bundle checks a command fans out.
const bundleProbeConcurrency = 8

// bundlePostings reports which of ids name a bundle row rather than a thread. A box
// listing mixes the two, so an id taken from `hey box view` can be either; the
// bundles/unseen route decides it, on the 404 bundleNotFound reads above. It answers for
// a bundle read through as well, carrying its contact and no postings, so a bundle is
// recognised whether or not it still holds unseen mail.
//
// The probes run concurrently because a bulk action passes many ids at once. Anything
// but a 404 is a real error and stops the command rather than passing for "not a
// bundle".
func bundlePostings(ctx context.Context, ids []int64) ([]int64, error) {
	isBundle := make([]bool, len(ids))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(bundleProbeConcurrency)
	for i, id := range ids {
		group.Go(func() error {
			page, err := sdk.Postings().BundleUnseenPage(groupCtx, id, "")
			if err != nil {
				converted := apierr.FromSDK(err)
				var apiErr *apierr.Error
				if errors.As(converted, &apiErr) && apiErr.Code == apierr.CodeNotFound {
					return nil
				}
				return converted
			}
			isBundle[i] = page != nil
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	bundles := make([]int64, 0, len(ids))
	for i, id := range ids {
		if isBundle[i] {
			bundles = append(bundles, id)
		}
	}
	return bundles, nil
}

// errBundleMove refuses a move that would relocate a bundle row. A bundle is the
// container for one sender's mail in the box that sender is delivered to; moving it takes
// the container away, so their next email arrives as a thread of its own, and threads
// delivered while the bundle is gone never join it once it returns. HEY's own web app
// offers a bundle only Mark Seen, Note and Ignore, so refusing keeps the CLI to what the
// product allows.
func errBundleMove(ids []int64) error {
	labels := make([]string, len(ids))
	for i, id := range ids {
		labels[i] = strconv.FormatInt(id, 10)
	}
	message := fmt.Sprintf("%s is a bundle row, not a thread", labels[0])
	if len(ids) > 1 {
		message = fmt.Sprintf("%s are bundle rows, not threads", strings.Join(labels, ", "))
	}
	return apierr.ErrUsageHint(message,
		"change a sender's grouping with hey contact bundle|unbundle <contact-id>")
}
