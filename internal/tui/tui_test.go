package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"hey-cli/internal/models"
)

// Test helpers

func testModel() model {
	return newModel(nil)
}

func sizedModel() model {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(model)
}

func modelWithBoxes() model {
	m := sizedModel()
	updated, _ := m.Update(boxesLoadedMsg(testBoxes()))
	return updated.(model)
}

func testBoxes() []models.Box {
	return []models.Box{
		{ID: 1, Name: "Imbox", Kind: "imbox"},
		{ID: 2, Name: "Feed", Kind: "feedbox"},
		{ID: 3, Name: "Paper Trail", Kind: "papertrailbox"},
	}
}

func testPostings() []models.Posting {
	return []models.Posting{
		{
			ID:        10,
			Summary:   "Hello world",
			CreatedAt: "2025-01-15T10:30:00Z",
			Creator:   models.Contact{ID: 1, Name: "Alice"},
			Topic:     &models.Topic{ID: 100, Name: "Hello world"},
		},
		{
			ID:        11,
			Summary:   "Meeting notes",
			CreatedAt: "2025-01-16T14:00:00Z",
			Creator:   models.Contact{ID: 2, Name: "Bob"},
			Topic:     &models.Topic{ID: 101, Name: "Meeting notes"},
		},
	}
}

func testEntries() []models.Entry {
	return []models.Entry{
		{
			ID:        200,
			CreatedAt: "2025-01-15T10:30:00Z",
			Creator:   models.Contact{Name: "Alice", EmailAddress: "alice@hey.com"},
			Summary:   "Hello world",
			Body:      "This is the message body.",
		},
		{
			ID:        201,
			CreatedAt: "2025-01-15T11:00:00Z",
			Creator:   models.Contact{Name: "Bob", EmailAddress: "bob@hey.com"},
			Summary:   "Re: Hello world",
			Body:      "Thanks for reaching out!",
		},
	}
}

func keyPress(key string) tea.KeyPressMsg {
	switch key {
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
	case "backspace":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace})
	case "ctrl+c":
		return tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	default:
		// Single character keys
		r := rune(key[0])
		return tea.KeyPressMsg(tea.Key{Code: r, Text: key})
	}
}

// --- boxItem tests ---

func TestBoxItemTitle(t *testing.T) {
	item := boxItem{box: models.Box{Name: "Imbox"}}
	if got := item.Title(); got != "Imbox" {
		t.Errorf("Title() = %q, want %q", got, "Imbox")
	}
}

func TestBoxItemDescription(t *testing.T) {
	item := boxItem{box: models.Box{Kind: "imbox"}}
	if got := item.Description(); got != "imbox" {
		t.Errorf("Description() = %q, want %q", got, "imbox")
	}
}

func TestBoxItemFilterValue(t *testing.T) {
	item := boxItem{box: models.Box{Name: "Feed"}}
	if got := item.FilterValue(); got != "Feed" {
		t.Errorf("FilterValue() = %q, want %q", got, "Feed")
	}
}

// --- postingItem tests ---

func TestPostingItemTitle(t *testing.T) {
	item := postingItem{posting: models.Posting{Summary: "Hello world"}}
	if got := item.Title(); got != "Hello world" {
		t.Errorf("Title() = %q, want %q", got, "Hello world")
	}
}

func TestPostingItemDescription(t *testing.T) {
	item := postingItem{posting: models.Posting{
		CreatedAt: "2025-01-15T10:30:00Z",
		Creator:   models.Contact{Name: "Alice"},
	}}
	got := item.Description()
	if got != "Alice · 2025-01-15" {
		t.Errorf("Description() = %q, want %q", got, "Alice · 2025-01-15")
	}
}

func TestPostingItemDescriptionShortDate(t *testing.T) {
	item := postingItem{posting: models.Posting{
		CreatedAt: "short",
		Creator:   models.Contact{Name: "Bob"},
	}}
	got := item.Description()
	if got != "Bob · " {
		t.Errorf("Description() = %q, want %q", got, "Bob · ")
	}
}

func TestPostingItemFilterValue(t *testing.T) {
	item := postingItem{posting: models.Posting{Summary: "Meeting notes"}}
	if got := item.FilterValue(); got != "Meeting notes" {
		t.Errorf("FilterValue() = %q, want %q", got, "Meeting notes")
	}
}

// --- Model initialization ---

func TestNewModelInitialState(t *testing.T) {
	m := testModel()
	if m.state != viewBoxes {
		t.Errorf("initial state = %d, want viewBoxes (%d)", m.state, viewBoxes)
	}
	if m.loading {
		t.Error("loading should be false initially")
	}
	if m.err != nil {
		t.Error("err should be nil initially")
	}
}

