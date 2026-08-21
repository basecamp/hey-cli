package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/models"
)

// --- Test helpers ---

func testModel() model {
	return newModel()
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
	updated, _ = m.Update(currentPostingsLoaded(m.mailView, testPostings()))
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

func TestModelResizesContentWhenThreadHelpChangesHeight(t *testing.T) {
	for _, width := range []int{32, 40, 48, 64, 80, 100} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := modelWithBoxes()
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			m = updated.(model)
			m.mailView.activeRequestID = 7
			m.mailView.activeRequestKind = mailRequestTopic
			updated, _ = m.Update(topicLoadedMsg{
				requestID:   7,
				boxID:       1,
				topicID:     100,
				title:       "Quarterly planning",
				entries:     []models.Entry{{ID: 501, Creator: models.Contact{Name: "Alice"}}},
				attachments: []messageAttachment{{ID: "501:1", MessageID: 501, Filename: "agenda.pdf"}},
			})
			m = updated.(model)

			wantHeight := m.height - headerHeight - m.help.height() - 3
			if wantHeight < 1 {
				wantHeight = 1
			}
			if m.vc.height != wantHeight || m.mailView.topicViewport.Height() != wantHeight {
				t.Errorf("thread content height = context:%d viewport:%d, want %d for help height %d", m.vc.height, m.mailView.topicViewport.Height(), wantHeight, m.help.height())
			}
		})
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
	if ordered[1].Name != "The Feed" {
		t.Errorf("second box = %q, want The Feed", ordered[1].Name)
	}
	if ordered[2].Name != "Paper Trail" {
		t.Errorf("third box = %q, want Paper Trail", ordered[2].Name)
	}
	if ordered[3].Name != "Custom Box" {
		t.Errorf("last box = %q, want Custom Box", ordered[3].Name)
	}
}

// --- Navigation: Tab cycles focus rows ---

func TestOrderBoxesPreservesFolderWithCollidingIDAndName(t *testing.T) {
	boxes := []models.Box{
		{ID: 1, Kind: hey.BoxKindImbox, Name: "Imbox"},
		{ID: 1, Kind: mailSourceKindFolder, Name: "Imbox"},
	}
	ordered := orderBoxes(boxes)
	if len(ordered) != 2 || ordered[0].Kind != hey.BoxKindImbox || ordered[1].Kind != mailSourceKindFolder {
		t.Errorf("ordered sources = %+v", ordered)
	}
	if index := boxForShortcut("1", ordered); index != 0 {
		t.Errorf("Imbox shortcut index = %d, want box index 0", index)
	}
	items := boxNavItems(ordered)
	if items[0].shortcut == "" || items[1].shortcut != "" {
		t.Errorf("navigation shortcuts = %+v", items)
	}
}

func TestFolderDiscoveryCompletesWhileAnotherSectionIsActive(t *testing.T) {
	m := newModel()
	m.section = sectionCalendar
	m.activeView = m.calendarView
	m.mailView.sourceRequestID = 1

	updated, cmd := m.Update(mailSourcesLoadedMsg{
		requestID: 1,
		sources: []models.Box{
			{ID: 1, Kind: hey.BoxKindImbox, Name: "Imbox"},
			{ID: 12, Kind: mailSourceKindFolder, Name: "Receipts"},
		},
	})
	m = updated.(model)
	if len(m.mailView.boxes) != 2 || m.mailView.boxes[1].Name != "Receipts" {
		t.Errorf("mail sources = %+v", m.mailView.boxes)
	}
	if cmd == nil {
		t.Error("inactive Mail view should continue loading its selected source")
	}
}

