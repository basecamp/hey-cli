package smoke_test

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

type smokeSentRecipient struct {
	Name         string `json:"name"`
	EmailAddress string `json:"email_address"`
}

type smokeSentMessage struct {
	ID      int64                `json:"id"`
	Subject string               `json:"subject"`
	To      []smokeSentRecipient `json:"to"`
	CC      []smokeSentRecipient `json:"cc"`
	BCC     []smokeSentRecipient `json:"bcc"`
	SentAt  *time.Time           `json:"sent_at"`
	AppURL  string               `json:"app_url"`
}

func TestSentListing(t *testing.T) {
	resp := heyJSON(t, "sent", "--all")
	messages := dataAs[[]smokeSentMessage](t, resp)
	for _, message := range messages {
		if message.ID <= 0 {
			t.Errorf("sent thread id = %d, want positive", message.ID)
		}
		if strings.TrimSpace(message.Subject) == "" {
			t.Errorf("sent thread %d has no subject", message.ID)
		}
		if message.SentAt != nil && message.SentAt.IsZero() {
			t.Errorf("sent thread %d has an invalid sent_at", message.ID)
		}
		if strings.TrimSpace(message.AppURL) == "" {
			t.Errorf("sent thread %d has no app_url", message.ID)
		}
		if message.To == nil || message.CC == nil || message.BCC == nil {
			t.Errorf("sent thread %d recipient arrays = to:%v cc:%v bcc:%v, want arrays even when empty", message.ID, message.To, message.CC, message.BCC)
		}
	}

	ids := strings.Fields(heyOK(t, "sent", "--ids-only"))
	countText := strings.TrimSpace(heyOK(t, "sent", "--count"))
	count, err := strconv.Atoi(countText)
	if err != nil {
		t.Fatalf("sent --count = %q: %v", countText, err)
	}
	if len(ids) != count {
		t.Errorf("sent --ids-only returned %d ids, --count returned %d", len(ids), count)
	}
}
