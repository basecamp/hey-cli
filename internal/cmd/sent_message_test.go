package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

// What HEY answers for a delivered message (entries/_sent.jbuilder), once it names the
// entry it delivered and before.
const (
	heySentNow = `{"id":2201,"topic_id":880,"subject":"Board update","delayed":false}`

	heySentDelayed = `{"id":2201,"topic_id":880,"subject":"Board update","delayed":true,` +
		`"notice":"Message sent","undo_action":"https://app.hey.com/topics/880/undo_send","undo_timeout":12}`

	heySentBeforeIDs        = `{}`
	heySentDelayedBeforeIDs = `{"notice":"Message sent","undo_action":"https://app.hey.com/topics/880/undo_send","undo_timeout":12}`
)

// answeringDeliveries puts HEY's answer for a delivered message in front of a test server
// that answers the delivery some other way. A draft save (204 with a Location) and every
// other request pass through untouched.
func answeringDeliveries(inner http.Handler, answer string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		inner.ServeHTTP(recorder, r)
		delivering := r.Method == http.MethodPost && (r.URL.Path == "/messages.json" || strings.HasSuffix(r.URL.Path, "/replies.json")) ||
			r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/messages/")
		if delivering && recorder.Code < 300 && recorder.Header().Get("Location") == "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, answer)
			return
		}
		maps.Copy(w.Header(), recorder.Header())
		w.WriteHeader(recorder.Code)
		_, _ = io.Copy(w, bytes.NewReader(recorder.Body.Bytes()))
	})
}

func decodeResponse(t *testing.T, stdout string) output.Response {
	t.Helper()
	var response output.Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	return response
}

func assertSentData(t *testing.T, response output.Response, want map[string]any) {
	t.Helper()
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want an object", response.Data)
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("data = %#v, want %#v", data, want)
	}
}

func assertReadsThread(t *testing.T, response output.Response, threadID string) {
	t.Helper()
	want := "hey thread read " + threadID
	for _, breadcrumb := range response.Breadcrumbs {
		if breadcrumb.Command == want {
			return
		}
	}
	t.Errorf("breadcrumbs = %+v, want %q", response.Breadcrumbs, want)
}

func composeNewMessage(t *testing.T, answer string, extra ...string) (output.Response, error) {
	t.Helper()
	var writes []draftWrite
	args := append([]string{"compose", "--to", "maria@example.com", "--subject", "Board update", "-m", "Numbers to follow."}, extra...)
	return runJSONCommand(t, answeringDeliveries(draftLifecycleServer(t, draftEditJSON, &writes), answer), args...)
}

