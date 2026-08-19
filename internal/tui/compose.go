package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

// --- Messages ---

// replyContextLoadedMsg carries what a reply needs from the thread: the entry to
// reply to and who the thread is addressed to.
type replyContextLoadedMsg struct {
	requestID uint64
	boxID     int64
	topicID   int64
	topicName string
	entryID   int64
	to, cc    []string
	bcc       []string
	err       error
}

// composeSentMsg reports the outcome of a send.
type composeSentMsg struct {
	label string
	err   error
}

// --- Compose form ---

type composeMode int

const (
	composeNew composeMode = iota
	composeReply
)

// composeField indexes the inputs in a form. Body is always the last field.
type composeField int

const (
	fieldTo composeField = iota
	fieldCc
	fieldBcc
	fieldSubject
	fieldBody
)

// composeForm is the in-TUI editor for a new message or a reply. It owns its
// inputs, validation and status; sending is done by mailView so the form stays
// free of SDK calls.
type composeForm struct {
	mode      composeMode
	topicName string
	entryID   int64 // reply target (composeReply only)

	inputs []textinput.Model // to, cc, bcc, subject (subject only for composeNew)
	body   textarea.Model
	focus  int // index into inputs, or len(inputs) for body

	status  string
	isError bool
	sending bool

	styles styles
	width  int
	height int
}

func newComposeForm(mode composeMode, s styles) *composeForm {
	f := &composeForm{mode: mode, styles: s}
	labels := []string{"To", "Cc", "Bcc"}
	if mode == composeNew {
		labels = append(labels, "Subject")
	}
	for _, l := range labels {
		in := textinput.New()
		in.Prompt = ""
		in.Placeholder = placeholderFor(l)
		f.inputs = append(f.inputs, in)
	}
	f.body = textarea.New()
	f.body.Prompt = ""
	f.body.ShowLineNumbers = false
	f.body.Placeholder = "Write your message…"
	return f
}

func placeholderFor(label string) string {
	switch label {
	case "To":
		return "someone@example.com, another@example.com"
	case "Subject":
		return "Subject"
	default:
		return ""
	}
}

// newReplyForm prefills recipients from the thread. Users can still edit them.
func newReplyForm(ctxMsg replyContextLoadedMsg, s styles) *composeForm {
	f := newComposeForm(composeReply, s)
	f.topicName = ctxMsg.topicName
	f.entryID = ctxMsg.entryID
	f.inputs[fieldTo].SetValue(strings.Join(ctxMsg.to, ", "))
	f.inputs[fieldCc].SetValue(strings.Join(ctxMsg.cc, ", "))
	f.inputs[fieldBcc].SetValue(strings.Join(ctxMsg.bcc, ", "))
	f.focus = len(f.inputs) // start in the body
	return f
}

func (f *composeForm) bodyIndex() int { return len(f.inputs) }

func (f *composeForm) init() tea.Cmd {
	return f.focusCurrent()
}

func (f *composeForm) focusCurrent() tea.Cmd {
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	f.body.Blur()
	if f.focus == f.bodyIndex() {
		return f.body.Focus()
	}
	return f.inputs[f.focus].Focus()
}

func (f *composeForm) resize(width, height int) {
	f.width = width
	f.height = height
	inner := max(width-4, 10)
	for i := range f.inputs {
		f.inputs[i].SetWidth(inner - 9) // room for the "Subject: " label
	}
	f.body.SetWidth(inner)
	// title + fields + blank + status + blank
	bodyH := height - len(f.inputs) - 5
	f.body.SetHeight(max(bodyH, 3))
}

func (f *composeForm) values() (to, cc, bcc []string, subject, body string) {
	to = parseAddressList(f.inputs[fieldTo].Value())
	cc = parseAddressList(f.inputs[fieldCc].Value())
	bcc = parseAddressList(f.inputs[fieldBcc].Value())
	if f.mode == composeNew {
		subject = strings.TrimSpace(f.inputs[fieldSubject].Value())
	}
	body = strings.TrimSpace(f.body.Value())
	return
}

// validate returns a user-facing problem, or "" when the form can be sent.
func (f *composeForm) validate() string {
	to, cc, bcc, subject, body := f.values()
	if f.mode == composeNew && len(to)+len(cc)+len(bcc) == 0 {
		return "Add at least one recipient"
	}
	if f.mode == composeNew && subject == "" {
		return "Subject is required"
	}
	if body == "" {
		return "Message is empty"
	}
	return ""
}

func (f *composeForm) setStatus(s string, isErr bool) {
	f.status = s
	f.isError = isErr
}

