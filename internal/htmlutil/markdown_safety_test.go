package htmlutil

import (
	"bytes"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"golang.org/x/net/html"
)

// The oracle for these tests is a conformant Markdown renderer: goldmark, with the
// same extensions glamour enables. What it renders is what a reader — or the agent
// that rendered a `--json` body — sees, so the assertions are about that rendering
// rather than about the Markdown text.

var conformant = goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList))

// renderedText is the text a conformant renderer shows for md, entities and
// backslash escapes resolved.
func renderedText(t *testing.T, md string) string {
	t.Helper()
	return strings.TrimSpace(domText(t, renderedHTML(t, md)))
}

// renderedLinks is every href a conformant renderer emits for md, in order, with the
// percent-encoding both sides add decoded back to the URL a consumer follows.
func renderedLinks(t *testing.T, md string) []string {
	t.Helper()
	var links []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "a" || n.Data == "img") {
			for _, attr := range n.Attr {
				if attr.Key == "href" || attr.Key == "src" {
					decoded, err := url.PathUnescape(attr.Val)
					if err != nil {
						t.Fatalf("decode %q: %v", attr.Val, err)
					}
					links = append(links, decoded)
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(parsedHTML(t, renderedHTML(t, md)))
	return links
}

func renderedHTML(t *testing.T, md string) string {
	t.Helper()
	var out bytes.Buffer
	if err := conformant.Convert([]byte(md), &out); err != nil {
		t.Fatalf("goldmark: %v", err)
	}
	return out.String()
}

func parsedHTML(t *testing.T, s string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse rendered HTML: %v", err)
	}
	return doc
}

func domText(t *testing.T, s string) string {
	t.Helper()
	var b strings.Builder
	collectText(parsedHTML(t, s), &b)
	return b.String()
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return r != '\n' && r != '\t' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f))
	})
}

// The double decode: "&amp;#27;" in the HTML is the eight characters "&#27;[31m"
// in the email, and a Markdown renderer that decodes entities would turn them into
// a live escape byte.
func TestToMarkdownTextEntityCannotDecodeTwice(t *testing.T) {
	got := toMarkdown(`<p>hello &amp;#27;[31mRED</p>`)
	if got != `hello &amp;#27;\[31mRED` {
		t.Errorf("ToMarkdown = %q", got)
	}
	if text := renderedText(t, got); text != "hello &#27;[31mRED" || hasControl(text) {
		t.Errorf("rendered = %q, want the literal characters", text)
	}
}

func TestToMarkdownTextIsNeverSyntax(t *testing.T) {
	for _, literal := range []string{
		"*not emphasis* _nor this_ ~~nor that~~",
		"[not a link](x) ![nor an image](x.png)",
		"<b>not a tag</b> <!-- nor a comment --> </b> <?php",
		"Fried & Hansson, a < b, 3 > 2, &copy; &#169; &#xA9; &amp;",
		"AT&T, a&b=1",
		"`not code` ``nor this``",
		"# not a heading",
		"> not a quote",
		"- not a list item",
		"+ not a list item",
		"1. not a list item",
		"2024. was a year",
		"1) not a list item",
		"---",
		"===",
		"a | b",
		"back\\slash",
	} {
		md := toMarkdown("<p>" + html.EscapeString(literal) + "</p>")
		if got := renderedText(t, md); got != literal {
			t.Errorf("%q: ToMarkdown = %q renders as %q", literal, md, got)
		}
		if links := renderedLinks(t, md); len(links) != 0 {
			t.Errorf("%q: ToMarkdown = %q renders links %q", literal, md, links)
		}
	}
}

// Text arrives a node at a time, and what is syntax is decided by what ends up side by
// side on a line, not by what one node held.
func TestToMarkdownTextIsNeverSyntaxAcrossNodes(t *testing.T) {
	for _, test := range []struct{ in, literal string }{
		{`<p>x&lt;<span>b&gt;</span></p>`, "x<b>"},
		{`<p><img alt="&amp;#20">;</p>`, "&#20;"},
		{`<p>1<span>.</span> x</p>`, "1. x"},
		{`<p><span>&amp;</span>amp;</p>`, "&amp;"},
		{`<p><span>*</span>x<span>*</span></p>`, "*x*"},
	} {
		md := toMarkdown(test.in)
		if got := renderedText(t, md); got != test.literal {
			t.Errorf("%s: ToMarkdown = %q renders as %q, want %q", test.in, md, got, test.literal)
		}
		if rendered := renderedHTML(t, md); strings.Contains(rendered, "<ol>") || strings.Contains(rendered, "<b>") || strings.Contains(rendered, "<em>") {
			t.Errorf("%s: ToMarkdown = %q renders structure: %q", test.in, md, rendered)
		}
	}
}

