package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/hey-cli/internal/mail"
)

func TestActionPickersPreserveTheScreenBehindThem(t *testing.T) {
	for _, key := range []string{"v", "b", "n"} {
		for _, screen := range []string{"inbox", "thread", "search", "bundle", "seen"} {
			t.Run(key+"/"+screen, func(t *testing.T) {
				v := modalViewWithPostings(t)
				v.Resize(100, 30)
				v.postingList.toggleSelected()
				switch screen {
				case "thread":
					v.inThread = true
					v.threadPosting = v.postingList.postings[0]
					v.topicViewport.SetContent(strings.Repeat("Hotel newsletter body\n", 80))
					v.topicViewport.SetYOffset(10)
				case "search":
					v.searchActive = true
					v.searchList.setPostings([]mail.Posting{{ID: 201, TopicID: 301, Summary: "Search result"}})
				case "bundle":
					v.bundleActive = true
					v.bundleList.setPostings([]mail.Posting{{ID: 201, TopicID: 301, Summary: "Bundle thread"}})
				case "seen":
					v.seenActive = true
					v.seenList.setPostings([]mail.Posting{{ID: 201, TopicID: 301, Summary: "Seen thread"}})
				}
				// Bulk-selection policy is separate from these single-thread presentations.
				// Open the picker directly to exercise the same rendering from every screen.
				before := v.View()
				offset := v.topicViewport.YOffset()
				switch key {
				case "v":
					v.startMove()
				case "b":
					v.startFolderPicker()
				case "n":
					v.startCollectionPicker()
				}
				if v.modal == nil {
					t.Fatal("picker did not open")
				}
				rendered := stripANSI(v.View())
				if !strings.Contains(rendered, "╭") {
					t.Fatal("picker did not draw a frame")
				}
				if screen == "thread" && !strings.Contains(rendered, "Hotel newsletter body") {
					t.Fatal("thread disappeared behind picker")
				}
				if key == "v" {
					p := moveModal(v)
					frame := p.view(v.vc.styles, v.vc.width)
					if lipgloss.Width(frame) >= v.vc.width || lipgloss.Height(frame) >= v.vc.height {
						t.Fatal("short picker consumed the screen")
					}
				}
				v.HandleContentKey(keyPress("esc"))
				if v.modal != nil || v.View() != before || v.topicViewport.YOffset() != offset || len(v.postingList.selectedIDs()) != 1 {
					t.Fatal("dismissal changed the underlying screen, scroll, or selection")
				}
			})
		}
	}
}

