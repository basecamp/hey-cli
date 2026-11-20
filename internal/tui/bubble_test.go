package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/mail"
)

func bubbleClock() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }

func TestBubblePickerOpensFromListAndThread(t *testing.T) {
	for _, inThread := range []bool{false, true} {
		v := mailWithPostings()
		v.inThread = inThread
		if inThread {
			v.threadPosting = v.postingList.postings[1]
		}
		v.HandleContentKey(keyPress("w"))
		p := modalOf[*bubblePicker](v)
		if p == nil {
			t.Fatal("w did not open the picker")
		}
		want := v.postingList.postings[0].ID
		if inThread {
			want = v.threadPosting.ID
		}
		if p.postingID != want {
			t.Fatalf("picker posting = %d, want %d", p.postingID, want)
		}
		if screen := v.View(); !strings.Contains(plainText(screen), "Bubble up") {
			t.Fatal("picker not rendered")
		}
		v.HandleContentKey(keyPress("esc"))
		if v.modal != nil {
			t.Fatal("Escape did not close the picker")
		}
	}
}

func TestBubblePickerRefusesBundlesAndSelections(t *testing.T) {
	v := mailWithPostings()
	v.postingList.postings[0].IsBundle = true
	v.HandleContentKey(keyPress("w"))
	if v.modal != nil {
		t.Fatal("offered bubble-up for a bundle")
	}
	v.postingList.postings[0].IsBundle = false
	v.postingList.toggleSelected()
	v.HandleContentKey(keyPress("w"))
	if v.modal != nil {
		t.Fatal("offered single-thread action for a selection")
	}
}

func TestBubblePresetsAndCancellation(t *testing.T) {
	p := newBubblePicker(mail.Posting{ID: 123}, false, bubbleClock)
	wants := map[string]string{"Later today": "2026-11-20T18:00:00Z", "Tomorrow": "2026-11-21T08:00:00Z", "This weekend": "2026-11-21T08:00:00Z", "Next week": "2026-11-23T08:00:00Z"}
	for _, c := range p.choices {
		if want, ok := wants[c.name]; ok {
			if c.at.Format(time.RFC3339) != want {
				t.Errorf("%s at %s, want %s", c.name, c.at, want)
			}
			delete(wants, c.name)
		}
		if c.name == "Cancel bubble-up" {
			t.Error("offered cancellation for unscheduled mail")
		}
	}
	if len(wants) != 0 {
		t.Fatalf("missing choices %v", wants)
	}
	for _, post := range []mail.Posting{{ID: 123, BubbledUp: true}, {ID: 123, BubbleScheduled: true}} {
		p = newBubblePicker(post, post.BubbleScheduled, bubbleClock)
		if p.choices[len(p.choices)-1].name != "Cancel bubble-up" {
			t.Fatal("missing cancellation")
		}
	}
	late := newBubblePicker(mail.Posting{ID: 123}, false, func() time.Time { return bubbleClock().Add(8 * time.Hour) })
	for _, c := range late.choices {
		if c.name == "Later today" {
			t.Fatal("offered past slot")
		}
	}
	if nextBubbleDay(time.Saturday, time.Saturday) != 7 || nextBubbleDay(time.Monday, time.Monday) != 7 {
		t.Fatal("next weekday should be in the future")
	}
}

