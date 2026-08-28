package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/mcp/catalog"
)

// fakeAPI records the single request the dispatcher makes and answers with a
// canned response or error.
type fakeAPI struct {
	method string
	path   string
	body   any

	resp *hey.Response
	err  error
}

func (f *fakeAPI) record(method, path string, body any) (*hey.Response, error) {
	f.method, f.path, f.body = method, path, body
	if f.resp == nil && f.err == nil {
		return &hey.Response{Data: json.RawMessage(`{"ok":true}`), StatusCode: 200}, nil
	}
	return f.resp, f.err
}

func (f *fakeAPI) Get(_ context.Context, path string) (*hey.Response, error) {
	return f.record("GET", path, nil)
}
func (f *fakeAPI) Post(_ context.Context, path string, body any) (*hey.Response, error) {
	return f.record("POST", path, body)
}
func (f *fakeAPI) Put(_ context.Context, path string, body any) (*hey.Response, error) {
	return f.record("PUT", path, body)
}
func (f *fakeAPI) Patch(_ context.Context, path string, body any) (*hey.Response, error) {
	return f.record("PATCH", path, body)
}
func (f *fakeAPI) Delete(_ context.Context, path string) (*hey.Response, error) {
	return f.record("DELETE", path, nil)
}

func op() *catalog.Operation {
	return &catalog.Operation{
		ID:     "GetBox",
		Action: "get_box",
		Method: "GET",
		Path:   "/boxes/{boxId}",
		Params: []catalog.Param{
			{Name: "boxId", In: "path", Required: true},
			{Name: "page", In: "query"},
		},
	}
}

func TestBuildRequestSubstitutesAndEscapesPathParams(t *testing.T) {
	path, body, err := buildRequest(op(), map[string]any{"boxId": "im box/1"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/boxes/im%20box%2F1" {
		t.Errorf("path = %q", path)
	}
	if body != nil {
		t.Errorf("body = %v, want nil", body)
	}
}

func TestBuildRequestFormatsScalars(t *testing.T) {
	path, _, err := buildRequest(op(), map[string]any{"boxId": float64(42), "page": float64(3)})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/boxes/42?page=3" {
		t.Errorf("path = %q", path)
	}
}

