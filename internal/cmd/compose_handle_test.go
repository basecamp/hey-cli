package cmd

import (
	"net/http"
	"testing"
)

// handleFromResponse only accepts shapes HEY is known to answer with. Every accepted
// shape has a case here; everything else is an error rather than a guess, because a
// lenient parser's failure mode is reporting a message as sent with nothing that can
// show which one it was.
func TestHandleFromResponseReadsTheShapesHEYAnswersWith(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		headers   http.Header
		body      string
		wantMsg   int64
		wantTopic int64
		wantURL   string
	}{
		{
			name:    "a Location naming the entry, which is how a draft save answers",
			status:  http.StatusNoContent,
			headers: locationHeader("https://app.hey.com/messages/9101"),
			body:    "null",
			wantMsg: 9101,
		},
		{
			name:    "a relative Location",
			status:  http.StatusCreated,
			headers: locationHeader("/messages/9101"),
			wantMsg: 9101,
		},
		{
			name:      "a Location naming the thread",
			status:    http.StatusCreated,
			headers:   locationHeader("https://app.hey.com/topics/7742"),
			wantTopic: 7742,
		},
		{
			name:      "a Location naming both",
			status:    http.StatusCreated,
			headers:   locationHeader("https://app.hey.com/topics/7742/messages/9101"),
			wantMsg:   9101,
			wantTopic: 7742,
		},
		{
			name:    "a body that is the created message",
			status:  http.StatusCreated,
			body:    `{"id": 9101, "subject": "Inovo Customer Update"}`,
			wantMsg: 9101,
		},
		{
			name:    "a body naming the message explicitly",
			status:  http.StatusCreated,
			body:    `{"message_id": 9101}`,
			wantMsg: 9101,
		},
		{
			name:      "a body wrapping the message",
			status:    http.StatusCreated,
			body:      `{"message": {"id": 9101, "topic_id": 7742}}`,
			wantMsg:   9101,
			wantTopic: 7742,
		},
		{
			name:      "a body naming only the thread",
			status:    http.StatusOK,
			body:      `{"topic_id": 7742, "app_url": "https://app.hey.com/topics/7742"}`,
			wantTopic: 7742,
			wantURL:   "https://app.hey.com/topics/7742",
		},
		{
			name:      "an app_url that names the thread the id field did not",
			status:    http.StatusOK,
			body:      `{"id": 9101, "app_url": "https://app.hey.com/topics/7742"}`,
			wantMsg:   9101,
			wantTopic: 7742,
			wantURL:   "https://app.hey.com/topics/7742",
		},
		{
			name:    "the header improves a body that named only the thread",
			status:  http.StatusCreated,
			headers: locationHeader("https://app.hey.com/messages/9101"),
			body:    `{"topic_id": 7742}`,
			wantMsg: 9101, wantTopic: 7742,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle, err := handleFromResponse(tt.status, tt.headers, []byte(tt.body))
			if err != nil {
				t.Fatalf("handleFromResponse: %v", err)
			}
			if handle.MessageID != tt.wantMsg {
				t.Errorf("message id = %d, want %d", handle.MessageID, tt.wantMsg)
			}
			if handle.TopicID != tt.wantTopic {
				t.Errorf("topic id = %d, want %d", handle.TopicID, tt.wantTopic)
			}
			if handle.AppURL != tt.wantURL {
				t.Errorf("app url = %q, want %q", handle.AppURL, tt.wantURL)
			}
		})
	}
}

// Anything that does not name a message or a thread is refused. The request may well
// have been accepted — that is exactly why it is refused rather than reported as a
// plain success: a caller must not read "sent" off a response nothing can be read back
// from.
func TestHandleFromResponseRefusesWhatItCannotReadBack(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		headers http.Header
		body    string
	}{
		{name: "no body and no header", status: http.StatusNoContent, body: ""},
		{name: "the null the SDK maps 204 to", status: http.StatusNoContent, body: "null"},
		{name: "an empty object", status: http.StatusCreated, body: "{}"},
		{name: "a shape nothing here knows", status: http.StatusCreated, body: `{"status": "ok"}`},
		{name: "an array", status: http.StatusCreated, body: `[{"id": 9101}]`},
		{name: "HTML", status: http.StatusOK, body: `<html><body>Sent</body></html>`},
		{name: "a zero id", status: http.StatusCreated, body: `{"id": 0}`},
		{name: "a negative id", status: http.StatusCreated, body: `{"id": -3}`},
		{name: "an id that is not a number", status: http.StatusCreated, body: `{"id": "9101"}`},
		{
			name:    "a Location naming neither",
			status:  http.StatusCreated,
			headers: locationHeader("https://app.hey.com/imbox"),
		},
		{
			name:    "a Location whose id is not a number",
			status:  http.StatusCreated,
			headers: locationHeader("https://app.hey.com/messages/new"),
		},
		{
			name:    "a status outside 2xx, header or not",
			status:  http.StatusInternalServerError,
			headers: locationHeader("https://app.hey.com/messages/9101"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := handleFromResponse(tt.status, tt.headers, []byte(tt.body)); err == nil {
				t.Fatal("want an error, got a handle")
			}
		})
	}
}

func locationHeader(location string) http.Header {
	return http.Header{"Location": []string{location}}
}
