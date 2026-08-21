package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCollectionsCommand(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/collections.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"id":12,"name":"Kitchen remodel","app_url":"/collections/12"},
			{"id":34,"name":"Project Apollo","app_url":"/collections/34"}
		]`)
	}), "collections", "--limit", "1")
	if err != nil {
		t.Fatalf("execute collections: %v", err)
	}
	if response.Summary != "1 collection" || response.Notice != "Showing 1 of 2 results. Use --all to see everything." {
		t.Errorf("response = %#v", response)
	}
	collections, ok := response.Data.([]any)
	if !ok || len(collections) != 1 {
		t.Fatalf("data = %#v, want one collection", response.Data)
	}
	collection := collections[0].(map[string]any)
	if collection["id"] != float64(12) || collection["name"] != "Kitchen remodel" {
		t.Errorf("collection = %#v", collection)
	}
}

func TestCollectionsCommandOutputFormats(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":12,"name":"Kitchen remodel"},{"id":34,"name":"Project Apollo"}]`)
	})
	ids, err := runFormattedCommand(t, handler, []string{"--ids-only"}, "collections")
	if err != nil || ids != "12\n34\n" {
		t.Errorf("ids output = %q, err = %v", ids, err)
	}
	count, err := runFormattedCommand(t, handler, []string{"--count"}, "collections")
	if err != nil || count != "2\n" {
		t.Errorf("count output = %q, err = %v", count, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "collections")
	if err != nil || !strings.Contains(markdown, "| id |") || !strings.Contains(markdown, "Kitchen remodel") {
		t.Errorf("markdown output = %q, err = %v", markdown, err)
	}
	styled, err := runStyledCommand(t, handler, "collections")
	if err != nil || !strings.Contains(styled, "Kitchen remodel") || !strings.Contains(styled, "Project Apollo") {
		t.Errorf("styled output = %q, err = %v", styled, err)
	}
}

func TestCollectionsCommandPreservesEmptyList(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}), "collections")
	if err != nil {
		t.Fatalf("execute collections: %v", err)
	}
	collections, ok := response.Data.([]any)
	if !ok || len(collections) != 0 || response.Summary != "0 collections" {
		t.Errorf("response = %#v, want an empty collection list", response)
	}
}

func TestCollectionCommand(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/collections/12.json" {
			t.Errorf("request = %s %s, want GET /collections/12.json", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("page"); got != "current-cursor" {
			t.Errorf("page = %q, want current-cursor", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "2")
		_, _ = io.WriteString(w, `{"id":12,"name":"Kitchen remodel","postings":[
			{"id":101,"kind":"topic","summary":"Cabinet estimate","app_url":"https://app.hey.com/topics/501","creator":{"name":"Jane Doe"},"created_at":"2026-08-20T09:30:00Z"},
			{"id":102,"kind":"topic","summary":"Tile samples"}
		]}`)
	}), "collection", "12", "--page", "current-cursor", "--limit", "1")
	if err != nil {
		t.Fatalf("execute collection: %v", err)
	}
	if response.Summary != "1 thread in Kitchen remodel" || response.Notice != "Showing 1 of 2 results. Use --all to see everything." {
		t.Errorf("response = %#v", response)
	}
	collection := response.Data.(map[string]any)
	if collection["id"] != float64(12) || collection["name"] != "Kitchen remodel" || collection["total_count"] != float64(2) {
		t.Errorf("collection = %#v", collection)
	}
	postings := collection["postings"].([]any)
	posting := postings[0].(map[string]any)
	if posting["id"] != float64(101) || posting["topic_id"] != float64(501) {
		t.Errorf("posting = %#v", posting)
	}
}

func TestCollectionCommandEmptyPreservesCollectionAndCount(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "0")
		_, _ = io.WriteString(w, `{"id":12,"name":"Kitchen remodel","postings":[]}`)
	}), "collection", "12")
	if err != nil {
		t.Fatalf("execute empty collection: %v", err)
	}
	collection := response.Data.(map[string]any)
	postings, ok := collection["postings"].([]any)
	if !ok || len(postings) != 0 || collection["total_count"] != float64(0) {
		t.Errorf("empty collection contract = %#v", collection)
	}
}

