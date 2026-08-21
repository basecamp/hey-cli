package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"golang.org/x/sync/errgroup"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// Contact is someone in the reader's address book. An alias is a contact in its own
// right — another address the same person writes from — which is why Aliases nests.
type Contact struct {
	ID           int64
	Name         string
	EmailAddress string
	Aliases      []Contact
}

type contactRequestKind int

const (
	contactRequestNone contactRequestKind = iota
	contactRequestList
	contactRequestDetail
	contactRequestMutation
)

type contactsLoadedMsg struct {
	requestResult
	page     int
	contacts []Contact
}

type contactDetailLoadedMsg struct {
	requestResult
	contact Contact
	note    string
}

type contactSavedMsg struct {
	requestResult
	originalID int64
	contact    Contact
	created    bool
}

type contactHiddenMsg struct {
	requestResult
	contact Contact
}

type contactRevealedMsg struct {
	requestResult
	contact Contact
}

type contactNoteSavedMsg struct {
	requestResult
	note    string
	deleted bool
}

type contactsView struct {
	vc *viewContext

	list                contactList
	loaded              bool
	page                int
	detail              Contact
	note                string
	inDetail            bool
	detailView          viewport.Model
	contactForm         *contactForm
	noteForm            *contactNoteForm
	lastHiddenID        int64
	pendingSavedContact bool
	pendingOriginalID   int64
	confirmNoteDelete   bool
	notice              string

	requests requestLane[contactRequestKind]
}

func newContactsView(vc *viewContext) *contactsView {
	return &contactsView{
		vc:         vc,
		page:       1,
		detailView: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
	}
}

func (v *contactsView) Init() tea.Cmd {
	if v.loaded {
		return nil
	}
	return v.requestContacts(1)
}

func (v *contactsView) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case contactsLoadedMsg:
		if cmd, ok := v.requests.settle(msg.requestResult); !ok {
			return cmd, true
		}
		if len(msg.contacts) == 0 && v.loaded && msg.page > v.page {
			v.notice = "No more contacts"
			return nil, true
		}
		v.loaded = true
		v.page = msg.page
		v.list.setContacts(msg.contacts)
		return nil, true

	case contactDetailLoadedMsg:
		if !v.requests.accepts(msg.requestResult) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			v.pendingSavedContact = false
			v.pendingOriginalID = 0
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		if v.pendingSavedContact {
			if v.pendingOriginalID != 0 && v.pendingOriginalID != msg.contact.ID {
				v.list.remove(v.pendingOriginalID)
			}
			v.updateContactInList(msg.contact)
			v.pendingSavedContact = false
			v.pendingOriginalID = 0
		}
		v.detail = msg.contact
		v.note = msg.note
		v.inDetail = true
		v.refreshDetailView()
		return nil, true

	case contactSavedMsg:
		if !v.requests.accepts(msg.requestResult) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			var conflict *hey.ContactConflictError
			if errors.As(msg.err, &conflict) && conflict.ContactID != 0 {
				v.contactForm = nil
				v.notice = contactSaveFailure(msg.err)
				v.pendingSavedContact = true
				v.pendingOriginalID = msg.originalID
				return v.requestContactDetail(conflict.ContactID), true
			}
			if v.contactForm != nil {
				v.contactForm.saving = false
				v.contactForm.status = contactSaveFailure(msg.err)
				v.contactForm.isError = true
			}
			return nil, true
		}
		v.contactForm = nil
		if msg.created {
			v.notice = "Contact added"
		} else {
			v.notice = "Contact updated"
		}
		if msg.originalID != 0 && msg.originalID != msg.contact.ID {
			v.list.remove(msg.originalID)
		}
		v.updateContactInList(msg.contact)
		return v.requestContactDetail(msg.contact.ID), true

	case contactHiddenMsg:
		if cmd, ok := v.requests.settle(msg.requestResult); !ok {
			return cmd, true
		}
		v.lastHiddenID = msg.contact.ID
		v.list.remove(msg.contact.ID)
		v.inDetail = false
		v.detail = Contact{}
		v.note = ""
		v.notice = "Contact hidden"
		return nil, true

	case contactRevealedMsg:
		if cmd, ok := v.requests.settle(msg.requestResult); !ok {
			return cmd, true
		}
		v.lastHiddenID = 0
		v.notice = "Contact shown again"
		return v.requestContacts(v.page), true

	case contactNoteSavedMsg:
		if !v.requests.accepts(msg.requestResult) {
			return nil, true
		}
		v.requests.finish(msg.requestID)
		if msg.err != nil {
			if v.noteForm != nil {
				v.noteForm.saving = false
				v.noteForm.status = errorNotice("Save failed", msg.err)
				v.noteForm.isError = true
				return nil, true
			}
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		v.noteForm = nil
		v.note = msg.note
		if msg.deleted {
			v.notice = "Private note deleted"
		} else {
			v.notice = "Private note saved"
		}
		v.refreshDetailView()
		return nil, true
	}

	if v.contactForm != nil {
		return v.contactForm.update(msg), true
	}
	if v.noteForm != nil {
		return v.noteForm.update(msg), true
	}
	if v.inDetail {
		var cmd tea.Cmd
		v.detailView, cmd = v.detailView.Update(msg)
		return cmd, cmd != nil
	}
	return nil, false
}

