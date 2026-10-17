---
name: hey
description: |
  Interact with HEY via the HEY CLI. Read and send emails, manage contacts,
  boxes, labels, collections, calendars, todos, habits, time tracking, and journal entries. Use for ANY
  HEY-related question or action.
triggers:
  # Direct invocations
  - hey
  - /hey
  # Email actions
  - hey account
  - hey box
  - hey label
  - hey collection
  - hey set-aside
  - hey workflow
  - hey clip
  - hey snippet
  - hey search
  - hey contact
  - hey bundle
  - hey thread read
  - hey thread update
  - rename a thread
  - hey attachment
  - hey share
  - hey unshare
  - hey reply
  - hey bulk-reply
  - hey forward
  - hey compose
  - hey draft list
  - hey draft show
  - hey draft edit
  - hey draft send
  - hey draft delete
  - hey screener
  - hey watch
  - screen a sender
  - approve a sender
  - deny a sender
  # Calendar actions
  - hey calendar list
  - hey event
  # Todos
  - hey todo
  # Seen/unseen
  - hey seen
  - hey unseen
  - hey move
  - hey bubble
  - hey trash
  - hey spam
  - hey ignore
  - hey stop-ignoring
  - move email
  - bubble a thread up
  - trash email
  - mark as spam
  - ignore email thread
  - stop ignoring email thread
  - mark as read
  - mark as seen
  - mark as unseen
  - mark as unread
  # Habits
  - hey habit
  # Time tracking
  - hey timetrack
  # Journal
  - hey journal
  # Auth
  - hey auth
  # Common actions
  - check my email
  - read email
  - send email
  - reply to email
  - forward email
  - share email thread
  - turn off sharing link
  - compose email
  - list mailboxes
  - search email
  - find email
  - list contacts
  - add contact
  - edit contact
  - hide contact
  - bundle contact mail
  - unbundle contact mail
  - contact note
  - contact photo
  - find a contact by email
  - check calendar
  - add todo
  - complete todo
  - track time
  - write journal
  # Questions
  - can I hey
  - how do I hey
  - what's in hey
  - what hey
  - does hey
  # My work
  - my emails
  - my inbox
  - my imbox
  - my todos
  - my calendar
  - my journal
  # URLs
  - hey.com
invocable: true
argument-hint: "[command] [args...]"
---

# /hey - HEY Email Workflow Command

CLI for HEY: mailboxes, labels, collections, email threads, contacts, replies, compose, calendars, todos, habits, time tracking, and journal entries.

Use the installed CLI as the command reference. This skill holds the rules that span commands; it does not copy the command catalog, because the binary's own help is the source of truth and always matches the version you are running.

## Discovery-first workflow

For each task:

1. Infer the deepest plausible leaf command from the user's words.
2. Unless its arguments and flags are already established by a tool result in this conversation, read that leaf's help before running it:

   ```bash
   hey <group> <subcommand> --agent --help
   ```

3. If the leaf does not exist, read its nearest parent group. Use root help only when the top-level group itself is unclear:

   ```bash
   hey <group> --agent --help   # fallback
   hey --agent --help           # last resort
   hey commands --json          # every command with its agent_notes
   ```

4. Treat the leaf's `short`, `long`, `agent_notes` and `flags` as the source of truth. Do not guess positional arguments, flags, accepted values, or limits.
5. Run with an explicit output mode, then read structured errors before deciding what to do next.

Leaf help is cheap; root help and `hey commands --json` print the whole catalog and are not. Global flags (`--json`, `--jq`, `--quiet`, `--account`, `--stats`) apply wherever a command supports them.

## Non-negotiable rules

