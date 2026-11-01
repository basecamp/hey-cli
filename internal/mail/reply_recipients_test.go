package mail

import (
	"reflect"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

func contacts(addresses ...string) []generated.Contact {
	list := make([]generated.Contact, 0, len(addresses))
	for _, address := range addresses {
		list = append(list, generated.Contact{EmailAddress: address})
	}
	return list
}

func TestReplyRecipientsForBlindCopies(t *testing.T) {
	sender := generated.Contact{EmailAddress: "jane@example.com"}
	tests := []struct {
		name      string
		addressed generated.Addressed
		want      ReplyRecipients
	}{
		{
			name:      "blind copies stay off a reply with a visible audience",
			addressed: generated.Addressed{Directly: contacts("rick@example.com"), Blindcopied: contacts("secret@example.com")},
			want:      ReplyRecipients{To: []string{"rick@example.com", "jane@example.com"}},
		},
		{
			name:      "blind copies are kept when they are the whole audience",
			addressed: generated.Addressed{Directly: contacts("jane@example.com"), Blindcopied: contacts("list@example.com")},
			want:      ReplyRecipients{To: []string{"jane@example.com"}, BCC: []string{"list@example.com"}},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := ReplyRecipientsFor(generated.Message{Sender: sender, Addressed: testCase.addressed})
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("got %+v, want %+v", got, testCase.want)
			}
		})
	}
}
