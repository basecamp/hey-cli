package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestScreenerSingleLineRowsKeepIdentitySubjectAndRightAlignedDate(t *testing.T) {
	row := screenerRow{id: 91, name: "Morgan Shaw", email: "morgan@example.com", subject: "October workshop schedule", summary: "Please bring the printed agenda.", trailing: "Nov 24"}
	pane := screenerPane{rows: []screenerRow{row}}
	compact := ansi.Strip(renderScreenerRows(&pane, 1, 160))
	if strings.Count(compact, "\n") != 1 {
		t.Fatal("pending sender should occupy one line")
	}
	for _, want := range []string{row.name, row.email, row.subject} {
		if !strings.Contains(compact, want) {
			t.Errorf("single-line sender lost %q", want)
		}
	}
	if strings.Contains(compact, row.summary) {
		t.Fatal("single-line row still includes the excerpt")
	}
	if !strings.HasSuffix(strings.TrimSuffix(compact, "\n"), row.trailing) || displayWidth(strings.TrimSuffix(compact, "\n")) != 160 {
		t.Fatal("date did not stay at the right edge")
	}
	for _, width := range []int{12, 20, 40, 80, 160} {
		pane.rows[0].name = "Alexandra Montgomery-Williams"
		pane.rows[0].email = "alexandra.montgomery@example.org"
		pane.rows[0].subject = strings.Repeat("旅程の相談 ", 20)
		for _, line := range strings.Split(ansi.Strip(renderScreenerRows(&pane, 1, width)), "\n") {
			if displayWidth(line) > width {
				t.Errorf("row overflowed %d columns: %q", width, line)
			}
		}
	}
	history := screenerPane{rows: []screenerRow{{id: 92, name: "Morgan Shaw", email: "morgan@example.com", verdict: "Screened in", trailing: "Screened in · Nov 24"}}}
	if got := ansi.Strip(renderScreenerRows(&history, 1, 160)); strings.Count(got, "\n") != 1 || !strings.Contains(got, "Screened in") {
		t.Fatal("history should show the sender and verdict on one line")
	}
}

func TestScreenerBudgetsOneLinePerSender(t *testing.T) {
	v := newModel().screenerView
	v.vc.width = 160
	v.vc.height = 18
	v.pending.setRows(make([]screenerRow, 40), "")
	if got := v.visibleRows(); got != 16 {
		t.Fatalf("visible senders = %d, want 16", got)
	}
	v.pending.cursor = 7
	v.pending.pageDown(v.visibleRows())
	if v.pending.cursor != 23 || v.pending.scroll != 16 {
		t.Fatalf("page down: cursor=%d scroll=%d", v.pending.cursor, v.pending.scroll)
	}
	v.vc.height = 10
	v.Resize(160, 10)
	if v.pending.cursor < v.pending.scroll || v.pending.cursor >= v.pending.scroll+v.visibleRows() {
		t.Fatal("resize hid the selected sender")
	}
}