func (v *contactsView) View() string {
	if v.contactForm != nil {
		return v.contactForm.view()
	}
	if v.noteForm != nil {
		return v.noteForm.view()
	}
	if v.inDetail {
		if v.notice != "" {
			return v.vc.styles.title.Render(v.notice) + "\n" + v.detailView.View()
		}
		return v.detailView.View()
	}
	if v.notice != "" {
		return v.vc.styles.title.Render(v.notice) + "\n" + v.list.view()
	}
	return v.list.view()
}

func (v *contactsView) HelpBindings() []helpBinding {
	if v.contactForm != nil {
		return v.contactForm.helpBindings()
	}
	if v.noteForm != nil {
		return v.noteForm.helpBindings()
	}
	if v.inDetail {
		bindings := []helpBinding{{"e", "edit"}, {"n", "edit note"}, {"h", "hide"}}
		if v.note != "" {
			label := "delete note"
			if v.confirmNoteDelete {
				label = "confirm delete"
			}
			bindings = append(bindings, helpBinding{"x", label})
		}
		return bindings
	}
	bindings := []helpBinding{{"a", "add"}, {"r", "refresh"}, {"n/p", "pages"}}
	if v.lastHiddenID != 0 {
		bindings = append(bindings, helpBinding{"u", "show again"})
	}
	return bindings
}

func (v *contactsView) SubnavItems() ([]navItem, int, string, bool) {
	return nil, 0, fmt.Sprintf("Contacts (page %d)", v.page), true
}

func (v *contactsView) SubnavLeft() tea.Cmd  { return nil }
func (v *contactsView) SubnavRight() tea.Cmd { return nil }

