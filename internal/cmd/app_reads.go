package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// The reads the HEY app answers on an app profile: the door's imbox.list, screener.list and
// drafts.list behind hey box view imbox, hey screener list and history, and hey draft list.
// Rows keep the field names the server's answers use; a fact only the app has rides along
// under its own name, and one only the server has is absent. An id is the app's own.

// appThread is one Imbox row as the door's imbox.list answers it: id is the row's, topic
// the thread's.
type appThread struct {
	ID        int64  `json:"id"`
	Topic     int64  `json:"topic"`
	Subject   string `json:"subject"`
	Summary   string `json:"summary"`
	Sender    string `json:"sender"`
	SenderID  int64  `json:"sender_id"`
	Seen      bool   `json:"seen"`
	BubbledUp bool   `json:"bubbled_up"`
}

type appImbox struct {
	Threads []appThread `json:"threads"`
	HasMore *bool       `json:"has_more"`
}

// appBoxOutput is hey box view's --json answer on the app: the box's kind and name, as the
// server's box payload carries them, and its postings.
type appBoxOutput struct {
	Kind     string       `json:"kind"`
	Name     string       `json:"name"`
	Postings []appPosting `json:"postings"`
}

// appPosting is an Imbox row under the server posting's field names, with only the fields
// the app has: a generated.Posting would add the server's empty ones.
type appPosting struct {
	ID        int64      `json:"id"`
	TopicID   int64      `json:"topic_id"`
	Kind      string     `json:"kind"`
	Name      string     `json:"name"`
	Summary   string     `json:"summary"`
	Seen      bool       `json:"seen"`
	BubbledUp bool       `json:"bubbled_up"`
	Creator   appCreator `json:"creator"`
}

// appCreator is a posting's sender; id is the app's sender id.
type appCreator struct {
	ID   int64  `json:"id,omitempty"`
	Name string `json:"name"`
}

