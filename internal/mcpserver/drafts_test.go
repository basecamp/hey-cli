package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/mcp/mcptest"
)

// sentBody decodes what the dispatcher handed the client, so assertions see
// the wire body rather than Go types.
func sentBody(t *testing.T, api *fakeAPI) map[string]any {
	t.Helper()
	data, err := json.Marshal(api.body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func entryOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	entry, ok := body["entry"].(map[string]any)
	if !ok {
		t.Fatalf("entry is %T, want an object", body["entry"])
	}
	return entry
}

func callThreads(t *testing.T, api API, action string, params map[string]any) (string, bool) {
	t.Helper()
	cat := loadForTest(t)
	if err := holdToDrafts(cat); err != nil {
		t.Fatal(err)
	}
	threads := domainByKey(t, cat, "threads")
	result, err := (dispatcher{api: api}).handle(context.Background(), threads, mustFind(t, threads, action), params)
	if err != nil {
		t.Fatal(err)
	}
	return textContent(t, result), result.IsError
}

// paramsFor is a valid call to one of the draft-first actions, with entry
// set when it isn't nil.
func paramsFor(action string, entry any) map[string]any {
	params := map[string]any{"acting_sender_id": float64(1), "message": map[string]any{"content": "x"}}
	switch action {
	case "create_reply":
		params["entryId"] = float64(9)
	case "update_message":
		params["messageId"] = float64(5)
	}
	if entry != nil {
		params["entry"] = entry
	}
	return params
}

func draftSaved(location string) *hey.Response {
	return &hey.Response{StatusCode: http.StatusNoContent, Data: json.RawMessage(`null`), Headers: http.Header{"Location": {location}}}
}

// Each of the three operations that can deliver mail saves a draft instead,
// whether or not the caller named a status, and reports it as a draft.
func TestDeliveringOperationsSaveDrafts(t *testing.T) {
	cases := map[string]struct {
		action string
		params map[string]any
		method string
		path   string
	}{
		"create_message, no entry": {
			action: "create_message",
			params: map[string]any{"acting_sender_id": float64(1), "message": map[string]any{"subject": "Hi", "content": "Hello"}},
			method: "POST", path: "/messages.json",
		},
		"create_reply with recipients": {
			action: "create_reply",
			params: map[string]any{
				"entryId": float64(9), "acting_sender_id": float64(1), "message": map[string]any{"content": "Thanks"},
				"entry": map[string]any{"addressed": map[string]any{"directly": []any{"ann@example.com"}}},
			},
			method: "POST", path: "/entries/9/replies.json",
		},
		"update_message naming drafted": {
			action: "update_message",
			params: map[string]any{
				"messageId": float64(5), "acting_sender_id": float64(1), "message": map[string]any{"content": "v2"},
				"entry": map[string]any{"status": "drafted"},
			},
			method: "PUT", path: "/messages/5",
		},
		"update_message with no entry": {
			action: "update_message",
			params: map[string]any{"messageId": float64(5), "acting_sender_id": float64(1), "message": map[string]any{"content": "v2"}},
			method: "PUT", path: "/messages/5",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			api := &fakeAPI{resp: draftSaved("/messages/77")}
			text, isError := callThreads(t, api, tc.action, tc.params)
			if isError {
				t.Fatalf("draft save refused: %s", text)
			}
			if api.method != tc.method || api.path != tc.path {
				t.Errorf("dispatched %s %s, want %s %s", api.method, api.path, tc.method, tc.path)
			}
			entry := entryOf(t, sentBody(t, api))
			if entry["status"] != drafted {
				t.Errorf("entry.status = %v, want drafted: anything else delivers", entry["status"])
			}
			for key := range entry {
				if !draftEntryKeys[key] {
					t.Errorf("entry carried %q to HEY", key)
				}
			}

			var result map[string]any
			if err := json.Unmarshal([]byte(text), &result); err != nil {
				t.Fatalf("result %q: %v", text, err)
			}
			if result["drafted"] != true || result["location"] != "/messages/77" || result["draft_id"] != "77" {
				t.Errorf("result = %v, want drafted, its location and draft_id 77", result)
			}
			if note, _ := result["note"].(string); !strings.Contains(note, "not sent") || !strings.Contains(note, "hey draft send 77") {
				t.Errorf("note = %q, want it to say the draft wasn't sent and how to send it", note)
			}
		})
	}
}

