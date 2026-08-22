package markdown

import (
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

// ToMarkdown writes every ampersand in prose as &amp;, and Render must show that as
// the ampersand it is — not re-encode it into a visible "&amp;" — while still never
// decoding what the email spelled as an entity.
func TestRenderShowsWhatToMarkdownWrote(t *testing.T) {
	for html, want := range map[string]string{
		"<p>Fried &amp; Hansson</p>":                                     "Fried & Hansson",
		"<p>AT&amp;T, a&amp;b=1</p>":                                     "AT&T, a&b=1",
		"<p>hello &amp;#27;[31mRED</p>":                                  "hello &#27;[31mRED",
		"<p>&amp;copy; is not ©</p>":                                     "&copy; is not ©",
		"<p><code>a &amp;&amp; b</code></p>":                             "a && b",
		"<p><code>&amp;amp;</code></p>":                                  "&amp;",
		"<p><code>&amp;#27;[31m</code></p>":                              "&#27;[31m",
		"<pre>x &amp;&amp; y</pre>":                                      "x && y",
		"<pre>&amp;#27;[31m</pre>":                                       "&#27;[31m",
		"<p><img alt=\"&amp;#27;[31m\" src=\"https://e.com/i.png\"></p>": "&#27;[31m",
		"<p><a href=\"https://e.com/?a=1&amp;b=2\">q</a></p>":            "https://e.com/?a=1&b=2",
		"<p><img alt=\"a &amp; b\" src=\"https://e.com/i.png\"></p>":     "a & b",
		"<p>\u200b# not a heading</p>":                                   "# not a heading",
		"<p>1\u200b. not a list</p>":                                     "1. not a list",
	} {
		out := Render(htmlutil.ToMarkdown(html), 200)
		if shown := visible(out); !strings.Contains(shown, want) {
			t.Errorf("Render(ToMarkdown(%q)) shows %q, want %q in it", html, shown, want)
		}
		if strings.Contains(out, "\x1b[31m") {
			t.Errorf("Render(ToMarkdown(%q)) = %q turned red", html, out)
		}
	}
}
