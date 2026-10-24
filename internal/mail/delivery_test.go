package mail

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// HEY refuses a delivery by keeping the message as a draft and redirecting to it, so the
// answer is the draft — an id and no thread. That is a refusal only once the draft's edit
// page says it is still a draft; anything else is taken as sent.
func TestConfirmDelivered(t *testing.T) {
	const draft = `{"id":2201,"subject":"Board update","content":"<div>Numbers to follow.</div>"}`
	for _, tt := range []struct {
		name    string
		sent    *generated.SentMessage
		edit    int // the edit page's status; 0 means it must not be asked
		refused bool
	}{
		{"a thread named", &generated.SentMessage{Id: 2201, TopicId: 880}, 0, false},
		{"no id, from a HEY that predates them", &generated.SentMessage{}, 0, false},
		{"no answer", nil, 0, false},
		{"an id, no thread, still a draft", &generated.SentMessage{Id: 2201}, http.StatusOK, true},
		{"an id, no thread, already sent", &generated.SentMessage{Id: 2201}, http.StatusUnprocessableEntity, false},
		{"an id, no thread, the check failing", &generated.SentMessage{Id: 2201}, http.StatusInternalServerError, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			asked := false
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/messages/2201/edit.json" || tt.edit == 0 {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				asked = true
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.edit)
				if tt.edit == http.StatusOK {
					_, _ = io.WriteString(w, draft)
				}
			})
			err := ConfirmDelivered(context.Background(), client, tt.sent)
			var refused *NotDeliveredError
			if got := errors.As(err, &refused); got != tt.refused {
				t.Fatalf("refused = %v (%v), want %v", got, err, tt.refused)
			}
			if tt.refused && refused.DraftID != 2201 {
				t.Errorf("draft = %d, want 2201", refused.DraftID)
			}
			if !tt.refused && err != nil {
				t.Errorf("err = %v, want the send taken as delivered", err)
			}
			if asked != (tt.edit != 0) {
				t.Errorf("asked the edit page = %v, want %v", asked, tt.edit != 0)
			}
		})
	}
}
