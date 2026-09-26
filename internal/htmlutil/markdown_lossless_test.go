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