func (v *contactsView) HandleContentKey(msg tea.KeyPressMsg) tea.Cmd {
	if v.requests.loading {
		return nil
	}
	if msg.String() != "x" {
		v.confirmNoteDelete = false
	}
	v.notice = ""
	if v.contactForm != nil {
		if msg.Key().Code == tea.KeyEscape && !v.contactForm.saving {
			v.contactForm = nil
			return nil
		}
		cmd, submit := v.contactForm.handleKey(msg)
		if submit {
			return v.saveContact()
		}
		return cmd
	}
	if v.noteForm != nil {
		if msg.Key().Code == tea.KeyEscape && !v.noteForm.saving {
			v.noteForm = nil
			return nil
		}
		cmd, submit := v.noteForm.handleKey(msg)
		if submit {
			return v.saveNote()
		}
		return cmd
	}
	if v.inDetail {
		switch msg.String() {
		case "e":
			return v.startEditContact()
		case "n":
			return v.startNote()
		case "h":
			return v.hideContact()
		case "x":
			if v.note != "" {
				if !v.confirmNoteDelete {
					v.confirmNoteDelete = true
					v.notice = "Press x again to permanently delete this note"
					return nil
				}
				v.confirmNoteDelete = false
				return v.deleteNote()
			}
		}
		var cmd tea.Cmd
		v.detailView, cmd = v.detailView.Update(msg)
		return cmd
	}

	switch msg.Key().Code {
	case tea.KeyUp:
		v.list.moveUp()
	case tea.KeyDown:
		v.list.moveDown()
	case tea.KeyEnter:
		if contact := v.list.selected(); contact != nil {
			return v.requestContactDetail(contact.ID)
		}
	default:
		switch msg.String() {
		case "a":
			return v.startAddContact()
		case "r":
			return v.requestContacts(v.page)
		case "n":
			return v.requestContacts(v.page + 1)
		case "p":
			if v.page > 1 {
				return v.requestContacts(v.page - 1)
			}
			v.notice = "Already on the first contacts page"
		case "u":
			if v.lastHiddenID != 0 {
				return v.revealContact(v.lastHiddenID)
			}
		}
	}
	return nil
}

func (v *contactsView) InThread() bool { return v.inDetail }

func (v *contactsView) ExitDetail(_ string) {
	if v.requests.loading && v.requests.kind == contactRequestMutation {
		return
	}
	v.ExitThread()
}

func (v *contactsView) ExitThread() {
	v.inDetail = false
	v.pendingSavedContact = false
	v.pendingOriginalID = 0
	v.contactForm = nil
	v.noteForm = nil
	v.detail = Contact{}
	v.note = ""
	v.confirmNoteDelete = false
	v.requests.cancel()
}

func (v *contactsView) CancelPendingDetail() bool {
	if v.requests.kind != contactRequestDetail {
		return false
	}
	v.pendingSavedContact = false
	v.pendingOriginalID = 0
	v.requests.cancel()
	return true
}

func (v *contactsView) CapturingInput() bool {
	return v.contactForm != nil || v.noteForm != nil
}

func (v *contactsView) AccountSwitchBlocked() bool {
	return v.requests.loading && v.requests.kind == contactRequestMutation
}

func (v *contactsView) Resize(width, height int) {
	v.list.setSize(width, height)
	v.detailView.SetWidth(width)
	v.detailView.SetHeight(height)
	if v.contactForm != nil {
		v.contactForm.resize(width, height)
	}
	if v.noteForm != nil {
		v.noteForm.resize(width, height)
	}
}

func (v *contactsView) Loading() bool { return v.requests.loading }

// Restyle re-renders the cached contact detail and hands the new styles to any
// open form. The contact list renders live.
func (v *contactsView) Restyle() {
	if v.inDetail {
		offset := v.detailView.YOffset()
		v.detailView.SetContent(v.renderContactDetail())
		v.detailView.SetYOffset(offset)
	}
	if v.contactForm != nil {
		v.contactForm.styles = v.vc.styles
	}
	if v.noteForm != nil {
		v.noteForm.styles = v.vc.styles
	}
}

func (v *contactsView) requestContacts(page int) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, contactRequestList)
	return v.fetchContacts(ctx, requestID, max(page, 1))
}

func (v *contactsView) requestContactDetail(contactID int64) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, contactRequestDetail)
	return v.fetchContactDetail(ctx, requestID, contactID)
}

func (v *contactsView) startAddContact() tea.Cmd {
	v.contactForm = newContactForm(contactFormAdd, Contact{}, v.vc.styles)
	v.contactForm.resize(v.vc.width, v.vc.height)
	return v.contactForm.init()
}

