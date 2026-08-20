# API Coverage

Mapping of HEY API endpoints used by the CLI. API interactions use the HEY SDK (`hey-sdk/go`).
The remaining HTML-reading gaps use the SDK's authenticated HTML helper and are marked below.

| Endpoint | Method | Client | CLI Command | Status |
|----------|--------|--------|-------------|--------|
| `/boxes.json` | GET | SDK `Boxes().List` | `hey boxes` | covered |
| `/boxes/{id}.json` | GET | SDK `Boxes().Get` | `hey box <id>` | covered |
| `/imbox.json` | GET | SDK `Boxes().GetImbox` | `hey box imbox` | covered |
| `/feedbox.json` | GET | SDK `Boxes().GetFeedbox` | `hey box feedbox` | covered |
| `/trailbox.json` | GET | SDK `Boxes().GetTrailbox` | `hey box trailbox` | covered |
| `/asidebox.json` | GET | SDK `Boxes().GetAsidebox` | `hey box asidebox` | covered |
| `/laterbox.json` | GET | SDK `Boxes().GetLaterbox` | `hey box laterbox` | covered |
| `/bubblebox.json` | GET | SDK `Boxes().GetBubblebox` | `hey box bubblebox` | covered |
| `/advanced_search.json` | GET | SDK `Search().Search` | `hey search`, TUI `/` | covered |
| `/advanced_search_filters.json` | GET | SDK `Search().Filters` | `hey search filters` | covered |
| `/contacts.json` | GET | SDK `Contacts().List` | `hey contacts list`, Contacts TUI | covered |
| `/contacts/{id}.json` | GET | SDK `Contacts().Get` | `hey contacts show`, Contacts TUI | covered |
| `/contacts.json` | POST | SDK `Contacts().Create` | `hey contacts add`, Contacts TUI | covered |
| `/contacts/{id}.json` | PATCH | SDK `Contacts().Update` | `hey contacts update`, Contacts TUI | covered |
| `/contacts/{id}.json` | DELETE | SDK `Contacts().Hide` | `hey contacts hide`, Contacts TUI | covered |
| `/contacts/{id}/reveal.json` | POST | SDK `Contacts().Reveal` | `hey contacts show-again`, Contacts TUI | covered |
| `/contacts/{id}/bundle.json` | POST | SDK `Contacts().Bundle` | `hey contacts bundle` | covered |
| `/contacts/{id}/bundle.json` | DELETE | SDK `Contacts().Unbundle` | `hey contacts unbundle` | covered |
| `/contacts/{id}/note.json` | GET | SDK `Contacts().Note` | `hey contacts note show`, Contacts TUI | covered |
| `/contacts/{id}/note.json` | PATCH | SDK `Contacts().SetNote` | `hey contacts note set`, Contacts TUI | covered |
| `/contacts/{id}/note.json` | DELETE | SDK `Contacts().DeleteNote` | `hey contacts note delete`, Contacts TUI | covered |
| `/calendars.json` | GET | SDK `Calendars().List` | `hey calendars` | covered |
| `/calendars/{id}/recordings.json` | GET | SDK `Calendars().GetRecordings` | `hey recordings <calendar-id>`, `hey todo list`, `hey timetrack list`, `hey journal list` | covered |
| `/topics/{id}/entries` | GET (HTML) | SDK `GetHTML` | `hey threads <id>` | gap: SDK Entry lacks body |
| `/topics/{id}/entries.json` | GET | SDK `Topics().GetEntries` | `hey attachments <topic-id>` | covered |
| `/messages/{id}.json` | GET | SDK `Messages().Get` | `hey attachments <topic-id>`, `hey attachments save <id>` | covered |
| `/entries/drafts.json` | GET | SDK `Entries().ListDrafts` | `hey drafts` | covered |
| `/rails/active_storage/direct_uploads.json` | POST | SDK `Attachments().Upload` | `hey compose --attach`, `hey reply --attach`, `hey bulk-reply send --attach` | covered |
| signed Active Storage upload URL | PUT | SDK `Attachments().Upload` | `hey compose --attach`, `hey reply --attach`, `hey bulk-reply send --attach` | covered |
| signed Active Storage blob URL | GET | SDK `DownloadBlob` | `hey attachments save <id>` | covered |
| `/messages.json` | POST | SDK `Messages().Create` | `hey compose`, `hey forward <topic-id>` | covered |
| `/entries/{id}/replies` | POST | SDK `Entries().CreateReply` | `hey reply <topic-id>` | covered |
| `/topics/{id}.json` | GET | SDK `Topics().Get` | `hey forward <topic-id>` | covered |
| `/entries/{id}/forwards/new.json` | GET | SDK `Entries().NewForward` | `hey forward <topic-id>` | covered |
| `/bulk_replies/new.json` | GET | SDK `BulkReplies().Draft` | `hey bulk-reply preview`, `hey bulk-reply send`, TUI `b` | covered |
| `/bulk_replies.json` | POST | SDK `BulkReplies().Send` | `hey bulk-reply send`, TUI bulk reply | covered |
| `/bulk_replies/{id}/undo_send` | POST | SDK `BulkReplies().Undo` | `hey bulk-reply undo`, TUI `u` | covered |
| `/bulk_replies/{id}` | GET (redirect target) | SDK `BulkReplies().Undo` redirect handling | `hey bulk-reply undo`, TUI `u` | covered |
| `/postings/moves.json` | POST | SDK `Postings().Move` | `hey move <id> --to <box>`, TUI `m` | covered |
| `/postings/trash.json` | POST | SDK `Postings().MoveToTrash` | `hey trash <id>`, TUI `t` | covered |
| `/postings/spam.json` | POST | SDK `Postings().MarkSpam` | `hey spam <id>`, TUI `s` | covered |
| `/postings/mutings.json` | POST | SDK `Postings().Mute` | `hey ignore <id>`, TUI `-` | covered |
| `/postings/mutings.json` | DELETE | SDK `Postings().Unmute` | `hey stop-ignoring <id>`, TUI `+` | covered |
| `/calendar/days/{date}/habits/{id}/completions.json` | POST | SDK `Habits().Complete` | `hey habit complete <id>` | covered |
| `/calendar/days/{date}/habits/{id}/completions.json` | DELETE | SDK `Habits().Uncomplete` | `hey habit uncomplete <id>` | covered |
| `/calendar/days/{date}/journal_entry.json` | GET | SDK `Journal().Get` | `hey journal read [date]` | partial: falls back to legacy |
| `/calendar/days/{date}/journal_entry/edit` | GET (HTML) | Legacy `GetJournalEntry` | `hey journal read [date]` | gap: fallback for 204 response |
| `/calendar/days/{date}/journal_entry.json` | PATCH | SDK `Journal().Update` | `hey journal write [date]` | covered |
| `/calendar/ongoing_time_track.json` | GET | SDK `TimeTracks().GetOngoing` | `hey timetrack current` | covered |
| `/calendar/ongoing_time_track.json` | POST | SDK `TimeTracks().Start` | `hey timetrack start` | covered |
| `/calendar/time_tracks/{id}.json` | PUT | SDK `TimeTracks().Stop` | `hey timetrack stop` | covered |
| `/calendar/todos.json` | POST | SDK `CalendarTodos().Create` | `hey todo add` | covered |
| `/calendar/todos/{id}/completions.json` | POST | SDK `CalendarTodos().Complete` | `hey todo complete <id>` | covered |
| `/calendar/todos/{id}/completions.json` | DELETE | SDK `CalendarTodos().Uncomplete` | `hey todo uncomplete <id>` | covered |
| `/calendar/todos/{id}.json` | DELETE | SDK `CalendarTodos().Delete` | `hey todo delete <id>` | covered |