// runApp answers hey box view from the app, which holds the Imbox alone.
func (c *boxCommand) runApp(cmd *cobra.Command, box string) error {
	if boxKindFor(box) != hey.BoxKindImbox {
		return appNotOffered(fmt.Sprintf("The box %q", box))
	}
	if err := appRefusesAll(c.all); err != nil {
		return err
	}
	if c.page != "" {
		return appNotOffered("--page")
	}
	result, err := callApp(cmd, "imbox.list", nil)
	if err != nil {
		return err
	}
	imbox, err := decodeAppAnswer("imbox.list", result, func(imbox appImbox) error {
		if imbox.Threads == nil {
			return errors.New("it lists no threads")
		}
		// Missing, has_more would read as a complete Imbox, and an id as thread 0.
		if imbox.HasMore == nil {
			return errors.New("it does not say whether the Imbox holds more")
		}
		for _, thread := range imbox.Threads {
			if thread.ID <= 0 || thread.Topic <= 0 {
				return errors.New("it lists a thread without an ID")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	rows := make([]appPosting, 0, len(imbox.Threads))
	for _, thread := range imbox.Threads {
		rows = append(rows, appPosting{
			ID: thread.ID, TopicID: thread.Topic, Kind: "topic", Name: thread.Subject, Summary: thread.Summary,
			Seen: thread.Seen, BubbledUp: thread.BubbledUp, Creator: appCreator{ID: thread.SenderID, Name: thread.Sender},
		})
	}
	notice := ""
	if c.limit > 0 && len(rows) > c.limit {
		notice = appTruncationNotice(c.limit, len(rows))
		rows = rows[:c.limit]
	}
	if *imbox.HasMore {
		notice = strings.TrimSpace(notice + " The Imbox holds more threads than the HEY app has loaded.")
	}

	const name = "Imbox"
	switch writer.EffectiveFormat() {
	case output.FormatStyled:
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Box: %s\n\n", name)
		table := newTable(out)
		table.addRow([]string{"ID", "Thread", "From", "Summary"})
		for _, row := range rows {
			table.addRow([]string{
				fmt.Sprintf("%d", row.ID),
				fmt.Sprintf("%d", row.TopicID),
				terminal.SanitizeLine(row.Creator.Name),
				truncate(terminal.SanitizeLine(row.Summary), 60),
			})
		}
		table.print()
		if notice != "" {
			fmt.Fprintln(out, notice)
		}
		return nil
	case output.FormatIDs, output.FormatCount:
		if stderrNotice := paginationNoticeForStderr(writer.EffectiveFormat(), notice); stderrNotice != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
		}
		return writeOK(rows)
	case output.FormatMarkdown:
		fmt.Fprintf(cmd.OutOrStdout(), "# %s\n\n", name)
		markdown := make([]sourcePostingRow, 0, len(rows))
		for _, row := range rows {
			markdown = append(markdown, sourcePostingRow{ID: row.ID, TopicID: row.TopicID, From: row.Creator.Name, Summary: row.Summary})
		}
		if err := writeOK(markdown); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\n**Total threads:** %d\n", len(rows))
		if notice != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", markdownSafeText(notice))
		}
		return nil
	default:
		return writeOK(appBoxOutput{Kind: hey.BoxKindImbox, Name: name, Postings: rows},
			output.WithSummary(boxSummary(len(rows), name)),
			output.WithNotice(notice),
			output.WithMeta("has_more", *imbox.HasMore),
		)
	}
}

// appSender is a sender as the door's screener.list answers one. Its id is the app's
// sender, where the server's screener answers a clearance.
type appSender struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	ScreenedIn *bool  `json:"screened_in,omitempty"`
}

type appScreener struct {
	Waiting      []appSender `json:"waiting"`
	WaitingCount *int        `json:"waiting_count"`
	Decided      []appSender `json:"decided"`
}

// readAppScreener reads the door's screener.list, refusing an answer whose missing parts
// would read as nobody waiting, sender 0 or a sender screened out.
func readAppScreener(cmd *cobra.Command) (appScreener, error) {
	result, err := callApp(cmd, "screener.list", nil)
	if err != nil {
		return appScreener{}, err
	}
	return decodeAppAnswer("screener.list", result, func(screener appScreener) error {
		if screener.Waiting == nil || screener.Decided == nil {
			return errors.New("it lists neither who waits nor who was decided")
		}
		if screener.WaitingCount == nil {
			return errors.New("it does not count who waits")
		}
		if *screener.WaitingCount < len(screener.Waiting) {
			return fmt.Errorf("it counts %d waiting but lists %d", *screener.WaitingCount, len(screener.Waiting))
		}
		for _, sender := range append(append([]appSender{}, screener.Waiting...), screener.Decided...) {
			if sender.ID <= 0 {
				return errors.New("it lists a sender without an ID")
			}
		}
		for _, sender := range screener.Decided {
			if sender.ScreenedIn == nil {
				return fmt.Errorf("it does not say whether sender %d was screened in or out", sender.ID)
			}
		}
		return nil
	})
}

// appIDKind names what an id is where the app's differs from the server's.
func appIDKind(kind string) output.ResponseOption {
	return output.WithMeta("id_kind", kind)
}

func (c *screenerListCommand) runApp(cmd *cobra.Command) error {
	if c.page != "" {
		return appNotOffered("--page")
	}
	if err := appRefusesAll(c.all); err != nil {
		return err
	}
	screener, err := readAppScreener(cmd)
	if err != nil {
		return err
	}
	waitingCount := *screener.WaitingCount
	if writer.EffectiveFormat() == output.FormatCount {
		fmt.Fprintln(cmd.OutOrStdout(), waitingCount)
		return nil
	}
	pending := make([]pendingClearance, 0, len(screener.Waiting))
	for _, sender := range screener.Waiting {
		pending = append(pending, pendingClearance{ID: sender.ID, Name: sender.Name, Email: sender.Address})
	}
	notice := ""
	if unlisted := waitingCount - len(pending); unlisted > 0 {
		notice = fmt.Sprintf("%d more %s waiting than the HEY app lists.", unlisted, senderNoun(unlisted))
	}

	if writer.IsStyled() {
		if len(pending) == 0 && notice == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "Nobody is waiting to be screened")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Sender", "Name", "Email"})
		for _, sender := range pending {
			table.addRow([]string{fmt.Sprintf("%d", sender.ID), truncate(sender.Name, 24), truncate(sender.Email, 32)})
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
	return writeOK(pending,
		output.WithSummary(fmt.Sprintf("%d %s waiting", waitingCount, senderNoun(waitingCount))),
		output.WithNotice(notice),
		output.WithMeta("total_count", waitingCount),
		appIDKind("sender"),
	)
}

func (c *screenerHistoryCommand) runApp(cmd *cobra.Command) error {
	if c.page != "" {
		return appNotOffered("--page")
	}
	if err := appRefusesAll(c.all); err != nil {
		return err
	}
	screener, err := readAppScreener(cmd)
	if err != nil {
		return err
	}
	screened := make([]screenedClearance, 0, len(screener.Decided))
	for _, sender := range screener.Decided {
		// The server's clearance statuses for the same two decisions.
		status := "denied"
		if *sender.ScreenedIn {
			status = "approved"
		}
		screened = append(screened, screenedClearance{ID: sender.ID, Status: status, Name: sender.Name, Email: sender.Address})
	}

	if writer.IsStyled() {
		if len(screened) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Nobody has been screened yet")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Sender", "Status", "Name", "Email"})
		for _, sender := range screened {
			table.addRow([]string{fmt.Sprintf("%d", sender.ID), sender.Status, truncate(sender.Name, 24), truncate(sender.Email, 32)})
		}
		table.print()
		return nil
	}
	return writeOK(screened,
		output.WithSummary(fmt.Sprintf("%d %s screened", len(screened), senderNoun(len(screened)))),
		appIDKind("sender"),
	)
}

