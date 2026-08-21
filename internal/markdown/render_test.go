package markdown

import (
	"strings"
	"testing"
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

func TestRenderFallsBackToDefaultWidth(t *testing.T) {
	if render("Hello", 0) == "" {
		t.Error("Render with no width returned nothing")
	}
}
