package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

func journalWithEntry() *journalView {
	v := newJournalView(testVC())
	v.Init()
	v.Update(journalDetailMsg{requestResult: currentJournalRequest(v), body: htmlutil.ToMarkdown("<p>Today was great</p>")})
	return v
}

// currentJournalRequest tags a response as the answer to the read the journal is
// waiting on, the way the fetch command that started it would.
func currentJournalRequest(v *journalView) requestResult {
	return requestResult{requestID: v.requests.id}
}

// --- Init ---

func TestJournalViewInitFetchesEntry(t *testing.T) {
	v := newJournalView(testVC())
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("Init should return a fetch command")
	}
	if !v.requests.loading {
		t.Error("Init should set loading = true")
	}
}

func TestJournalViewInitSelectsToday(t *testing.T) {
	v := newJournalView(testVC())
	v.Init()
	today := time.Now().Format("2006-01-02")
	if v.dateIndex < 0 || v.dateIndex >= len(v.dates) {
		t.Fatalf("dateIndex = %d out of range", v.dateIndex)
	}
	if v.dates[v.dateIndex] != today {
		t.Errorf("selected date = %q, want today %q", v.dates[v.dateIndex], today)
	}
}

// --- Update: message routing ---

func TestJournalTextFallbackDoesNotFetchImages(t *testing.T) {
	var imageRequests atomic.Int64
	imageData := testPNG(t)
	untrusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		imageRequests.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageData)
	}))
	t.Cleanup(untrusted.Close)

	heyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      1,
			"content": fmt.Sprintf(`<img src=%q>`, untrusted.URL+"/tracking.png"),
			"type":    "Calendar::JournalEntry",
		})
	}))
	t.Cleanup(heyServer.Close)

	client := hey.NewClient(
		&hey.Config{BaseURL: heyServer.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = client
	vc.imageFetcher = newTrustedImageFetcher(client)
	v := newJournalView(vc)

	loaded := v.fetchJournalEntry(vc.ctx, 1, "2026-08-19")().(journalDetailMsg)

	if got := imageRequests.Load(); got != 0 {
		t.Fatalf("text journal fetched an image %d time(s)", got)
	}
	if len(loaded.images) != 0 {
		t.Fatalf("text journal returned %d images", len(loaded.images))
	}
}

func TestJournalViewHandlesDetailLoaded(t *testing.T) {
	v := newJournalView(testVC())
	v.Init() // sets dateIndex to today

	_, consumed := v.Update(journalDetailMsg{requestResult: currentJournalRequest(v), body: htmlutil.ToMarkdown("<p>Entry body</p>")})
	if !consumed {
		t.Error("journalDetailMsg should be consumed")
	}
	if v.requests.loading {
		t.Error("loading should be false after detail loaded")
	}
	if !v.inThread {
		t.Error("should be in thread after detail loaded")
	}
}

func TestJournalViewIgnoresStaleResponse(t *testing.T) {
	v := newJournalView(testVC())
	v.Init()

	// A response to the read the reader has since moved off
	_, consumed := v.Update(journalDetailMsg{requestResult: requestResult{requestID: v.requests.id - 1}, body: htmlutil.ToMarkdown("<p>old content</p>")})
	if !consumed {
		t.Error("stale journalDetailMsg should still be consumed")
	}
	if !v.requests.loading {
		t.Error("loading should remain true after stale response")
	}
	if v.topicContent == "old content" {
		t.Error("stale response should not overwrite content")
	}
}

func TestJournalViewIgnoresUnrelatedMessages(t *testing.T) {
	v := newJournalView(testVC())
	_, consumed := v.Update(boxesLoadedMsg{})
	if consumed {
		t.Error("boxesLoadedMsg should not be consumed by journalView")
	}
}

// --- Content key handling ---

func TestJournalViewContentKeyScrolls(t *testing.T) {
	v := journalWithEntry()
	v.Resize(80, 30)

	// Keys should go to viewport without crashing
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress("up"))
}

// --- Subnav ---

func TestJournalViewSubnavItems(t *testing.T) {
	v := newJournalView(testVC())
	v.Init()
	items, selected, label, centered := v.SubnavItems()

	if len(items) != 30 {
		t.Errorf("expected 30 subnav items, got %d", len(items))
	}
	if selected != len(items)-1 {
		t.Errorf("selected = %d, want last item %d", selected, len(items)-1)
	}
	today := time.Now().Format("2006-01-02")
	if label != today {
		t.Errorf("label = %q, want %q", label, today)
	}
	if centered {
		t.Error("journal subnav should not be centered")
	}
}

func TestJournalViewSubnavLeftRight(t *testing.T) {
	v := newJournalView(testVC())
	v.Init()
	lastIdx := v.dateIndex

	v.SubnavLeft()
	if v.dateIndex != lastIdx-1 {
		t.Errorf("after SubnavLeft: dateIndex = %d, want %d", v.dateIndex, lastIdx-1)
	}
	if !v.requests.loading {
		t.Error("SubnavLeft should set loading")
	}

	v.SubnavRight()
	if v.dateIndex != lastIdx {
		t.Errorf("after SubnavRight: dateIndex = %d, want %d", v.dateIndex, lastIdx)
	}

	// Can't go right past the end
	v.SubnavRight()
	if v.dateIndex != lastIdx {
		t.Errorf("SubnavRight at end: dateIndex = %d, want %d", v.dateIndex, lastIdx)
	}
}

// --- Thread state ---

func TestJournalViewInThread(t *testing.T) {
	v := newJournalView(testVC())
	if v.InThread() {
		t.Error("should not be in thread initially")
	}
	v.inThread = true
	if !v.InThread() {
		t.Error("InThread should return true")
	}
	// ExitThread is a no-op for journal — content always stays visible
	v.ExitThread()
	if !v.InThread() {
		t.Error("ExitThread should be a no-op for journal")
	}
}

// --- Help bindings ---

func TestJournalViewHelpBindingsEmpty(t *testing.T) {
	v := journalWithEntry()
	bindings := v.HelpBindings()
	if len(bindings) != 0 {
		t.Errorf("journal should have no extra bindings, got %d", len(bindings))
	}
}
