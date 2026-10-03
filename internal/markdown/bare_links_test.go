package markdown

import (
	"slices"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/htmlutil"
)

// A bare URL in an email reaches glamour as escaped prose, and glamour's linkify reads
// the escapes rather than the text: it used to show and open &amp; for every & in a
// query string, and to cut a link short at the first _, ~, *, [ or é in it. Each one
// must show as the email wrote it and open where it says.
func TestRenderLinksBareURLsWhole(t *testing.T) {
	for _, test := range []struct {
		name, html, shown string
		links             []string
	}{
		{
			"a query string",
			`<p>The plan is at https://docs.example.com/offsite?team=harbour&amp;day=2 now.</p>`,
			"https://docs.example.com/offsite?team=harbour&day=2 now.",
			[]string{"https://docs.example.com/offsite?team=harbour&day=2"},
		},
		{
			"an underscore",
			`<p>Slides: https://docs.example.com/harbour_offsite_2026 today</p>`,
			"https://docs.example.com/harbour_offsite_2026 today",
			[]string{"https://docs.example.com/harbour_offsite_2026"},
		},
		{
			"a tilde",
			`<p>Notes at https://people.example.com/~devi/offsite today</p>`,
			"https://people.example.com/~devi/offsite today",
			[]string{"https://people.example.com/~devi/offsite"},
		},
		{
			"an asterisk",
			`<p>Search https://search.example.com/find?q=harbour*ferry today</p>`,
			"https://search.example.com/find?q=harbour*ferry today",
			[]string{"https://search.example.com/find?q=harbour*ferry"},
		},
		{
			"square brackets",
			`<p>Filtered: https://tickets.example.com/list?tags[]=ferry&amp;tags[]=hotel today</p>`,
			"https://tickets.example.com/list?tags[]=ferry&tags[]=hotel today",
			[]string{"https://tickets.example.com/list?tags[]=ferry&tags[]=hotel"},
		},
		{
			"balanced parentheses",
			`<p>Background: https://en.wikipedia.org/wiki/Harbour_(disambiguation) if you want it.</p>`,
			"https://en.wikipedia.org/wiki/Harbour_(disambiguation) if",
			[]string{"https://en.wikipedia.org/wiki/Harbour_(disambiguation)"},
		},
		{
			"closing punctuation after it",
			`<p>The map (https://maps.example.com/harbour_pier) and https://maps.example.com/ferry_dock.</p>`,
			"(https://maps.example.com/harbour_pier) and https://maps.example.com/ferry_dock.",
			[]string{"https://maps.example.com/harbour_pier", "https://maps.example.com/ferry_dock"},
		},
		{
			"curly quotes around it",
			`<p>She said “https://maps.example.com/harbour_pier” twice.</p>`,
			"“https://maps.example.com/harbour_pier” twice.",
			[]string{"https://maps.example.com/harbour_pier"},
		},
		{
			"an entity spelled out in it",
			`<p>Odd one: https://legacy.example.com/view?a=1&amp;copy;=2 today</p>`,
			"https://legacy.example.com/view?a=1&copy;=2 today",
			[]string{"https://legacy.example.com/view?a=1&copy;=2"},
		},
		{
			"letters outside ASCII",
			`<p>Menu: https://cafe.example.com/menus/café_du_port today</p>`,
			"https://cafe.example.com/menus/café_du_port today",
			[]string{"https://cafe.example.com/menus/café_du_port"},
		},
		{
			"a www address",
			`<p>Tickets at www.ferries.example.com/harbour_line?day=2&amp;seats=6 today</p>`,
			"http://www.ferries.example.com/harbour_line?day=2&seats=6 today",
			[]string{"http://www.ferries.example.com/harbour_line?day=2&seats=6"},
		},
		{
			"an email address",
			`<p>Write to tessa_nolan@example.com about rooms.</p>`,
			"tessa_nolan@example.com about rooms.",
			[]string{"mailto:tessa_nolan@example.com"},
		},
		{
			"a list item",
			`<ul><li>https://docs.example.com/room_list?floor=2&amp;wing=east</li></ul>`,
			"https://docs.example.com/room_list?floor=2&wing=east",
			[]string{"https://docs.example.com/room_list?floor=2&wing=east"},
		},
		{
			"a quote",
			`<blockquote>https://docs.example.com/room_list?floor=2&amp;wing=east</blockquote>`,
			"https://docs.example.com/room_list?floor=2&wing=east",
			[]string{"https://docs.example.com/room_list?floor=2&wing=east"},
		},
		{
			"bold",
			`<p><strong>https://docs.example.com/room_list?floor=2&amp;wing=east</strong></p>`,
			"https://docs.example.com/room_list?floor=2&wing=east",
			[]string{"https://docs.example.com/room_list?floor=2&wing=east"},
		},
		{
			"a heading",
			`<h2>https://docs.example.com/room_list?floor=2&amp;wing=east</h2>`,
			"https://docs.example.com/room_list?floor=2&wing=east",
			[]string{"https://docs.example.com/room_list?floor=2&wing=east"},
		},
		{
			"a line break after it",
			`<p>https://docs.example.com/room_list?floor=2&amp;wing=east<br>See you there</p>`,
			"https://docs.example.com/room_list?floor=2&wing=east\nSee you there",
			[]string{"https://docs.example.com/room_list?floor=2&wing=east"},
		},
		{
			"a table cell",
			`<table><tr><td>https://docs.example.com/room_list?floor=2&amp;wing=east</td><td>rooms | wings</td></tr></table>`,
			"rooms | wings",
			[]string{"https://docs.example.com/room_list?floor=2&wing=east"},
		},
		{
			"inline code",
			`<p>Run against <code>https://staging.example.com/api_v2</code> first.</p>`,
			"https://staging.example.com/api_v2",
			[]string{"https://staging.example.com/api_v2"},
		},
		{
			"a link's label",
			`<p><a href="https://docs.example.com/room_list">see https://docs.example.com/room_list</a></p>`,
			"see https://docs.example.com/room_list https://docs.example.com/room_list",
			[]string{"https://docs.example.com/room_list"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			linked := RenderLinked(htmlutil.ToMarkdown(test.html), 200, -1)
			if shown := visible(linked.Text); !strings.Contains(shown, test.shown) {
				t.Errorf("shows %q, want %q in it", shown, test.shown)
			}
			links := make([]string, 0, len(linked.Links))
			for _, link := range linked.Links {
				links = append(links, link.Destination)
			}
			if !slices.Equal(links, test.links) {
				t.Errorf("links = %q, want %q", links, test.links)
			}
			if !sgrOnly(linked.Text) {
				t.Errorf("output escaped containment: %q", linked.Text)
			}
		})
	}
}

