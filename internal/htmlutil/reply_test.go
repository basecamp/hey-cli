package htmlutil

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseReplyFormHTML(t *testing.T) {
	page := `
<form action="/search"><input name="message[subject]" value="Wrong form"></form>
<form action="/entries/67890/replies" method="post">
  <select name="entry[addressed][directly][]" multiple>
    <option value="wrong@example.com">Wrong</option>
    <option value="alice@example.com" selected>Alice</option>
  </select>
  <select name="entry[addressed][copied][]" multiple>
    <option value="carol@example.com">Carol</option>
  </select>
  <select name="entry[addressed][blindcopied][]" multiple></select>
</form>`

	form, err := ParseReplyFormHTML(page)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(form.To, []string{"alice@example.com"}) {
		t.Errorf("to = %#v", form.To)
	}
	if !reflect.DeepEqual(form.CC, []string{"carol@example.com"}) {
		t.Errorf("cc = %#v", form.CC)
	}
	if len(form.BCC) != 0 {
		t.Errorf("bcc = %#v", form.BCC)
	}
}

func TestParseReplyFormHTMLSupportsAbsoluteActionAndHiddenRecipients(t *testing.T) {
	page := `<form action="https://app.hey.com/entries/67890/replies">
  <input type="hidden" name="entry[addressed][directly][]" value="alice@example.com">
  <input type="hidden" name="entry[addressed][directly][]" value="bob@example.org">
</form>`

	form, err := ParseReplyFormHTML(page)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(form.To, []string{"alice@example.com", "bob@example.org"}) {
		t.Errorf("to = %#v", form.To)
	}
}

func TestParseReplyFormHTMLFallsBackToHiddenRecipientsWhenSelectIsEmpty(t *testing.T) {
	page := `<form action="/entries/67890/replies">
  <select name="entry[addressed][directly][]" multiple></select>
  <input type="hidden" name="entry[addressed][directly][]" value="alice@example.com">
</form>`

	form, err := ParseReplyFormHTML(page)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(form.To, []string{"alice@example.com"}) {
		t.Errorf("to = %#v", form.To)
	}
}

func TestParseReplyFormHTMLRejectsMissingReplyForm(t *testing.T) {
	_, err := ParseReplyFormHTML(`<form action="/messages"></form>`)
	if err == nil || !strings.Contains(err.Error(), "entry reply form") {
		t.Fatalf("error = %v, want missing form error", err)
	}
}

func TestParseReplyFormHTMLRejectsMissingRecipients(t *testing.T) {
	_, err := ParseReplyFormHTML(`<form action="/entries/67890/replies"></form>`)
	if err == nil || !strings.Contains(err.Error(), "recipients") {
		t.Fatalf("error = %v, want missing recipients error", err)
	}
}
