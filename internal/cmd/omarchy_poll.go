package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
)

// hey omarchy poll is the engine under the 37signals.hey bar plugin
// (github.com/basecamp/omarchy-hey-plugin): one Imbox read returns the
// postings the panel renders and, with --notify, diffs them against the
// fingerprint file to toast new mail. The plugin owns the rendering — icon,
// panel, settings — and hey-cli owns what needs HEY's semantics: pagination,
// what counts as new, and a toast that honors Do Not Disturb.

type omarchyCommand struct {
	cmd *cobra.Command
}

func newOmarchyCommand() *omarchyCommand {
	omarchyCommand := &omarchyCommand{}
	omarchyCommand.cmd = &cobra.Command{
		Use:   "omarchy",
		Short: "Omarchy shell integration, used by the 37signals.hey bar plugin",
	}
	omarchyCommand.cmd.AddCommand(newOmarchyPollCommand().cmd)
	return omarchyCommand
}

type omarchyPollCommand struct {
	cmd    *cobra.Command
	limit  int
	notify bool
	env    omarchyEnv
}

// omarchyPollEnv supplies the environment the poll toasts through; tests swap
// in a recorder so no notification daemon is ever reached.
var omarchyPollEnv = liveOmarchyEnv

func newOmarchyPollCommand() *omarchyPollCommand {
	omarchyPollCommand := &omarchyPollCommand{env: omarchyPollEnv()}
	omarchyPollCommand.cmd = &cobra.Command{
		Use:   "poll",
		Short: "Read the Imbox for the bar plugin, toasting new mail with --notify",
		Long: `Read the Imbox the way hey box imbox --json does, for the Omarchy bar plugin to
render. The response is the same shape: the box with its postings, newest first,
truncated to --limit.

With --notify, the same read also toasts newly unseen Imbox mail via
omarchy-notification-send: at most one toast per poll, replacing the previous one
rather than stacking, identified as HEY so notification silencing applies. The
first poll seeds silently and never toasts the backlog, and a poll without
--notify forgets the seed, so turning toasts on always starts silent. A toast that
cannot be sent is retried on the next poll; it never fails the command.`,
		Example: `  hey omarchy poll --limit 50 --json
  hey omarchy poll --limit 50 --notify --json`,
		Annotations: map[string]string{
			"agent_notes": "Run by the 37signals.hey Omarchy bar plugin on its poll interval. Prefer hey box imbox for reading the Imbox yourself; --notify keeps per-user toast state in the state directory.",
		},
		Args: cobra.NoArgs,
		RunE: omarchyPollCommand.run,
	}
	omarchyPollCommand.cmd.Flags().IntVar(&omarchyPollCommand.limit, "limit", 50, "Maximum number of threads to return")
	omarchyPollCommand.cmd.Flags().BoolVar(&omarchyPollCommand.notify, "notify", false, "Toast new unseen Imbox mail")
	return omarchyPollCommand
}

// errConfigDegraded is set by the root pre-run when the global configuration could
// not be loaded and a config-ignoring command went on with the defaults. The
// poll then reports it instead of answering for a guessed server.
var errConfigDegraded error

