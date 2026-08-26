package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/terminal"
)

type bulkReplyDraftLoadedMsg struct {
	requestID  uint64
	boxID      int64
	postingIDs []int64
	seen       bool
	draft      *generated.BulkReplyDraft
	err        error
}

type bulkReplySentMsg struct {
	delivery *generated.BulkReplyDelivery
	skipped  int
	seen     bool
	err      error
}

type bulkReplyUndoneMsg struct {
	id  int64
	err error
}

type bulkReplyForm struct {
	postingIDs []int64
	draft      generated.BulkReplyDraft
	seen       bool
	composing  bool
	preview    viewport.Model
	body       textarea.Model
	status     string
	isError    bool
	sending    bool
	styles     styles
	width      int
	height     int
}

func newBulkReplyForm(postingIDs []int64, draft *generated.BulkReplyDraft, seen bool, s styles) *bulkReplyForm {
	form := &bulkReplyForm{
		postingIDs: append([]int64(nil), postingIDs...),
		seen:       seen,
		styles:     s,
	}
	if draft != nil {
		form.draft = *draft
	}
	form.preview = viewport.New(viewport.WithWidth(80), viewport.WithHeight(24))
	form.preview.SetContent(form.previewContent(80))
	form.body = textarea.New()
	form.body.Prompt = ""
	form.body.ShowLineNumbers = false
	form.body.Placeholder = "Write the reply that every selected thread will receive…"
	return form
}

func (f *bulkReplyForm) init() tea.Cmd {
	if !f.composing {
		return nil
	}
	return f.body.Focus()
}

func (f *bulkReplyForm) resize(width, height int) {
	f.width = width
	f.height = height
	previewOffset := f.preview.YOffset()
	f.preview.SetWidth(max(width, 1))
	f.preview.SetHeight(max(height, 1))
	f.preview.SetContent(f.previewContent(width))
	f.preview.SetYOffset(previewOffset)
	f.body.SetWidth(max(width-4, 10))
	f.body.SetHeight(max(height-8, 3))
}

// handleKey routes keys while the preview or the editor is open. A form that is sending
// holds on to every key, including escape: the send is already on its way.
func (f *bulkReplyForm) handleKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.sending {
		return nil, true
	}
	if msg.Key().Code == tea.KeyEscape {
		return nil, false
	}
	if !f.composing {
		if msg.Key().Code == tea.KeyEnter {
			f.composing = true
			return f.body.Focus(), true
		}
		var cmd tea.Cmd
		f.preview, cmd = f.preview.Update(msg)
		return cmd, true
	}
	if msg.String() == "ctrl+s" {
		if strings.TrimSpace(f.body.Value()) == "" {
			f.status = "Message is empty"
			f.isError = true
			return nil, true
		}
		f.sending = true
		f.status = "Sending…"
		f.isError = false
		return view.sendBulkReply(f), true
	}
	var cmd tea.Cmd
	f.body, cmd = f.body.Update(msg)
	return cmd, true
}

func (f *bulkReplyForm) handleMsg(msg tea.Msg) (tea.Cmd, bool) {
	return f.update(msg), true
}

func (f *bulkReplyForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	if !f.composing {
		f.preview, cmd = f.preview.Update(msg)
		return cmd
	}
	f.body, cmd = f.body.Update(msg)
	return cmd
}

func (f *bulkReplyForm) helpBindings() []helpBinding {
	if !f.composing {
		return []helpBinding{{"↑↓", "review recipients"}, {"enter", "write reply"}, {"esc", "cancel"}}
	}
	return []helpBinding{{"ctrl+s", "send to all"}, {"esc", "cancel"}}
}

// restyle re-renders the preview, whose content is built with the styles it was drawn
// with rather than styled on the way out.
func (f *bulkReplyForm) restyle(s styles) {
	f.styles = s
	f.resize(f.width, f.height)
}

func (f *bulkReplyForm) draw(_ *mailView) string {
	return f.view()
}

func (f *bulkReplyForm) view() string {
	if !f.composing {
		return f.preview.View()
	}
	return f.composeView()
}

