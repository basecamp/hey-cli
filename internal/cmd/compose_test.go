package cmd

import (
	"errors"
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
	server, sent := threadReplyServer(t, replyForm)

	err := runCLI(t, server, "compose", "-m", "body")
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("a new message with no subject should be a usage error, got %v", err)
	}

	// And the reply goes all the way out: --thread-id used to post a message to the
	// topic, and now answers the thread's last entry with that entry's recipients.
	if err := runCLI(t, server, "compose", "--thread-id", "7", "-m", "the reply body"); err != nil {
		t.Fatalf("a reply should not need a subject, got %v", err)
	}
	if !strings.Contains(sent.Path, "/entries/12/replies") {
		t.Errorf("expected the reply to answer the last entry, went to %q", sent.Path)
	}
	if !strings.Contains(sent.Content, "the reply body") {
		t.Errorf("content = %q", sent.Content)
	}
	if len(sent.To) != 1 || sent.To[0] != "jane@example.com" {
		t.Errorf("to = %v, want the thread's recipients", sent.To)
	}
	if len(sent.CC) != 1 || sent.CC[0] != "cc@example.com" {
		t.Errorf("cc = %v", sent.CC)
	}
}