func TestCollectionCommandReturnsContinuation(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "3")
		w.Header().Set("Link", "<http://"+r.Host+"/collections/12.json?page=next-cursor>; rel=\"next\"")
		_, _ = io.WriteString(w, `{"id":12,"name":"Kitchen remodel","postings":[{"id":101,"kind":"topic"},{"id":102,"kind":"topic"}]}`)
	}), "collection", "12")
	if err != nil {
		t.Fatalf("execute collection: %v", err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want first page only", requests.Load())
	}
	collection := response.Data.(map[string]any)
	if collection["next_page"] != "next-cursor" || collection["total_count"] != float64(3) {
		t.Errorf("pagination metadata = %#v", collection)
	}
}

func TestCollectionCommandFetchesAllPages(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "2")
		if request == 1 {
			w.Header().Set("Link", "<http://"+r.Host+"/collections/12.json?page=next-cursor>; rel=\"next\"")
			_, _ = io.WriteString(w, `{"id":12,"name":"Kitchen remodel","postings":[{"id":101,"kind":"topic"}]}`)
			return
		}
		if got := r.URL.Query().Get("page"); got != "next-cursor" {
			t.Errorf("page = %q, want next-cursor", got)
		}
		_, _ = io.WriteString(w, `{"id":12,"name":"Kitchen remodel","postings":[{"id":102,"kind":"topic"}]}`)
	}), "collection", "12", "--all")
	if err != nil {
		t.Fatalf("execute collection --all: %v", err)
	}
	collection := response.Data.(map[string]any)
	if requests.Load() != 2 || len(collection["postings"].([]any)) != 2 || collection["next_page"] != nil {
		t.Errorf("requests = %d, collection = %#v", requests.Load(), collection)
	}
}

func TestCollectionCommandOutputFormats(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "2")
		_, _ = io.WriteString(w, `{"id":12,"name":"Kitchen remodel","postings":[
			{"id":101,"kind":"topic","summary":"Cabinet estimate","app_url":"https://app.hey.com/topics/501","creator":{"name":"Jane Doe"}},
			{"id":102,"kind":"topic","summary":"Tile samples","app_url":"https://app.hey.com/topics/502"}
		]}`)
	})

	ids, err := runFormattedCommand(t, handler, []string{"--ids-only"}, "collection", "12")
	if err != nil || ids != "101\n102\n" {
		t.Errorf("ids output = %q, err = %v", ids, err)
	}
	count, err := runFormattedCommand(t, handler, []string{"--count"}, "collection", "12")
	if err != nil || count != "2\n" {
		t.Errorf("count output = %q, err = %v", count, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "collection", "12")
	if err != nil {
		t.Fatalf("markdown collection: %v", err)
	}
	for _, want := range []string{"# Kitchen remodel", "| id |", "topic_id", "Cabinet estimate", "**Total threads:** 2"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown %q does not contain %q", markdown, want)
		}
	}
	styled, err := runStyledCommand(t, handler, "collection", "12")
	if err != nil {
		t.Fatalf("styled collection: %v", err)
	}
	for _, want := range []string{"Collection: Kitchen remodel", "Thread", "Jane Doe", "Cabinet estimate", "501"} {
		if !strings.Contains(styled, want) {
			t.Errorf("styled output %q does not contain %q", styled, want)
		}
	}
}

func TestCollectionDataOnlyFormatsReportPaginationOnStderr(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "3")
		w.Header().Set("Link", "<http://"+r.Host+"/collections/12.json?page=next-cursor>; rel=\"next\"")
		_, _ = io.WriteString(w, `{"id":12,"name":"Kitchen remodel","postings":[{"id":101,"kind":"topic"},{"id":102,"kind":"topic"}]}`)
	})

	for _, format := range []string{"--ids-only", "--count"} {
		t.Run(format, func(t *testing.T) {
			_, stderr, err := runFormattedCommandWithStderr(t, handler, []string{format}, "collection", "12")
			if err != nil {
				t.Fatalf("collection %s: %v", format, err)
			}
			for _, want := range []string{"notice: Showing 2 of 3 results", "next_page: next-cursor"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr %q does not contain %q", stderr, want)
				}
			}
		})
	}
}

