# Mail sources and paging

## A mail source reads its own page

`internal/mail` is where a box, a label, a collection, a bundle's unseen threads and a
contact's threads stop being five endpoints and become one `Source` with one `ReadPage`. It follows the shape `internal/folders` and
`internal/habit` already set: a domain package taking `client *hey.Client`, imported by
whoever needs it.

**A source knows what it is from the kind HEY served, never from its name.**
`Source.BoxKind` holds `hey.BoxKindImbox` and friends, and `Coverable()` is the one place
that asks — haystack's `Box::Imbox#coverable?` in Go. A display name is the user's to
change; a kind is not.

**Which route a page comes from is not a detail.** The Feed, the Paper Trail and Bubbled Up
override `render_box`'s ordering and per_page, so `/boxes/{id}` pages a named box in a
different order than its own route does — follow a cursor across the two and postings
repeat or vanish. `readBox` therefore dispatches on `BoxKind` to `GetImbox`, `GetFeedbox`
and the rest, and only an unfamiliar kind falls through to `Boxes().Get`.

**`Page.Cursor` is opaque and per-kind.** A label and a collection carry a geared_pagination
cursor; a box carries HEY's own `next_history_url`, which is what `hey box view --json` reports
and is the only cursor the named routes hand out. The URL is never fetched: `historyPageCursor`
takes the `page` parameter out of it and gives that to the typed operation, which is why
there is no same-origin check here to get wrong. A foreign URL is refused for carrying no
cursor rather than declined for pointing elsewhere.

**`mail.Posting` is a row, not the JSON.** `Page.Postings` stays `[]generated.Posting`
because `hey box view --json` publishes those fields verbatim; `mail.Postings` describes them as
the rows a reader acts on, and everything the TUI never reads — `bundled`, `entry_kind`,
`kind`, `updated_at` — is left in the SDK type where the CLI can still reach it. `TopicID`
is resolved once, out of the posting's URLs: HEY's `_posting.jbuilder` serves neither
`topic` nor `topic_id`, so a URL is the only place a thread is named. `app_url` names it
for a plain posting; a bundle's `app_url` names a contact, so `mail.TopicIDOf` falls back
to `app_bundle_url`, which haystack's `bundle_posting` route points at a topic exactly
when the bundle holds one unseen thread — the thread the row opens in the web app. A
bundle with several unseen threads (or none) names no topic and answers zero.
`mail.TopicIDOf` and the `mail.TopicIDIn` parse under it are exported because
`internal/cmd` needs the same answer — `resolvePostingTopicID` in `sdk.go` is a call to
them, not a second copy.

