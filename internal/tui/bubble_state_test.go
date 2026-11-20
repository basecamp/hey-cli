package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/mail"
)

func TestBubbleSearchResolvesRealMetadata(t *testing.T) {
	for _, state := range []string{"ordinary", "scheduled", "bubbled"} {
		for _, inThread := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/thread=%v", state, inThread), func(t *testing.T) {
				var reads []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method != http.MethodGet && r.URL.Path != "/postings/seen.json" && r.URL.Path != "/postings/bubble_up.json" {
						t.Errorf("lookup mutated mail: %s", r.Method)
					}
					reads = append(reads, r.URL.RequestURI())
					switch r.URL.Path {
					case "/advanced_search.json":
						fmt.Fprint(w, `{"matches":[{"posting_id":987,"topic":{"id":654,"name":"Hotel newsletter"}}]}`)
					case "/topics/654.json":
						fmt.Fprint(w, `{"id":654,"entries":[{"id":765,"kind":"message","summary":"Hotel newsletter"}]}`)
					case "/topics/654/entries.json":
						fmt.Fprint(w, `[{"id":765,"kind":"message","summary":"Hotel newsletter"}]`)
					case "/messages/765.json":
						fmt.Fprint(w, `{"id":765,"content":"<p>Hotel newsletter</p>"}`)
					case "/postings/bubble_up.json":
						if r.Method != http.MethodDelete || r.URL.Query().Get("posting_ids") != "987" {
							t.Errorf("cancel targeted the wrong resource: %s %s", r.Method, r.URL)
						}
						w.WriteHeader(http.StatusOK)
					case "/postings/seen.json":
						w.WriteHeader(http.StatusOK)
					case "/imbox.json":
						if state == "bubbled" {
							fmt.Fprint(w, `{"postings":[{"id":987,"bubbled_up":true}]}`)
						} else {
							// The ordinary prefix ends here: do not follow this cursor.
							fmt.Fprint(w, `{"postings":[{"id":456}],"next_history_url":"https://app.hey.com/imbox?page=unneeded"}`)
						}
					case "/bubble_up.json":
						if state == "scheduled" {
							if r.URL.Query().Get("page") == "next / opaque" {
								fmt.Fprint(w, `{"postings":[{"id":987}]}`)
							} else {
								fmt.Fprint(w, `{"postings":[{"id":456}],"next_history_url":"https://app.hey.com/bubble_up?page=next+%2F+opaque"}`)
							}
						} else {
							fmt.Fprint(w, `{"postings":[]}`)
						}
					default:
						t.Errorf("unexpected read %s", r.URL)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				v := mailWithPostings()
				v.vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
				v.postingList.toggleSelected()
				v.HandleContentKey(keyPress("s"))
				modalOf[*mailSearchForm](v).input.SetValue("newsletter")
				deliverToView(v, v.HandleContentKey(keyPress("enter")))
				if !v.searchActive || len(v.searchList.postings) != 1 {
					t.Fatal("real search response did not open results")
				}
				if inThread {
					next, _ := v.Update(v.openSelected()())
					deliverToView(v, next)
					if !v.inThread || v.threadPosting.ID != 987 {
						t.Fatal("search result did not open its thread")
					}
				}
				cmd := v.HandleContentKey(keyPress("w"))
				p := modalOf[*bubblePicker](v)
				if cmd == nil || p == nil || !p.loading || p.postingID != 987 {
					t.Fatal("hidden selection blocked the visible search target")
				}
				if v.pendingMutations != 0 {
					t.Fatal("lookup counted as a mutation")
				}
				v.Update(cmd())
				if p.loading || p.unknownState || p.problem != "" {
					t.Fatalf("lookup did not finish: %+v", p)
				}
				hasCancel := p.choices[len(p.choices)-1].action == bubbleCancel
				if hasCancel != (state != "ordinary") {
					t.Fatalf("cancel=%v for %s; reads=%v", hasCancel, state, reads)
				}
				if len(v.postingList.selectedIDs()) != 1 {
					t.Fatal("lookup cleared the hidden inbox selection")
				}
				if hasCancel {
					p.cursor = len(p.choices) - 1
					cmd := v.HandleContentKey(keyPress("enter"))
					if cmd == nil {
						t.Fatal("cancel was not dispatched")
					}
					if msg := cmd().(bubbleDoneMsg); msg.err != nil {
						t.Fatal(msg.err)
					}
				}
				for _, read := range reads {
					if strings.Contains(read, "unneeded") {
						t.Fatal("scanned ordinary Imbox mail")
					}
				}
			})
		}
	}
}

func TestBubbleBundleIgnoresHiddenSelection(t *testing.T) {
	v := mailWithPostings()
	v.postingList.toggleSelected()
	v.bundleActive = true
	v.bundleList.setPostings([]mail.Posting{{ID: 987, BoxID: 1}})
	v.HandleContentKey(keyPress("w"))
	if p := modalOf[*bubblePicker](v); p == nil || p.postingID != 987 {
		t.Fatal("hidden inbox selection blocked bundle thread")
	}
}

func TestBubbleLookupFailureRetryAndDismissal(t *testing.T) {
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"postings":[{"id":987,"bubbled_up":true}]}`)
	}))
	defer server.Close()
	v := mailWithPostings()
	v.vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	p := newBubblePicker(mail.Posting{ID: 987}, false, bubbleClock)
	v.openModal(p)
	v.Update(v.loadBubbleState(p)())
	if p.loading || !p.unknownState || p.problem == "" {
		t.Fatal("failure silently hid cancellation")
	}
	if cmd := v.HandleContentKey(keyPress("enter")); cmd != nil || v.pendingMutations != 0 {
		t.Fatal("unknown state permitted mutation")
	}
	fail = false
	deliverToView(v, v.HandleContentKey(keyPress("ctrl+r")))
	if p.unknownState || p.choices[len(p.choices)-1].action != bubbleCancel {
		t.Fatal("retry did not recover cancellation")
	}
	cmd := v.loadBubbleState(p)
	v.HandleContentKey(keyPress("esc"))
	if v.modal != nil {
		t.Fatal("Escape did not dismiss loading picker")
	}
	replacement := newBubblePicker(mail.Posting{ID: 456}, false, bubbleClock)
	v.openModal(replacement)
	msg := cmd().(bubbleStateLoadedMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("lookup was not canceled: %v", msg.err)
	}
	v.Update(msg)
	v.Update(bubbleStateLoadedMsg{picker: p, canCancel: true})
	if v.modal != replacement || replacement.choices[len(replacement.choices)-1].action == bubbleCancel {
		t.Fatal("stale result affected replacement picker")
	}
}

func TestBubbleLookupBounds(t *testing.T) {
	for _, mode := range []string{"cycle", "page cap", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				calls++
				cursor := "repeated"
				if mode == "page cap" {
					cursor = fmt.Sprint(calls)
				}
				json.NewEncoder(w).Encode(map[string]any{"postings": []map[string]any{{"id": 456, "bubbled_up": true}}, "next_history_url": "https://app.hey.com/imbox?page=" + cursor})
			}))
			defer server.Close()
			sdk := hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			found, err := readBubbleState(ctx, sdk, 987)
			if found || err == nil {
				t.Fatal("incomplete read reported a known state")
			}
			want := 2
			if mode == "page cap" {
				want = maxBubbleLookupPages
			}
			if mode == "canceled" {
				want = 0
			}
			if calls != want {
				t.Fatalf("read %d pages, want %d", calls, want)
			}
		})
	}
}
