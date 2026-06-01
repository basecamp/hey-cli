package cmd

import "testing"

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
