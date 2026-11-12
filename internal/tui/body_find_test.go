package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/basecamp/hey-cli/internal/markdown"
)

func TestBodyFindUsesVisibleTextAndLiteralUnicodeCaseFolding(t *testing.T) {
	line := "\x1b[31mÉCOLE [weekly]\x1b[0m \x1b]8;;https://example.org/hidden-unsubscribe\aManage preferences\x1b]8;;\a"
	for _, testCase := range []struct {
		query string
		want  int
	}{
		{"école", 1}, {"[weekly]", 1}, {"unsubscribe", 0}, {"preferences", 1},
	} {
		t.Run(testCase.query, func(t *testing.T) {
			find := bodyFind{query: testCase.query}
			find.scan(findDocument{lines: []string{line}})
			if len(find.matches) != testCase.want {
				t.Fatalf("matches = %v, want %d", find.matches, testCase.want)
			}
			highlighted := find.highlight([]string{line})[0]
			if ansi.Strip(highlighted) != ansi.Strip(line) {
				t.Errorf("highlighting changed the text: %q", highlighted)
			}
			if !strings.Contains(highlighted, "\x1b]8;;https://example.org/hidden-unsubscribe\a") {
				t.Errorf("highlighting removed the hyperlink: %q", highlighted)
			}
		})
	}
}

func TestBodyFindWrapsAndStartsAtTheVisibleMessage(t *testing.T) {
	find := bodyFind{query: "budget"}
	find.scan(findDocument{lines: []string{"Budget update", "Next week", "The budget is ready"}})
	find.firstFrom(1)
	if find.current != 1 {
		t.Fatalf("initial match = %d, want second", find.current)
	}
	find.next(1)
	if find.current != 0 {
		t.Error("next did not wrap to the first match")
	}
	find.next(-1)
	if find.current != 1 {
		t.Error("previous did not wrap to the last match")
	}
	find.query = "absent"
	find.scan(findDocument{lines: []string{"Budget update"}})
	find.next(1)
	if _, found := find.selected(); found || !strings.Contains(find.footer(80), "no matches") {
		t.Error("an unmatched query should report no matches")
	}
}

func TestBodyFindDestinationsBelongToTheMatchedText(t *testing.T) {
	line := "Outside \x1b]8;;https://example.org/first\aFirst\x1b]8;;\a and \x1b]8;;https://example.org/second\x1b\\Second\x1b]8;;\x1b\\"
	plain := ansi.Strip(line)
	for _, testCase := range []struct{ text, destination string }{
		{"Outside", ""}, {"First", "https://example.org/first"}, {"and", ""}, {"Second", "https://example.org/second"},
	} {
		if got := matchDestination(line, strings.Index(plain, testCase.text)); got != testCase.destination {
			t.Errorf("destination at %q = %q, want %q", testCase.text, got, testCase.destination)
		}
	}
}

func TestBodyFindStopsAtParagraphListAndTableBoundaries(t *testing.T) {
	for _, lines := range [][]string{
		{"Quarterly", "", "forecast"},
		{"• Quarterly", "• forecast"},
		{"Quarterly │ forecast", "Budget │ planning"},
	} {
		find := bodyFind{query: "Quarterly forecast"}
		find.scan(findDocument{lines: lines})
		if len(find.matches) != 0 {
			t.Errorf("search crossed a structural boundary in %q", lines)
		}
	}
}

func TestBodyFindPreservesLiteralTableGlyphsInProse(t *testing.T) {
	find := bodyFind{query: "a│b"}
	find.scan(findDocument{lines: []string{"Search a│b here"}})
	if len(find.matches) != 1 {
		t.Fatalf("visible literal has %d matches, want one", len(find.matches))
	}
}

func TestBodyFindDoesNotAddOverlappingMatchesAtURLWraps(t *testing.T) {
	for _, spans := range [][]markdown.TextSpan{
		{{Line: 0, Start: 0, End: 6}},
		{{Line: 0, Start: 0, End: 2}, {Line: 1, Start: 0, End: 4}},
	} {
		lines := []string{"aaaaaa"}
		if len(spans) > 1 {
			lines = []string{"aa", "aaaa"}
		}
		find := bodyFind{query: "aaa"}
		find.scan(findDocument{lines: lines, links: []markdown.LinkTextRun{{Destination: true, Spans: spans}}})
		if len(find.matches) != 2 {
			t.Errorf("%q: got %d occurrences, want two nonoverlapping matches", lines, len(find.matches))
		}
		if got := ansi.Strip(strings.Join(find.highlight(lines), "\n")); got != strings.Join(lines, "\n") {
			t.Error("overlapping highlights changed the visible text")
		}
	}
}

func TestBodyFindURLRunsTakePrecedenceOverWrappedTableCellLines(t *testing.T) {
	find := bodyFind{query: "aaa"}
	find.scan(findDocument{
		lines: []string{"aa │ Budget", "aaaa │ News"},
		links: []markdown.LinkTextRun{{Destination: true, Spans: []markdown.TextSpan{{Line: 0, Start: 0, End: 2}, {Line: 1, Start: 0, End: 4}}}},
	})
	if len(find.matches) != 2 {
		t.Fatalf("URL has %d matches, want two without overlapping cell-line matches", len(find.matches))
	}
}
