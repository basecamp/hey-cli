package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/threadload"
)

func bodyFindModel(t *testing.T) model {
	t.Helper()
	m := modelWithBoxes()
	v := m.mailView
	v.inThread = true
	v.topicID = 100
	v.topicName = "Monthly newsletter"
	v.entries = []mail.Entry{{
		ID: 501, Creator: mail.Contact{Name: "Alex Morgan", EmailAddress: "alex@example.org"},
		Body: htmlutil.ToMarkdown("<p>Budget highlights</p>" + strings.Repeat("<p>The monthly newsletter.</p>", 25) +
			"<p><a href=\"https://example.org/read\">Read online</a> or <a href=\"https://example.org/leave\">Unsubscribe</a></p>" +
			"<p><a href=\"https://example.org/unsubscribe\">Manage preferences</a></p>"),
		BodyState: string(threadload.StateHydrated),
	}}
	v.rebuildTopicContent()
	v.topicViewport.GotoTop()
	m.updateHelpBindings()
	return m
}

func typeBodyFind(m model, query string) model {
	updated, _ := m.Update(keyPress("/"))
	m = updated.(model)
	m.mailView.bodyFind.input.SetValue("")
	for _, key := range []string{query, "enter"} {
		updated, _ := m.Update(keyPress(key))
		m = updated.(model)
	}
	return m
}

func TestThreadFindScrollsHighlightsAndSelectsTheCorrectLink(t *testing.T) {
	m := typeBodyFind(bodyFindModel(t), "unsubscribe")
	v := m.mailView
	if len(v.bodyFind.matches) != 2 {
		t.Fatalf("matches = %v, want label and displayed destination", v.bodyFind.matches)
	}
	if v.topicViewport.YOffset() == 0 || !strings.Contains(ansi.Strip(v.View()), "Unsubscribe") {
		t.Errorf("find did not reveal the match:\n%s", ansi.Strip(v.View()))
	}
	if v.selectedLink < 0 || v.links[v.selectedLink].destination != "https://example.org/leave" {
		t.Fatalf("the matching label selected the wrong link: %d", v.selectedLink)
	}
	var opened string
	v.vc.openURL = func(destination string) error { opened = destination; return nil }
	_, cmd := m.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("Enter did not open the selected match")
	}
	runCmd(cmd)
	if opened != "https://example.org/leave" {
		t.Errorf("opened %q, want the matching label's destination", opened)
	}
	updated, _ := m.Update(keyPress("n"))
	m = updated.(model)
	if v.bodyFind.current != 1 || v.selectedLink < 0 || v.links[v.selectedLink].destination != "https://example.org/unsubscribe" {
		t.Errorf("n did not select the second match: current=%d link=%d", v.bodyFind.current, v.selectedLink)
	}
	updated, _ = m.Update(keyPress("N"))
	m = updated.(model)
	if v.bodyFind.current != 0 {
		t.Error("N did not return to the first match")
	}
	updated, _ = m.Update(keyPress("esc"))
	m = updated.(model)
	if !v.inThread || v.bodyFind.active() || v.selectedLink >= 0 {
		t.Error("Escape should clear find and its link selection, keeping the thread open")
	}
	m.Update(keyPress("esc"))
	if v.inThread {
		t.Error("the next Escape should leave the thread")
	}
}

func TestThreadFindCapturesShortcutsAndCancelKeepsThePreviousQuery(t *testing.T) {
	m := typeBodyFind(bodyFindModel(t), "budget")
	for _, key := range []string{"/", "?", "1", "n", "q", "r", "space"} {
		updated, _ := m.Update(keyPress(key))
		m = updated.(model)
	}
	v := m.mailView
	if !v.CapturingInput() || !v.inThread || m.help.hidden || v.modal != nil {
		t.Fatal("query keystrokes triggered an action outside the find prompt")
	}
	if got := v.bodyFind.input.Value(); !strings.Contains(got, "?1nqr") {
		t.Errorf("find input = %q, want the typed shortcut characters", got)
	}
	updated, _ := m.Update(keyPress("esc"))
	m = updated.(model)
	if v.CapturingInput() || v.bodyFind.query != "budget" {
		t.Error("canceling an edit should retain the committed query")
	}
	m = typeBodyFind(m, "") // Enter on an empty query clears it.
	if m.mailView.bodyFind.active() {
		t.Error("an empty query should clear find")
	}
}

