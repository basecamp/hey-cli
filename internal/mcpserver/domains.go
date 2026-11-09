package mcpserver

import (
	"slices"

	"github.com/basecamp/mcp/catalog"
)

// DomainSpecs curates which slice of the hey-sdk surface each domain gateway
// tool exposes, in tool display order. Tags are hey-sdk's OpenAPI tags (each
// operation carries exactly one); a spec may merge several tags into one
// tool. This mapping is the only hand-maintained part of the catalog —
// everything else derives from the SDK model via the toolkit.
//
// The first release serves five domains covering everyday mail and task
// work: boxes, search, threads, contacts, todos. Tags left unmapped are
// reported in Catalog.Unmapped and pinned by tests, so growing the surface
// is a one-line change here plus a snapshot refresh.
var DomainSpecs = []catalog.DomainSpec{
	{
		Key:  "boxes",
		Tags: []string{"Boxes"},
		Blurb: "HEY mail boxes: the Imbox, The Feed, the Paper Trail, Reply Later, Set Aside and Bubble Up, box groups and designations, and incremental posting changes. " +
			boxPaging,
	},
	{
		Key:   "search",
		Tags:  []string{"Search"},
		Blurb: "Search HEY mail: advanced search with the same refinements the search page offers.",
	},
	{
		Key:   "threads",
		Tags:  []string{"Topics", "Entries", "Messages"},
		Blurb: "HEY email threads: topics and their entries, renaming a thread, full message content, replies and forwards, drafts, and triage (trash, spam, restore, move). create_message, create_reply and update_message only ever save drafts: they never send or schedule mail, and a request to send, schedule or triage in the same call is refused. Updating a scheduled draft clears its schedule. The person can have a draft shown or revised as they like, and sends it with one press of their own: from its page in HEY (app_url), with `hey draft send <id>`, or on a confirmation this assistant asks for, where it can. Each draft save's note says which this client has.",
	},
	{
		Key:   "contacts",
		Tags:  []string{"Contacts"},
		Blurb: "HEY contacts and The Screener: contact records and notes, bundling, and clearance (screening) decisions.",
	},
	{
		Key:   "todos",
		Tags:  []string{"Calendar Todos"},
		Blurb: "HEY Calendar to-dos: create, update, complete, uncomplete, and delete. Read existing to-dos through the calendar domain's get_calendar_recordings.",
	},
	{
		Key:   "calendar",
		Tags:  []string{"Calendars"},
		Blurb: "HEY Calendars: list calendars, read their recordings (to-dos and events — the to-do read path), and toggle calendar visibility.",
	},
	{
		Key:   "identity",
		Tags:  []string{"Identity"},
		Blurb: "Your HEY identity: accounts, senders, and preferences — the acting_sender_id and acting_user_id lookups that replies and contact writes ask for.",
	},
}

// boxPaging says how HEY sizes a box's pages, in either mode.
const boxPaging = "HEY sizes each page: The Feed sends 2 threads, then 6, 9 and 12, and the other boxes 30, then 10, so a short page isn't the end of a box; one with next_page has more."

// readOnlyBlurbs replace, for a read-only server, the blurb of every domain
// that has a write action and still serves a read: the gateway drops every
// write, and a blurb describing one (create_message, designations, bundling,
// toggling a calendar) would point the model at an action this server doesn't
// list. TestReadOnlyBlurbsCoverEveryWritingDomain holds that every such domain
// is here; search writes nothing, and todos, all writes, isn't served
// read-only.
var readOnlyBlurbs = map[string]string{
	"boxes":    "HEY mail boxes: the Imbox, The Feed, the Paper Trail, Reply Later, Set Aside and Bubble Up, box groups, and incremental posting changes. " + boxPaging,
	"threads":  "HEY email threads: topics and their entries, full message content, and drafts. These actions only read: nothing here sends, saves or changes mail.",
	"contacts": "HEY contacts and The Screener: contact records and notes, the contacts mail can be addressed to, and the senders waiting in The Screener. These actions only read: nothing here screens, bundles or changes a contact.",
	"calendar": "HEY Calendars: list calendars and read their recordings (to-dos and events).",
	"identity": "Your HEY identity: accounts, senders, and preferences.",
}

// domainSpecs is DomainSpecs as a server serves it: with the read-only
// blurbs when it is read-only.
func domainSpecs(readOnly bool) []catalog.DomainSpec {
	specs := slices.Clone(DomainSpecs)
	if readOnly {
		for i, spec := range specs {
			if blurb, ok := readOnlyBlurbs[spec.Key]; ok {
				specs[i].Blurb = blurb
			}
		}
	}
	return specs
}