func TestToMarkdownKeepsTheReplacementCharacter(t *testing.T) {
	got := toMarkdown("<p>caf\uFFFDe <code>\uFFFD</code></p>")
	if got != "caf\uFFFDe `\uFFFD`" {
		t.Errorf("ToMarkdown = %q", got)
	}
}

func TestToMarkdownTextStripsControls(t *testing.T) {
	for name, in := range map[string]string{
		"escape":          "<p>\x1b[31mRED</p>",
		"osc":             "<p>\x1b]0;title\x07name</p>",
		"numeric entity":  "<p>&#27;[31mRED</p>",
		"c1":              "<p>caf\u009ce</p>",
		"del":             "<p>note\x7f</p>",
		"carriage return": "<p>Invoice\rPAID</p>",
	} {
		if got := toMarkdown(in); hasControl(got) {
			t.Errorf("%s: ToMarkdown = %q carries a control character", name, got)
		}
	}
}

func TestToMarkdownInlineCodeEntityCannotDecode(t *testing.T) {
	got := toMarkdown(`<p><code>&amp;#27;[31mRED</code></p>`)
	if got != "`&#27;[31mRED`" {
		t.Errorf("ToMarkdown = %q", got)
	}
	if text := renderedText(t, got); text != "&#27;[31mRED" {
		t.Errorf("rendered = %q, want the literal characters", text)
	}
}

func TestToMarkdownInlineCodeSizesItsDelimiters(t *testing.T) {
	for _, test := range []struct{ in, want, literal string }{
		{"<code>a ` b</code>", "``a ` b``", "a ` b"},
		{"<code>a  b\tc</code>", "`a  b\tc`", "a  b\tc"},
		{"<code> x </code>", "`  x  `", " x "},
		{"<code>   </code>", "", ""},
		{"<code>`tick</code>", "`` `tick ``", "`tick"},
		{"<code>``</code>", "``` `` ```", "``"},
		{"<code>\x1b[31m x</code>", "`[31m x`", "[31m x"},
		{"<code>a\nb</code>", "`a b`", "a b"},
		{"<code></code>", "", ""},
	} {
		got := toMarkdown("<p>" + test.in + "</p>")
		if got != test.want {
			t.Errorf("%s: ToMarkdown = %q, want %q", test.in, got, test.want)
		}
		// The code element in the rendered HTML holds the content exactly, padding
		// and all, which is more than the trimmed text oracle can say.
		rendered := renderedHTML(t, got)
		wantCode := "<code>" + html.EscapeString(test.literal) + "</code>"
		if test.literal == "" {
			wantCode = ""
		}
		if (wantCode == "" && strings.Contains(rendered, "<code>")) || (wantCode != "" && !strings.Contains(rendered, wantCode)) {
			t.Errorf("%s: rendered = %q, want %q", test.in, rendered, wantCode)
		}
	}
}

