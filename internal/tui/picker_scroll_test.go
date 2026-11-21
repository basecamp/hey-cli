package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

func testPickerScrollThroughModel(t *testing.T, key string) {
	t.Helper()
	for _, position := range []string{"middle", "bottom"} {
		for _, selected := range []bool{false, true} {
			name := position
			if selected {
				name += "/selected-link"
			}
			t.Run(name, func(t *testing.T) {
				m := openLinkThreadThroughModel(t)
				v := m.mailView
				v.pendingMutations = 0
				v.entries[0].Body = htmlutil.ToMarkdown(`<p><a href="https://example.org/` + strings.Repeat("newsletter/", 12) + `">Read the newsletter</a></p>` + strings.Repeat("<p>Travel plans and hotel reservations.</p>", 100))
				v.rebuildTopicContent()
				updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
				m = updated.(model)
				if selected {
					updated, _ = m.Update(keyPress("tab"))
					m = updated.(model)
					footer, visible := m.linkFooter()
					if !visible || !strings.Contains(footer, "\n") {
						t.Fatal("expected a wrapping link footer")
					}
				}
				if position == "bottom" {
					for range 30 {
						updated, _ = m.Update(keyPress("pgdown"))
						m = updated.(model)
					}
				} else {
					for range 35 {
						updated, _ = m.Update(keyPress("down"))
						m = updated.(model)
					}
				}
				offset := v.topicViewport.YOffset()
				if offset == 0 {
					t.Fatal("thread did not scroll")
				}
				link, linkKey := v.selectedLink, v.selectedLinkKey
				before := m.View().Content
				updated, _ = m.Update(keyPress(key))
				m = updated.(model)
				if v.modal == nil {
					t.Fatal("picker did not open")
				}
				// A taller background may clamp at the bottom while the picker is open.
				want := min(offset, max(v.topicViewport.TotalLineCount()-v.topicViewport.Height(), 0))
				if got := v.topicViewport.YOffset(); got != want {
					t.Fatalf("opening picker offset = %d, want %d", got, want)
				}
				updated, _ = m.Update(keyPress("esc"))
				m = updated.(model)
				if v.modal != nil {
					t.Fatal("picker did not close")
				}
				if got := v.topicViewport.YOffset(); got != offset {
					t.Errorf("dismissal offset = %d, want %d", got, offset)
				}
				if v.selectedLink != link || v.selectedLinkKey != linkKey {
					t.Error("picker changed link selection")
				}
				if got := m.View().Content; got != before {
					t.Error("dismissal changed the rendered screen")
				}
			})
		}
	}
}
