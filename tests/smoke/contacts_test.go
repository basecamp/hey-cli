package smoke_test

import (
	"fmt"
	"testing"
	"time"
)

type smokeContact struct {
	ID           int            `json:"id"`
	Name         string         `json:"name"`
	EmailAddress string         `json:"email_address"`
	Aliases      []smokeContact `json:"aliases"`
	Note         string         `json:"note"`
}

func TestContactsListAndShow(t *testing.T) {
	contacts := dataAs[[]smokeContact](t, heyJSON(t, "contacts", "list"))
	if len(contacts) == 0 {
		t.Skip("no contacts available for read-only detail validation")
	}
	contact := dataAs[smokeContact](t, heyJSON(t, "contacts", "show", intStr(contacts[0].ID)))
	if contact.ID != contacts[0].ID || contact.EmailAddress == "" {
		t.Errorf("contact detail is incomplete: id=%d email_present=%v", contact.ID, contact.EmailAddress != "")
	}
}

func TestContactLifecycleAndPrivateNote(t *testing.T) {
	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	email := "sam.rivera+" + unique + "@example.com"
	alias := "samuel.rivera+" + unique + "@example.org"

	created := dataAs[smokeContact](t, heyJSON(t, "contacts", "add", "--name", "Sam Rivera", "--email", email))
	if created.ID == 0 || created.EmailAddress != email {
		t.Fatalf("created contact is incomplete: id=%d email_matches=%v", created.ID, created.EmailAddress == email)
	}
	id := intStr(created.ID)
	t.Cleanup(func() { _, _, _ = hey(t, "contacts", "hide", id) })

	updated := dataAs[smokeContact](t, heyJSON(t, "contacts", "update", id, "--name", "Samuel Rivera", "--alias", alias))
	if updated.ID == 0 || updated.Name != "Samuel Rivera" {
		t.Errorf("contact update was not returned")
	}
	detail := dataAs[smokeContact](t, heyJSON(t, "contacts", "show", id))
	if len(detail.Aliases) != 1 || detail.Aliases[0].EmailAddress != alias {
		t.Errorf("contact aliases were not updated")
	}

	noteText := "Prefers email follow-ups for project planning."
	note := dataAs[struct {
		Note string `json:"note"`
	}](t, heyJSON(t, "contacts", "note", "set", id, noteText))
	if note.Note != noteText {
		t.Error("private note was not saved")
	}
	shownNote := dataAs[struct {
		Note string `json:"note"`
	}](t, heyJSON(t, "contacts", "note", "show", id))
	if shownNote.Note != noteText {
		t.Error("private note read did not match the saved note")
	}
	heyJSON(t, "contacts", "note", "delete", id)
	deletedNote := dataAs[struct {
		Note string `json:"note"`
	}](t, heyJSON(t, "contacts", "note", "show", id))
	if deletedNote.Note != "" {
		t.Error("private note was not deleted")
	}

	heyJSON(t, "contacts", "hide", id)
	shownAgain := dataAs[smokeContact](t, heyJSON(t, "contacts", "show-again", id))
	if shownAgain.ID != created.ID {
		t.Error("hidden contact was not shown again")
	}
	heyJSON(t, "contacts", "hide", id)
}

func TestContactCommandsValidateInput(t *testing.T) {
	heyFail(t, "contacts", "add", "--name", "Sam Rivera")
	heyFail(t, "contacts", "update", "12345")
	heyFail(t, "contacts", "show", "not-an-id")
}
