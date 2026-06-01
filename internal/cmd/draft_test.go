package cmd

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

func TestDraftValues(t *testing.T) {
	values := draftValues(123, draftFormRequest{
		Subject: "Hello",
		Content: "<div>Hi there</div>",
		To:      []string{"alice@example.com", "bob@example.org"},
		CC:      []string{"carol@example.com"},
		BCC:     []string{"dave@example.org"},
	})

	if got := values.Get("acting_sender_id"); got != "123" {
		t.Fatalf("acting_sender_id = %q, want 123", got)
	}
	if got := values.Get("entry[status]"); got != "drafted" {
		t.Fatalf("entry status = %q, want drafted", got)
	}
	if got := values.Get("message[subject]"); got != "Hello" {
		t.Fatalf("subject = %q, want Hello", got)
	}
	if got := values.Get("message[content]"); got != "<div>Hi there</div>" {
		t.Fatalf("content = %q", got)
	}

	to := values["entry[addressed][directly][]"]
	if len(to) != 2 || to[0] != "alice@example.com" || to[1] != "bob@example.org" {
		t.Fatalf("to = %#v", to)
	}
}

func TestDraftResponseFromLocation(t *testing.T) {
	resp := draftResponseFromLocation("https://app.hey.com/messages/2159062391")

	if resp.ID != 2159062391 {
		t.Fatalf("ID = %d, want 2159062391", resp.ID)
	}
	if resp.EditURL != "https://app.hey.com/messages/2159062391/edit" {
		t.Fatalf("EditURL = %q", resp.EditURL)
	}
}

func TestParseMessageSubject(t *testing.T) {
	html := `<input class="input" value="Re: Research &amp; Planning" name="message[subject]" id="message_subject" />`

	if got := parseMessageSubject(html); got != "Re: Research & Planning" {
		t.Fatalf("subject = %q", got)
	}
}

func TestParseDraftForm(t *testing.T) {
	html := `
<meta name="csrf-token" content="csrf-123" />
<select name="entry[addressed][directly][]" hidden multiple>
  <option value="alice@example.com" selected>Alice</option>
  <option value="bob@example.com">Bob</option>
</select>
<select name="entry[addressed][copied][]" hidden multiple>
  <option value="carol@example.com" selected>Carol</option>
</select>
<select name="entry[addressed][blindcopied][]" hidden multiple>
  <option value="dave@example.com" selected>Dave</option>
</select>
<input value="Hello &amp; welcome" name="message[subject]" />
<input type="hidden" name="message[content]" value="Body &amp; more" />`

	state := parseDraftForm(html)

	if state.CSRFToken != "csrf-123" {
		t.Fatalf("csrf = %q", state.CSRFToken)
	}
	if !state.HasSubject || !state.HasContent || !state.HasTo || !state.HasCC || !state.HasBCC {
		t.Fatalf("field presence = subject:%t content:%t to:%t cc:%t bcc:%t", state.HasSubject, state.HasContent, state.HasTo, state.HasCC, state.HasBCC)
	}
	if state.Request.Subject != "Hello & welcome" {
		t.Fatalf("subject = %q", state.Request.Subject)
	}
	if state.Request.Content != "Body & more" {
		t.Fatalf("content = %q", state.Request.Content)
	}
	if !reflect.DeepEqual(state.Request.To, []string{"alice@example.com"}) {
		t.Fatalf("to = %#v", state.Request.To)
	}
	if !reflect.DeepEqual(state.Request.CC, []string{"carol@example.com"}) {
		t.Fatalf("cc = %#v", state.Request.CC)
	}
	if !reflect.DeepEqual(state.Request.BCC, []string{"dave@example.com"}) {
		t.Fatalf("bcc = %#v", state.Request.BCC)
	}
}

func TestParseDraftFormMissingFields(t *testing.T) {
	state := parseDraftForm(`<meta name="csrf-token" content="csrf-123" />`)

	if state.CSRFToken != "csrf-123" {
		t.Fatalf("csrf = %q", state.CSRFToken)
	}
	if state.HasSubject || state.HasContent || state.HasTo || state.HasCC || state.HasBCC {
		t.Fatalf("unexpected field presence = subject:%t content:%t to:%t cc:%t bcc:%t", state.HasSubject, state.HasContent, state.HasTo, state.HasCC, state.HasBCC)
	}
}

func TestParseSelectedAddressesFieldRejectsSelectedOptionWithoutValue(t *testing.T) {
	html := `
<select name="entry[addressed][directly][]" hidden multiple>
  <option selected>Alice</option>
</select>`

	addresses, ok := parseSelectedAddressesField(html, "entry[addressed][directly][]")

	if ok {
		t.Fatalf("expected parser to reject selected option without value, got %#v", addresses)
	}
}

func TestDraftUpdateWithoutFieldsFailsBeforeAuth(t *testing.T) {
	cmd := newDraftUpdateCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"123"})

	err := cmd.Execute()

	var usageErr *output.Error
	if !errors.As(err, &usageErr) {
		t.Fatalf("error = %T, want output.Error", err)
	}
	if usageErr.Code != "usage" {
		t.Fatalf("code = %q, want usage", usageErr.Code)
	}
	if usageErr.Message != "No update fields specified" {
		t.Fatalf("message = %q", usageErr.Message)
	}
}