// Editing a scheduled draft clears its schedule: HEY clears an existing
// scheduled delivery when an update omits scheduled_delivery, and a draft
// update never carries it.
func TestUpdatingAScheduledDraftClearsItsSchedule(t *testing.T) {
	api := &fakeAPI{resp: draftSaved("/messages/5")}
	text, isError := callThreads(t, api, "update_message", map[string]any{
		"messageId": float64(5), "acting_sender_id": float64(1), "message": map[string]any{"content": "v2"},
		"entry": map[string]any{"addressed": map[string]any{"directly": []any{"ann@example.com"}}},
	})
	if isError {
		t.Fatal(text)
	}
	entry := entryOf(t, sentBody(t, api))
	for _, key := range []string{"scheduled_delivery", "scheduled_delivery_at_date", "scheduled_delivery_at_hour"} {
		if _, ok := entry[key]; ok {
			t.Errorf("update carried entry.%s, which keeps or sets a schedule", key)
		}
	}
	if entry["status"] != drafted {
		t.Errorf("entry.status = %v, want drafted", entry["status"])
	}
}

// Nothing a caller can put in the entry delivers, schedules or triages, and a
// refused call never reaches HEY.
func TestDraftSavesRefuseWhatWouldDeliver(t *testing.T) {
	refused := map[string]any{
		"delivered status":    map[string]any{"status": "sent"},
		"empty status":        map[string]any{"status": ""},
		"non-string status":   map[string]any{"status": true},
		"scheduled delivery":  map[string]any{"scheduled_delivery": "true", "scheduled_delivery_at_date": "tomorrow"},
		"scheduled date only": map[string]any{"scheduled_delivery_at_date": "tomorrow"},
		"scheduled hour only": map[string]any{"scheduled_delivery_at_hour": "9"},
		"mark done":           map[string]any{"mark_done": "1"},
		"pop":                 map[string]any{"pop": "1"},
		"scheduled bubble up": map[string]any{"scheduled_bubble_up": "tomorrow"},
		"unknown key":         map[string]any{"deliver_now": true},
		"entry not an object": "drafted",
	}
	for name, entry := range refused {
		for _, action := range []string{"create_message", "create_reply", "update_message"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				api := &fakeAPI{}
				text, isError := callThreads(t, api, action, paramsFor(action, entry))
				if !isError {
					t.Fatalf("accepted: %s", text)
				}
				if !strings.Contains(text, action+" saves a draft") {
					t.Errorf("refused for the wrong reason: %s", text)
				}
				if api.method != "" {
					t.Errorf("a refused draft save reached HEY: %s %s", api.method, api.path)
				}
				if name != "entry not an object" && !strings.Contains(text, "hey draft send <id>") {
					t.Errorf("refusal doesn't say how a person sends the draft: %s", text)
				}
			})
		}
	}
}

// The caller's params are its own: forcing the draft copies them.
func TestDraftOnlyLeavesParamsUntouched(t *testing.T) {
	entry := map[string]any{"addressed": map[string]any{"directly": []any{"ann@example.com"}}}
	params := map[string]any{"message": map[string]any{"content": "x"}, "entry": entry}

	forced, err := draftOnly("create_message", params)
	if err != nil {
		t.Fatal(err)
	}
	if entryOf(t, forced)["status"] != drafted {
		t.Error("draft not forced")
	}
	if _, ok := entry["status"]; ok {
		t.Error("the caller's entry was modified")
	}
	if !reflect.DeepEqual(params, map[string]any{"message": map[string]any{"content": "x"}, "entry": entry}) {
		t.Errorf("params modified: %v", params)
	}
}

// A save HEY didn't answer as a draft is refused as unconfirmed, since it may
// have been sent: delivery answers the sent message, and an entry HEY didn't
// keep as a draft answers a redirect that net/http follows to a 200.
func TestDraftSaveNotAnsweredAsADraftIsUnconfirmed(t *testing.T) {
	for name, resp := range map[string]*hey.Response{
		"the sent message":     {StatusCode: http.StatusOK, Data: json.RawMessage(`{"id":77,"status":"delivered"}`)},
		"a followed redirect":  {StatusCode: http.StatusOK, Data: json.RawMessage(`{"id":4471830}`)},
		"created":              {StatusCode: http.StatusCreated, Data: json.RawMessage(`null`)},
		"204 without Location": {StatusCode: http.StatusNoContent, Data: json.RawMessage(`null`)},
	} {
		for _, action := range []string{"create_message", "create_reply", "update_message"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				text, isError := callThreads(t, &fakeAPI{resp: resp}, action, paramsFor(action, nil))
				if !isError {
					t.Fatalf("unconfirmed save reported as a success: %s", text)
				}
				if !strings.Contains(text, "may not have saved a draft") || !strings.Contains(text, "may have been sent") {
					t.Errorf("error = %q", text)
				}
				if strings.Contains(text, `"drafted"`) {
					t.Errorf("unconfirmed save reported as drafted: %s", text)
				}
			})
		}
	}
}

