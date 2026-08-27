package htmlutil

import "testing"

func TestFromMarkdown(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want string
	}{
		{
			name: "plain paragraph",
			md:   "Hello there",
			want: "<p>Hello there</p>",
		},
		{
			name: "single newline becomes a hard break without indenting the next line",
			md:   "Line one\nLine two",
			want: "<p>Line one<br>Line two</p>",
		},
		{
			name: "CRLF newline becomes a hard break without indenting the next line",
			md:   "Line one\r\nLine two",
			want: "<p>Line one<br>Line two</p>",
		},
		{
			name: "every continuation line starts flush",
			md:   "Line one\nLine two\nLine three",
			want: "<p>Line one<br>Line two<br>Line three</p>",
		},
		{
			name: "two-space Markdown break starts the next line flush",
			md:   "Line one  \nLine two",
			want: "<p>Line one<br>Line two</p>",
		},
		{
			name: "backslash Markdown break starts the next line flush",
			md:   "Line one\\\nLine two",
			want: "<p>Line one<br>Line two</p>",
		},
		{
			name: "blank line splits paragraphs",
			md:   "Para one\n\nPara two",
			want: "<p>Para one</p>\n<p>Para two</p>",
		},
		{
			name: "wrapped inline markup keeps escaping",
			md:   "**Bold** & safe\n[HEY](https://hey.com)",
			want: `<p><strong>Bold</strong> &amp; safe<br><a href="https://hey.com">HEY</a></p>`,
		},
		{
			name: "line after inline code starts flush",
			md:   "Run `hey box list`\nthen choose a box",
			want: "<p>Run <code>hey box list</code><br>then choose a box</p>",
		},
		{
			name: "line after image starts flush",
			md:   "![Revenue chart](https://example.com/chart.png)\nReview the chart",
			want: `<p><img src="https://example.com/chart.png" alt="Revenue chart"><br>Review the chart</p>`,
		},
		{
			name: "wrapped list item keeps its structure",
			md:   "- First line\n  second line",
			want: "<ul>\n<li>First line<br>second line</li>\n</ul>",
		},
		{
			name: "wrapped blockquote keeps its structure",
			md:   "> First line\n> second line",
			want: "<blockquote>\n<p>First line<br>second line</p>\n</blockquote>",
		},
		{
			name: "inline emphasis and strikethrough",
			md:   "**bold** and *italic* and ~~gone~~",
			want: "<p><strong>bold</strong> and <em>italic</em> and <del>gone</del></p>",
		},
		{
			name: "heading and list",
			md:   "# Agenda\n\n- budget\n- hiring",
			want: "<h1>Agenda</h1>\n<ul>\n<li>budget</li>\n<li>hiring</li>\n</ul>",
		},
		{
			name: "links keep their destinations",
			md:   "[HEY](https://hey.com) and <https://example.com>",
			want: `<p><a href="https://hey.com">HEY</a> and <a href="https://example.com">https://example.com</a></p>`,
		},
		{
			name: "metacharacters are escaped",
			md:   "a & b < c",
			want: "<p>a &amp; b &lt; c</p>",
		},
		{
			name: "raw HTML passes through",
			md:   "<div>raw <strong>html</strong></div>",
			want: "<div>raw <strong>html</strong></div>",
		},
		{
			name: "raw preformatted HTML keeps break-adjacent whitespace",
			md:   "<pre>first<br>\n second</pre>",
			want: "<pre>first<br>\n second</pre>",
		},
		{
			name: "code span and fence stay verbatim",
			md:   "`hey box list`\n\n```\nmake test\n```",
			want: "<p><code>hey box list</code></p>\n<pre><code>make test\n</code></pre>",
		},
		{
			name: "fenced code preserves newlines and literal breaks",
			md:   "```\nfirst<br>\n second\n```",
			want: "<pre><code>first&lt;br&gt;\n second\n</code></pre>",
		},
		{
			name: "fence language becomes HEY's pre attribute",
			md:   "```ruby\nputs \"hey\"\n```",
			want: "<pre language=\"ruby\"><code>puts &quot;hey&quot;\n</code></pre>",
		},
		{
			name: "fence language aliases map to HEY's names",
			md:   "```js\nconsole.log(1 < 2)\n```",
			want: "<pre language=\"javascript\"><code>console.log(1 &lt; 2)\n</code></pre>",
		},
		{
			name: "unknown fence language carries no attribute",
			md:   "```brainfuck\n+++\n```",
			want: "<pre><code>+++\n</code></pre>",
		},
		{
			name: "blockquote",
			md:   "> quoted reply",
			want: "<blockquote>\n<p>quoted reply</p>\n</blockquote>",
		},
		{
			name: "empty input stays empty",
			md:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromMarkdown(tt.md); got != tt.want {
				t.Errorf("FromMarkdown(%q) = %q, want %q", tt.md, got, tt.want)
			}
		})
	}
}

func TestMarkdownHardBreakRoundTripKeepsItsMeaningWithoutHTMLWhitespace(t *testing.T) {
	const source = "AAA.\nBBB.\n\nCCC."
	const body = "<p>AAA.<br>BBB.</p>\n<p>CCC.</p>"

	if got := FromMarkdown(source); got != body {
		t.Fatalf("FromMarkdown() = %q, want %q", got, body)
	}
	markdown := ToMarkdown(body).String()
	if want := "AAA.  \nBBB.\n\nCCC."; markdown != want {
		t.Fatalf("ToMarkdown() = %q, want %q", markdown, want)
	}
	if got := FromMarkdown(markdown); got != body {
		t.Errorf("FromMarkdown(ToMarkdown()) = %q, want %q", got, body)
	}
}

func TestPrependHTML(t *testing.T) {
	got := PrependHTML("<div>Forwarded message</div>", "<p>For <strong>your</strong> review</p>")
	want := "<p>For <strong>your</strong> review</p><br><div>Forwarded message</div>"
	if got != want {
		t.Errorf("PrependHTML() = %q, want %q", got, want)
	}
}

func TestPrependHTMLWithoutNote(t *testing.T) {
	content := "<div>Forwarded message</div>"
	if got := PrependHTML(content, "  "); got != content {
		t.Errorf("PrependHTML() = %q, want the content untouched", got)
	}
}