func TestInitReturnsCmd(t *testing.T) {
	m := testModel()
	cmd := m.Init()
	// Init should return a command (fetchBoxes), not nil
	// We can't execute it without a real client, but it shouldn't be nil
	if cmd == nil {
		t.Error("Init() should return a non-nil command")
	}
}

// --- WindowSizeMsg ---

func TestWindowSizeMsg(t *testing.T) {
	m := testModel()
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	result := updated.(model)

	if result.width != 120 {
		t.Errorf("width = %d, want 120", result.width)
	}
	if result.height != 40 {
		t.Errorf("height = %d, want 40", result.height)
	}
	if cmd != nil {
		t.Error("WindowSizeMsg should return nil cmd")
	}
}

// --- Data loading messages ---

func TestBoxesLoadedMsg(t *testing.T) {
	m := sizedModel()
	m.loading = true

	updated, _ := m.Update(boxesLoadedMsg(testBoxes()))
	result := updated.(model)

	if result.loading {
		t.Error("loading should be false after boxesLoadedMsg")
	}
	if result.state != viewBoxes {
		t.Errorf("state = %d, want viewBoxes", result.state)
	}
	selected := result.boxes.selectedBox()
	if selected == nil {
		t.Fatal("selectedBox() returned nil after setting items")
	}
	if selected.Name != "Imbox" {
		t.Errorf("first selected box = %q, want %q", selected.Name, "Imbox")
	}
}

func TestBoxLoadedMsg(t *testing.T) {
	m := sizedModel()
	m.loading = true

	box := models.Box{ID: 1, Name: "Imbox", Kind: "imbox"}
	postings := testPostings()

	updated, _ := m.Update(boxLoadedMsg{box: box, postings: postings})
	result := updated.(model)

	if result.loading {
		t.Error("loading should be false after boxLoadedMsg")
	}
	if result.state != viewBox {
		t.Errorf("state = %d, want viewBox (%d)", result.state, viewBox)
	}
	if result.box.list.Title != "Imbox" {
		t.Errorf("box list title = %q, want %q", result.box.list.Title, "Imbox")
	}
}

func TestTopicLoadedMsg(t *testing.T) {
	m := sizedModel()
	m.loading = true

	updated, _ := m.Update(topicLoadedMsg{title: "Hello world", entries: testEntries()})
	result := updated.(model)

	if result.loading {
		t.Error("loading should be false after topicLoadedMsg")
	}
	if result.state != viewTopic {
		t.Errorf("state = %d, want viewTopic (%d)", result.state, viewTopic)
	}
	if result.topic.title != "Hello world" {
		t.Errorf("topic title = %q, want %q", result.topic.title, "Hello world")
	}
}

func TestErrMsg(t *testing.T) {
	m := sizedModel()
	m.loading = true

	updated, _ := m.Update(errMsg{err: errors.New("network error")})
	result := updated.(model)

	if result.loading {
		t.Error("loading should be false after errMsg")
	}
	if result.err == nil {
		t.Fatal("err should be set after errMsg")
	}
	if result.err.Error() != "network error" {
		t.Errorf("err = %q, want %q", result.err.Error(), "network error")
	}
}

// --- Navigation: ctrl+c quits from any view ---

func TestCtrlCQuitsFromBoxes(t *testing.T) {
	m := modelWithBoxes()
	_, cmd := m.Update(keyPress("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c should return a quit cmd")
	}
	// Execute the cmd and check it returns a QuitMsg
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c cmd produced %T, want tea.QuitMsg", msg)
	}
}

func TestCtrlCQuitsFromBox(t *testing.T) {
	m := modelWithBoxes()
	m.state = viewBox
	_, cmd := m.Update(keyPress("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c should return a quit cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c cmd produced %T, want tea.QuitMsg", msg)
	}
}

func TestCtrlCQuitsFromTopic(t *testing.T) {
	m := modelWithBoxes()
	m.state = viewTopic
	_, cmd := m.Update(keyPress("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c should return a quit cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c cmd produced %T, want tea.QuitMsg", msg)
	}
}

// --- Navigation: esc goes back ---

func TestEscFromBoxGoesBackToBoxes(t *testing.T) {
	m := modelWithBoxes()
	// Transition to box view
	m.state = viewBox

	updated, _ := m.Update(keyPress("esc"))
	result := updated.(model)

	if result.state != viewBoxes {
		t.Errorf("state after esc = %d, want viewBoxes (%d)", result.state, viewBoxes)
	}
}

func TestBackspaceFromBoxGoesBackToBoxes(t *testing.T) {
	m := modelWithBoxes()
	m.state = viewBox

	updated, _ := m.Update(keyPress("backspace"))
	result := updated.(model)

	if result.state != viewBoxes {
		t.Errorf("state after backspace = %d, want viewBoxes (%d)", result.state, viewBoxes)
	}
}

