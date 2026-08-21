package htmlutil

import (
	"strings"
	"testing"
)

func TestToMarkdownEmpty(t *testing.T) {
	if got := ToMarkdown(""); got != "" {
		t.Errorf("ToMarkdown empty = %q, want empty", got)
	}
}

func TestToMarkdownParagraphs(t *testing.T) {
	got := ToMarkdown("<p>The numbers are in.</p><p>Details to follow.</p>")
	want := "The numbers are in.\n\nDetails to follow."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownHeadings(t *testing.T) {
	got := ToMarkdown("<h1>Quarterly update</h1><h3>Revenue</h3>")
	want := "# Quarterly update\n\n### Revenue"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownEmphasis(t *testing.T) {
	got := ToMarkdown("<p><strong>Ryan</strong> shipped <em>fast</em>, <s>eventually</s> <code>gild</code></p>")
	want := "**Ryan** shipped *fast*, ~~eventually~~ `gild`"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownKeepsLinkURLs(t *testing.T) {
	got := ToMarkdown(`<p>See the <a href="https://example.com/reports/q3">Q3 report</a>.</p>`)
	want := "See the [Q3 report](https://example.com/reports/q3)."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownCollapsesSelfLinkingURL(t *testing.T) {
	got := ToMarkdown(`<p><a href="https://example.org">https://example.org</a></p>`)
	want := "<https://example.org>"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownNestedLists(t *testing.T) {
	got := ToMarkdown("<ul><li>Revenue up</li><li>Churn down<ul><li>enterprise flat</li></ul></li></ul>")
	want := "- Revenue up\n- Churn down\n  - enterprise flat"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownOrderedList(t *testing.T) {
	got := ToMarkdown("<ol><li>Draft the pricing change</li><li>Ship it</li></ol>")
	want := "1. Draft the pricing change\n2. Ship it"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownBlockquote(t *testing.T) {
	got := ToMarkdown("<blockquote><p>Ship before the holidays.</p></blockquote>")
	want := "> Ship before the holidays."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownCodeBlockWithLanguage(t *testing.T) {
	got := ToMarkdown(`<pre><code class="language-ruby">Card.gilded.count
</code></pre>`)
	want := "```ruby\nCard.gilded.count\n```"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownTable(t *testing.T) {
	got := ToMarkdown("<table><tr><th>Plan</th><th>Price</th></tr><tr><td>Personal</td><td>$99/yr</td></tr></table>")
	want := "| Plan | Price |\n| --- | --- |\n| Personal | $99/yr |"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownHardBreak(t *testing.T) {
	got := ToMarkdown("<p>Thanks,<br>Jason</p>")
	want := "Thanks,  \nJason"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownHorizontalRule(t *testing.T) {
	got := ToMarkdown("<p>Above</p><hr><p>Below</p>")
	want := "Above\n\n---\n\nBelow"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownImage(t *testing.T) {
	got := ToMarkdown(`<p><img src="/rails/blobs/chart.png" alt="Revenue chart"></p>`)
	want := "![Revenue chart](/rails/blobs/chart.png)"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownTrixImageAttachment(t *testing.T) {
	got := ToMarkdown(`<figure data-trix-attachment='{"url":"/rails/blobs/chart.png","filename":"chart.png","contentType":"image/png"}'></figure>`)
	want := "![chart.png](/rails/blobs/chart.png)"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownTrixFileAttachment(t *testing.T) {
	got := ToMarkdown(`<figure data-trix-attachment='{"url":"/rails/blobs/q3.pdf","filename":"q3-report.pdf","contentType":"application/pdf"}'></figure>`)
	want := "📎 q3-report.pdf"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownActionTextAttachment(t *testing.T) {
	got := ToMarkdown(`<p>Attached:</p><action-text-attachment url="/rails/blobs/q3.pdf" filename="q3-report.pdf" content-type="application/pdf"></action-text-attachment>`)
	want := "Attached:\n\n📎 q3-report.pdf"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

// HEY wraps an inbound HTML email that Trix cannot represent in a single
// text/html attachment: the entire body lives in the attachment's content
// attribute, and skipping it leaves nothing but HEY's truncated summary.
func TestToMarkdownEmbeddedHTMLAttachment(t *testing.T) {
	got := ToMarkdown(`<div><figure data-trix-attachment='{"contentType":"text/html","content":"<shadow-content><template><style>p{color:red}</style><p>Dear customer,</p><p>Please <a href=\"https://example.com/sign\">sign the document</a>.</p></template></shadow-content>","data":{}}'></figure></div>`)

	want := "Dear customer,\n\nPlease [sign the document](https://example.com/sign)."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownEmbeddedContentStopsRecursing(t *testing.T) {
	nested := `<figure data-trix-attachment='{"contentType":"text/html","content":"<p>innermost</p>"}'></figure>`
	for range embeddedContentDepthLimit + 2 {
		nested = `<figure data-trix-attachment='{"contentType":"text/html","content":"` +
			strings.ReplaceAll(nested, `"`, `\"`) + `"}'></figure>`
	}

	if got := ToMarkdown(nested); strings.Contains(got, "innermost") {
		t.Errorf("ToMarkdown = %q, should stop before the innermost level", got)
	}
}

func TestToMarkdownKeepsSpaceAroundInlineElements(t *testing.T) {
	for _, test := range []struct{ name, in, want string }{
		{
			name: "space inside a link",
			in:   `<p>Prijavio sam se na<a href="https://example.com/sign"> Web Sign</a> i radilo je.</p>`,
			want: "Prijavio sam se na [Web Sign](https://example.com/sign) i radilo je.",
		},
		{
			name: "space inside bold",
			in:   "<p><strong>Bold </strong>then text</p>",
			want: "**Bold** then text",
		},
		{
			name: "space on both sides of emphasis",
			in:   "<p>a<em> b </em>c</p>",
			want: "a *b* c",
		},
		{
			name: "no space to keep",
			in:   `<p>a<a href="https://example.com">b</a>c</p>`,
			want: "a[b](https://example.com)c",
		},
		{
			// Outlook separates a link from its neighbours with &nbsp;, which
			// is two bytes wide — a byte-wise whitespace check misses it and
			// runs the words into the link.
			name: "non-breaking spaces around a link",
			in:   "<div>I signed the forms on&nbsp;<a href=\"https://example.com/sign\">Web Sign</a>&nbsp;and it worked.</div>",
			want: "I signed the forms on [Web Sign](https://example.com/sign) and it worked.",
		},
		{
			name: "non-breaking space between words",
			in:   "<p>the invoice came to 10&nbsp;000 kroner</p>",
			want: "the invoice came to 10 000 kroner",
		},
	} {
		if got := ToMarkdown(test.in); got != test.want {
			t.Errorf("%s: ToMarkdown = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestToMarkdownStripsScriptAndStyle(t *testing.T) {
	got := ToMarkdown("<style>p{color:red}</style><p>Hello</p><script>alert('xss')</script>")
	if got != "Hello" {
		t.Errorf("ToMarkdown = %q, want %q", got, "Hello")
	}
}

// An entity is decoded once, by the HTML parser, and what it stood for is then
// escaped so that no Markdown renderer decodes it a second time.
func TestToMarkdownDecodesEntitiesOnce(t *testing.T) {
	got := ToMarkdown("<p>Fried &amp; Hansson &lt;hey&gt; &amp;amp;</p>")
	want := `Fried &amp; Hansson \<hey\> &amp;amp;`
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
	if text := renderedText(t, got); text != "Fried & Hansson <hey> &amp;" {
		t.Errorf("rendered = %q, want the literal text back", text)
	}
}

func TestToMarkdownCollapsesSourceWhitespace(t *testing.T) {
	got := ToMarkdown("<div>\n  <p>\n    The numbers   are\n    in.\n  </p>\n</div>")
	want := "The numbers are in."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownWholeEmail(t *testing.T) {
	got := ToMarkdown(`<div class="trix-content"><h2>Quarterly update</h2>` +
		`<p>Hi <strong>Ryan</strong>,</p>` +
		`<p>See the <a href="https://example.com/reports/q3">Q3 report</a>.</p>` +
		`<ul><li>Revenue up <em>12%</em></li></ul>` +
		`<blockquote><p>Ship the pricing change first.</p></blockquote>` +
		`<p>Thanks,<br>Jason</p></div>`)

	want := "## Quarterly update\n\n" +
		"Hi **Ryan**,\n\n" +
		"See the [Q3 report](https://example.com/reports/q3).\n\n" +
		"- Revenue up *12%*\n\n" +
		"> Ship the pricing change first.\n\n" +
		"Thanks,  \nJason"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}
