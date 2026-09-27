package htmlutil

import (
	"strings"
	"testing"
)

func nestedLists(depth int) string {
	return strings.Repeat("<ul><li>Partners", depth) + strings.Repeat("</li></ul>", depth)
}

func nestedQuotes(depth int) string {
	return strings.Repeat("<blockquote>", depth) + "Call after six" + strings.Repeat("</blockquote>", depth)
}

func TestMarkdownIsLossless(t *testing.T) {
	tests := []struct {
		name string
		html string
		want bool
	}{
		{name: "empty", html: "", want: true},
		{name: "a note from HEY's editor", html: webEditedNoteHTML, want: true},
		{name: "every kind of formatting the editor writes", html: heyServesForEditing(
			`<h1>Acme Corporation</h1><div><strong>Buyer:</strong> Jane Doe, <em>head of purchasing</em>, <del>Paris</del> Lisbon<br><br></div>` +
				`<blockquote>Call me after six</blockquote><ul><li>Partners<ul><li>Globex</li></ul></li></ul><ol><li>Send the proposal</li></ol>` +
				`<pre>quote #4821</pre><div><a href="https://example.com/acme">Acme's site</a> and <a href="mailto:jane@example.com">email</a></div><hr>`), want: true},
		{name: "a Trix attachment", html: heyServesForEditing(`<div>Signed contract:</div><figure data-trix-attachment='{"contentType":"application/pdf","filename":"contract.pdf","url":"/rails/blobs/contract.pdf"}'><figcaption>contract.pdf</figcaption></figure>`), want: false},
		{name: "an Action Text attachment", html: `<div>Logo <action-text-attachment content-type="image/png" url="/rails/blobs/logo.png"></action-text-attachment></div>`, want: false},
		{name: "an image", html: `<div><img src="/rails/blobs/chart.png" alt="Revenue chart"></div>`, want: false},
		{name: "a table", html: `<table><tr><td>Personal</td><td>$99/yr</td></tr></table>`, want: false},
		{name: "underline", html: `<div><u>Always</u> copy Jane</div>`, want: false},
		{name: "a span with no attributes", html: `<div>Call <span>Jane</span> tomorrow</div>`, want: true},
		{name: "a span inside bold", html: `<div><strong>Call <span>Jane</span></strong> tomorrow</div>`, want: true},
		{name: "a coloured span", html: `<div>Call <span style="color:red">Jane</span> tomorrow</div>`, want: false},
		{name: "a span with a class", html: `<div>Call <span class="mention">Jane</span> tomorrow</div>`, want: false},
		{name: "a link Markdown will not write", html: `<div><a href="javascript:alert(1)">Open</a></div>`, want: false},
		{name: "an anchor without a destination", html: `<div><a name="top">Top</a></div>`, want: false},
		{name: "a link with more than a destination", html: `<div><a href="https://example.com/acme" title="Acme">Acme</a></div>`, want: false},
		{name: "a list starting at four", html: `<ol start="4"><li>Send the proposal</li></ol>`, want: false},
		{name: "a coloured div", html: `<div style="color:red">Overdue invoice</div>`, want: false},
		{name: "a div with a class of its own", html: `<div class="callout">Overdue invoice</div>`, want: false},
		{name: "a code block in a language HEY highlights", html: `<pre language="ruby">Contact.find(7)</pre>`, want: true},
		{name: "a code block in a language HEY does not", html: `<pre language="haskell">main = pure ()</pre>`, want: false},
		{name: "formatting inside a code block", html: `<pre>quote <strong>#4821</strong></pre>`, want: false},
		{name: "line breaks inside a code block", html: `<pre>line one<br>line two</pre>`, want: true},
		{name: "blank lines inside a code block", html: `<pre>line one<br><br><br>line four</pre>`, want: false},
		{name: "a paragraph break", html: `<div>Met at RailsConf.<br><br>Prefers texts.</div>`, want: true},
		{name: "two blank lines", html: `<div>Met at RailsConf.<br><br><br>Prefers texts.</div>`, want: false},
		{name: "a line break in a list item", html: `<ul><li>Jane Doe<br>Head of purchasing</li></ul>`, want: true},
		{name: "a paragraph break in a list item", html: `<ul><li>Jane Doe<br><br>Head of purchasing</li></ul>`, want: false},
		{name: "inline code", html: `<div>Quote <code>#4821</code> on every invoice</div>`, want: true},
		{name: "formatting inside inline code", html: `<div>Quote <code><strong>#4821</strong></code> on every invoice</div>`, want: false},
		{name: "lists nested as deep as ToMarkdown renders", html: nestedLists(maxNestingDepth), want: true},
		{name: "lists nested deeper than ToMarkdown renders", html: nestedLists(maxNestingDepth + 1), want: false},
		{name: "quotes nested as deep as ToMarkdown renders", html: nestedQuotes(maxNestingDepth), want: true},
		{name: "quotes nested deeper than ToMarkdown renders", html: nestedQuotes(maxNestingDepth + 1), want: false},
		{name: "HEY's wrapper nested by earlier round trips", html: heyServesForEditing(heyServesForEditing("<div>Prefers email</div>") + "<div>Call after six</div>"), want: true},
		{name: "a trix-content div of the author's own", html: `<div>Intro</div><div class="trix-content"><div>Details</div></div>`, want: false},
		{name: "a break starting a block", html: `<div><br>Call after six</div>`, want: false},
		{name: "a break starting a block inside bold", html: `<div><strong><br>Call after six</strong></div>`, want: false},
		{name: "a break after bold text", html: `<div><strong>Call</strong><br>after six</div>`, want: true},
		{name: "a break ending a block", html: `<div>Call after six<br></div><div>Prefers texts</div>`, want: true},
		{name: "a break in a heading", html: `<h1>Acme<br>Corporation</h1>`, want: false},
		{name: "a soft hyphen", html: `<p>Q3&#173; planning</p>`, want: false},
		{name: "a zero width space", html: `<p>Jane&#8203;Doe</p>`, want: false},
		{name: "a control character", html: "<p>Invoice\x1b[31m overdue</p>", want: false},
		{name: "an emoji family", html: "<p>Family \U0001F468\u200D\U0001F469\u200D\U0001F467 dinner on Sunday</p>", want: true},
		{name: "a non-breaking space between words", html: `<p>June&nbsp;12</p>`, want: true},
		{name: "spaces held apart by non-breaking spaces", html: `<p>Total:&nbsp;&nbsp; $4,200</p>`, want: false},
		{name: "a control character in code", html: "<pre>quote\x1b #4821</pre>", want: false},
		{name: "a tab in a code block", html: "<pre>quote\t#4821</pre>", want: true},
		{name: "a list item outside a list", html: `<li>Prefers email</li>`, want: false},
		{name: "a blank line ending a code block", html: `<pre>quote #4821<br><br></pre>`, want: false},
		{name: "bold inside bold", html: `<div><strong>A<strong>B</strong>C</strong></div>`, want: false},
		{name: "a link with no label", html: `<div>Site: <a href="https://example.com/acme"></a></div>`, want: false},
		{name: "a link labelled with a URL Markdown rewrites", html: `<div><a href="https://example.com/acme deals">https://example.com/acme deals</a></div>`, want: false},
		{name: "a link labelled with its own URL", html: `<div><a href="https://example.com/acme">https://example.com/acme</a></div>`, want: true},
		{name: "text after a list", html: `<ul><li>Partners</li></ul>Leads`, want: true},
		{name: "a list straight inside a list", html: `<ul><ul><li>Indented</li></ul></ul>`, want: false},
		{name: "a block holding only a non-breaking space", html: `<div>Met at RailsConf.</div><div>&nbsp;</div><div>Prefers texts.</div>`, want: false},
		{name: "a block holding only a break", html: `<div>Met at RailsConf.</div><div><br></div><div>Prefers texts.</div>`, want: false},
		{name: "spaces held apart across an element", html: `<p>Total:&nbsp;<strong>&nbsp;</strong>$4,200</p>`, want: false},
		{name: "a non-breaking space before bold", html: `<p>Total:&nbsp;<strong>$4,200</strong></p>`, want: true},
		{name: "a list inside a list item", html: `<ul><li>Partners<ol><li>Acme</li><li>Globex</li></ol></li></ul>`, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MarkdownIsLossless(tt.html); got != tt.want {
				t.Errorf("MarkdownIsLossless(%q) = %v, want %v", tt.html, got, tt.want)
			}
			if !tt.want {
				return
			}
			// What is called lossless reads back as the same Markdown once written.
			markdown := ToMarkdown(tt.html).String()
			if again := ToMarkdown(heyServesForEditing(FromMarkdown(markdown))).String(); again != markdown {
				t.Errorf("Markdown written back reads as\n %q\nwant %q", again, markdown)
			}
		})
	}
}