func TestThreadFindSkipsHeadersAndSurvivesResizeAndRestyle(t *testing.T) {
	m := typeBodyFind(bodyFindModel(t), "alex")
	v := m.mailView
	if len(v.bodyFind.matches) != 0 {
		t.Error("body search matched a sender header")
	}
	v.bodyFind.query = "unsubscribe"
	v.scanBodyFind()
	v.showBodyMatch()
	v.vc.width = 100
	v.Resize(100, 12)
	v.Restyle()
	if len(v.bodyFind.matches) != 2 || v.bodyFind.query != "unsubscribe" {
		t.Errorf("resize/restyle lost the search: %+v", v.bodyFind)
	}
	v.ExitThread()
	if v.bodyFind.active() {
		t.Error("leaving the thread retained its body search")
	}
}

func TestThreadFindPreservesTheViewportTextAndFitsItsFooter(t *testing.T) {
	m := bodyFindModel(t)
	v := m.mailView
	before := ansi.Strip(strings.Join(v.topicLines, "\n"))
	typeBodyFind(m, "unsubscribe")
	if got := ansi.Strip(v.topicViewport.GetContent()); got != before {
		t.Error("highlighting changed the thread's visible text")
	}
	for _, width := range []int{20, 40, 80} {
		v.vc.width = width
		v.Resize(width, 10)
		footer := v.bodyFind.footer(width)
		if displayWidth(footer) > width || strings.Contains(footer, "\n") {
			t.Errorf("find footer exceeds %d columns: %q", width, footer)
		}
	}
	v.ExitDetail("esc")
	if got := ansi.Strip(v.topicViewport.GetContent()); got != ansi.Strip(strings.Join(v.topicLines, "\n")) {
		t.Error("clearing find did not restore the thread's unhighlighted content")
	}
}

func TestThreadFindKeepsAttachmentOpeningAvailable(t *testing.T) {
	m, events := openAttachmentLinkThreadThroughModel(t)
	m = typeBodyFind(m, "Padding")
	if m.mailView.LinkSelectionActive() || !hasHelpBinding(m.mailView.HelpBindings(), "enter") {
		t.Fatal("a plain-text result should retain the attachment Enter binding")
	}
	_, cmd := m.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("Enter did not open the selected attachment during find")
	}
	runCmd(cmd)
	if strings.Join(*events, ",") != "save,open attachment" {
		t.Errorf("Enter actions = %v, want the selected attachment opened", *events)
	}
}

func TestThreadFindStillRequiresRoomToReviewTheSelectedURL(t *testing.T) {
	m := typeBodyFind(bodyFindModel(t), "unsubscribe")
	m.height = headerHeight + m.help.height() + 4
	m.updateHelpBindings()
	if m.linkFooterFits() {
		t.Fatal("fixture should leave insufficient room for the find and URL footers")
	}
	if _, cmd := m.Update(keyPress("enter")); cmd != nil {
		t.Error("Enter should wait until the terminal can show the entire selected URL")
	}
}

func TestThreadFindDistinguishesEscapedURLDelimiters(t *testing.T) {
	m := bodyFindModel(t)
	v := m.mailView
	v.vc.width = 240
	v.entries[0].Body = htmlutil.ToMarkdown(`<p><a href="https://example.org/a%2Fb">First report</a> and <a href="https://example.org/a/b">Unsubscribe</a></p>`)
	v.rebuildTopicContent()
	m = typeBodyFind(m, "unsubscribe")
	if v.selectedLink < 0 || v.links[v.selectedLink].destination != "https://example.org/a/b" {
		t.Fatalf("selected link = %d, want the exact matching destination", v.selectedLink)
	}
	var opened string
	v.vc.openURL = func(destination string) error { opened = destination; return nil }
	_, cmd := m.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("Enter did not return an open command")
	}
	runCmd(cmd)
	if opened != "https://example.org/a/b" {
		t.Errorf("opened %q, want the URL with literal slashes", opened)
	}
}

