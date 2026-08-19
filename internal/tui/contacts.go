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

	"github.com/basecamp/hey-cli/internal/models"
)

type contactRequestKind int

const (
	contactRequestNone contactRequestKind = iota
	contactRequestList
	contactRequestDetail
	contactRequestMutation
)

type contactsLoadedMsg struct {
	requestID uint64
	page      int
	contacts  []models.Contact
	err       error
}

type contactDetailLoadedMsg struct {
	requestID uint64
	contact   models.Contact
	note      string
	err       error
}

type contactSavedMsg struct {
	requestID  uint64
	originalID int64
	contact    models.Contact
	created    bool
	err        error
}

type contactHiddenMsg struct {
	requestID uint64
	contact   models.Contact
	err       error
}

type contactRevealedMsg struct {
	requestID uint64
	contact   models.Contact
	err       error
}

type contactNoteSavedMsg struct {
	requestID uint64
	note      string
	deleted   bool
	err       error
}

type contactsView struct {
	vc *viewContext

	list              contactList
	loaded            bool
	page              int
	detail            models.Contact
	note              string
	inDetail          bool
	detailView        viewport.Model
	contactForm       *contactForm
	noteForm          *contactNoteForm
	lastHiddenID      int64
	confirmNoteDelete bool
	notice            string
	loading           bool

	activeRequestID   uint64
	activeRequestKind contactRequestKind
	requestCancel     context.CancelFunc
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
		if msg.requestID != v.activeRequestID {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
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
		if msg.requestID != v.activeRequestID {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		v.detail = msg.contact
		v.note = msg.note
		v.inDetail = true
		v.refreshDetailView()
		return nil, true

	case contactSavedMsg:
		if msg.requestID != v.activeRequestID {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			var conflict *hey.ContactConflictError
			if errors.As(msg.err, &conflict) && conflict.ContactID != 0 {
				v.contactForm = nil
				v.notice = contactSaveFailure(msg.err)
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
		if msg.requestID != v.activeRequestID {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		v.lastHiddenID = msg.contact.ID
		v.list.remove(msg.contact.ID)
		v.inDetail = false
		v.detail = models.Contact{}
		v.note = ""
		v.notice = "Contact hidden"
		return nil, true

	case contactRevealedMsg:
		if msg.requestID != v.activeRequestID {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			return func() tea.Msg { return errMsg{msg.err} }, true
		}
		v.lastHiddenID = 0
		v.notice = "Contact shown again"
		return v.requestContacts(v.page), true

	case contactNoteSavedMsg:
		if msg.requestID != v.activeRequestID {
			return nil, true
		}
		v.finishRequest(msg.requestID)
		if msg.err != nil {
			if v.noteForm != nil {
				v.noteForm.saving = false
				v.noteForm.status = "Save failed: " + msg.err.Error()
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
	if v.loading {
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
	if v.loading && v.activeRequestKind == contactRequestMutation {
		return
	}
	v.ExitThread()
}

func (v *contactsView) ExitThread() {
	v.inDetail = false
	v.contactForm = nil
	v.noteForm = nil
	v.detail = models.Contact{}
	v.note = ""
	v.confirmNoteDelete = false
	v.cancelRequest()
}

func (v *contactsView) CancelPendingDetail() bool {
	if v.activeRequestKind != contactRequestDetail {
		return false
	}
	v.cancelRequest()
	return true
}

func (v *contactsView) CapturingInput() bool {
	return v.contactForm != nil || v.noteForm != nil
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

func (v *contactsView) Loading() bool { return v.loading }

func (v *contactsView) beginRequest(kind contactRequestKind) (uint64, context.Context) {
	if v.requestCancel != nil {
		v.requestCancel()
	}
	v.activeRequestID++
	ctx, cancel := context.WithCancel(v.vc.ctx)
	v.activeRequestKind = kind
	v.requestCancel = cancel
	v.loading = true
	return v.activeRequestID, ctx
}

func (v *contactsView) finishRequest(requestID uint64) {
	if requestID != v.activeRequestID {
		return
	}
	if v.requestCancel != nil {
		v.requestCancel()
	}
	v.activeRequestKind = contactRequestNone
	v.requestCancel = nil
	v.loading = false
}

func (v *contactsView) cancelRequest() {
	if v.requestCancel != nil {
		v.requestCancel()
	}
	v.activeRequestID++
	v.activeRequestKind = contactRequestNone
	v.requestCancel = nil
	v.loading = false
}

func (v *contactsView) requestContacts(page int) tea.Cmd {
	requestID, ctx := v.beginRequest(contactRequestList)
	return v.fetchContacts(ctx, requestID, max(page, 1))
}

func (v *contactsView) requestContactDetail(contactID int64) tea.Cmd {
	requestID, ctx := v.beginRequest(contactRequestDetail)
	return v.fetchContactDetail(ctx, requestID, contactID)
}

func (v *contactsView) startAddContact() tea.Cmd {
	v.contactForm = newContactForm(contactFormAdd, models.Contact{}, v.vc.styles)
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
	requestID, ctx := v.beginRequest(contactRequestMutation)
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
		if err != nil {
			return contactSavedMsg{requestID: requestID, originalID: form.contactID, created: created, err: err}
		}
		if contact == nil {
			return contactSavedMsg{requestID: requestID, originalID: form.contactID, created: created, err: fmt.Errorf("contact save returned no data")}
		}
		return contactSavedMsg{requestID: requestID, originalID: form.contactID, contact: sdkContactToModel(*contact), created: created}
	}
}

func (v *contactsView) hideContact() tea.Cmd {
	contact := v.detail
	requestID, ctx := v.beginRequest(contactRequestMutation)
	return func() tea.Msg {
		err := v.vc.sdk.Contacts().Hide(ctx, contact.ID)
		return contactHiddenMsg{requestID: requestID, contact: contact, err: err}
	}
}

func (v *contactsView) revealContact(contactID int64) tea.Cmd {
	requestID, ctx := v.beginRequest(contactRequestMutation)
	return func() tea.Msg {
		contact, err := v.vc.sdk.Contacts().Reveal(ctx, contactID)
		if err != nil {
			return contactRevealedMsg{requestID: requestID, err: err}
		}
		if contact == nil {
			return contactRevealedMsg{requestID: requestID, err: fmt.Errorf("contact reveal returned no data")}
		}
		return contactRevealedMsg{requestID: requestID, contact: sdkContactToModel(*contact)}
	}
}

func (v *contactsView) saveNote() tea.Cmd {
	form := v.noteForm
	content := strings.TrimSpace(form.input.Value())
	requestID, ctx := v.beginRequest(contactRequestMutation)
	return func() tea.Msg {
		note, err := v.vc.sdk.Contacts().SetNote(ctx, form.contactID, content)
		if err != nil {
			return contactNoteSavedMsg{requestID: requestID, err: err}
		}
		if note == nil {
			return contactNoteSavedMsg{requestID: requestID, err: fmt.Errorf("contact note save returned no data")}
		}
		return contactNoteSavedMsg{requestID: requestID, note: note.Note}
	}
}

func (v *contactsView) deleteNote() tea.Cmd {
	contactID := v.detail.ID
	requestID, ctx := v.beginRequest(contactRequestMutation)
	return func() tea.Msg {
		err := v.vc.sdk.Contacts().DeleteNote(ctx, contactID)
		return contactNoteSavedMsg{requestID: requestID, deleted: true, err: err}
	}
}

func (v *contactsView) updateContactInList(contact models.Contact) {
	for i := range v.list.contacts {
		if v.list.contacts[i].ID == contact.ID {
			v.list.contacts[i] = contact
			return
		}
	}
	v.list.contacts = append([]models.Contact{contact}, v.list.contacts...)
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
			return contactsLoadedMsg{requestID: requestID, page: page, err: err}
		}
		var sdkContacts []generated.Contact
		if result != nil {
			sdkContacts = *result
		}
		contacts := make([]models.Contact, 0, len(sdkContacts))
		for _, contact := range sdkContacts {
			contacts = append(contacts, sdkContactToModel(contact))
		}
		return contactsLoadedMsg{requestID: requestID, page: page, contacts: contacts}
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
		if err := group.Wait(); err != nil {
			return contactDetailLoadedMsg{requestID: requestID, err: err}
		}
		if detail == nil {
			return contactDetailLoadedMsg{requestID: requestID, err: fmt.Errorf("contact %d returned no data", contactID)}
		}
		content := ""
		if note != nil {
			content = note.Note
		}
		return contactDetailLoadedMsg{requestID: requestID, contact: sdkContactDetailToModel(*detail), note: content}
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
	return "Save failed: " + err.Error()
}

func sdkContactToModel(contact generated.Contact) models.Contact {
	return models.Contact{
		ID:           contact.Id,
		AccountID:    contact.AccountId,
		Name:         contact.Name,
		EmailAddress: contact.EmailAddress,
		Avatar:       contact.AvatarUrl,
		UpdatedAt:    formatTimestamp(contact.UpdatedAt),
	}
}

func sdkContactDetailToModel(contact generated.ContactDetail) models.Contact {
	result := models.Contact{
		ID:           contact.Id,
		AccountID:    contact.AccountId,
		Name:         contact.Name,
		EmailAddress: contact.EmailAddress,
		Avatar:       contact.AvatarUrl,
		UpdatedAt:    formatTimestamp(contact.UpdatedAt),
		Aliases:      make([]models.Contact, 0, len(contact.Aliases)),
	}
	for _, alias := range contact.Aliases {
		result.Aliases = append(result.Aliases, sdkContactToModel(alias))
	}
	return result
}
