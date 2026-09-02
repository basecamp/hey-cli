package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type recordedSetAside struct {
	requests []string
	bodies   []map[string]any
}

// setAsideServer is a Set Aside of three threads over two pages, two of them in group 42,
// and a group index that also lists an empty group 43.
func setAsideServer(recorded *recordedSetAside) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.requests = append(recorded.requests, r.Method+" "+r.URL.Path)
		if r.Body != nil {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				recorded.bodies = append(recorded.bodies, body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /set_aside.json":
			if r.URL.Query().Get("page") == "page-2" {
				_, _ = io.WriteString(w, `{"id":3,"kind":"asidebox","name":"Set Aside","postings":[
					{"id":103,"kind":"topic","summary":"Tile samples","box_group_id":42,"app_url":"https://app.hey.com/topics/503","creator":{"name":"Marta Novak"}}
				]}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":3,"kind":"asidebox","name":"Set Aside","next_history_url":"https://app.hey.com/set_aside.json?page=page-2","postings":[
				{"id":101,"kind":"topic","summary":"Cabinet estimate","box_group_id":42,"app_url":"https://app.hey.com/topics/501","creator":{"name":"Jane Doe"}},
				{"id":102,"kind":"topic","summary":"Flight confirmation","app_url":"https://app.hey.com/topics/502","creator":{"name":"Ada Lovelace"}}
			]}`)
		case "GET /boxes.json":
			_, _ = io.WriteString(w, `[{"id":1,"kind":"imbox","name":"Imbox"},{"id":3,"kind":"asidebox","name":"Set Aside"}]`)
		case "GET /boxes/3/groups.json":
			_, _ = io.WriteString(w, `{"box_groups":[{"id":43},{"id":42}]}`)
		case "GET /boxes/3/groups/42.json":
			w.Header().Set("X-Total-Count", "2")
			if r.URL.Query().Get("page") == "group-page-2" {
				_, _ = io.WriteString(w, `{"id":42,"box_id":3,"postings":[
					{"id":103,"kind":"topic","summary":"Tile samples","box_group_id":42,"app_url":"https://app.hey.com/topics/503","creator":{"name":"Marta Novak"}}
				]}`)
				return
			}
			w.Header().Set("Link", `<http://`+r.Host+`/boxes/3/groups/42.json?page=group-page-2>; rel="next"`)
			_, _ = io.WriteString(w, `{"id":42,"box_id":3,"postings":[
				{"id":101,"kind":"topic","summary":"Cabinet estimate","box_group_id":42,"app_url":"https://app.hey.com/topics/501","creator":{"name":"Jane Doe"}}
			]}`)
		case "GET /boxes/3/groups/43.json":
			w.Header().Set("X-Total-Count", "0")
			_, _ = io.WriteString(w, `{"id":43,"box_id":3,"postings":[]}`)
		case "POST /boxes/3/groups.json":
			_, _ = io.WriteString(w, `{"id":44}`)
		case "DELETE /boxes/3/groups/42.json":
			w.WriteHeader(http.StatusNoContent)
		case "POST /postings/box_groups.json", "DELETE /postings/box_groups.json":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
}

func TestSetAsideViewCarriesGroups(t *testing.T) {
	recorded := &recordedSetAside{}
	response, err := runJSONCommand(t, setAsideServer(recorded), "set-aside", "view", "--all")
	if err != nil {
		t.Fatalf("execute set-aside view: %v", err)
	}
	if response.Summary != "3 threads in Set Aside" {
		t.Errorf("summary = %q", response.Summary)
	}
	box := response.Data.(map[string]any)
	postings := box["postings"].([]any)
	if len(postings) != 3 {
		t.Fatalf("postings = %d, want 3", len(postings))
	}
	first := postings[0].(map[string]any)
	if first["id"] != float64(101) || first["topic_id"] != float64(501) || first["box_group_id"] != float64(42) {
		t.Errorf("first posting = %#v", first)
	}
	if _, grouped := postings[1].(map[string]any)["box_group_id"]; grouped {
		t.Errorf("ungrouped posting carries box_group_id: %#v", postings[1])
	}

	styled, err := runStyledCommand(t, setAsideServer(&recordedSetAside{}), "set-aside", "view")
	if err != nil {
		t.Fatalf("execute styled set-aside view: %v", err)
	}
	if !strings.Contains(styled, "Group") || !strings.Contains(styled, "42") || !strings.Contains(styled, "Cabinet estimate") {
		t.Errorf("styled output = %q, want a Group column", styled)
	}

	markdown, err := runFormattedCommand(t, setAsideServer(&recordedSetAside{}), []string{"--markdown"}, "set-aside", "view")
	if err != nil {
		t.Fatalf("execute markdown set-aside view: %v", err)
	}
	if !strings.Contains(markdown, "| group |") {
		t.Errorf("markdown output = %q, want a group column", markdown)
	}
}

func TestBoxViewHasNoGroupColumn(t *testing.T) {
	styled, err := runStyledCommand(t, setAsideServer(&recordedSetAside{}), "box", "view", "set aside")
	if err != nil {
		t.Fatalf("execute box view: %v", err)
	}
	if strings.Contains(styled, "Group") {
		t.Errorf("box view styled output = %q, want no Group column", styled)
	}
}

func TestSetAsideGroupList(t *testing.T) {
	recorded := &recordedSetAside{}
	response, err := runJSONCommand(t, setAsideServer(recorded), "set-aside", "group", "list")
	if err != nil {
		t.Fatalf("execute group list: %v", err)
	}
	want := []string{"GET /boxes.json", "GET /boxes/3/groups.json", "GET /boxes/3/groups/43.json", "GET /boxes/3/groups/42.json"}
	if strings.Join(recorded.requests, ",") != strings.Join(want, ",") {
		t.Errorf("requests = %v, want %v", recorded.requests, want)
	}
	if response.Summary != "2 groups in Set Aside" || response.Notice != "" {
		t.Errorf("response = %#v", response)
	}
	groups := response.Data.([]any)
	if len(groups) != 2 {
		t.Fatalf("groups = %#v, want two", groups)
	}
	full := groups[0].(map[string]any)
	if full["id"] != float64(42) || full["thread_count"] != float64(2) {
		t.Errorf("group 42 = %#v", full)
	}
	empty := groups[1].(map[string]any)
	if empty["id"] != float64(43) || empty["thread_count"] != float64(0) {
		t.Errorf("group 43 = %#v", empty)
	}

	styled, err := runStyledCommand(t, setAsideServer(&recordedSetAside{}), "set-aside", "group", "list")
	if err != nil {
		t.Fatalf("execute styled group list: %v", err)
	}
	if !strings.Contains(styled, "Threads") || !strings.Contains(styled, "42") || !strings.Contains(styled, "43") {
		t.Errorf("styled output = %q", styled)
	}

	ids, err := runFormattedCommand(t, setAsideServer(&recordedSetAside{}), []string{"--ids-only"}, "set-aside", "group", "list")
	if err != nil || ids != "42\n43\n" {
		t.Errorf("ids output = %q, err = %v", ids, err)
	}
	count, err := runFormattedCommand(t, setAsideServer(&recordedSetAside{}), []string{"--count"}, "set-aside", "group", "list")
	if err != nil || count != "2\n" {
		t.Errorf("count output = %q, err = %v", count, err)
	}
}

func TestSetAsideGroupView(t *testing.T) {
	recorded := &recordedSetAside{}
	response, err := runJSONCommand(t, setAsideServer(recorded), "set-aside", "group", "view", "42", "--all")
	if err != nil {
		t.Fatalf("execute group view: %v", err)
	}
	want := []string{"GET /boxes.json", "GET /boxes/3/groups/42.json", "GET /boxes/3/groups/42.json"}
	if strings.Join(recorded.requests, ",") != strings.Join(want, ",") {
		t.Errorf("requests = %v, want %v", recorded.requests, want)
	}
	if response.Summary != "2 threads in Set Aside group 42" {
		t.Errorf("summary = %q", response.Summary)
	}
	group := response.Data.(map[string]any)
	if group["id"] != float64(42) || group["box_id"] != float64(3) || group["total_count"] != float64(2) {
		t.Errorf("group = %#v", group)
	}
	postings := group["postings"].([]any)
	if len(postings) != 2 {
		t.Fatalf("postings = %#v, want two", postings)
	}
	second := postings[1].(map[string]any)
	if second["id"] != float64(103) || second["topic_id"] != float64(503) {
		t.Errorf("second posting = %#v", second)
	}

	firstPage, err := runJSONCommand(t, setAsideServer(&recordedSetAside{}), "set-aside", "group", "view", "42")
	if err != nil {
		t.Fatalf("execute first page: %v", err)
	}
	if firstPage.Notice != "Showing 1 of 2 results. Use --all to see everything." {
		t.Errorf("first page notice = %q", firstPage.Notice)
	}
	if next := firstPage.Data.(map[string]any)["next_page"]; next != "group-page-2" {
		t.Errorf("next_page = %#v, want group-page-2", next)
	}

	ids, err := runFormattedCommand(t, setAsideServer(&recordedSetAside{}), []string{"--ids-only"}, "set-aside", "group", "view", "42", "--all")
	if err != nil || ids != "101\n103\n" {
		t.Errorf("ids output = %q, err = %v", ids, err)
	}

	empty, err := runJSONCommand(t, setAsideServer(&recordedSetAside{}), "set-aside", "group", "view", "43")
	if err != nil {
		t.Fatalf("execute empty group view: %v", err)
	}
	if empty.Summary != "0 threads in Set Aside group 43" {
		t.Errorf("empty summary = %q", empty.Summary)
	}

	_, err = runJSONCommand(t, setAsideServer(&recordedSetAside{}), "set-aside", "group", "view", "99")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown group err = %v, want not found", err)
	}
}

func TestSetAsideGroupCreate(t *testing.T) {
	recorded := &recordedSetAside{}
	response, err := runJSONCommand(t, setAsideServer(recorded), "set-aside", "group", "create", "101", "102")
	if err != nil {
		t.Fatalf("execute group create: %v", err)
	}
	want := []string{"GET /boxes.json", "POST /boxes/3/groups.json"}
	if strings.Join(recorded.requests, ",") != strings.Join(want, ",") {
		t.Errorf("requests = %v, want %v", recorded.requests, want)
	}
	if ids := recorded.bodies[0]["posting_ids"].([]any); len(ids) != 2 || ids[0] != float64(101) || ids[1] != float64(102) {
		t.Errorf("posting_ids = %#v", recorded.bodies[0])
	}
	if response.Summary != "Group 44 created with 2 threads" {
		t.Errorf("summary = %q", response.Summary)
	}
	if data := response.Data.(map[string]any); data["id"] != float64(44) {
		t.Errorf("data = %#v", data)
	}

	styled, err := runStyledCommand(t, setAsideServer(&recordedSetAside{}), "set-aside", "group", "create", "101")
	if err != nil || styled != "Group 44 created with 1 thread.\n" {
		t.Errorf("styled output = %q, err = %v", styled, err)
	}
}

func TestSetAsideGroupAdd(t *testing.T) {
	recorded := &recordedSetAside{}
	response, err := runJSONCommand(t, setAsideServer(recorded), "set-aside", "group", "add", "102", "--to", "42")
	if err != nil {
		t.Fatalf("execute group add: %v", err)
	}
	want := []string{"GET /boxes.json", "POST /postings/box_groups.json"}
	if strings.Join(recorded.requests, ",") != strings.Join(want, ",") {
		t.Errorf("requests = %v, want %v", recorded.requests, want)
	}
	body := recorded.bodies[0]
	if body["box_id"] != float64(3) || body["box_group_id"] != float64(42) {
		t.Errorf("body = %#v", body)
	}
	if ids := body["posting_ids"].([]any); len(ids) != 1 || ids[0] != float64(102) {
		t.Errorf("posting_ids = %#v", body["posting_ids"])
	}
	if response.Summary != "1 thread added to group 42" {
		t.Errorf("summary = %q", response.Summary)
	}

	_, err = runJSONCommand(t, setAsideServer(&recordedSetAside{}), "set-aside", "group", "add", "102")
	if err == nil || !strings.Contains(err.Error(), "--to <group-id>") {
		t.Errorf("missing --to err = %v", err)
	}
}

func TestSetAsideGroupRemove(t *testing.T) {
	recorded := &recordedSetAside{}
	response, err := runJSONCommand(t, setAsideServer(recorded), "set-aside", "group", "remove", "101", "103")
	if err != nil {
		t.Fatalf("execute group remove: %v", err)
	}
	if len(recorded.requests) != 1 || recorded.requests[0] != "DELETE /postings/box_groups.json" {
		t.Errorf("requests = %v", recorded.requests)
	}
	if response.Summary != "2 threads removed from their group" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestSetAsideGroupDelete(t *testing.T) {
	recorded := &recordedSetAside{}
	response, err := runJSONCommand(t, setAsideServer(recorded), "set-aside", "group", "delete", "42")
	if err != nil {
		t.Fatalf("execute group delete: %v", err)
	}
	want := []string{"GET /boxes.json", "DELETE /boxes/3/groups/42.json"}
	if strings.Join(recorded.requests, ",") != strings.Join(want, ",") {
		t.Errorf("requests = %v, want %v", recorded.requests, want)
	}
	if response.Summary != "Group 42 deleted; its threads moved to Previously Seen" {
		t.Errorf("summary = %q", response.Summary)
	}

	_, err = runJSONCommand(t, setAsideServer(&recordedSetAside{}), "set-aside", "group", "delete", "0")
	if err == nil {
		t.Error("deleting group 0 succeeded, want a usage error")
	}
}
