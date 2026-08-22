package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

func journalWithEntry() *journalView {
	v := newJournalView(testVC())
	v.Init()
	v.Update(journalDetailMsg{
		requestResult: currentJournalRequest(v),
		content:       "<p>Today was great</p>",
		body:          htmlutil.ToMarkdown("<p>Today was great</p>"),
	})
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

func TestJournalFetchKeepsRichContentForEditing(t *testing.T) {
	heyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           1,
			"content":      "Today was great",
			"content_html": `<div class="trix-content"><p><strong>Today</strong> was great</p></div>`,
			"type":         "Calendar::JournalEntry",
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
	v := newJournalView(vc)

	loaded := v.fetchJournalEntry(vc.ctx, 1, "2026-08-19")().(journalDetailMsg)

	if loaded.content != "<p><strong>Today</strong> was great</p>" {
		t.Errorf("editable content = %q", loaded.content)
	}
	if loaded.body.IsEmpty() {
		t.Error("rich-text body should be rendered")
	}
}

func TestJournalEditorContentRemovesTrixContainers(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "read wrapper",
			content: `<div class="trix-content"><p>Today was great</p></div>`,
			want:    `<p>Today was great</p>`,
		},
		{
			name: "wrappers stored by earlier saves",
			content: `<div class="trix-content">
  <div class="trix-content">
    <div>Today was great</div>
  </div>
  and then some
</div>`,
			want: "<div>Today was great</div>\n  \n  and then some",
		},
		{
			name:    "attachment bytes",
			content: `<div class="trix-content"><figure data-trix-attachment="{&quot;sgid&quot;:&quot;abc&quot;}"></figure></div>`,
			want:    `<figure data-trix-attachment="{&quot;sgid&quot;:&quot;abc&quot;}"></figure>`,
		},
		{
			name:    "ordinary div",
			content: `<div class="note"><strong>Keep me</strong></div>`,
			want:    `<div class="note"><strong>Keep me</strong></div>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := journalEditorContent(tt.content); got != tt.want {
				t.Errorf("journalEditorContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJournalFailedReadDoesNotOpenEditor(t *testing.T) {
	heyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	t.Cleanup(heyServer.Close)

	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(
		&hey.Config{BaseURL: heyServer.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	v := newJournalView(vc)
	cmd := v.Init()

	errCmd, consumed := v.Update(cmd())
	if !consumed || errCmd == nil {
		t.Fatalf("failed read = consumed:%v error command:%v", consumed, errCmd != nil)
	}
	if v.inThread || v.editContent != "" {
		t.Fatalf("failed read state = open:%v content:%q", v.inThread, v.editContent)
	}
	if cmd := v.HandleContentKey(keyPress("e")); cmd != nil || v.form != nil {
		t.Fatalf("edit after failed read = cmd:%v form:%v", cmd != nil, v.form != nil)
	}
}

func TestJournalPlainTextFallbackStaysLiteral(t *testing.T) {
	var imageRequests atomic.Int64
	imageData := testPNG(t)
	literal := `<img src="/rails/active_storage/blobs/redirect/tracking.png">`
	heyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/rails/active_storage/blobs/") {
			imageRequests.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageData)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      1,
			"content": literal,
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
	vc.imageRenderer = kittyImageRenderer{}
	vc.imageFetcher = newTrustedImageFetcher(client)
	v := newJournalView(vc)

	loaded := v.fetchJournalEntry(vc.ctx, 1, "2026-08-19")().(journalDetailMsg)

	if loaded.content != literal {
		t.Errorf("editable content = %q, want literal %q", loaded.content, literal)
	}
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

	_, consumed := v.Update(journalDetailMsg{
		requestResult: currentJournalRequest(v),
		content:       "Entry body",
		body:          htmlutil.ToMarkdown("<p>Entry body</p>"),
	})
	if !consumed {
		t.Error("journalDetailMsg should be consumed")
	}
	if v.requests.loading {
		t.Error("loading should be false after detail loaded")
	}
	if !v.inThread {
		t.Error("should be in thread after detail loaded")
	}
	if v.editContent != "Entry body" {
		t.Errorf("editable content = %q", v.editContent)
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

func TestJournalViewEditsSelectedDay(t *testing.T) {
	v := journalWithEntry()
	v.Resize(80, 30)

	if cmd := v.HandleContentKey(keyPress("e")); cmd == nil {
		t.Fatal("edit should focus the journal form")
	}
	if !v.CapturingInput() {
		t.Fatal("journal form should capture input")
	}
	if got := v.form.input.Value(); got != "<p>Today was great</p>" {
		t.Errorf("form content = %q", got)
	}
	if got := v.form.date; got != v.dates[v.dateIndex] {
		t.Errorf("form date = %q, want %q", got, v.dates[v.dateIndex])
	}

	v.HandleContentKey(keyPress("esc"))
	if v.form != nil || v.CapturingInput() {
		t.Fatal("escape should cancel the journal form")
	}
}

func TestJournalViewSavesAndRemovesEntries(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantContent string
		wantNotice  string
	}{
		{name: "save", input: "  A better day\n", wantContent: "A better day", wantNotice: "Journal entry saved"},
		{
			name:        "preserve rich content",
			input:       `<div><strong>Great</strong></div><figure data-trix-attachment="{&quot;sgid&quot;:&quot;abc&quot;}"></figure>`,
			wantContent: `<div><strong>Great</strong></div><figure data-trix-attachment="{&quot;sgid&quot;:&quot;abc&quot;}"></figure>`,
			wantNotice:  "Journal entry saved",
		},
		{name: "remove", input: " \n ", wantContent: "", wantNotice: "Journal entry removed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotContent string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPatch {
					t.Fatalf("method = %s, want PATCH", r.Method)
				}
				var payload struct {
					CalendarJournalEntry struct {
						Content string `json:"content"`
					} `json:"calendar_journal_entry"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				gotContent = payload.CalendarJournalEntry.Content
				if gotContent == "" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":           1,
					"content":      gotContent,
					"content_html": "<p>" + gotContent + "</p>",
					"type":         "Calendar::JournalEntry",
				})
			}))
			t.Cleanup(server.Close)

			vc := testVC()
			vc.ctx = context.Background()
			vc.sdk = hey.NewClient(
				&hey.Config{BaseURL: server.URL},
				&hey.StaticTokenProvider{Token: "test-token"},
				hey.WithMaxRetries(0),
			)
			v := newJournalView(vc)
			v.Init()
			v.Update(journalDetailMsg{requestResult: currentJournalRequest(v)})
			v.HandleContentKey(keyPress("e"))
			v.form.input.SetValue(tt.input)

			cmd := v.HandleContentKey(keyPress("ctrl+s"))
			if cmd == nil || !v.form.saving || !v.AccountSwitchBlocked() {
				t.Fatalf("save state = cmd:%v saving:%v blocked:%v", cmd != nil, v.form.saving, v.AccountSwitchBlocked())
			}
			refresh, consumed := v.Update(cmd())
			if !consumed || refresh == nil {
				t.Fatalf("saved message = consumed:%v refresh:%v", consumed, refresh != nil)
			}
			if gotContent != tt.wantContent {
				t.Errorf("saved content = %q, want %q", gotContent, tt.wantContent)
			}
			if v.form != nil || v.notice != tt.wantNotice {
				t.Errorf("saved state = form:%v notice:%q", v.form != nil, v.notice)
			}
			if got, want := v.topicViewport.Height(), v.vc.height-1; got != want {
				t.Errorf("viewport height with notice = %d, want %d", got, want)
			}
			if v.requests.kind != journalRequestEntry {
				t.Errorf("request kind = %v, want refresh", v.requests.kind)
			}
		})
	}
}

