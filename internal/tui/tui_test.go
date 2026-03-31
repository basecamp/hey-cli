package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/models"
)

// --- Test helpers ---

func testModel() model {
	return newModel(nil, nil)
}

func sizedModel() model {
	m := testModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	return updated.(model)
}

func modelWithBoxes() model {
	m := sizedModel()
	updated, _ := m.Update(boxesLoadedMsg(testBoxes()))
	m = updated.(model)
	// Simulate postings loaded for first box
	updated, _ = m.Update(postingsLoadedMsg{postings: testPostings()})
	return updated.(model)
}

func testBoxes() []models.Box {
	return []models.Box{
		{ID: 1, Name: "Imbox", Kind: "inbox"},
		{ID: 2, Name: "The Feed", Kind: "feed"},
		{ID: 3, Name: "Paper Trail", Kind: "paper_trail"},
	}
}

func testPostings() []models.Posting {
	return []models.Posting{
		{
			ID:        100,
			Summary:   "Hello world",
			CreatedAt: "2025-03-01T10:00:00Z",
			Seen:      false,
			Creator:   models.Contact{Name: "Alice"},
		},
		{
			ID:        101,
			Summary:   "Meeting notes",
			CreatedAt: "2025-03-01T09:00:00Z",
			Seen:      true,
			Creator:   models.Contact{Name: "Bob"},
		},
	}
}

func keyPress(key string) tea.KeyPressMsg {
	k := tea.Key{Text: key}
	switch key {
	case "ctrl+c":
		k = tea.Key{Code: 'c', Mod: tea.ModCtrl}
	case "esc":
		k = tea.Key{Code: tea.KeyEscape}
	case "enter":
		k = tea.Key{Code: tea.KeyEnter}
	case "tab":
		k = tea.Key{Code: tea.KeyTab}
	case "shift+tab":
		k = tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}
	case "left":
		k = tea.Key{Code: tea.KeyLeft}
	case "right":
		k = tea.Key{Code: tea.KeyRight}
	case "up":
		k = tea.Key{Code: tea.KeyUp}
	case "down":
		k = tea.Key{Code: tea.KeyDown}
	}
	return tea.KeyPressMsg(k)
}

// --- Model initialization ---

func TestNewModelInitialState(t *testing.T) {
	m := testModel()
	if m.section != sectionMail {
		t.Errorf("initial section = %d, want sectionMail", m.section)
	}
	if m.focus != rowContent {
		t.Errorf("initial focus = %d, want rowContent", m.focus)
	}
	if !m.loading {
		t.Error("loading should be true initially")
	}
}

func TestInitReturnsCmd(t *testing.T) {
	m := testModel()
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should return a command")
	}
}

// --- Box ordering ---

func TestOrderBoxes(t *testing.T) {
	boxes := []models.Box{
		{ID: 1, Name: "The Feed"},
		{ID: 2, Name: "Imbox"},
		{ID: 3, Name: "Custom Box"},
		{ID: 4, Name: "Paper Trail"},
	}
	ordered := orderBoxes(boxes)
	if ordered[0].Name != "Imbox" {
		t.Errorf("first box = %q, want Imbox", ordered[0].Name)
	}
	if ordered[1].Name != "Paper Trail" {
		t.Errorf("second box = %q, want Paper Trail", ordered[1].Name)
	}
	if ordered[2].Name != "The Feed" {
		t.Errorf("third box = %q, want The Feed", ordered[2].Name)
	}
	if ordered[3].Name != "Custom Box" {
		t.Errorf("last box = %q, want Custom Box", ordered[3].Name)
	}
}

// --- Navigation: Tab cycles focus rows ---

func TestTabCyclesFocus(t *testing.T) {
	m := modelWithBoxes()
	m.focus = rowSection

	updated, _ := m.Update(keyPress("tab"))
	m = updated.(model)
	if m.focus != rowSubnav {
		t.Errorf("after tab from rowSection: focus = %d, want rowSubnav", m.focus)
	}

	updated, _ = m.Update(keyPress("tab"))
	m = updated.(model)
	if m.focus != rowContent {
		t.Errorf("after tab from rowSubnav: focus = %d, want rowContent", m.focus)
	}

	updated, _ = m.Update(keyPress("tab"))
	m = updated.(model)
	if m.focus != rowSection {
		t.Errorf("after tab from rowContent: focus = %d, want rowSection", m.focus)
	}
}