func TestThreadFindPhrasesAndURLsAcrossWraps(t *testing.T) {
	for _, testCase := range []struct{ name, html, query string }{
		{"phrase", "<p>Monthly budget discussion about the quarterly forecast and board meeting.</p>", "the quarterly forecast"},
		{"quoted phrase", "<blockquote><p>Monthly budget discussion about the quarterly forecast and board meeting.</p></blockquote>", "the quarterly forecast"},
		{"URL", `<p><a href="https://example.org/xxxxxxxxxxxxxxxunsubscribe?newsletter=monthly">Manage preferences</a></p>`, "unsubscribe"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			v := bodyFindModel(t).mailView
			v.entries[0].Body = htmlutil.ToMarkdown(testCase.html)
			v.bodyFind.query = testCase.query
			for width := 44; width <= 100; width += 4 {
				v.vc.width = width
				v.Resize(width, 12)
				v.rebuildTopicContent()
				if len(v.bodyFind.matches) != 1 {
					t.Fatalf("width %d: got %d matches, want one for %q:\n%s", width, len(v.bodyFind.matches), testCase.query, ansi.Strip(v.topicContent))
				}
				before := ansi.Strip(v.topicContent)
				v.showBodyMatch()
				if got := ansi.Strip(v.topicViewport.GetContent()); got != before {
					t.Errorf("width %d: highlighting changed the text", width)
				}
			}
		})
	}
}

func TestThreadFindKeepsTheSelectedTextVisibleAfterResize(t *testing.T) {
	m := bodyFindModel(t)
	v := m.mailView
	v.entries[0].Body = htmlutil.ToMarkdown("<p>" + strings.Repeat("Monthly news ", 25) + "</p><p>Budget one.</p>" +
		strings.Repeat("<p>More newsletter text.</p>", 25) + "<p>Budget two.</p>")
	v.rebuildTopicContent()
	m = typeBodyFind(m, "budget")
	updated, _ := m.Update(keyPress("n"))
	m = updated.(model)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	v.Restyle()
	match, found := v.bodyFind.selected()
	if !found || v.bodyFind.current != 1 {
		t.Fatal("resize lost the logical second match")
	}
	if match.line < v.topicViewport.YOffset() || match.line >= v.topicViewport.YOffset()+v.topicViewport.Height() {
		t.Errorf("selected match at line %d is outside the viewport after resize", match.line)
	}
}

func BenchmarkThreadFindNext(b *testing.B) {
	for _, testCase := range []struct{ name, footer string }{
		{"plain", "<p>Unsubscribe from this newsletter.</p>"},
		{"linked", `<p><a href="https://example.org/preferences">Unsubscribe</a> from this newsletter.</p>`},
	} {
		b.Run(testCase.name, func(b *testing.B) {
			v := newMailView(testVC())
			v.inThread = true
			v.entries = []mail.Entry{{ID: 501, Body: htmlutil.ToMarkdown(strings.Repeat("<p>The monthly newsletter covers our quarterly budget.</p>", 200) + testCase.footer)}}
			v.rebuildTopicContent()
			v.bodyFind.query = "unsubscribe"
			v.scanBodyFind()
			v.showBodyMatch()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				v.bodyFind.next(1)
				v.showBodyMatch()
			}
		})
	}
}

