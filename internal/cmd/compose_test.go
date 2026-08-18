package cmd

import (
	"context"
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
	server := threadReplyServer(t, topicWithRecipients, topicEntries)
	withSDKPointedAt(t, server)

	newMessage := newComposeCommand()
	newMessage.cmd.SetContext(context.Background())
	newMessage.message = "body"
	err := newMessage.run(newMessage.cmd, nil)
	var cliErr *apierr.Error
	if !errors.As(err, &cliErr) || cliErr.Code != "usage" {
		t.Fatalf("a new message with no subject should be a usage error, got %v", err)
	}

	// The reply goes no further here — the stub server does not stand in for the whole
	// send — but it has to get past the subject check, which is what changed.
	reply := newComposeCommand()
	reply.cmd.SetContext(context.Background())
	reply.message = "body"
	reply.threadID = "7"
	err = reply.run(reply.cmd, nil)
	if errors.As(err, &cliErr) && strings.Contains(cliErr.Message, "--subject") {
		t.Errorf("a reply should not be asked for a subject, got %v", err)
	}
}
