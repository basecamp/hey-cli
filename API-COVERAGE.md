# API Coverage

Mapping of HEY API endpoints used by the CLI, based on `internal/client/*.go`.

| Endpoint | Method | Client Function | CLI Command | Status |
|----------|--------|-----------------|-------------|--------|
| `/boxes.json` | GET | `ListBoxes` | `hey boxes` | covered |
| `/boxes/{id}.json` | GET | `GetBox` | `hey box <name\|id>` | covered |
| `/calendars.json` | GET | `ListCalendars` | `hey calendars` | covered |
| `/calendars/{id}/recordings.json` | GET | `GetCalendarRecordings` | `hey recordings <calendar-id>` | covered |
| `/entries/{id}` | GET (HTML) | `GetEntry` | `hey topic <id>` | covered |
| `/entries/drafts.json` | GET | `ListDrafts` | `hey drafts` | covered |
| `/topics/{id}/entries` | GET (HTML) | `GetTopicEntries` | `hey topic <id>` | covered |
| `/topics/messages` | POST | `CreateMessage` | `hey compose` | covered |
| `/topics/{id}/messages` | POST | `CreateMessage` | `hey compose --topic` | covered |
| `/entries/{id}/replies` | POST | `ReplyToEntry` | `hey reply <topic-id>` | covered |
| `/calendar/days/{date}/habits/{id}/completions.json` | POST | `CompleteHabit` | `hey habit complete <id>` | covered |
| `/calendar/days/{date}/habits/{id}/completions.json` | DELETE | `UncompleteHabit` | `hey habit uncomplete <id>` | covered |
| `/calendar/journal_entries.json` | GET | `ListJournalEntries` | `hey journal list` | covered |
| `/calendar/days/{date}/journal_entry/edit` | GET (HTML) | `GetJournalEntry` | `hey journal read [date]` | covered |
| `/calendar/days/{date}/journal_entry.json` | PATCH | `UpdateJournalEntry` | `hey journal write [date]` | covered |
| `/calendar/time_tracks.json` | GET | `ListTimeTracks` | `hey timetrack list` | covered |
| `/calendar/ongoing_time_track.json` | GET | `GetOngoingTimeTrack` | `hey timetrack current` | covered |
| `/calendar/ongoing_time_track.json` | POST | `StartTimeTrack` | `hey timetrack start` | covered |
| `/calendar/time_tracks/{id}.json` | PUT | `StopTimeTrack` | `hey timetrack stop` | covered |
| `/calendar/todos.json` | GET | `ListTodos` | `hey todo list` | covered |
| `/calendar/todos.json` | POST | `CreateTodo` | `hey todo add` | covered |
| `/calendar/todos/{id}/completions.json` | POST | `CompleteTodo` | `hey todo complete <id>` | covered |
| `/calendar/todos/{id}/completions.json` | DELETE | `UncompleteTodo` | `hey todo uncomplete <id>` | covered |
| `/calendar/todos/{id}.json` | DELETE | `DeleteTodo` | `hey todo delete <id>` | covered |