func TestCollectionMutationCommands(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		method      string
		path        string
		wantSummary string
		check       func(*testing.T, *http.Request)
	}{
		{
			name: "create", args: []string{"collection", "create", "Kitchen remodel", "--summary", "Plans and decisions"},
			method: http.MethodPost, path: "/collections", wantSummary: `Collection "Kitchen remodel" created`,
			check: func(t *testing.T, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.Form.Get("collection[name]") != "Kitchen remodel" || r.Form.Get("collection[summary]") != "Plans and decisions" {
					t.Errorf("form = %v", r.Form)
				}
			},
		},
		{
			name: "update", args: []string{"collection", "update", "12", "--name", "Kitchen renovation", "--summary", "Contractor decisions"},
			method: http.MethodPatch, path: "/collections/12.json", wantSummary: "Collection 12 updated",
			check: func(t *testing.T, r *http.Request) {
				var body struct {
					Collection struct {
						Name    string `json:"name"`
						Summary string `json:"summary"`
					} `json:"collection"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Collection.Name != "Kitchen renovation" || body.Collection.Summary != "Contractor decisions" {
					t.Errorf("body = %#v", body)
				}
			},
		},
		{
			name: "add", args: []string{"collection", "add", "501", "--to", "12"},
			method: http.MethodPost, path: "/topics/501/collecting", wantSummary: "1 thread added to collection 12",
			check: func(t *testing.T, r *http.Request) {
				if r.URL.Query().Get("collection_id") != "12" {
					t.Errorf("query = %s", r.URL.RawQuery)
				}
			},
		},
		{
			name: "remove", args: []string{"collection", "remove", "501", "--from", "12"},
			method: http.MethodDelete, path: "/topics/501/collecting", wantSummary: "1 thread removed from collection 12",
			check: func(t *testing.T, r *http.Request) {
				if r.URL.Query().Get("collection_id") != "12" {
					t.Errorf("query = %s", r.URL.RawQuery)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method || r.URL.Path != tt.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tt.method, tt.path)
					http.NotFound(w, r)
					return
				}
				tt.check(t, r)
				w.WriteHeader(http.StatusNoContent)
			}), tt.args...)
			if err != nil {
				t.Fatalf("execute mutation: %v", err)
			}
			if response.Summary != tt.wantSummary {
				t.Errorf("summary = %q, want %q", response.Summary, tt.wantSummary)
			}
		})
	}
}

func TestCollectionMutationHandlesMultipleTopics(t *testing.T) {
	var paths []string
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		w.WriteHeader(http.StatusNoContent)
	}), "collection", "add", "501", "502", "--to", "12")
	if err != nil {
		t.Fatalf("execute add: %v", err)
	}
	want := []string{"/topics/501/collecting?collection_id=12", "/topics/502/collecting?collection_id=12"}
	if strings.Join(paths, "|") != strings.Join(want, "|") || response.Summary != "2 threads added to collection 12" {
		t.Errorf("paths = %v, response = %#v", paths, response)
	}
}

func TestCollectionValidationMakesNoRequest(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"collection", "invalid"}, want: "invalid collection ID"},
		{args: []string{"collection", "create", "   "}, want: "collection name is required"},
		{args: []string{"collection", "update", "12"}, want: "at least one"},
		{args: []string{"collection", "update", "12", "--name", "   "}, want: "collection name is required"},
		{args: []string{"collection", "update", "12", "--summary", "   "}, want: "collection summary is required"},
		{args: []string{"collection", "add", "501"}, want: "collection is required"},
		{args: []string{"collection", "add", "bad", "--to", "12"}, want: "invalid topic ID"},
		{args: []string{"collection", "add", "501", "501", "--to", "12"}, want: "duplicate topic ID"},
		{args: []string{"collection", "remove", "501"}, want: "collection is required"},
		{args: []string{"collection", "remove", "501", "--from", "bad"}, want: "invalid collection ID"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, "_"), func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "unexpected request", http.StatusInternalServerError)
			}), tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want 0", requests.Load())
			}
		})
	}
}

func TestCollectionCreateOmitsUnselectedAccount(t *testing.T) {
	var form url.Values
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		form = r.Form
		w.WriteHeader(http.StatusNoContent)
	}), "collection", "create", "Kitchen remodel")
	if err != nil {
		t.Fatalf("execute create: %v", err)
	}
	if form.Has("account_id") || form.Has("collection[summary]") {
		t.Errorf("form = %v, want only the required name", form)
	}
}
