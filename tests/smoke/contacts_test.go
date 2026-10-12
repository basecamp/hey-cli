package smoke_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

	// Screened Out is terminal for this lifecycle: exercise every other contact
	// write before denying future mail from the disposable contact.
	for _, destination := range []string{"papertrail", "feed", "imbox", "screened-out"} {
		delivered := dataAs[struct {
			ID          int    `json:"id"`
			Destination string `json:"destination"`
		}](t, contactWriteJSON(t, "contact", "deliver", id, "--to", destination))
		if delivered.ID != created.ID || delivered.Destination != destination {
			t.Errorf("accepted contact delivery to %s was not returned", destination)
		}
	}
}

func TestContactPhoto(t *testing.T) {
	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	created := dataAs[struct {
		ID        int    `json:"id"`
		AvatarURL string `json:"avatar_url"`
	}](t, contactWriteJSON(t, "contact", "add", "--name", "Priya Shah", "--email", "priya.shah+"+unique+"@example.com"))
	id := intStr(created.ID)
	t.Cleanup(func() {
		_, _, _ = hey(t, "contact", "photo", "remove", id)
		_, _, _ = hey(t, "contact", "hide", id)
	})

	portrait := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			portrait.Set(x, y, color.RGBA{R: uint8(x * 4), G: 96, B: uint8(y * 4), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, portrait); err != nil {
		t.Fatal(err)
	}
	photo := filepath.Join(t.TempDir(), "priya-shah.png")
	if err := os.WriteFile(photo, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	withPhoto := dataAs[struct {
		ID        int    `json:"id"`
		AvatarURL string `json:"avatar_url"`
	}](t, contactWriteJSON(t, "contact", "photo", "set", id, photo))
	if withPhoto.ID != created.ID {
		t.Fatalf("photo set answered contact %d, want %d", withPhoto.ID, created.ID)
	}
	// The avatar URL carries a version of whatever the avatar is drawn from.
	if withPhoto.AvatarURL == "" || withPhoto.AvatarURL == created.AvatarURL {
		t.Errorf("avatar_url did not change after setting a photo: %q", withPhoto.AvatarURL)
	}
	// The URL changes with the blob whether or not HEY can resize it; one it cannot is
	// answered with the contact's initials as SVG. So fetch it: the photo is a raster.
	// The avatar route needs no session.
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(withPhoto.AvatarURL)
	if err != nil {
		t.Fatalf("fetch avatar: %v", err)
	}
	_ = resp.Body.Close()
	if contentType := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || !strings.HasPrefix(contentType, "image/") || strings.Contains(contentType, "svg") {
		t.Errorf("avatar answered %d %q, want the uploaded photo as a raster image", resp.StatusCode, contentType)
	}

	contactWriteJSON(t, "contact", "photo", "remove", id)
	withoutPhoto := dataAs[struct {
		AvatarURL string `json:"avatar_url"`
	}](t, heyJSON(t, "contact", "show", id))
	if withoutPhoto.AvatarURL != created.AvatarURL {
		t.Errorf("avatar_url after removing the photo = %q, want the default %q", withoutPhoto.AvatarURL, created.AvatarURL)
	}
	// Removing again, from a contact with no photo, still succeeds.
	contactWriteJSON(t, "contact", "photo", "remove", id)
}

func TestContactThreads(t *testing.T) {
	// An Imbox sender is a contact with at least one thread on record, so the
	// listing has something real to answer with.
	id := 0
	for _, row := range imboxRows(t) {
		if row.Creator.ID != 0 {
			id = row.Creator.ID
			break
		}
	}
	if id == 0 {
		skipf(t, "no imbox sender available to list threads for")
	}

	threads := dataAs[struct {
		ID       int               `json:"id"`
		Postings []json.RawMessage `json:"postings"`
	}](t, heyJSON(t, "contact", "threads", intStr(id)))
	if threads.ID != id {
		t.Errorf("contact threads returned id %d, want %d", threads.ID, id)
	}
	if len(threads.Postings) == 0 {
		t.Error("expected at least one thread with an imbox sender")
	}

	limited := dataAs[struct {
		Postings []json.RawMessage `json:"postings"`
	}](t, heyJSON(t, "contact", "threads", intStr(id), "--limit", "1"))
	if len(limited.Postings) > 1 {
		t.Errorf("expected at most 1 posting with --limit 1, got %d", len(limited.Postings))
	}
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
	heyFail(t, "contact", "threads", "not-an-id")
	heyFail(t, "contact", "deliver", "12345")
	heyFail(t, "contact", "deliver", "12345", "--to", "later")
	heyFail(t, "contact", "deliver", "not-an-id", "--to", "imbox")
	heyFail(t, "contact", "bundle", "not-an-id")
	heyFail(t, "contact", "unbundle", "0")
	heyFail(t, "contact", "photo", "set", "12345")
	heyFail(t, "contact", "photo", "set", "not-an-id", "priya-shah.png")
	heyFail(t, "contact", "photo", "set", "12345", filepath.Join(t.TempDir(), "missing.png"))
	heyFail(t, "contact", "photo", "remove", "not-an-id")
}
