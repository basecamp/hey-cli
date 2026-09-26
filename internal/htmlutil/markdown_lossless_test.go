package htmlutil

import "testing"

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
		{name: "a link Markdown will not write", html: `<div><a href="javascript:alert(1)">Open</a></div>`, want: false},
		{name: "an anchor without a destination", html: `<div><a name="top">Top</a></div>`, want: false},
		{name: "a link with more than a destination", html: `<div><a href="https://example.com/acme" title="Acme">Acme</a></div>`, want: false},
		{name: "a list starting at four", html: `<ol start="4"><li>Send the proposal</li></ol>`, want: false},
		{name: "a coloured div", html: `<div style="color:red">Overdue invoice</div>`, want: false},
		{name: "a div with a class of its own", html: `<div class="callout">Overdue invoice</div>`, want: false},
		{name: "a code block in a language HEY highlights", html: `<pre language="ruby">Contact.find(7)</pre>`, want: true},
		{name: "a code block in a language HEY does not", html: `<pre language="haskell">main = pure ()</pre>`, want: false},
		{name: "formatting inside a code block", html: `<pre>quote <strong>#4821</strong></pre>`, want: false},
		{name: "line breaks inside a code block", html: `<pre>line one<br><br><br>line four</pre>`, want: true},
		{name: "a paragraph break", html: `<div>Met at RailsConf.<br><br>Prefers texts.</div>`, want: true},
		{name: "two blank lines", html: `<div>Met at RailsConf.<br><br><br>Prefers texts.</div>`, want: false},
		{name: "a line break in a list item", html: `<ul><li>Jane Doe<br>Head of purchasing</li></ul>`, want: true},
		{name: "a paragraph break in a list item", html: `<ul><li>Jane Doe<br><br>Head of purchasing</li></ul>`, want: false},
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
