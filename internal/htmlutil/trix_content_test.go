package htmlutil

import (
	"strings"
	"testing"
)

// heyServesForEditing is what HEY answers for rich text it stored: the stored HTML
// inside Action Text's layout, which is what note_html and content_html carry.
func heyServesForEditing(stored string) string {
	return "<div class=\"trix-content\">\n  " + stored + "\n</div>\n"
}

// A note as HEY's web editor saves it: Trix writes <div>s and <br>s, not <p>s.
const webEditedNoteHTML = "<div class=\"trix-content\">\n  <div><strong>Anniversary:</strong> June 12<br><br></div>\n<ul>\n<li>Prefers texts after six</li>\n</ul>\n</div>\n"

func TestUnwrapTrixContent(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "a note as HEY serves it",
			html: webEditedNoteHTML,
			want: "<div><strong>Anniversary:</strong> June 12<br/><br/></div>\n<ul>\n<li>Prefers texts after six</li>\n</ul>",
		},
		{
			name: "a note read back with a paragraph added after it",
			html: webEditedNoteHTML + "<p>Moved to the Lisbon office in March.</p>",
			want: "<div><strong>Anniversary:</strong> June 12<br/><br/></div>\n<ul>\n<li>Prefers texts after six</li>\n</ul>\n\n<p>Moved to the Lisbon office in March.</p>",
		},
		{
			name: "a note already nested by earlier round trips",
			html: heyServesForEditing(heyServesForEditing("<div>Prefers email</div>")),
			want: "<div>Prefers email</div>",
		},
		{
			name: "a wrapper with more classes than trix-content",
			html: `<div class="trix-content trix-content--wide"><div>Prefers email</div></div>`,
			want: "<div>Prefers email</div>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UnwrapTrixContent(tt.html); got != tt.want {
				t.Errorf("UnwrapTrixContent(%q)\n got %q\nwant %q", tt.html, got, tt.want)
			}
		})
	}
}

func TestUnwrapTrixContentLeavesOtherHTMLAsItCame(t *testing.T) {
	for _, html := range []string{
		"<p>Prefers <strong>email</strong> &amp; calls</p>",
		`<div class="note">Prefers email</div>`,
		`<blockquote><div class="trix-content">Quoted from another note</div></blockquote>`,
		"Mentions trix-content in plain text",
		"",
	} {
		if got := UnwrapTrixContent(html); got != html {
			t.Errorf("UnwrapTrixContent(%q) = %q, want it unchanged", html, got)
		}
	}
}

// Reading a note's HTML, adding to it and writing it back used to sink the note one
// wrapper deeper each time.
func TestUnwrapTrixContentKeepsRoundTripsFromNesting(t *testing.T) {
	stored := "<div>Prefers email</div>"
	for range 3 {
		stored = UnwrapTrixContent(heyServesForEditing(stored) + "<div>Call after six</div>")
	}
	if served := heyServesForEditing(stored); strings.Count(served, "trix-content") != 1 {
		t.Errorf("after three round trips HEY serves %q, want one wrapper", served)
	}
	if strings.Count(stored, "Call after six") != 3 {
		t.Errorf("stored = %q, want every addition kept", stored)
	}
}

// The Markdown a note is read as is the Markdown it is written with: converting it,
// storing it, serving it back inside HEY's wrapper and reading it again gives the same
// Markdown, and the same HTML, however many times it goes round.
func TestMarkdownSurvivesARoundTripThroughHEY(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
	}{
		{name: "bold and a list", markdown: "**Anniversary:** June 12\n\n- Prefers texts after six\n- Met at *RailsConf*"},
		{name: "italics", markdown: "Met at *RailsConf* in *Detroit*"},
		{name: "nested lists", markdown: "- Partners\n  - Acme\n  - Globex\n- Leads"},
		{name: "an ordered list", markdown: "1. Send the proposal\n2. Follow up on Friday"},
		{name: "a link", markdown: "Website: [Acme Corporation](https://example.com/acme)"},
		{name: "headings", markdown: "# Acme Corporation\n\n## Contacts\n\nJane Doe runs purchasing"},
		{name: "line breaks", markdown: "Jane Doe  \nHead of purchasing  \nPrefers email"},
		{name: "an ampersand", markdown: "Budget: $50k &amp; rising"},
		{name: "a quote", markdown: "> Call me after six"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := FromMarkdown(tt.markdown)
			readBack := ToMarkdown(heyServesForEditing(stored)).String()
			if readBack != tt.markdown {
				t.Errorf("Markdown read back\n got %q\nwant %q", readBack, tt.markdown)
			}
			if again := FromMarkdown(readBack); again != stored {
				t.Errorf("writing the Markdown back stores\n %q\nwant %q", again, stored)
			}
		})
	}
}

// A note written in HEY's web editor reads as Markdown that writes back as the same
// note, and from then on it is stable.
func TestWebEditedNoteReadsAsStableMarkdown(t *testing.T) {
	markdown := ToMarkdown(webEditedNoteHTML).String()
	if want := "**Anniversary:** June 12  \n\n- Prefers texts after six"; markdown != want {
		t.Fatalf("Markdown = %q, want %q", markdown, want)
	}
	stored := FromMarkdown(markdown)
	if want := "<p><strong>Anniversary:</strong> June 12</p>\n<ul>\n<li>Prefers texts after six</li>\n</ul>"; stored != want {
		t.Errorf("stored = %q, want %q", stored, want)
	}
	settled := ToMarkdown(heyServesForEditing(stored)).String()
	if want := "**Anniversary:** June 12\n\n- Prefers texts after six"; settled != want {
		t.Errorf("second read = %q, want %q", settled, want)
	}
	if again := FromMarkdown(settled); again != stored {
		t.Errorf("third write = %q, want %q", again, stored)
	}
}