func TestComposeAnswersTheMessageHEYDeliveredAndItsThread(t *testing.T) {
	response, err := composeNewMessage(t, heySentNow)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	assertSentData(t, response, map[string]any{"id": float64(2201), "topic_id": float64(880), "subject": "Board update", "delayed": false})
	assertReadsThread(t, response, "880")
	if response.Summary != "Message sent" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestComposeSaysWhenUndoSendHoldsTheMessageBack(t *testing.T) {
	response, err := composeNewMessage(t, heySentDelayed)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	assertSentData(t, response, map[string]any{"id": float64(2201), "topic_id": float64(880), "subject": "Board update", "delayed": true})
}

// A HEY that has not started naming the delivered entry still delivers the message: the
// answer carries none of the ids and nothing is invented in their place.
func TestComposeAgainstAHEYThatNamesNoEntryAnswersWithoutIDs(t *testing.T) {
	for _, tt := range []struct {
		name   string
		answer string
		want   map[string]any
	}{
		{"nothing", heySentBeforeIDs, map[string]any{}},
		{"only the undo", heySentDelayedBeforeIDs, map[string]any{"delayed": true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response, err := composeNewMessage(t, tt.answer)
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			assertSentData(t, response, tt.want)
			if len(response.Breadcrumbs) != 0 {
				t.Errorf("breadcrumbs = %+v, want none without a thread", response.Breadcrumbs)
			}
		})
	}
}

// A message can go out without a subject — a draft edited to none, say — and HEY then
// answers an empty one. That is the subject it went out with, and it is reported as HEY
// served it, never replaced by one the command knew locally.
func TestSendsReportTheEmptySubjectHEYServed(t *testing.T) {
	const sentWithoutSubject = `{"id":2201,"topic_id":880,"subject":"","delayed":false}`
	want := map[string]any{"id": float64(2201), "topic_id": float64(880), "subject": "", "delayed": false}

	t.Run("compose", func(t *testing.T) {
		response, err := composeNewMessage(t, sentWithoutSubject)
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		assertSentData(t, response, want)
	})

	t.Run("draft send", func(t *testing.T) {
		var writes []draftWrite
		response, err := runJSONCommand(t, answeringDeliveries(draftLifecycleServer(t, draftEditJSON, &writes), sentWithoutSubject),
			"draft", "send", "12345")
		if err != nil {
			t.Fatalf("draft send: %v", err)
		}
		assertSentData(t, response, want)
	})

	t.Run("forward", func(t *testing.T) {
		server, sent := forwardServer(t, `[{"id":11},{"id":12}]`)
		sent.SendAnswer = sentWithoutSubject

		stdout, err := runCLIOutput(t, server, "--account", "8", "forward", "7", "--to", "alice@example.com")
		if err != nil {
			t.Fatalf("forward: %v", err)
		}
		assertSentData(t, decodeResponse(t, stdout), map[string]any{
			"thread_id": float64(7), "entry_id": float64(12),
			"id": float64(2201), "topic_id": float64(880), "subject": "", "delayed": false,
			"to": []any{"alice@example.com"}, "cc": []any{}, "bcc": []any{},
		})
	})
}

func TestComposeFromAnswersTheMessageHEYDelivered(t *testing.T) {
	var writes []draftWrite
	response, err := runJSONCommand(t, answeringDeliveries(senderServer(t, senderIdentity, &writes, nil), heySentNow),
		"compose", "--from", "billing@example.org", "--to", "maria@example.com", "--subject", "Board update", "-m", "Numbers.")
	if err != nil {
		t.Fatalf("compose --from: %v", err)
	}
	assertSentData(t, response, map[string]any{"id": float64(2201), "topic_id": float64(880), "subject": "Board update", "delayed": false})
}

func TestComposeDraftStillAnswersTheDraftID(t *testing.T) {
	response, err := composeNewMessage(t, heySentNow, "--draft")
	if err != nil {
		t.Fatalf("compose --draft: %v", err)
	}
	assertSentData(t, response, map[string]any{"id": float64(12345)})
}

// A reply lands on the thread it answers — or, when it breaks out into a thread of its
// own on a Domains account, on that new one, which is the thread worth reading next.
func TestReplyAnswersTheThreadTheReplyLandedOn(t *testing.T) {
	for _, command := range [][]string{
		{"reply", "7", "-m", "Thursday works."},
		{"compose", "--thread-id", "7", "-m", "Thursday works."},
	} {
		t.Run(command[0], func(t *testing.T) {
			server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)
			sent.SendAnswer = `{"id":2202,"topic_id":881,"subject":"Re: Weekly sync","delayed":false}`

			stdout, err := runCLIOutput(t, server, append([]string{"--account", "8"}, command...)...)
			if err != nil {
				t.Fatalf("%s: %v", command[0], err)
			}
			response := decodeResponse(t, stdout)
			assertSentData(t, response, map[string]any{"id": float64(2202), "topic_id": float64(881), "subject": "Re: Weekly sync", "delayed": false})
			assertReadsThread(t, response, "881")
		})
	}
}

func TestReplyAgainstAHEYThatNamesNoEntryStillPointsAtTheThreadItAnswered(t *testing.T) {
	server, _ := threadReplyServer(t, messageAddressedToJane, 11, 12)

	stdout, err := runCLIOutput(t, server, "--account", "8", "reply", "7", "-m", "Thursday works.")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	response := decodeResponse(t, stdout)
	assertSentData(t, response, map[string]any{})
	assertReadsThread(t, response, "7")
}

// thread_id and entry_id name what was forwarded, and stay; id and topic_id name the
// message that went out and the thread it started.
func TestForwardAnswersTheNewThreadBesideTheOneItForwarded(t *testing.T) {
	server, sent := forwardServer(t, `[{"id":11},{"id":12}]`)
	sent.SendAnswer = `{"id":2203,"topic_id":882,"subject":"Fwd: Quarterly planning","delayed":false}`

	stdout, err := runCLIOutput(t, server, "--account", "8", "forward", "7", "--to", "alice@example.com")
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	response := decodeResponse(t, stdout)
	assertSentData(t, response, map[string]any{
		"thread_id": float64(7), "entry_id": float64(12),
		"id": float64(2203), "topic_id": float64(882),
		"subject": "Fwd: Quarterly planning", "delayed": false,
		"to": []any{"alice@example.com"}, "cc": []any{}, "bcc": []any{},
	})
	assertReadsThread(t, response, "882")
}

func TestForwardAgainstAHEYThatNamesNoEntryKeepsItsOwnFields(t *testing.T) {
	server, _ := forwardServer(t, `[{"id":11},{"id":12}]`)

	stdout, err := runCLIOutput(t, server, "--account", "8", "forward", "7", "--to", "alice@example.com")
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	assertSentData(t, decodeResponse(t, stdout), map[string]any{
		"thread_id": float64(7), "entry_id": float64(12), "subject": "Fwd: Quarterly planning",
		"to": []any{"alice@example.com"}, "cc": []any{}, "bcc": []any{},
	})
}