// appDraft is a draft as the door's drafts.list answers one, under the server's field
// names where the server has the field: saved_at is updated_at, sends_at is
// scheduled_delivery_at. id is the app's; made_as, kind, sendable and the addresses are
// the app's alone.
type appDraft struct {
	ID                  int64      `json:"id"`
	Subject             string     `json:"subject,omitempty"`
	Summary             string     `json:"summary,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at,omitzero"`
	ScheduledDeliveryAt *time.Time `json:"scheduled_delivery_at,omitempty"`
	MadeAs              *int64     `json:"made_as,omitempty"`
	Kind                string     `json:"kind"`
	Thread              *int64     `json:"thread,omitempty"`
	Account             string     `json:"account"`
	From                string     `json:"from"`
	To                  []string   `json:"to"`
	Cc                  []string   `json:"cc"`
	Bcc                 []string   `json:"bcc"`
	Sendable            bool       `json:"sendable"`
}

// appDoorDraft is the door's own spelling of a draft.
type appDoorDraft struct {
	ID       int64      `json:"id"`
	MadeAs   *int64     `json:"made_as,omitempty"`
	Kind     string     `json:"kind"`
	Thread   *int64     `json:"thread,omitempty"`
	Account  string     `json:"account"`
	From     string     `json:"from"`
	To       []string   `json:"to"`
	Cc       []string   `json:"cc"`
	Bcc      []string   `json:"bcc"`
	Subject  string     `json:"subject"`
	Summary  string     `json:"summary"`
	SavedAt  time.Time  `json:"saved_at"`
	SendsAt  *time.Time `json:"sends_at,omitempty"`
	Sendable *bool      `json:"sendable"`
}

func (c *draftsCommand) runApp(cmd *cobra.Command) error {
	if c.page != "" {
		return appNotOffered("--page")
	}
	if err := appRefusesAll(c.all); err != nil {
		return err
	}
	result, err := callApp(cmd, "drafts.list", nil)
	if err != nil {
		return err
	}
	answer, err := decodeAppAnswer("drafts.list", result, func(answer struct {
		Drafts []appDoorDraft `json:"drafts"`
	}) error {
		if answer.Drafts == nil {
			return errors.New("it lists no drafts")
		}
		for _, draft := range answer.Drafts {
			if draft.ID <= 0 {
				return errors.New("it gives a draft without an ID")
			}
			// Missing, sendable would read as a draft HEY would not send.
			if draft.Sendable == nil {
				return fmt.Errorf("it does not say whether draft %d can be sent", draft.ID)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	drafts := make([]appDraft, 0, len(answer.Drafts))
	for _, d := range answer.Drafts {
		drafts = append(drafts, appDraftFrom(d))
	}
	notice := ""
	if c.limit > 0 && len(drafts) > c.limit {
		notice = appTruncationNotice(c.limit, len(drafts))
		drafts = drafts[:c.limit]
	}

	if writer.IsStyled() {
		if len(drafts) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No drafts.")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"ID", "Summary", "Subject", "Date"})
		for _, d := range drafts {
			table.addRow([]string{fmt.Sprintf("%d", d.ID), truncate(d.Summary, 60), d.Subject, formatDate(d.UpdatedAt)})
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
	return writeOK(drafts,
		output.WithSummary(fmt.Sprintf("%d drafts", len(drafts))),
		output.WithNotice(notice),
	)
}

// appDraftFrom is a draft the door answered under the server's field names. Its sendable
// has been checked to be there.
func appDraftFrom(d appDoorDraft) appDraft {
	return appDraft{
		ID: d.ID, Subject: d.Subject, Summary: d.Summary, UpdatedAt: d.SavedAt, ScheduledDeliveryAt: d.SendsAt,
		MadeAs: d.MadeAs, Kind: d.Kind, Thread: d.Thread, Account: d.Account,
		From: d.From, To: orEmpty(d.To), Cc: orEmpty(d.Cc), Bcc: orEmpty(d.Bcc), Sendable: *d.Sendable,
	}
}

// orEmpty keeps a missing address list a list in the JSON, never null.
func orEmpty(addresses []string) []string {
	if addresses == nil {
		return []string{}
	}
	return addresses
}

// appTruncationNotice is output.TruncationNotice for the app, which refuses --all
// (appRefusesAll): the rest is seen by asking again without --limit.
func appTruncationNotice(shown, total int) string {
	return fmt.Sprintf("Showing %d of %d results. Run it again without --limit to see them all.", shown, total)
}

// appRefusesAll refuses --all on the app, which answers with what it has loaded and has no
// further page to fetch: an answer to "all" that may be partial would read as complete.
func appRefusesAll(all bool) error {
	if all {
		return appNotOffered("--all")
	}
	return nil
}
