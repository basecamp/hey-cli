package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestStyleInlineMarkdownSpans(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"bold",
			"**bold** move",
			"\x1b[2m**\x1b[0m\x1b[1mbold\x1b[0m\x1b[2m**\x1b[0m move",
		},
		{
			"italic",
			"an *italic* aside",
			"an \x1b[2m*\x1b[0m\x1b[3mitalic\x1b[0m\x1b[2m*\x1b[0m aside",
		},
		{
			"bold italic",
			"***both***",
			"\x1b[2m***\x1b[0m\x1b[1;3mboth\x1b[0m\x1b[2m***\x1b[0m",
		},
		{
			"underscores",
			"__bold__ and _italic_",
			"\x1b[2m__\x1b[0m\x1b[1mbold\x1b[0m\x1b[2m__\x1b[0m and \x1b[2m_\x1b[0m\x1b[3mitalic\x1b[0m\x1b[2m_\x1b[0m",
		},
		{
			"strikethrough",
			"~~scrapped~~",
			"\x1b[2m~~\x1b[0m\x1b[9mscrapped\x1b[0m\x1b[2m~~\x1b[0m",
		},
		{
			"code",
			"run `make test` first",
			"run \x1b[2m`\x1b[0m\x1b[38;5;11mmake test\x1b[0m\x1b[2m`\x1b[0m first",
		},
		{
			"heading",
			"## Plans",
			"\x1b[2m## \x1b[0m\x1b[1;38;5;12mPlans\x1b[0m",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := styleInlineMarkdown(c.in); got != c.want {
				t.Errorf("styleInlineMarkdown(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestStyleInlineMarkdownLeavesPlainTextAlone(t *testing.T) {
	cases := []string{
		"nothing to style here",
		"**unclosed bold",
		"2 * 3 * 4 = 24",
		"* a bullet, not emphasis",
		"a snake_case_name stays plain",
		"#hashtag is not a heading",
		"####### seven hashes is not one either",
		"`unclosed code",
		"",
	}
	for _, in := range cases {
		if got := styleInlineMarkdown(in); got != in {
			t.Errorf("styleInlineMarkdown(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestStyleInlineMarkdownKeepsTheText(t *testing.T) {
	in := "# Plan\n**bold** and *italic* and `code` and ~~struck~~\nsnake_case"
	if got := ansi.Strip(styleInlineMarkdown(in)); got != in {
		t.Errorf("styling changed the text: %q, want %q", got, in)
	}
}

func TestStyleInlineMarkdownRestoresTheLinesOwnStyling(t *testing.T) {
	// A cursor line's background must pass through, be re-asserted under our
	// spans, and come back exactly when a span ends.
	in := "\x1b[48;5;255m**hi** there\x1b[0m"
	want := "\x1b[48;5;255m\x1b[2m**\x1b[0m\x1b[48;5;255m\x1b[1mhi\x1b[0m\x1b[48;5;255m\x1b[2m**\x1b[0m\x1b[48;5;255m there\x1b[0m"
	if got := styleInlineMarkdown(in); got != want {
		t.Errorf("styleInlineMarkdown(%q) = %q, want %q", in, got, want)
	}
}

func TestStyleInlineMarkdownReassertsAcrossEmbeddedSequences(t *testing.T) {
	// The virtual cursor drops a reset mid-span; the span's styling has to come
	// back for the characters after it.
	in := "**bo\x1b[7ml\x1b[0md**"
	got := styleInlineMarkdown(in)
	if !strings.Contains(got, "\x1b[0m\x1b[1md") {
		t.Errorf("bold should be re-asserted after the cursor's reset, got %q", got)
	}
	if got := ansi.Strip(got); got != "**bold**" {
		t.Errorf("styling changed the text: %q", got)
	}
}

func TestStyleInlineMarkdownSpansStayOnTheirLine(t *testing.T) {
	in := "**bold\nstill**"
	if got := styleInlineMarkdown(in); got != in {
		t.Errorf("a pair split across lines must not style, got %q", got)
	}
}

func TestStyleInlineMarkdownSkipsEmphasisInCode(t *testing.T) {
	got := styleInlineMarkdown("`a * b` and `c * d`")
	if strings.Contains(got, "\x1b[3m") {
		t.Errorf("asterisks inside code spans must not pair as italics, got %q", got)
	}
	if !strings.Contains(got, "\x1b[38;5;11ma * b") {
		t.Errorf("the first code span should be styled as code, got %q", got)
	}
}
