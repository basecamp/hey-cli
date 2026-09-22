package markdown

import (
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

func TestRenderEmpty(t *testing.T) {
	if got := render("   \n ", 80); got != "" {
		t.Errorf("Render blank = %q, want empty", got)
	}
}

func TestRenderStylesEmphasis(t *testing.T) {
	got := render("Hi **Ryan**", 80)
	if !strings.Contains(got, "Ryan") {
		t.Errorf("Render = %q, should keep the text", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("Render = %q, should carry ANSI styling", got)
	}
}

func TestRenderDropsHeadingMarkers(t *testing.T) {
	got := render("## Quarterly update", 80)
	if strings.Contains(got, "#") {
		t.Errorf("Render = %q, should not leave heading markers", got)
	}
	if !strings.Contains(got, "Quarterly") || !strings.Contains(got, "update") {
		t.Errorf("Render = %q, should keep the heading text", got)
	}
}

func TestRenderLeavesNoTrailingWhitespace(t *testing.T) {
	for _, line := range strings.Split(render("Hi **Ryan**", 80), "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("line %q has trailing whitespace", line)
		}
	}
}

func TestRenderWrapsToWidth(t *testing.T) {
	got := render(strings.Repeat("word ", 40), 30)
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 30 {
			t.Errorf("line %q is wider than 30 columns", line)
		}
	}
}

func TestRenderLinksAreClickable(t *testing.T) {
	got := render("See the [Q3 report](https://example.com/reports/q3).", 80)
	if !strings.Contains(got, "\x1b]8;") {
		t.Errorf("Render = %q, should emit an OSC 8 hyperlink", got)
	}
	if !strings.Contains(got, "https://example.com/reports/q3") {
		t.Errorf("Render = %q, should keep the URL", got)
	}
}

// The confusable policy covers a link's destination as well as its text: a zero-width
// character in the href is dropped from the OSC 8 target the same way it is dropped from
// the label, so the link goes where the reader sees it going.
func TestRenderLinksToWhatItShows(t *testing.T) {
	got := render("[https://example.com/a\u200bb](https://example.com/a\u200bb)", 80)
	if strings.Contains(got, "\u200b") {
		t.Errorf("Render = %q, a zero-width space survived", got)
	}
	if !strings.Contains(got, ";https://example.com/ab\a") {
		t.Errorf("Render = %q, want the OSC 8 target to be the URL as shown", got)
	}
}

func TestRenderFallsBackToDefaultWidth(t *testing.T) {
	if render("Hello", 0) == "" {
		t.Error("Render with no width returned nothing")
	}
}

func TestRenderLinkedReturnsOneOccurrence(t *testing.T) {
	linked := RenderLinked(htmlutil.ToMarkdown(`<p>Read <a href="https://example.com/report">the report</a>.</p>`), 80, -1)
	if len(linked.Links) != 1 {
		t.Fatalf("RenderLinked returned %d links, want 1: %#v", len(linked.Links), linked.Links)
	}
	if linked.Links[0].Destination != "https://example.com/report" {
		t.Errorf("destination = %q", linked.Links[0].Destination)
	}
	if linked.Links[0].StartLine != 0 || linked.Links[0].EndLine != 0 {
		t.Errorf("line range = %d-%d, want 0-0", linked.Links[0].StartLine, linked.Links[0].EndLine)
	}
	if !strings.Contains(linked.Text, "\x1b]8;") {
		t.Errorf("linked text = %q, want OSC 8", linked.Text)
	}
}

func TestRenderLinkedPreservesOrderDuplicatesAndSchemes(t *testing.T) {
	linked := RenderLinked(htmlutil.ToMarkdown(`<p><a href="https://example.com/one">one</a> <a href="https://example.com/one">again</a> <a href="mailto:reader@example.com">mail</a> https://example.org/two</p>`), 80, -1)
	want := []string{"https://example.com/one", "https://example.com/one", "mailto:reader@example.com", "https://example.org/two"}
	if len(linked.Links) != len(want) {
		t.Fatalf("links = %#v, want %d occurrences", linked.Links, len(want))
	}
	for i, destination := range want {
		if linked.Links[i].Destination != destination {
			t.Errorf("links[%d].Destination = %q, want %q", i, linked.Links[i].Destination, destination)
		}
	}
}

func TestRenderLinkedRejectsUnsafeAndRelativeDestinations(t *testing.T) {
	linked := RenderLinked(htmlutil.ToMarkdown(`<p><a href="/relative">relative</a> <a href="ftp://example.com">ftp</a> <a href="javascript:alert(1)">script</a> <a href="https://">bad</a> <a href="https://trusted.example@evil.example/path">userinfo</a></p>`), 80, -1)
	if len(linked.Links) != 0 {
		t.Fatalf("links = %#v, want no selectable links", linked.Links)
	}
}

func TestRenderLinkedStylesOnlySelectedOccurrence(t *testing.T) {
	linked := RenderLinked(htmlutil.ToMarkdown(`<p><a href="https://example.com/one">one</a> <a href="https://example.com/two">two</a></p>`), 80, 1)
	if len(linked.Links) != 2 {
		t.Fatalf("links = %#v, want 2 occurrences", linked.Links)
	}
	if strings.Count(linked.Text, "\x1b[7m") < 2 || strings.Count(linked.Text, "\x1b[27m") == 0 {
		t.Errorf("selected styling missing from %q", linked.Text)
	}
	if !sgrOnly(linked.Text) {
		t.Errorf("selected output escaped containment: %q", linked.Text)
	}
}