1. **Choose the right structured output.** Use `--jq '<expression>'` to filter or extract fields and `--json` for the full response. Never pipe to an external `jq`; `--jq` is built in and implies `--json`.
2. **Reuse stored authentication.** Run the requested data command; it uses stored credentials and refreshes expiring OAuth tokens automatically. If it returns an auth error, follow the retry in [Authentication](#authentication) before you report the task as blocked. Use `hey auth status --json` when an explicit authentication check is needed (it reports stored credentials and does not contact HEY). Never run `hey auth login` unattended; use it only for interactive recovery with the user present.
3. **Linked mail accounts share one login.** Use `hey account list --json`, then `--account <id|all>` when a task must target one account.
4. **Local HEY configuration requires human trust.** Never run `hey config trust-local` without the user's explicit approval.
5. **Never resend a send that failed with `not_delivered`.** See [Sending](#sending).
6. **Confirm box item mutations.** See [IDs](#ids): HEY does not report IDs that matched nothing.

## Output

`--jq` filters the full JSON success envelope, so result data is under `.data`. String results print as plain text; objects and arrays print as formatted JSON. Use `--quiet --jq` when the expression should run against result data directly. Errors keep their complete structured envelope. Commands with raw output of their own (`auth token`, `shell-completion generate`, `setup`, `skill`, `tui`, `--version`, and `timetrack export` without `--output`) reject `--jq`. `hey watch` writes one raw JSON event per line with no envelope and does not apply `--jq`.

```bash
hey box list --jq '.data[] | {id, name}'
hey search "quarterly planning" --jq '.data[].topic_id'
hey box list --quiet --jq '.[].name'
```

- An empty result is an empty array rather than `null`, so `.data[]` is safe on a listing that found nothing.
- A field HEY's answer leaves out is left out, never guessed. A posting's `seen` is present only when true, so test `.seen != true` for unseen.
- `--ids-only` prints one ID per line and `--count` a bare number. They work on listings: every `list` command, the thread listings, `hey workflow view`, `hey search`, `hey screener history`, `hey event day|week`, `hey timetrack categories`, `hey attachment list` and `hey thread read` (its entries, read from the index without bodies). Both answer for what was read, which is the first page unless `--all` or `--limit` reads more, and not every listing warns that more pages exist; `hey screener list --count` is the exception and counts the whole queue. On thread listings they count the postings, not the box, label or contact around them.
- `--all` stops at a page cap, which each listing's help names. A capped read says where to continue: `next_page` for a cursor listing such as `hey box view`, or the `notice`, naming the `--page` value, for a numbered one such as `hey search` or `hey contact list`. Continue from there rather than treating the list as complete.
- `--html` writes the original HTML of `hey thread read`, `hey journal read` and a contact's private note to a pipe or file; a terminal is refused.

## IDs

Every email thread has two IDs: `id`, its **box item ID** (one posting in one box), and `topic_id`, its **thread ID**. Each posting in a thread listing has `id`, `topic_id`, `kind` (`bundle` for a bundle row), `name` (subject), `created_at` and `app_url`; a thread row also has `contacts`, `summary` and `visible_entry_count`.

- **Box item ID** (`id`): `hey seen`, `unseen`, `move`, `label add|remove|create`, `trash`, `spam`, `ignore`, `stop-ignoring`, `bubble up|pop`, `set-aside group`, `bulk-reply` and `bundle view`.
- **Thread ID** (`topic_id`): `hey thread read|update`, `share`, `unshare`, `attachment list`, `reply`, `forward`, `compose --thread-id`, `collection add|remove` and `workflow add|move|remove`.
- **Entry ID**: an entry's `id` from `hey thread read`; `hey clip create` takes it, and the passage must appear in that message's text.
- **Contact ID** from `hey contact list` or contact data, **clearance ID** from `hey screener list` (which also gives the `topic_id` of the thread to read before deciding), **box ID** from `hey box list`, **label ID** from `hey label list`. Never substitute one kind for another, or an email address for a contact ID.

Thread listings (`hey box view`, `label view`, `collection view`, `bundle view`, `contact threads`, `search`) carry both IDs, with two exceptions: a bundle row can omit `topic_id`, and a search result omits `id` when you have no box item for its thread.

Passing the wrong kind is not redirected. **Unmatched IDs are not reported**: HEY checks only that *some* box item ID in a call is yours, and changes every one that matches. `hey trash`, `spam`, `label add|remove|create` and `bubble up|pop` answer `not_found` only when none match; `hey seen`, `unseen`, `move`, `ignore`, `stop-ignoring` and `set-aside group create|add|remove` never check (`group create` with no matching ID leaves an empty group). So a mixed batch is a partial success reported as a whole one, a typo is silently skipped, and a `topic_id` that happens to equal one of your box item IDs acts on an unrelated thread; likewise a box item ID given to `hey thread read` can read an unrelated thread. Confirm rather than trust the envelope:

```bash
hey box view imbox --json --all --jq '{notice, next_page: .data.next_page, match: [.data.postings[] | select(.id == 12345) | {id, topic_id, seen}]}'
```

for a mark, or read the destination box for a move. An empty `match` with `next_page` still set means the box is larger than `--all` reads: continue with `--page <next_page>` rather than calling it a non-match.

**Bundle rows.** A posting whose `kind` is `bundle` rolls one sender's mail into one row and names the sender, not a thread. It carries `topic_id` only while it holds exactly one unseen thread. Never pass a bundle row's `id` to `hey thread read`. `hey bundle view <id>` lists its unseen threads with their `topic_id`; `hey contact threads <contact_id>` lists every thread with that sender, seen or not, which is the only place a bundle's mail is listed once read through. Change grouping with `hey contact bundle|unbundle <contact_id>`.

## Sending

- A send (`compose`, `reply`, `forward`, `draft send`) answers the new message's `id` and the `topic_id` of the thread it landed on; read it with `hey thread read <topic_id>`.
- `delayed: true` means Undo Send is still holding it, and HEY leaves it out of its thread until it goes out: `hey thread read` shows the thread without it, or answers `not_found` for a thread it started. Wait and read again rather than treating the send as lost.
- A send HEY refused (usually the account's sending limit) fails with `not_delivered` (exit 7). HEY kept it as a draft, named by `draft_id` in the error; `hey draft send <draft_id>` sends it later. Do not resend the message; that makes a second draft.
- To let a person review before anything goes out, add `--draft` to `compose` or `reply`; the answer carries the draft ID for `hey draft show|edit|send|delete`. The person reviews and sends it from any HEY app, or you send it later with `hey draft send`.
- `hey bulk-reply`: run `preview` before `send`. A delivery sent by mistake can be recalled with `hey bulk-reply undo <delivery_id>` while the window is open.

## Writing content

Message bodies, drafts, journal entries, snippets and contact notes are Markdown by default (`-m`, `--content`, `--note`, positional content, stdin and `$EDITOR` alike) and are converted to rich text on the way out; clip passages, event notes and time track notes are plain text. Each Markdown flag has an HTML twin (such as `--message-html`, `--note-html`, `--content-html`) for raw HTML, and each pair is mutually exclusive. A fenced code block's language survives for the languages HEY highlights (Ruby, Python, JavaScript, TypeScript, Go, Rust, Java, C#, C++, PHP, Swift, HTML and CSS); any other is dropped.

Everything a calendar holds is a recording, and each kind has its own command: `hey event`, `hey todo`, `hey journal`, `hey habit`, `hey timetrack`. Without a date, those commands read the account's time zone to find today; name the date to skip the account read.

## Adding to something written whole

`hey contact note set` and `hey journal write` replace the whole note or entry; there is no append. To add, read it, change it, and write all of it back. Keep the `&&`: a failed read must not go on to write. The read and the write are not atomic, so write straight after reading.

**Contact note.** When `note_markdown_lossless` is `true`, `note_markdown` holds the whole note; the read checks the flag itself and fails when it is `false`, so nothing is written:

```bash
note=$(hey contact note show 12345 --jq 'if .data.note_markdown_lossless then .data.note_markdown else error("note_markdown would drop part of this note: change note_html with --note-html") end') &&
  printf '%s\n\nMoved to the Lisbon office in March.\n' "$note" | hey contact note set 12345
```

When it is `false`, the note holds markup Markdown cannot carry. Add to the HTML instead:

```bash
note=$(hey contact note show 12345 --jq '.data.note_html') &&
  hey contact note set 12345 --note-html "$note<p>Moved to the Lisbon office in March.</p>"
```

**Journal entry.** The same, with `content_markdown_lossless`; the read also fails for a day with no entry:

```bash
entry=$(hey journal read 2026-03-15 --jq 'if .data.content_markdown_lossless then .data.content_markdown else error("content_markdown would drop part of this entry, or there is none: change content with --content-html") end') &&
  printf '%s\n\nBooked the venue for the second day.\n' "$entry" | hey journal write 2026-03-15
```

```bash
entry=$(hey journal read 2026-03-15 --jq '.data.content // error("no journal entry for this day")') &&
  hey journal write 2026-03-15 --content-html "$entry<p>Booked the venue for the second day.</p>"
```

## Authentication

Data commands use the credentials HEY already stores and refresh expiring OAuth tokens automatically. Run the requested data command without a login preflight.

If a data command returns exit code 3 with `"code": "auth"`, or fails with `could not read stored credentials`, authentication is unavailable to that process. On macOS under Codex, the sandbox can hide credentials that the HEY CLI stores in Keychain. Before reporting the task as blocked, use the harness's normal approval flow to retry `hey auth status --json` once with elevated sandbox permission. Limit the escalation to this one read-only command. If the response's `data.authenticated` is `true`, rerun only the exact `hey` command the user requested through a separate one-command approval. If `data.authenticated` is `false` or the status command fails, report the task as blocked and tell the user to run `hey auth login`; do not run it for them unattended.

Never run the macOS `security` command to read Keychain contents, print or copy credentials, or move credentials into a file; never disable the sandbox globally; never set `HEY_NO_KEYRING=1` as an authentication workaround.

Piped, machine-output and non-TTY commands do not prompt for sign-in. When an agent harness runs commands under a PTY, set `HEY_NONINTERACTIVE=1` so a missing login returns the same actionable auth error instead of opening an interactive prompt.
