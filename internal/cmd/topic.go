package cmd

import (
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
	"github.com/basecamp/hey-cli/internal/threadload"
)

const threadEntrySeparatorWidth = 60

// threadContact is somebody an entry names: whoever wrote it, or an address it was sent to.
type threadContact struct {
	ID           int64  `json:"id,omitempty"`
	Name         string `json:"name"`
	EmailAddress string `json:"email_address"`
}

// threadRecipients is who a message was sent to, as HEY recorded its To, Cc and Bcc
// lines. The three lists are present even when empty once the message itself was read.
type threadRecipients struct {
	To  []threadContact `json:"to"`
	CC  []threadContact `json:"cc"`
	BCC []threadContact `json:"bcc"`
}

// threadReceivedVia is one exact account address HEY recorded an inbound message
// arriving through, optionally resolved to a contact. The address is kept losslessly
// for JSON; unlike the ordinary recipients, it can carry an envelope alias or plus tag.
type threadReceivedVia struct {
	EmailAddress string         `json:"email_address"`
	Contact      *threadContact `json:"contact,omitempty"`
}

// threadEntry is one message in a thread. Body is Markdown, converted once here from
// HEY's Trix HTML; BodyHTML keeps that HTML for --html. BodyState says what became of
// the body: hydrated, bodyless (HEY served none), over_limit or failed (it was not
// read), or not_requested (the format did not need it). Recipients and received-via
// records come from the message like the body does, so they are absent exactly when the
// message was not read; received_via is also absent when HEY served none for a sent or
// generated message. The entry index carries neither kind of message metadata.
type threadEntry struct {
	ID                    int64               `json:"id"`
	CreatedAt             string              `json:"created_at"`
	UpdatedAt             string              `json:"updated_at"`
	Creator               threadContact       `json:"creator"`
	Sender                *threadContact      `json:"sender,omitempty"`
	AlternativeSenderName string              `json:"alternative_sender_name"`
	Summary               string              `json:"summary"`
	Kind                  string              `json:"kind"`
	AppURL                string              `json:"app_url"`
	Recipients            *threadRecipients   `json:"recipients,omitempty"`
	ReceivedVia           []threadReceivedVia `json:"received_via,omitempty"`
	Body                  htmlutil.Markdown   `json:"body,omitzero"`
	BodyState             string              `json:"body_state,omitempty"`
	BodyHTML              string              `json:"-"`
}

type topicCommand struct {
	cmd          *cobra.Command
	allowPartial bool
}

func newThreadCommand() *cobra.Command {
	thread := &cobra.Command{
		Use:   "thread",
		Short: "Read email threads",
		Annotations: map[string]string{
			"agent_notes": "Subcommands: read. Thread IDs come from hey box view. Use the same ID with hey reply or hey forward.",
		},
	}
	thread.AddCommand(newThreadsCommand().cmd)
	return thread
}

func newThreadsCommand() *topicCommand {
	threadsCommand := &topicCommand{}
	threadsCommand.cmd = &cobra.Command{
		Use:   "read <thread-id>",
		Short: "Read a thread",
		Long: "Read every entry in a thread, oldest first. JSON entries keep the " +
			"creator as the account user and, when HEY used another From address, " +
			"include that address as sender. Entries also include To/CC/BCC recipients " +
			"and, for inbound mail, received_via with the exact account delivery addresses HEY recorded.",
		Annotations: map[string]string{
			"agent_notes": "Returns a thread with all entries, oldest first. Entry bodies are Markdown; the creator is the account user, while a non-default send-as address is the optional sender on a hydrated entry. Each entry whose message was read carries recipients as to, cc and bcc contact lists. In JSON, inbound entries also carry received_via: the exact account delivery addresses HEY recorded, distinct from the visible recipients, with an optional resolved contact. --html writes an HTML document instead, one <article> per entry with a From/To/CC/BCC header and HEY's original body HTML. A thread that could only be read in part is refused unless --allow-partial is passed, in which case each entry's body_state says what was read. Use the topic ID with hey reply or hey forward.",
		},
		Example: `  hey thread read 12345
  hey thread read 12345 --json
  hey thread read 12345 --markdown
  hey thread read 12345 --count
  hey thread read 12345 --allow-partial`,
		RunE: threadsCommand.run,
		Args: usageExactOneArg(),
	}
	threadsCommand.cmd.Flags().BoolVar(&threadsCommand.allowPartial, "allow-partial", false,
		"Take a thread that could only be read in part, with a notice saying what is missing")

	return threadsCommand
}

