package mail

import (
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

func TestNewPostingKeepsWhatARowShows(t *testing.T) {
	created := time.Date(2026, 8, 20, 23, 30, 0, 0, time.UTC)

	posting := NewPosting(generated.Posting{
		Id:                    4471829,
		AppUrl:                "https://app.hey.com/topics/501",
		CreatedAt:             created,
		Kind:                  "topic",
		Name:                  "Kitchen remodel quote",
		Summary:               "Here is the revised quote for the cabinets",
		AlternativeSenderName: "Ryan at Fine Woodwork",
		Seen:                  true,
		Bundled:               true,
		BubbledUp:             true,
		Muted:                 true,
		VisibleEntryCount:     3,
		Creator:               generated.Contact{Id: 88, Name: "Ryan Singer", EmailAddress: "ryan@example.com"},
		Extenzions:            []generated.Extenzion{{Id: 7, Name: "remodel"}},
		Folders:               []generated.Folder{{Id: 12, Name: "Receipts"}},
		Collections:           []generated.Collection{{Id: 34, Name: "Kitchen remodel"}},
	})

	if !posting.CreatedAt.Equal(created) {
		t.Errorf("created at = %s, want %s", posting.CreatedAt, created)
	}
	if posting.ID != 4471829 || posting.TopicID != 501 || posting.Name != "Kitchen remodel quote" {
		t.Errorf("posting = %+v", posting)
	}
	if !posting.Seen || !posting.Bundled || !posting.BubbledUp || !posting.Muted || posting.VisibleEntryCount != 3 {
		t.Errorf("posting state = %+v", posting)
	}
	if posting.Summary != "Here is the revised quote for the cabinets" || posting.AlternativeSenderName != "Ryan at Fine Woodwork" {
		t.Errorf("posting text = %+v", posting)
	}
	if posting.Creator != (Contact{ID: 88, Name: "Ryan Singer", EmailAddress: "ryan@example.com"}) {
		t.Errorf("creator = %+v", posting.Creator)
	}
	if len(posting.Extenzions) != 1 || posting.Extenzions[0] != (Extenzion{ID: 7, Name: "remodel"}) {
		t.Errorf("extenzions = %+v", posting.Extenzions)
	}
	if len(posting.Folders) != 1 || posting.Folders[0] != (Folder{ID: 12, Name: "Receipts"}) {
		t.Errorf("folders = %+v", posting.Folders)
	}
	if len(posting.Collections) != 1 || posting.Collections[0] != (Collection{ID: 34, Name: "Kitchen remodel"}) {
		t.Errorf("collections = %+v", posting.Collections)
	}
}

// The zone HEY served is the reader's own, and it decides which day a late-evening thread
// falls on. Normalizing it away is the bug that made `hey journal list` print yesterday.
func TestNewPostingKeepsTheZoneHEYServed(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*3600)
	created := time.Date(2026, 8, 21, 1, 30, 0, 0, berlin)

	posting := NewPosting(generated.Posting{Id: 1, CreatedAt: created})

	if posting.CreatedAt.Format(time.RFC3339) != created.Format(time.RFC3339) {
		t.Errorf("created at = %s, want %s", posting.CreatedAt.Format(time.RFC3339), created.Format(time.RFC3339))
	}
	if day := posting.CreatedAt.Format("2006-01-02"); day != "2026-08-21" {
		t.Errorf("day = %s, want 2026-08-21", day)
	}
}

// HEY's posting JSON carries no topic, so the thread is read out of app_url — and a
// posting that addresses something else has no thread to find.
func TestNewPostingReadsTheThreadOutOfTheAppURL(t *testing.T) {
	tests := map[string]int64{
		"https://app.hey.com/topics/1943585351":         1943585351,
		"https://app.hey.com/topics/501/replies/new":    501,
		"https://app.hey.com/topics/501?entry_id=77":    501,
		"https://app.hey.com/contacts/88":               0,
		"https://app.hey.com/topics/not-a-number":       0,
		"https://app.hey.com/bundles/12/topics/700/see": 700,
		"": 0,
	}

	for appURL, want := range tests {
		if got := NewPosting(generated.Posting{AppUrl: appURL}).TopicID; got != want {
			t.Errorf("topic ID in %q = %d, want %d", appURL, got, want)
		}
	}
}

// A bundle's app_url is its sender's contact page, but HEY points app_bundle_url at a
// topic when the bundle holds one unseen thread — the thread the row opens in the web
// app. That is the only URL such a posting names its thread in.
func TestTopicIDOfFallsBackToTheBundleURL(t *testing.T) {
	tests := map[string]struct {
		posting generated.Posting
		want    int64
	}{
		"app_url wins when both name a topic": {
			posting: generated.Posting{
				AppUrl:       "https://app.hey.com/topics/501",
				AppBundleUrl: "https://app.hey.com/topics/700",
			},
			want: 501,
		},
		"a bundle around one unseen thread names it in app_bundle_url": {
			posting: generated.Posting{
				AppUrl:       "https://app.hey.com/contacts/88",
				AppBundleUrl: "https://app.hey.com/topics/700#entry_1_9",
			},
			want: 700,
		},
		"a bundle of several unseen threads names no topic": {
			posting: generated.Posting{
				AppUrl:       "https://app.hey.com/contacts/88",
				AppBundleUrl: "https://app.hey.com/postings/bundles/12/unseen",
			},
			want: 0,
		},
		"a fully seen bundle names its contact twice": {
			posting: generated.Posting{
				AppUrl:       "https://app.hey.com/contacts/88",
				AppBundleUrl: "https://app.hey.com/contacts/88",
			},
			want: 0,
		},
	}

	for name, test := range tests {
		if got := TopicIDOf(test.posting); got != test.want {
			t.Errorf("%s: topic ID = %d, want %d", name, got, test.want)
		}
		if described := NewPosting(test.posting); described.TopicID != test.want {
			t.Errorf("%s: NewPosting topic ID = %d, want %d", name, described.TopicID, test.want)
		}
	}
}

func TestPostingsDescribesAPage(t *testing.T) {
	described := Postings([]generated.Posting{{Id: 100}, {Id: 101, AppUrl: "https://app.hey.com/topics/9"}})

	if len(described) != 2 || described[0].ID != 100 || described[1].TopicID != 9 {
		t.Errorf("described = %+v", described)
	}
	if Postings(nil) == nil {
		t.Error("an empty page should describe as an empty slice, not nil")
	}
}
