package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

type bulkReplyDraftLoadedMsg struct {
	requestID  uint64
	boxID      int64
	postingIDs []int64
	draft      *generated.BulkReplyDraft
	err        error
}

type bulkReplySentMsg struct {
	delivery *generated.BulkReplyDelivery
	skipped  int
	err      error
}

type bulkReplyUndoneMsg struct {
	id  int64
	err error
}

type bulkReplyForm struct {
	postingIDs []int64
	draft      generated.BulkReplyDraft
	composing  bool
	body       textarea.Model
	status     string
	isError    bool
	sending    bool
	styles     styles
	width      int
	height     int
}

func newBulkReplyForm(postingIDs []int64, draft *generated.BulkReplyDraft, s styles) *bulkReplyForm {
	form := &bulkReplyForm{
		postingIDs: append([]int64(nil), postingIDs...),
		styles:     s,
	}
	if draft != nil {
		form.draft = *draft
	}
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
	f.body.SetWidth(max(width-4, 10))
	f.body.SetHeight(max(height-8, 3))
}

func (f *bulkReplyForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.sending {
		return nil, false
	}
	if !f.composing {
		if msg.Key().Code == tea.KeyEnter {
			f.composing = true
			return f.body.Focus(), false
		}
		return nil, false
	}
	if msg.String() == "ctrl+s" {
		if strings.TrimSpace(f.body.Value()) == "" {
			f.status = "Message is empty"
			f.isError = true
			return nil, false
		}
		f.sending = true
		f.status = "Sending…"
		f.isError = false
		return nil, true
	}
	var cmd tea.Cmd
	f.body, cmd = f.body.Update(msg)
	return cmd, false
}

func (f *bulkReplyForm) update(msg tea.Msg) tea.Cmd {
	if !f.composing {
		return nil
	}
	var cmd tea.Cmd
	f.body, cmd = f.body.Update(msg)
	return cmd
}

func (f *bulkReplyForm) helpBindings() []helpBinding {
	if !f.composing {
		return []helpBinding{{"enter", "write reply"}, {"esc", "cancel"}}
	}
	return []helpBinding{{"ctrl+s", "send to all"}, {"esc", "cancel"}}
}

func (f *bulkReplyForm) view() string {
	if !f.composing {
		return f.previewView()
	}
	return f.composeView()
}

func (f *bulkReplyForm) previewView() string {
	var b strings.Builder
	b.WriteString(f.styles.title.Render("Bulk reply preview"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%d replyable %s", len(f.draft.Entries), tuiThreadNoun(len(f.draft.Entries)))
	if skipped := len(f.postingIDs) - len(f.draft.Entries); skipped > 0 {
		fmt.Fprintf(&b, " · %d skipped", skipped)
	}
	b.WriteString("\n\n")

	labelStyle := lipgloss.NewStyle().Foreground(colorMuted)
	for i, entry := range f.draft.Entries {
		fmt.Fprintf(&b, "%d. %s\n", i+1, terminalSafeAttachmentText(entry.TopicName))
		fmt.Fprintf(&b, "%s %s\n", labelStyle.Render("   To:"), formatBulkReplyContacts(entry.Addressed.Directly))
		if len(entry.Addressed.Copied) > 0 {
			fmt.Fprintf(&b, "%s %s\n", labelStyle.Render("   CC:"), formatBulkReplyContacts(entry.Addressed.Copied))
		}
		if len(entry.Addressed.Blindcopied) > 0 {
			fmt.Fprintf(&b, "%s %s\n", labelStyle.Render("  BCC:"), formatBulkReplyContacts(entry.Addressed.Blindcopied))
		}
		b.WriteString("\n")
	}
	if nameTag := htmlutil.ToText(f.draft.Content); nameTag != "" {
		b.WriteString(labelStyle.Render("HEY will preserve your name tag: "))
		b.WriteString(terminalSafeAttachmentText(nameTag))
		b.WriteString("\n\n")
	}
	b.WriteString(labelStyle.Render("Review every recipient, then press enter to write the reply."))
	return b.String()
}

func (f *bulkReplyForm) composeView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", f.styles.title.Render(fmt.Sprintf("Bulk reply to %d %s", len(f.draft.Entries), tuiThreadNoun(len(f.draft.Entries)))))
	b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render("The exact recipients from the preview and HEY's name tag will be preserved."))
	b.WriteString("\n\n")
	b.WriteString(f.body.View())
	b.WriteString("\n")
	if f.status != "" {
		statusStyle := lipgloss.NewStyle().Foreground(colorMuted)
		if f.isError {
			statusStyle = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString(statusStyle.Render(f.status))
	}
	return b.String()
}

func formatBulkReplyContacts(contacts []generated.Contact) string {
	formatted := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		name := terminalSafeAttachmentText(contact.Name)
		email := terminalSafeAttachmentText(contact.EmailAddress)
		switch {
		case name != "" && email != "":
			formatted = append(formatted, fmt.Sprintf("%s <%s>", name, email))
		case email != "":
			formatted = append(formatted, email)
		case name != "":
			formatted = append(formatted, name)
		}
	}
	return strings.Join(formatted, ", ")
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
	postingIDs := v.postingList.selectedIDs()
	if len(postingIDs) == 0 {
		v.notice = "Select threads with space before starting a bulk reply"
		return nil
	}
	requestID, ctx := v.beginRequest(mailRequestBulkReply)
	boxID := v.currentBoxID()
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
			draft:      draft,
			err:        err,
		}
	}
}

func (v *mailView) sendBulkReply() tea.Cmd {
	form := v.bulkReply
	entryIDs := make([]int64, len(form.draft.Entries))
	for i, entry := range form.draft.Entries {
		entryIDs[i] = entry.Id
	}
	content := htmlutil.PrependText(form.draft.Content, form.body.Value())
	skipped := len(form.postingIDs) - len(entryIDs)
	return func() tea.Msg {
		delivery, err := v.vc.sdk.BulkReplies().Send(v.vc.ctx, entryIDs, content)
		return bulkReplySentMsg{delivery: delivery, skipped: max(skipped, 0), err: err}
	}
}

func (v *mailView) undoBulkReply() tea.Cmd {
	id := v.lastBulkReplyID
	if id == 0 {
		v.notice = "No delayed bulk reply is available to undo"
		return nil
	}
	return func() tea.Msg {
		err := v.vc.sdk.BulkReplies().Undo(v.vc.ctx, id)
		return bulkReplyUndoneMsg{id: id, err: err}
	}
}
