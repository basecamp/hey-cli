package markdown

import (
	"strings"
	"testing"
)

func TestHyperlinkWrapsText(t *testing.T) {
	got := Hyperlink("Q3 report", "https://example.com/reports/q3")
	if !strings.Contains(got, "Q3 report") || !strings.Contains(got, "\x1b]8;") {
		t.Errorf("Hyperlink = %q", got)
	}
}

func TestHyperlinkWithoutURL(t *testing.T) {
	if got := Hyperlink("Q3 report", ""); got != "Q3 report" {
		t.Errorf("Hyperlink = %q, want the text unchanged", got)
	}
}

func TestHyperlinkStripsControlCharacters(t *testing.T) {
	got := Hyperlink("report", "https://example.com/\x07\x1b[2Jq3")
	if strings.Contains(got, "\x1b[2J") || strings.Contains(got, "com/\x07") {
		t.Errorf("Hyperlink = %q, should strip control characters from the URL", got)
	}
	if !strings.Contains(got, "https://example.com/[2Jq3") {
		t.Errorf("Hyperlink = %q, should keep the printable part of the URL", got)
	}
}

func TestLinkifyURLs(t *testing.T) {
	got := LinkifyURLs("Read https://example.com/reports/q3 today.")
	if !strings.Contains(got, "\x1b]8;") {
		t.Errorf("LinkifyURLs = %q, should wrap the bare URL", got)
	}
	if !strings.Contains(got, " today.") {
		t.Errorf("LinkifyURLs = %q, should keep the trailing text", got)
	}
}

func TestLinkifyURLsLeavesPlainText(t *testing.T) {
	text := "No links in this sentence."
	if got := LinkifyURLs(text); got != text {
		t.Errorf("LinkifyURLs = %q, want unchanged", got)
	}
}

func TestLinkifyURLsSkipsAlreadyLinkedURLs(t *testing.T) {
	linked := Hyperlink("https://example.com/q3", "https://example.com/q3")
	got := LinkifyURLs(linked)
	if strings.Count(got, "\x1b]8;;\x07") != strings.Count(linked, "\x1b]8;;\x07") {
		t.Errorf("LinkifyURLs double-wrapped an existing hyperlink: %q", got)
	}
}

func TestLinkifyURLsTrimsTrailingPunctuation(t *testing.T) {
	got := LinkifyURLs("See https://example.com/q3.")
	if !strings.HasSuffix(got, ".") {
		t.Errorf("LinkifyURLs = %q, should leave the sentence period outside the link", got)
	}
	if strings.Contains(got, "q3.\x1b]8;;") {
		t.Errorf("LinkifyURLs = %q, should not swallow the period", got)
	}
}

// A URL ends at a space in any script and before punctuation outside ASCII: glamour
// pads inline code with no-break spaces, and a curly quote closes a quotation.
func TestLinkifyURLsStopsAtUnicodeSpacesAndPunctuation(t *testing.T) {
	for text, url := range map[string]string{
		" https://staging.example.com/api ":                  "https://staging.example.com/api",
		"She said “https://maps.example.com/harbour” twice.": "https://maps.example.com/harbour",
		"Lien : https://exemple.fr/menu»":                    "https://exemple.fr/menu",
		"Menu: https://cafe.example.com/café today":          "https://cafe.example.com/café",
		"Rates: https://fx.example.com/convert?to=€ today":   "https://fx.example.com/convert?to=€",
		"Tag: https://notes.example.com/tags/☕ today":        "https://notes.example.com/tags/☕",
	} {
		if got := LinkifyURLs(text); !strings.Contains(got, Hyperlink(url, url)) {
			t.Errorf("LinkifyURLs(%q) = %q, want %q linked", text, got, url)
		}
	}
}

func TestLinkifyURLsBalancesParentheses(t *testing.T) {
	got := LinkifyURLs("See https://example.com/wiki/HEY_(email) now.")
	if !strings.Contains(got, "HEY_(email)") {
		t.Errorf("LinkifyURLs = %q, should keep balanced parentheses in the URL", got)
	}
}