func TestThreadFindKeepsTableTextAndFollowingProseSearchable(t *testing.T) {
	v := bodyFindModel(t).mailView
	v.entries[0].Body = htmlutil.ToMarkdown(`<table><tr><th>Discussion</th><th>Owner</th></tr><tr><td>Quarterly budget discussion and planning for next year</td><td>Maria Gonzalez</td></tr></table><p>Unsubscribe preferences follow the table.</p>`)
	for _, width := range []int{44, 80, 120} {
		v.vc.width = width
		v.rebuildTopicContent()
		for _, query := range []string{"Quarterly", "Maria", "Unsubscribe"} {
			v.bodyFind.query = query
			v.scanBodyFind()
			if len(v.bodyFind.matches) != 1 {
				t.Errorf("width %d: %q has %d matches, want one:\n%s", width, query, len(v.bodyFind.matches), ansi.Strip(v.topicContent))
			}
		}
	}
}

func TestThreadFindFollowsTheMostRecentNavigationAfterLayoutChanges(t *testing.T) {
	m := bodyFindModel(t)
	v := m.mailView
	v.entries[0].Body = htmlutil.ToMarkdown(`<p><a href="https://example.org/leave">Unsubscribe</a></p>` +
		strings.Repeat(`<p>Monthly budget newsletter.</p>`, 30) +
		`<p><a href="https://example.org/` + strings.Repeat("monthly-newsletter-", 9) + `">Read more</a></p>`)
	v.rebuildTopicContent()
	m = typeBodyFind(m, "unsubscribe")
	updated, _ := m.Update(keyPress("tab"))
	m = updated.(model)
	if v.selectedLink != 1 || v.followFind {
		t.Fatal("Tab should select the second link and take navigation priority")
	}
	assertLinkVisible := func() {
		t.Helper()
		link := v.links[v.selectedLink]
		if link.startLine < v.topicViewport.YOffset() || link.startLine >= v.topicViewport.YOffset()+v.topicViewport.Height() {
			t.Errorf("selected link at line %d is hidden after layout", link.startLine)
		}
	}
	assertLinkVisible() // The URL footer changed the viewport height.
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	m = updated.(model)
	v.Restyle()
	assertLinkVisible()
	m.Update(keyPress("n"))
	if !v.followFind {
		t.Fatal("n should return navigation priority to find")
	}
	match, found := v.bodyFind.selected()
	if !found || match.line < v.topicViewport.YOffset() || match.line >= v.topicViewport.YOffset()+v.topicViewport.Height() {
		t.Error("n did not reveal the selected find result")
	}
}

func TestThreadFindPreservesManualLinkSelectionWithoutResults(t *testing.T) {
	m := typeBodyFind(bodyFindModel(t), "No matching phrase")
	updated, _ := m.Update(keyPress("tab"))
	m = updated.(model)
	v := m.mailView
	key := v.selectedLinkKey
	if key == "" || v.followFind {
		t.Fatal("Tab should select a manual link")
	}
	for _, action := range []string{"n", "N"} {
		updated, _ = m.Update(keyPress(action))
		m = updated.(model)
		if v.selectedLinkKey != key || v.followFind {
			t.Errorf("%s with no matches changed the manual link selection", action)
		}
	}
	m = typeBodyFind(m, "")
	if v.selectedLinkKey != key || v.followFind || m.mailView.bodyFind.active() {
		t.Error("submitting an empty query should retain the manual link and clear find")
	}
}

func TestThreadFindClearsItsOwnLinkAfterAnUnmatchedQuery(t *testing.T) {
	m := typeBodyFind(bodyFindModel(t), "unsubscribe")
	v := m.mailView
	if !v.LinkSelectionActive() || !v.followFind {
		t.Fatal("find should select an unsubscribe link")
	}
	m = typeBodyFind(m, "No matching phrase")
	if m.mailView.LinkSelectionActive() || v.selectedLinkKey != "" {
		t.Error("an unmatched query kept a stale find-owned link")
	}
}