func TestInactiveMailIgnoresStalePostingErrors(t *testing.T) {
	m := newModel()
	m.section = sectionCalendar
	m.activeView = m.calendarView
	m.mailView.boxes = []models.Box{{ID: 1, Kind: hey.BoxKindImbox, Name: "Imbox"}}
	m.mailView.boxIndex = 0
	m.mailView.activeRequestID = 2
	m.mailView.activeRequestKind = mailRequestPostings
	m.mailView.loading = true
	m.mailView.notice = "Current mail state"

	updated, cmd := m.Update(postingsLoadedMsg{
		requestID:  1,
		boxID:      1,
		sourceKind: hey.BoxKindImbox,
		err:        fmt.Errorf("stale failure"),
	})
	m = updated.(model)
	if cmd != nil || m.mailView.notice != "Current mail state" || !m.mailView.loading || m.mailView.activeRequestID != 2 {
		t.Errorf("stale error changed inactive Mail: notice=%q loading=%v request=%d", m.mailView.notice, m.mailView.loading, m.mailView.activeRequestID)
	}

	updated, cmd = m.Update(postingsLoadedMsg{
		requestID:  2,
		boxID:      1,
		sourceKind: hey.BoxKindImbox,
		err:        fmt.Errorf("current failure"),
	})
	m = updated.(model)
	if cmd != nil || m.mailView.notice != "Could not load mail: current failure" || m.mailView.loading {
		t.Errorf("current error state = notice:%q loading:%v", m.mailView.notice, m.mailView.loading)
	}
}

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

func TestLoadingViewKeepsItsSectionUntilResponse(t *testing.T) {
	m := modelWithBoxes()
	m.loading = true
	m.mailView.loading = true

	updated, cmd := m.Update(keyPress("O"))
	result := updated.(model)
	if result.activeView != result.mailView || result.section != sectionMail {
		t.Error("section changed while the active view was loading")
	}
	if cmd != nil {
		t.Error("section shortcut should not start another request while loading")
	}
}

func TestContactsSectionShortcut(t *testing.T) {
	m := modelWithBoxes()
	updated, cmd := m.Update(keyPress("O"))
	result := updated.(model)
	if result.section != sectionContacts || result.activeView != result.contactsView {
		t.Errorf("O shortcut selected section %d and view %T", result.section, result.activeView)
	}
	if cmd == nil || !result.loading {
		t.Error("opening Contacts should start its initial list request")
	}
}

func TestSectionNavigationIncludesContacts(t *testing.T) {
	m := modelWithBoxes()
	m.focus = rowSection
	updated, _ := m.Update(keyPress("right"))
	result := updated.(model)
	if result.section != sectionContacts {
		t.Errorf("right from Mail selected section %d, want Contacts", result.section)
	}
}

func TestSubnavNavigationUpdatesHelpWhenItOpensLabels(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.boxes = orderBoxes(append(m.mailView.boxes,
		models.Box{ID: 12, Kind: mailSourceKindFolder, Name: "Receipts"},
	))
	m.mailView.boxIndex = len(m.mailView.tabBoxIndexes()) - 1
	m.focus = rowSubnav
	m.updateHelpBindings()

	updated, _ := m.Update(keyPress("right"))
	m = updated.(model)

	if m.mailView.labels == nil {
		t.Fatal("right from the last mail tab should open Labels")
	}
	for _, want := range []helpBinding{{"↑/↓", "choose"}, {"enter", "open"}, {"esc", "cancel"}} {
		if !slices.Contains(m.help.bindings, want) {
			t.Errorf("Labels help is missing %+v: %v", want, m.help.bindings)
		}
	}
}

// --- Navigation: Esc/q in thread goes back ---

func TestThreadHelpIncludesMailActions(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.inThread = true
	m.updateHelpBindings()

	keys := make(map[string]bool)
	for _, binding := range m.help.bindings {
		keys[binding.key] = true
	}
	for _, want := range []string{"↑↓", "esc/q", "r", "f"} {
		if !keys[want] {
			t.Errorf("thread help is missing %q: %v", want, m.help.bindings)
		}
	}
}

func TestCalendarDetailHelpOmitsListActions(t *testing.T) {
	m := sizedModel()
	m.section = sectionCalendar
	m.activeView = m.calendarView
	m.calendarView.inThread = true
	m.updateHelpBindings()

	for _, binding := range m.help.bindings {
		if binding.key == "v" {
			t.Errorf("calendar detail help advertises inactive view toggle: %v", m.help.bindings)
		}
	}
}

