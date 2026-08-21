package markdown

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/ansi"
)

// visible is what the terminal shows: the output with every escape sequence removed.
func visible(out string) string {
	return ansi.Strip(out)
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return r != '\n' && r != '\t' && isControl(r)
	})
}

// sgrOnly reports whether every escape sequence in out is SGR or OSC 8.
func sgrOnly(out string) bool {
	_, ok := allowSequences(out)
	return ok
}

// glamour decodes entities in the text it reads, which would turn "&#27;[31m" in
// an email into a red terminal. Every place it reads text is covered here.
func TestRenderNeverDecodesEntities(t *testing.T) {
	for name, test := range map[string]struct{ md, literal string }{
		"prose":         {"hello &#27;[31mRED", "&#27;[31mRED"},
		"escaped prose": {`hello \&#27;[31mRED`, "&#27;[31mRED"},
		"emphasis":      {"**&#27;[31mRED**", "&#27;[31mRED"},
		"strikethrough": {"~~&#27;[31mRED~~", "&#27;[31mRED"},
		"heading":       {"## &#27;[31mRED", "&#27;[31mRED"},
		"code span":     {"`&#27;[31mRED`", "&#27;[31mRED"},
		"code block":    {"```\n&#27;[31mRED\n```", "&#27;[31mRED"},
		"indented code": {"    &#27;[31mRED", "&#27;[31mRED"},
		"link label":    {"[a &#27;b](https://example.com)", "a &#27;b"},
		"image alt":     {"![a &#27;b](https://example.com/x.png)", "a &#27;b"},
		"table cell":    {"| a |\n| --- |\n| &#27;[31mRED |", "&#27;[31mRED"},
		"list item":     {"- &#27;[31mRED", "&#27;[31mRED"},
		"blockquote":    {"> &#27;[31mRED", "&#27;[31mRED"},
		"raw html":      {"<span>&#27;[31mRED</span>", "&#27;[31mRED"},
		"html block":    {"<div>\n&#27;[31mRED\n</div>", "&#27;[31mRED"},
		"hex entity":    {"&#x1b;[31mRED", "&#x1b;[31mRED"},
		"named entity":  {"&lpar;&#27;[31mRED", "&lpar;&#27;[31mRED"},
	} {
		out := Render(test.md, 80)
		if strings.Contains(out, "\x1b[31m") {
			t.Errorf("%s: Render(%q) = %q turned red", name, test.md, out)
		}
		if !strings.Contains(visible(out), test.literal) {
			t.Errorf("%s: Render(%q) = %q lost the literal text", name, test.md, out)
		}
	}
}

func TestRenderShowsAmpersandsAsWritten(t *testing.T) {
	for md, want := range map[string]string{
		"Fried & Hansson":             "Fried & Hansson",
		`Fried \& Hansson`:            "Fried & Hansson",
		"`a && b`":                    "a && b",
		"&copy; 2026":                 "&copy; 2026",
		"[q](https://e.com/?a=1&b=2)": "https://e.com/?a=1&b=2",
	} {
		if out := visible(Render(md, 200)); !strings.Contains(out, want) {
			t.Errorf("Render(%q) shows %q, want %q in it", md, out, want)
		}
	}
}

func TestRenderKeepsQueryStringsInHyperlinks(t *testing.T) {
	out := Render("[q](https://example.com/?a=1&b=2)", 80)
	if !strings.Contains(out, "\x1b]8;id=") || !strings.Contains(out, ";https://example.com/?a=1&b=2\x07") {
		t.Errorf("Render = %q, want an OSC 8 link to the URL with its query string", out)
	}
}

