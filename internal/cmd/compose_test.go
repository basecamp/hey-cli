package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

func TestParseAddresses(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "single address",
			input: "alice@example.com",
			want:  []string{"alice@example.com"},
		},
		{
			name:  "multiple addresses",
			input: "alice@example.com,bob@example.com",
			want:  []string{"alice@example.com", "bob@example.com"},
		},
		{
			name:  "addresses with whitespace",
			input: " alice@example.com , bob@example.com , carol@example.org ",
			want:  []string{"alice@example.com", "bob@example.com", "carol@example.org"},
		},
		{
			name:  "trailing comma",
			input: "alice@example.com,",
			want:  []string{"alice@example.com"},
		},
		{
			name:  "empty entries between commas",
			input: "alice@example.com,,bob@example.com",
			want:  []string{"alice@example.com", "bob@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAddresses(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("parseAddresses(%q) = %v (len %d), want %v (len %d)",
					tt.input, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseAddresses(%q)[%d] = %q, want %q",
						tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// A reply carries the thread's subject with it, so --subject is only wanted when
// starting a new thread. Requiring it either way made people pass one that HEY ignores.
func TestComposeSubjectRequiredOnlyForANewMessage(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)

	err := runCLI(t, server, "compose", "-m", "body")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("a new message with no subject should be a usage error, got %v", err)
	}

	// And the reply goes all the way out: --thread-id used to post a message to the
	// topic, and now answers the thread's last entry with that entry's recipients.
	if err := runCLI(t, server, "--account", "8", "compose", "--thread-id", "7", "-m", "the reply body"); err != nil {
		t.Fatalf("a reply should not need a subject, got %v", err)
	}
	if !strings.Contains(sent.Path, "/entries/12/replies") {
		t.Errorf("expected the reply to answer the last entry, went to %q", sent.Path)
	}
	if !strings.Contains(sent.Content, "the reply body") {
		t.Errorf("content = %q", sent.Content)
	}
	if sent.TopicAccountFilter != "" {
		t.Errorf("topic account filter = %q, want unscoped discovery", sent.TopicAccountFilter)
	}
	if sent.MessageAccountFilter != "9" {
		t.Errorf("message account filter = %q, want thread account 9", sent.MessageAccountFilter)
	}
	if sent.ActingSenderID != 42 {
		t.Errorf("acting sender = %d, want thread account sender 42", sent.ActingSenderID)
	}
	if len(sent.To) != 2 || sent.To[0] != "jane@example.com" || sent.To[1] != "rick@example.com" {
		t.Errorf("to = %v, want the entry's recipients and its sender", sent.To)
	}
	if len(sent.CC) != 1 || sent.CC[0] != "cc@example.com" {
		t.Errorf("cc = %v", sent.CC)
	}
}

func TestComposeSendsTheMessageAsMarkdown(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)

	err := runCLI(t, server, "--account", "8", "compose", "--thread-id", "7",
		"-m", "The plan:\n\n- **ship** it\n- announce it")
	if err != nil {
		t.Fatalf("compose failed: %v", err)
	}

	want := "<p>The plan:</p>\n<ul>\n<li><strong>ship</strong> it</li>\n<li>announce it</li>\n</ul>"
	if sent.Content != want {
		t.Errorf("content = %q, want %q", sent.Content, want)
	}
}

func TestComposeSendsRawHTMLVerbatim(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)

	err := runCLI(t, server, "--account", "8", "compose", "--thread-id", "7",
		"--message-html", "<h1>March</h1><p>What we shipped.</p>")
	if err != nil {
		t.Fatalf("compose failed: %v", err)
	}
	if want := "<h1>March</h1><p>What we shipped.</p>"; sent.Content != want {
		t.Errorf("content = %q, want %q", sent.Content, want)
	}
}