func TestShiftTabReversesFocus(t *testing.T) {
	m := modelWithBoxes()
	m.focus = rowContent

	updated, _ := m.Update(keyPress("shift+tab"))
	m = updated.(model)
	if m.focus != rowSubnav {
		t.Errorf("after shift+tab from rowContent: focus = %d, want rowSubnav", m.focus)
	}
}

// --- Navigation: Double Ctrl+C to quit ---

func TestDoubleCtrlCQuits(t *testing.T) {
	m := modelWithBoxes()
	updated, _ := m.Update(keyPress("ctrl+c"))
	m = updated.(model)
	if !m.ctrlCOnce {
		t.Fatal("first ctrl+c should arm quit")
	}

	_, cmd := m.Update(keyPress("ctrl+c"))
	if cmd == nil {
		t.Fatal("double ctrl+c should return a quit cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("double ctrl+c produced %T, want tea.QuitMsg", msg)
	}
}

func TestSingleCtrlCDoesNotQuit(t *testing.T) {
	m := modelWithBoxes()
	_, cmd := m.Update(keyPress("ctrl+c"))
	if cmd == nil {
		t.Fatal("first ctrl+c should return a timer cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); ok {
		t.Error("single ctrl+c should NOT quit")
	}
}

// --- Navigation: Esc/q in thread goes back ---

func TestEscExitsThread(t *testing.T) {
	m := modelWithBoxes()
	m.inThread = true

	updated, _ := m.Update(keyPress("esc"))
	result := updated.(model)
	if result.inThread {
		t.Error("esc should exit thread")
	}
}

func TestQExitsThread(t *testing.T) {
	m := modelWithBoxes()
	m.inThread = true

	updated, _ := m.Update(keyPress("q"))
	result := updated.(model)
	if result.inThread {
		t.Error("q should exit thread")
	}
}

// --- Content list ---

func TestContentListNavigation(t *testing.T) {
	cl := &contentList{}
	cl.setPostings(testPostings())
	cl.setSize(80, 20)

	if cl.cursor != 0 {
		t.Errorf("initial cursor = %d, want 0", cl.cursor)
	}

	cl.moveDown()
	if cl.cursor != 1 {
		t.Errorf("after moveDown cursor = %d, want 1", cl.cursor)
	}

	cl.moveDown() // already at end
	if cl.cursor != 1 {
		t.Errorf("moveDown at end: cursor = %d, want 1", cl.cursor)
	}

	cl.moveUp()
	if cl.cursor != 0 {
		t.Errorf("after moveUp cursor = %d, want 0", cl.cursor)
	}
}

func TestContentListSelectedPosting(t *testing.T) {
	cl := &contentList{}
	cl.setPostings(testPostings())

	p := cl.selectedPosting()
	if p == nil || p.Summary != "Hello world" {
		t.Error("selectedPosting should return first posting")
	}
}

// --- View rendering ---

func TestViewShowsHeader(t *testing.T) {
	m := modelWithBoxes()
	v := m.View()
	if !strings.Contains(v.Content, "HEY") {
		t.Error("View should contain HEY header")
	}
	if !strings.Contains(v.Content, "Mail") {
		t.Error("View should contain Mail section")
	}
}

func TestViewShowsBoxNames(t *testing.T) {
	m := modelWithBoxes()
	v := m.View()
	if !strings.Contains(v.Content, "Imbox") {
		t.Error("View should contain Imbox")
	}
}

// --- Journal dates ---

func TestGenerateJournalDates(t *testing.T) {
	dates := generateJournalDates(7)
	if len(dates) != 7 {
		t.Fatalf("expected 7 dates, got %d", len(dates))
	}
	today := time.Now().Format("2006-01-02")
	if dates[6] != today {
		t.Errorf("last date = %q, want today %q", dates[6], today)
	}
}
