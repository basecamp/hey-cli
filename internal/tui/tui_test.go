package tui

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/mail"
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

func testBoxes() []mail.Source {
	return []mail.Source{
		{Kind: mail.KindBox, ID: 1, Name: "Imbox", BoxKind: hey.BoxKindImbox},
		{Kind: mail.KindBox, ID: 2, Name: "The Feed", BoxKind: hey.BoxKindFeed},
		{Kind: mail.KindBox, ID: 3, Name: "Paper Trail", BoxKind: hey.BoxKindTrail},
	}
}

func testPostings() []mail.Posting {
	return []mail.Posting{
		{
			ID:        100,
			Summary:   "Hello world",
			CreatedAt: time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC),
			Seen:      false,
			Creator:   mail.Contact{Name: "Alice"},
		},
		{
			ID:        101,
			Summary:   "Meeting notes",
			CreatedAt: time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC),
			Seen:      true,
			Creator:   mail.Contact{Name: "Bob"},
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
	case " ", "space":
		k = tea.Key{Code: tea.KeySpace, Text: " "}
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

func TestTopicRequestSwitchesToMailAndStartsTheThreadRead(t *testing.T) {
	m := sizedModel()
	m.mailSourcesLoaded = true
	m.section = sectionCalendar
	m.activeView = m.calendarView

	updated, cmd := m.Update(TopicRequest{TopicID: 5511})
	m = updated.(model)
	if m.section != sectionMail || m.activeView != m.mailView || m.focus != rowContent {
		t.Fatalf("topic request left the TUI at section=%d focus=%d view=%T", m.section, m.focus, m.activeView)
	}
	if cmd == nil {
		t.Fatal("topic request did not start a thread read")
	}
}

func TestTopicRequestSanitizesAnUntrustedTitle(t *testing.T) {
	m := sizedModel()
	mailView, _ := mailWithTestServer(t, http.StatusOK)
	m.mailView = mailView
	m.activeView = mailView
	m.mailSourcesLoaded = true
	m.loading = true

	_, cmd := m.Update(TopicRequest{TopicID: 100, Title: "\x1b[31mRed\x1b[0m\nalert"})
	msg := runCmd(cmd)
	stamped, ok := msg.(viewGenerationMsg)
	if !ok {
		t.Fatalf("topic request returned %T, want viewGenerationMsg", msg)
	}
	loaded, ok := stamped.msg.(topicLoadedMsg)
	if !ok {
		t.Fatalf("topic request returned %T, want topicLoadedMsg", stamped.msg)
	}
	if loaded.title != "Red alert" {
		t.Fatalf("sanitized title = %q, want %q", loaded.title, "Red alert")
	}
}

func TestTopicRequestWaitsForMailSourcesBeforeReading(t *testing.T) {
	m := sizedModel()
	updated, cmd := m.Update(TopicRequest{TopicID: 5511})
	m = updated.(model)
	if cmd != nil || m.pendingTopic == nil {
		t.Fatalf("topic started before mail sources loaded: cmd=%v pending=%v", cmd != nil, m.pendingTopic != nil)
	}

	updated, cmd = m.Update(mailSourcesLoadedMsg{})
	m = updated.(model)
	if cmd == nil || m.pendingTopic != nil {
		t.Fatalf("topic did not start after mail sources loaded: cmd=%v pending=%v", cmd != nil, m.pendingTopic != nil)
	}
}

func TestQuestionMarkTogglesHelpAndResizesContent(t *testing.T) {
	m := modelWithBoxes()
	visibleHeight := m.vc.height
	visibleView := m.View().Content
	var saved []bool
	m.saveHelpHidden = func(hidden bool) error {
		saved = append(saved, hidden)
		return nil
	}

	updated, _ := m.Update(keyPress("?"))
	m = updated.(model)
	if !m.help.hidden || m.help.view() != "" {
		t.Error("question mark should hide shortcut help")
	}
	if want := m.height - headerHeight; m.vc.height != want {
		t.Errorf("content height after hiding help = %d, want %d", m.vc.height, want)
	}
	lines := strings.Split(strings.TrimRight(stripANSI(m.View().Content), "\n"), "\n")
	if last := lines[len(lines)-1]; last == strings.Repeat("─", m.width) {
		t.Error("hiding help left its bottom divider on screen")
	}
	if !slices.Equal(saved, []bool{true}) {
		t.Errorf("saved preferences = %v, want [true]", saved)
	}

	updated, _ = m.Update(keyPress("?"))
	m = updated.(model)
	if m.help.hidden || m.help.view() == "" {
		t.Error("question mark should restore shortcut help")
	}
	if m.vc.height != visibleHeight {
		t.Errorf("content height after restoring help = %d, want %d", m.vc.height, visibleHeight)
	}
	if restored := m.View().Content; restored != visibleView {
		t.Error("restoring help did not restore the original layout")
	}
	if !slices.Equal(saved, []bool{true, false}) {
		t.Errorf("saved preferences = %v, want [true false]", saved)
	}
	if !slices.Contains(m.help.bindings, helpBinding{"?", "toggle help"}) {
		t.Errorf("visible shortcut help does not explain its toggle: %v", m.help.bindings)
	}
}

func TestQuestionMarkRemainsTextInsideSearch(t *testing.T) {
	m := modelWithBoxes()
	updated, _ := m.Update(keyPress("/"))
	m = updated.(model)

	updated, _ = m.Update(keyPress("?"))
	m = updated.(model)
	form := searchModal(m.mailView)
	if form == nil {
		t.Fatal("search form closed after typing a question mark")
	}
	if form.input.Value() != "?" {
		t.Errorf("search value = %q, want question mark", form.input.Value())
	}
	if m.help.hidden {
		t.Error("typing a question mark in search should not toggle help")
	}
}

func TestQuestionMarkTogglesHelpInsideScreener(t *testing.T) {
	m := modelWithBoxes()
	m.activeView = m.screenerView
	m.updateHelpBindings()

	updated, _ := m.Update(keyPress("?"))
	m = updated.(model)
	if !m.help.hidden {
		t.Error("question mark should toggle help inside The Screener")
	}
}

func TestHelpPreferenceFailureIsShown(t *testing.T) {
	m := modelWithBoxes()
	m.saveHelpHidden = func(bool) error { return errors.New("read-only file system") }

	updated, _ := m.Update(keyPress("?"))
	m = updated.(model)
	if !strings.Contains(stripANSI(m.help.view()), "Could not save the help preference") {
		t.Errorf("help preference failure is not visible: %q", m.help.view())
	}
}

func TestModelResizesContentWhenThreadHelpChangesHeight(t *testing.T) {
	for _, width := range []int{32, 40, 48, 64, 80, 100} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := modelWithBoxes()
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			m = updated.(model)
			m.mailView.requests.id = 7
			m.mailView.requests.kind = mailRequestTopic
			updated, _ = m.Update(topicLoadedMsg{
				requestID:   7,
				boxID:       1,
				topicID:     100,
				title:       "Quarterly planning",
				entries:     []mail.Entry{{ID: 501, Creator: mail.Contact{Name: "Alice"}}},
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
	boxes := []mail.Source{
		{Kind: mail.KindBox, ID: 1, Name: "The Feed"},
		{Kind: mail.KindBox, ID: 2, Name: "Imbox"},
		{Kind: mail.KindBox, ID: 3, Name: "Custom Box"},
		{Kind: mail.KindBox, ID: 4, Name: "Paper Trail"},
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
	boxes := []mail.Source{
		{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"},
		{Kind: mail.KindFolder, ID: 1, Name: "Imbox"},
	}
	ordered := orderBoxes(boxes)
	if len(ordered) != 2 || ordered[0].Kind != mail.KindBox || ordered[1].Kind != mail.KindFolder {
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
		sources: []mail.Source{
			{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"},
			{ID: 12, Kind: mail.KindFolder, Name: "Receipts"},
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
	m.mailView.boxes = []mail.Source{{Kind: mail.KindBox, ID: 1, BoxKind: hey.BoxKindImbox, Name: "Imbox"}}
	m.mailView.boxIndex = 0
	m.mailView.requests.id = 2
	m.mailView.requests.kind = mailRequestPostings
	m.mailView.requests.loading = true
	m.mailView.notice = "Current mail state"

	updated, cmd := m.Update(postingsLoadedMsg{
		requestID:  1,
		boxID:      1,
		sourceKind: mail.KindBox,
		err:        fmt.Errorf("stale failure"),
	})
	m = updated.(model)
	if cmd != nil || m.mailView.notice != "Current mail state" || !m.mailView.requests.loading || m.mailView.requests.id != 2 {
		t.Errorf("stale error changed inactive Mail: notice=%q loading=%v request=%d", m.mailView.notice, m.mailView.requests.loading, m.mailView.requests.id)
	}

	updated, cmd = m.Update(postingsLoadedMsg{
		requestID:  2,
		boxID:      1,
		sourceKind: mail.KindBox,
		err:        fmt.Errorf("current failure"),
	})
	m = updated.(model)
	if cmd != nil || m.mailView.notice != "Could not load mail: current failure" || m.mailView.requests.loading {
		t.Errorf("current error state = notice:%q loading:%v", m.mailView.notice, m.mailView.requests.loading)
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
	m.mailView.requests.loading = true

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
		mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Receipts"},
	))
	m.mailView.boxIndex = len(m.mailView.tabBoxIndexes()) - 1
	m.focus = rowSubnav
	m.updateHelpBindings()

	updated, _ := m.Update(keyPress("right"))
	m = updated.(model)

	if labelsModal(m.mailView) == nil {
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

func TestMovePickerOwnsNavigationKeys(t *testing.T) {
	m := modelWithBoxes()
	m.focus = rowContent

	updated, _ := m.Update(keyPress("v"))
	m = updated.(model)
	if moveModal(m.mailView) == nil || !m.mailView.CapturingInput() {
		t.Fatal("v should open the move picker")
	}

	updated, _ = m.Update(keyPress("tab"))
	m = updated.(model)
	if m.focus != rowContent || moveModal(m.mailView) == nil {
		t.Error("tab should remain inside the move picker")
	}

	updated, _ = m.Update(keyPress("esc"))
	m = updated.(model)
	if moveModal(m.mailView) != nil || m.mailView.CapturingInput() {
		t.Error("escape should close the move picker")
	}
}

func TestSearchFormOwnsNavigationKeys(t *testing.T) {
	m := modelWithBoxes()
	m.focus = rowContent

	updated, _ := m.Update(keyPress("/"))
	m = updated.(model)
	if searchModal(m.mailView) == nil || !m.mailView.CapturingInput() {
		t.Fatal("/ should open the search form")
	}

	updated, _ = m.Update(keyPress("tab"))
	m = updated.(model)
	if m.focus != rowContent || searchModal(m.mailView) == nil {
		t.Error("tab should remain inside the search form")
	}

	updated, _ = m.Update(keyPress("esc"))
	m = updated.(model)
	if searchModal(m.mailView) != nil || m.mailView.CapturingInput() {
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
	m.mailView.searchList.setPostings([]mail.Posting{{ID: 10, TopicID: 100, Name: "Hello world"}})
	m.mailView.requestTopic(m.mailView.currentBoxID(), 100, 10, "Hello world")
	m.loading = true

	updated, _ := m.Update(keyPress("esc"))
	result := updated.(model)
	if !result.mailView.searchActive || result.mailView.searchQuery != "quarterly planning" || len(result.mailView.searchList.postings) != 1 {
		t.Error("escape during thread load should preserve search results")
	}
	if result.mailView.requests.loading || result.loading {
		t.Error("escape should cancel the pending thread load")
	}
}

func TestQExitsSearchDuringPendingResult(t *testing.T) {
	m := modelWithBoxes()
	m.mailView.searchActive = true
	m.mailView.searchQuery = "quarterly planning"
	m.mailView.searchList.setPostings([]mail.Posting{{ID: 10, TopicID: 100, Name: "Hello world"}})
	m.mailView.requestTopic(m.mailView.currentBoxID(), 100, 10, "Hello world")
	m.loading = true

	updated, _ := m.Update(keyPress("q"))
	result := updated.(model)
	if result.mailView.searchActive || len(result.mailView.searchList.postings) != 0 {
		t.Error("q during thread load should exit search results")
	}
	if result.mailView.requests.loading || result.loading {
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
	if !m.mailView.requests.loading || !m.loading {
		t.Fatal("reply should start loading")
	}

	updated, _ = m.Update(keyPress("q"))
	result := updated.(model)
	if result.mailView.requests.loading || result.loading {
		t.Error("q should stop a canceled reply load")
	}
}

func TestQExitsPendingReplyLoadFromPostingList(t *testing.T) {
	m := modelWithBoxes()

	updated, cmd := m.Update(keyPress("r"))
	m = updated.(model)
	if cmd == nil || !m.mailView.requests.loading || !m.loading {
		t.Fatal("reply from the posting list should start loading")
	}
	requestID := m.mailView.requests.id

	updated, _ = m.Update(keyPress("q"))
	m = updated.(model)
	if m.mailView.requests.loading || m.loading {
		t.Error("q should stop a reply started from the posting list")
	}
	if m.mailView.requests.kind != mailRequestNone || m.mailView.requests.requestCancel != nil {
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
	if composeModal(m.mailView) != nil {
		t.Error("a canceled reply load should not open the reply form")
	}
}

func TestQExitsPendingForwardLoadFromPostingList(t *testing.T) {
	m := modelWithBoxes()

	updated, cmd := m.Update(keyPress("f"))
	m = updated.(model)
	if cmd == nil || !m.mailView.requests.loading || m.mailView.requests.kind != mailRequestForward {
		t.Fatal("forward from the posting list should start loading")
	}
	requestID := m.mailView.requests.id

	updated, _ = m.Update(keyPress("q"))
	m = updated.(model)
	if m.mailView.requests.loading || m.loading {
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
	if composeModal(m.mailView) != nil {
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
	postings := make([]mail.Posting, 5)
	for i := range postings {
		postings[i] = mail.Posting{ID: int64(i + 1), Name: fmt.Sprintf("Thread %d", i+1)}
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

// HEY serves every timestamp in UTC — its JSON requests set Time.zone — so the day a row
// shows is the reader's own day or the wrong one. A thread that arrived late in the
// evening must not read as the day after.
func TestFormatDisplayDateReadsTheDayInTheReadersZone(t *testing.T) {
	lateEvening := time.Date(2026, 8, 20, 23, 30, 0, 0, time.Local)

	if got := formatDisplayDate(lateEvening); got != "Aug 20, 2026" {
		t.Errorf("date = %q, want Aug 20, 2026", got)
	}
	if got := formatDisplayDate(lateEvening.UTC()); got != "Aug 20, 2026" {
		t.Errorf("the same instant as HEY serves it = %q, want Aug 20, 2026", got)
	}
	if got := formatDisplayDate(time.Time{}); got != "" {
		t.Errorf("a posting with no timestamp = %q, want no date", got)
	}
}

func TestContentListStylesSeenAndUnseenRows(t *testing.T) {
	unseen := mail.Posting{
		ID:        200,
		Name:      "Quarterly planning kickoff",
		Summary:   "Draft agenda attached for review",
		CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC),
		Creator:   mail.Contact{Name: "Maria Gonzalez"},
	}
	seen := unseen
	seen.ID = 201
	seen.Seen = true

	cl := &contentList{}
	cl.setPostings([]mail.Posting{{ID: 199, Name: "Cursor row", CreatedAt: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)}, unseen, seen})
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
	if !strings.Contains(cursorLine1, "\x1b[1;94mCursor row") || !strings.Contains(cursorLine1, "│") {
		t.Errorf("cursor row should keep all text in the contrast-checked primary color: %q", cursorLine1)
	}
	if !strings.Contains(unseenLine1, "●") || !strings.Contains(unseenLine1, "\x1b[1;31m") {
		t.Errorf("unseen row should show the unread dot in the alert color: %q", unseenLine1)
	}
	if strings.Contains(seenLine1, "●") {
		t.Errorf("seen row should not show the unread dot: %q", seenLine1)
	}

	// Seen rows look the same as unseen rows — the section carries the state.
	// The sender takes the hyperlink color; the date shares the subject color.
	if !strings.Contains(seenLine1, "\x1b[1;97m") || !strings.Contains(seenLine1, "\x1b[97m") {
		t.Errorf("seen row should keep the full row styling: %q", seenLine1)
	}
	if !strings.Contains(unseenLine2, "\x1b[1;96m") || !strings.Contains(seenLine2, "\x1b[1;96m") {
		t.Errorf("sender names should be bold hyperlink cyan in both sections: %q / %q", unseenLine2, seenLine2)
	}
	if !strings.Contains(unseenLine2, "\x1b[2m") || !strings.Contains(seenLine2, "\x1b[2m") {
		t.Errorf("second lines should be faint secondary text in both sections: %q / %q", unseenLine2, seenLine2)
	}
}

func TestContentListMovesSeenPostingToItsSection(t *testing.T) {
	cl := &contentList{}
	cl.setPostings([]mail.Posting{
		{ID: 1, Name: "Weekly release notes", CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)},
		{ID: 2, Name: "Invoice for July hosting", CreatedAt: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)},
		{ID: 3, Name: "Standup notes", CreatedAt: time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC), Seen: true},
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

func TestContentListMovesSeenAndBubbledUpPostingsToNewForYou(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		posting mail.Posting
	}{
		{"seen", mail.Posting{ID: 3, Name: "Previously read", Seen: true}},
		{"bubbled up", mail.Posting{ID: 3, Name: "Bubbled reminder", BubbledUp: true}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cl := &contentList{}
			cl.setPostings([]mail.Posting{
				{ID: 1, Name: "Already new"},
				testCase.posting,
				{ID: 4, Name: "Older read", Seen: true},
				{ID: 9, Name: "Keep bubbled", BubbledUp: true},
			})
			cl.cursor = slices.IndexFunc(cl.postings, func(p mail.Posting) bool { return p.ID == 3 })

			cl.markUnseen(cl.cursor)

			wantOrder := []int64{9, 3, 1, 4}
			gotOrder := make([]int64, len(cl.postings))
			for i, posting := range cl.postings {
				gotOrder[i] = posting.ID
			}
			index := slices.IndexFunc(cl.postings, func(p mail.Posting) bool { return p.ID == 3 })
			if !slices.Equal(gotOrder, wantOrder) || cl.postings[index].Seen || cl.postings[index].BubbledUp {
				t.Errorf("posting order after unseen = %v, want %v; postings=%+v", gotOrder, wantOrder, cl.postings)
			}
			if selected := cl.selectedPosting(); selected == nil || selected.ID != 3 {
				t.Errorf("cursor did not follow posting: %+v", selected)
			}
		})
	}
}

func TestContentListOpensWithTheBubbledUpSection(t *testing.T) {
	cl := &contentList{}
	cl.setPostings([]mail.Posting{
		{ID: 1, Name: "Weekly release notes", CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)},
		{ID: 2, Name: "Standup notes", CreatedAt: time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC), Seen: true},
		{ID: 3, Name: "Invoice for July hosting", CreatedAt: time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC), BubbledUp: true},
	})
	cl.setSize(80, 20)

	if cl.postings[0].ID != 3 || cl.postings[1].ID != 1 || cl.postings[2].ID != 2 {
		t.Errorf("bubbled up postings should sort above the unseen and seen ones: %+v", cl.postings)
	}

	lines := strings.Split(stripANSI(cl.view()), "\n")
	if !strings.HasPrefix(lines[0], "Bubbled Up") {
		t.Errorf("the list should open with the bubbled up header: %q", lines[0])
	}
	if !strings.Contains(lines[1], "Invoice for July hosting") || !strings.Contains(lines[1], "●") {
		t.Errorf("a bubbled up row should show the unread dot: %q", lines[1])
	}
	if !strings.HasPrefix(lines[3], "New for You") || !strings.HasPrefix(lines[6], "Previously Seen") {
		t.Errorf("the other sections should follow with their headers: %q / %q", lines[3], lines[6])
	}
}

func TestContentListMovesSeenBubbledUpPostingToItsSection(t *testing.T) {
	cl := &contentList{}
	cl.setPostings([]mail.Posting{
		{ID: 1, Name: "Invoice for July hosting", CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC), BubbledUp: true},
		{ID: 2, Name: "Weekly release notes", CreatedAt: time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)},
	})
	cl.setSize(80, 20)

	cl.markSeen(0)

	if cl.postings[0].ID != 2 || cl.postings[1].ID != 1 {
		t.Errorf("a seen bubbled up posting should move below the unseen ones: %+v", cl.postings)
	}
	if cl.postings[1].BubbledUp {
		t.Error("marking a bubbled up posting seen should clear its bubbled up state")
	}
	if got := cl.selectedPosting(); got == nil || got.ID != 1 {
		t.Errorf("cursor should follow the moved posting: %+v", got)
	}
}

func TestContentListAlignsDateColumn(t *testing.T) {
	long := mail.Posting{
		ID:        300,
		Name:      strings.Repeat("Quarterly planning update for the leadership group ", 3),
		Summary:   strings.Repeat("Agenda items and pre-reads for the quarterly review ", 3),
		CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC),
		Creator:   mail.Contact{Name: "Maria Gonzalez"},
	}
	short := mail.Posting{
		ID:        301,
		Name:      "Lunch on Friday?",
		Summary:   "Trattoria at noon",
		CreatedAt: time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC),
		Seen:      true,
		Creator:   mail.Contact{Name: "Ana Lucia Ortiz"},
	}

	cl := &contentList{}
	cl.setPostings([]mail.Posting{long, short})
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
	// The cursor row (row 0) pads its second line to the full row width so
	// the selection background also covers the space under the date.
	if lipgloss.Width(lines[2]) != firstWidth {
		t.Errorf("cursor row second line width = %d, want the full row width %d", lipgloss.Width(lines[2]), firstWidth)
	}
	if lipgloss.Width(lines[5]) > firstWidth-dateCol-2 {
		t.Errorf("second line text reaches into the date column: %q", lines[5])
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
