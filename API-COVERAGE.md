# API Coverage

Mapping of HEY API endpoints used by the CLI. Most endpoints use the HEY SDK (`hey-sdk/go`).
The legacy `internal/client/` is used only for HTML-scraping gap operations marked below.

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
| `/calendars.json` | GET | SDK `Calendars().List` | `hey calendars` | covered |
| `/calendars/{id}/recordings.json` | GET | SDK `Calendars().GetRecordings` | `hey recordings <calendar-id>`, `hey todo list`, `hey timetrack list`, `hey journal list` | covered |
| `/topics/{id}/entries` | GET (HTML) | Legacy `GetTopicEntries` | `hey threads <id>` | gap: SDK Entry lacks body |
| `/entries/drafts.json` | GET | SDK `Entries().ListDrafts` | `hey drafts` | covered |
| `/topics/messages` | POST | SDK `Messages().Create` | `hey compose` | covered |
| `/topics/{id}/messages` | POST | SDK `Messages().CreateTopicMessage` | `hey compose --topic` | covered |
| `/entries/{id}/replies` | POST | SDK `Entries().CreateReply` | `hey reply <topic-id>` | covered |
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
