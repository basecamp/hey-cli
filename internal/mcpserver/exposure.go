package mcpserver

// Exposure on the stdio gateway (sending-safety D6).
//
// A grant is exposed for ten minutes after a read returns anything an unscreened sender
// wrote, and while it's exposed send_draft refuses drafts to anyone not known (D4). The
// gateway serves HEY's responses raw, and most of them don't say whether a sender is
// screened in (GetTopic's screened_in, haystack#9083, isn't in the hey-sdk model this
// package embeds), so each served read is classified by what it can return:
//
//   - false: it can't carry an unscreened sender's words. A box read lists postings,
//     and HEY posts a thread to a box only once the Screener let it through
//     (hey-mcp-server's provenance.go reads boxes the same way). Calendars, identity
//     and box groups are the account's own.
//   - true: it can, or it can't be ruled out. Search, threads, messages, drafts (a reply
//     draft quotes what it answers), the Screener, contacts (a contact may be a sender
//     waiting in the Screener) and calendar recordings (an invite's title is its
//     sender's) all expose.
//
// The marker is written before the content is returned, and a read whose marker can't be
// written is refused, so unscreened content never reaches the model unrecorded.
// TestEveryServedReadIsClassifiedForExposure holds this table total: serving another
// read fails until it has an entry here.
var readExposes = map[string]bool{
	// boxes
	"GetAsidebox":          false,
	"GetBox":               false,
	"GetBoxGroup":          false,
	"GetBoxPostingChanges": false,
	"GetBubblebox":         false,
	"GetFeedbox":           false,
	"GetImbox":             false,
	"GetImboxSeen":         false,
	"GetLaterbox":          false,
	"GetTrailbox":          false,
	"ListBoxGroups":        false,
	"ListBoxes":            false,

	// search
	"AdvancedSearch":           true,
	"GetAdvancedSearchFilters": true,

	// threads
	"GetEverythingTopics":     true,
	"GetMessage":              true,
	"GetMessageEdit":          true,
	"GetSentTopics":           true,
	"GetSpamTopics":           true,
	"GetTopic":                true,
	"GetTopicCommentAudience": true,
	"GetTopicEntries":         true,
	"GetTrashTopics":          true,
	"ListDrafts":              true,
	"NewEntryForward":         true,
	"NewEntryReply":           true,

	// contacts and the Screener
	"GetClearances":           true,
	"GetContact":              true,
	"GetContactNote":          true,
	"GetMyClearances":         true,
	"ListAddressableContacts": true,
	"ListContacts":            true,

	// calendar
	"GetCalendarRecordings": true,
	"ListCalendars":         false,

	// identity
	"GetIdentity":   false,
	"GetNavigation": false,
}