func TestDraftSendAnswersTheEntryHEYDelivered(t *testing.T) {
	for _, tt := range []struct {
		name   string
		answer string
		want   map[string]any
	}{
		{"named", `{"id":12345,"topic_id":880,"subject":"Quarterly planning","delayed":true,"notice":"Message sent","undo_action":"https://app.hey.com/topics/880/undo_send","undo_timeout":12}`,
			map[string]any{"id": float64(12345), "topic_id": float64(880), "subject": "Quarterly planning", "delayed": true}},
		// A draft that breaks out into a thread of its own goes out as a new entry.
		{"broken out", `{"id":2204,"topic_id":883,"subject":"Quarterly planning","delayed":false}`,
			map[string]any{"id": float64(2204), "topic_id": float64(883), "subject": "Quarterly planning", "delayed": false}},
		{"before ids", heySentBeforeIDs, map[string]any{"id": float64(12345)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var writes []draftWrite
			response, err := runJSONCommand(t, answeringDeliveries(draftLifecycleServer(t, draftEditJSON, &writes), tt.answer),
				"draft", "send", "12345")
			if err != nil {
				t.Fatalf("draft send: %v", err)
			}
			assertSentData(t, response, tt.want)
			if response.Summary != "Draft sent" {
				t.Errorf("summary = %q", response.Summary)
			}
		})
	}
}

func TestSendsNameTheMessageAndThreadInTheirStyledLine(t *testing.T) {
	composing := []string{"compose", "--to", "maria@example.com", "--subject", "Board update", "-m", "Numbers."}
	answering := func(answer string) func(t *testing.T) http.Handler {
		return func(t *testing.T) http.Handler {
			var writes []draftWrite
			return answeringDeliveries(draftLifecycleServer(t, draftEditJSON, &writes), answer)
		}
	}
	for _, tt := range []struct {
		name    string
		handler func(t *testing.T) http.Handler
		args    []string
		want    string
	}{
		{"compose", answering(heySentNow), composing, "Message sent (message 2201, thread 880).\n"},
		{"compose held by Undo Send", answering(heySentDelayed), composing,
			"Message sent (message 2201, thread 880); Undo Send is holding it back.\n"},
		{"compose without the entry", answering(heySentBeforeIDs), composing, "Message sent.\n"},
		{"compose held without the entry", answering(heySentDelayedBeforeIDs), composing,
			"Message sent; Undo Send is holding it back.\n"},
		// A draft goes out under its own id, which the line already names.
		{"draft send", answering(`{"id":12345,"topic_id":880,"subject":"Quarterly planning","delayed":false}`),
			[]string{"draft", "send", "12345"}, "Draft 12345 sent (thread 880).\n"},
		// One that breaks out goes out as a new entry, and only HEY's answer can name it.
		{"draft send broken out", answering(`{"id":2204,"topic_id":883,"subject":"Quarterly planning","delayed":false}`),
			[]string{"draft", "send", "12345"}, "Draft 12345 sent (message 2204, thread 883).\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler(t))
			t.Cleanup(server.Close)

			stdout, _, err := runCLIRaw(t, server, append([]string{"--styled"}, tt.args...)...)
			if err != nil {
				t.Fatalf("%v: %v", tt.args, err)
			}
			if stdout != tt.want {
				t.Errorf("stdout = %q, want %q", stdout, tt.want)
			}
		})
	}
}

func TestAReplyNamesItselfAndTheThreadItLandedOnInItsStyledLine(t *testing.T) {
	server, sent := threadReplyServer(t, messageAddressedToJane, 11, 12)
	sent.SendAnswer = `{"id":2202,"topic_id":881,"subject":"Re: Weekly sync","delayed":false}`

	stdout, _, err := runCLIRaw(t, server, "--styled", "--account", "8", "reply", "7", "-m", "Thursday works.")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if stdout != "Reply sent (message 2202, thread 881).\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

// A delivery HEY refuses is still an error, with no confirmation and no ids.
func TestARefusedSendIsStillAnError(t *testing.T) {
	refusing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/messages.json" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"errors":["Recipient maria@example is not a valid email address"]}`)
			return
		}
		var writes []draftWrite
		draftLifecycleServer(t, draftEditJSON, &writes).ServeHTTP(w, r)
	})

	response, err := runJSONCommand(t, refusing, "compose", "--to", "maria@example", "--subject", "Board update", "-m", "Numbers.")
	var cliErr *apierr.Error
	if err == nil || !errors.As(err, &cliErr) {
		t.Fatalf("err = %v, want the refusal", err)
	}
	if response.OK || response.Data != nil {
		t.Errorf("response = %+v, want no confirmation", response)
	}
}
