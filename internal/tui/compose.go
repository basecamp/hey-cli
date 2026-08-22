package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

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
	sdk       *hey.Client
	to, cc    []string
	bcc       []string
	err       error
}

// forwardContextLoadedMsg carries HEY's prefilled subject and quoted message
// for forwarding the latest entry in a thread.
type forwardContextLoadedMsg struct {
	requestID uint64
	boxID     int64
	topicID   int64
	topicName string
	sdk       *hey.Client
	subject   string
	content   string
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
	composeForward
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

// composeForm is the in-TUI editor for a new message, reply or forward. It owns
// its inputs, validation and status; sending is done by mailView so the form
// stays free of SDK calls.
type composeForm struct {
	mode             composeMode
	topicName        string
	entryID          int64 // reply target (composeReply only)
	sendSDK          *hey.Client
	forwardedContent string

	inputs []textinput.Model // to, cc, bcc, subject (subject omitted for replies)
	body   textarea.Model
	focus  int // index into inputs, or len(inputs) for body

	status  string
	isError bool
	sending bool

	snippetPicker     *snippetPicker
	availableSnippets []generated.Snippet
	snippetsLoaded    bool
	snippetRequestID  uint64

	styles styles
	width  int
	height int
}

func newComposeForm(mode composeMode, s styles) *composeForm {
	f := &composeForm{mode: mode, styles: s}
	labels := []string{"To", "Cc", "Bcc"}
	if mode != composeReply {
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
	f.sendSDK = ctxMsg.sdk
	f.inputs[fieldTo].SetValue(strings.Join(ctxMsg.to, ", "))
	f.inputs[fieldCc].SetValue(strings.Join(ctxMsg.cc, ", "))
	f.inputs[fieldBcc].SetValue(strings.Join(ctxMsg.bcc, ", "))
	f.focus = len(f.inputs) // start in the body
	return f
}

func newForwardForm(ctxMsg forwardContextLoadedMsg, s styles) *composeForm {
	f := newComposeForm(composeForward, s)
	f.topicName = ctxMsg.topicName
	f.sendSDK = ctxMsg.sdk
	f.forwardedContent = ctxMsg.content
	f.inputs[fieldSubject].SetValue(ctxMsg.subject)
	f.body.Placeholder = "Add a note…"
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
	if f.snippetPicker != nil {
		f.snippetPicker.resize(width, height)
	}
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
	if f.mode != composeReply {
		subject = strings.TrimSpace(f.inputs[fieldSubject].Value())
	}
	body = strings.TrimSpace(f.body.Value())
	if f.mode == composeForward {
		body = htmlutil.PrependText(f.forwardedContent, body)
	}
	return
}

// validate returns a user-facing problem, or "" when the form can be sent.
func (f *composeForm) validate() string {
	to, cc, bcc, subject, body := f.values()
	if f.mode != composeReply && len(to)+len(cc)+len(bcc) == 0 {
		return "Add at least one recipient"
	}
	if f.mode != composeReply && subject == "" {
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

// handleKey routes keys while the form is open. A form that is sending holds on to
// every key, including escape: the send is already on its way.
func (f *composeForm) handleKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if f.sending {
		return nil, true
	}
	if f.snippetPicker != nil {
		picker := f.snippetPicker
		cmd, open, snippet := picker.handleKey(msg)
		if !open {
			f.snippetPicker = nil
			f.focus = picker.returnFocus
			return f.focusCurrent(), true
		}
		if snippet != nil {
			f.body.InsertString(snippet.Content)
			f.snippetPicker = nil
			f.focus = f.bodyIndex()
			return f.focusCurrent(), true
		}
		return cmd, true
	}
	switch {
	case msg.String() == "ctrl+t":
		return f.openSnippetPicker(view), true
	case msg.Key().Code == tea.KeyEscape:
		return nil, false
	case msg.Key().Code == tea.KeyTab && msg.Key().Mod == tea.ModShift:
		f.focus = (f.focus + f.bodyIndex()) % (f.bodyIndex() + 1)
		return f.focusCurrent(), true
	case msg.Key().Code == tea.KeyTab:
		f.focus = (f.focus + 1) % (f.bodyIndex() + 1)
		return f.focusCurrent(), true
	case msg.Key().Code == tea.KeyEnter && f.focus != f.bodyIndex():
		// Enter on a header field moves on, like tab.
		f.focus++
		return f.focusCurrent(), true
	case msg.String() == "ctrl+s":
		if problem := f.validate(); problem != "" {
			f.setStatus(problem, true)
			return nil, true
		}
		f.sending = true
		f.setStatus("Sending…", false)
		return view.send(f), true
	}
	return f.update(msg), true
}

func (f *composeForm) handleMsg(msg tea.Msg) (tea.Cmd, bool) {
	if f.snippetPicker != nil {
		return f.snippetPicker.handleMsg(msg), true
	}
	return f.update(msg), true
}

func (f *composeForm) openSnippetPicker(view *mailView) tea.Cmd {
	picker := newSnippetPicker(f.focus)
	picker.resize(f.width, f.height)
	f.snippetPicker = picker
	if f.snippetsLoaded {
		picker.loaded(f.availableSnippets, nil)
		return picker.focus()
	}
	f.snippetRequestID++
	return tea.Batch(picker.focus(), view.loadSnippets(f, f.snippetRequestID))
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
	if f.snippetPicker != nil {
		return f.snippetPicker.helpBindings()
	}
	return []helpBinding{
		{"tab", "next field"},
		{"ctrl+t", "snippets"},
		{"ctrl+s", "send"},
		{"esc", "cancel"},
	}
}

func (f *composeForm) restyle(s styles) {
	f.styles = s
}

func (f *composeForm) draw(view *mailView) string {
	if f.snippetPicker != nil {
		return f.snippetPicker.view(view.vc.styles, view.vc.width)
	}
	return f.view()
}

func (f *composeForm) view() string {
	var b strings.Builder
	title := "New message"
	switch f.mode {
	case composeNew:
	case composeReply:
		title = "Reply"
	case composeForward:
		title = "Forward"
	}
	if f.mode != composeNew && f.topicName != "" {
		title += ": " + f.topicName
	}
	b.WriteString(f.styles.title.Render(title))
	b.WriteString("\n")

	labels := []string{"To", "Cc", "Bcc", "Subject"}
	labelStyle := styleMuted
	for i := range f.inputs {
		b.WriteString(labelStyle.Render(fmt.Sprintf("%8s: ", labels[i])))
		b.WriteString(f.inputs[i].View())
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(f.body.View())
	b.WriteString("\n")
	if f.mode == composeForward {
		b.WriteString(labelStyle.Render("The original message will be included."))
		b.WriteString("\n")
	}

	if f.status != "" {
		st := styleMuted
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
	form := newComposeForm(composeNew, v.vc.styles)
	v.openModal(form)
	return form.init()
}

// loadReplyContext fetches the thread's account, latest entry, and recipients,
// then opens a reply form bound to that account's sender.
func (v *mailView) loadReplyContext(topicID int64, topicName string) tea.Cmd {
	sdk := v.vc.sdk
	boxID := v.currentBoxID()
	requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestReply)
	return func() tea.Msg {
		topic, err := sdk.Topics().Get(ctx, topicID)
		if err != nil {
			return replyContextLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		if topic == nil || len(topic.Entries) == 0 {
			return replyContextLoadedMsg{
				requestID: requestID,
				boxID:     boxID,
				err:       fmt.Errorf("no entries found in thread %d", topicID),
			}
		}
		accountSDK, err := v.clientForTopicAccount(ctx, topic.AccountId)
		if err != nil {
			return replyContextLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		entryID := topic.Entries[len(topic.Entries)-1].Id
		message, err := accountSDK.Messages().Get(ctx, entryID)
		if err != nil {
			return replyContextLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		if message == nil {
			return replyContextLoadedMsg{
				requestID: requestID,
				boxID:     boxID,
				err:       fmt.Errorf("message %d returned no data", entryID),
			}
		}
		to, cc, bcc := recipientsForReplyTo(*message)
		return replyContextLoadedMsg{
			requestID: requestID,
			boxID:     boxID,
			topicID:   topicID,
			topicName: topicName,
			entryID:   entryID,
			sdk:       accountSDK,
			to:        to,
			cc:        cc,
			bcc:       bcc,
		}
	}
}

// recipientsForReplyTo answers who a reply to this message goes to: the message's own
// recipients, with whoever sent it moved onto the To line. That is what HEY does in
// Entry::Addressed#participating_contacts_in_reply_by_kind, so a reply reaches the
// person who wrote the message as well as everyone they wrote to.
func recipientsForReplyTo(message generated.Message) (to, cc, bcc []string) {
	sender := message.Sender.EmailAddress
	if sender == "" {
		sender = message.Creator.EmailAddress
	}

	to = addressesOf(message.Addressed.Directly, sender)
	if sender != "" {
		to = append(to, sender)
	}
	return to, addressesOf(message.Addressed.Copied, sender), addressesOf(message.Addressed.Blindcopied, sender)
}

// addressesOf answers the contacts' email addresses, dropping blanks, repeats, and the
// one address HEY addresses directly instead.
func addressesOf(contacts []generated.Contact, excluding string) []string {
	seen := map[string]bool{strings.ToLower(excluding): true}
	var addresses []string
	for _, contact := range contacts {
		address := strings.TrimSpace(contact.EmailAddress)
		key := strings.ToLower(address)
		if address != "" && !seen[key] {
			seen[key] = true
			addresses = append(addresses, address)
		}
	}
	return addresses
}

// loadForwardContext fetches HEY's prefilled forward for the latest entry in
// the thread, then opens the forward form on forwardContextLoadedMsg.
func (v *mailView) loadForwardContext(topicID int64, topicName string) tea.Cmd {
	sdk := v.vc.sdk
	boxID := v.currentBoxID()
	requestID, ctx := v.requests.begin(v.vc.ctx, mailRequestForward)
	return func() tea.Msg {
		topic, err := sdk.Topics().Get(ctx, topicID)
		if err != nil {
			return forwardContextLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		if topic == nil || len(topic.Entries) == 0 {
			return forwardContextLoadedMsg{
				requestID: requestID,
				boxID:     boxID,
				err:       fmt.Errorf("no entries found in thread %d", topicID),
			}
		}
		accountSDK, err := v.clientForTopicAccount(ctx, topic.AccountId)
		if err != nil {
			return forwardContextLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		entryID := topic.Entries[len(topic.Entries)-1].Id
		draft, err := accountSDK.Entries().NewForward(ctx, entryID)
		if err != nil {
			return forwardContextLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		if draft == nil {
			return forwardContextLoadedMsg{
				requestID: requestID,
				boxID:     boxID,
				err:       fmt.Errorf("thread %d returned no forward draft", topicID),
			}
		}
		return forwardContextLoadedMsg{
			requestID: requestID,
			boxID:     boxID,
			topicID:   topicID,
			topicName: topicName,
			sdk:       accountSDK,
			subject:   draft.Subject,
			content:   draft.Content,
		}
	}
}

func (v *mailView) clientForTopicAccount(ctx context.Context, accountID int64) (*hey.Client, error) {
	if accountID <= 0 {
		return nil, fmt.Errorf("thread did not identify its mail account")
	}
	if v.vc.rootSDK == nil {
		return nil, fmt.Errorf("mail account switching is unavailable")
	}
	return v.vc.rootSDK.ForAccount(ctx, accountID)
}

func (v *mailView) loadSnippets(form *composeForm, requestID uint64) tea.Cmd {
	ctx := v.vc.ctx
	sdk := v.vc.sdk
	if form.sendSDK != nil {
		sdk = form.sendSDK
	}
	return func() tea.Msg {
		snippets, err := sdk.Snippets().List(ctx)
		return snippetsLoadedMsg{form: form, requestID: requestID, snippets: snippets, err: err}
	}
}

// send submits the open form through the SDK.
func (v *mailView) send(f *composeForm) tea.Cmd {
	to, cc, bcc, subject, body := f.values()
	ctx := v.vc.ctx
	sdk := v.vc.sdk
	if f.sendSDK != nil {
		sdk = f.sendSDK
	}
	switch f.mode {
	case composeReply:
		entryID := f.entryID
		return func() tea.Msg {
			err := sdk.Entries().CreateReply(ctx, entryID, body, to, cc, bcc)
			return composeSentMsg{label: "Reply sent", err: err}
		}
	case composeForward:
		return func() tea.Msg {
			err := sdk.Messages().Create(ctx, subject, body, to, cc, bcc)
			return composeSentMsg{label: "Message forwarded", err: err}
		}
	default:
		return func() tea.Msg {
			err := sdk.Messages().Create(ctx, subject, body, to, cc, bcc)
			return composeSentMsg{label: "Message sent", err: err}
		}
	}
}
