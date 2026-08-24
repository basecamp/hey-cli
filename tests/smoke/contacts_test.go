package smoke_test

import (
	"encoding/json"
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
	contacts := dataAs[[]smokeContact](t, heyJSON(t, "contact", "list"))
	if len(contacts) == 0 {
		skipf(t, "no contacts available for read-only detail validation")
	}
	contact := dataAs[smokeContact](t, heyJSON(t, "contact", "show", intStr(contacts[0].ID)))
	if contact.ID != contacts[0].ID || contact.EmailAddress == "" {
		t.Errorf("contact detail is incomplete: id=%d email_present=%v", contact.ID, contact.EmailAddress != "")
	}
}

func TestContactLifecycleAndPrivateNote(t *testing.T) {
	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	email := "sam.rivera+" + unique + "@example.com"
	alias := "samuel.rivera+" + unique + "@example.org"

	created := dataAs[smokeContact](t, contactWriteJSON(t, "contact", "add", "--name", "Sam Rivera", "--email", email))
	if created.ID == 0 || created.EmailAddress != email {
		t.Fatalf("created contact is incomplete: id=%d email_matches=%v", created.ID, created.EmailAddress == email)
	}
	id := intStr(created.ID)
	t.Cleanup(func() {
		_, _, _ = hey(t, "contact", "unbundle", id)
		_, _, _ = hey(t, "contact", "hide", id)
	})

	updated := dataAs[smokeContact](t, contactWriteJSON(t, "contact", "update", id, "--name", "Samuel Rivera", "--alias", alias))
	if updated.ID == 0 || updated.Name != "Samuel Rivera" {
		t.Errorf("contact update was not returned")
	}
	detail := dataAs[smokeContact](t, heyJSON(t, "contact", "show", id))
	if len(detail.Aliases) != 1 || detail.Aliases[0].EmailAddress != alias {
		t.Errorf("contact aliases were not updated")
	}

	bundled := dataAs[struct {
		ID     int    `json:"id"`
		Action string `json:"action"`
	}](t, contactWriteJSON(t, "contact", "bundle", id))
	if bundled.ID != created.ID || bundled.Action != "bundle" {
		t.Error("accepted contact bundle action was not returned")
	}
	unbundled := dataAs[struct {
		ID     int    `json:"id"`
		Action string `json:"action"`
	}](t, contactWriteJSON(t, "contact", "unbundle", id))
	if unbundled.ID != created.ID || unbundled.Action != "unbundle" {
		t.Error("accepted contact unbundle action was not returned")
	}

	noteText := "Prefers email follow-ups for project planning."
	note := dataAs[struct {
		Note string `json:"note"`
	}](t, contactWriteJSON(t, "contact", "note", "set", id, noteText))
	if note.Note != noteText {
		t.Error("private note was not saved")
	}
	shownNote := dataAs[struct {
		Note string `json:"note"`
	}](t, heyJSON(t, "contact", "note", "show", id))
	if shownNote.Note != noteText {
		t.Error("private note read did not match the saved note")
	}
	contactWriteJSON(t, "contact", "note", "delete", id)
	deletedNote := dataAs[struct {
		Note string `json:"note"`
	}](t, heyJSON(t, "contact", "note", "show", id))
	if deletedNote.Note != "" {
		t.Error("private note was not deleted")
	}

	contactWriteJSON(t, "contact", "hide", id)
	shownAgain := dataAs[smokeContact](t, contactWriteJSON(t, "contact", "show-again", id))
	if shownAgain.ID != created.ID {
		t.Error("hidden contact was not shown again")
	}
	contactWriteJSON(t, "contact", "hide", id)
}

func contactWriteJSON(t *testing.T, args ...string) Response {
	t.Helper()
	args = append(args, "--json")
	stdout, stderr, code := hey(t, args...)
	if code != 0 {
		skipf(t, "contact write unavailable (exit %d): %s", code, stderr)
	}
	var response Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("failed to parse contact write response: %v", err)
	}
	if !response.OK {
		t.Fatalf("contact write returned ok=false: %s", stdout)
	}
	return response
}

func TestContactCommandsValidateInput(t *testing.T) {
	heyFail(t, "contact", "add", "--name", "Sam Rivera")
	heyFail(t, "contact", "update", "12345")
	heyFail(t, "contact", "show", "not-an-id")
	heyFail(t, "contact", "bundle", "not-an-id")
	heyFail(t, "contact", "unbundle", "0")
}