func (v *contactsView) startEditContact() tea.Cmd {
	v.contactForm = newContactForm(contactFormEdit, v.detail, v.vc.styles)
	v.contactForm.resize(v.vc.width, v.vc.height)
	return v.contactForm.init()
}

func (v *contactsView) startNote() tea.Cmd {
	v.noteForm = newContactNoteForm(v.detail.ID, v.note, v.vc.styles)
	v.noteForm.resize(v.vc.width, v.vc.height)
	return v.noteForm.init()
}

func (v *contactsView) saveContact() tea.Cmd {
	form := v.contactForm
	name, email, aliases := form.values()
	requestID, ctx := v.requests.begin(v.vc.ctx, contactRequestMutation)
	return func() tea.Msg {
		params := hey.ContactParams{Name: name, EmailAddress: email, AliasEmailAddresses: aliases}
		var contact *generated.Contact
		var err error
		created := form.mode == contactFormAdd
		if created {
			contact, err = v.vc.sdk.Contacts().Create(ctx, params)
		} else {
			contact, err = v.vc.sdk.Contacts().Update(ctx, form.contactID, params)
		}
		if err == nil && contact == nil {
			err = fmt.Errorf("contact save returned no data")
		}
		if err != nil {
			return contactSavedMsg{requestResult: newRequestResult(requestID, err), originalID: form.contactID, created: created}
		}
		return contactSavedMsg{requestResult: newRequestResult(requestID, nil), originalID: form.contactID, contact: sdkContactToModel(*contact), created: created}
	}
}

func (v *contactsView) hideContact() tea.Cmd {
	contact := v.detail
	requestID, ctx := v.requests.begin(v.vc.ctx, contactRequestMutation)
	return func() tea.Msg {
		err := v.vc.sdk.Contacts().Hide(ctx, contact.ID)
		return contactHiddenMsg{requestResult: newRequestResult(requestID, err), contact: contact}
	}
}

func (v *contactsView) revealContact(contactID int64) tea.Cmd {
	requestID, ctx := v.requests.begin(v.vc.ctx, contactRequestMutation)
	return func() tea.Msg {
		contact, err := v.vc.sdk.Contacts().Reveal(ctx, contactID)
		if err == nil && contact == nil {
			err = fmt.Errorf("contact reveal returned no data")
		}
		if err != nil {
			return contactRevealedMsg{requestResult: newRequestResult(requestID, err)}
		}
		return contactRevealedMsg{requestResult: newRequestResult(requestID, nil), contact: sdkContactToModel(*contact)}
	}
}

func (v *contactsView) saveNote() tea.Cmd {
	form := v.noteForm
	content := strings.TrimSpace(form.input.Value())
	requestID, ctx := v.requests.begin(v.vc.ctx, contactRequestMutation)
	return func() tea.Msg {
		note, err := v.vc.sdk.Contacts().SetNote(ctx, form.contactID, content)
		if err == nil && note == nil {
			err = fmt.Errorf("contact note save returned no data")
		}
		if err != nil {
			return contactNoteSavedMsg{requestResult: newRequestResult(requestID, err)}
		}
		return contactNoteSavedMsg{requestResult: newRequestResult(requestID, nil), note: note.Note}
	}
}

func (v *contactsView) deleteNote() tea.Cmd {
	contactID := v.detail.ID
	requestID, ctx := v.requests.begin(v.vc.ctx, contactRequestMutation)
	return func() tea.Msg {
		err := v.vc.sdk.Contacts().DeleteNote(ctx, contactID)
		return contactNoteSavedMsg{requestResult: newRequestResult(requestID, err), deleted: true}
	}
}

func (v *contactsView) updateContactInList(contact Contact) {
	for i := range v.list.contacts {
		if v.list.contacts[i].ID == contact.ID {
			v.list.contacts[i] = contact
			return
		}
	}
	v.list.contacts = append([]Contact{contact}, v.list.contacts...)
}

func (v *contactsView) refreshDetailView() {
	v.detailView.SetContent(v.renderContactDetail())
	v.detailView.GotoTop()
}