func TestJournalRepeatedSavesDoNotAddTrixWrapper(t *testing.T) {
	stored := `<div><strong>Today</strong> was great</div><figure data-trix-attachment="{&quot;sgid&quot;:&quot;abc&quot;}"></figure>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           1,
				"content":      "Today was great",
				"content_html": `<div class="trix-content">` + stored + `</div>`,
				"type":         "Calendar::JournalEntry",
			})
		case http.MethodPatch:
			var payload struct {
				CalendarJournalEntry struct {
					Content string `json:"content"`
				} `json:"calendar_journal_entry"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			stored = payload.CalendarJournalEntry.Content
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           1,
				"content":      "Today was great",
				"content_html": `<div class="trix-content">` + stored + `</div>`,
				"type":         "Calendar::JournalEntry",
			})
		default:
			t.Fatalf("method = %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(
		&hey.Config{BaseURL: server.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	v := newJournalView(vc)
	load := v.Init()
	v.Update(load())

	want := stored
	for range 3 {
		v.HandleContentKey(keyPress("e"))
		save := v.HandleContentKey(keyPress("ctrl+s"))
		refresh, _ := v.Update(save())
		v.Update(refresh())
	}

	if stored != want {
		t.Errorf("stored content after repeated saves = %q, want %q", stored, want)
	}
	if strings.Contains(stored, "trix-content") {
		t.Errorf("stored content contains a Trix wrapper: %q", stored)
	}
}

func TestJournalViewKeepsEditorOnSaveFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	vc := testVC()
	vc.ctx = context.Background()
	vc.sdk = hey.NewClient(
		&hey.Config{BaseURL: server.URL},
		&hey.StaticTokenProvider{Token: "test-token"},
		hey.WithMaxRetries(0),
	)
	v := newJournalView(vc)
	v.Init()
	v.Update(journalDetailMsg{requestResult: currentJournalRequest(v)})
	v.HandleContentKey(keyPress("e"))
	v.form.input.SetValue("Keep this draft")

	cmd := v.HandleContentKey(keyPress("ctrl+s"))
	v.Update(cmd())

	if v.form == nil || v.form.saving {
		t.Fatalf("failed save state = form:%v saving:%v", v.form != nil, v.form != nil && v.form.saving)
	}
	if v.form.input.Value() != "Keep this draft" {
		t.Errorf("draft = %q", v.form.input.Value())
	}
	if !v.form.isError || v.form.status == "" {
		t.Errorf("failure status = error:%v status:%q", v.form.isError, v.form.status)
	}
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

func TestJournalViewHelpBindings(t *testing.T) {
	v := journalWithEntry()
	bindings := v.HelpBindings()
	if len(bindings) != 1 || bindings[0] != (helpBinding{"e", "edit"}) {
		t.Errorf("journal bindings = %#v", bindings)
	}

	v.HandleContentKey(keyPress("e"))
	bindings = v.HelpBindings()
	want := []helpBinding{{"ctrl+s", "save"}, {"esc", "cancel"}}
	if len(bindings) != len(want) {
		t.Fatalf("form bindings = %#v", bindings)
	}
	for i := range want {
		if bindings[i] != want[i] {
			t.Errorf("form binding %d = %#v, want %#v", i, bindings[i], want[i])
		}
	}
}
