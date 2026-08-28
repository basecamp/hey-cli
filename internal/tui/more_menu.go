package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/mail"
)

// moreMenu is the thread view's More menu, as in the HEY apps: m swaps the help bar
// for the actions a thread offers beyond reading it, each behind the key the same
// action answers to on the posting list. The thread stays on screen and every thread
// key keeps its meaning underneath — the menu is a mode of the help bar, not a box
// over the mail. It holds the posting the thread was opened from, so the actions keep
// their aim even while the list underneath refreshes.
type moreMenu struct {
	plainModal

	posting mail.Posting
	// organizes is whether label and move are offered: only a list that knows the
	// thread's box and labels can back their pickers.
	organizes bool
}

func newMoreMenu(posting mail.Posting, organizes bool) *moreMenu {
	return &moreMenu{posting: posting, organizes: organizes}
}

// handleKey commits an action by its key and sends every other key through the
// thread's own routing, so having the menu up never gets in the way of reading.
func (m *moreMenu) handleKey(view *mailView, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if msg.Key().Code == tea.KeyEscape {
		return nil, false
	}
	switch key := strings.ToLower(msg.String()); key {
	case "f":
		return view.loadForwardContext(view.topicID, view.topicName), false
	case "b":
		if m.organizes {
			cmd, open := view.openFolderPicker(m.posting)
			// A failed label discovery leaves its "press b to retry" notice over the
			// thread, where bare b means nothing — the retry keeps the menu up so
			// the advertised key stays bound to retrying.
			return cmd, open || view.folderDiscoveryErr != ""
		}
	case "v":
		if m.organizes {
			return nil, view.openMovePicker(m.posting)
		}
	case "t":
		return view.trashOpenThread(m.posting.ID), false
	case "q", "m":
		return nil, false
	}
	return view.handleThreadKey(msg), true
}

func (m *moreMenu) draw(view *mailView) string {
	return view.threadView()
}

func (m *moreMenu) helpBindings() []helpBinding {
	bindings := []helpBinding{{"f", "forward"}}
	if m.organizes {
		bindings = append(bindings, helpBinding{"b", "label"}, helpBinding{"v", "move"})
	}
	return append(bindings, helpBinding{"t", "trash"}, helpBinding{"esc/q", "back"})
}
