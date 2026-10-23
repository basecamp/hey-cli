package mcpserver

import "github.com/basecamp/mcp/catalog"

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
		Key:   "boxes",
		Tags:  []string{"Boxes"},
		Blurb: "HEY mail boxes: the Imbox, Feed, Paper Trail, Reply Later, Set Aside and Bubble Up stacks, box groups and designations, and incremental posting changes.",
	},
	{
		Key:   "search",
		Tags:  []string{"Search"},
		Blurb: "Search HEY mail: advanced search with the same refinements the search page offers.",
	},
	{
		Key:   "threads",
		Tags:  []string{"Topics", "Entries", "Messages"},
		Blurb: "HEY email threads: topics and their entries, renaming a thread, full message content, replies and forwards, drafts, and triage (trash, spam, restore, move). create_message, create_reply and update_message only ever save drafts: they never send or schedule mail, and a request to send, schedule or triage in the same call is refused. Updating a scheduled draft clears its schedule. Delivery is hey_send's send_draft, which asks the person to confirm each email, where it's served; a person can also send a draft from HEY or with `hey draft send <id>`.",
	},
	{
		Key:   "contacts",
		Tags:  []string{"Contacts"},
		Blurb: "HEY contacts and the Screener: contact records and notes, bundling, and clearance (screening) decisions.",
	},
	{
		Key:   "todos",
		Tags:  []string{"Calendar Todos"},
		Blurb: "HEY Calendar todos: create, update, complete, uncomplete, and delete. Read existing todos through the calendar domain's get_calendar_recordings.",
	},
	{
		Key:   "calendar",
		Tags:  []string{"Calendars"},
		Blurb: "HEY Calendars: list calendars, read their recordings (todos and events — the todo read path), and toggle calendar visibility.",
	},
	{
		Key:   "identity",
		Tags:  []string{"Identity"},
		Blurb: "Your HEY identity: accounts, senders, and preferences — the acting_sender_id and acting_user_id lookups that replies and contact writes ask for.",
	},
}