func TestRenderLinkedReportsWrappedLinkRange(t *testing.T) {
	linked := RenderLinked(htmlutil.ToMarkdown(`<p><a href="https://example.com/long">this is a link label that must wrap over several lines</a></p>`), 12, -1)
	if len(linked.Links) != 1 {
		t.Fatalf("links = %#v, want one occurrence", linked.Links)
	}
	if linked.Links[0].EndLine <= linked.Links[0].StartLine {
		t.Errorf("line range = %d-%d, want a wrapped range", linked.Links[0].StartLine, linked.Links[0].EndLine)
	}
}

func TestRenderLinkedKeepsOneAnchorWhoseLabelContainsItsDestinationTogether(t *testing.T) {
	for _, tt := range []struct {
		name          string
		label         string
		selectedSpans int
	}{
		{name: "ends with destination", label: "Read https://example.com", selectedSpans: 2},
		{name: "equals destination", label: "https://example.com", selectedSpans: 1},
		{name: "equals after whitespace is removed", label: "https://example. com", selectedSpans: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := `<p><a href="https://example.com">` + tt.label + `</a></p>`
			linked := RenderLinked(htmlutil.ToMarkdown(source), 80, 0)
			if len(linked.Links) != 1 {
				t.Fatalf("links = %#v, want one occurrence; rendered text = %q", linked.Links, linked.Text)
			}
			if got := strings.Count(linked.Text, "\x1b[27m"); got != tt.selectedSpans {
				t.Errorf("selected spans = %d, want %d: %q", got, tt.selectedSpans, linked.Text)
			}
		})
	}
}

func TestRenderLinkedKeepsWrappedDuplicateLinksSeparate(t *testing.T) {
	linked := RenderLinked(htmlutil.ToMarkdown(`<p><a href="https://example.com/very/long/path">one</a> <a href="https://example.com/very/long/path">again</a></p>`), 12, -1)
	if len(linked.Links) != 2 {
		t.Fatalf("links = %#v, want two occurrences; rendered text = %q", linked.Links, linked.Text)
	}
}

func TestRenderLinkedKeepsDuplicateLinksSeparate(t *testing.T) {
	for name, source := range map[string]string{
		"labels":     `<p><a href="https://example.com/one">one</a><a href="https://example.com/one">again</a></p>`,
		"autolinks":  `<p>https://example.com/one https://example.com/one</p>`,
		"line break": `<p>https://example.com/one<br>https://example.com/one</p>`,
	} {
		t.Run(name, func(t *testing.T) {
			linked := RenderLinked(htmlutil.ToMarkdown(source), 80, -1)
			if len(linked.Links) != 2 {
				t.Fatalf("links = %#v, want two occurrences", linked.Links)
			}
		})
	}
}

func TestRenderLinkedSelectedNestedLabelStaysSelected(t *testing.T) {
	linked := RenderLinked(htmlutil.ToMarkdown(`<p><a href="https://example.com/one"><strong>bold</strong> and <em>italic</em></a></p>`), 80, 0)
	if len(linked.Links) != 1 {
		t.Fatalf("links = %#v, want one occurrence", linked.Links)
	}
	if !strings.Contains(linked.Text, "\x1b[7m\x1b[94;1m\x1b[7mbold") || !strings.Contains(linked.Text, "\x1b[7m\x1b[94;3m\x1b[7mitalic") || !strings.Contains(linked.Text, "\x1b[m\x1b[7m") {
		t.Errorf("nested SGR styling lost selection: %q", linked.Text)
	}
	if !sgrOnly(linked.Text) {
		t.Errorf("selected output escaped containment: %q", linked.Text)
	}
}

func TestRenderLinkedSelectedWrappedLabelStaysSelected(t *testing.T) {
	linked := RenderLinked(htmlutil.ToMarkdown(`<p><a href="https://example.com/long">this is a link label that must wrap over several lines</a></p>`), 12, 0)
	if len(linked.Links) != 1 {
		t.Fatalf("links = %#v, want one occurrence", linked.Links)
	}
	if strings.Count(linked.Text, "\x1b[7m") < 6 {
		t.Errorf("selected wrapped label lost selection: %q", linked.Text)
	}
	if !sgrOnly(linked.Text) {
		t.Errorf("selected output escaped containment: %q", linked.Text)
	}
}

func TestRenderLinkedSanitizesLinkTextAndDestination(t *testing.T) {
	linked := RenderLinked(htmlutil.ToMarkdown("<p><a href=\"https://example.com/a\u200bb\x01\">shown\u200b\x01\u202ename</a></p>"), 80, -1)
	if len(linked.Links) != 1 || linked.Links[0].Destination != "https://example.com/ab" {
		t.Fatalf("links = %#v, want one sanitized link", linked.Links)
	}
	if strings.Contains(linked.Text, "\u200b") || strings.Contains(linked.Text, "\u202e") {
		t.Errorf("sanitized link output = %q", linked.Text)
	}
}

func TestRenderLinkedRejectsMalformedDestinationAndHandlesNoLinks(t *testing.T) {
	for _, source := range []string{
		`<p><a href="https://[">malformed</a></p>`,
		`<p><a href="mailto://example.com">malformed mail address</a></p>`,
		`<p>There is no link here.</p>`,
	} {
		if linked := RenderLinked(htmlutil.ToMarkdown(source), 80, -1); len(linked.Links) != 0 {
			t.Errorf("source %q produced links %#v", source, linked.Links)
		}
	}
}