func TestActionPickerFilteringKeepsTheCorrectTarget(t *testing.T) {
	v, recorded := mailWithTestServer(t, 200)
	v.boxes = append(v.boxes,
		mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Hotel receipts"},
		mail.Source{ID: 13, Kind: mail.KindFolder, Name: "Travel plans"},
		mail.Source{ID: 24, Kind: mail.KindCollection, Name: "Hotel receipts"},
		mail.Source{ID: 25, Kind: mail.KindCollection, Name: "Travel plans"})
	v.postingList.postings[0].TopicID = 501
	v.postingList.postings[0].Folders = []mail.Folder{{ID: 13, Name: "Travel plans"}}
	v.HandleContentKey(keyPress("b"))
	v.HandleContentKey(keyPress("down"))
	v.HandleContentKey(keyPress("HOTEL"))
	p := folderModal(v)
	if p.cursor != 0 || p.selected().folder.ID != 12 {
		t.Fatal("filter kept a stale cursor or wrong label")
	}
	if screen := stripANSI(v.View()); strings.Contains(screen, "[x] Travel plans") || !strings.Contains(screen, "Filter: HOTEL") {
		t.Fatal("filter did not update label choices")
	}
	cmd := v.HandleContentKey(keyPress("enter"))
	done := cmd()
	v.Update(done)
	if recorded.path != "/postings/filings.json" || recorded.body.FolderID == nil || *recorded.body.FolderID != 12 {
		t.Fatalf("wrong label operation: %s", recorded.path)
	}
	v.HandleContentKey(keyPress("n"))
	v.HandleContentKey(keyPress("Travel"))
	c := collectionModal(v)
	if c.selected() == nil || c.selected().ID != 25 {
		t.Fatal("collection filter used an unfiltered index")
	}
	v.HandleContentKey(keyPress("zzzz"))
	if c.selected() != nil || !strings.Contains(stripANSI(v.View()), "No matches") {
		t.Fatal("no-match filter retained an actionable collection")
	}
	if cmd := v.HandleContentKey(keyPress("enter")); cmd != nil || v.modal != c {
		t.Fatal("Enter on no matches dispatched or dismissed")
	}
	v.HandleContentKey(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if c.filter != "" || c.selected().ID != 24 {
		t.Fatal("clearing filter failed to restore choices")
	}
	v.HandleContentKey(keyPress("esc"))
	v.postingList.postings[0].Collections = []mail.Collection{{ID: 25, Name: "Travel plans"}}
	v.HandleContentKey(keyPress("n"))
	v.HandleContentKey(keyPress("travel"))
	collection := collectionModal(v)
	if collection.selected() == nil || collection.selected().ID != 25 {
		t.Fatal("filter targeted the wrong collection")
	}
	if !strings.Contains(stripANSI(v.View()), "[x] Travel plans") {
		t.Fatal("filtered collection lost membership mark")
	}
	cmd = v.HandleContentKey(keyPress("enter"))
	cmd()
	if recorded.method != "DELETE" || recorded.path != "/topics/501/collecting" || recorded.rawQueries[len(recorded.rawQueries)-1] != "collection_id=25" {
		t.Fatalf("wrong filtered collection operation: %s %s %v", recorded.method, recorded.path, recorded.rawQueries)
	}
}

func TestLabelCreationPreservesFilterOnBack(t *testing.T) {
	v := modalViewWithPostings(t)
	v.startFolderPicker()
	v.HandleContentKey(keyPress("missing"))
	p := folderModal(v)
	if p.selected().kind != folderPickerCreate {
		t.Fatal("create is unreachable with no matching labels")
	}
	v.HandleContentKey(keyPress("enter"))
	if !p.creating || !strings.Contains(stripANSI(v.View()), "Create label") {
		t.Fatal("create did not stay in modal")
	}
	p.input.SetValue(strings.Repeat("Holiday receipts ", 10))
	p.input.SetCursor(70)
	for _, width := range []int{24, 40, 80} {
		v.Resize(width, 24)
		frame := p.view(newStyles(), width)
		if lipgloss.Width(frame) > width {
			t.Fatalf("create label exceeds width %d: %d", width, lipgloss.Width(frame))
		}
	}
	v.HandleContentKey(keyPress("esc"))
	if v.modal != p || p.creating || p.filter != "missing" || p.selected().kind != folderPickerCreate {
		t.Fatal("back lost filtered choices")
	}
	v.HandleContentKey(keyPress("esc"))
	if v.modal != nil {
		t.Fatal("second Escape did not dismiss")
	}
}

func TestActionPickerFilteringHandlesUnicodeAndControls(t *testing.T) {
	filter := ""
	editPickerFilter(&filter, keyPress("café"))
	editPickerFilter(&filter, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if filter != "caf" {
		t.Fatalf("backspace split Unicode: %q", filter)
	}
	editPickerFilter(&filter, keyPress("\x1b]2;owned\a"))
	if filter != "caf" {
		t.Fatalf("filter retained control sequence: %q", filter)
	}
	editPickerFilter(&filter, keyPress(strings.Repeat("é", 200)))
	if len([]rune(filter)) != 80 {
		t.Fatal("filter exceeded its limit")
	}
	if !pickerNameMatches("Café\x1b]2;owned\a receipts", "CAFÉ") {
		t.Fatal("matching differs from displayed names")
	}
}

func TestActionPickerFitsSmallScreensAndScrollsAfterResize(t *testing.T) {
	sources := make([]mail.Source, 0, 40)
	for i := range 40 {
		sources = append(sources, mail.Source{ID: int64(i + 100), Kind: mail.KindFolder, Name: fmt.Sprintf("Label %02d", i)})
	}
	p := newFolderPicker(mail.Posting{Summary: "An estimate for kitchen cabinets"}, sources)
	for range 39 {
		p.moveCursor(keyPress("down"))
	}
	for _, size := range [][2]int{{80, 24}, {24, 8}, {16, 7}, {80, 24}} {
		p.resize(size[0], size[1])
		frame := p.view(newStyles(), size[0])
		if lipgloss.Width(frame) > size[0] || lipgloss.Height(frame) > size[1] {
			t.Fatalf("frame exceeds %v: %dx%d", size, lipgloss.Width(frame), lipgloss.Height(frame))
		}
		if size[0] >= 24 && !strings.Contains(stripANSI(frame), "Label 39") {
			t.Fatal("resize hid the selected label")
		}
	}
}

func TestActionPickersShowTheWrappedSubject(t *testing.T) {
	const subject = "Your updated travel itinerary and hotel reservation for the long weekend in New Orleans"
	posting := mail.Posting{ID: 100, Name: subject, Summary: "Plus, agent controls, single-use cards, and a clearer view of your spending"}
	sources := []mail.Source{{ID: 12, Kind: mail.KindFolder, Name: "Travel plans"}, {ID: 24, Kind: mail.KindCollection, Name: "Receipts"}}
	folders := newFolderPicker(posting, sources)
	collections := newCollectionMembershipPicker(posting, sources)
	move := newMovePicker(posting, []mail.Source{{ID: 2, Kind: mail.KindBox, Name: "The Feed"}}, mail.Source{ID: 1, Kind: mail.KindBox})
	for _, picker := range []interface {
		resize(int, int)
		view(styles, int) string
	}{folders, collections, move} {
		picker.resize(80, 24)
		frame := picker.view(newStyles(), 80)
		text := strings.Join(strings.Fields(stripANSI(frame)), " ")
		for _, line := range wrapText(subject, modalContentWidth(64)) {
			if !strings.Contains(text, line) {
				t.Fatalf("subject was clipped: %q", text)
			}
		}
		if strings.Contains(text, "agent controls") || strings.Contains(text, "...") {
			t.Fatalf("picker used a clipped message excerpt: %q", text)
		}
		if lipgloss.Width(frame) > 64 || lipgloss.Height(frame) > 24 {
			t.Fatal("wrapping overflowed the menu")
		}
	}
	if got := pickerThreadSubject(mail.Posting{Summary: "Hotel reservation confirmed"}); got != "Hotel reservation confirmed" {
		t.Fatalf("missing subject fallback: %q", got)
	}
}

func TestActionPickerLongContextLeavesChoicesUsable(t *testing.T) {
	for _, height := range []int{8, 12, 24} {
		frame := actionPickerFrame("Label thread", strings.Repeat("A very long subject ", 100), "travel", "Type to filter", []string{"Travel plans"}, 0, 40, height)
		if lipgloss.Height(frame) > height || !strings.Contains(stripANSI(frame), "Travel plans") {
			t.Fatal("long subject crowded out the selected action")
		}
	}
}

func TestActionPickersPreserveScrollThroughModel(t *testing.T) {
	for _, key := range []string{"v", "b"} {
		t.Run(key, func(t *testing.T) {
			testPickerScrollThroughModel(t, key)
		})
	}
}

func TestActionPickersPreserveListThroughModel(t *testing.T) {
	for _, key := range []string{"v", "b", "n"} {
		t.Run(key, func(t *testing.T) {
			m := modelWithBoxes()
			v := m.mailView
			v.boxes = append(v.boxes, mail.Source{ID: 12, Kind: mail.KindFolder, Name: "Travel plans"}, mail.Source{ID: 24, Kind: mail.KindCollection, Name: "Hotel receipts"})
			updated, _ := m.Update(keyPress("down"))
			m = updated.(model)
			posting := v.postingList.selectedPosting().ID
			before := m.View().Content
			updated, _ = m.Update(keyPress(key))
			m = updated.(model)
			if v.modal == nil {
				t.Fatal("picker did not open")
			}
			updated, _ = m.Update(keyPress("esc"))
			m = updated.(model)
			if v.modal != nil || v.postingList.selectedPosting().ID != posting || m.View().Content != before {
				t.Fatal("dismissal changed the list or cursor")
			}
		})
	}
}
