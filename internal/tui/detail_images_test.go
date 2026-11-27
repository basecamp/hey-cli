package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

const inlineImageTestPath = "/rails/active_storage/blobs/redirect/signed/photo.png"

var inlineImageTestBody = "<p>Readable before the photo</p>" + strings.Repeat("<p>More text to scroll</p>", 50) + `<img src="` + inlineImageTestPath + `">`

type inlineImageGate struct {
	started  chan struct{}
	released chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newInlineImageGate(t *testing.T) *inlineImageGate {
	t.Helper()
	return &inlineImageGate{started: make(chan struct{}, 1), released: make(chan struct{}), canceled: make(chan struct{}, 1)}
}

func (g *inlineImageGate) release() { g.once.Do(func() { close(g.released) }) }

func (g *inlineImageGate) await(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func inlineImageTestContext(t *testing.T, gate *inlineImageGate, serveData http.HandlerFunc) *viewContext {
	t.Helper()
	imageData := testPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != inlineImageTestPath {
			w.Header().Set("Content-Type", "application/json")
			serveData(w, r)
			return
		}
		select {
		case gate.started <- struct{}{}:
		default:
		}
		select {
		case <-gate.released:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageData)
		case <-r.Context().Done():
			select {
			case gate.canceled <- struct{}{}:
			default:
			}
		}
	}))
	t.Cleanup(func() { gate.release(); server.Close() })
	vc := testVC()
	vc.sdk = hey.NewClient(&hey.Config{BaseURL: server.URL}, &hey.StaticTokenProvider{Token: "test-token"}, hey.WithMaxRetries(0))
	vc.imageFetcher = newTrustedImageFetcher(vc.sdk)
	vc.imageRenderer = kittyImageRenderer{}
	return vc
}

func runInlineImageCommand(cmd tea.Cmd) <-chan tea.Msg {
	done := make(chan tea.Msg, 1)
	go func() { done <- runCmd(cmd) }()
	return done
}

func awaitInlineImageMessage(t *testing.T, done <-chan tea.Msg, what string) tea.Msg {
	t.Helper()
	select {
	case msg := <-done:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

func serveInlineImageMail(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/topics/100/entries.json":
		_, _ = w.Write([]byte(`[{"id":501,"kind":"message"}]`))
	case "/messages/501.json":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 501, "content": inlineImageTestBody})
	default:
		http.NotFound(w, r)
	}
}

func serveInlineImageJournal(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": 1, "content_html": inlineImageTestBody, "type": "Calendar::JournalEntry",
	})
}

func TestDetailImagesCancelWhenNavigatingAway(t *testing.T) {
	for _, tc := range []struct {
		name    string
		section section
		leave   func(model)
	}{
		{"MailExit", sectionMail, func(m model) { m.mailView.ExitThread() }},
		{"MailBox", sectionMail, func(m model) { m.mailView.switchBox(m.mailView.boxIndex) }},
		{"MailPreviouslySeen", sectionMail, func(m model) { m.mailView.openPreviouslySeen() }},
		{"JournalExit", sectionJournal, func(m model) { m.journalView.ExitThread() }},
		{"JournalNewDate", sectionJournal, func(m model) {
			m.journalView.Update(runCmd(m.journalView.requestDate("2026-08-20", false)))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := newInlineImageGate(t)
			m, open := inlineImageTestModel(t, gate, tc.section)
			cmd, _ := m.activeView.Update(runCmd(open()))
			images := runInlineImageCommand(cmd)
			gate.await(t, gate.started, "image request")
			tc.leave(m)
			gate.await(t, gate.canceled, "image HTTP cancellation")
			stale := awaitInlineImageMessage(t, images, "canceled image result")
			if upload, _ := m.activeView.Update(stale); upload != nil {
				t.Fatal("canceled image result returned an upload")
			}
		})
	}
}

func TestDetailImagesReachTheOpenDetailAfterSectionSwitch(t *testing.T) {
	for _, sec := range []section{sectionMail, sectionJournal} {
		t.Run(map[section]string{sectionMail: "Mail", sectionJournal: "Journal"}[sec], func(t *testing.T) {
			gate := newInlineImageGate(t)
			m, open := inlineImageTestModel(t, gate, sec)
			cmd, _ := m.activeView.Update(runCmd(open()))
			images := runInlineImageCommand(cmd)
			gate.await(t, gate.started, "started image request")
			away, _ := m.switchSection(sectionContacts)
			gate.release()
			finished := awaitInlineImageMessage(t, images, "finished images")
			away, upload := away.Update(finished)
			if upload == nil {
				t.Fatal("no terminal upload for the still-open detail")
			}
			back, _ := away.(model).switchSection(sec)
			returned := back.(model)
			if (sec == sectionMail && !strings.Contains(returned.mailView.topicContent, string(placeholder))) ||
				(sec == sectionJournal && !strings.Contains(returned.journalView.detailView.GetContent(), string(placeholder))) {
				t.Fatal("returning to the section lost the image")
			}
		})
	}
}

func TestDetailImagesSurviveFailedReplacement(t *testing.T) {
	gate := newInlineImageGate(t)
	m, open := inlineImageTestModel(t, gate, sectionMail)
	cmd, _ := m.activeView.Update(runCmd(open()))
	images := runInlineImageCommand(cmd)
	gate.await(t, gate.started, "started image request")
	m.mailView.Update(runCmd(m.mailView.requestTopic(0, 999, 0, "Missing thread")))
	gate.release()
	if upload, _ := m.mailView.Update(awaitInlineImageMessage(t, images, "finished images")); upload == nil ||
		!strings.Contains(m.mailView.topicContent, string(placeholder)) {
		t.Fatal("failed replacement discarded the still-open thread's image")
	}
}

func inlineImageTestModel(t *testing.T, gate *inlineImageGate, sec section) (model, func() tea.Cmd) {
	t.Helper()
	m := newModel()
	t.Cleanup(m.cancel)
	t.Cleanup(m.stopWatching)
	m.section = sec
	if sec == sectionMail {
		vc := inlineImageTestContext(t, gate, serveInlineImageMail)
		m.mailView = newMailView(vc)
		m.mailView.boxes = orderBoxes(testBoxes())
		m.mailView.boxIndex = 0
		m.activeView = m.mailView
		return m, func() tea.Cmd { return m.mailView.requestTopic(0, 100, 0, "Mail with images") }
	}
	vc := inlineImageTestContext(t, gate, serveInlineImageJournal)
	m.journalView = newJournalView(vc)
	m.activeView = m.journalView
	return m, func() tea.Cmd { return m.journalView.requestDate("2026-08-19", false) }
}