func (f *bulkReplyForm) previewContent(width int) string {
	var b strings.Builder
	b.WriteString(f.styles.title.Render("Bulk reply preview"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%d replyable %s", len(f.draft.Entries), tuiThreadNoun(len(f.draft.Entries)))
	if skipped := len(f.postingIDs) - len(f.draft.Entries); skipped > 0 {
		fmt.Fprintf(&b, " · %d skipped", skipped)
	}
	b.WriteString("\n\n")

	labelStyle := styleMuted
	for i, entry := range f.draft.Entries {
		writeBulkReplyWrappedLine(&b, fmt.Sprintf("%d. ", i+1), terminal.SanitizeLine(entry.TopicName), width)
		writeBulkReplyContacts(&b, "To", entry.Addressed.Directly, width, labelStyle)
		writeBulkReplyContacts(&b, "CC", entry.Addressed.Copied, width, labelStyle)
		writeBulkReplyContacts(&b, "BCC", entry.Addressed.Blindcopied, width, labelStyle)
		b.WriteString("\n")
	}
	if nameTag := htmlutil.ToText(f.draft.Content); nameTag != "" {
		b.WriteString(labelStyle.Render("HEY will preserve your name tag:"))
		b.WriteString("\n")
		for _, line := range wrapBulkReplyText(terminal.SanitizeLine(nameTag), max(width-4, 10)) {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		b.WriteString("\n")
	}
	b.WriteString(labelStyle.Render("Review every recipient, then press enter to write the reply."))
	return b.String()
}

func (f *bulkReplyForm) composeView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", f.styles.title.Render(fmt.Sprintf("Bulk reply to %d %s", len(f.draft.Entries), tuiThreadNoun(len(f.draft.Entries)))))
	b.WriteString(styleMuted.Render("The exact recipients from the preview and HEY's name tag will be preserved."))
	b.WriteString("\n\n")
	b.WriteString(f.body.View())
	b.WriteString("\n")
	if f.status != "" {
		statusStyle := styleMuted
		if f.isError {
			statusStyle = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString(statusStyle.Render(f.status))
	}
	return b.String()
}

func writeBulkReplyContacts(b *strings.Builder, label string, contacts []generated.Contact, width int, labelStyle lipgloss.Style) {
	fmt.Fprintf(b, "%s\n", labelStyle.Render(label+":"))
	if len(contacts) == 0 {
		fmt.Fprintln(b, "  (none)")
		return
	}
	for _, contact := range contacts {
		writeBulkReplyWrappedLine(b, "  - ", formatBulkReplyContact(contact), width)
	}
}

func formatBulkReplyContact(contact generated.Contact) string {
	name := terminal.SanitizeLine(contact.Name)
	email := terminal.SanitizeLine(contact.EmailAddress)
	switch {
	case name != "" && email != "":
		return fmt.Sprintf("%s <%s>", name, email)
	case email != "":
		return email
	default:
		return name
	}
}

func writeBulkReplyWrappedLine(b *strings.Builder, prefix, text string, width int) {
	prefixWidth := runewidth.StringWidth(prefix)
	lines := wrapBulkReplyText(text, max(width-prefixWidth, 1))
	for i, line := range lines {
		if i == 0 {
			fmt.Fprintf(b, "%s%s\n", prefix, line)
		} else {
			fmt.Fprintf(b, "%s%s\n", strings.Repeat(" ", prefixWidth), line)
		}
	}
}

func wrapBulkReplyText(text string, width int) []string {
	width = max(width, 1)
	if text == "" {
		return []string{""}
	}
	var lines []string
	var line strings.Builder
	lineWidth := 0
	for _, r := range text {
		runeWidth := runewidth.RuneWidth(r)
		if line.Len() > 0 && lineWidth+runeWidth > width {
			lines = append(lines, line.String())
			line.Reset()
			lineWidth = 0
		}
		line.WriteRune(r)
		lineWidth += runeWidth
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}

func tuiThreadNoun(count int) string {
	if count == 1 {
		return "thread"
	}
	return "threads"
}

func replyNoun(count int) string {
	if count == 1 {
		return "reply"
	}
	return "replies"
}

func (v *mailView) startBulkReply() tea.Cmd {
	postingIDs := v.actionList().selectedIDs()
	if len(postingIDs) == 0 {
		v.notice = "Select threads with space before starting a bulk reply"
		return nil
	}
	requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestBulkReply)
	boxID := v.currentBoxID()
	seen := v.seenActive
	return func() tea.Msg {
		draft, err := v.vc.sdk.BulkReplies().Draft(ctx, postingIDs)
		if err != nil && hey.AsError(err).Code == hey.CodeNotFound {
			draft = &generated.BulkReplyDraft{}
			err = nil
		}
		return bulkReplyDraftLoadedMsg{
			requestID:  requestID,
			boxID:      boxID,
			postingIDs: postingIDs,
			seen:       seen,
			draft:      draft,
			err:        err,
		}
	}
}

func (v *mailView) sendBulkReply(form *bulkReplyForm) tea.Cmd {
	entryIDs := make([]int64, len(form.draft.Entries))
	for i, entry := range form.draft.Entries {
		entryIDs[i] = entry.Id
	}
	content := htmlutil.PrependHTML(form.draft.Content, htmlutil.FromMarkdown(strings.TrimSpace(form.body.Value())))
	skipped := len(form.postingIDs) - len(entryIDs)
	return func() tea.Msg {
		delivery, err := v.vc.sdk.BulkReplies().Send(v.vc.ctx, entryIDs, content)
		return bulkReplySentMsg{delivery: delivery, skipped: max(skipped, 0), seen: form.seen, err: err}
	}
}

func (v *mailView) undoBulkReply() tea.Cmd {
	id := v.lastBulkReplyID
	if id == 0 {
		v.notice = "No delayed bulk reply is available to undo"
		return nil
	}
	v.pendingMutations++
	return func() tea.Msg {
		err := v.vc.sdk.BulkReplies().Undo(v.vc.ctx, id)
		return bulkReplyUndoneMsg{id: id, err: err}
	}
}