// A delimiter is sized on the content as a terminal will see it: the renderer's
// sanitizer drops a zero width space between two runs of backticks, joining them, so
// the delimiter must be longer than the joined run or the code could be closed from
// inside by text that only looks shorter.
func TestToMarkdownCodeDelimitersOutlastTheSanitizer(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{"<p><code>``\u200b``</code></p>", "````` ``\u200b`` `````"},
		{"<p><code>\u200b`x</code></p>", "`` \u200b`x ``"},
		{"<p><code>x`\u200b</code></p>", "`` x`\u200b ``"},
		{"<pre>``\u200b``\nafter</pre>", "`````\n``\u200b``\nafter\n`````"},
		{"<pre>``\u200d``</pre>", "`````\n``\u200d``\n`````"},
	} {
		got := toMarkdown(test.in)
		if got != test.want {
			t.Errorf("ToMarkdown(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestToMarkdownFencedCodeCannotBeClosedFromInside(t *testing.T) {
	got := toMarkdown("<pre>first\n```\nnot outside\n</pre><p>after</p>")
	want := "````\nfirst\n```\nnot outside\n````\n\nafter"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
	rendered := renderedHTML(t, got)
	if !strings.Contains(rendered, "<code>first\n```\nnot outside\n</code>") {
		t.Errorf("rendered = %q, want one code block holding the fence", rendered)
	}
}

func TestToMarkdownFencedCodeKeepsBlankLinesAndStripsControls(t *testing.T) {
	got := toMarkdown("<pre>one\n\n\x1b[31mtwo\t3</pre>")
	want := "```\none\n\n[31mtwo\t3\n```"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownFencedCodeInfoStringIsALanguageOrNothing(t *testing.T) {
	for _, test := range []struct{ class, want string }{
		{"language-ruby", "```ruby"},
		{"language-c++", "```c++"},
		{"language-objective-c", "```objective-c"},
		{"language-rb`", "```"},
		{"language-a b", "```a"},
		{"language-" + strings.Repeat("x", 40), "```"},
		{"language-\x1b[31m", "```"},
	} {
		got := toMarkdown(`<pre><code class="` + test.class + `">x</code></pre>`)
		if first := strings.SplitN(got, "\n", 2)[0]; first != test.want {
			t.Errorf("%s: fence = %q, want %q", test.class, first, test.want)
		}
	}
}

// A closing parenthesis in an href would end the Markdown destination, leaving the
// rest of the attribute in the paragraph as text a renderer parses.
func TestToMarkdownDestinationCannotExitTheLink(t *testing.T) {
	got := toMarkdown(`<p><a href=")&#27;[31mRED">x</a></p>`)
	if got != "[x](%29[31mRED)" {
		t.Errorf("ToMarkdown = %q", got)
	}
	if hasControl(got) {
		t.Errorf("ToMarkdown = %q carries a control character", got)
	}
	if links := renderedLinks(t, got); len(links) != 1 || links[0] != ")[31mRED" {
		t.Errorf("rendered links = %q, want the label's own URL, decoded", links)
	}
	if text := renderedText(t, got); text != "x" {
		t.Errorf("rendered = %q, want just the label", text)
	}
}

func TestToMarkdownDestinationKeepsQueryStrings(t *testing.T) {
	got := toMarkdown(`<p><a href="https://example.com/a?b=1&amp;c=2&amp;#27;&amp;copy=1">q</a></p>`)
	if got != "[q](https://example.com/a?b=1&c=2&amp;#27;&copy=1)" {
		t.Errorf("ToMarkdown = %q", got)
	}
	if links := renderedLinks(t, got); len(links) != 1 || links[0] != "https://example.com/a?b=1&c=2&#27;&copy=1" {
		t.Errorf("rendered links = %q, want the query string intact", links)
	}
}

func TestToMarkdownDestinationEncodesWhatWouldEndIt(t *testing.T) {
	got := toMarkdown(`<p><a href="https://example.com/a b(c)<d>\e|f">x</a></p>`)
	want := `[x](https://example.com/a%20b%28c%29%3Cd%3E%5Ce%7Cf)`
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
	if links := renderedLinks(t, got); len(links) != 1 || links[0] != `https://example.com/a b(c)<d>\e|f` {
		t.Errorf("rendered links = %q, want the URL decoded", links)
	}
}

func TestToMarkdownDestinationSchemes(t *testing.T) {
	for _, test := range []struct {
		href, want string
		linked     bool
	}{
		{"https://example.com/x", "[x](https://example.com/x)", true},
		{"HTTP://EXAMPLE.COM/x", "[x](HTTP://EXAMPLE.COM/x)", true},
		{"mailto:jane@example.com", "[x](mailto:jane@example.com)", true},
		{"/rails/active_storage/blobs/abc/chart.png", "[x](/rails/active_storage/blobs/abc/chart.png)", true},
		{"#section", "[x](#section)", true},
		{"javascript:alert(1)", "x", false},
		{"data:text/html,<script>", "x", false},
		{"file:///etc/passwd", "x", false},
		{"vbscript:x", "x", false},
		{"  javascript:alert(1)", "x", false},
		{"", "x", false},
	} {
		got := toMarkdown(`<p><a href="` + html.EscapeString(test.href) + `">x</a></p>`)
		if got != test.want {
			t.Errorf("%q: ToMarkdown = %q, want %q", test.href, got, test.want)
		}
		if linked := len(renderedLinks(t, got)) == 1; linked != test.linked {
			t.Errorf("%q: linked = %v, want %v", test.href, linked, test.linked)
		}
	}
}

func TestToMarkdownAutolinkOnlyForAbsoluteURLs(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{`<a href="https://example.org">https://example.org</a>`, "<https://example.org>"},
		{`<a href="https://example.org"></a>`, "<https://example.org>"},
		{`<a href="/rails/blobs/q3.pdf">/rails/blobs/q3.pdf</a>`, "[/rails/blobs/q3.pdf](/rails/blobs/q3.pdf)"},
		{`<a href="/rails/blobs/q3.pdf"></a>`, "[/rails/blobs/q3.pdf](/rails/blobs/q3.pdf)"},
		{`<a href="https://example.org/a b">https://example.org/a b</a>`, "<https://example.org/a%20b>"},
	} {
		if got := toMarkdown("<p>" + test.in + "</p>"); got != test.want {
			t.Errorf("%s: ToMarkdown = %q, want %q", test.in, got, test.want)
		}
	}
}

// A label that reads as one URL or host while pointing at another never collapses
// into an autolink: the destination is written beside the label, where it can be
// compared. That holds for a homoglyph host as much as an honest one — the Cyrillic
// а in "pаypal" is not detected, it simply is not the href — and a conformant
// renderer links to the destination while showing the label.
func TestToMarkdownDeceptiveLabelShowsTheDestination(t *testing.T) {
	const homoglyphHost = "https://p\u0430ypal.com/login"
	for _, test := range []struct {
		name, label, href, want string
	}{
		{"a URL", "https://bank.example/login", "https://evil.example/login", "[https://bank.example/login](https://evil.example/login)"},
		{"a homoglyph URL", homoglyphHost, "https://evil.example/login", "[" + homoglyphHost + "](https://evil.example/login)"},
		{"a www host", "www.bank.example", "https://evil.example", "[www.bank.example](https://evil.example)"},
		{"a bare host and path", "bank.example/login", "https://evil.example/login", "[bank.example/login](https://evil.example/login)"},
		{"the same host, a different path", "https://bank.example/", "https://bank.example/login", "[https://bank.example/](https://bank.example/login)"},
		{"a label that is its href", homoglyphHost, homoglyphHost, "<" + homoglyphHost + ">"},
	} {
		got := toMarkdown(`<p><a href="` + test.href + `">` + test.label + `</a></p>`)
		if got != test.want {
			t.Errorf("%s: ToMarkdown = %q, want %q", test.name, got, test.want)
		}
		if links := renderedLinks(t, got); len(links) != 1 || links[0] != test.href {
			t.Errorf("%s: rendered links = %q, want the destination %q", test.name, links, test.href)
		}
		if text := renderedText(t, got); text != test.label {
			t.Errorf("%s: rendered = %q, want the label %q", test.name, text, test.label)
		}
	}
}

func TestToMarkdownImageAltAndSourceAreSerialized(t *testing.T) {
	got := toMarkdown(`<p><img alt="a]b &amp;#27;" src="https://example.com/i.png?x=1&amp;y=2"> <img alt="gone" src="javascript:x"> <img src="data:image/png;base64,AAAA"></p>`)
	want := `![a\]b &amp;#27;](https://example.com/i.png?x=1&y=2) gone`
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
	if links := renderedLinks(t, got); len(links) != 1 || links[0] != "https://example.com/i.png?x=1&y=2" {
		t.Errorf("rendered links = %q", links)
	}
}

// An image whose source may not be linked leaves its alt text as prose, escaped as
// prose: at the start of a line "# title" is a heading unless it is escaped there.
func TestToMarkdownUnlinkableImageAltIsProse(t *testing.T) {
	got := toMarkdown(`<p><img alt="# title" src="javascript:x"></p>`)
	if got != `\# title` {
		t.Errorf("ToMarkdown = %q", got)
	}
	if text := renderedText(t, got); text != "# title" {
		t.Errorf("rendered = %q, want the literal alt text", text)
	}
}

func TestToMarkdownAttachmentNamesAreSerialized(t *testing.T) {
	got := toMarkdown(`<figure data-trix-attachment='{"url":"javascript:x","filename":"*report*.png","contentType":"image/png"}'></figure>` +
		`<figure data-trix-attachment='{"url":"/rails/blobs/q3.pdf","filename":"[q3].pdf","contentType":"application/pdf"}'></figure>`)
	want := "📎 \\*report\\*.png\n\n📎 \\[q3\\].pdf"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownTableCellsEscapePipes(t *testing.T) {
	got := toMarkdown("<table><tr><th>a|b</th></tr><tr><td>c | d</td></tr></table>")
	want := "| a\\|b |\n| --- |\n| c \\| d |"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
	if text := renderedText(t, got); !strings.Contains(text, "a|b") || !strings.Contains(text, "c | d") {
		t.Errorf("rendered = %q, want both cells intact", text)
	}
}

// A run of dots on a long line is linear work: the ordered-marker check looks at the
// length before it joins the line to the run.
func TestToMarkdownEscapesALongLineInLinearTime(t *testing.T) {
	long := strings.Repeat("a.b)c. ", 100_000)
	start := time.Now()
	got := toMarkdown("<p>" + long + "</p>")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("ToMarkdown took %s on a %d-byte line", elapsed, len(long))
	}
	if !strings.HasPrefix(got, "a.b)c. a.b)c.") {
		t.Errorf("ToMarkdown = %.40q, want the dots unescaped mid-line", got)
	}
}

// Conversion is linear in the size of the body: a line is built in a buffer, not by
// concatenating strings, so doubling the input doubles the work rather than
// quadrupling it.
func TestToMarkdownAllocatesLinearly(t *testing.T) {
	allocated := func(size int) uint64 {
		body := "<p>" + strings.Repeat("Fried &amp; Hansson <b>shipped</b> it. ", size/40) + "</p>"
		toMarkdown(body)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		toMarkdown(body)
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	small, large := allocated(256<<10), allocated(1024<<10)
	if ratio := float64(large) / float64(small); ratio > 6 {
		t.Errorf("4x the input allocated %.1fx as much (%d vs %d bytes); conversion is not linear", ratio, large, small)
	}
}

func TestToMarkdownBoundsQuoteAndListDepth(t *testing.T) {
	quotes := toMarkdown(strings.Repeat("<blockquote>", 40) + "deep" + strings.Repeat("</blockquote>", 40))
	if got := strings.Count(quotes, ">"); got != maxNestingDepth {
		t.Errorf("quote depth = %d, want %d: %q", got, maxNestingDepth, quotes)
	}

	var b strings.Builder
	for range 40 {
		b.WriteString("<ul><li>item")
	}
	lists := toMarkdown(b.String())
	for _, line := range strings.Split(lists, "\n") {
		if indent := len(line) - len(strings.TrimLeft(line, " ")); indent > 2*(maxNestingDepth-1) {
			t.Errorf("list indented %d columns, want at most %d: %q", indent, 2*(maxNestingDepth-1), line)
		}
	}
	if got := strings.Count(lists, "- item"); got != 40 {
		t.Errorf("kept %d items with markers, want 40: %q", got, lists)
	}
}

// Past the cap, sibling items keep their markers and their lines rather than running
// into one another.
func TestToMarkdownCappedListItemsStayApart(t *testing.T) {
	var b strings.Builder
	for range maxNestingDepth {
		b.WriteString("<ul><li>outer")
	}
	b.WriteString("<ul><li>b</li><li>c</li></ul>")
	got := toMarkdown(b.String())
	if strings.Contains(got, "bc") || !strings.Contains(got, "- b\n") || !strings.Contains(got, "- c") {
		t.Errorf("ToMarkdown = %q, want b and c as separate items", got)
	}
	if rendered := renderedHTML(t, got); strings.Count(rendered, "<li>") != maxNestingDepth+2 {
		t.Errorf("rendered %d items, want %d: %q", strings.Count(rendered, "<li>"), maxNestingDepth+2, rendered)
	}
}

func TestToMarkdownDeepNestingIsText(t *testing.T) {
	got := toMarkdown(strings.Repeat("<div>", 5000) + "x" + strings.Repeat("</div>", 5000))
	if hasControl(got) {
		t.Errorf("ToMarkdown carries a control character")
	}
	if rendered := renderedHTML(t, got); strings.Contains(rendered, "<div>") {
		t.Errorf("rendered = %.80q, want no raw HTML", rendered)
	}
}

// assertSafeTree holds the serializer property on goldmark's own tree: Markdown from
// ToMarkdown parses to no raw HTML, links and images only to allowed destinations, and
// text with no control character in it. It is the AST-level statement of what the four
// serializers promise, and the one a future serializer has to keep.
func assertSafeTree(t *testing.T, md string) {
	t.Helper()
	source := []byte(md)
	doc := conformant.Parser().Parse(text.NewReader(source))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.RawHTML, *ast.HTMLBlock:
			t.Errorf("ToMarkdown produced raw HTML in %q", md)
		case *ast.Link:
			if !allowedScheme(string(n.Destination)) || hasControl(string(n.Destination)) {
				t.Errorf("ToMarkdown linked to %q in %q", n.Destination, md)
			}
		case *ast.Image:
			if !allowedScheme(string(n.Destination)) || hasControl(string(n.Destination)) {
				t.Errorf("ToMarkdown imaged %q in %q", n.Destination, md)
			}
		case *ast.AutoLink:
			if url := string(n.URL(source)); !allowedScheme(url) || hasControl(url) {
				t.Errorf("ToMarkdown autolinked %q in %q", url, md)
			}
		case *ast.Text:
			if hasControl(string(n.Segment.Value(source))) {
				t.Errorf("ToMarkdown left a control in %q", md)
			}
		}
		return ast.WalkContinue, nil
	})
}