func TestBuildRequestMissingRequiredQueryParam(t *testing.T) {
	required := op()
	required.Params = append(required.Params, catalog.Param{Name: "since", In: "query", Required: true})
	_, _, err := buildRequest(required, map[string]any{"boxId": "1"})
	if err == nil || !strings.Contains(err.Error(), `missing required query parameter "since"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRequestMissingPathParam(t *testing.T) {
	_, _, err := buildRequest(op(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), `missing required path parameter "boxId"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRequestRejectsNonScalarPathParam(t *testing.T) {
	_, _, err := buildRequest(op(), map[string]any{"boxId": []any{1}})
	if err == nil || !strings.Contains(err.Error(), `path parameter "boxId"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRequestRejectsStrayParamsWithoutBody(t *testing.T) {
	_, _, err := buildRequest(op(), map[string]any{"boxId": "1", "bogus": "x"})
	if err == nil || !strings.Contains(err.Error(), `unknown parameter "bogus"`) {
		t.Fatalf("err = %v", err)
	}
}

func bodyOp() *catalog.Operation {
	return &catalog.Operation{
		ID:     "CreateBoxDesignation",
		Action: "create_box_designation",
		Method: "POST",
		Path:   "/boxes/{boxId}/designations.json",
		Params: []catalog.Param{{Name: "boxId", In: "path", Required: true}},
		Body: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"posting_ids": map[string]any{"type": "array"},
			},
		},
		BodyRequired: true,
	}
}

func TestBuildRequestGathersBodyFromRemainingParams(t *testing.T) {
	path, body, err := buildRequest(bodyOp(), map[string]any{
		"boxId":       "7",
		"posting_ids": []any{float64(1), float64(2)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/boxes/7/designations.json" {
		t.Errorf("path = %q", path)
	}
	want := map[string]any{"posting_ids": []any{float64(1), float64(2)}}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body = %#v, want %#v", body, want)
	}
}

func TestBuildRequestRejectsBodyPropertyOutsideSchema(t *testing.T) {
	_, _, err := buildRequest(bodyOp(), map[string]any{"boxId": "7", "bogus": "x"})
	if err == nil || !strings.Contains(err.Error(), `unknown parameter "bogus"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRequestSendsEmptyRequiredBody(t *testing.T) {
	_, body, err := buildRequest(bodyOp(), map[string]any{"boxId": "7"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body, map[string]any{}) {
		t.Errorf("body = %#v, want empty map", body)
	}
}

func TestDispatchCallRoutesByMethod(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		api := &fakeAPI{}
		if _, err := (dispatcher{api: api}).call(context.Background(), method, "/x.json", nil); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if api.method != method {
			t.Errorf("recorded method = %q, want %q", api.method, method)
		}
	}

	if _, err := (dispatcher{api: &fakeAPI{}}).call(context.Background(), "TRACE", "/x.json", nil); err == nil {
		t.Error("unsupported method did not error")
	}
}

func TestDispatchAPIErrorIsInBand(t *testing.T) {
	cat := loadForTest(t)
	boxes := cat.Domains[0]

	api := &fakeAPI{err: errors.New("api: box not found")}
	result, err := (dispatcher{api: api}).handle(context.Background(), boxes, mustFind(t, boxes, "list_boxes"), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("API failure did not produce an isError result")
	}
}

func TestDispatchSurfacesNextPageCursor(t *testing.T) {
	cat := loadForTest(t)
	boxes := cat.Domains[0]

	api := &fakeAPI{resp: &hey.Response{
		Data:       json.RawMessage(`[{"id":1}]`),
		StatusCode: 200,
		Headers: http.Header{"Link": []string{
			`<https://app.hey.com/boxes.json?page=abc123>; rel="next"`,
		}},
	}}
	result, err := (dispatcher{api: api}).handle(context.Background(), boxes, mustFind(t, boxes, "list_boxes"), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %v", result.Content)
	}
	var wrapped struct {
		NextPage string          `json:"next_page"`
		Results  json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal([]byte(textContent(t, result)), &wrapped); err != nil {
		t.Fatal(err)
	}
	if wrapped.NextPage != "abc123" {
		t.Errorf("next_page = %q, want abc123", wrapped.NextPage)
	}
	if string(wrapped.Results) != `[{"id":1}]` {
		t.Errorf("results = %s", wrapped.Results)
	}
}

func TestDispatchChangesFeedFinalCursor(t *testing.T) {
	cat := loadForTest(t)
	boxes := cat.Domains[0]

	api := &fakeAPI{resp: &hey.Response{
		Data:       json.RawMessage(`{"added":[],"updated":[],"deleted":[]}`),
		StatusCode: 200,
		Headers: http.Header{"Link": []string{
			`<https://app.hey.com/boxes/1/postings/changes.json?since=2026-08-28T10%3A00%3A00.000Z&v=5>; rel="next"`,
		}},
	}}
	result, err := (dispatcher{api: api}).handle(context.Background(), boxes, mustFind(t, boxes, "get_box_posting_changes"),
		map[string]any{"boxId": "1", "since": "2026-08-28T09:00:00.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %v", result.Content)
	}
	var wrapped struct {
		NextPage  string          `json:"next_page"`
		NextSince string          `json:"next_since"`
		NextV     string          `json:"next_v"`
		Results   json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal([]byte(textContent(t, result)), &wrapped); err != nil {
		t.Fatal(err)
	}
	if wrapped.NextPage != "" {
		t.Errorf("next_page = %q, want empty on the final changes page", wrapped.NextPage)
	}
	if wrapped.NextSince != "2026-08-28T10:00:00.000Z" {
		t.Errorf("next_since = %q, want 2026-08-28T10:00:00.000Z", wrapped.NextSince)
	}
	if wrapped.NextV != "5" {
		t.Errorf("next_v = %q, want 5", wrapped.NextV)
	}
	if string(wrapped.Results) != `{"added":[],"updated":[],"deleted":[]}` {
		t.Errorf("results = %s", wrapped.Results)
	}
}

func TestDispatchLastPageStaysUnwrapped(t *testing.T) {
	cat := loadForTest(t)
	boxes := cat.Domains[0]

	api := &fakeAPI{resp: &hey.Response{Data: json.RawMessage(`[{"id":1}]`), StatusCode: 200}}
	result, err := (dispatcher{api: api}).handle(context.Background(), boxes, mustFind(t, boxes, "list_boxes"), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if text := textContent(t, result); text != `[{"id":1}]` {
		t.Errorf("result = %q, want the raw listing", text)
	}
}

func TestDispatchBodyCursorFromNextHistoryURL(t *testing.T) {
	cat := loadForTest(t)
	boxes := cat.Domains[0]

	for name, data := range map[string]string{
		"flat":   `{"id":1,"next_history_url":"https://app.hey.com/boxes/1?page=zzz9"}`,
		"nested": `{"box":{"id":1,"next_history_url":"https://app.hey.com/boxes/1?page=zzz9"}}`,
	} {
		api := &fakeAPI{resp: &hey.Response{Data: json.RawMessage(data), StatusCode: 200}}
		result, err := (dispatcher{api: api}).handle(context.Background(), boxes, mustFind(t, boxes, "get_box"), map[string]any{"boxId": "1"})
		if err != nil {
			t.Fatal(err)
		}
		var wrapped struct {
			NextPage string `json:"next_page"`
		}
		if err := json.Unmarshal([]byte(textContent(t, result)), &wrapped); err != nil {
			t.Fatal(err)
		}
		if wrapped.NextPage != "zzz9" {
			t.Errorf("%s: next_page = %q, want zzz9", name, wrapped.NextPage)
		}
	}
}

func TestDispatchEmptyResponseWithLocation(t *testing.T) {
	cat := loadForTest(t)
	threads := domainByKey(t, cat, "threads")

	api := &fakeAPI{resp: &hey.Response{
		StatusCode: 204,
		Headers:    http.Header{"Location": []string{"https://app.hey.com/messages/987"}},
	}}
	result, err := (dispatcher{api: api}).handle(context.Background(), threads, mustFind(t, threads, "create_message"), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	text := textContent(t, result)
	if !strings.Contains(text, "204") || !strings.Contains(text, "messages/987") {
		t.Errorf("result = %q, want status and location", text)
	}
}

func TestDispatchEmptyResponseReportsStatus(t *testing.T) {
	cat := loadForTest(t)
	boxes := cat.Domains[0]

	api := &fakeAPI{resp: &hey.Response{StatusCode: 204}}
	result, err := (dispatcher{api: api}).handle(context.Background(), boxes, mustFind(t, boxes, "get_box"), map[string]any{"boxId": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected isError: %v", result.Content)
	}
	text := textContent(t, result)
	if !strings.Contains(text, "204") {
		t.Errorf("result = %q, want status report", text)
	}
}