func (c *topicCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	threadID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return apierr.ErrUsage(fmt.Sprintf("invalid thread ID: %s", args[0]))
	}

	// A count or a list of IDs needs the index and nothing else; every other format
	// shows bodies, so it reads them. JSON-shaped output retains recipients and inbound
	// delivery records. HTML retains only the recipients it renders. Styled and Markdown
	// output show neither, so that metadata must not consume their body budget.
	format := writer.EffectiveFormat()
	hydrate := format != output.FormatCount && format != output.FormatIDs
	load := loadThread
	switch format {
	case output.FormatJSON, output.FormatQuiet:
		load = loadThreadWithJSONMetadata
	case output.FormatHTML:
		load = loadThreadWithRecipients
	case output.FormatAuto, output.FormatStyled, output.FormatIDs, output.FormatCount, output.FormatMarkdown:
	}
	thread, err := load(cmd.Context(), threadID, hydrate)
	if err != nil {
		return err
	}
	notice := threadNotice(thread)
	if notice != "" && !c.allowPartial {
		return errPartialThread(threadID, notice)
	}
	entries := threadEntries(thread, format == output.FormatHTML)

	// The envelope carries the notice, the styled view, the Markdown document and the
	// HTML document print it; every other format — a count, IDs, --quiet — gets it on
	// stderr, so what is missing is said wherever the output cannot say it. --html gets
	// it on stderr as well: a comment in a file nobody opens is not a warning.
	if stderrNotice := paginationNoticeForStderr(format, notice); stderrNotice != "" && format != output.FormatMarkdown {
		fmt.Fprintln(cmd.ErrOrStderr(), stderrNotice)
	}

	switch format {
	case output.FormatHTML:
		return writeThreadHTML(cmd.OutOrStdout(), threadID, entries, notice)
	case output.FormatStyled:
		printThreadStyled(cmd.OutOrStdout(), entries, notice)
		return nil
	case output.FormatMarkdown:
		return writeThreadMarkdown(cmd.OutOrStdout(), threadID, entries, notice)
	case output.FormatCount, output.FormatIDs:
		return writeOK(entries)
	case output.FormatAuto, output.FormatJSON, output.FormatQuiet:
	}

	return writeOK(entries,
		output.WithSummary(fmt.Sprintf("%d entries in thread %d", len(entries), threadID)),
		output.WithNotice(notice),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "reply",
				Command:     fmt.Sprintf("hey reply %d", threadID),
				Description: "Reply to this thread",
			},
			output.Breadcrumb{
				Action:      "forward",
				Command:     fmt.Sprintf("hey forward %d --to <email>", threadID),
				Description: "Forward the latest message",
			},
		),
	)
}

// printThreadStyled writes a thread for a terminal. A body is rendered; a body that was
// read and has nothing to show says so; an entry HEY served no body for shows its
// summary, which is all HEY has for it; an entry whose body was not read says so rather
// than passing a preview off as the message.
func printThreadStyled(w io.Writer, entries []threadEntry, notice string) {
	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(w, strings.Repeat("─", threadEntrySeparatorWidth))
		}
		fmt.Fprintf(w, "From: %s  [%s]  #%d\n", terminal.SanitizeLine(threadEntrySender(e)), e.CreatedAt, e.ID)
		fmt.Fprintln(w)
		switch {
		case !e.Body.IsEmpty():
			fmt.Fprintln(w, markdown.Render(e.Body, stdoutWidth()))
		case e.BodyState == string(threadload.StateHydrated):
			fmt.Fprintln(w, "(empty body)")
		case e.BodyState == string(threadload.StateBodyless) && e.Summary != "":
			fmt.Fprintln(w, terminal.SanitizeLine(e.Summary))
		case e.BodyState == string(threadload.StateBodyless):
			fmt.Fprintln(w, "(no body)")
		default:
			fmt.Fprintf(w, "(body not read: %s)\n", e.BodyState)
		}
		fmt.Fprintln(w)
	}
	if notice != "" {
		fmt.Fprintf(w, "notice: %s\n", notice)
	}
}

