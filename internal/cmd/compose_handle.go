package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/basecamp/hey-cli/internal/mail"
)

// composeHandle names what a send created.
//
// Without one, `hey compose` can report that HEY accepted a request and nothing more,
// and "the server said 2xx" is not evidence that a particular message exists. Anything
// that has to prove what it sent — an automated sender, an audit, a retry that must not
// deliver twice — needs an identifier it can read back, and this endpoint carries no
// idempotency key to fall back on.
type composeHandle struct {
	MessageID int64
	TopicID   int64
	AppURL    string
}

// usable reports whether the handle names something that can be read back. A message id
// is what the readback wants; a topic id alone still names the thread the message landed
// in, which is enough for a caller to go and find it.
func (h composeHandle) usable() bool {
	return h.MessageID != 0 || h.TopicID != 0
}

// composeResponseBody is every field of a send's response this program will read. It is
// a closed list on purpose: the failure mode of a lenient parser is reporting a message
// as sent when nothing can show which one it was, which is worse than saying we do not
// know, because a caller that is told "sent" stops looking. A body that does not
// unmarshal into this shape — HTML, an array, an id served as a string — is left to the
// Location header rather than guessed at.
type composeResponseBody struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	TopicID   int64  `json:"topic_id"`
	AppURL    string `json:"app_url"`
	Message   *struct {
		ID      int64  `json:"id"`
		TopicID int64  `json:"topic_id"`
		AppURL  string `json:"app_url"`
	} `json:"message"`
}

// handleFromResponse mines a send's response for a handle, from the two places HEY is
// known to put one:
//
//  1. the Location header, which is how a saved draft answers already
//     (204 No Content, Location: …/messages/{entry_id}) — see the SDK's
//     draftEntryIDFromLocation, which the same controllers serve;
//  2. a JSON body naming the message (`id`, `message_id`, `message.id`) or the thread
//     (`topic_id`, `app_url`).
//
// Both are consulted whichever answered first: a mutation may serve an empty body and a
// header, and a body that named only the thread is improved by a header that names the
// message.
//
// A response that names neither is an error. The request was accepted, so a message may
// well exist — that is exactly why this refuses rather than reporting a plain success.
func handleFromResponse(status int, headers http.Header, body []byte) (composeHandle, error) {
	if status < 200 || status > 299 {
		return composeHandle{}, fmt.Errorf("the send answered HTTP %d", status)
	}

	var handle composeHandle
	mergeResponseBody(&handle, body)
	mergeLocation(&handle, headers)

	if !handle.usable() {
		return composeHandle{}, fmt.Errorf(
			"HTTP %d named no message id, no thread id and no Location", status)
	}
	return handle, nil
}

// mergeResponseBody fills in whatever the body names, and stays silent otherwise: the
// Location header may still carry the handle, and handleFromResponse decides whether
// what was collected between them is enough.
func mergeResponseBody(handle *composeHandle, body []byte) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return
	}
	var parsed composeResponseBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return
	}

	// The endpoint creates a message, so a bare `id` is that message's.
	setID(&handle.MessageID, parsed.ID, parsed.MessageID)
	setID(&handle.TopicID, parsed.TopicID)
	if parsed.Message != nil {
		setID(&handle.MessageID, parsed.Message.ID)
		setID(&handle.TopicID, parsed.Message.TopicID)
		if handle.AppURL == "" {
			handle.AppURL = parsed.Message.AppURL
		}
	}
	if handle.AppURL == "" {
		handle.AppURL = parsed.AppURL
	}
	setID(&handle.TopicID, mail.TopicIDIn(handle.AppURL))
}

// mergeLocation reads the ids out of the Location header's path. The header is HEY's
// own, and only its path is read, so there is no origin to get wrong here: a URL that
// names neither a message nor a topic simply contributes nothing.
func mergeLocation(handle *composeHandle, headers http.Header) {
	location := headers.Get("Location")
	if location == "" {
		return
	}
	setID(&handle.MessageID, idAfter(location, "/messages/"))
	setID(&handle.TopicID, mail.TopicIDIn(location))
}

// setID keeps the first positive id it is offered, so a body that already named the
// message is not overwritten by a header that names it again.
func setID(target *int64, candidates ...int64) {
	if *target > 0 {
		return
	}
	for _, candidate := range candidates {
		if candidate > 0 {
			*target = candidate
			return
		}
	}
}

// idAfter reads the number following the last occurrence of segment in a URL path,
// stopping where the segment does. It answers zero for anything that is not a number,
// which is what keeps a `…/messages/new` out of a handle.
func idAfter(rawURL, segment string) int64 {
	marker := strings.LastIndex(rawURL, segment)
	if marker < 0 {
		return 0
	}
	rest := rawURL[marker+len(segment):]
	if end := strings.IndexAny(rest, "/?#."); end >= 0 {
		rest = rest[:end]
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}
