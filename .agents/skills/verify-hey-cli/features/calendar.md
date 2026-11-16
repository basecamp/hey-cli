# Calendar events and repeating days

Create an event with the intended fields/date/time, edit without losing other fields, and change/delete the intended occurrence or future series.

## Sub-features

- Calendar discovery and event list/day/week windows.
- Timed/all-day event creation, editing, field preservation and deletion.
- Current/future repeating-day edits and split-series cleanup.

## How to get to it (user POV)

Use `hey calendar list`, `hey event list/day/week/add/edit/delete`, and occurrence arguments documented in `hey event edit --help`/`delete --help`. The TUI calendar/cards/forms are a separate entry point in [TUI](tui.md).

## Driving it with the smoke harness

Preconditions: doctor passed; the ordinary calendar selected by the fixture must be writable and switched on, with the relevant account time zone; day-page assertions omit switched-off calendars. Read `tests/smoke/events_test.go` and `docs/architecture/calendar.md` before selecting mutations.

- **Fields/dates.** Run `"$VERIFY" TestEventCRUD TestEventAllDay TestEventListWindowsAndFormats`. CRUD observes the created title on the calendar page, then checks fields in the edit response; AllDay checks the create response flag. These baselines do not independently prove persisted edited fields or exact dates/times. For such a report, add a fresh all-calendar list read and independent page observation of the exact stored values.
- **Repeating scopes.** Run `"$VERIFY" TestEventOccurrenceEditScopes TestEventOccurrenceFutureSplitAllowsAnEarlierTime TestEventOccurrenceFutureSplitRefusesARealizedDayMovedEarlier`. These own their series and track touched dates/splits. Add the exact time-zone, realized/virtual-day or scope condition for the report.
- **Validation.** Run `"$VERIFY" TestEventCommandsValidateInput` for the existing refusals. An empty event list on its own is not positive calendar coverage.
- **Cleanup.** Require the owned titles/series to be absent through all-calendar list windows, including switched-off calendars. The day view omits switched-off calendars and cannot prove complete cleanup.

## Gotchas

Server/TUI virtual occurrences have ID 0; CLI day/week rows expose the series ID for virtual days. Never use numeric 0. Edit a realized day through its recording ID, but delete that day through `--occurrence`; virtual days use the series ID plus occurrence key. List returns stored series and written-out occurrence rows, while day/week expand selected calendars into occurrences. All-day dates are inclusive calendar dates, not UTC instants. Updates resend fields rather than applying a partial patch; whole-event edits flatten notes and remove countdowns unless supplied again. Week output and occurrence deletion are user paths not exercised by the named baseline cases; add live assertions when they matter. Do not select an existing personal event as a mutation fixture; calendar, todo, habit, journal and time tracking are not interchangeable coverage.