// writeThreadMarkdown writes a thread as one Markdown document: a heading per entry
// naming the sender, the date and the entry ID, the body as the Markdown it already is,
// and a rule between entries. The metadata is escaped for a Markdown reader the way the
// body already was by ToMarkdown; a body that rendered to nothing says so, an entry HEY
// served no body for shows its summary, and one whose body was not read says so. A
// write that fails is the command's error, as it is for --html: a document cut short by
// a full disk must not exit 0.
func writeThreadMarkdown(w io.Writer, threadID int64, entries []threadEntry, notice string) error {
	// The document is written an entry at a time rather than assembled whole: a thread
	// is held once as Markdown, not a second time as the document.
	write := func(s string) error {
		if _, err := io.WriteString(w, s); err != nil {
			return fmt.Errorf("write thread Markdown: %w", err)
		}
		return nil
	}
	if err := write(fmt.Sprintf("# Thread %d\n", threadID)); err != nil {
		return err
	}
	for _, e := range entries {
		var b strings.Builder
		b.WriteString("\n")
		fmt.Fprintf(&b, "## From: %s — %s (#%d)\n\n", markdownSafeText(threadEntrySender(e)), e.CreatedAt, e.ID)
		if err := write(b.String()); err != nil {
			return err
		}
		var body string
		switch {
		case !e.Body.IsEmpty():
			body = e.Body.String() + "\n"
		case e.BodyState == string(threadload.StateHydrated):
			body = "*(empty body)*\n"
		case e.BodyState == string(threadload.StateBodyless) && e.Summary != "":
			body = markdownSafeText(e.Summary) + "\n"
		case e.BodyState == string(threadload.StateBodyless):
			body = "*(no body)*\n"
		default:
			body = fmt.Sprintf("*(body not read: %s)*\n", e.BodyState)
		}
		if err := write(body); err != nil {
			return err
		}
		if err := write("\n---\n"); err != nil {
			return err
		}
	}
	if notice != "" {
		return write(fmt.Sprintf("\n**Notice:** %s\n", markdownSafeText(notice)))
	}
	return nil
}

