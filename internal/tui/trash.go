package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-cli/internal/mail"
)

type trashRestoredMsg struct {
	topicID int64
	err     error
}

func (v *mailView) trashIndex() int {
	for i, source := range v.boxes {
		if source.Kind == mail.KindTrash {
			return i
		}
	}
	return -1
}

func (v *mailView) inTrash() bool {
	if v.inThread {
		return v.threadFromTrash
	}
	return v.currentSourceKind() == mail.KindTrash && !v.searchActive && !v.bundleActive && !v.seenActive
}

// Trash rows are topics, not posting IDs. Only restore is a mutation here.
func (v *mailView) handleTrashKey(key string) (tea.Cmd, bool) {
	if !v.inTrash() {
		return nil, false
	}
	if v.trashRestoreID != 0 && key == "enter" {
		return nil, true
	}
	if key == "i" || key == "I" {
		return v.restoreTrashTopic(), true
	}
	switch key {
	case "r", "R", "f", "F", "v", "V", "b", "B", "n", "N", "w", "W", "e", "E", "u", "U", "l", "a", "A", "d", "D", "p", "P", "t", "T", "!", "-", "+", "ctrl+b", "ctrl+u", "space", " ", "x":
		v.notice = "Restore this thread before changing it"
		return nil, true
	}
	return nil, false
}

func (v *mailView) restoreTrashTopic() tea.Cmd {
	if v.requests.loading || v.trashRestoreID != 0 {
		return nil
	}
	selected := v.actionPosting()
	if v.inThread && v.threadFromTrash {
		selected = &v.threadPosting
	}
	if selected == nil || selected.TopicID == 0 {
		return nil
	}
	id := selected.TopicID
	v.trashRestoreID = id
	v.pendingMutations++
	sdk, ctx := v.vc.sdk, v.vc.ctx
	return func() tea.Msg { return trashRestoredMsg{topicID: id, err: sdk.Topics().Restore(ctx, id)} }
}

func (v *mailView) trashRestored(msg trashRestoredMsg) tea.Cmd {
	v.finishMutation()
	v.trashRestoreID = 0
	if msg.err != nil {
		return func() tea.Msg { return errMsg{msg.err} }
	}
	if v.inThread && v.threadFromTrash && v.topicID == msg.topicID {
		v.ExitThread()
	}
	// A live read begun before the restore may still contain this topic, even
	// if the reader switched away and returns to Trash before the read arrives.
	v.liveRequestID++
	if v.currentSourceKind() != mail.KindTrash {
		return notify("Thread restored")
	}
	// Navigation already invalidates pages; only cancel paging here in Trash.
	v.moreRequestID++
	v.postingPaging.loading = false
	if index := postingIndexIn(v.postingList.postings, msg.topicID); index >= 0 {
		v.removePostingAt(index)
	}
	if v.requests.kind == mailRequestPostings && v.requests.loading {
		v.requests.cancel()
		return tea.Batch(notify("Thread restored"), v.reloadPostings())
	}
	return tea.Batch(notify("Thread restored"), v.loadMorePostings())
}

func (v *mailView) trashHelp(bindings []helpBinding) []helpBinding {
	if !v.inTrash() {
		return bindings
	}
	filtered := make([]helpBinding, 0, len(bindings))
	for _, binding := range bindings {
		switch strings.ToLower(binding.key) {
		case "r", "f", "v", "b", "n", "w", "e", "u", "l", "a", "d", "p", "t", "!", "-", "+", "ctrl+b", "ctrl+u", "space/x":
			continue
		case "i":
			binding.desc = "restore"
		}
		filtered = append(filtered, binding)
	}
	if v.inThread {
		filtered = append(filtered, helpBinding{"i", "restore"})
	}
	return filtered
}