// A link glamour finds whole by itself is left for it to find, and a body with nothing
// link-shaped in it is not parsed for links at all.
func TestWholeBareLinksLeavesWhatGlamourReadsWhole(t *testing.T) {
	for _, md := range []string{
		"See https://docs.example.com/offsite/plan today",
		"Write to tessa@example.com about rooms",
		`Nothing to link \~ here, \_ none \*`,
		"[the plan](https://docs.example.com/harbour_offsite)",
		"`https://staging.example.com/api_v2`",
		"![https://images.example.com/pier\\_photo](https://images.example.com/pier_photo.png)",
	} {
		if got := wholeBareLinks(md); got != md {
			t.Errorf("wholeBareLinks(%q) = %q, want it unchanged", md, got)
		}
	}
}

func TestWholeBareLinksWritesAutolinks(t *testing.T) {
	for md, want := range map[string]string{
		`See https://docs.example.com/plan?team=harbour&amp;day=2 now`: "See <https://docs.example.com/plan?team=harbour&day=2> now",
		`See https://docs.example.com/harbour\_offsite now`:            "See <https://docs.example.com/harbour_offsite> now",
		`See www.ferries.example.com/harbour\_line now`:                "See <http://www.ferries.example.com/harbour_line> now",
		`Write to tessa\_nolan@example.com now`:                        "Write to <tessa_nolan@example.com> now",
		`\\https://docs.example.com/a\_b`:                              `\\https://docs.example.com/a\_b`,
		"| https://docs.example.com/a\\_b | x |\n| --- | --- |":        "| <https://docs.example.com/a_b> | x |\n| --- | --- |",
	} {
		if got := wholeBareLinks(md); got != want {
			t.Errorf("wholeBareLinks(%q) = %q, want %q", md, got, want)
		}
	}
}

// glamour resolves backslash escapes from its own list, which has no \~ or \=, and
// showed the backslash in front of every tilde in an email.
func TestRenderShowsEscapesGlamourDoesNotResolve(t *testing.T) {
	for html, want := range map[string]string{
		"<p>About ~40 people, give or take.</p>":                                    "About ~40 people, give or take.",
		"<p>Files are in ~/Offsite/2026 on the shared drive.</p>":                   "Files are in ~/Offsite/2026 on the shared drive.",
		"<p>~~not struck~~</p>":                                                     "~~not struck~~",
		"<p>===</p>":                                                                "===",
		`<p>A literal \~ and a literal \&amp; stay as written.</p>`:                 `A literal \~ and a literal \& stay as written.`,
		`<p><img alt="Pier at ~6pm" src="https://images.example.com/pier.png"></p>`: "Pier at ~6pm",
	} {
		if shown := visible(Render(htmlutil.ToMarkdown(html), 200)); !strings.Contains(shown, want) {
			t.Errorf("Render(ToMarkdown(%q)) shows %q, want %q in it", html, shown, want)
		}
	}
}