// writeThreadHTML is what --html writes for a thread: one HTML5 document, parseable by
// anything that reads HTML, titled after the thread and declaring its charset, with one
// <article> per entry, oldest first. The article carries the entry's ID, date and body
// state as data attributes, opens with a <header> naming the sender, date and non-empty
// recipient lines, and then holds the entry's original HTML exactly as HEY served it —
// the whole point of the format. An entry without a body holds nothing after its header;
// data-body-state says
// whether HEY served none (bodyless), the load left it unread (over_limit, failed) or it
// was read and was empty (hydrated). A thread read in part, which --allow-partial lets
// through as it does for every other format, ends with the notice in a comment before
// </body>, alongside the copy on stderr.
//
// A thread is a document because it has entries to frame; the single-body reads —
// journal read, contacts show, contacts note show — write a fragment instead (see
// writeNoteHTML), one body as HEY served it and nothing for an empty one, because one
// body is what gets pasted into something else. --stats is refused with --html like every
// other selector: there is no envelope here to carry stats.
//
// The sender and recipients are whatever the entry says they are, so they are sanitized
// and HTML-escaped before they are written; the date and state are this program's own and
// are escaped all the same. A write that fails is the command's error: a document
// cut short by a full disk must not exit 0.
//
// The body itself is written as HEY served it, which is the point of --html, so nothing
// here keeps a body from carrying an </article> of its own. It cannot: a body is Trix
// output, and article is not among the tags Trix keeps; an inbound email's markup
// arrives attribute-escaped inside the figure's JSON (see htmlutil). Re-serializing
// the body through a parser would hold the framing against HTML HEY does not serve, at
// the price of the verbatim contract. A reader that needs each entry as its own value
// has --json.
func writeThreadHTML(w io.Writer, threadID int64, entries []threadEntry, notice string) error {
	write := func(s string) error {
		if _, err := io.WriteString(w, s); err != nil {
			return fmt.Errorf("write thread HTML: %w", err)
		}
		return nil
	}
	if err := write(fmt.Sprintf("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>Thread %d</title>\n</head>\n<body>\n", threadID)); err != nil {
		return err
	}
	for _, e := range entries {
		sender := html.EscapeString(terminal.SanitizeLine(threadEntrySender(e)))
		createdAt := html.EscapeString(e.CreatedAt)
		var header strings.Builder
		fmt.Fprintf(&header, "<article id=\"entry-%d\" data-entry-id=\"%d\" data-created-at=\"%s\" data-body-state=\"%s\">\n<header>\n<div>From: %s — %s</div>\n",
			e.ID, e.ID, createdAt, html.EscapeString(e.BodyState), sender, createdAt)
		if e.Recipients != nil {
			writeThreadRecipientRows(&header, e.Recipients)
		}
		header.WriteString("</header>\n")
		if err := write(header.String()); err != nil {
			return err
		}
		// The body is written straight from the entry, not through the closure, so the
		// sink check sees the one raw write this format is made of and its exemption in
		// the manifest says why.
		if e.BodyHTML != "" {
			if _, err := io.WriteString(w, e.BodyHTML+"\n"); err != nil {
				return fmt.Errorf("write thread HTML: %w", err)
			}
		}
		if err := write("</article>\n"); err != nil {
			return err
		}
	}
	if notice != "" {
		if err := write("<!-- notice: " + htmlCommentSafe(notice) + " -->\n"); err != nil {
			return err
		}
	}
	return write("</body>\n</html>\n")
}

func writeThreadRecipientRows(header *strings.Builder, recipients *threadRecipients) {
	for _, row := range []struct {
		label    string
		contacts []threadContact
	}{
		{label: "To", contacts: recipients.To},
		{label: "CC", contacts: recipients.CC},
		{label: "BCC", contacts: recipients.BCC},
	} {
		if formatted := formatThreadContactsHTML(row.contacts); formatted != "" {
			fmt.Fprintf(header, "<div>%s: %s</div>\n", row.label, formatted)
		}
	}
}

func formatThreadContactsHTML(contacts []threadContact) string {
	formatted := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		name := html.EscapeString(terminal.SanitizeLine(contact.Name))
		email := html.EscapeString(terminal.SanitizeLine(contact.EmailAddress))
		switch {
		case name != "" && email != "":
			formatted = append(formatted, fmt.Sprintf("%s &lt;%s&gt;", name, email))
		case email != "":
			formatted = append(formatted, email)
		case name != "":
			formatted = append(formatted, name)
		}
	}
	return strings.Join(formatted, ", ")
}

// htmlCommentSafe keeps a value from ending the comment it is written into.
func htmlCommentSafe(value string) string {
	return strings.ReplaceAll(terminal.SanitizeLine(value), "--", "- -")
}

func threadEntrySender(entry threadEntry) string {
	if entry.Sender != nil && entry.Sender.EmailAddress != "" {
		if entry.Sender.Name != "" {
			return fmt.Sprintf("%s <%s>", terminal.SanitizeLine(entry.Sender.Name), terminal.SanitizeLine(entry.Sender.EmailAddress))
		}
		return entry.Sender.EmailAddress
	}
	switch {
	case entry.AlternativeSenderName != "":
		return entry.AlternativeSenderName
	case entry.Creator.Name != "":
		return entry.Creator.Name
	default:
		return entry.Creator.EmailAddress
	}
}