func TestToMarkdownTreeIsSafe(t *testing.T) {
	for _, in := range []string{
		`<p>hello &amp;#27;[31mRED <a href="javascript:alert(1)">x</a> <img src="data:x" alt="a"> <b>&lt;script&gt;</b></p>`,
		`<p><a href=")&#27;[31m">x</a> <a href="https://example.com/a?b=1&amp;c=2">q</a></p>`,
		"<pre>```\n&amp;#27;</pre><table><tr><td>&amp;#27;|</td></tr></table>",
	} {
		assertSafeTree(t, toMarkdown(in))
	}
}

// Whatever the HTML, the Markdown carries no control character, and neither does
// what a conformant renderer makes of it: an entity that survives to the renderer
// is decoded there, so this is the property that closes the double decode.
func FuzzToMarkdownTerminalSafety(f *testing.F) {
	for _, seed := range []string{
		`<p>hello &amp;#27;[31mRED</p>`,
		`<p><code>&amp;#27;[31m</code></p>`,
		`<p><a href=")&#27;[31mRED">x</a></p>`,
		`<p><a href="https://example.com/?a=1&amp;b=2">q</a></p>`,
		"<pre>```\n&amp;#27;</pre>",
		"<p>\x1b]8;;https://example.com\x07link\x1b]8;;\x07</p>",
		`<img alt="&amp;#x1b;" src="&amp;#27;">`,
		`<table><tr><td>&amp;#27;|</td></tr></table>`,
		`<figure data-trix-attachment='{"filename":"&amp;#27;","url":"javascript:1","contentType":"image/png"}'></figure>`,
		"<p>caf\u009c\u0085e</p>",
		`<p><a href="https://evil.example/login">https://p\u0430ypal.com/login</a></p>`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		if !utf8.ValidString(in) {
			t.Skip()
		}
		md := toMarkdown(in)
		if hasControl(md) {
			t.Fatalf("toMarkdown(%q) = %q carries a control character", in, md)
		}
		var out bytes.Buffer
		if err := conformant.Convert([]byte(md), &out); err != nil {
			t.Fatalf("goldmark: %v", err)
		}
		if text := domText(t, out.String()); hasControl(text) {
			t.Fatalf("toMarkdown(%q) = %q renders with a control character: %q", in, md, text)
		}
		assertSafeTree(t, md)
	})
}