// A draft save that fails without HEY's answer saying it declined (a dropped
// connection, a timeout, a 5xx) may still have landed, so the error says to
// check HEY before retrying. A 4xx is HEY declining, and passes through.
func TestDraftSaveWithAnUnknownOutcomeSaysToCheckHEY(t *testing.T) {
	unknown := map[string]error{
		"connection dropped": errors.New("connection reset by peer"),
		"timeout":            context.DeadlineExceeded,
		"server error":       &hey.Error{Message: "server error", HTTPStatus: http.StatusBadGateway},
		"request timeout":    &hey.Error{Message: "timeout", HTTPStatus: http.StatusRequestTimeout},
	}
	for name, callErr := range unknown {
		for _, action := range []string{"create_message", "create_reply", "update_message"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				text, isError := callThreads(t, &fakeAPI{err: callErr}, action, paramsFor(action, nil))
				if !isError {
					t.Fatalf("failed save reported as a success: %s", text)
				}
				if !strings.Contains(text, action+" may have saved a draft") || !strings.Contains(text, "before trying again") || !strings.Contains(text, callErr.Error()) {
					t.Errorf("error = %q, want the cause and a warning to check HEY before retrying", text)
				}
			})
		}
	}

	declined := &hey.Error{Message: "validation failed", HTTPStatus: http.StatusUnprocessableEntity}
	for _, action := range []string{"create_message", "create_reply", "update_message"} {
		text, isError := callThreads(t, &fakeAPI{err: declined}, action, paramsFor(action, nil))
		if !isError || strings.Contains(text, "may have saved") || !strings.Contains(text, "validation failed") {
			t.Errorf("%s: a declined save = %q, want HEY's error as is", action, text)
		}
	}
}

func TestDraftIDReadsTheLocation(t *testing.T) {
	for location, want := range map[string]string{
		"/messages/77":                      "77",
		"https://app.hey.com/messages/77":   "77",
		"https://app.hey.com/messages/77/":  "77",
		"/topics/3":                         "",
		"/messages/new":                     "",
		"https://app.hey.com/messages/77/x": "",
		"https://app.hey.com/xmessages/77":  "",
		"%zz":                               "",
	} {
		if got := draftID(location); got != want {
			t.Errorf("draftID(%q) = %q, want %q", location, got, want)
		}
	}
}

// A call with no params at all reaches request validation, not a panic.
func TestDraftSaveWithoutParamsIsAnInBandError(t *testing.T) {
	api := &fakeAPI{}
	_, session := connect(t, api, Config{})
	text, isError := mcptest.CallText(t, session, "hey_threads", map[string]any{"action": "update_message"})
	if !isError || !strings.Contains(text, "messageId") {
		t.Fatalf("result = %q (isError %v)", text, isError)
	}
	if api.method != "" {
		t.Errorf("reached HEY: %s %s", api.method, api.path)
	}
}

