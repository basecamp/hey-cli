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
			name: "a note nested by earlier round trips that each added to it",
			html: heyServesForEditing(heyServesForEditing("<div>Prefers email</div>") + "<div>Call after six</div>"),
			want: "<div>Prefers email</div>\n\n<div>Call after six</div>",
		},
		{
			name: "a non-breaking space the note starts with",
			html: heyServesForEditing("&nbsp;Indented"),
			want: " Indented",
		},
		{
			name: "whitespace around the wrapper",
			html: "\n\n  " + heyServesForEditing("<div>Prefers email</div>") + "\n\n",
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
		// Only HEY's layout is taken off: <div class="trix-content"> and nothing else, at
		// the start of the note. A div of the author's own is left alone wherever it is.
		`<p>Intro</p><div class="trix-content" data-id="section"><p>Details</p></div>`,
		`<p>Intro</p><div class="trix-content"><p>Details</p></div>`,
		`<div class="trix-content" data-id="section"><p>Details</p></div>`,
		`<div class="trix-content trix-content--wide"><p>Details</p></div>`,
		`<div class="trix-content" id="details"><p>Details</p></div><p>Outro</p>`,
		// A non-breaking space shows, so a wrapper after one is not first in the note.
		"&nbsp;<div class=\"trix-content\"><p>Details</p></div>",
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

// A note written in HEY's web editor — <div>s, with two <br>s for a paragraph break —
// reads as Markdown that is already stable: the first write normalizes the HTML to
// <p>s, and from then on every read gives the same Markdown and every write the same
// HTML.
func TestWebEditedNoteReadsAsStableMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		markdown string
		stored   string
	}{
		{
			name:     "a label, a paragraph break and a list",
			html:     webEditedNoteHTML,
			markdown: "**Anniversary:** June 12\n\n- Prefers texts after six",
			stored:   "<p><strong>Anniversary:</strong> June 12</p>\n<ul>\n<li>Prefers texts after six</li>\n</ul>",
		},
		{
			name:     "a paragraph break inside one div",
			html:     heyServesForEditing("<div>Met at RailsConf in Detroit.<br><br>Prefers texts after six.<br>Never on Sundays.</div>"),
			markdown: "Met at RailsConf in Detroit.\n\nPrefers texts after six.  \nNever on Sundays.",
			stored:   "<p>Met at RailsConf in Detroit.</p>\n<p>Prefers texts after six.<br>Never on Sundays.</p>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markdown := ToMarkdown(tt.html).String()
			if markdown != tt.markdown {
				t.Fatalf("Markdown = %q, want %q", markdown, tt.markdown)
			}
			stored := FromMarkdown(markdown)
			if stored != tt.stored {
				t.Errorf("stored = %q, want %q", stored, tt.stored)
			}
			again := ToMarkdown(heyServesForEditing(stored)).String()
			if again != markdown {
				t.Errorf("second read = %q, want the first read %q", again, markdown)
			}
			if rewritten := FromMarkdown(again); rewritten != stored {
				t.Errorf("second write = %q, want the first write %q", rewritten, stored)
			}
		})
	}
}