// threadEntries is a loaded thread in the CLI's shape, oldest first. A thread is held
// in one form: --html keeps each body's HTML and converts nothing, every other format
// converts each body to Markdown once and lets the HTML go, so a body is never held as
// both — Markdown can be several times the size of the HTML it came from.
func threadEntries(thread *threadload.Thread, html bool) []threadEntry {
	entries := make([]threadEntry, len(thread.Entries))
	for i := range thread.Entries {
		entries[i] = newThreadEntry(&thread.Entries[i], html)
	}
	return entries
}

func newThreadEntry(loaded *threadload.Entry, html bool) threadEntry {
	entry := loaded.Entry
	creator := entry.Creator
	createdAt := entry.CreatedAt
	updatedAt := entry.UpdatedAt
	summary := entry.Summary
	appURL := entry.AppUrl
	var body htmlutil.Markdown
	bodyHTML := ""
	var recipients *threadRecipients
	var receivedVia []threadReceivedVia
	var sender *threadContact

	if message := loaded.Message; message != nil {
		if creator.Id == 0 {
			creator = message.Creator
		}
		if createdAt.IsZero() {
			createdAt = message.CreatedAt
		}
		if updatedAt.IsZero() {
			updatedAt = message.UpdatedAt
		}
		if summary == "" {
			summary = message.Subject
		}
		if appURL == "" {
			appURL = message.Url
		}
		if html {
			bodyHTML = message.Content
		} else {
			body = htmlutil.ToMarkdown(message.Content)
		}
		recipients = newThreadRecipients(message.Addressed)
		receivedVia = newThreadReceivedVia(message.ReceivedVia)
		if message.Sender.Id != 0 {
			contact := newThreadContact(message.Sender)
			sender = &contact
		}
		// The loaded thread's copy is released as it is converted.
		loaded.Message = nil
	}

	return threadEntry{
		ID:                    entry.Id,
		CreatedAt:             formatTimestamp(createdAt),
		UpdatedAt:             formatTimestamp(updatedAt),
		AlternativeSenderName: entry.AlternativeSenderName,
		Summary:               summary,
		Kind:                  entry.Kind,
		AppURL:                appURL,
		Recipients:            recipients,
		ReceivedVia:           receivedVia,
		Body:                  body,
		BodyState:             string(loaded.State),
		BodyHTML:              bodyHTML,
		Creator:               newThreadContact(creator),
		Sender:                sender,
	}
}

func newThreadContact(contact generated.Contact) threadContact {
	return threadContact{
		ID:           contact.Id,
		Name:         contact.Name,
		EmailAddress: contact.EmailAddress,
	}
}

// newThreadRecipients is a message's recipients in the CLI's shape. Each line is a list
// even when empty, so a reader ranging over `to` never has to ask whether it is there:
// a message HEY served is a message whose recipients are known.
func newThreadRecipients(addressed generated.Addressed) *threadRecipients {
	return &threadRecipients{
		To:  threadContacts(addressed.Directly),
		CC:  threadContacts(addressed.Copied),
		BCC: threadContacts(addressed.Blindcopied),
	}
}

func newThreadReceivedVia(deliveries []generated.MessageReceivedVia) []threadReceivedVia {
	receivedVia := make([]threadReceivedVia, len(deliveries))
	for i, delivery := range deliveries {
		receivedVia[i].EmailAddress = delivery.EmailAddress
		if delivery.Contact != nil {
			contact := newThreadContact(*delivery.Contact)
			receivedVia[i].Contact = &contact
		}
	}
	return receivedVia
}

func threadContacts(contacts []generated.Contact) []threadContact {
	described := make([]threadContact, len(contacts))
	for i, contact := range contacts {
		described[i] = newThreadContact(contact)
	}
	return described
}