// The tool description and describe serve the draft-only contract: the
// summary and doc say so and name how a person sends, and the entry schema
// offers recipients and a drafted status, nothing else.
func TestDescribeServesTheDraftOnlyContract(t *testing.T) {
	_, session := connect(t, &fakeAPI{}, Config{})

	tool := mcptest.ListTools(t, session)["hey_threads"]
	if tool == nil {
		t.Fatal("hey_threads not served")
	}
	if !strings.Contains(tool.Description, "only ever save drafts") || !strings.Contains(tool.Description, "hey draft send <id>") {
		t.Errorf("hey_threads description doesn't state draft-first: %s", tool.Description)
	}

	for _, action := range []string{"create_message", "create_reply", "update_message"} {
		t.Run(action, func(t *testing.T) {
			if !strings.Contains(tool.Description, "- "+action+": ") || !strings.Contains(tool.Description, "(saved as a draft, never sent)") {
				t.Errorf("tool description's %s line doesn't say draft-only", action)
			}
			text, isError := mcptest.CallText(t, session, "hey_threads", map[string]any{
				"action": "describe", "params": map[string]any{"action": action},
			})
			if isError {
				t.Fatal(text)
			}
			var op struct {
				Summary string `json:"summary"`
				Doc     string `json:"doc"`
				Body    struct {
					Properties struct {
						Entry struct {
							Properties           map[string]map[string]any `json:"properties"`
							AdditionalProperties *bool                     `json:"additionalProperties"`
						} `json:"entry"`
					} `json:"properties"`
				} `json:"body"`
			}
			if err := json.Unmarshal([]byte(text), &op); err != nil {
				t.Fatalf("%s: %v", text, err)
			}
			if !strings.HasSuffix(op.Summary, "(saved as a draft, never sent)") {
				t.Errorf("summary = %q", op.Summary)
			}
			if !strings.HasPrefix(op.Doc, draftDoc) {
				t.Errorf("doc doesn't lead with the draft-only contract: %q", op.Doc)
			}
			entry := op.Body.Properties.Entry
			if got := slices.Sorted(maps.Keys(entry.Properties)); !reflect.DeepEqual(got, []string{"addressed", "status"}) {
				t.Errorf("entry properties = %v, want addressed and status", got)
			}
			if !reflect.DeepEqual(entry.Properties["status"]["enum"], []any{drafted}) {
				t.Errorf("status enum = %v", entry.Properties["status"]["enum"])
			}
			if entry.AdditionalProperties == nil || *entry.AdditionalProperties {
				t.Error("entry schema admits other properties")
			}
		})
	}
}

// End to end through the real SDK against a stub HEY: a request to send is
// refused without an HTTP request, and a plain save comes back as a draft.
func TestDraftFirstThroughTheSDK(t *testing.T) {
	var requests []string
	var lastEntry map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		var body struct {
			Entry map[string]any `json:"entry"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastEntry = body.Entry
		w.Header().Set("Location", "/messages/987")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	client := hey.NewClient(&hey.Config{BaseURL: upstream.URL}, &hey.StaticTokenProvider{Token: "t"})
	_, session := connect(t, client, Config{})

	for _, entry := range []map[string]any{{"status": "sent"}, {"scheduled_delivery": "true"}, {"mark_done": "1"}} {
		text, isError := mcptest.CallText(t, session, "hey_threads", map[string]any{
			"action": "create_message",
			"params": map[string]any{"acting_sender_id": float64(1), "message": map[string]any{"content": "x"}, "entry": entry},
		})
		if !isError {
			t.Errorf("entry %v accepted: %s", entry, text)
		}
	}
	if len(requests) != 0 {
		t.Fatalf("refused requests reached HEY: %v", requests)
	}

	text, isError := mcptest.CallText(t, session, "hey_threads", map[string]any{
		"action": "create_message",
		"params": map[string]any{"acting_sender_id": float64(1), "message": map[string]any{"content": "x"}},
	})
	if isError {
		t.Fatal(text)
	}
	if !reflect.DeepEqual(requests, []string{"POST /messages.json"}) {
		t.Errorf("requests = %v", requests)
	}
	if lastEntry["status"] != drafted {
		t.Errorf("HEY received entry %v, want status drafted", lastEntry)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if got["drafted"] != true || got["draft_id"] != "987" {
		t.Errorf("result = %v", got)
	}
}

// TestEveryServedOpenWorldOperationIsDraftFirst holds draftFirst total over
// the catalog: an operation the model marks open_world (it can reach people
// outside the mailbox) is served only with a draft rule, and each rule names
// a served open-world operation. Mapping a new sending operation (bulk
// reply, a calendar delete that emails attendees) fails here until someone
// decides how it's held.
func TestEveryServedOpenWorldOperationIsDraftFirst(t *testing.T) {
	data, err := modelFS.ReadFile("model/behavior-model.json")
	if err != nil {
		t.Fatal(err)
	}
	var model struct {
		Operations map[string]struct {
			OpenWorld bool `json:"open_world"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	if len(model.Operations) == 0 {
		t.Fatal("behavior model has no operations")
	}

	served := map[string]bool{}
	for _, d := range loadForTest(t).Domains {
		for _, op := range d.Operations {
			served[op.ID] = true
			if model.Operations[op.ID].OpenWorld && !draftFirst[op.ID] {
				t.Errorf("%s is open-world and served without a draft rule", op.ID)
			}
		}
	}
	for id := range draftFirst {
		if !served[id] {
			t.Errorf("draft rule for %s, which isn't served", id)
		}
		if !model.Operations[id].OpenWorld {
			t.Errorf("draft rule for %s, which the model no longer marks open-world: decide whether it still needs one", id)
		}
	}
}