func TestComposeKeepsWhitespaceInRawHTML(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)
	body := "<pre>first<br>\n second</pre>"

	err := runCLI(t, server, "--account", "8", "compose", "--thread-id", "7", "--message-html", body)
	if err != nil {
		t.Fatalf("compose failed: %v", err)
	}
	if sent.Content != body {
		t.Errorf("content = %q, want raw HTML %q", sent.Content, body)
	}
}

func TestComposeRefusesMessageAndMessageHTMLTogether(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)

	err := runCLI(t, server, "compose", "--thread-id", "7",
		"-m", "Hello", "--message-html", "<p>Hello</p>")
	if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
		t.Fatalf("error = %v, want the flags refused as mutually exclusive", err)
	}
	if sent.Content != "" {
		t.Errorf("nothing should have been sent, got %q", sent.Content)
	}
}

// nameTaggedServer is draftLifecycleServer with a name tag on the default sender, which
// is where HEY serves it: the identity's senders carry name_tag as sanitized HTML.
func nameTaggedServer(t *testing.T, nameTag string, writes *[]draftWrite) http.Handler {
	t.Helper()
	inner := draftLifecycleServer(t, draftEditJSON, writes)
	tag, _ := json.Marshal(nameTag)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "identity") {
			inner.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":1,"senders":[{"id":42,"default":true,"name_tag":%s},{"id":43,"name_tag":"<div>Not this one</div>"}],"primary_contact":{"id":42}}`, tag)
	})
}

func sentContent(t *testing.T, writes []draftWrite) string {
	t.Helper()
	if len(writes) != 1 || writes[0].Path != "/messages.json" {
		t.Fatalf("writes = %+v, want one POST /messages.json", writes)
	}
	message, _ := writes[0].Body["message"].(map[string]any)
	content, _ := message["content"].(string)
	return content
}

// HEY puts the sender's name tag into the compose form, not onto the saved message, so a
// message written here has to end with it itself — on a draft and on a send alike.
func TestComposeEndsANewMessageWithTheSendersNameTag(t *testing.T) {
	const nameTag = "<div>Maria Delgado<br>Chief of Staff</div>"
	want := "<p>Numbers to follow.</p><br>" + nameTag

	var drafted []draftWrite
	if _, err := runJSONCommand(t, nameTaggedServer(t, nameTag, &drafted),
		"compose", "--subject", "Board update", "-m", "Numbers to follow.", "--draft"); err != nil {
		t.Fatalf("compose --draft: %v", err)
	}
	if got := sentContent(t, drafted); got != want {
		t.Errorf("draft content = %q, want %q", got, want)
	}

	var sent []draftWrite
	if _, err := runJSONCommand(t, nameTaggedServer(t, nameTag, &sent),
		"compose", "--to", "alice@example.com", "--subject", "Board update", "-m", "Numbers to follow."); err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got := sentContent(t, sent); got != want {
		t.Errorf("sent content = %q, want %q", got, want)
	}
}

func TestComposeNoNameTagLeavesTheMessageAlone(t *testing.T) {
	var writes []draftWrite
	if _, err := runJSONCommand(t, nameTaggedServer(t, "<div>Maria Delgado</div>", &writes),
		"compose", "--subject", "Board update", "-m", "Numbers to follow.", "--draft", "--no-name-tag"); err != nil {
		t.Fatalf("compose --draft --no-name-tag: %v", err)
	}
	if got, want := sentContent(t, writes), "<p>Numbers to follow.</p>"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

// A sender with no name tag configured has nothing to append: the message goes as written,
// without a stray break at the end.
func TestComposeWithoutANameTagSendsTheMessageAsWritten(t *testing.T) {
	var writes []draftWrite
	if _, err := runJSONCommand(t, nameTaggedServer(t, "", &writes),
		"compose", "--subject", "Board update", "-m", "Numbers to follow.", "--draft"); err != nil {
		t.Fatalf("compose --draft: %v", err)
	}
	if got, want := sentContent(t, writes), "<p>Numbers to follow.</p>"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}