func TestMovePickerOwnsNavigationKeys(t *testing.T) {
	m := modelWithBoxes()
	m.focus = rowContent

	updated, _ := m.Update(keyPress("m"))
	m = updated.(model)
	if m.mailView.movePicker == nil || !m.mailView.CapturingInput() {
		t.Fatal("m should open the move picker")
	}

	updated, _ = m.Update(keyPress("tab"))
	m = updated.(model)
	if m.focus != rowContent || m.mailView.movePicker == nil {
		t.Error("tab should remain inside the move picker")
	}

	updated, _ = m.Update(keyPress("esc"))
	m = updated.(model)
	if m.mailView.movePicker != nil || m.mailView.CapturingInput() {
		t.Error("escape should close the move picker")
	}
}

func TestSearchFormOwnsNavigationKeys(t *testing.T) {
	m := modelWithBoxes()
	m.focus = rowContent

	updated, _ := m.Update(keyPress("/"))
	m = updated.(model)
	if m.mailView.searchForm == nil || !m.mailView.CapturingInput() {
		t.Fatal("/ should open the search form")
	}

	updated, _ = m.Update(keyPress("tab"))
	m = updated.(model)
	if m.focus != rowContent || m.mailView.searchForm == nil {
		t.Error("tab should remain inside the search form")
	}

	updated, _ = m.Update(keyPress("esc"))
	m = updated.(model)
	if m.mailView.searchForm != nil || m.mailView.CapturingInput() {
		t.Error("escape should close the search form")
	}
}

func TestQExitsSearchResults(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.searchActive = true
	m.mailView.searchQuery = "quarterly planning"
	m.mailView.notice = "No more search results"

	updated, _ := m.Update(keyPress("q"))
	result := updated.(model)
	if result.mailView.searchActive || result.activeView.InThread() {
		t.Error("q should exit search results")
	}
	if result.mailView.notice != "" {
		t.Errorf("q left a stale search notice: %q", result.mailView.notice)
	}
}

func TestEscCancelsPendingSearchResultAndPreservesResults(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.searchActive = true
	m.mailView.searchQuery = "quarterly planning"
	m.mailView.searchPage = 1
	m.mailView.searchList.setPostings([]models.Posting{{ID: 10, TopicID: 100, Name: "Hello world"}})
	m.mailView.requestTopic(m.mailView.currentBoxID(), 100, 10, "Hello world")
	m.loading = true

	updated, _ := m.Update(keyPress("esc"))
	result := updated.(model)
	if !result.mailView.searchActive || result.mailView.searchQuery != "quarterly planning" || len(result.mailView.searchList.postings) != 1 {
		t.Error("escape during thread load should preserve search results")
	}
	if result.mailView.loading || result.loading {
		t.Error("escape should cancel the pending thread load")
	}
}

func TestQExitsSearchDuringPendingResult(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.searchActive = true
	m.mailView.searchQuery = "quarterly planning"
	m.mailView.searchPage = 1
	m.mailView.searchList.setPostings([]models.Posting{{ID: 10, TopicID: 100, Name: "Hello world"}})
	m.mailView.requestTopic(m.mailView.currentBoxID(), 100, 10, "Hello world")
	m.loading = true

	updated, _ := m.Update(keyPress("q"))
	result := updated.(model)
	if result.mailView.searchActive || len(result.mailView.searchList.postings) != 0 {
		t.Error("q during thread load should exit search results")
	}
	if result.mailView.loading || result.loading {
		t.Error("q should cancel the pending thread load")
	}
}

func TestEscExitsThread(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.inThread = true

	updated, _ := m.Update(keyPress("esc"))
	result := updated.(model)
	if result.activeView.InThread() {
		t.Error("esc should exit thread")
	}
}

func TestQExitsThread(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.inThread = true

	updated, _ := m.Update(keyPress("q"))
	result := updated.(model)
	if result.activeView.InThread() {
		t.Error("q should exit thread")
	}
}

func TestQExitsPendingReplyLoad(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.inThread = true
	m.mailView.topicID = 100
	m.mailView.topicName = "Hello world"

	updated, _ := m.Update(keyPress("r"))
	m = updated.(model)
	if !m.mailView.loading || !m.loading {
		t.Fatal("reply should start loading")
	}

	updated, _ = m.Update(keyPress("q"))
	result := updated.(model)
	if result.mailView.loading || result.loading {
		t.Error("q should stop a canceled reply load")
	}
}

