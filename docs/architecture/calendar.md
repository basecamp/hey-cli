# Calendar and time tracking in the TUI

## Each calendar span gives the arrows what it is made of

The three spans are not one screen with a different date on it, so the arrows mean something
different in each — `handleArrowKey` in `calendar.go` is where that is decided.

The **day** is one day, so ← and → walk the grid and carry on into the day either side, landing
on the far end of it. Its all-day band belongs to no hour, so ↑ and ↓ cross down into it and back
up to the event they left (`crossTheDay`, `lastTimedEvent`) rather than it being walked sideways.

The **week** is seven days, so ← and → walk the days and ↑ and ↓ that day's events. **The cursor
is the anchor** — that is the whole trick, and it is why `b`, `s` and `a` need telling nothing:
they all file on `v.day()` already. Moving inside the week does not re-read; crossing its edge
does, keeping the weekday.

The **year** is a grid, and a grid wants moving through before anything in it can be worked on:
the arrows move between cells, enter steps into one (`inYearCell`), and only then do ↑ and ↓
belong to that day's events. `esc` comes back out through `CancelPendingDetail`, because the
model reads esc before a view sees a key. A year read carries no recordings, so `b` there manages
habits without keeping them — nothing on that screen knows what was kept on the cursor's day.

`renderYearView` answers the cursor's line range as well as the year, and `revealRows` scrolls
the minimum needed. A week row is as tall as its busiest day, so a week's *number* says nothing
about which line it is on — scrolling by the number is what let the cursor walk off the bottom.

**All-day start and end dates are inclusive.** HEY serves them as UTC midnights, but they
are calendar dates, not instants to convert or exclusive boundaries. The card shows both
dates, and `eventsByDate` keeps the final day in the grid. Only a timed event ending at
midnight excludes the following day.

**A selection is a `Recording.key()`, not an id.** HEY serves a repeating event's days as virtual
occurrences with `id: 0` and an `occurrence_id` of `"<series>_<date>"`, so an id keyed selection
could not pick one out at all. The same fact decides how it is written: `occurrenceOf` sends an
edit or a delete of such a day through `CalendarEvents().UpdateOccurrence`/`DeleteOccurrence`
with `OccurrenceScopeThisEvent`. A day HEY has written out on its own has an id as well as its
`occurrence_id`: an edit goes by the id, but a delete still goes through the occurrence, because
HEY's `events#destroy` removes that recording without writing the day into the series'
exceptions, and the series then draws the day again. `hey event delete` refuses that id for the
same reason and deletes a day with `--occurrence`.

The selected event is marked by a light travelling round its edge rather than a color of its own
— it keeps its calendar's, which is what says whose it is. `sweepIntensity` and `edgePosition` in
`calendar_views.go`; the tick is `calendarTickMsg`, and it stops itself when nothing is selected
or when nothing has drawn the calendar since the last one, which is how it notices the reader
went to another section without a hook for it. The blend only happens between colors a theme
named: mixing an ANSI slot would put a color from a palette nobody is using on screen.

## Tracked time is read from its own index, and nothing else works

`l` opens time tracking: the stopwatch, the tracked-time screen and the categories. Three facts
about that corner are worth knowing before touching it, because each one cost a wrong turn.

**Tracked time is only readable through `GET /calendar/time_tracks`.** Reading `Calendar::TimeTrack`
out of the personal calendar's recordings does not work — a 90-day window answers none at all and
a three-year one misses older tracks — and the CSV export, which does have the right scope, carries
no ids, so nothing read that way can be edited. That index answered 406 until haystack grew a JSON
view for it (basecamp/haystack#8657); the SDK's `TimeTracks().ListPage` models it, and the screen
grows down it the way every other list here grows.

**A track's category is a title, not an id.** It arrives as a plain string, empty for a track filed
under nothing, and it is written back as `category_title` — which *creates* the category if HEY has
none by that name. A blank does not un-file a track: once filed, a track can only be moved. The
form says both of those out loud rather than letting a reader find out.

**Every update completes a track.** `@recording.complete` is unconditional, so there is no
adjusting a running one — an update stops it. That is why starting a track cannot name a category
either, and why the stopwatch on the menu is the only place a running track appears.

**An event write is not a partial.** HEY clears the notes, location, link and attached entry on
any update that omits them, and reminders and the countdown likewise, so `startEventForm` hands
an edit what the event carries (`setDetails`) and `saveEvent` sends all of it back. Without that,
renaming an event wipes its notes. Notes are served as plain text however they were written, so
the form says out loud that saving replaces any formatting.