// handleKey routes keys while the form is open. It returns (cmd, send) where
// send=true means the caller should submit the form.
func (f *composeForm) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.sending {
		return nil, false
	}
	switch {
	case msg.Key().Code == tea.KeyTab && msg.Key().Mod == tea.ModShift:
		f.focus = (f.focus + f.bodyIndex()) % (f.bodyIndex() + 1)
		return f.focusCurrent(), false
	case msg.Key().Code == tea.KeyTab:
		f.focus = (f.focus + 1) % (f.bodyIndex() + 1)
		return f.focusCurrent(), false
	case msg.Key().Code == tea.KeyEnter && f.focus != f.bodyIndex():
		// Enter on a header field moves on, like tab.
		f.focus++
		return f.focusCurrent(), false
	case msg.String() == "ctrl+s":
		if problem := f.validate(); problem != "" {
			f.setStatus(problem, true)
			return nil, false
		}
		f.sending = true
		f.setStatus("Sending…", false)
		return nil, true
	}
	return f.update(msg), false
}

// update forwards a message to the focused input (keys, cursor blinks, ...).
func (f *composeForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	if f.focus == f.bodyIndex() {
		f.body, cmd = f.body.Update(msg)
		return cmd
	}
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	return cmd
}

func (f *composeForm) helpBindings() []helpBinding {
	return []helpBinding{
		{"tab", "next field"},
		{"ctrl+s", "send"},
		{"esc", "cancel"},
	}
}

func (f *composeForm) view() string {
	var b strings.Builder
	title := "New message"
	if f.mode == composeReply {
		title = "Reply"
		if f.topicName != "" {
			title += ": " + f.topicName
		}
	}
	b.WriteString(f.styles.title.Render(title))
	b.WriteString("\n")

	labels := []string{"To", "Cc", "Bcc", "Subject"}
	labelStyle := lipgloss.NewStyle().Foreground(colorMuted)
	for i := range f.inputs {
		b.WriteString(labelStyle.Render(fmt.Sprintf("%8s: ", labels[i])))
		b.WriteString(f.inputs[i].View())
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(f.body.View())
	b.WriteString("\n")

	if f.status != "" {
		st := lipgloss.NewStyle().Foreground(colorMuted)
		if f.isError {
			st = lipgloss.NewStyle().Foreground(colorError)
		}
		b.WriteString(st.Render(f.status))
	}
	return b.String()
}

// parseAddressList splits a comma-separated list, trimming blanks.
func parseAddressList(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// --- mailView glue ---

// startCompose opens an empty new-message form.
func (v *mailView) startCompose() tea.Cmd {
	v.compose = newComposeForm(composeNew, v.vc.styles)
	v.compose.resize(v.vc.width, v.vc.height)
	return v.compose.init()
}

// loadReplyContext fetches the thread's recipients and latest entry, the same
// way `hey reply` does, then opens the reply form on replyContextLoadedMsg.
func (v *mailView) loadReplyContext(topicID int64, topicName string) tea.Cmd {
	sdk := v.vc.sdk
	boxID := v.currentBoxID()
	requestID, ctx := v.beginRequest(mailRequestReply)
	return func() tea.Msg {
		topicResp, err := sdk.GetHTML(ctx, fmt.Sprintf("/topics/%d", topicID))
		if err != nil {
			return replyContextLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		addressed := htmlutil.ParseTopicAddressed(string(topicResp.Data))

		entriesResp, err := sdk.GetHTML(ctx, fmt.Sprintf("/topics/%d/entries", topicID))
		if err != nil {
			return replyContextLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		entries := htmlutil.ParseTopicEntriesHTML(string(entriesResp.Data))
		if len(entries) == 0 {
			return replyContextLoadedMsg{
				requestID: requestID,
				boxID:     boxID,
				err:       fmt.Errorf("no entries found in thread %d", topicID),
			}
		}
		return replyContextLoadedMsg{
			requestID: requestID,
			boxID:     boxID,
			topicID:   topicID,
			topicName: topicName,
			entryID:   entries[len(entries)-1].ID,
			to:        addressed.To,
			cc:        addressed.CC,
			bcc:       addressed.BCC,
		}
	}
}

// send submits the open form through the SDK.
func (v *mailView) send() tea.Cmd {
	f := v.compose
	to, cc, bcc, subject, body := f.values()
	ctx := v.vc.ctx
	sdk := v.vc.sdk
	switch f.mode {
	case composeReply:
		entryID := f.entryID
		return func() tea.Msg {
			err := sdk.Entries().CreateReply(ctx, entryID, body, to, cc, bcc)
			return composeSentMsg{label: "Reply sent", err: err}
		}
	default:
		return func() tea.Msg {
			err := sdk.Messages().Create(ctx, subject, body, to, cc, bcc)
			return composeSentMsg{label: "Message sent", err: err}
		}
	}
}
