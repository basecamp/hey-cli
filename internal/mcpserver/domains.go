package mcpserver

import "github.com/basecamp/mcp/catalog"

// DomainSpecs curates which slice of the hey-sdk surface each domain gateway
// tool exposes, in tool display order. Tags are hey-sdk's OpenAPI tags (each
// operation carries exactly one); a spec may merge several tags into one
// tool. This mapping is the only hand-maintained part of the catalog —
// everything else derives from the SDK model via the toolkit.
//
// The domains cover everyday mail, task and calendar work. Tags left unmapped are
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
		Blurb: "HEY email threads: topics and their entries, full message content, replies and forwards, drafts, and triage (trash, spam, restore, move).",
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
		Tags:  []string{"Calendars", "Calendar Periods"},
		Blurb: "HEY Calendars: list calendars, read their recordings (todos and events — the todo read path), toggle calendar visibility, and read a day, week or year as HEY draws it (repeating events expanded into occurrences, plus habits).",
	},
	{
		Key:   "timetracks",
		Tags:  []string{"Calendar Time Tracks"},
		Blurb: "HEY Calendar time tracks: start tracking, read the ongoing track, stop it (update_time_track with ends_at, optionally filing it under a category_title), record a finished stretch, and list, edit or delete completed tracks and categories.",
	},
	{
		Key:   "habits",
		Tags:  []string{"Calendar Habits"},
		Blurb: "HEY Calendar habits: create, update, stop, resume and delete habits, and mark or unmark a day complete. List habits through the calendar domain's week read.",
	},
	{
		Key:   "journal",
		Tags:  []string{"Calendar Journal"},
		Blurb: "HEY Calendar journal: list and search entries, read one day's entry, and write it. A write replaces the whole entry, so read it first; empty content deletes it.",
	},
	{
		Key:   "identity",
		Tags:  []string{"Identity"},
		Blurb: "Your HEY identity: accounts, senders, and preferences — the acting_sender_id and acting_user_id lookups that replies and contact writes ask for.",
	},
}
