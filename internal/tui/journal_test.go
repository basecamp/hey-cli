package tui

import (
	"testing"
	"time"
)

func journalWithEntry() *journalView {
	v := newJournalView(testVC())
	v.Init()
	today := v.dates[v.dateIndex]
	v.Update(journalDetailMsg{title: today, body: "Today was great"})
	return v
}

// --- Init ---

func TestJournalViewInitFetchesEntry(t *testing.T) {
	v := newJournalView(testVC())
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("Init should return a fetch command")
	}
	if !v.loading {
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

func TestJournalViewHandlesDetailLoaded(t *testing.T) {
	v := newJournalView(testVC())
	v.Init() // sets dateIndex to today
	v.loading = true

	today := v.dates[v.dateIndex]
	_, consumed := v.Update(journalDetailMsg{title: today, body: "Entry body"})
	if !consumed {
		t.Error("journalDetailMsg should be consumed")
	}
	if v.loading {
		t.Error("loading should be false after detail loaded")
	}
	if !v.inThread {
		t.Error("should be in thread after detail loaded")
	}
}

func TestJournalViewIgnoresStaleResponse(t *testing.T) {
	v := newJournalView(testVC())
	v.Init()
	v.loading = true

	// Simulate a response for a different date than currently selected
	staleDate := "1999-01-01"
	_, consumed := v.Update(journalDetailMsg{title: staleDate, body: "old content"})
	if !consumed {
		t.Error("stale journalDetailMsg should still be consumed")
	}
	if !v.loading {
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
	if !v.loading {
		t.Error("SubnavLeft should set loading")
	}

	v.loading = false
	v.SubnavRight()
	if v.dateIndex != lastIdx {
		t.Errorf("after SubnavRight: dateIndex = %d, want %d", v.dateIndex, lastIdx)
	}

	// Can't go right past the end
	v.loading = false
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

func TestJournalViewHelpBindingsEmpty(t *testing.T) {
	v := journalWithEntry()
	bindings := v.HelpBindings()
	if len(bindings) != 0 {
		t.Errorf("journal should have no extra bindings, got %d", len(bindings))
	}
}