**A bundle row's mail is reached the way the TUI reaches it.** `hey bundle view` lists
the unseen threads a bundle groups (`KindBundle`, the `bundles/unseen` route) and
`hey contact threads` lists every thread with its contact (`KindContact`, the contact
show route's postings page) — a read-through bundle has no unseen threads and no single
topic, so its mail lives only on the contact's list. The likeliest misuse is handing
`hey thread read` a bundle row's own id, which the topic route 404s; `loadThread` checks
a not-found against the bundle route and, when it answers, says what the id really is
instead of letting "not found" read as "no content".

**`mail.Entry` is one message in a thread**, described by `mail.NewEntry` against the
message HEY served for it, because a topic's entry list and a message read on its own
disagree about what they carry: an entry under a bundle has no creator and no timestamp,
and the subject is the message's.

Timestamps stay `time.Time` all the way through. Formatting one for display and parsing it
back is how `hey journal list` printed the wrong day; a domain type is not the place to
start that again. HEY's JSON is always UTC — `ApiRequest#set_utc_timezone` sets
`Time.zone` for every JSON request — so whatever shows a date converts it: the TUI's
`formatDisplayDate` renders `.Local()`, because a 23:30Z thread belongs to the next day
east of UTC.

The TUI's own source of sources is `mailView.boxes []mail.Source`: HEY's boxes through
`mail.ListedBoxSource`, then the labels and the collections as `KindFolder` and
`KindCollection` sources. Everything downstream switches on `mail.Kind` —
`isOrganizedMailSource` is the one predicate that asks "not one of HEY's boxes" — and
`showsImbox` asks `Coverable()`, so the box HEY splits into sections and lets you cover is
the one HEY says it is.

On the CLI side `collectPages` in `internal/cmd/pages.go` is the other half: `mail.ReadPage`
answers one page, and `collectPages` decides how many pages this invocation wants from
`--limit`, `--all` and a page cap. An empty page ends the list whatever cursor came with it.
Search is the exception it does not cover the same way — `Search::Matches::Page` numbers its
pages instead of handing out a cursor, so `searchPageReader` returns the next page's number
as its cursor and owns the increment.

## Lists grow as the reader scrolls

Every mail list — a box, a label, a collection, a search — both of The Screener's panes and
the contact list read one page and then grow downwards. There are no page keys: the cursor
coming within `loadMoreThreshold` of the bottom reads the page below, and so does a list the
reader can already see the end of, which is why a first page too short to fill the window
keeps reading until it does.

The pages come from geared_pagination, which is not offset paging: every JSON read carries
a `Link` header whose `page` is a cursor into the ordering, and the last page carries none.
`listPaging` in `content.go` holds that cursor, and an empty page ends the list whatever
cursor came with it. On the SDK side the header is what `Boxes().GetPage`,
`Clearances().PendingPage`, `Clearances().ScreenedPage` and `Search().SearchPage` exist for —
`Get`, `Pending`, `Screened` and `Search` throw it away.

A box is the exception, and not because of the TUI: `mail.ReadPage` reads it on its own
route, where the cursor is HEY's `next_history_url` rather than the `Link` header. What
`listPaging` holds is whatever `mail.Page.Cursor` answered, opaque either way, so the four
request lanes and `refreshHead` never learn which kind of source they are following.

Search is the exception to the cursor: `Search::Matches::Page` is a shim over geared's page
rather than the real thing, and it numbers its pages, so the search results carry
`searchNextPage int` instead of a `listPaging`. There is nothing to keep a `headIDs` for
either — search results are never re-read live.

Contacts are the same kind of exception, for a different reason. `ContactsController#index`
pages an Elasticsearch relation with no `ordered_by`, so geared hands out offsets and its
`Link` header carries a page number; `contactsView` keeps a `nextPage int` and zeroes it on
the first empty page, the way `hey contact list` already does in `readContactsPage`. The `Link`
header is not read at all — `Contacts().List` throws it away, and an empty page is a
cheaper way to learn the same thing than a `ListPage` in the SDK. Contacts are never
re-read live either, so there is no `headIDs` here.

The subtle part is the live re-read. It reads the box's *top* page, because that is where a
change shows up, so it may only replace that much of a list that has grown past it:
`contentList.refreshHead` (and `screenerPane.refreshHead`) splices the fresh page in front
of the rows below it, and `listPaging.headIDs` — what the top page held last time — is how a
thread that has left the top page leaves the list with it instead of sinking below the fresh
rows. The pages further down stay as they were read until ctrl+r reads the list again from
the top. The cursor for what comes next belongs to the deepest page, so a re-read of the top
only moves it while the top page is the whole list.

A read the user asked for, a page below, and a live re-read are separate lanes
(`requests`, the shared `requestLane`; `moreRequestID` — `searchMoreID` for the results —
and `liveRequestID`, which are bare counters for exactly that reason) with a message each,
so growing a list never shows the spinner, never cancels the read the reader is waiting on,
and never carries the cursor back to the top.

All four sections wait on a `requestLane` — the journal's is one kind deep, since its whole
question is which day is selected — so "is this answer still the one the reader asked for"
has one answer in the TUI rather than one per section.
