# API Coverage

Mapping of HEY API endpoints used by the CLI. API interactions use the HEY SDK (`hey-sdk/go`).
Every endpoint below is read as JSON through a typed SDK operation; nothing parses HTML.

**`/topics/{id}/entries.json` cannot be paged by number.** `Topics::EntriesController` uses
`set_page_and_extract_portion_from`, so like every other list here its `page` is
geared_pagination's opaque cursor out of the `Link` header, not an offset. An integer there
is not an error — it is ignored, and the first page comes back again. `Topics().GetEntriesPage`
is the SDK read that keeps that header; `hey thread read` and `hey attachment list` walk it
through `internal/threadload`. The drafts index (`/entries/drafts.json`) pages the same way,
which is what `Entries().ListDraftsPage` and `hey draft list --page` exist for.

| Endpoint | Method | Client | CLI Command | Status |
|----------|--------|--------|-------------|--------|
| `/boxes.json` | GET | SDK `Boxes().List` | `hey box list` | covered |
| `/boxes/{id}.json` | GET | SDK `Boxes().Get`, `Boxes().GetPage` | `hey box view <id>`, TUI mail list growth | covered |
| `/imbox.json` | GET | SDK `Boxes().GetImbox` | `hey box view imbox` | covered |
| `/imbox/seen.json` | GET | SDK `Boxes().GetImboxSeen` | TUI Previously Seen screen | covered |
| `/feedbox.json` | GET | SDK `Boxes().GetFeedbox` | `hey box view feedbox` | covered |
| `/trailbox.json` | GET | SDK `Boxes().GetTrailbox` | `hey box view trailbox` | covered |
| `/asidebox.json` | GET | SDK `Boxes().GetAsidebox` | `hey box view asidebox` | covered |
| `/laterbox.json` | GET | SDK `Boxes().GetLaterbox` | `hey box view laterbox` | covered |
| `/bubblebox.json` | GET | SDK `Boxes().GetBubblebox` | `hey box view bubblebox`, `hey bubble list` (scheduled bucket) | covered |
| `/imbox/bubbled_up` | GET | — | — | not served: HTML only; the Imbox JSON orders bubbled-up threads first, so `hey bubble list` reads that prefix instead |
| `/my/navigation.json` | GET | SDK `Identity().GetNavigation` | `hey label list`, Mail TUI navigation | covered |
| `/folders/{id}.json` | GET | SDK `Folders().GetPage` | `hey label view <id>`, Mail TUI labels | covered |
| `/postings/filings.json` | POST | SDK `Postings().File` | `hey label add`, TUI `b/B` | covered |
| `/postings/filings.json` | DELETE | SDK `Postings().Unfile` | `hey label remove`, TUI `b/B` | covered |
| `/postings/folders.json` | POST | SDK `Postings().CreateFolder` | `hey label create`, TUI `b/B` | covered |
| `/collections.json` | GET | SDK `Collections().List` | `hey collection list`, Mail TUI navigation | covered |
| `/collections/{id}.json` | GET | SDK `Collections().GetPage` | `hey collection view <id>`, Mail TUI collections | covered |
| `/collections` | POST | SDK `Collections().Create` | `hey collection create` | covered |
| `/collections/{id}.json` | PATCH | SDK `Collections().Update` | `hey collection update` | covered |
| `/topics/{id}/collecting` | POST | SDK `Collections().AddTopic` | `hey collection add`, TUI `n/N` | covered |
| `/topics/{id}/collecting` | DELETE | SDK `Collections().RemoveTopic` | `hey collection remove`, TUI `n/N` | covered |
| `/advanced_search.json` | GET | SDK `Search().Search`, `Search().SearchPage` | `hey search`, TUI `/` or `s/S` | covered |
| `/advanced_search_filters.json` | GET | SDK `Search().Filters` | `hey search filters` | covered |
| `/clearances.json` | GET | SDK `Clearances().Summary`, `Pending`, `PendingPage`, `PendingCount` | `hey screener list`, TUI ctrl+s | covered |
| `/clearances/{id}` | PATCH | SDK `Clearances().Screen` | `hey screener approve`/`deny` (one ID), TUI `y`/`n` | covered |
| `/clearances/bulk.json` | PATCH | SDK `Clearances().ScreenMany` | `hey screener approve`/`deny` (several IDs) | covered |
| `/clearances/punt.json` | POST | SDK `Clearances().Punt` | `hey screener clear`, TUI `X` | covered |
| `/my/clearances.json` | GET | SDK `Clearances().Screened`, `ScreenedPage` | `hey screener history`, TUI Screener History | covered |
| `/my/clearances/{id}` | PATCH | SDK `Clearances().Rescreen` | — | unused by the CLI |
| `/contacts.json` | GET | SDK `Contacts().List` | `hey contact list`, Contacts TUI | covered |
| `/contacts/{id}.json` | GET | SDK `Contacts().Get` | `hey contact show`, Contacts TUI | covered |
| `/contacts.json` | POST | SDK `Contacts().Create` | `hey contact add`, Contacts TUI | covered |
| `/contacts/{id}.json` | PATCH | SDK `Contacts().Update` | `hey contact update`, Contacts TUI | covered |
| `/contacts/{id}.json` | DELETE | SDK `Contacts().Hide` | `hey contact hide`, Contacts TUI | covered |
| `/contacts/{id}/reveal.json` | POST | SDK `Contacts().Reveal` | `hey contact show-again`, Contacts TUI | covered |
| `/contacts/{id}/bundle.json` | POST | SDK `Contacts().Bundle` | `hey contact bundle` | covered |
| `/contacts/{id}/bundle.json` | DELETE | SDK `Contacts().Unbundle` | `hey contact unbundle` | covered |
| `/contacts/{id}/note.json` | GET | SDK `Contacts().Note` | `hey contact note show`, Contacts TUI | covered |
| `/contacts/{id}/note.json` | PATCH | SDK `Contacts().SetNote` | `hey contact note set`, Contacts TUI | covered |
| `/contacts/{id}/note.json` | DELETE | SDK `Contacts().DeleteNote` | `hey contact note delete`, Contacts TUI | covered |
| `/calendars.json` | GET | SDK `Calendars().List` | `hey calendar list` | covered |
| `/calendars/{id}/recordings.json` | GET | SDK `Calendars().GetRecordings` | `hey event list`, `hey event edit <id>` (reading the event back), `hey todo list`, `hey journal list` | covered |
| `/topics/{id}/entries.json` | GET | SDK `Topics().GetEntries` | `hey thread read <id>`, `hey attachment list <topic-id>` | covered, but see the paging note below |
| `/topics/{id}/publication` | POST | SDK `Publications().Create` | `hey share <thread-id>` | covered |
| `/topics/{id}/publication.json` | GET | SDK `Publications().Create` readback | `hey share <thread-id>` | covered |
| `/topics/{id}/publication` | DELETE | SDK `Publications().Delete` | `hey unshare <thread-id>` | covered |
| `/messages/{id}.json` | GET | SDK `Messages().Get` | `hey thread read <id>` (bodies), `hey reply <topic-id>` and TUI `r` (recipient fallback), `hey attachment list <topic-id>`, `hey attachment save <id>` | covered |
| `/messages/{id}` | PUT | SDK `Messages().UpdateDraft`, `Messages().SendDraft` | `hey draft edit <id>`, `hey draft send <id>` | covered |
| `/messages/{id}/edit.json` | GET | SDK `Messages().GetEdit` | `hey draft show <id>`, `hey draft edit/send` (reading the state an update resends) | covered |
| `/entries/{id}/replies/new.json` | GET | SDK `Entries().NewReply` | `hey reply <topic-id>` and `hey compose --thread-id` (recipients, with the acting user excluded) | covered |
| `/entries/drafts.json` | GET | SDK `Entries().ListDraftsPage` | `hey draft list` (`--all`/`--page` follow its cursor) | covered |
| `/entries/drafts/{id}` | DELETE | SDK `Entries().DeleteDraft` | `hey draft delete <id>...` | covered |
| `/rails/active_storage/direct_uploads.json` | POST | SDK `Attachments().Upload` | `hey compose --attach`, `hey reply --attach`, `hey bulk-reply send --attach` | covered |
| signed Active Storage upload URL | PUT | SDK `Attachments().Upload` | `hey compose --attach`, `hey reply --attach`, `hey bulk-reply send --attach` | covered |
| signed Active Storage blob URL | GET | SDK `DownloadBlob` | `hey attachment save <id>` | covered |
| `/messages.json` | POST | SDK `Messages().Create`, `Messages().CreateDraft` | `hey compose`, `hey compose --draft`, `hey forward <topic-id>` | covered |
| `/entries/{id}/replies` | POST | SDK `Entries().CreateReply`, `Entries().CreateReplyDraft` | `hey reply <topic-id>`, `hey reply --draft`, `hey compose --thread-id [--draft]` | covered |
| `/topics/{id}.json` | GET | SDK `Topics().Get` | `hey forward <topic-id>`, `hey reply <topic-id>`, TUI `r` | covered |
| `/entries/{id}/forwards/new.json` | GET | SDK `Entries().NewForward` | `hey forward <topic-id>` | covered |
| `/bulk_replies/new.json` | GET | SDK `BulkReplies().Draft` | `hey bulk-reply preview`, `hey bulk-reply send`, TUI `ctrl+b` | covered |
| `/bulk_replies.json` | POST | SDK `BulkReplies().Send` | `hey bulk-reply send`, TUI bulk reply | covered |
| `/bulk_replies/{id}/undo_send` | POST | SDK `BulkReplies().Undo` | `hey bulk-reply undo`, TUI `ctrl+u` | covered |
| `/bulk_replies/{id}` | GET (redirect target) | SDK `BulkReplies().Undo` redirect handling | `hey bulk-reply undo`, TUI `ctrl+u` | covered |
| `/postings/seen.json` | POST | SDK `Postings().MarkSeen` | `hey seen <id>`, TUI `e/E` and opening a thread | covered |
| `/postings/unseen.json` | POST | SDK `Postings().MarkUnseen` | `hey unseen <id>`, TUI `u/U` | covered |
| `/postings/moves.json` | POST | SDK `Postings().Move`, `MoveToFeed`, `MoveToSetAside`, `MoveToReplyLater`, `MoveToPaperTrail` | `hey move <id> --to <box>`, TUI `v/V` | covered |
| `/postings/trash.json` | POST | SDK `Postings().MoveToTrash` | `hey trash <id>`, TUI `t/T` | covered |
| `/postings/spam.json` | POST | SDK `Postings().MarkSpam` | `hey spam <id>`, TUI `!` | covered |
| `/postings/mutings.json` | POST | SDK `Postings().Mute` | `hey ignore <id>`, TUI `-` | covered |
| `/postings/mutings.json` | DELETE | SDK `Postings().Unmute` | `hey stop-ignoring <id>`, TUI `+` | covered |
| `/postings/bulk_bubble_up_now.json` | POST | SDK `Postings().BubbleUpNow` | `hey bubble up <id> --now` | covered |
| `/postings/bubble_up.json` | POST | SDK `Postings().ScheduleBubbleUp` | `hey bubble up <id> --on <date>` | covered |
| `/postings/bubble_up.json` | DELETE | SDK `Postings().CancelBubbleUp` | `hey bubble pop <id>` | covered |
| `/calendar/events.json` | POST | SDK `CalendarEvents().Create` | `hey event add`, Calendar TUI `a` | covered |
| `/calendar/events/{id}.json` | PATCH | SDK `CalendarEvents().Update` | `hey event edit <id>`, Calendar TUI `e` | covered: a write replaces rather than patches, so the caller reads the event and sends back what it keeps |
| `/calendar/events/{id}` | DELETE | SDK `CalendarEvents().Delete` | `hey event delete <id>`, Calendar TUI `x` | covered |
| `/calendar/events/{id}/occurrences/{date}.json` | PATCH | SDK `CalendarEvents().UpdateOccurrence` | Calendar TUI `e` on one day of a repeating event | covered; no CLI equivalent yet |
| `/calendar/events/{id}/occurrences/{date}.json` | DELETE | SDK `CalendarEvents().DeleteOccurrence` | Calendar TUI `x` on one day of a repeating event | covered; no CLI equivalent yet |
| `/calendar/events/{id}.json` | GET | — | — | not served: HEY has no JSON show for an event, which is why `hey event edit` finds one through the recordings listing |
| `/calendar/habits.json` | POST | SDK `Habits().Create` | `hey habit create`, Calendar TUI `a` | covered |
| `/calendar/habits/{id}.json` | PATCH | SDK `Habits().Update` | `hey habit edit <id>`, Calendar TUI `e` | covered |
| `/calendar/habits/{id}.json` | DELETE | SDK `Habits().Delete` | `hey habit delete <id>`, Calendar TUI `x` | covered |
| `/calendar/days/{date}.json` | GET | SDK `CalendarPeriods().Day` | Calendar TUI day view | covered |
| `/calendar/weeks/{date}.json` | GET | SDK `CalendarPeriods().Week` | `hey habit list`, Calendar TUI week view | covered: a period expands a recurrence into its occurrences and is the only read that carries habits, which a calendar's recordings listing does not |
| `/calendar/years/{date}.json` | GET | SDK `CalendarPeriods().Year` | Calendar TUI year view | covered |
| `/calendar/days/{date}/habits/{id}/completions.json` | POST | SDK `Habits().Complete` | `hey habit complete <id>` | covered |
| `/calendar/days/{date}/habits/{id}/completions.json` | DELETE | SDK `Habits().Uncomplete` | `hey habit uncomplete <id>` | covered |
| `/calendar/days/{date}/journal_entry.json` | GET | SDK `Journal().Get` | `hey journal read [date]` | covered: a 204 means the day is empty |
| `/calendar/days/{date}/journal_entry.json` | PATCH | SDK `Journal().Update` | `hey journal write [date]` | covered |
| `/calendar/ongoing_time_track.json` | GET | SDK `TimeTracks().GetOngoing` | `hey timetrack current` | covered |
| `/calendar/ongoing_time_track.json` | POST | SDK `TimeTracks().Start` | `hey timetrack start` | covered |
| `/calendar/time_tracks/{id}.json` | PUT | SDK `TimeTracks().Stop` | `hey timetrack stop` | covered |
| `/calendar/time_tracks/exports` | GET | SDK `TimeTracks().Export` | `hey timetrack export` | covered |
| `/calendar/time_tracks/categories.json` | GET | SDK `TimeTracks().Categories` | `hey timetrack categories`, Calendar TUI `c` | covered |
| `/calendar/time_tracks/categories` | POST | SDK `TimeTracks().CreateCategory` | `hey timetrack category create`, Calendar TUI `c` | covered |
| `/calendar/time_tracks/categories/{id}` | PATCH | SDK `TimeTracks().UpdateCategory` | `hey timetrack category rename`, Calendar TUI `c` | covered |
| `/calendar/time_tracks/categories/{id}` | DELETE | SDK `TimeTracks().DeleteCategory` | `hey timetrack category delete`, Calendar TUI `c` | covered |
| `/calendar/todos.json` | POST | SDK `CalendarTodos().Create` | `hey todo add` | covered |
| `/calendar/todos/{id}/completions.json` | POST | SDK `CalendarTodos().Complete` | `hey todo complete <id>` | covered |
| `/calendar/todos/{id}/completions.json` | DELETE | SDK `CalendarTodos().Uncomplete` | `hey todo uncomplete <id>` | covered |
| `/calendar/todos/{id}.json` | DELETE | SDK `CalendarTodos().Delete` | `hey todo delete <id>` | covered |
| `/boxes/{id}/postings/changes.json` | GET | SDK `Postings().AllChanges` | `hey watch` | covered |
| `/cable` (`Postings::ChangesChannel`) | WS | `internal/cable` + actioncable-go | `hey watch`, TUI mail list | covered |
| `/cable` (`Turbo::StreamsChannel`) | WS | `internal/cable` + actioncable-go | TUI Screener (stream name from `/clearances.json`) | covered |
| `/identity.json` | GET | SDK `Identity().GetIdentity` | `hey account list`, `--account` validation | covered |
| `/oauth/authorizations/new` | GET | `internal/auth` (PKCE S256) | `hey auth login` | covered |
| `/oauth/tokens` | POST | `internal/auth` | `hey auth login`, `hey auth refresh`, automatic refresh | covered |