func TestEscFromTopicGoesBackToBox(t *testing.T) {
	m := modelWithBoxes()
	m.state = viewTopic

	updated, _ := m.Update(keyPress("esc"))
	result := updated.(model)

	if result.state != viewBox {
		t.Errorf("state after esc = %d, want viewBox (%d)", result.state, viewBox)
	}
}

func TestBackspaceFromTopicGoesBackToBox(t *testing.T) {
	m := modelWithBoxes()
	m.state = viewTopic

	updated, _ := m.Update(keyPress("backspace"))
	result := updated.(model)

	if result.state != viewBox {
		t.Errorf("state after backspace = %d, want viewBox (%d)", result.state, viewBox)
	}
}

func TestQFromTopicGoesBackToBox(t *testing.T) {
	m := modelWithBoxes()
	m.state = viewTopic

	updated, _ := m.Update(keyPress("q"))
	result := updated.(model)

	if result.state != viewBox {
		t.Errorf("state after q in topic = %d, want viewBox (%d)", result.state, viewBox)
	}
}

// --- View rendering ---

func TestViewShowsLoadingState(t *testing.T) {
	m := sizedModel()
	m.loading = true
	v := m.View()
	if !strings.Contains(v.Content, "Loading...") {
		t.Error("View should show 'Loading...' when loading")
	}
	if !v.AltScreen {
		t.Error("View should have AltScreen enabled")
	}
}

func TestViewShowsError(t *testing.T) {
	m := sizedModel()
	m.err = errors.New("connection failed")
	v := m.View()
	if !strings.Contains(v.Content, "connection failed") {
		t.Error("View should display the error message")
	}
}

func TestViewAltScreenAlwaysEnabled(t *testing.T) {
	m := sizedModel()
	v := m.View()
	if !v.AltScreen {
		t.Error("View should always have AltScreen = true")
	}
}

// --- Topic rendering ---

func TestRenderEntries(t *testing.T) {
	s := newStyles()
	tm := newTopicModel(s)
	content := tm.renderEntries(testEntries())

	if !strings.Contains(content, "Alice") {
		t.Error("rendered entries should contain creator name 'Alice'")
	}
	if !strings.Contains(content, "Bob") {
		t.Error("rendered entries should contain creator name 'Bob'")
	}
	if !strings.Contains(content, "This is the message body.") {
		t.Error("rendered entries should contain body text")
	}
	if !strings.Contains(content, "Thanks for reaching out!") {
		t.Error("rendered entries should contain second entry body")
	}
	if !strings.Contains(content, "─") {
		t.Error("rendered entries should contain separator between entries")
	}
}

func TestRenderEntriesAlternativeSender(t *testing.T) {
	s := newStyles()
	tm := newTopicModel(s)
	entries := []models.Entry{
		{
			Creator:               models.Contact{Name: "System", EmailAddress: "system@hey.com"},
			AlternativeSenderName: "Custom Sender",
			CreatedAt:             "2025-01-15T10:30:00Z",
			Body:                  "test body",
		},
	}
	content := tm.renderEntries(entries)

	if !strings.Contains(content, "Custom Sender") {
		t.Error("should use AlternativeSenderName when set")
	}
}

func TestRenderEntriesFallsBackToEmail(t *testing.T) {
	s := newStyles()
	tm := newTopicModel(s)
	entries := []models.Entry{
		{
			Creator:   models.Contact{EmailAddress: "nobody@hey.com"},
			CreatedAt: "2025-01-15T10:30:00Z",
			Body:      "test",
		},
	}
	content := tm.renderEntries(entries)

	if !strings.Contains(content, "nobody@hey.com") {
		t.Error("should fall back to email when name is empty")
	}
}

func TestRenderEntriesEmpty(t *testing.T) {
	s := newStyles()
	tm := newTopicModel(s)
	content := tm.renderEntries(nil)
	if content != "" {
		t.Errorf("renderEntries(nil) = %q, want empty string", content)
	}
}

// --- Sub-model construction ---

func TestNewBoxesModelTitle(t *testing.T) {
	bm := newBoxesModel()
	if bm.list.Title != "Mailboxes" {
		t.Errorf("boxes list title = %q, want %q", bm.list.Title, "Mailboxes")
	}
}

func TestBoxesSelectedBoxNilWhenEmpty(t *testing.T) {
	bm := newBoxesModel()
	if bm.selectedBox() != nil {
		t.Error("selectedBox() should return nil when list is empty")
	}
}

func TestBoxSelectedPostingNilWhenEmpty(t *testing.T) {
	bm := newBoxModel()
	if bm.selectedPosting() != nil {
		t.Error("selectedPosting() should return nil when list is empty")
	}
}

// --- errMsg ---

func TestErrMsgError(t *testing.T) {
	e := errMsg{err: errors.New("test error")}
	if e.Error() != "test error" {
		t.Errorf("Error() = %q, want %q", e.Error(), "test error")
	}
}