func TestQExitsPendingReplyLoadFromPostingList(t *testing.T) {
	m := modelWithBoxes()

	updated, cmd := m.Update(keyPress("r"))
	m = updated.(model)
	if cmd == nil || !m.mailView.loading || !m.loading {
		t.Fatal("reply from the posting list should start loading")
	}
	requestID := m.mailView.activeRequestID

	updated, _ = m.Update(keyPress("q"))
	m = updated.(model)
	if m.mailView.loading || m.loading {
		t.Error("q should stop a reply started from the posting list")
	}
	if m.mailView.activeRequestKind != mailRequestNone || m.mailView.requestCancel != nil {
		t.Error("q should clear the pending reply request")
	}

	updated, _ = m.Update(replyContextLoadedMsg{
		requestID: requestID,
		boxID:     1,
		topicID:   100,
		topicName: "Hello world",
		entryID:   501,
		to:        []string{"jane@example.com"},
	})
	m = updated.(model)
	if m.mailView.compose != nil {
		t.Error("a canceled reply load should not open the reply form")
	}
}

func TestQExitsPendingForwardLoadFromPostingList(t *testing.T) {
	m := modelWithBoxes()

	updated, cmd := m.Update(keyPress("f"))
	m = updated.(model)
	if cmd == nil || !m.mailView.loading || m.mailView.activeRequestKind != mailRequestForward {
		t.Fatal("forward from the posting list should start loading")
	}
	requestID := m.mailView.activeRequestID

	updated, _ = m.Update(keyPress("q"))
	m = updated.(model)
	if m.mailView.loading || m.loading {
		t.Error("q should stop a forward started from the posting list")
	}

	updated, _ = m.Update(forwardContextLoadedMsg{
		requestID: requestID,
		boxID:     1,
		topicID:   100,
		topicName: "Hello world",
		subject:   "Fwd: Hello world",
		content:   "<div>Hello world</div>",
	})
	m = updated.(model)
	if m.mailView.compose != nil {
		t.Error("a canceled forward load should not open the forward form")
	}
}

