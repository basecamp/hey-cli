package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// --- Messages ---

// replyContextLoadedMsg carries what a reply needs from the thread: the entry to
// reply to, the "Re: …" subject it goes out under, the sender it goes out as, and who
// the thread is addressed to.
type replyContextLoadedMsg struct {
	requestID      uint64
	boxID          int64
	topicID        int64
	topicName      string
	entryID        int64
	sdk            *hey.Client
	actingSenderID int64
	subject        string
	to, cc         []string
	bcc            []string
	err            error
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

// draftSavedMsg reports the outcome of keeping a message as a draft on the way out.
type draftSavedMsg struct {
	form *composeForm
	err  error
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

// composeLabelWidth is the column a field's text starts at: a label right-aligned
// in eight cells and its ": ".
const composeLabelWidth = 10

// composeForm is the in-TUI editor for a new message, reply or forward. It owns
// its inputs, validation and status; sending is done by mailView so the form
// stays free of SDK calls.
type composeForm struct {
	mode                composeMode
	topicName           string
	topicID             int64  // the thread a reply or forward was written from; 0 for a new message
	entryID             int64  // reply target (composeReply only)
	replySubject        string // the "Re: …" subject a reply goes out under (composeReply only)
	replyActingSenderID int64  // the sender a reply goes out as; 0 = account default (composeReply only)
	sendSDK             *hey.Client
	forwardedContent    string

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

	// recipients is HEY's list of who can be written to, shared with the view
	// and never written through. suggest is the list open under a recipient
	// field, and typing says the last key edited that field, which is what
	// opens it.
	recipients []recipientSuggestion
	suggest    *recipientPopover
	typing     bool

	// pristine is every field as the form opened, prefill included, so that
	// esc knows whether leaving would lose anything. confirmLeave is the
	// question asked when it would.
	pristine     []string
	confirmLeave bool

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
		in := newTextInput()
		in.Prompt = ""
		in.Placeholder = placeholderFor(l)
		f.inputs = append(f.inputs, in)
	}
	f.body = newTextArea()
	f.body.Prompt = ""
	f.body.ShowLineNumbers = false
	f.body.Placeholder = "Write your message… Markdown works here"
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
	f.topicID = ctxMsg.topicID
	f.entryID = ctxMsg.entryID
	f.replySubject = ctxMsg.subject
	f.replyActingSenderID = ctxMsg.actingSenderID
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
	f.topicID = ctxMsg.topicID
	f.sendSDK = ctxMsg.sdk
	f.forwardedContent = ctxMsg.content
	f.inputs[fieldSubject].SetValue(ctxMsg.subject)
	f.body.Placeholder = "Add a note… Markdown works here"
	return f
}

func (f *composeForm) bodyIndex() int { return len(f.inputs) }

func (f *composeForm) init() tea.Cmd {
	f.pristine = f.fieldValues()
	return f.focusCurrent()
}

// fieldValues answers what every field holds, in order, body last.
func (f *composeForm) fieldValues() []string {
	values := make([]string, 0, len(f.inputs)+1)
	for i := range f.inputs {
		values = append(values, f.inputs[i].Value())
	}
	return append(values, f.body.Value())
}

// edited reports whether the form holds anything it did not open with, which
// is what leaving it would lose.
func (f *composeForm) edited() bool {
	if f.pristine == nil {
		return false
	}
	for i, value := range f.fieldValues() {
		if strings.TrimSpace(value) != strings.TrimSpace(f.pristine[i]) {
			return true
		}
	}
	return false
}

func (f *composeForm) focusCurrent() tea.Cmd {
	f.suggest = nil
	f.typing = false
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
		f.inputs[i].SetWidth(inner - composeLabelWidth + 1)
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
	if f.mode == composeReply {
		subject = f.replySubject
	} else {
		subject = strings.TrimSpace(f.inputs[fieldSubject].Value())
	}
	body = strings.TrimSpace(f.body.Value())
	if f.mode == composeForward {
		body = htmlutil.PrependHTML(f.forwardedContent, htmlutil.FromMarkdown(body))
	} else if body != "" {
		body = htmlutil.FromMarkdown(body)
	}
	return
}

// validate returns a user-facing problem, or "" when the form can be sent.
func (f *composeForm) validate() string {
	to, cc, bcc, subject, body := f.values()
	if f.mode != composeReply && len(to)+len(cc)+len(bcc) == 0 {
		return "Add at least one recipient"
	}
	if address := mail.InvalidAddress(to, cc, bcc); address != "" {
		return "Not a valid email address: " + terminal.SanitizeLine(address)
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
	if f.confirmLeave {
		return f.handleLeaveKey(view, msg)
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
	if f.handleSuggestionKey(msg) {
		return nil, true
	}
	switch {
	case msg.String() == "ctrl+t":
		return f.openSnippetPicker(view), true
	case msg.Key().Code == tea.KeyEscape:
		// Nothing typed, nothing to lose. Otherwise ask, the way HEY never lets a
		// message go without a draft of it.
		if !f.edited() {
			return nil, false
		}
		f.confirmLeave = true
		return nil, true
	case msg.String() == "shift+tab":
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
		return f.submit(view), true
	}
	return f.edit(msg), true
}

// edit hands a key to the focused field. In a recipient field, a key that changes
// what is typed narrows the list under it, and one that only moves the cursor
// closes it: the list is about the recipient being written, not the one the cursor
// happens to pass through.
func (f *composeForm) edit(msg tea.KeyPressMsg) tea.Cmd {
	if !isRecipientField(f.focus) {
		return f.update(msg)
	}
	input := &f.inputs[f.focus]
	value, position := input.Value(), input.Position()
	if msg.String() == "backspace" {
		if rest, cursor, ok := deleteRecipientBefore(value, byteOffset(value, position)); ok {
			input.SetValue(rest)
			input.SetCursor(len([]rune(rest[:cursor])))
			f.suggest = nil
			f.typing = false
			return nil
		}
	}
	cmd := f.update(msg)
	switch {
	case input.Value() != value:
		f.typing = true
		f.refreshSuggestions()
	case input.Position() != position:
		f.suggest = nil
		f.typing = false
	}
	return cmd
}

// handleLeaveKey answers the question esc asks of an edited message: keep it as a
// draft, throw it away, or go back to it.
func (f *composeForm) handleLeaveKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "s", "S", "enter":
		f.confirmLeave = false
		return f.saveDraft(view), true
	case "d", "D":
		return nil, false
	case "esc":
		f.confirmLeave = false
	}
	return nil, true
}

// saveDraft keeps the message in HEY's drafts, where the web app and the phone
// can pick it up. A draft needs nobody on it yet, but an address HEY would drop
// is refused here for the same reason a send refuses it: HEY drops it without
// saying, and the draft would come back without that recipient.
func (f *composeForm) saveDraft(view *mailView) tea.Cmd {
	to, cc, bcc, _, _ := f.values()
	if address := mail.InvalidAddress(to, cc, bcc); address != "" {
		f.setStatus("Not a valid email address: "+terminal.SanitizeLine(address), true)
		return nil
	}
	f.sending = true
	f.setStatus("Saving draft…", false)
	return view.saveDraft(f)
}

func (f *composeForm) submit(view *mailView) tea.Cmd {
	if problem := f.validate(); problem != "" {
		f.setStatus(problem, true)
		return nil
	}
	f.sending = true
	f.setStatus("Sending…", false)
	return view.send(f)
}

func (f *composeForm) handleMsg(msg tea.Msg) (tea.Cmd, bool) {
	// A paste is typing, so it is held off for as long as keys are: while the
	// close question is up, and while a send or a draft save is on its way,
	// which has already taken the form's values and would close it on top of
	// whatever was pasted.
	if _, paste := msg.(tea.PasteMsg); paste && (f.sending || f.confirmLeave) {
		return nil, true
	}
	if f.snippetPicker != nil {
		return f.snippetPicker.handleMsg(msg), true
	}
	// A paste changes a recipient field without a key press, and the list
	// under it keeps the byte range of the recipient being typed, so it has to
	// follow the change the way typing does.
	if !isRecipientField(f.focus) {
		return f.update(msg), true
	}
	input := &f.inputs[f.focus]
	value := input.Value()
	cmd := f.update(msg)
	if input.Value() != value {
		f.typing = true
		f.refreshSuggestions()
	}
	return cmd, true
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
	if f.confirmLeave {
		return []helpBinding{
			{"s", "save draft"},
			{"d", "discard"},
			{"esc", "keep editing"},
		}
	}
	if f.snippetPicker != nil {
		return f.snippetPicker.helpBindings()
	}
	if f.suggest != nil {
		return []helpBinding{
			{"↑/↓", "choose"},
			{"tab", "add"},
			{"esc", "close list"},
			{"ctrl+s", "send"},
		}
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
	form := f.view()
	switch {
	case f.confirmLeave:
		return overlayModal(form, f.leaveView(), f.width, max(lipgloss.Height(form), f.height))
	case f.suggest != nil:
		// The list hangs from the line under its field, just clear of the labels
		// so that the fields it covers still say what they are.
		list := f.suggestionsView()
		x, y := composeLabelWidth-1, 2+int(f.suggest.field)
		return overlayAt(form, list, x, y, max(lipgloss.Width(form), f.width), max(lipgloss.Height(form), y+lipgloss.Height(list)))
	}
	return form
}

// leaveView is the question esc asks of a message with something in it.
func (f *composeForm) leaveView() string {
	key := lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	body := "Keep it as a draft to finish later, here or in HEY?\n\n" +
		key.Render("s") + " save draft   " + key.Render("d") + " discard   " + key.Render("esc") + " keep editing"
	// The frame only fits its title to the screen, so the question and the
	// choices wrap to the room inside it, or a narrow terminal cuts them off.
	body = lipgloss.NewStyle().Width(modalContentWidth(f.width)).Render(body)
	return modalFrame("Close this message?", body, f.width)
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
	b.WriteString(styleInlineMarkdown(f.body.View()))
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
	return mail.SplitAddresses(s)
}

// --- mailView glue ---

// startCompose opens an empty new-message form.
func (v *mailView) startCompose() tea.Cmd {
	return v.openComposeForm(newComposeForm(composeNew, v.vc.styles))
}

// openComposeForm puts a new message, a reply or a forward on screen with the
// recipient list as it stands, and reads the list again behind it.
func (v *mailView) openComposeForm(form *composeForm) tea.Cmd {
	form.recipients = v.recipients
	v.openModal(form)
	return tea.Batch(form.init(), v.loadRecipients())
}

// loadReplyContext fetches the thread's account, the entry a reply answers — its latest
// emailed message, never a note or share notice after it — and recipients, then opens a
// reply form bound to that account's sender.
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
		entry, err := replyTargetEntry(ctx, accountSDK, topic)
		if err != nil {
			return replyContextLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		entryID := entry.Id

		// HEY's reply prefill is the authority on how a reply starts out — see
		// mail.ReplyPrefillFromServer. A failed read falls back to the local
		// computation, and so does an empty answer: on a thread with yourself,
		// everyone HEY excludes is everyone there is. The prefill's subject and
		// sender survive that recipient fallback — only the recipients needed it.
		prefill, ok := mail.ReplyPrefillFromServer(ctx, accountSDK, entryID)
		if ok {
			return replyContextLoadedMsg{
				requestID:      requestID,
				boxID:          boxID,
				topicID:        topicID,
				topicName:      topicName,
				entryID:        entryID,
				sdk:            accountSDK,
				actingSenderID: prefill.ActingSenderID,
				subject:        prefill.Subject,
				to:             prefill.Addressed.To,
				cc:             prefill.Addressed.CC,
				bcc:            prefill.Addressed.BCC,
			}
		}

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
		subject := prefill.Subject
		if subject == "" {
			subject = replySubjectFor(*message)
		}
		return replyContextLoadedMsg{
			requestID:      requestID,
			boxID:          boxID,
			topicID:        topicID,
			topicName:      topicName,
			entryID:        entryID,
			sdk:            accountSDK,
			actingSenderID: prefill.ActingSenderID,
			subject:        subject,
			to:             to,
			cc:             cc,
			bcc:            bcc,
		}
	}
}

// replySubjectFor answers the subject a reply to this message goes out under, the way
// HEY derives it in Entry::Replyable#reply_subject: a "Re: " prefix, without doubling
// one already there in any casing. HEY never derives a reply's subject server-side, so
// the reply must carry it. An empty subject stays empty rather than becoming a bare
// "Re:".
func replySubjectFor(message generated.Message) string {
	subject := strings.TrimSpace(message.Subject)
	if subject == "" {
		return ""
	}

	rest := subject
	if len(rest) >= 3 && strings.EqualFold(rest[:3], "Re:") {
		rest = strings.TrimPrefix(rest[3:], " ")
	}
	return strings.TrimRight("Re: "+rest, " ")
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

// loadForwardContext fetches HEY's prefilled forward for the latest emailed message in
// the thread, then opens the forward form on forwardContextLoadedMsg. A note posted
// after it is not what gets forwarded: it is internal to the thread.
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
		entry, err := replyTargetEntry(ctx, accountSDK, topic)
		if err != nil {
			return forwardContextLoadedMsg{requestID: requestID, boxID: boxID, err: err}
		}
		entryID := entry.Id
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

// replyTargetEntry is mail.ReplyTarget with its refusal told to the reader: a thread of
// nothing but notes and share notices has nothing a reply or a forward could answer.
func replyTargetEntry(ctx context.Context, client *hey.Client, topic *generated.Topic) (generated.Entry, error) {
	entry, err := mail.ReplyTarget(ctx, client, topic)
	if errors.Is(err, mail.ErrNoReplyableEntry) {
		return entry, fmt.Errorf("thread %d has %w; notes and share notices are never emailed", topic.Id, err)
	}
	return entry, err
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

// saveDraft keeps the open form as a draft through the SDK: a reply as a reply
// to its entry, a new message or a forward as a message of its own, which is
// how a forward is sent too.
func (v *mailView) saveDraft(f *composeForm) tea.Cmd {
	to, cc, bcc, subject, body := f.values()
	ctx := v.vc.ctx
	sdk := v.vc.sdk
	if f.sendSDK != nil {
		sdk = f.sendSDK
	}
	if f.mode == composeReply {
		entryID := f.entryID
		actingSenderID := f.replyActingSenderID
		return func() tea.Msg {
			_, err := sdk.Entries().CreateReplyDraft(ctx, entryID, actingSenderID, subject, body, to, cc, bcc)
			return draftSavedMsg{form: f, err: err}
		}
	}
	return func() tea.Msg {
		_, err := sdk.Messages().CreateDraft(ctx, hey.DraftContent{Subject: subject, Content: body, To: to, CC: cc, BCC: bcc})
		return draftSavedMsg{form: f, err: err}
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
		actingSenderID := f.replyActingSenderID
		return func() tea.Msg {
			_, err := sdk.Entries().CreateReply(ctx, entryID, actingSenderID, subject, body, to, cc, bcc)
			return composeSentMsg{label: "Reply sent", err: err}
		}
	case composeForward:
		return func() tea.Msg {
			_, err := sdk.Messages().Create(ctx, subject, body, to, cc, bcc)
			return composeSentMsg{label: "Message forwarded", err: err}
		}
	default:
		return func() tea.Msg {
			_, err := sdk.Messages().Create(ctx, subject, body, to, cc, bcc)
			return composeSentMsg{label: "Message sent", err: err}
		}
	}
}
