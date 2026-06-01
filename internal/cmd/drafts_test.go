package cmd

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

func TestPaginateDraftsAllFollowsNextLinks(t *testing.T) {
	pages := map[string]struct {
		drafts []generated.DraftMessage
		next   string
		total  int
	}{
		"/entries/drafts.json": {
			drafts: []generated.DraftMessage{{Id: 1}, {Id: 2}},
			next:   "https://app.hey.com/entries/drafts.json?page=2",
			total:  3,
		},
		"https://app.hey.com/entries/drafts.json?page=2": {
			drafts: []generated.DraftMessage{{Id: 3}},
			total:  3,
		},
	}
	var calls []string
	fetch := func(_ context.Context, pageURL string) ([]generated.DraftMessage, string, int, error) {
		calls = append(calls, pageURL)
		page, ok := pages[pageURL]
		if !ok {
			t.Fatalf("unexpected page URL %q", pageURL)
		}
		return page.drafts, page.next, page.total, nil
	}

	drafts, total, hasMore, err := paginateDrafts(context.Background(), 0, true, fetch)
	if err != nil {
		t.Fatalf("paginateDrafts: %v", err)
	}

	if got := draftIDs(drafts); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("draft ids = %#v", got)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if hasMore {
		t.Fatal("expected hasMore=false after final page")
	}
	if !reflect.DeepEqual(calls, []string{"/entries/drafts.json", "https://app.hey.com/entries/drafts.json?page=2"}) {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestPaginateDraftsLimitStopsAfterEnoughResults(t *testing.T) {
	pages := map[string]struct {
		drafts []generated.DraftMessage
		next   string
		total  int
	}{
		"/entries/drafts.json": {
			drafts: []generated.DraftMessage{{Id: 1}, {Id: 2}},
			next:   "https://app.hey.com/entries/drafts.json?page=2",
			total:  4,
		},
		"https://app.hey.com/entries/drafts.json?page=2": {
			drafts: []generated.DraftMessage{{Id: 3}, {Id: 4}},
			total:  4,
		},
	}
	fetch := func(_ context.Context, pageURL string) ([]generated.DraftMessage, string, int, error) {
		page, ok := pages[pageURL]
		if !ok {
			t.Fatalf("unexpected page URL %q", pageURL)
		}
		return page.drafts, page.next, page.total, nil
	}

	drafts, total, hasMore, err := paginateDrafts(context.Background(), 3, false, fetch)
	if err != nil {
		t.Fatalf("paginateDrafts: %v", err)
	}

	if got := draftIDs(drafts); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("draft ids = %#v", got)
	}
	if total != 4 {
		t.Fatalf("total = %d, want 4", total)
	}
	if !hasMore {
		t.Fatal("expected hasMore=true when limit truncates fetched results")
	}
}

func TestParseNextLinkHeader(t *testing.T) {
	header := `<https://app.hey.com/entries/drafts.json?page=prev>; rel="prev", <https://app.hey.com/entries/drafts.json?page=next>; rel="next"`

	got := parseNextLinkHeader(header)

	if got != "https://app.hey.com/entries/drafts.json?page=next" {
		t.Fatalf("next link = %q", got)
	}
}

func TestFetchDraftsPageRejectsCrossOriginURL(t *testing.T) {
	originalSDK := sdk
	sdk = hey.NewClient(&hey.Config{BaseURL: "https://app.hey.com"}, nil)
	t.Cleanup(func() { sdk = originalSDK })

	_, _, _, err := fetchDraftsPage(context.Background(), "https://evil.example/entries/drafts.json")

	if err == nil {
		t.Fatal("expected cross-origin pagination URL to fail")
	}
	if !strings.Contains(err.Error(), "does not match base") {
		t.Fatalf("error = %q, want origin mismatch", err.Error())
	}
}

func TestDraftsTruncationNotice(t *testing.T) {
	tests := []struct {
		name    string
		shown   int
		total   int
		hasMore bool
		all     bool
		want    string
	}{
		{
			name:    "default page has more",
			shown:   15,
			total:   146,
			hasMore: true,
			want:    "Showing 15 of 146 drafts. Use --all to fetch all.",
		},
		{
			name:    "all capped",
			shown:   1500,
			total:   1600,
			hasMore: true,
			all:     true,
			want:    "Showing 1500 of at least 1600 drafts. Pagination limit reached; not all drafts could be fetched.",
		},
		{
			name:  "complete result",
			shown: 3,
			total: 3,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := draftsTruncationNotice(tt.shown, tt.total, tt.hasMore, tt.all)
			if got != tt.want {
				t.Fatalf("notice = %q, want %q", got, tt.want)
			}
		})
	}
}

func draftIDs(drafts []generated.DraftMessage) []int64 {
	ids := make([]int64, 0, len(drafts))
	for _, draft := range drafts {
		ids = append(ids, draft.Id)
	}
	return ids
}