func TestBoxShortcutExitsThread(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.inThread = true

	updated, _ := m.Update(keyPress("2"))
	result := updated.(model)
	if result.activeView.InThread() {
		t.Error("switching boxes should exit thread")
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

func TestContentListUsesRowsReleasedByScrolledOffHeader(t *testing.T) {
	postings := make([]models.Posting, 5)
	for i := range postings {
		postings[i] = models.Posting{ID: int64(i + 1), Name: fmt.Sprintf("Thread %d", i+1)}
	}

	cl := &contentList{}
	cl.setPostings(postings)
	cl.setSize(80, 6)
	cl.moveDown()
	cl.moveDown()
	cl.moveDown()

	if cl.scrollOff != 1 {
		t.Errorf("scroll offset = %d, want 1 while three rows fit below the scrolled-off header", cl.scrollOff)
	}
	view := stripANSI(cl.view())
	for _, want := range []string{"Thread 2", "Thread 3", "Thread 4"} {
		if !strings.Contains(view, want) {
			t.Errorf("visible list is missing %q after its section header scrolled away: %q", want, view)
		}
	}
}

func TestContentListStylesSeenAndUnseenRows(t *testing.T) {
	unseen := models.Posting{
		ID:        200,
		Name:      "Quarterly planning kickoff",
		Summary:   "Draft agenda attached for review",
		CreatedAt: "2026-08-20T10:00:00Z",
		Creator:   models.Contact{Name: "Maria Gonzalez"},
	}
	seen := unseen
	seen.ID = 201
	seen.Seen = true

	cl := &contentList{}
	cl.setPostings([]models.Posting{{ID: 199, Name: "Cursor row", CreatedAt: "2026-08-20T09:00:00Z"}, unseen, seen})
	cl.setSize(100, 20)

	lines := strings.Split(cl.view(), "\n")
	newHeader := lines[0]
	cursorLine1 := lines[1]
	unseenLine1, unseenLine2 := lines[3], lines[4]
	seenHeader := lines[5]
	seenLine1, seenLine2 := lines[6], lines[7]

	if !strings.Contains(newHeader, "New for You") || !strings.Contains(newHeader, "\x1b[1;34m") {
		t.Errorf("the unseen section should open with a bold chrome header: %q", newHeader)
	}
	if !strings.Contains(seenHeader, "Previously Seen") {
		t.Errorf("the seen section should open with its header: %q", seenHeader)
	}
	if !strings.Contains(cursorLine1, "\x1b[1;94m") || !strings.Contains(cursorLine1, "│") {
		t.Errorf("cursor row should show the primary-colored bar: %q", cursorLine1)
	}
	if !strings.Contains(unseenLine1, "●") || !strings.Contains(unseenLine1, "\x1b[1;31m") {
		t.Errorf("unseen row should show the unread dot in the alert color: %q", unseenLine1)
	}
	if strings.Contains(seenLine1, "●") {
		t.Errorf("seen row should not show the unread dot: %q", seenLine1)
	}

	// Seen rows look the same as unseen rows — the section carries the state.
	if !strings.Contains(seenLine1, "\x1b[1;97m") || !strings.Contains(seenLine1, "\x1b[2m") {
		t.Errorf("seen row should keep the full row styling: %q", seenLine1)
	}
	if !strings.Contains(unseenLine2, "\x1b[2m") || !strings.Contains(seenLine2, "\x1b[2m") {
		t.Errorf("second lines should be faint secondary text in both sections: %q / %q", unseenLine2, seenLine2)
	}
}

func TestContentListMovesSeenPostingToItsSection(t *testing.T) {
	cl := &contentList{}
	cl.setPostings([]models.Posting{
		{ID: 1, Name: "Weekly release notes", CreatedAt: "2026-08-20T10:00:00Z"},
		{ID: 2, Name: "Invoice for July hosting", CreatedAt: "2026-08-20T09:00:00Z"},
		{ID: 3, Name: "Standup notes", CreatedAt: "2026-08-19T10:00:00Z", Seen: true},
	})
	cl.setSize(80, 20)

	cl.postings[0].Seen = true
	cl.resort()

	if cl.postings[0].ID != 2 || cl.postings[1].ID != 1 {
		t.Errorf("seen posting should move below the unseen ones: %+v", cl.postings)
	}
	if got := cl.selectedPosting(); got == nil || got.ID != 1 {
		t.Errorf("cursor should follow the moved posting: %+v", got)
	}
}

func TestContentListAlignsDateColumn(t *testing.T) {
	long := models.Posting{
		ID:        300,
		Name:      strings.Repeat("Quarterly planning update for the leadership group ", 3),
		Summary:   strings.Repeat("Agenda items and pre-reads for the quarterly review ", 3),
		CreatedAt: "2026-08-20T10:00:00Z",
		Creator:   models.Contact{Name: "Maria Gonzalez"},
	}
	short := models.Posting{
		ID:        301,
		Name:      "Lunch on Friday?",
		Summary:   "Trattoria at noon",
		CreatedAt: "2026-08-04T09:00:00Z",
		Seen:      true,
		Creator:   models.Contact{Name: "Ana Lucia Ortiz"},
	}

	cl := &contentList{}
	cl.setPostings([]models.Posting{long, short})
	cl.setSize(60, 20)

	// Lines: 0 "New for You", 1-2 long row, 3 "Previously Seen", 4-5 short row.
	lines := strings.Split(cl.view(), "\n")
	firstWidth, secondWidth := lipgloss.Width(lines[1]), lipgloss.Width(lines[4])
	if firstWidth != secondWidth {
		t.Errorf("dates should end in the same column: row widths %d and %d", firstWidth, secondWidth)
	}
	if firstWidth > 60 {
		t.Errorf("row width %d exceeds the list width 60", firstWidth)
	}

	dateCol := lipgloss.Width("Aug 20, 2026")
	for _, second := range []string{lines[2], lines[5]} {
		if lipgloss.Width(second) > firstWidth-dateCol-2 {
			t.Errorf("second line reaches into the date column: %q", second)
		}
	}
}

func TestChromeUsesBlueBoldConvention(t *testing.T) {
	if rule := renderRule(40, "Imbox"); !strings.Contains(rule, "\x1b[34m") {
		t.Errorf("rules should render in regular blue: %q", rule)
	}

	items := []navItem{{label: "Mail"}, {label: "Contacts"}, {label: "Calendar"}}
	row := renderNavRow(items, 0, true, 60, false)
	if !strings.Contains(row, "\x1b[1;33mMail") {
		t.Errorf("the selected tab should be bold yellow: %q", row)
	}
	if !strings.Contains(row, "\x1b[1;34mContacts") {
		t.Errorf("inactive tabs should be bold blue: %q", row)
	}
	if unfocused := renderNavRow(items, 0, false, 60, false); !strings.Contains(unfocused, "\x1b[1;94mMail") {
		t.Errorf("the selected tab in an unfocused row should use the less prominent primary color: %q", unfocused)
	}

	shortcuts := renderNavRow([]navItem{{shortcut: "I", label: "Imbox"}, {shortcut: "O", label: "Contacts"}}, 0, true, 60, false)
	if !strings.Contains(shortcuts, "\x1b[1;4;33;4mI\x1b[m") {
		t.Errorf("the selected tab should underline its shortcut letter: %q", shortcuts)
	}
	if !strings.Contains(shortcuts, "\x1b[1;4;34;4mo\x1b[m") {
		t.Errorf("inactive tabs should underline the shortcut letter inside the word: %q", shortcuts)
	}

	bar := newHelpBar(newStyles())
	bar.setWidth(60)
	bar.setBindings([]helpBinding{{"r", "reply"}})
	view := bar.view()
	if !strings.Contains(view, "\x1b[1;34mr") || !strings.Contains(view, "\x1b[34mreply") {
		t.Errorf("hotkeys should be bold blue with regular blue labels: %q", view)
	}
}

func TestTopRuleCentersHeyAndRightAlignsAccount(t *testing.T) {
	line := renderTopRule(80, "frank.castillo@example.com")
	if got := lipgloss.Width(line); got != 80 {
		t.Errorf("top rule width = %d, want 80", got)
	}
	if !strings.Contains(line, "\x1b[1;34mHEY") {
		t.Errorf("HEY should be bold chrome: %q", line)
	}
	if !strings.Contains(line, "\x1b[1;34mfrank.castillo@example.com\x1b[m \x1b[34m──") {
		t.Errorf("the account should be bold and sit before the closing rule: %q", line)
	}

	stripped := stripANSI(line)
	heyStart := lipgloss.Width(stripped[:strings.Index(stripped, "HEY")])
	if heyStart < 36 || heyStart > 40 {
		t.Errorf("HEY should be centered at width 80, found at column %d: %q", heyStart, stripped)
	}
	if !strings.HasSuffix(stripped, "frank.castillo@example.com ──") {
		t.Errorf("the account should be right-aligned: %q", stripped)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == '\x1b':
			inEscape = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestTruncateToWidth(t *testing.T) {
	if got := truncateToWidth("short", 10); got != "short" {
		t.Errorf("truncateToWidth kept = %q, want %q", got, "short")
	}
	if got := truncateToWidth("a much longer sentence", 10); got != "a much ..." {
		t.Errorf("truncateToWidth cut = %q, want %q", got, "a much ...")
	}
	if got := truncateToWidth("abcdef", 3); got != "" {
		t.Errorf("truncateToWidth narrow = %q, want empty", got)
	}
}

// --- View rendering ---

func TestRenderRuleBoundsLongLabels(t *testing.T) {
	for _, width := range []int{1, 10, 24} {
		rule := renderRule(width, "Search: a query that is much wider than the terminal (page 1)")
		if got := lipgloss.Width(rule); got != width {
			t.Errorf("renderRule(%d) width = %d, want %d: %q", width, got, width, rule)
		}
	}
}

func TestViewShowsHeader(t *testing.T) {
	m := modelWithBoxes()
	content := stripANSI(m.View().Content)
	if !strings.Contains(content, "HEY") {
		t.Error("View should contain HEY header")
	}
	if !strings.Contains(content, "Mail") {
		t.Error("View should contain Mail section")
	}
}

func TestViewShowsBoxNames(t *testing.T) {
	m := modelWithBoxes()
	v := m.View()
	if !strings.Contains(stripANSI(v.Content), "Imbox") {
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