func (v *contactsView) renderContactDetail() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", v.vc.styles.title.Render(v.detail.Name))
	fmt.Fprintf(&b, "%s\n", v.detail.EmailAddress)
	if len(v.detail.Aliases) > 0 {
		aliases := make([]string, 0, len(v.detail.Aliases))
		for _, alias := range v.detail.Aliases {
			aliases = append(aliases, alias.EmailAddress)
		}
		fmt.Fprintf(&b, "Aliases: %s\n", strings.Join(aliases, ", "))
	}
	fmt.Fprintf(&b, "Contact ID: %d\n", v.detail.ID)
	b.WriteString("\n")
	b.WriteString(v.vc.styles.entryFrom.Render("Private note"))
	b.WriteString("\n")
	if v.note == "" {
		b.WriteString(v.vc.styles.entryDate.Render("(empty)"))
	} else {
		b.WriteString(v.note)
	}
	b.WriteString("\n")
	return b.String()
}

func (v *contactsView) fetchContacts(ctx context.Context, requestID uint64, page int) tea.Cmd {
	return func() tea.Msg {
		pageValue := fmt.Sprintf("%d", page)
		params := &generated.ListContactsParams{Page: &pageValue}
		result, err := v.vc.sdk.Contacts().List(ctx, params)
		if err != nil {
			return contactsLoadedMsg{requestResult: newRequestResult(requestID, err), page: page}
		}
		var sdkContacts []generated.Contact
		if result != nil {
			sdkContacts = *result
		}
		contacts := make([]Contact, 0, len(sdkContacts))
		for _, contact := range sdkContacts {
			contacts = append(contacts, sdkContactToModel(contact))
		}
		return contactsLoadedMsg{requestResult: newRequestResult(requestID, nil), page: page, contacts: contacts}
	}
}

func (v *contactsView) fetchContactDetail(ctx context.Context, requestID uint64, contactID int64) tea.Cmd {
	return func() tea.Msg {
		var detail *generated.ContactDetail
		var note *generated.ContactNote
		group, groupCtx := errgroup.WithContext(ctx)
		group.Go(func() error {
			var err error
			detail, err = v.vc.sdk.Contacts().Get(groupCtx, contactID)
			return err
		})
		group.Go(func() error {
			var err error
			note, err = v.vc.sdk.Contacts().Note(groupCtx, contactID)
			return err
		})
		err := group.Wait()
		if err == nil && detail == nil {
			err = fmt.Errorf("contact %d returned no data", contactID)
		}
		if err != nil {
			return contactDetailLoadedMsg{requestResult: newRequestResult(requestID, err)}
		}
		content := ""
		if note != nil {
			content = note.Note
		}
		return contactDetailLoadedMsg{requestResult: newRequestResult(requestID, nil), contact: sdkContactDetailToModel(*detail), note: content}
	}
}

func contactSaveFailure(err error) string {
	var conflict *hey.ContactConflictError
	if errors.As(err, &conflict) {
		message := fmt.Sprintf("Contact %d was saved but conflicts with existing contact IDs", conflict.ContactID)
		for _, id := range conflict.ConflictingContactIDs {
			message += fmt.Sprintf(" %d", id)
		}
		return message
	}
	return errorNotice("Save failed", err)
}

func sdkContactToModel(contact generated.Contact) Contact {
	return Contact{
		ID:           contact.Id,
		Name:         contact.Name,
		EmailAddress: contact.EmailAddress,
	}
}

func sdkContactDetailToModel(contact generated.ContactDetail) Contact {
	result := Contact{
		ID:           contact.Id,
		Name:         contact.Name,
		EmailAddress: contact.EmailAddress,
		Aliases:      make([]Contact, 0, len(contact.Aliases)),
	}
	for _, alias := range contact.Aliases {
		result.Aliases = append(result.Aliases, sdkContactToModel(alias))
	}
	return result
}