func TestBubbleRequestsAndFailureRetry(t *testing.T) {
	for _, name := range []string{"Now", "Later today", "Tomorrow", "This weekend", "Next week", "Choose a date…", "Cancel bubble-up"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			status := http.StatusUnprocessableEntity
			var path, method string
			var body struct {
				PostingIDs []int64 `json:"posting_ids"`
				Slot       string  `json:"slot"`
				Date       string  `json:"date"`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				path = r.URL.Path
				method = r.Method
				if r.Method == http.MethodPost {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			v := mailWithPostings()
			v.vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
			p := newBubblePicker(mail.Posting{ID: 123}, true, bubbleClock)
			v.openModal(p)
			for i, c := range p.choices {
				if c.name == name {
					p.cursor = i
				}
			}
			if name == "Choose a date…" {
				v.HandleContentKey(keyPress("enter"))
				p.date.dateInput.SetValue("2026-12-04")
			}
			cmd := v.HandleContentKey(keyPress("enter"))
			if cmd == nil || !p.busy || v.pendingMutations != 1 {
				t.Fatal("no pending request")
			}
			if duplicate := v.HandleContentKey(keyPress("enter")); duplicate != nil {
				t.Fatal("duplicate dispatch")
			}
			v.Update(cmd())
			if calls != 1 || v.modal != p || p.problem == "" || p.busy || v.pendingMutations != 0 {
				t.Fatal("failure did not preserve retryable picker")
			}
			if name == "Cancel bubble-up" {
				if method != http.MethodDelete || !strings.HasPrefix(path, "/postings/bubble_up") {
					t.Fatalf("cancel request = %s %s", method, path)
				}
			} else {
				if len(body.PostingIDs) != 1 || body.PostingIDs[0] != 123 {
					t.Fatalf("wrong target: %+v", body)
				}
				if name == "Now" && path != "/postings/bulk_bubble_up_now.json" {
					t.Fatal(path)
				}
				if name == "Choose a date…" && (body.Date != "2026-12-04" || body.Slot != "custom") {
					t.Fatalf("custom payload = %+v", body)
				}
				slots := map[string]string{"Later today": "today", "Tomorrow": "tomorrow", "This weekend": "weekend", "Next week": "next_week"}
				if want, ok := slots[name]; ok && body.Slot != want {
					t.Fatalf("slot = %s want %s", body.Slot, want)
				}
			}
			status = http.StatusOK
			cmd = v.HandleContentKey(keyPress("enter"))
			refresh, _ := v.Update(cmd())
			if calls != 2 || v.modal != nil || v.pendingMutations != 0 || refresh == nil {
				t.Fatal("success did not close picker and refresh")
			}
		})
	}
}

func TestBubbleCustomDateValidationAndBack(t *testing.T) {
	v := mailWithPostings()
	p := newBubblePicker(mail.Posting{ID: 123}, false, bubbleClock)
	v.openModal(p)
	for i, c := range p.choices {
		if c.name == "Choose a date…" {
			p.cursor = i
		}
	}
	v.HandleContentKey(keyPress("enter"))
	for _, date := range []string{"2026-02-30", "2026-11-19", "not a date"} {
		p.date.dateInput.SetValue(date)
		if cmd := v.HandleContentKey(keyPress("enter")); cmd != nil || p.problem == "" || p.busy {
			t.Fatalf("accepted invalid date %s", date)
		}
	}
	v.HandleContentKey(keyPress("esc"))
	if v.modal != p || p.date != nil {
		t.Fatal("Escape should return to preset menu")
	}
	p.now = func() time.Time { return bubbleClock().AddDate(0, 0, 1) }
	p.cursor = 1
	if cmd := v.HandleContentKey(keyPress("enter")); cmd != nil || !strings.Contains(p.problem, "date changed") {
		t.Fatal("stale preset accepted after midnight")
	}
}

func TestBubblePickerUsesOverlayRowAndScheduledMetadata(t *testing.T) {
	for _, source := range []string{"search", "bundle", "seen"} {
		t.Run(source, func(t *testing.T) {
			v := mailWithPostings()
			rows := []mail.Posting{{ID: 987, BubbleScheduled: true}}
			switch source {
			case "search":
				v.searchActive = true
				v.searchList.setPostings(rows)
			case "bundle":
				v.bundleActive = true
				v.bundleList.setPostings(rows)
			case "seen":
				v.seenActive = true
				v.seenList.setPostings(rows)
			}
			v.HandleContentKey(keyPress("w"))
			p := modalOf[*bubblePicker](v)
			if p == nil || p.postingID != 987 || p.choices[len(p.choices)-1].action != bubbleCancel {
				t.Fatalf("%s picker ignored its own row", source)
			}
		})
	}
}

func TestBubbleCustomTodayUsesEveningSlot(t *testing.T) {
	var slot, date string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Slot string `json:"slot"`
			Date string `json:"date"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		slot, date = body.Slot, body.Date
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	v := mailWithPostings()
	v.vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	p := newBubblePicker(mail.Posting{ID: 123}, false, bubbleClock)
	p.date = newDateTimePicker(bubbleClock(), true)
	cmd := p.submit(v, bubbleChoice{action: bubbleChooseDate})
	if cmd == nil {
		t.Fatal("today's future evening slot was refused")
	}
	v.Update(cmd())
	if slot != "today" || date != "" {
		t.Fatalf("today request slot=%q date=%q", slot, date)
	}
	p.now = func() time.Time { return bubbleClock().Add(8 * time.Hour) }
	if cmd := p.submit(v, bubbleChoice{action: bubbleChooseDate}); cmd != nil {
		t.Fatal("accepted today's elapsed evening slot")
	}
}