func (c *omarchyPollCommand) run(cmd *cobra.Command, args []string) error {
	if c.limit < 1 {
		return output.ErrUsage("--limit must be at least 1")
	}
	if errConfigDegraded != nil {
		return &output.Error{Code: "config_error", Message: "configuration could not be loaded: " + errConfigDegraded.Error(),
			Hint: "fix " + filepath.Join(config.ConfigDir(), "config.json") + " and poll again", Cause: errConfigDegraded}
	}
	if err := requireAuth(); err != nil {
		return err
	}
	// The omarchy command is exempt from pre-run account scoping so the auth
	// check above comes first; the configured account still applies.
	ctx := cmd.Context()
	if err := selectConfiguredAccount(ctx); err != nil {
		return err
	}
	// Toasts need the unseen set: capped on a steady-state poll (new mail always
	// lands on page 1), exhaustive when seeding — a first run or a new identity —
	// so no pre-existing thread can later read as new.
	// With no absolute state directory (HOME and XDG_STATE_HOME unset, or a
	// relative XDG_STATE_HOME) the state would land in the working directory,
	// wherever the shell started us: no fingerprints are touched at all then,
	// and the toasts stay off the way they do when the identity is unknown.
	identity, notifyPages := "", 0
	switch {
	case !omarchyPollStateUsable():
	case !c.notify:
		_ = removeOmarchyPollState()
	default:
		id, ok := omarchyPollIdentity(ctx)
		if !ok {
			break
		}
		identity = id
		if state, existed := loadOmarchyPollState(); !existed || state.Identity != identity {
			notifyPages = unseenSeedPageCap
		} else {
			notifyPages = unseenPageCap
		}
	}
	resp, unseen, complete, err := pollImbox(ctx, c.limit, notifyPages)
	if identity != "" && resp != nil {
		// Also after a later page failed: the pages that were read are a valid
		// (incomplete) snapshot, and notifyNewMail keeps fingerprints it could
		// not see rather than pruning them.
		withOmarchyPollLock(func() { notifyNewMail(c.env, identity, unseen, complete) })
	}
	if err != nil {
		return err
	}

	fetched := len(resp.Postings)
	hasMore := resp.NextHistoryUrl != ""
	if fetched > c.limit {
		resp.Postings = resp.Postings[:c.limit]
		// As in hey box: a client-side cut clears next_history_url so a
		// consumer following it cannot skip the truncated postings.
		resp.NextHistoryUrl = ""
	}
	notice := pollTruncationNotice(len(resp.Postings), fetched, hasMore)

	if writer.IsStyled() {
		printBoxTable(cmd.OutOrStdout(), resp, resp.Postings, notice)
		return nil
	}
	return writeOK(resp,
		output.WithSummary(boxSummary(len(resp.Postings), resp.Name)),
		output.WithNotice(notice),
		output.WithBreadcrumbs(boxBreadcrumbs()...),
	)
}

func pollTruncationNotice(shown, fetched int, hasMore bool) string {
	if shown < fetched || hasMore {
		return fmt.Sprintf("Showing %d results. More available; raise --limit to fetch more.", shown)
	}
	return ""
}

// Page limits for following an all-unseen Imbox. A steady-state poll stops at
// ten pages — three hundred unseen threads — because new mail always lands on
// page 1 and older threads are already fingerprinted. Seeding reads the whole
// unseen set (bounded only as the box command is) so that no pre-existing
// thread can later surface as new; it happens once per identity.
var (
	unseenPageCap = 10
	// The +1 is the initial page: maxAdditionalPages caps pages fetched after
	// it (as in paginateBoxPostings), so a box whose unseen set spans exactly
	// the cap still finds its closing seen page and seeds completely.
	unseenSeedPageCap = maxAdditionalPages + 1
)

// pollImbox reads the Imbox once for both consumers. Pages are followed while
// the panel still wants postings (fewer than limit so far, capped as hey box
// is) or while the toasts still need them: HEY orders Imbox postings
// unseen-first, so a page holding any seen posting (or nothing at all) closes
// the unseen set, and an all-unseen page means the next one is fetched, up to
// notifyPages (zero when not notifying). The response carries every posting
// read and the last page's next_history_url; unseen is the unseen postings
// among them and complete reports whether they are the whole unseen Imbox. A
// later page that cannot be fetched is returned as the error alongside what
// was read — the panel keeps its last complete list rather than showing a
// short one, while the toasts still get their (incomplete) snapshot.
func pollImbox(ctx context.Context, limit, notifyPages int) (resp *generated.BoxShowResponse, unseen []generated.Posting, complete bool, err error) {
	resp, err = sdk.Boxes().GetImbox(ctx, nil)
	if err != nil {
		return nil, nil, false, convertSDKError(err)
	}
	if resp == nil {
		return nil, nil, false, output.ErrAPI(0, "empty Imbox response")
	}
	page, postings := resp, resp.Postings
	for pages, additional := 1, 0; ; pages++ {
		for _, posting := range page.Postings {
			if posting.Seen {
				complete = true
			} else {
				unseen = append(unseen, posting)
			}
		}
		if len(page.Postings) == 0 || page.NextHistoryUrl == "" {
			complete = true
			break
		}
		wantForPanel := len(postings) < limit && additional < maxAdditionalPages
		wantForToasts := !complete && pages < notifyPages
		if !wantForPanel && !wantForToasts {
			break
		}
		next, ferr := fetchNextBoxPage(ctx, page.NextHistoryUrl)
		if ferr == nil && next == nil {
			ferr = output.ErrAPI(0, "empty Imbox page")
		}
		if ferr != nil {
			err = ferr
			break
		}
		page, additional = next, additional+1
		postings = append(postings, page.Postings...)
	}
	resp.Postings = postings
	resp.NextHistoryUrl = page.NextHistoryUrl
	return resp, unseen, complete, err
}
