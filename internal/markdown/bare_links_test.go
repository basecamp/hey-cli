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
			"a currency symbol at its end",
			`<p>Rates: https://fx.example.com/convert?from=USD&amp;to=€ today</p>`,
			"https://fx.example.com/convert?from=USD&to=€ today",
			[]string{"https://fx.example.com/convert?from=USD&to=€"},
		},
		{
			"an emoji in its path",
			`<p>Tagged https://notes.example.com/tags/harbour_☕ today</p>`,
			"https://notes.example.com/tags/harbour_☕ today",
			[]string{"https://notes.example.com/tags/harbour_☕"},
		},
		{
			"angle brackets around it",
			`<p>Unsubscribe: &lt;https://lists.example.org/options/announce?token=8f2c1a9e&gt;</p>`,
			"<https://lists.example.org/options/announce?token=8f2c1a9e>",
			[]string{"https://lists.example.org/options/announce?token=8f2c1a9e"},
		},
		{
			"a label with no space after it",
			`<p>Link:https://docs.example.com/room_list and "https://docs.example.com/ferry_times"</p>`,
			`Link:https://docs.example.com/room_list and "https://docs.example.com/ferry_times"`,
			[]string{"https://docs.example.com/room_list", "https://docs.example.com/ferry_times"},
		},
		{
			"a sender's address in a reply header",
			`<p>On Tuesday, Jane Smith &lt;jane.smith@example.org&gt; wrote:</p>`,
			"Jane Smith <jane.smith@example.org> wrote:",
			[]string{"mailto:jane.smith@example.org"},
		},
		{
			"a mailto: address",
			`<p>Replies to mailto:offsite@example.org please.</p>`,
			"mailto:offsite@example.org please.",
			[]string{"mailto:offsite@example.org"},
		},
		{
			"a semicolon, a quote and a bracket after it",
			`<p>See https://docs.example.com/room_list; https://docs.example.com/ferry' and [https://docs.example.com/pier]</p>`,
			"https://docs.example.com/room_list; https://docs.example.com/ferry' and [https://docs.example.com/pier]",
			[]string{"https://docs.example.com/room_list", "https://docs.example.com/ferry", "https://docs.example.com/pier"},
		},
		{
			"entity-shaped text after it",
			`<p>See https://docs.example.com/room_list&amp;nbsp; today</p>`,
			"https://docs.example.com/room_list&nbsp; today",
			[]string{"https://docs.example.com/room_list"},
		},
		{
			"an uppercase scheme",
			`<p>VISIT HTTPS://DOCS.EXAMPLE.COM/ROOM_LIST TODAY</p>`,
			"HTTPS://DOCS.EXAMPLE.COM/ROOM_LIST TODAY",
			[]string{"HTTPS://DOCS.EXAMPLE.COM/ROOM_LIST"},
		},
		{
			"an address GFM would match only part of",
			`<p>Write to devi*rao@example.com or o'brien@example.com</p>`,
			"devi*rao@example.com or o'brien@example.com",
			[]string{"mailto:o'brien@example.com"},
		},
		{
			"an emoji sequence in its path",
			"<p>Album: https://photos.example.com/albums/family_\U0001F468\u200d\U0001F469\u200d\U0001F467 today</p>",
			"https://photos.example.com/albums/family_\U0001F468\u200d\U0001F469\u200d\U0001F467 today",
			[]string{"https://photos.example.com/albums/family_\U0001F468\u200d\U0001F469\u200d\U0001F467"},
		},
		{
			"full-width brackets in its path",
			`<p>資料：https://docs.example.com/資料_（最終版） です</p>`,
			"https://docs.example.com/資料_（最終版） です",
			[]string{"https://docs.example.com/資料_（最終版）"},
		},
		{
			"corner brackets around it",
			`<p>「https://docs.example.com/資料_最終版」を見て</p>`,
			"「https://docs.example.com/資料_最終版」を見て",
			[]string{"https://docs.example.com/資料_最終版"},
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

// A link to a page named in Japanese used to reach the terminal with a C1 control in
// its destination, and the containment check then stripped the whole body of its
// styling. Every link keeps its styling, opens the page, and shows its characters.
func TestRenderKeepsLinksWithNonASCIIDestinations(t *testing.T) {
	const page = "https://docs.example.com/資料_最終版"
	for _, html := range []string{
		`<p>See <a href="` + page + `">the files</a> today</p>`,
		`<p><a href="` + page + `">` + page + `</a></p>`,
		`<p>See ` + page + ` today</p>`,
	} {
		linked := RenderLinked(htmlutil.ToMarkdown(html), 200, -1)
		if !strings.Contains(linked.Text, "\x1b[") {
			t.Errorf("%s: rendered without styling: %q", html, linked.Text)
		}
		if !strings.Contains(visible(linked.Text), page) {
			t.Errorf("%s: shows %q, want %q in it", html, visible(linked.Text), page)
		}
		if len(linked.Links) == 0 || linked.Links[0].Destination != page {
			t.Errorf("%s: links = %#v, want %q", html, linked.Links, page)
		}
		if strings.ContainsFunc(linked.Text, func(r rune) bool { return r >= 0x80 && r <= 0x9f }) {
			t.Errorf("%s: output carries a C1 control: %q", html, linked.Text)
		}
	}
}

// A bare link longer than the line it starts on is found before glamour wraps it, so
// every line of it opens the whole URL — LinkifyURLs, which sees the wrapped output,
// linked the first line of a mailing list's footer and opened it without its token.
func TestRenderLinksAWrappedBareURLWhole(t *testing.T) {
	const url = "https://lists.example.org/mailman/options/announce/jane.smith%40example.com?token=8f2c1a9e4b7d6c3a"
	linked := RenderLinked(htmlutil.ToMarkdown(`<p>To unsubscribe: &lt;`+url+`&gt;</p>`), 60, -1)
	if len(linked.Links) != 1 || linked.Links[0].Destination != url {
		t.Fatalf("links = %#v, want one link to %q", linked.Links, url)
	}
	if linked.Links[0].EndLine <= linked.Links[0].StartLine {
		t.Errorf("line range = %d-%d, want the link to wrap", linked.Links[0].StartLine, linked.Links[0].EndLine)
	}
}

// Code, a link's label and an image's alt text hold no bare links, and an address GFM
// would match only part of is not linked as the different address that part is.
func TestWholeBareLinksLeavesWhatIsNotABareLink(t *testing.T) {
	for _, md := range []string{
		`Nothing to link \~ here, \_ none \*`,
		"Not an address: devi\\*rao@example.com or o'brien@example.com",
		"Not a host: https://localhost/admin",
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
		`\\https://docs.example.com/a\_b`:                              `\\<https://docs.example.com/a_b>`,
		"See https://docs.example.com/offsite/plan today":              "See <https://docs.example.com/offsite/plan> today",
		`Footer: \<https://lists.example.org/options?token=8f2c\>`:     `Footer: \<<https://lists.example.org/options?token=8f2c>\>`,
		`Jane Smith \<jane@example.org\> wrote:`:                       `Jane Smith \<<jane@example.org>\> wrote:`,
		`Write to jane@www.example.org today`:                          "Write to <jane@www.example.org> today",
		`See https://docs.example.com/list?owner=jane@example.org now`: "See <https://docs.example.com/list?owner=jane@example.org> now",
		`Write to mailto:jane@example.org today`:                       "Write to <mailto:jane@example.org> today",
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
		"<p>About ~40 people, give or take.</p>":                                                   "About ~40 people, give or take.",
		"<p>Files are in ~/Offsite/2026 on the shared drive.</p>":                                  "Files are in ~/Offsite/2026 on the shared drive.",
		"<p>~~not struck~~</p>":                                                                    "~~not struck~~",
		"<p>===</p>":                                                                               "===",
		`<p>A literal \~ and a literal \&amp; stay as written.</p>`:                                `A literal \~ and a literal \& stay as written.`,
		`<p><img alt="Pier at ~6pm" src="https://images.example.com/pier.png"></p>`:                "Pier at ~6pm",
		`<p><img alt="~~closed~~ pier, ~4 boats" src="https://images.example.com/pier.png"></p>`:   "~~closed~~ pier, ~4 boats",
		`<p><img alt="a \~ and a ~` + "`" + ` too" src="https://images.example.com/pier.png"></p>`: `a \~ and a ~` + "`" + ` too`,
	} {
		if shown := visible(Render(htmlutil.ToMarkdown(html), 200)); !strings.Contains(shown, want) {
			t.Errorf("Render(ToMarkdown(%q)) shows %q, want %q in it", html, shown, want)
		}
	}
}