func TestThreadFindResolvesLinksWhoseOpenerIsOnAnEarlierRow(t *testing.T) {
	v := bodyFindModel(t).mailView
	const destination = "https://example.org/preferences"
	lines := []string{"\x1b]8;;" + destination + "\aRead", "Unsubscribe\x1b]8;;\a"}
	run := markdown.LinkTextRun{Spans: []markdown.TextSpan{{Line: 0, Start: 0, End: 4}, {Line: 1, Start: 0, End: 11}}}
	v.topicLines = lines
	v.linkBodies = []mailLinkBody{{lines: lines, linkRuns: []markdown.LinkTextRun{run}}}
	v.links = []mailLink{{destination: destination, body: 0, key: "preferences", textRuns: []markdown.LinkTextRun{run}}}
	v.selectedLink = -1
	v.selectedLinkKey = ""
	v.bodyFind.query = "unsubscribe"
	v.scanBodyFind()
	v.showBodyMatch()
	if v.selectedLink != 0 {
		t.Fatal("find did not select the link continued from the previous row")
	}
	var opened string
	v.vc.openURL = func(url string) error { opened = url; return nil }
	cmd := v.HandleContentKey(keyPress("enter"))
	if cmd == nil {
		t.Fatal("Enter did not return an open command for the wrapped match")
	}
	runCmd(cmd)
	if opened != destination {
		t.Errorf("opened %q, want the wrapped link's destination", opened)
	}
}

func TestThreadFindResynchronizesLinksWhenResizeChangesResults(t *testing.T) {
	const phrase = "Monthly newsletter subscription preferences"
	const table = `<table><tr><th>Action</th><th>Owner</th></tr><tr><td><a href="https://example.org/first">` + phrase + `</a></td><td>Maria Gonzalez</td></tr></table>`
	const paragraph = `<p><a href="https://example.org/second">` + phrase + `[1]</a></p>`
	for _, testCase := range []struct {
		name               string
		html               string
		fromWidth, toWidth int
		before, after      int
		destination        string
	}{
		{"earlier result appears", table + paragraph, 44, 240, 1, 2, "https://example.org/first"},
		{"earlier result disappears", table + paragraph, 240, 44, 2, 1, "https://example.org/second"},
		{"all results disappear", table, 240, 44, 1, 0, ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := bodyFindModel(t)
			v := m.mailView
			v.entries[0].Body = htmlutil.ToMarkdown(testCase.html)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: testCase.fromWidth, Height: 40})
			// The table marker distinguishes the cell from the plain link note.
			m = typeBodyFind(updated.(model), phrase+"[1]")
			if len(v.bodyFind.matches) != testCase.before || !v.LinkSelectionActive() {
				t.Fatalf("fixture before resize: %d matches, link %d, wanted %d:\n%s\n%+v", len(v.bodyFind.matches), v.selectedLink, testCase.before, ansi.Strip(v.topicContent), v.bodyFind.matches)
			}
			updated, _ = m.Update(tea.WindowSizeMsg{Width: testCase.toWidth, Height: 40})
			m = updated.(model)
			v.Restyle()
			if len(v.bodyFind.matches) != testCase.after {
				t.Fatalf("after resize: %d matches, want %d", len(v.bodyFind.matches), testCase.after)
			}
			if testCase.destination == "" {
				if v.LinkSelectionActive() || v.selectedLinkKey != "" {
					t.Error("resize kept a link despite there being no find results")
				}
				if _, cmd := m.Update(keyPress("enter")); cmd != nil {
					t.Error("Enter should have no link to open after all results disappear")
				}
				return
			}
			if !v.LinkSelectionActive() || v.links[v.selectedLink].destination != testCase.destination {
				t.Fatalf("selected link %d is out of sync with the find result", v.selectedLink)
			}
			footer, _ := v.LinkFooter()
			if !strings.Contains(footer, testCase.destination) {
				t.Error("the footer does not show the current result's destination")
			}
			var opened string
			v.vc.openURL = func(url string) error { opened = url; return nil }
			_, cmd := m.Update(keyPress("enter"))
			if cmd == nil {
				t.Fatal("Enter did not return a command for the current result")
			}
			runCmd(cmd)
			if opened != testCase.destination {
				t.Errorf("opened stale destination %q, want %q", opened, testCase.destination)
			}
		})
	}
}
