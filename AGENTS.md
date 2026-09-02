# hey-cli

This file provides guidance to AI coding agents working with this repository.

## What is hey-cli?

hey-cli is a CLI and TUI interface for [HEY](https://hey.com). 
It allows users to read and send emails, manage their boxes, manage their calendars and journal entries.
The TUI is primarily intended for human use, while the CLI is primarily intended for use by AI agents and for scripting.

## Development commands

This project uses make. `make help` covers most targets -- though not all of them:
`install`, `check-toolchain` and `help` itself are defined but unlisted.

```bash
make build   # Builds ./bin/hey
make test    # Runs the tests
make lint    # Lints the code
make install # Installs the binary to /usr/local/bin/hey (not shown by make help)
```

## Git

History is linear: merge commits are disallowed in this repository. Bring a branch up to
date by rebasing it onto main, never by merging main into it.

## Architecture Overview

This is a Go project that uses:
- [spf13/cobra](github.com/spf13/cobra) for the CLI interface
- [charm.land/bubbletea/v2] for the TUI interface along with bubbles/v2 and lipgloss/v2 (these are new versions that recently came out and differ from the v1 versions!)

All API interactions go through the HEY SDK (`hey-sdk/go`), with typed service methods accessed via `internal/cmd/sdk.go` (e.g., `sdk.Boxes().List`, `sdk.Messages().Create`, `sdk.Calendars().GetRecordings`). Authentication and token refresh are handled via `internal/auth/`.

### Authentication

Authentication supports four methods, all managed through `internal/auth/`:

1. **Browser-based OAuth with PKCE** (primary) — `hey auth login` opens a browser for OAuth authentication against HEY's own OAuth server (`/oauth/authorizations/new`), using PKCE (S256) for security. A local callback server on an ephemeral `127.0.0.1` port (RFC 8252 §7.3) receives the authorization code, which is exchanged for access and refresh tokens at `/oauth/tokens`.
2. **Pre-generated bearer token** — `hey auth login --token TOKEN` stores a token directly.
3. **Browser session cookie** — `hey auth login --cookie COOKIE` uses an existing HEY.com session.
4. **Environment variable** — Set `HEY_TOKEN` to use a token without storing it.

The auth Manager (`internal/auth/auth.go`) proactively refreshes tokens with a 5-minute expiry buffer. The SDK uses the Manager to authenticate requests via a bridge in `internal/cmd/sdk.go`.

All data-access commands call `requireAuth()` before making API calls. At an interactive
terminal with styled output, `requireAuth()` offers to sign in on the spot ("Not logged in.
Sign in now?" → OAuth → the command continues); piped, machine-output or declined runs get
`Error: Not logged in` / `Run: hey auth login` with exit code 3. Auth subcommands (`hey auth
login`, `hey auth logout`, `hey auth status`), the `hey login`/`hey logout` shortcuts,
`hey setup` and `hey doctor` work without authentication.

Bare `hey` at an interactive terminal runs the setup wizard when logged out
(`runSetupWizard` in `internal/cmd/setup.go` — welcome, OAuth sign-in, signed-in identity,
coding-agent setup, summary) and stops at the summary; `hey setup --skip-agents`
and `--skip-omarchy` leave those integrations unchanged. In every other case bare `hey`
prints help. The TUI lives at `hey tui` (plus the hidden `hey hey`). `config.json`'s
`onboarded` flag only trims a later logged-out run to the sign-in step. `HEY_NONINTERACTIVE=1`
disables interactive sign-in regardless of TTY detection; detected agent setup continues
without prompting.

On Omarchy, the full `hey setup` wizard installs and enables the `37signals.hey` bar plugin
without prompting. Other interactive OAuth sign-ins — `hey auth login`/`hey login`, the
lite wizard, or `requireAuth`'s prompt — offer it once and remember the answer in
`StateDir()/omarchy/bar-plugin.json`, serialized by a flock next to it. The sign-in hook
never runs for machine output, `HEY_NONINTERACTIVE`, a non-TTY, or `--token`/`--cookie`
logins, and it never re-enables a plugin that is off the bar. The full wizard and explicit
`hey setup omarchy` do re-enable it; the explicit command installs in every output format
and fails loudly (`setup_failed`) on any incomplete outcome. `--remove` writes its
tombstone first, disables, and keeps the checkout. Details and the state model are in
docs/omarchy.md.

Coding-agent integration lives in `internal/harness` (agent registry, Claude Code / Codex / Grok
detection, plugin and skill health checks) and `internal/cmd/setup_agent*.go` (`hey setup
claude|codex|grok|agents`). Claude Code gets the `hey@37signals` plugin from `basecamp/claude-plugins`
plus a skill link; Codex and Grok get the skill only until a `.codex-plugin` / `.grok-plugin` ships. `HEY_SETUP_AGENT`
selects the target for `hey setup agents`; `hey setup agents --remove` uninstalls the
Claude plugin and removes only hey-cli-managed skill files. `hey doctor` reports per-agent diagnostics, and a
`PersistentPostRunE` hook (`skill_refresh.go`) re-syncs installed skill copies once per release
version change. Ownership is explicit: every skill write goes through `claimSkillDir`, which
marks a directory it creates (`.managed-by-hey-cli`) and refuses a populated one without the
marker; removal (`removeExistingSkillLink`) touches only our canonical symlink or a marked
copy; refresh writes only marked, non-symlinked files. Do not add a write path that bypasses
the gate.

New top-level commands and subcommands need the `.surface` snapshot updated
(`go test ./internal/cmd -run TestSurfaceSnapshot -update-surface`), and anything listed in
root help needs the byte-exact literal in `help_test.go` updated.

### State storage

Configuration (the base URL, the default linked account, the Imbox's cover, and the `onboarded` flag) is stored in `~/.config/hey-cli/config.json`; `onboarded` is read from the global file only, never from a repository's `.hey/config.json`. Credentials are stored in the system keyring (service name: `hey`) with automatic fallback to `~/.config/hey-cli/credentials.json` when the keyring is unavailable. Set `HEY_NO_KEYRING=1` to force file storage.

### CLI

Remember to update the examples in the README when you change, add or remove CLI commands.

### HTML content

**Nothing scrapes a web page any more.** HEY grew JSON for every endpoint the CLI reads,
the SDK dropped its own scrapers, and the last three scrapes here are gone. Do not answer
"the JSON looks incomplete" by parsing a web page — check whether the SDK already has a
typed operation, and if HEY does not serve JSON for what you need, add it to the SDK
rather than working around it here. `sdk.GetHTML` has no callers left; if you find yourself
reaching for it, that is the signal to stop.

The journal used to be the example of this: a day with an entry answered 204, so the
content was scraped from the Trix input on `/calendar/days/{date}/journal_entry/edit`.
HEY answers the entry itself now, a 204 means the day is empty, and the scrape is gone.

The last three went the same way. Two facts about the typed reads are worth keeping,
because both were mis-stated here before:

- **`Topics.Get` does not carry bodies.** It carries the topic's first page of entries as
  summaries; the SDK says so at `generated/client.gen.go`. A body comes from
  `Messages().Get` per entry. `internal/threadload` is the one place that read is done
  for the CLI: it walks `Topics().GetEntriesPage` newest first and then fans out over
  `Messages().Get`, within literal limits (`threadload.DefaultLimits`: pages, entries,
  message requests, retries, concurrency, retained bytes, deadline), and reports per
  entry whether the body was `hydrated`, `bodyless`, `not_requested`, `over_limit` or
  `failed`. Hydration is the caller's decision: `hey thread read --count` and `--ids-only`
  read the index only, `hey attachment list` always reads bodies because the metadata lives
  in the HTML. A thread that could be read only in part is refused without
  `--allow-partial`; with it the notice says what is missing (`threadNotice` in
  `internal/cmd/thread_source.go`). The package takes a `Source` interface rather than the
  SDK; `threadload.NewSDKSource` is the adapter both the CLI and the TUI hand it, and the TUI's `mailView.fetchTopic` reads through it
  too — every page, oldest first, unread bodies marked and a notice for a partial read —
  and then fetches inline images within `imageBudget` (`internal/tui/image_budget.go`:
  requests, bytes, deadline, de-dup, clean HEY blob paths only — every request is charged
  to the count whatever it answers, and each fetch is told what is left of the bytes so an
  oversized image stops on the wire). A thread read only in part keeps its notice on
  screen for as long as it is open (`mailView.threadNotice`) and is not marked seen by
  being opened; the seen key still is. HEY serves the entry index newest first; the
  loader admits newest first and reverses once, so a thread reads oldest first.

  The entry index is geared_pagination like every other list here, so its page is a cursor
  out of the `Link` header and a number is answered with the first page forever —
  `Topics().GetEntries` throws that header away, which is what `GetEntriesPage` exists
  for.

  The SDK bounds its own reads: its transport caps JSON and HTML response bodies —
  success and error alike, decompressed — at `hey.DefaultMaxResponseBodyBytes` (16 MiB;
  `hey.WithMaxResponseBodyBytes` to change it, and there is no opt-out — a zero or
  negative value means the default), leaving blobs to the SDK's own streaming and caps.
  An oversized *error* response keeps its status: the refusal arrives as the `*hey.Error`
  for the status wrapping `hey.ErrResponseTooLarge`, which is why
  `internal/threadload/sdk.go` classifies a failed message read by status before size —
  an oversized 500 is still systemic and an oversized 404 is still just a missing
  message; only an oversized success is `over_limit`.
- **A reply starts from HEY's prefill, with a local fallback.** The SDK's
  `Entries().NewReply` (`GET /entries/{id}/replies/new.json`) answers how a reply starts
  out: its "Re: …" subject and its recipients, with the entry's sender moved onto the To
  line (haystack's `directly_address_sender`) *and* the acting user's own addresses,
  aliases, catch-alls and redelivery contacts removed — the exclusion this CLI cannot
  compute locally. Both reply paths — `hey reply` in `internal/cmd/thread_reply.go`,
  and the TUI's reply form via `loadReplyContext` in `internal/tui/compose.go` — ask
  the shared `mail.ReplyPrefillFromServer` (`internal/mail/reply_prefill.go`) first and
  fall back to their local computation (`recipientsForReplyTo` plus the derived subject)
  on a failed read or an empty recipient answer, which a thread with yourself produces;
  the prefill's subject survives that recipient fallback. Extend
  `mail.ReplyPrefillFromServer` rather than reimplementing HEY's exclusion rules in
  each caller.

`internal/htmlutil` provides `ToMarkdown` (HTML→Markdown), `ToText` (HTML→plain text),
`ExtractImageURLs` and `ExtractAttachments`, which are presentation helpers rather than
scrapers and are staying. `ToMarkdown` is also where email content stops being trusted:
see "Terminal safety" below. There is no `internal/models` any more: the CLI's output shapes
are local to the commands that print them, mail's shapes are `internal/mail`'s `Source`,
`Posting` and `Entry`, and `Contact`, `Calendar` and `Recording` are plain view types
declared next to the Contacts and Calendar sections that read them.
HEY uses the Trix editor with `<figure data-trix-attachment="{...}">` for
attachments — image URLs in those attributes are relative paths requiring authentication
via `sdk.Get`.

### Terminal safety

Everything HEY serves was written by somebody else, and a terminal acts on what it is
handed. The model has three layers, and it is worth knowing which one a change touches:

- **Metadata** — a sender, a subject, a filename, a box name, a habit title — goes through
  `terminal.Sanitize`/`SanitizeLine` (`internal/terminal`), which strips escape
  sequences, C0/C1 controls and the `Bidi_Control` set. Its confusable policy (in the
  package doc) strips the format characters that draw nothing (zero width space, word
  joiner, soft hyphen, BOM, …) and combining marks past eight on a base, keeps ZWJ/ZWNJ
  where they join (emoji families, Persian, Devanagari), drops a byte that is not
  UTF-8, leaves NBSP and friends alone, and does not detect homoglyphs — a
  URL-shaped link label is shown beside its destination by `htmlutil.ToMarkdown`
  instead. `markdown.Render` runs the same pass over a whole body, destinations
  included, so `htmlutil` sizes a code delimiter on the content as that pass leaves it
  (`backtickRun`), or a zero width space between two backtick runs would join them
  into a closing delimiter. It is applied once where it
  covers the most: the CLI's `table.addRow` sanitizes its cells, and the TUI's read-only
  models (`mail.NewPosting`, `mail.NewEntry`, `searchMatchToPosting`) are sanitized as
  they are built. The
  editable ones — `sdkContactToModel`, `sdkRecordingToModel` — are deliberately **not**:
  their fields go back through the edit forms, and sanitizing them would rewrite a name
  on an unrelated save. Every view of a contact or a recording sanitizes what it shows. `TestSinksAreSanitized` (`internal/cmd/
  sink_manifest_test.go`, manifest in `testdata/sink_manifest.txt`) fails on a direct
  write of a listed field to a listed sink without a sanitizer. That check is syntactic —
  it does not follow a value through a local variable — so it is an inventory of the
  direct pattern, not a taint analysis; treat a new exemption as something to justify in
  the manifest.
- **Bodies** are `htmlutil.Markdown`, a sealed type only `htmlutil.ToMarkdown` can make
  (unexported field, no constructor, marshals as the string it holds) and the only thing
  `markdown.Render` accepts — so a string from anywhere else cannot reach glamour by
  looking like Markdown. `ToMarkdown` serializes each context on its own terms: prose escapes metacharacters and writes every `&` as `&amp;` (the live
  bug this closed was `&amp;#27;[31m` decoding twice, once in the HTML parser and once in
  the Markdown renderer, into a red terminal); inline code and fences are verbatim inside
  delimiters longer than any run they hold; destinations percent-encode what would end
  them, keep a query string's `&`, and link only http, https, mailto and relative paths.
  Prose goes through `terminal.Sanitize` first and is written in that form; code and destinations strip controls and are measured through the sanitizer (`backtickRun`, `needsPadding`). `markdown.Render` then strips controls and bidi controls from the body, rewrites the
  spans glamour decodes so that its extra entity decode is the identity, shows a
  document nested past twenty levels unrendered rather than handing glamour something
  exponential, and checks its own output: anything but SGR and OSC 8 to an allowed scheme strips all
  styling rather than guessing. That last check is a backstop, not the guarantee — an
  injected SGR is byte-identical to one of ours.
- **JSON** is lossless and escapes the C1 controls (`output.MarshalJSON`), which
  `encoding/json` would otherwise write raw. A `--jq` string result on a pipe stays raw,
  as `jq -r` writes it.

The Markdown in `--json` is standard CommonMark; what a conformant renderer shows for it
is the literal text and URL the email held — less, in prose, what `terminal.Sanitize`
removes, since prose is written in its sanitized form so that the renderer's own pass
over the body cannot uncover a block marker the escaping did not see (`\u200b# heading`);
the HTML keeps the original. That is what the htmlutil tests assert
through goldmark. Fuzz targets hold the invariants: `FuzzToMarkdownTerminalSafety` in
htmlutil and `FuzzContainment` in markdown.

### Email bodies are Markdown

An email body reaches the CLI and the TUI as HEY's Trix HTML and is converted once, at the
edge, by `htmlutil.ToMarkdown` — for a thread's messages that edge is `mail.NewEntry`.
Everything downstream — `mail.Entry.Body`, `--json`, the TUI viewport — carries Markdown, so
links keep their URLs and headings, lists, quotes, tables and code survive.
`Entry.BodyHTML` keeps the original HTML for `--html` and for
`ExtractImageURLs`/`ExtractAttachments`, which need the attributes the Markdown drops.

`--html` is the one format that writes that HTML out, and it has two shapes. A thread
(`writeThreadHTML` in `internal/cmd/topic.go`) is an HTML5 document: `<!doctype html>`,
`<meta charset="utf-8">`, `<title>Thread N</title>`, then one
`<article id="entry-ID" data-entry-id data-created-at data-body-state>` per entry, oldest
first, with a `<header>` naming the sender and date (sanitized, then HTML-escaped) and the
entry's HTML verbatim — or nothing, with `data-body-state` saying why (`bodyless`,
`over_limit`, `failed`, or `hydrated` and empty). A partial thread needs `--allow-partial`
as everywhere else and then ends with `<!-- notice: … -->` before `</body>`
(`htmlCommentSafe` keeps a value from closing the comment), with the notice on stderr too.
A single body — `journal read`, `contacts show`, `contacts note show` (`writeNoteHTML`) —
is a fragment: the body as HEY served it, nothing for an empty one. The difference is
deliberate: a thread has entries to frame; one body gets pasted into something else.
`--stats` is refused with `--html` like every other selector, since there is no envelope to
carry stats. `writeThreadHTML` is exempt in the sink manifest because writing the body raw
*is* the format, and `validateHTMLFlag` keeps it off a terminal.

`internal/markdown` renders that Markdown for a terminal: `Render(md, width)` wraps
glamour, and `LinkifyURLs` wraps bare URLs in OSC 8 so they stay clickable. The style in
`style.go` uses ANSI color slots rather than glamour's fixed palettes, for the same reason
`internal/tui/styles.go` does — the user's terminal theme wins. `Render` never fails
loudly: if glamour cannot be set up it returns the Markdown source, so a styling problem
costs formatting rather than the message.

`ToText` is still right where the result is not shown to anyone — the name tag
`hey bulk-reply` reads out of a draft, for instance.

### Text somebody else wrote goes through `internal/terminal`

A sender's name, a subject, a filename, a label — a terminal acts on the escape
sequences in all of it, so nothing HEY serves is printed raw. `terminal.Sanitize`
strips the sequences with `ansi.Strip` and then drops the leftover control
characters, keeping `\n` and `\t` because a body, a jq result and a Markdown cell
carry them on purpose. `terminal.SanitizeLine` is the same thing where only one
line fits — a table cell, a mutation confirmation — and turns those two into
spaces so nothing can move what comes after them.

Strip the sequence; do not deface it. Replacing the ESC byte with U+FFFD, which
this repo used to do in six places — three of them the TUI's own
`terminalSafeFolderText`, `terminalSafeCollectionText` and
`terminalSafeAttachmentText` — leaves the payload on screen as debris of
somebody else's choosing — and `runewidth` then measures that debris when it lays
out a table, so the column width is theirs to pick.

### One line for a person, the envelope for everyone else

Every command that writes something confirms it through `writeMutation` (or
`writeMutationLine`, where the line carries an id or a name the summary does not).
It prints the sanitized line under `--styled` and hands `writeOK` the data and the
summary otherwise. Do not hand-roll the `if writer.IsStyled()` branch again: it was
copy-pasted about forty times, and three of the copies had drifted.

#### Inbound email hides its body in an attachment

`/messages/{id}` serves `content` through haystack's `trix_html_for_rich_text_editing`,
which has to hand Trix something Trix can edit. An HTML email from outside HEY is not
that, so the whole body is wrapped in one `<figure data-trix-attachment>` whose JSON
carries no `filename` and no `url` — just `contentType: "text/html"` and a `content`
string holding the original markup inside `<shadow-content><template>…`.

Treat that figure as a file attachment and the body vanishes: the TUI then shows nothing
but `Entry.Summary`, HEY's ~105-character preview ending in an ellipsis, which reads as a
truncated email. `htmlutil` tells the two apart on `filename` and parses the embedded
markup instead, bounded by `embeddedContentDepthLimit` so a chain of attachments that
each embed another cannot recurse without end. `ExtractImageURLs` looks inside it too,
since that is where an inbound email's inline images are; `ExtractAttachments` does not
list it, because an embedded body is not a downloadable file.

The web app has the same content and renders it in a sandboxed iframe, which is why the
`srcdoc` scrape in `ParseTopicEntriesHTML` never hit this.

### TUI structure

The TUI uses the `sectionView` interface pattern. Each top-level section (Mail, Calendar, Journal) implements `sectionView` and owns its data, fetch commands, key handling, rendering, and help bindings. The main model delegates to the active view.

Four files implement `sectionView` -- `mail.go`, `contacts.go`, `calendar.go` and
`journal.go`, one per section. `screener.go` implements it too, but The Screener is not a
section: ctrl+s from the mail list swaps it in as the active view, it captures every key
while it is open, and it asks to be closed again with `screenerClosedMsg`.
The rest of `internal/tui/` is shared infrastructure: `tui.go` (model and
router), `section_view.go` (the interface), plus `nav.go`, `content.go`, `help.go`,
`styles.go`, `loading.go`, `kitty.go`, `html.go`, `live.go`, `covers.go`, `cover_picker.go`,
`calendar_views.go`, `datetime.go`, `event_repeat.go`, `time_track.go` and
`time_track_form.go`. Read the directory rather than a table here.

To add a new section: implement the `sectionView` interface in a new file, add a field and constructor call in `newModel`, and add a case in `switchSection`.

### Each calendar span gives the arrows what it is made of

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

**A selection is a `Recording.key()`, not an id.** HEY serves a repeating event's days as virtual
occurrences with `id: 0` and an `occurrence_id` of `"<series>_<date>"`, so an id keyed selection
could not pick one out at all. The same fact decides how it is written: `occurrenceOf` sends an
edit or a delete of such a day through `CalendarEvents().UpdateOccurrence`/`DeleteOccurrence`
with `OccurrenceScopeThisEvent`.

The selected event is marked by a light travelling round its edge rather than a color of its own
— it keeps its calendar's, which is what says whose it is. `sweepIntensity` and `edgePosition` in
`calendar_views.go`; the tick is `calendarTickMsg`, and it stops itself when nothing is selected
or when nothing has drawn the calendar since the last one, which is how it notices the reader
went to another section without a hook for it. The blend only happens between colors a theme
named: mixing an ANSI slot would put a color from a palette nobody is using on screen.

### Tracked time is read from its own index, and nothing else works

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

### Lists grow as the reader scrolls

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

### A mail source reads its own page

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

### Imbox cover art

A cover is a lid over Previously Seen, not a decoration next to it. When the Imbox is
covered the threads the reader has already read are not drawn at all — the box ends at
what still wants attention — and the art fills the list to the bottom of the screen. That
is the whole point of the feature in the web app, and it is why `hey`'s version hides
rather than merely paints.

`internal/tui/covers.go` draws the art. HEY's covers are SVG assets
(`box-covers/{light,dark}/*.svg` in haystack) and a terminal cannot draw an SVG, so these
are the same six patterns — blobs, grid, peace, terrazzo, topo, waves — drawn as
characters. That is not a fallback: a blueprint grid is what box-drawing characters are
for.

**Curves go in braille.** A `brailleLayer` is a dot grid at 2×4 the resolution of the
canvas, folded into cells by `drawInto`. `paintTopo` draws contours into one. The dots are
square, incidentally: a cell is about twice as tall as it is wide and the 2×4 split cancels
that exactly, so nothing drawn there needs aspect correction. Reach for braille whenever a
pattern wants a curve, because the cell grid cannot hold one: those same contours in
box-drawing characters are cell-wide staircases that read as noise.

**Peace is the one cover that is a mark, and the terminal already has the mark.**
`paintPeace` tiles U+270C — two fingers up in a V, which is what the "peace" cover is, not
the CND symbol — offsetting alternate rows the way the web app's asset repeats it. The
glyph is deliberately bare: `peaceHand` carries no U+FE0F, because a variation selector
asks for emoji presentation and the glyph then measures two cells in some terminals and one
in others, which slides every hand to its right by an amount nothing here can know.
`TestPeaceHandIsOneCellWide` pins that.

Two things this costs, both accepted rather than overlooked. A color emoji font ignores the
foreground color it is handed, so the hand arrives in the font's colors rather than the
reader's `Yellow` — the field is still theirs, and the ink is a fallback for the terminals
that render it monochrome. And a glyph is one size forever, so `peaceSpacingX`/`Y` are the
only constants here that do *not* scale with the block: the lattice gets denser on a big
cover rather than bigger, which is what a repeating asset does anyway.

**Otherwise, scale every dimension with the block, then check the extremes.** A cover is
anywhere from 40×6 to 300×90, so a constant tuned at one size is a constant that only looks
right there. `waveRibbonShape` is the worked example: the amplitude follows the height, and
the wavelength takes whichever is larger of a quarter of the width and whatever the
amplitude needs to stay under `maxWaveSlope`. Scaling by height alone drew chevrons on a
tall cover; by width alone, a busy repeat on a wide short one. Pin it with a test over a
spread of aspect ratios — it is much cheaper than looking.

A painter writes glyphs into a `coverCanvas` and the field is whatever it leaves blank,
which is why a colorless terminal still gets the art instead of an empty band. The colors
are the ANSI-16 slots, for the reason `styles.go` gives: a desktop theme defines those
sixteen and retints running terminals over OSC 4, so a cover wears the reader's palette
and restyles live on a theme switch without anything here being told. HEY's own covers are
yellow and mint and violet; `coverPalettes` names the slots those stand in for. Do not put
hex back — a cover in HEY's colors on someone's gruvbox is the one thing that will look
wrong on every theme but one.

**There is one palette per preset, not a light one and a dark one**, even though the web
app needs both. Pick each slot for the job it does in a theme rather than for how bright it
happens to be, and the light-versus-dark question stops being asked. `Black` is the
background and `White` the foreground in *every* theme, so a field of `Black` is the
reader's own paper and foreground ink is legible on it either way — that is why HEY's white
terrazzo chips correctly turn black in light mode without a light palette existing. Hues
are mid-tones everywhere, so a hue on a hue holds its contrast too.

Choosing a slot for its brightness is the trap, and it fails in the direction that is
hardest to notice: a light-mode field painted `BrightWhite` comes out *dark*, because on a
light theme the bright foreground is a dark color. The cover is then inverted on exactly
the themes the light palette was added to fix. `TestCoversDoNotDependOnTheThemeMode` is
there to catch it — flipping `Theme.Dark` must not change a single byte of what is drawn.

Everything is deterministic: the scatter and the noise come from a cell's own hash, so a
cover is the same picture every time, the way the web app's asset is. `coverRenderer`
memoizes the last one it drew, because the posting list re-renders on every keystroke and
a cover is a thousand styled cells. Its memo key includes the palette, so a theme switch
repaints.

What the lid costs the list is in `contentList`. `coveredFrom` is the index the cover
starts at and `itemCount` is what the reader can still reach, so the cursor cannot land
under the art and a bulk action cannot aim at threads the reader thinks are put away.
`settleCover` is what keeps that true as the list changes underneath: opening a thread
turns it seen, which slides it under the cover, and a live re-read can do the same to a
selection. `listHeight` holds back the divider and `coverMinRows` at the bottom so the
cover can never be scrolled past, and `coverView` then gives the art every row the threads
above it did not use. A list too short for even the floor keeps the divider and skips the
art — the hint is worth more than a smear.

`x` lifts the cover (`coverPeeked`) and puts it back; the divider carries the hint and the
hidden count, where the web app puts its buttons. Setting a cover closes it, so a box
arrives covered rather than however the last one was left. Only the Imbox is coverable —
that is haystack's rule (`Box::Imbox#coverable?`), and it is the same box that gets the
seen sections at all. `mail.Source.Coverable()` is where that is decided, off the kind HEY
served rather than the box's name: a label somebody called "Imbox" is a label.

`ctrl+v` opens `coverPicker`, which draws whichever preset is highlighted — a cover is
picked by looking at it. The choice is stored locally, by `config.Cover` and
`config.SaveCover`, reached through the `loadCover` and `saveCover` seams on the view
context so the TUI does not depend on where it lives. A failed write is a notice, not a
refusal: the cover is on screen either way and all that is lost is remembering it.

**Local storage is the deliberate answer here, not a stopgap.** haystack does keep a cover
per box, in a `box_covers` table with a real `preset_image` column — but it serves it to
nobody. There is no `cover` in any jbuilder, and both native apps went their own way rather
than wait for one: `CoverArtPersistenceManager` on iOS keeps the choice in `Preferences`,
`CoverArtRepository` on Android keeps it in `CachePrefs`, each with its own numbered presets
and bundled assets, neither ever asking the server. So a cover set here does not show up on
the web, and that is true of every HEY client. Before adding a read of `box_covers`, work
out why two mobile teams decided against it.

An uploaded cover has no honest character version — that one wants the Kitty path in
`kitty.go`.

The help bar puts chorded keys last, via `modifiersLast`. The single-key bindings are what
a reader reaches for while working through mail; a `ctrl+` chord in the middle of them
pushes the everyday ones onto a second line and makes the bar read as a lookup table.

### Inline images in the TUI

The TUI renders inline images using the Kitty graphics protocol's Unicode Placeholder extension (`internal/tui/kitty.go`). This works because Bubble Tea's cell-based renderer corrupts raw APC escape sequences, but Unicode placeholders are regular text that survives rendering. The approach has three steps:

1. **Upload** — image data is sent to the terminal via `tea.Raw()` with `a=t` (transmit only) and `q=2` (suppress response), then a virtual placement is created with `U=1`. The upload declares `f=100`, which means PNG, so `pngEncoded` re-encodes anything else first: a JPEG or a GIF handed over under that format code is dropped by the terminal and the thread shows a gap where its image was.
2. **Display** — U+10EEEE placeholder characters with combining diacritics (encoding row/column) are placed in the viewport content. The image ID is encoded in the foreground color, so ids come from `nextImageID`: they start at `0x010101` to keep every color byte non-zero, stay inside three bytes, and are never reused. Reusing an id replaces the image the terminal holds under it while the placement drawn for the old geometry is still on screen, and the new image renders clipped into the old one's cells.
3. **Sizing** — `image.DecodeConfig` reads dimensions without full decoding. The terminal stretches the image over the cells it is placed in and scales each cell on its own, so an image placed in more cells than its pixels cover comes out smeared and seamed rather than merely soft. `imageDimensions` therefore treats the image's own size as the ceiling, and a tall image gives up columns instead of its proportions when it hits `maxImageRows`.

This works in Kitty and Ghostty. Other terminals show the text content normally (placeholders are invisible).

### Watching for changes over Action Cable

`hey watch` is told when a box changed instead of polling for it.
`internal/cable` dials HEY's cable server with [actioncable-go](github.com/basecamp/actioncable-go),
authorizing the upgrade request with the same credentials the SDK sends on an API request
(`HEY_CABLE_URL` overrides the endpoint). `internal/cmd/watch.go` subscribes to
haystack's `Postings::ChangesChannel`, which broadcasts only `{change, account_id, box_id,
box_kind, posting_ids, at}` — a doorbell, not the change itself.

The change is then read through `Postings().AllChanges`, the same incremental sync feed the
mail clients use, starting from the cursor in the box's `posting_changes_url`. That is what
makes a reconnect safe: the cursor, not the notification, is the source of truth, so a
missed broadcast costs nothing, and a 409 means catch up in full instead. A read that
fails leaves the cursor where it was and is retried on a doubling backoff, so a change
isn't lost with the notification that announced it, and a subscription that closes without
the watch being interrupted is an error rather than a quiet exit. `ready` waits for that
retry too (`catchUp`/`retryUnread`/`readyOnceCaughtUp`): a catch-up that left a box behind
owes its ready until the box is read, and a drop in between cancels the debt — the
reconnect's catch-up announces its own. The look at the transition queue and the ready
announcement are one critical section with `noteConnection`'s queueing, which is what keeps
a drop from landing between them.

New mail is a watch event, not a flag: every added and updated line carries `"new":
true|false` and `--events new` selects the true ones (`internal/cmd/watch_new.go`). New is
unseen, not muted, and `active_at` later than the watch's record of the thread — or than
the watch's start, on HEY's clock (`serverNow`), for a thread it has no record of — and
every posting the watch reads is recorded, in every box and whatever `--events` or `--box`
says (`--box` picks the boxes whose changes are reported; every box is followed), each
posting recorded as soon as it is classified. The
start is the Date header translated back to when the request was made (mail that lands while
the server answers is later than the start), it is taken before the box list, and each
box's cursor starts no later than it (`noLaterThan`): the server bakes the box's last posting
activity into the cursor, so mail that landed in between would otherwise sit behind the
cursor, read by nothing. That
is HEY's semantics and state across events, so the CLI decides it once; what to do about
it is the reader's. A 409 skip-ahead sets that box's floor at the cursor it skipped to
(`newMail.skippedTo`): activity at or before it is never new there, known thread or not,
because the watch never read the gap. `resync` is an event of its own — reported by default,
left out by `--events new` — so a script for new mail never runs on one. The Omarchy bar plugin toasts from those lines itself (app-name, glyph,
click-to-focus and the replace-not-stack id all live in the plugin), and nothing
desktop-shaped lives in `watch*.go`.

The TUI's mail list follows the same channel and wants less from it. `internal/cmd/tui_watch.go`
subscribes and relays typed `tui.MailWatchEvent` values; `internal/tui/live.go` defines
that `tui.MailWatcher` contract, so the TUI never sees cable or auth, and a test hands it
a plain channel. Box events are doorbells: `mailView.refreshBox` reads that box's top page
again, which is where a change shows up. Connection events carry disconnect/reconnect
state. A reconnect asks for `tui.AnyBoxChanged`, standing for broadcasts sent during the
gap, and the model re-reads the box on screen.

`internal/tui/live.go` owns both refresh and reconnect timing. `liveRefreshDelay` collects
one delivery's changes into a single re-read — a thread rings the doorbell once per
posting. `liveRetryDelay` is how long a re-read waits when a form or a picker is open over
the list, or a write hasn't landed: the change is held rather than dropped, because a
re-read replaces what the reader is looking at. `mailWatchRetryDelay` backs a watch that
could not start or stopped for good from two seconds up to thirty; authentication failures
stand without retrying. `contentList.refreshHead` is what makes a re-read safe — unlike
`setPostings` it keeps the cursor on its posting and keeps a selection — and the re-read
has its own request lane (`liveRequestID`, `postingsRefreshedMsg`) so it can never be
confused with a read the user asked for, or show the spinner.

The watch outlives the view context, which a mail account switch throws away; that is what
`model.watchCtx` is for. The model holds connection state above the active section, so an
offline or reconnecting notice remains visible in Mail, Contacts, Calendar and Journal.
A temporary drop is followed by Action Cable's own reconnect; an initial network failure
or a stream that closes for good gets a fresh bounded dial from the model. The initial dial
itself is capped so it can report state instead of waiting inside Action Cable forever.
A successful connection removes the status row and catches the current mail box up.

The Screener is told over a different stream, because haystack has no channel for it:
`Clearance::Broadcasting` re-renders the Screener's own button over a Turbo stream, and
`clearances/index.jbuilder` serves that stream's signed name next to the pending count.
So the TUI subscribes to `Turbo::StreamsChannel` with `signed_stream_name` (that is
`tui.ScreenerWatcher`, and the name is why the watch can only be opened after a read).
What HEY broadcasts there is markup for the web app — nothing is parsed out of it, the
arrival is the whole message, and the count is read again behind it. The SDK's
`Clearances().Summary` is that read: the same cheap request as `PendingCount`, keeping
the stream name it used to throw away.

Every read of the count carries that name, which is what lets a stream that closed be
opened again: `startScreenerWatch` opens one whenever the name it is handed is new, so
ctrl+r, closing The Screener, or the doorbell itself will all reopen a watch the server
hung up on. Opening one gives up the one before it — the subscription belongs to the
watch's context, and cancelling is what unsubscribes it, so a stream nobody is following
does not go on ringing. A mail account switch gives it up for the same reason: the signed
name is the account's, and the new account's sources read serves its own. The subscription
uses that short-lived context, while `tuiCableClient` is owned by the TUI-wide watch
context; replacing a Screener name therefore unsubscribes it without closing Mail's shared
connection.

The doorbell always re-reads the count, wherever the user is, because the mail list is
where The Screener announces itself. When The Screener is what's on screen the queue is
re-read too, through `screenerPane.refreshHead` — the same keep-your-place trick as the
mail list — and held while a decision is in flight or the clear-everything question is up.
On the history tab nothing is read; the pending pane is just marked unloaded, which is
what `switchTab` already looks at. All the watches share one websocket
(`tuiCableClient` in `internal/cmd/tui_watch.go`): many subscriptions, one authorization.
A client that stopped itself preserves its terminal failure and never dials again.
`tuiSubscribe` detects that state after any failed subscription, drops the stopped client,
and dials a fresh one; a replacement that also stops is dropped and its failure is
classified as authentication or network so the TUI can respond consistently. Mail
doorbells are coalesced when their bounded queue
is full, while a connection transition drains those stale doorbells and takes priority;
the reconnect catch-up reads the current state once.

The calendars are told the way The Screener is, one Turbo stream per calendar rather than
one channel: `Calendar` broadcasts a refresh on its own stream whenever a recording on it
is written, and `calendars.json` serves each calendar's `signed_stream_name` next to its
`recording_changes_url` — the box shape, on the calendar. Nothing is parsed out of the
frame; the arrival is the whole message. What nobody broadcasts is the calendar *list*
changing, and the web app has the same blind spot (a calendar shared mid-session appears
on its next navigation), so a slow poll of the calendar-level changes feed
(`calendar_changes_url`, `Calendars().AllCalendarChanges` — one page, usually empty) is
how both followers learn of a calendar arriving or leaving. Its `added` bucket carries the
same wrapper as the list, stream name included, so a calendar learned of either way is
immediately subscribable.

The TUI's side is `tui.CalendarWatcher` (`internal/tui/live.go`), implemented by
`watchCalendarChanges` in `internal/cmd/tui_watch.go`: every calendar's stream folded into
one doorbell channel, the poll resubscribing arrivals and dropping leavers, and any
subscription closing on its own tearing the whole watch down — reopening resubscribes
everything, which is cheaper to reason about than limping on with some calendars quiet.
The model follows it only while the Calendar or Journal section is on screen
(`watchingCalendars` in `tui.go`): every other section re-reads on entry, so a doorbell
rung for a section nobody is looking at would be answered by nothing. A ring is debounced
through the same `liveRefreshDelay`, and the re-read is `calendarView.refreshLive` — the
ordinary span read, which never shows the spinner once a day has been drawn and keeps the
selection by `Recording.key()` — or `journalView.refreshLive`, which splices the fresh top
page in front of what the list had grown past it, the `refreshHead` trick with dates for
ids. Both hold the re-read while a form, a picker or a pending delete is up
(`liveRetryDelay`), and a failed start or closed stream retries quietly on
`mailWatchRetryDelay`'s curve: the calendar re-reads on every step the reader takes, so a
watch that is down costs staleness, not a notice.

`hey watch` follows the same streams on its own connection and reports the changes
themselves (`internal/cmd/watch_calendar.go`). Rings are coalesced per calendar for
`calendarCoalesceDelay`, then the calendar's recording feed is read from its cursor
(`Calendars().AllRecordingChanges`, cursors capped at the watch's start like the boxes' —
`calendarCursorNoLaterThan`) and each recording is a `recording_added`, `recording_updated`
or `recording_deleted` line naming its calendar where a mail line names its box. The poll
reports `calendar_added`, `calendar_updated` and `calendar_deleted`, and a recording feed's
409 is `calendar_resync` after skipping ahead to a fresh cursor from the list. The
email-specific flags switch all of it off — `--box`, or an `--events` list naming only
mail changes (`watchingCalendars` in watch_calendar.go) — and `ready` waits for the
calendars' catch-up exactly as it waits for the boxes', on the same retry backoff and the
same `readyOnceCaughtUp` critical section.

### API documentation

If you are unsure what the API endpoints are, what they expect or what they respond to you can read through the server implementation to understand how the API works.

The server is the `basecamp/haystack` repo. If you have it checked out (conventionally
`~/Work/basecamp/haystack/`), read it whenever you need to know how the API behaves.

If you don't understand how the routes are laid out you can call rails routes in that directory to get a list of all the routes and their corresponding controller actions.

### SDK

All API interactions must go through the HEY SDK (`hey-sdk/go`). There is no legacy client — the SDK is the only HTTP client.

If you need to call an endpoint that the SDK doesn't support yet, **add it to the SDK**.
The SDK is the `basecamp/hey-sdk` repo, conventionally checked out at
`~/Work/basecamp/hey-sdk`. To use your local changes:

1. Add a `replace` directive in `go.mod` pointing at your checkout. Give it a real path.
   The shell does **not** expand `~` in the middle of an argument, so
   `...hey-sdk/go=~/Work/...` reaches Go as a literal tilde and the command fails with
   `unversioned new path must be local directory`:

   ```bash
   go mod edit -replace github.com/basecamp/hey-sdk/go="$HOME/Work/basecamp/hey-sdk/go"
   ```
2. Implement and test your changes
3. **Call out that you made changes to the SDK** when you're done — your operator will review those changes and publish a new SDK release, then you can remove the `replace` directive and pin to the released version

Those paths are in the SDK repo, not this one. There, the hand-written service wrappers
in `go/pkg/hey/` (e.g. `messages.go`, `journal.go`, `postings.go`) are safe to edit
directly. The Smithy-generated code in `go/pkg/generated/` is not — update the Smithy
model and run `make smithy-build` then `make go-generate` from the SDK root. hey-cli has
neither target.

### Examples

When you add any kind of example make it realistic.

For emails, always use @example.com or @example.org domains to avoid accidentally sending emails to real people.
For names, use common names or fictional characters. For calendar events, use plausible titles and times. 
The goal is to make the examples feel authentic without risking privacy or confusion.
Never use abbreviations or placeholders like "Test Event", "User1", "a@ex.com". Instead, use full names and descriptive titles that reflect real-world usage.

### Unit Testing

Whenever you add, remove or change any functionality add/remove/change tests as well. Tests are located in the same package as the code they test, with filenames ending in `_test.go`. Run `make test` to run all tests.

### Smoke Testing

Smoke tests verify all CLI commands against a real HEY server. They live in `tests/smoke/` as a separate Go module and use a pre-compiled binary built by `make build`.

**What they test:** Every CLI command and its flags — boxes, box, compose, reply, threads, drafts, calendars, recordings, todo, journal, habit, timetrack, seen/unseen, config, auth, and all output format flags (--json, --quiet, --ids-only, --count, --markdown, --styled, --verbose, --stats). Browser-based cross-verification tests confirm CLI actions are visible in the browser and vice versa.

**Running:**

```bash
make test-smoke   # Builds binary then runs tests (requires dev server)
```

The dev server must be running at `http://app.hey.localhost:3003` (override with `HEY_SMOKE_BASE_URL`). Default login: `david@basecamp.com` / `secret123456` (override with `HEY_SMOKE_EMAIL` and `HEY_SMOKE_PASSWORD`).

**How they work:**

1. `TestMain` in `helpers_test.go` orchestrates setup: finds the binary, checks server reachability, launches headless Chrome via chromedp to log in and extract the `session_token` cookie, then authenticates the CLI with `hey auth login --cookie`.
2. All CLI invocations run in an isolated environment: temp `XDG_CONFIG_HOME`, `HEY_NO_KEYRING=1`, `HEY_BASE_URL` pointing to the dev server.
3. Helper functions (`hey()`, `heyOK()`, `heyJSON()`, `heyFail()`) run the binary and parse output. `dataAs[T]()` generically unmarshals response data.
4. Tests that depend on write operations (compose, todo add, journal write, reply, timetrack start) skip gracefully when the server returns errors, since the SDK's parameter format may not match the server's expectations. Set `HEY_SMOKE_STRICT=1` to turn every such skip into a failure — that is the release gate, where a skipped check is not a passed one.
5. Test data uses `uniqueID()` (nanosecond timestamps) to avoid collisions. Cleanup happens via `t.Cleanup()`.
6. `hey upgrade` is covered only as far as a dev build's `upgrade_required` refusal (and `hey version --json`). The live self-update against a real release runs in `.github/workflows/upgrade-smoke.yml`, never against the dev server.

**How to add a new test:**

1. Create or edit a `*_test.go` file in `tests/smoke/` (package `smoke_test`).
2. Use `heyJSON(t, "command", "args...")` for commands that should succeed and return JSON.
3. Use `heyFail(t, "command", "args...")` for commands that should fail.
4. For write operations that may fail server-side, use `hey(t, ...)` directly and skip on non-zero exit through the strict-aware helper: `if code != 0 { skipf(t, "... (exit %d): %s", code, stderr) }`. Never call `t.Skip` directly — `skipf` is what turns a skip into a failure under `HEY_SMOKE_STRICT=1`.
5. Use `dataAs[T](t, resp)` to unmarshal response data into typed structs.
6. For browser cross-verification, use `browserPageText(t, url)` to get page content.

### Upgrade command

`hey upgrade` (`internal/cmd/upgrade.go`, `upgrade_selfupdate.go`, `release.go`) self-updates
installer/tarball installs under `$HOME` and delegates Homebrew and Scoop installs to their
package manager; system packages, Nix and `go install` builds are refused with guidance, as is
any build whose version is not a semantic version (`dev` included). The native path trusts
nothing it downloaded until the release's `checksums.txt.bundle` verifies with sigstore-go
against the Sigstore public-good trusted root (TUF-cached under `config.CacheDir()/sigstore-tuf`),
with the signing identity pinned to
`https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v<version>` and
the GitHub Actions OIDC issuer. Only then is the archive's SHA-256 checked against the verified
`checksums.txt`, the binary extracted with hardening (no links, no nested paths, size caps),
probed with `--version`, and swapped in behind a `flock` with the old binary preserved until
the installed one is confirmed.

The verification tests in `upgrade_selfupdate_test.go` run hermetically against real release
fixtures in `internal/cmd/testdata/selfupdate/` (see its README for provenance — they are from
basecamp-cli until hey-cli has a signed release of its own). Everything else goes through the
seam variables (`releaseFetcher`, `bundleVerifier`, `binaryVersionProber`, …) so no test talks
to GitHub or runs a package manager.

### Running

To run the CLI use `make build` and then `./bin/hey`. This ensures that you and I are running the same version of the program.

## Code style

See `STYLE.md` for the Go conventions used here. Read it when writing or reviewing Go in
this repo -- it is not imported, so it does not sit in context for sessions that never
touch Go.
