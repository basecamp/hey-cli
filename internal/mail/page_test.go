package mail

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

func testClient(t *testing.T, handler http.HandlerFunc) *hey.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
}

// Every named box orders and pages its postings its own way, so a page is read from the
// route the box is served by rather than from /boxes/{id}.
func TestReadPageStaysOnTheBoxsOwnRoute(t *testing.T) {
	routes := map[string]string{
		hey.BoxKindImbox:    "/imbox.json",
		hey.BoxKindFeed:     "/feedbox.json",
		hey.BoxKindTrail:    "/paper_trail.json",
		hey.BoxKindSetAside: "/set_aside.json",
		hey.BoxKindLater:    "/reply_later.json",
		hey.BoxKindBubbleUp: "/bubble_up.json",
		"receipts":          "/boxes/17.json",
	}

	for boxKind, wantPath := range routes {
		t.Run(boxKind, func(t *testing.T) {
			var path, cursor string
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				path, cursor = r.URL.Path, r.URL.Query().Get("page")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":17,"postings":[{"id":1}],"next_history_url":"/x.json?page=cursor-3"}`)
			})

			source := Source{Kind: KindBox, ID: 17, BoxKind: boxKind}
			page, err := ReadPage(context.Background(), client, source, "https://app.hey.com/anything.json?page=cursor-2")
			if err != nil {
				t.Fatalf("read page: %v", err)
			}
			if path != wantPath || cursor != "cursor-2" {
				t.Errorf("request = %s?page=%s, want %s?page=cursor-2", path, cursor, wantPath)
			}
			if len(page.Postings) != 1 || page.Cursor != "/x.json?page=cursor-3" {
				t.Errorf("page = %+v", page)
			}
		})
	}
}

func TestReadPageReadsTheFirstBoxPageWithoutACursor(t *testing.T) {
	var query string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"postings":[]}`)
	})

	page, err := ReadPage(context.Background(), client, Source{Kind: KindBox, ID: 1, BoxKind: hey.BoxKindImbox}, "")
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	if query != "" {
		t.Errorf("query = %q, want no page param", query)
	}
	if page.Cursor != "" || len(page.Postings) != 0 {
		t.Errorf("page = %+v", page)
	}
}

// The URL HEY hands over is never fetched — only the cursor inside it — so one that
// carries no cursor is unusable rather than a request to somewhere else.
func TestReadPageRefusesANextHistoryURLWithoutACursor(t *testing.T) {
	requests := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"postings":[]}`)
	})

	_, err := ReadPage(context.Background(), client, Source{Kind: KindBox, BoxKind: hey.BoxKindImbox}, "https://attacker.example/page-2")
	if err == nil || !strings.Contains(err.Error(), "carries no page cursor") {
		t.Fatalf("error = %v, want an unusable cursor", err)
	}
	if requests != 0 {
		t.Errorf("requests = %d, want none", requests)
	}
}

func TestReadPageReadsALabelsGearedCursor(t *testing.T) {
	var path, cursor string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path, cursor = r.URL.Path, r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "7")
		w.Header().Set("Link", fmt.Sprintf("<http://%s/folders/12.json?page=cursor-3>; rel=\"next\"", r.Host))
		_, _ = io.WriteString(w, `{"id":12,"name":"Receipts","postings":[{"id":101},{"id":102}]}`)
	})

	page, err := ReadPage(context.Background(), client, Source{Kind: KindFolder, ID: 12}, "cursor-2")
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	if path != "/folders/12.json" || cursor != "cursor-2" {
		t.Errorf("request = %s?page=%s", path, cursor)
	}
	if len(page.Postings) != 2 || page.Cursor != "cursor-3" || page.Total != 7 {
		t.Errorf("page = %+v", page)
	}
}

func TestReadPageReadsACollectionsGearedCursor(t *testing.T) {
	var path string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "3")
		_, _ = io.WriteString(w, `{"id":56,"name":"Kitchen remodel","postings":[{"id":201}]}`)
	})

	page, err := ReadPage(context.Background(), client, Source{Kind: KindCollection, ID: 56}, "")
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	if path != "/collections/56.json" {
		t.Errorf("path = %q", path)
	}
	if len(page.Postings) != 1 || page.Cursor != "" || page.Total != 3 {
		t.Errorf("page = %+v", page)
	}
}

// The seen route hands out a next_history_url naming /imbox, but its cursor belongs to
// the seen ordering: the next page is read from the seen route again, never from the box.
func TestReadSeenPageStaysOnTheSeenRoute(t *testing.T) {
	var path, cursor string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path, cursor = r.URL.Path, r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"postings":[{"id":611}],"next_history_url":"/imbox?page=seen-cursor-3"}`)
	})

	page, err := ReadSeenPage(context.Background(), client, "https://app.hey.com/imbox?page=seen-cursor-2")
	if err != nil {
		t.Fatalf("read seen page: %v", err)
	}
	if path != "/imbox/seen.json" || cursor != "seen-cursor-2" {
		t.Errorf("request = %s?page=%s, want /imbox/seen.json?page=seen-cursor-2", path, cursor)
	}
	if len(page.Postings) != 1 || page.Cursor != "/imbox?page=seen-cursor-3" {
		t.Errorf("page = %+v", page)
	}
}

func TestReadSeenPageReadsTheFirstPageWithoutACursor(t *testing.T) {
	var query string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"postings":[]}`)
	})

	page, err := ReadSeenPage(context.Background(), client, "")
	if err != nil {
		t.Fatalf("read seen page: %v", err)
	}
	if query != "" {
		t.Errorf("query = %q, want no page param", query)
	}
	if page.Cursor != "" || len(page.Postings) != 0 {
		t.Errorf("page = %+v", page)
	}
}

func TestReadSeenPageRefusesACursorlessURL(t *testing.T) {
	requests := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"postings":[]}`)
	})

	_, err := ReadSeenPage(context.Background(), client, "https://attacker.example/page-2")
	if err == nil || !strings.Contains(err.Error(), "carries no page cursor") {
		t.Fatalf("error = %v, want an unusable cursor", err)
	}
	if requests != 0 {
		t.Errorf("requests = %d, want none", requests)
	}
}

// A kind nobody taught this package about is a bug, not a box.
func TestReadPageRefusesAnUnknownKind(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})

	_, err := ReadPage(context.Background(), client, Source{Kind: "topic", ID: 5}, "")
	if err == nil || !strings.Contains(err.Error(), `no readable kind "topic"`) {
		t.Fatalf("error = %v, want a refusal", err)
	}
}

func TestReadPageReportsAFailedRead(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	if _, err := ReadPage(context.Background(), client, Source{Kind: KindFolder, ID: 12}, ""); err == nil {
		t.Fatal("expected the server failure to surface")
	}
}
