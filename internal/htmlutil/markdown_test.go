package htmlutil

import (
	"strings"
	"testing"
)

func TestToMarkdownEmpty(t *testing.T) {
	if got := toMarkdown(""); got != "" {
		t.Errorf("ToMarkdown empty = %q, want empty", got)
	}
}

func TestToMarkdownParagraphs(t *testing.T) {
	got := toMarkdown("<p>The numbers are in.</p><p>Details to follow.</p>")
	want := "The numbers are in.\n\nDetails to follow."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownHeadings(t *testing.T) {
	got := toMarkdown("<h1>Quarterly update</h1><h3>Revenue</h3>")
	want := "# Quarterly update\n\n### Revenue"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownEmphasis(t *testing.T) {
	got := toMarkdown("<p><strong>Ryan</strong> shipped <em>fast</em>, <s>eventually</s> <code>gild</code></p>")
	want := "**Ryan** shipped *fast*, ~~eventually~~ `gild`"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownKeepsLinkURLs(t *testing.T) {
	got := toMarkdown(`<p>See the <a href="https://example.com/reports/q3">Q3 report</a>.</p>`)
	want := "See the [Q3 report](https://example.com/reports/q3)."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownCollapsesSelfLinkingURL(t *testing.T) {
	got := toMarkdown(`<p><a href="https://example.org">https://example.org</a></p>`)
	want := "<https://example.org>"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownNestedLists(t *testing.T) {
	got := toMarkdown("<ul><li>Revenue up</li><li>Churn down<ul><li>enterprise flat</li></ul></li></ul>")
	want := "- Revenue up\n- Churn down\n  - enterprise flat"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownOrderedList(t *testing.T) {
	got := toMarkdown("<ol><li>Draft the pricing change</li><li>Ship it</li></ol>")
	want := "1. Draft the pricing change\n2. Ship it"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownBlockquote(t *testing.T) {
	got := toMarkdown("<blockquote><p>Ship before the holidays.</p></blockquote>")
	want := "> Ship before the holidays."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownCodeBlockWithLanguage(t *testing.T) {
	got := toMarkdown(`<pre><code class="language-ruby">Card.gilded.count
</code></pre>`)
	want := "```ruby\nCard.gilded.count\n```"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownTable(t *testing.T) {
	got := toMarkdown("<table><tr><th>Plan</th><th>Price</th></tr><tr><td>Personal</td><td>$99/yr</td></tr></table>")
	want := "| Plan | Price |\n| --- | --- |\n| Personal | $99/yr |"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownTableWithoutHeaderRow(t *testing.T) {
	got := toMarkdown("<table><tr><td>Personal</td><td>$99/yr</td></tr><tr><td>Team</td><td>$299/yr</td></tr></table>")
	want := "| Personal | $99/yr |\n| --- | --- |\n| Team | $299/yr |"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownLayoutTableWithBlockContent(t *testing.T) {
	got := toMarkdown(`<table><tbody><tr><td><div>
		<h3>Spotlights</h3>
		<ul><li>Polished off the animation controller</li><li>Fixed reordering in generated projects</li></ul>
		<p>Pending code review, this is ready to ship!</p>
	</div></td></tr></tbody></table>`)
	want := "### Spotlights\n\n- Polished off the animation controller\n- Fixed reordering in generated projects\n\nPending code review, this is ready to ship!"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownLayoutTableWithSpannedCells(t *testing.T) {
	got := toMarkdown(`<table><tbody>
		<tr><td colspan="2"></td></tr>
		<tr><td>Sent with Basecamp</td><td>You can reply to this email or <a href="https://example.com/answers/42">respond in Basecamp</a>.</td></tr>
	</tbody></table>`)
	want := "Sent with Basecamp\n\nYou can reply to this email or [respond in Basecamp](https://example.com/answers/42)."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownLayoutTableWithPresentationRole(t *testing.T) {
	got := toMarkdown(`<table role="presentation"><tr><td>Weekly digest</td><td>Everything that happened this week</td></tr></table>`)
	want := "Weekly digest\n\nEverything that happened this week"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownSingleColumnTable(t *testing.T) {
	got := toMarkdown("<table><tr><td>Thanks for signing up!</td></tr><tr><td>Your trial ends on March 14.</td></tr></table>")
	want := "Thanks for signing up!\n\nYour trial ends on March 14."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownDataTableInsideLayoutTable(t *testing.T) {
	got := toMarkdown(`<table><tbody><tr><td>
		<p>Here is where the launch stands:</p>
		<table><tr><th>Task</th><th>Owner</th></tr><tr><td>Ship the beta</td><td>Priya</td></tr></table>
	</td></tr></tbody></table>`)
	want := "Here is where the launch stands:\n\n| Task | Owner |\n| --- | --- |\n| Ship the beta | Priya |"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownLayoutTableInsideLayoutTable(t *testing.T) {
	got := toMarkdown(`<table><tbody><tr><td>
		<p>The launch checklist is done.</p>
		<table><tbody><tr><td><action-text-attachment content-type="image" url="https://example.com/logo.png"></action-text-attachment></td><td>Reply to this email to comment.</td></tr></tbody></table>
	</td></tr></tbody></table>`)
	want := "The launch checklist is done.\n\n![attachment](https://example.com/logo.png)\n\nReply to this email to comment."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownHardBreak(t *testing.T) {
	got := toMarkdown("<p>Thanks,<br>Jason</p>")
	want := "Thanks,  \nJason"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownHorizontalRule(t *testing.T) {
	got := toMarkdown("<p>Above</p><hr><p>Below</p>")
	want := "Above\n\n---\n\nBelow"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownImage(t *testing.T) {
	got := toMarkdown(`<p><img src="/rails/blobs/chart.png" alt="Revenue chart"></p>`)
	want := "![Revenue chart](/rails/blobs/chart.png)"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownTrixImageAttachment(t *testing.T) {
	got := toMarkdown(`<figure data-trix-attachment='{"url":"/rails/blobs/chart.png","filename":"chart.png","contentType":"image/png"}'></figure>`)
	want := "![chart.png](/rails/blobs/chart.png)"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownTrixFileAttachment(t *testing.T) {
	got := toMarkdown(`<figure data-trix-attachment='{"url":"/rails/blobs/q3.pdf","filename":"q3-report.pdf","contentType":"application/pdf"}'></figure>`)
	want := "📎 q3-report.pdf"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownActionTextAttachment(t *testing.T) {
	got := toMarkdown(`<p>Attached:</p><action-text-attachment url="/rails/blobs/q3.pdf" filename="q3-report.pdf" content-type="application/pdf"></action-text-attachment>`)
	want := "Attached:\n\n📎 q3-report.pdf"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

// A notification email surrounds its words with avatars, icons and tracking pixels:
// image attachments and <img> tags declaring icon-sized dimensions. They are
// decoration — the text beside them already says who commented and on what — and
// each would render as a full-width URL, so they render as nothing.
func TestToMarkdownDropsDecorativeImages(t *testing.T) {
	got := toMarkdown(`<p>
		<action-text-attachment content-type="image" url="https://gopher.hey.com/signed/avatar.png" width="40" height="40" caption="Michelle Harjani" previewable="true"></action-text-attachment>
		Michelle commented on <a href="https://app.basecamp.com/2914079/buckets/41746046/card_tables/cards/10224749161">My tasks capitalization</a>
		<action-text-attachment content-type="image" url="https://gopher.hey.com/signed/card-solid.png" width="11" height="11" previewable="true"></action-text-attachment>
		<img src="https://mailer.example.com/open?id=8fd3" width="1" height="1" alt="">
	</p>`)
	want := "Michelle commented on [My tasks capitalization](https://app.basecamp.com/2914079/buckets/41746046/card_tables/cards/10224749161)"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

// A content image declares its real size or declares nothing, and either keeps it
// on the page. A dimension that does not parse keeps it too.
func TestToMarkdownKeepsContentImages(t *testing.T) {
	for _, test := range []struct{ name, in, want string }{
		{
			name: "attachment without dimensions",
			in:   `<action-text-attachment content-type="image" url="https://gopher.hey.com/signed/screenshot.png" previewable="true"></action-text-attachment>`,
			want: "![attachment](https://gopher.hey.com/signed/screenshot.png)",
		},
		{
			name: "attachment at its real size",
			in:   `<action-text-attachment content-type="image" url="https://gopher.hey.com/signed/screenshot.png" width="1280" height="720"></action-text-attachment>`,
			want: "![attachment](https://gopher.hey.com/signed/screenshot.png)",
		},
		{
			name: "img with a percentage width",
			in:   `<img src="https://example.com/chart.png" alt="Revenue chart" width="100%" height="48">`,
			want: "![Revenue chart](https://example.com/chart.png)",
		},
		{
			name: "img with only one dimension",
			in:   `<img src="https://example.com/banner.png" alt="Launch banner" height="48">`,
			want: "![Launch banner](https://example.com/banner.png)",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := toMarkdown(test.in); got != test.want {
				t.Errorf("ToMarkdown = %q, want %q", got, test.want)
			}
		})
	}
}

// A Basecamp attachment tile is two anchors at one download URL: a preview image with
// no name, then the filename. The linked image renders as one link named by the
// filename its destination ends in, and the second, identical link collapses into it —
// one line for one attachment, instead of two URLs for the preview and a third for
// the caption.
func TestToMarkdownAttachmentTileRendersOnce(t *testing.T) {
	got := toMarkdown(`<table><tbody>
		<tr><td>
			<a href="https://storage.app.basecamp.com/2914079/blobs/61ea0356/download/money-rain-cash.gif"><action-text-attachment content-type="image" url="https://gopher.hey.com/signed/preview?variant=attachment_grid" previewable="true"></action-text-attachment></a>
		</td></tr>
		<tr><td>
			<a href="https://storage.app.basecamp.com/2914079/blobs/61ea0356/download/money-rain-cash.gif">money-rain-cash.gif</a>
		</td></tr>
	</tbody></table>`)
	want := "[money-rain-cash.gif](https://storage.app.basecamp.com/2914079/blobs/61ea0356/download/money-rain-cash.gif)"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownLinkedImageLabels(t *testing.T) {
	for _, test := range []struct{ name, in, want string }{
		{
			name: "caption names the link",
			in:   `<a href="https://app.basecamp.com/2914079/buckets/22311406/uploads/998877"><action-text-attachment content-type="image" url="https://gopher.hey.com/signed/photo.png" caption="Team photo from the meetup"></action-text-attachment></a>`,
			want: "[Team photo from the meetup](https://app.basecamp.com/2914079/buckets/22311406/uploads/998877)",
		},
		{
			name: "alt names the link",
			in:   `<a href="https://example.com/news/launch"><img src="https://example.com/hero.jpg" alt="Read the launch announcement"></a>`,
			want: "[Read the launch announcement](https://example.com/news/launch)",
		},
		{
			name: "the destination's filename names the link, percent-decoded",
			in:   `<a href="https://storage.app.basecamp.com/blobs/43aa4222/download/Screen%20Recording%202026-08-24.mov"><action-text-attachment content-type="image" url="https://gopher.hey.com/signed/preview"></action-text-attachment></a>`,
			want: "[Screen Recording 2026-08-24.mov](https://storage.app.basecamp.com/blobs/43aa4222/download/Screen%20Recording%202026-08-24.mov)",
		},
		{
			name: "image when nothing names it",
			in:   `<a href="https://app.basecamp.com/2914079/buckets/22311406/uploads/998877"><action-text-attachment content-type="image" url="https://gopher.hey.com/signed/preview"></action-text-attachment></a>`,
			want: "[image](https://app.basecamp.com/2914079/buckets/22311406/uploads/998877)",
		},
		{
			name: "an anchor around only a decorative image is decoration too",
			in:   `<p>Follow the launch <a href="https://social.example.com/37signals"><img src="https://example.com/icons/mastodon.png" width="24" height="24"></a> as it happens</p>`,
			want: "Follow the launch as it happens",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := toMarkdown(test.in); got != test.want {
				t.Errorf("ToMarkdown = %q, want %q", got, test.want)
			}
		})
	}
}

// Only a repeated whole-line link collapses. Repeated prose is content — a chorus, a
// deliberate echo — and a repeated link with content between marks two places.
func TestToMarkdownKeepsRepeatedContent(t *testing.T) {
	for _, test := range []struct{ name, in, want string }{
		{
			name: "repeated prose stays",
			in:   "<p>Location, location, location.</p><p>Location, location, location.</p>",
			want: "Location, location, location.\n\nLocation, location, location.",
		},
		{
			name: "repeated links with content between stay",
			in:   `<p><a href="https://example.com/vote">Vote here</a></p><p>Polls close Friday.</p><p><a href="https://example.com/vote">Vote here</a></p>`,
			want: "[Vote here](https://example.com/vote)\n\nPolls close Friday.\n\n[Vote here](https://example.com/vote)",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := toMarkdown(test.in); got != test.want {
				t.Errorf("ToMarkdown = %q, want %q", got, test.want)
			}
		})
	}
}

// An image attachment without a filename used to be labeled "attachment"; its caption —
// the alt text of the <img> HEY rewrote — names it better when there is one.
func TestToMarkdownImageAttachmentCaptionLabel(t *testing.T) {
	got := toMarkdown(`<action-text-attachment content-type="image" url="https://gopher.hey.com/signed/kevin.png" caption="Kevin McConnell"></action-text-attachment>`)
	want := "![Kevin McConnell](https://gopher.hey.com/signed/kevin.png)"
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

// HEY wraps an inbound HTML email that Trix cannot represent in a single
// text/html attachment: the entire body lives in the attachment's content
// attribute, and skipping it leaves nothing but HEY's truncated summary.
func TestToMarkdownEmbeddedHTMLAttachment(t *testing.T) {
	got := toMarkdown(`<div><figure data-trix-attachment='{"contentType":"text/html","content":"<shadow-content><template><style>p{color:red}</style><p>Dear customer,</p><p>Please <a href=\"https://example.com/sign\">sign the document</a>.</p></template></shadow-content>","data":{}}'></figure></div>`)

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

	if got := toMarkdown(nested); strings.Contains(got, "innermost") {
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
		if got := toMarkdown(test.in); got != test.want {
			t.Errorf("%s: ToMarkdown = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestToMarkdownStripsScriptAndStyle(t *testing.T) {
	got := toMarkdown("<style>p{color:red}</style><p>Hello</p><script>alert('xss')</script>")
	if got != "Hello" {
		t.Errorf("ToMarkdown = %q, want %q", got, "Hello")
	}
}

// An entity is decoded once, by the HTML parser, and what it stood for is then
// escaped so that no Markdown renderer decodes it a second time.
func TestToMarkdownDecodesEntitiesOnce(t *testing.T) {
	got := toMarkdown("<p>Fried &amp; Hansson &lt;hey&gt; &amp;amp;</p>")
	want := `Fried &amp; Hansson \<hey\> &amp;amp;`
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
	if text := renderedText(t, got); text != "Fried & Hansson <hey> &amp;" {
		t.Errorf("rendered = %q, want the literal text back", text)
	}
}

func TestToMarkdownCollapsesSourceWhitespace(t *testing.T) {
	got := toMarkdown("<div>\n  <p>\n    The numbers   are\n    in.\n  </p>\n</div>")
	want := "The numbers are in."
	if got != want {
		t.Errorf("ToMarkdown = %q, want %q", got, want)
	}
}

func TestToMarkdownWholeEmail(t *testing.T) {
	got := toMarkdown(`<div class="trix-content"><h2>Quarterly update</h2>` +
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