func TestRenderStripsRawControls(t *testing.T) {
	for name, md := range map[string]string{
		"escape":     "hello \x1b[31mRED",
		"osc title":  "hello \x1b]0;pwned\x07",
		"c1":         "cafe ",
		"del":        "note\x7f",
		"bel":        "ding\x07",
		"in code":    "`\x1b[31m`",
		"in a fence": "```\n\x1b[31m\n```",
		"in a link":  "[x](https://example.com/\x1b[31m)",
	} {
		out := Render(md, 80)
		if strings.Contains(out, "\x1b[31m") || strings.Contains(out, "\x1b]0;") {
			t.Errorf("%s: Render(%q) = %q carries the injected sequence", name, md, out)
		}
		if hasControl(visible(out)) {
			t.Errorf("%s: Render(%q) = %q carries a control character", name, md, out)
		}
	}
}

func TestRenderOutputIsOnlySGRAndHyperlinks(t *testing.T) {
	md := "# Title\n\nHi **Ryan**, see the [Q3 report](https://example.com/q3) and <https://example.org>.\n\n" +
		"- one *two* ~~three~~ `four`\n- https://example.com/bare\n\n> quote\n\n```ruby\nputs 1\n```\n\n" +
		"| a | b |\n| --- | --- |\n| 1 | 2 |\n\n---\n\n![chart](/rails/blobs/chart.png) and [relative](/rails/blobs/q3.pdf) and [mail](mailto:jane@example.com)"
	out := Render(md, 60)
	if !sgrOnly(out) {
		t.Fatalf("Render = %q carries a sequence outside the allow-list", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("Render = %q lost its styling", out)
	}
	if contained := contain(out); contained != out {
		t.Errorf("contain changed an output that was already clean:\n got %q\nwant %q", contained, out)
	}
}

func TestRenderHyperlinksOnlyAllowedSchemes(t *testing.T) {
	out := Render("[relative](/rails/blobs/q3.pdf) [mail](mailto:jane@example.com) [web](https://example.com) [ftp](ftp://example.com/x)", 200)
	for _, uri := range []string{"https://example.com", "mailto:jane@example.com"} {
		if !strings.Contains(out, ";"+uri+"\x07") {
			t.Errorf("Render = %q, want a hyperlink to %s", out, uri)
		}
	}
	for _, uri := range []string{"/rails/blobs/q3.pdf", "ftp://example.com/x"} {
		if strings.Contains(out, ";"+uri+"\x07") {
			t.Errorf("Render = %q, want no hyperlink to %s", out, uri)
		}
		if !strings.Contains(visible(out), uri) {
			t.Errorf("Render = %q, want %s still visible", out, uri)
		}
	}
}

func TestContainAllowsSGRAndHyperlinks(t *testing.T) {
	for _, out := range []string{
		"plain",
		"\x1b[1mbold\x1b[0m \x1b[38;5;12mcolor\x1b[m \x1b[4:3mcurly\x1b[m",
		"\x1b]8;id=1;https://example.com\x07link\x1b]8;;\x07",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
		"tabs\tand\nnewlines",
	} {
		if got := contain(out); got != out {
			t.Errorf("contain(%q) = %q, want it unchanged", out, got)
		}
	}
}

// Whatever glamour could not have emitted means the styling goes too. The text that is
// left is what a terminal would have shown of it: an unterminated OSC swallows the rest
// of the line, a lone ESC takes the byte after it, a CSI that is not SGR is still a CSI.
func TestContainStripsEverythingWhenASequenceIsNotAllowed(t *testing.T) {
	for name, test := range map[string]struct{ out, want string }{
		"window title":      {"\x1b[1mbold\x1b[0m \x1b]0;pwned\x07text", "bold text"},
		"cursor movement":   {"\x1b[1mbold\x1b[0m \x1b[2Jtext", "bold text"},
		"device control":    {"\x1b[1mbold\x1b[0m \x1bPq#0;2;0;0;0#0~~\x1b\\text", "bold text"},
		"unterminated osc":  {"\x1b[1mbold\x1b[0m \x1b]8;;https://example.com", "bold "},
		"lone escape":       {"\x1b[1mbold\x1b[0m \x1btext", "bold ext"},
		"c1 control":        {"\x1b[1mbold\x1b[0m \u009ctext", "bold text"},
		"bel":               {"\x1b[1mbold\x1b[0m \x07text", "bold text"},
		"carriage return":   {"\x1b[1mbold\x1b[0m \rtext", "bold text"},
		"sgr with a letter": {"\x1b[1mbold\x1b[0m \x1b[3ampm", "bold mpm"},
		"control in a uri":  {"\x1b[1mbold\x1b[0m \x1b]8;;https://example.com/\x01x\x07text", "bold text"},
	} {
		got := contain(test.out)
		if strings.Contains(got, "\x1b") || hasControl(got) {
			t.Errorf("%s: contain(%q) = %q still carries a sequence", name, test.out, got)
		}
		if got != test.want {
			t.Errorf("%s: contain(%q) = %q, want %q", name, test.out, got, test.want)
		}
	}
}

func TestContainDropsHyperlinksToOtherSchemes(t *testing.T) {
	out := "\x1b]8;;javascript:alert(1)\x07click\x1b]8;;\x07 \x1b]8;;file:///etc/passwd\x1b\\here\x1b]8;;\x1b\\ \x1b]8;;HTTPS://example.com\x07ok\x1b]8;;\x07"
	got := contain(out)
	if strings.Contains(got, "javascript:") || strings.Contains(got, "file://") {
		t.Errorf("contain = %q, want the non-http hyperlinks gone", got)
	}
	if !strings.Contains(got, "\x1b]8;;HTTPS://example.com\x07ok") {
		t.Errorf("contain = %q, want the https hyperlink kept", got)
	}
	if visible(got) != "click here ok" {
		t.Errorf("contain = %q, want the link text kept", got)
	}
}

func TestRenderBoundsQuoteDepth(t *testing.T) {
	done := make(chan string, 1)
	go func() { done <- Render(strings.Repeat(">", 400)+"deep\n"+strings.Repeat(" > ", 300)+"also deep", 40) }()
	select {
	case out := <-done:
		if !strings.Contains(visible(out), "deep") {
			t.Errorf("Render = %q lost the text", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Render hung on a deeply nested quote")
	}
}

func TestRendererCacheIsBounded(t *testing.T) {
	renderersMutex.Lock()
	renderers = map[int]*glamour.TermRenderer{}
	renderersMutex.Unlock()

	for width := 20; width < 20+maxCachedRenderers*3; width++ {
		if Render("hello", width) == "" {
			t.Fatalf("Render at width %d returned nothing", width)
		}
	}

	renderersMutex.Lock()
	defer renderersMutex.Unlock()
	if len(renderers) > maxCachedRenderers {
		t.Errorf("cached %d renderers, want at most %d", len(renderers), maxCachedRenderers)
	}
}

// Whatever the Markdown, what Render emits is made of SGR, OSC 8 to http, https or
// mailto, and text with no control character in it.
func FuzzContainment(f *testing.F) {
	for _, seed := range []string{
		"hello &#27;[31mRED",
		`hello \&#27;[31mRED`,
		"`&#27;[31m`",
		"```\n&#27;[31m\n```",
		"[x](https://example.com/?a=1&b=2)",
		"[x](javascript:alert(1))",
		"<https://example.com/&#27;>",
		"\x1b]0;title\x07",
		"| a |\n| --- |\n| &#x1b;[31m |",
		"![&#27;](/rails/blobs/x.png)",
		"abc",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, md string) {
		if !utf8.ValidString(md) {
			t.Skip()
		}
		out := Render(md, 40)
		if !sgrOnly(out) {
			t.Fatalf("Render(%q) = %q carries a sequence outside the allow-list", md, out)
		}
		for _, open := range strings.Split(out, "\x1b]8;")[1:] {
			params, rest, _ := strings.Cut(open, ";")
			uri, _, _ := strings.Cut(rest, "\x07")
			if i := strings.Index(uri, "\x1b\\"); i >= 0 {
				uri = uri[:i]
			}
			if uri != "" && !allowedHyperlink(uri) {
				t.Fatalf("Render(%q) = %q links to %q (params %q)", md, out, uri, params)
			}
		}
	})
}
