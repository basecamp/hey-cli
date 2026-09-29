package tui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// openedPreview presses space on the first sender and lands the email it reads.
func openedPreview(t *testing.T) (*screenerView, *screenerServerState) {
	t.Helper()
	view, state := loadedScreener(t)
	view.vc.width = 80
	view.vc.height = 20
	loaded, ok := runCmd(view.HandleContentKey(keyPress("space"))).(screenerPreviewLoadedMsg)
	if !ok {
		t.Fatalf("space returned %T, want screenerPreviewLoadedMsg", loaded)
	}
	if loaded.err != nil {
		t.Fatalf("preview read failed: %v", loaded.err)
	}
	view.Update(loaded)
	return view, state
}

// Space shows the whole of what the sender wrote, where the row only has room for the
// first line of it.
func TestScreenerSpacePreviewsTheSendersEmail(t *testing.T) {
	view, state := openedPreview(t)

	requests := state.snapshot()
	if last := requests[len(requests)-1]; last.method != http.MethodGet || last.path != "/messages/501.json" {
		t.Errorf("preview read = %+v, want the sender's most recent email", last)
	}
	screen := plainText(view.View())
	for _, want := range []string{"Jane Doe <jane@example.com>", "Quarterly planning", "walk through the budget before the board meeting", "Jane"} {
		if !strings.Contains(screen, want) {
			t.Errorf("preview is missing %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "Bob Smith") {
		t.Errorf("the queue should be covered by the preview:\n%s", screen)
	}
	if !hasBinding(view.HelpBindings(), "space/esc") {
		t.Errorf("help bar should say how to close the preview: %v", view.HelpBindings())
	}
}

// Space, escape and q all put the preview away and leave the reader in The Screener.
func TestScreenerPreviewClosesBackToTheQueue(t *testing.T) {
	for _, key := range []string{"space", "esc", "q"} {
		t.Run(key, func(t *testing.T) {
			view, _ := openedPreview(t)

			if cmd := view.HandleContentKey(keyPress(key)); cmd != nil {
				t.Errorf("%s returned %T, want the Screener to stay open", key, runCmd(cmd))
			}
			if view.preview != nil {
				t.Fatalf("%s left the preview open", key)
			}
			if screen := plainText(view.View()); !strings.Contains(screen, "Bob Smith") {
				t.Errorf("the queue should be back:\n%s", screen)
			}
		})
	}
}

// Yes and No answer from the preview, for the sender being previewed, and close it.
func TestScreenerAnswersFromThePreview(t *testing.T) {
	view, state := openedPreview(t)

	done, ok := runCmd(view.HandleContentKey(keyPress("n"))).(screenerDecisionDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("n returned %#v", done)
	}
	if view.preview != nil {
		t.Error("answering should close the preview")
	}
	requests := state.snapshot()
	last := requests[len(requests)-1]
	if last.method != http.MethodPatch || last.path != "/clearances/91.json" || !strings.Contains(last.body, `"status":"denied"`) {
		t.Errorf("screen out request = %+v", last)
	}
}

// An answer for a preview the reader already closed is dropped, rather than opening a
// preview nobody asked for.
func TestScreenerDropsAPreviewThatArrivesAfterClosing(t *testing.T) {
	view, _ := loadedScreener(t)
	cmd := view.HandleContentKey(keyPress("space"))
	view.HandleContentKey(keyPress("esc"))

	view.Update(runCmd(cmd))

	if view.preview != nil {
		t.Error("a late answer reopened the preview")
	}
}

// A sender HEY served no email for has nothing to preview, and the reader is told so.
func TestScreenerSaysWhenThereIsNothingToPreview(t *testing.T) {
	view, _ := loadedScreener(t)
	view.pending.rows[0].entryID = 0

	if cmd := view.HandleContentKey(keyPress("space")); cmd != nil {
		t.Errorf("space made a request for a sender with no email")
	}
	if view.preview != nil || view.notice != "HEY sent nothing to preview for Jane Doe" {
		t.Errorf("preview=%v notice=%q", view.preview, view.notice)
	}
}

// Screener History has decisions, not emails, so space does nothing there.
func TestScreenerHistoryHasNoPreview(t *testing.T) {
	view, _ := loadedScreener(t)
	view.tab = screenerHistoryTab

	if cmd := view.HandleContentKey(keyPress("space")); cmd != nil || view.preview != nil {
		t.Error("space opened a preview on Screener History")
	}
}

// A sender whose name and address fill the line still gets a header of one line each,
// the date included: a line that wrapped would make the preview taller than the screen.
func TestScreenerPreviewHeaderFitsTheWidth(t *testing.T) {
	view, _ := openedPreview(t)
	view.preview.row.name = strings.Repeat("Maria Fernanda Gonzalez de la Cruz ", 3)
	view.preview.row.email = "maria.fernanda.gonzalez.delacruz@example.com"
	view.preview.row.trailing = "Sep 29, 2026"
	view.preview.row.subject = strings.Repeat("Following up on the quarterly planning agenda ", 3)

	for index, line := range strings.Split(ansi.Strip(view.previewHeader()), "\n") {
		if width := displayWidth(line); width > view.vc.width {
			t.Errorf("header line %d is %d wide on an %d-wide screen: %q", index, width, view.vc.width, line)
		}
	}
	if header := plainText(view.previewHeader()); !strings.Contains(header, "Sep 29, 2026") {
		t.Errorf("the date was cut off: %q", header)
	}
}

// A long email scrolls inside the preview; the header stays put.
func TestScreenerPreviewScrolls(t *testing.T) {
	view, _ := openedPreview(t)
	view.vc.height = 6
	view.preview.body = htmlToMarkdown(strings.Repeat("<p>Another paragraph about the quarterly budget.</p>", 30))
	view.preview.width = 0

	view.HandleContentKey(keyPress("pgdown"))

	if view.preview.viewport.YOffset() == 0 {
		t.Error("PgDn did not scroll the preview")
	}
	if screen := plainText(view.View()); !strings.Contains(screen, "Jane Doe <jane@example.com>") {
		t.Errorf("the header scrolled away:\n%s", screen)
	}
}
