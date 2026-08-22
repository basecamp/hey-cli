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
  - hey accounts
  - hey box
  - hey label
  - hey collection
  - hey workflow
  - hey clip
  - hey snippet
  - hey search
  - hey contacts
  - hey threads
  - hey share
  - hey unshare
  - hey reply
  - hey bulk-reply
  - hey forward
  - hey compose
  - hey drafts
  - hey screener
  - screen a sender
  - approve a sender
  - deny a sender
  # Calendar actions
  - hey calendars
  - hey recordings
  # Todos
  - hey todo
  # Seen/unseen
  - hey seen
  - hey unseen
  - hey move
  - hey trash
  - hey spam
  - hey ignore
  - hey stop-ignoring
  - move email
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

## Agent Invariants

**MUST follow these rules:**

1. **Choose the right structured output** — use `--jq '<expression>'` to filter or extract fields and `--json` for the full response. Never pipe to an external `jq`; `--jq` is built in and implies `--json`.
2. **Authentication required** for all data commands — run `hey auth login` first
3. **HTML output** is available via `--html` for commands that return HTML content
4. **Linked mail accounts share one login** — use `hey accounts list --json`, then `--account <id|all>` when a task must target one account
5. **Local HEY configuration requires human trust** — never run `hey config trust-local` without the user's explicit approval

## Output Filtering

`--jq` filters the full JSON success envelope, so result data is under `.data`. String results print as plain text; objects and arrays print as formatted JSON. Use `--quiet --jq` when the expression should run against result data directly. Errors retain their complete structured envelope. Commands with dedicated raw output (`auth token`, `completion`, `skill`, `tui`, and `--version`) reject `--jq`.

```bash
hey box list --jq '.data[] | {id, name}'
hey search "quarterly planning" --jq '.data[].id'
hey box list --quiet --jq '.[].name'
```

An empty result is an empty array rather than `null`, so `.data[]` is safe to run against a
listing that found nothing.

For the two commonest shapes there is no need for an expression at all: `--ids-only` prints
one ID per line and `--count` prints a bare number, both on stdout with any pagination
notice on stderr. Both need list data, so they work on `hey box list`, `hey box view`,
`hey label list`, `hey label view`, `hey collection list`, `hey collection view`, `hey workflow list`,
`hey workflow view`, `hey clip list`, `hey snippet list`, `hey drafts`, `hey search`,
`hey contacts list`, `hey screener list`, `hey screener history`, `hey calendars`,
`hey recordings`, `hey todo list`, `hey timetrack list`, `hey journal list` and
`hey attachments`. On `hey box view` they count and list its postings, not the box itself.

## Quick Reference

| Task | Command |
|------|---------|
| List linked mail accounts | `hey accounts list --json` |
| Set default mail account | `hey accounts use <id\|all>` |
| Run once for one account | `hey --account <id> box list --json` |
| Review trusted local settings | `hey config trusted-locals --json` |
| Trust this repository's settings | `hey config trust-local` (requires explicit user approval) |
| List mailboxes | `hey box list --json` |
| List emails in a box | `hey box view imbox --json` |
| List labels | `hey label list --json` |
| List emails with a label | `hey label view <label_id> --all --json` |
| Add a label to a thread | `hey label add <id> --to <label_id>` |
| Create and add a label | `hey label create "Travel receipts" <id>` |
| Remove labels | `hey label remove <id> --from <label_id\|all>` |
| List collections | `hey collection list --json` |
| List collection threads | `hey collection view <collection_id> --all --json` |
| Create a collection | `hey collection create "Kitchen remodel"` |
| Update a collection | `hey collection update <collection_id> --name "Kitchen renovation"` |
| Add a thread to a collection | `hey collection add <topic_id> --to <collection_id>` |
| Remove a thread from a collection | `hey collection remove <topic_id> --from <collection_id>` |
| List workflows | `hey workflow list --json` |
| View workflow stages | `hey workflow view <workflow_id> --json` |
| List clips | `hey clip list --json` |
| List snippets | `hey snippet list --json` |
| Search email | `hey search "quarterly planning" --json` |
| List search filters | `hey search filters --json` |
| List contacts | `hey contacts list --json` |
| View contact | `hey contacts show <id> --json` |
| Add contact | `hey contacts add --name "Jane Doe" --email jane@example.com` |
| Edit contact | `hey contacts update <id> --name "Jane Dawson"` |
| Hide contact | `hey contacts hide <id>` |
| Show contact again | `hey contacts show-again <id>` |
| Bundle a contact's mail | `hey contacts bundle <id>` |
| List a contact's mail separately | `hey contacts unbundle <id>` |
| Read private contact note | `hey contacts note show <id> --json` |
| Set private contact note | `hey contacts note set <id> "Prefers email"` |
| Delete private contact note | `hey contacts note delete <id>` |
| Read email thread | `hey threads <topic_id> --json` |
| Get a sharing link | `hey share <thread_id>` |
| Turn off a sharing link | `hey unshare <thread_id>` |
| Reply to email | `hey reply <topic_id> -m "Friday works for me."` |
| Forward email | `hey forward <topic_id> --to alice@example.com -m "For your review"` |
| Compose email | `hey compose --to alice@example.com --subject "Lunch plans" -m "Are you free Friday?"` |
| Compose with CC/BCC | `hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Kitchen remodel timeline"` |
| List drafts | `hey drafts --json` |
| Who is waiting in The Screener | `hey screener list --json` (clearance IDs) |
| Number waiting | `hey screener list --count` |
| Let a sender through | `hey screener approve <clearance_id>` |
| Turn a sender away | `hey screener deny <clearance_id>` |
| Who was already screened | `hey screener history --json` |
| Preview a bulk reply | `hey bulk-reply preview <id> <id> --json` |
| Send a bulk reply | `hey bulk-reply send <id> <id> -m "Thanks for the update."` |
| Recall a bulk reply | `hey bulk-reply undo <delivery_id>` |
| List calendars | `hey calendars --json` |
| List calendar events | `hey recordings 123 --json` |
| List todos | `hey todo list --json` |
| Add todo | `hey todo add "Draft the quarterly report"` |
| Complete todo | `hey todo complete 123` |
| Uncomplete todo | `hey todo uncomplete 123` |
| Delete todo | `hey todo delete 123` |
| Wait for new mail | `hey watch --box imbox --exit-on-first` |
| Follow every change | `hey watch` |
| Mark as seen | `hey seen 12345` |
| Mark as unseen | `hey unseen 12345` |
| Move email threads | `hey move 12345 --to feed` |
| Move email threads to Trash | `hey trash 12345` |
| Mark email threads as spam | `hey spam 12345` |
| Ignore email threads | `hey ignore 12345` |
| Stop ignoring email threads | `hey stop-ignoring 12345` |
| Create habit | `hey habit create "Morning strength training"` |
| Edit habit | `hey habit edit 123 --days mon,wed,fri` |
| Delete habit | `hey habit delete 123` |
| Complete habit | `hey habit complete 123` |
| Uncomplete habit | `hey habit uncomplete 123` |
| Start time tracking | `hey timetrack start` |
| Stop time tracking | `hey timetrack stop` |
| Current timer | `hey timetrack current --json` |
| List time entries | `hey timetrack list --json` |
| Export completed time entries | `hey timetrack export > tracked-time.csv` (`--json` etc. need `--output`) |
| Save a time tracking export | `hey timetrack export --output tracked-time.csv --json` |
| List time track categories | `hey timetrack categories --json` |
| Create time track category | `hey timetrack category create "Client work"` |
| List journal entries | `hey journal list --json` |
| Read journal entry | `hey journal read 2024-03-15 --json` |
| Write journal entry | `hey journal write "Shipped the pagination fix."` (empty content removes the entry) |
| Check auth status | `hey auth status` |
| Print bearer token | `hey auth token` (refuses a `--cookie` login) |
| Launch TUI | `hey tui` (Ctrl+A switches linked mail accounts) |

## Decision Trees

### Reading Email

```
Want to read email?
├── Which mailbox? → hey box list --json
├── List emails in box? → hey box view <name|id> --json
├── List labels or labeled email? → hey label list --json / hey label view <label_id> --json
├── Add, create, or remove a label? → hey label add|create|remove
├── List collections or collection threads? → hey collection list --json / hey collection view <collection_id> --json
├── Create, update, add to, or remove from a collection? → hey collection create|update|add|remove
├── Search threads and messages? → hey search <query> --json
├── Need available refinements? → hey search filters --json
├── List or view contacts? → hey contacts list --json / hey contacts show <id> --json
├── Read full thread? → hey threads <topic_id> --json
├── Get a sharing link? → hey share <thread_id>
├── Turn off the sharing link? → hey unshare <thread_id>
├── Mark as seen? → hey seen <id>
├── Mark as unseen? → hey unseen <id>
├── Move to another box? → hey move <id> --to <box>
├── Move to Trash? → hey trash <id>
├── Mark as spam? → hey spam <id>
├── Ignore future activity? → hey ignore <id>
├── Stop ignoring? → hey stop-ignoring <id>
├── Who is waiting to be screened? → hey screener list --json
├── Screen a sender in or out? → hey screener approve|deny <clearance_id>
└── Launch interactive UI? → hey tui
```

### Sending Email

```
Want to send email?
├── Reply to thread? → hey reply <topic_id> -m "message"
│   ├── Open editor? → hey reply <topic_id> (omit -m to open $EDITOR)
│   └── Attach files? → add --attach ./report.pdf (repeatable)
├── Reply to many threads at once? → hey bulk-reply preview <id>... first, then send
│   └── Sent by mistake? → hey bulk-reply undo <delivery_id> (while the window is open)
├── Forward latest message? → hey forward <topic_id> --to <email>
│   └── Add a note? → add -m "note"
├── Compose new? → hey compose --to <email> --subject "Subject"
│   ├── With body? → hey compose --to <email> --subject "Subject" -m "Body"
│   ├── With files? → add --attach ./report.pdf (repeatable; body is optional)
│   ├── With CC? → add --cc <email>
│   └── With BCC? → add --bcc <email>
├── List files in a thread? → hey attachments <topic_id> --json
│   └── Save one? → hey attachments save <attachment_id> [--output <path>]
└── Check drafts? → hey drafts --json
```

### Managing Todos

```
Want to manage todos?
├── List todos? → hey todo list --json
├── Add todo? → hey todo add "Task description"
├── Complete? → hey todo complete <id>
├── Uncomplete? → hey todo uncomplete <id>
└── Delete? → hey todo delete <id>
```

## Resource Reference

### Email - Boxes

```bash
hey box list --json                          # List all mailboxes
hey box view imbox --json                    # List emails in Imbox (by name)
hey box view 123 --json                      # List emails in box (by ID)
hey box view imbox --page next-cursor --json # Continue from an earlier listing
```

Box names: `imbox`, `feedbox`, `trailbox`, `asidebox`, `laterbox`, `bubblebox`

**Response format:** `hey box view --json` returns the box itself — `id`, `kind`, `name`, `app_url`, `next_history_url`, `next_page` — with a `postings` array of the email threads in it. Each posting has: `id` (box item ID), `topic_id` (thread ID), `name` (subject), `seen` (read status), `created_at`, `contacts`, `summary`, `app_url`, `visible_entry_count`. Use `id` for `hey seen`, `hey unseen`, `hey move`, `hey label add`, `hey label remove`, `hey trash`, `hey spam`, `hey ignore`, and `hey stop-ignoring`, and `topic_id` for `hey threads`, `hey reply`, `hey forward`, `hey share` and `hey attachments`. A box item `id` passed to `hey threads` answers `not_found`, and so does a `topic_id` passed to `hey move`.

`next_page` is the cursor `--page` takes, and it is the cursor inside `next_history_url` — `--page` accepts either. `--all` reads to the end instead.

`--ids-only` and `--count` work here too, and answer for the postings: one box item ID per line, or how many threads were read.

### Email - Labels

```bash
hey label list --json                         # List labels and stable IDs
hey label view 789 --all --json                # List every thread with a label
hey label add 12345 --to 789                   # Add an existing label
hey label create "Travel receipts" 12345       # Create and add a label
hey label remove 12345 --from 789              # Remove one label
hey label remove 12345 --from all              # Remove every label
```

Label mutations take box item IDs from `hey box view`, `hey label view`, or active `hey search` results. Label IDs come from `hey label list`. `hey label view` returns `next_page` and `total_count`; pass `--page <next_page>` to continue or `--all` to fetch every page. HEY creates a label while adding it to at least one thread, so `label create` requires one or more thread item IDs.

### Email - Collections

```bash
hey collection list --json                                 # List collections and stable IDs
hey collection view 321 --all --json                        # List every thread in a collection
hey collection create "Kitchen remodel" --summary "Plans and decisions"
hey collection update 321 --name "Kitchen renovation"
hey collection add 987 --to 321                             # Add a topic ID
hey collection remove 987 --from 321                        # Remove a topic ID
```

Collection IDs come from `hey collection list`. `hey collection view` returns posting `id`, thread `topic_id`, `next_page`, and `total_count`; pass `--page <next_page>` to continue or `--all` to fetch every page. Collection membership commands take `topic_id`. Creating a collection confirms the mutation, and listing collections provides its ID for later commands.

### Email - Search

```bash
hey search "quarterly planning" --json         # Free-text search
hey search --from jane@example.com --date last_30_days --json  # Refined search
hey search --subject invoice --attachment pdfs --all --json    # Search up to 100 pages
hey search filters --json                      # Available box, date, label, and attachment values
```

Search refinements are `--required`, `--any`, `--none`, `--exact`, `--from`, `--to`, `--subject`, `--date`, `--in`, `--label`, and `--attachment`. `--page` selects one result page; `--all` fetches up to 100 pages from that point onward. When the cap is reached, the response notice provides the next `--page` value for continuation.

`--in`, `--date`, `--label` and `--attachment` accept only the values `hey search filters` lists: boxes are `imbox`, `feed`, `papertrail`, `trash`; dates are `last_7_days`, `last_30_days`, `last_90_days` or a four-digit year; attachment kinds are `any`, `images`, `pdfs`, `calendar_invites`, `documents`, `spreadsheets`, `presentations`, `media`, `zip_files`. The kinds are plural — `--attachment pdfs`, not `pdf`. An unrecognized `--in`, `--date` or `--attachment` is refused as a usage error naming the values it accepts, before anything is sent; `--label` is not checked, so read `hey search filters` when unsure of a label.

**Response format:** `data` contains one item per matching thread. Each result has `id` (box item ID for organization actions), `topic_id` (thread ID for `hey threads`, `hey reply`, and `hey forward`), `subject`, `updated_at`, and `messages` containing the matching message IDs, senders, dates, and summaries. A result can omit `id` when the thread has no active box item.

### Contacts

```bash
hey contacts list --json                       # List contacts
hey contacts list --page 2 --json              # List another page
hey contacts show 12345 --json                 # View details, aliases, and private note
hey contacts add --name "Jane Doe" --email jane@example.com
hey contacts add --name "Jane Doe" --email jane@example.com --alias jane.doe@example.org
hey contacts update 12345 --name "Jane Dawson"
hey contacts update 12345 --alias=              # Clear aliases
hey contacts hide 12345                         # Hide from lists and autocomplete
hey contacts show-again 12345                   # Reverse hiding
hey contacts bundle 12345                       # Group this contact's mail into one row
hey contacts unbundle 12345                     # List this contact's mail separately
hey contacts note show 12345 --json
hey contacts note set 12345 "Prefers email"
echo "Multiline private note" | hey contacts note set 12345
hey contacts note delete 12345
```

`hey contacts list` returns contact IDs, names, email addresses, and update timestamps. `hey contacts show` adds aliases, screening status, and the private note. Contact updates preserve omitted fields. Supplying `--alias` replaces the complete alias list, and `--alias=` clears it.

HEY hides contacts instead of permanently deleting them. A hidden contact leaves contact lists, autocomplete, and search results while remaining available by ID; `show-again` reverses the action. Bundling groups a contact's mail into one row without merging or deleting the underlying threads; `unbundle` lists those threads separately again. HEY applies bundling when the contact's current delivery setting supports bundles. Contact notes are private and support positional content, `--note`, stdin, or `$EDITOR`. Deleting a note leaves the contact unchanged.

### Email - Threads

```bash
hey threads <topic_id> --json                 # Read full email thread
hey threads <topic_id> --html                 # Read with raw HTML content
hey share <thread_id>                         # Get a sharing link
hey unshare <thread_id>                       # Turn off the sharing link
```

`hey threads` returns every entry in the thread, oldest first. Each entry's `body` is
**Markdown**, converted from HEY's Trix HTML at the edge, so headings, lists, quotes,
tables and code survive and links keep their URLs — read it as structure rather than as
flattened text. `--html` returns the original HTML instead. There is no `recipients` field
on an entry; use `hey reply`, which works the addressing out itself.

`hey share` returns a URL that shows the entire thread and future emails or replies sent to it. Anyone with the link can open it. `hey unshare` turns off the sharing link.

**ID note:** Every email thread has two IDs: an `id` (its box item ID) and a `topic_id` (its thread ID). `hey seen`, `hey unseen`, `hey move`, `hey label add`, `hey label remove`, `hey trash`, `hey spam`, `hey ignore`, and `hey stop-ignoring` expect `id`. `hey threads`, `hey share`, `hey unshare`, `hey attachments`, `hey reply`, `hey forward`, `hey collection add`, and `hey collection remove` expect `topic_id`. Passing the wrong one answers `not_found`, not a redirect.

`hey box view --json`, `hey label view --json`, `hey collection view --json` and `hey search --json` all carry both.

### Email - Attachments

```bash
hey attachments <topic_id> --json               # List files in every message
hey attachments save 67890:1                    # Save using a returned ID
hey attachments save 67890:1 --output ./reports # Save into a directory
hey attachments save 67890:1 --output ./report.pdf --force
```

An attachment ID combines its message ID and position, so `67890:1` identifies the first attachment in message `67890`. Saving uses the original filename unless `--output` names a destination. Existing files are preserved unless `--force` is set.

### Email - Reply, Forward & Compose

```bash
hey reply <topic_id> -m "Friday works for me — I'll send an agenda."  # Inline message
hey reply <topic_id>                          # Reply via $EDITOR
hey reply <topic_id> -m "Here is the wiring diagram." --attach ./diagram.png
hey forward <topic_id> --to alice@example.com                 # Forward the latest message
hey forward <topic_id> --to alice@example.com -m "Please review before Thursday."
hey compose --to alice@example.com --subject "Lunch plans"    # Body from $EDITOR
hey compose --to alice@example.com --subject "Lunch plans" -m "Are you free Friday?"
hey compose --to alice@example.com --subject "Q3 revenue report" --attach ./report.pdf  # Attachment-only message
hey compose --to alice@example.com --subject "Q3 revenue report" -m "The numbers are attached." --attach ./report.pdf --attach ./chart.png
hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Kitchen remodel timeline" -m "Cabinets land the week of the 14th."
hey compose --thread-id 12345 -m "Confirmed — see you then."  # Reply into an existing thread (no subject: it carries the thread's)
```

`hey reply` answers the thread's **latest** entry. HEY addresses the reply the way its own
web app does: everyone that entry was addressed to, plus whoever wrote it, on the To line.
A reply HEY cannot address is saved as a draft rather than sent, so the command fails
rather than guessing when it cannot work out the recipients.

### Email - The Screener

```bash
hey screener list --json                      # Who is waiting to be screened
hey screener list --count                     # Just the number waiting (cheap)
hey screener approve 91                       # Let a sender through, into the Imbox
hey screener approve 91 --box "The Feed"      # Let them through, into another box
hey screener approve 91 --seen                # Deliver what they sent, already read
hey screener deny 91 92                       # Turn several senders away
hey screener deny 91 --spam                   # Turn away and train the spam filter
hey screener history --json                   # Who has already been decided
hey screener clear                            # Empty the queue without deciding
```

The Screener is where first-time senders wait. `hey screener list` returns **clearance
IDs** — not contact IDs and not posting IDs — with the sender, what they sent, and a
`topic_id` for reading the thread before deciding. `--count` is a far cheaper request than
the queue and prints a bare number.

Approving delivers everything that sender has waiting; denying hides it. Either is
reversible with the opposite command. `--box` and `--seen` apply to one sender at a time;
several IDs go through HEY's bulk endpoint, which takes neither. `--spam` also trains HEY's
filter, which is harder to undo than a plain deny. `hey screener clear` decides nothing —
those senders are asked about again on their next email.

### Email - Bulk reply

```bash
hey bulk-reply preview 12345 67890 --json     # Read-only: threads and exact recipients
hey bulk-reply send 12345 67890 -m "Thanks for the update — noted."
hey bulk-reply undo 98765                     # Recall a delayed bulk reply
```

Takes posting IDs, which must be positive and unique. **Always run `preview` first** — it
resolves each posting to the entry a reply would answer and shows the exact To/CC/BCC, so
the blast radius is visible before anything sends. `send` resolves the selection again and
skips threads with no replyable entry, then returns the reply count, delivery ID, delayed
state, undo URL and undo command. `undo` works only while HEY's undo window is open.

### Email - Seen/Unseen

```bash
hey seen 12345                                # Mark a thread as seen
hey seen 12345 67890                          # Mark multiple threads as seen
hey unseen 12345                              # Mark a thread as unseen
hey unseen 12345 67890                        # Mark multiple threads as unseen
```

Takes box item IDs (the `id` field from `hey box view` output).

### Email - Moving Threads

```bash
hey move 12345 --to imbox                     # Move one thread
hey move 12345 67890 --to "paper trail"       # Move multiple threads
```

Takes box item IDs (the `id` field from `hey box view --json`). `--to` accepts a box name, kind, or ID. Supported destinations are Imbox, The Feed, Set Aside, Reply Later, and Paper Trail. Bubble Up requires a scheduled date and is not supported by this command.

### Email - Trash and Spam

```bash
hey trash 12345                               # Move one thread to Trash
hey trash 12345 67890                         # Move multiple threads to Trash
hey spam 12345                                # Mark one thread as spam
hey spam 12345 67890                          # Mark multiple threads as spam
```

Takes box item IDs (the `id` field from `hey box view --json`). Trashing a shared thread removes your access instead of deleting it for everyone. Marking a thread as spam moves it to Spam and trains HEY's filters.

### Email - Ignoring Threads

```bash
hey ignore 12345                              # Ignore one thread
hey ignore 12345 67890                        # Ignore multiple threads
hey stop-ignoring 12345                       # Stop ignoring one thread
hey stop-ignoring 12345 67890                 # Stop ignoring multiple threads
```

Takes box item IDs (the `id` field from `hey box view --json`). Ignored threads remain in their box; new replies do not bring them back to your attention. `hey stop-ignoring` reverses the action.

### Email - Watching for changes

```bash
hey watch                         # Follow every box until interrupted
hey watch --box imbox             # Report one box's changes (repeatable, by name or ID); every box is followed
hey watch --events added,deleted  # Only these changes (added, updated, deleted, new, resync)
hey watch --box imbox --events new  # New mail only: unseen, unmuted, active since the watch began
hey watch --box imbox --events new --exit-on-first  # Block until new mail lands, print it, exit
hey watch --exit-on-first         # Wait for one change of any kind, print it, exit
hey watch --timeout 30m           # Give up waiting after a while
hey watch --since 2026-03-15      # Report changes since then first, then follow
hey watch --run-sync ./triage.sh  # Run a command per change instead of printing
```

Long-running, and driven by a websocket rather than polling — never poll `hey box` in a
loop when this will do. Writes one JSON object per changed posting to stdout, one per
line, instead of the usual envelope: `{"change": "added", "at": ..., "box": {"id", "kind",
"name"}, "posting_id": ..., "thread_id": ..., "new": true|false, "posting": {...}}`. Use
`thread_id` with `hey threads`. `new` is on every `added` and `updated` line and says whether
the posting is new mail — unseen, not muted, and active since the watch last saw the thread,
or since the watch began for a thread it has not seen; the backlog a watch starts with is
never new, nor is reading, muting or moving a thread, and a reply on a known thread is.
`--events new` selects the new ones, alone or in a union with the other three. A deleted
posting carries no `posting`, `thread_id` or `new`. Three more lines
describe the watch itself: `{"change": "ready"}` once every box is caught up and the subscription
is live (again after every reconnect's catch-up), `{"change": "disconnected"}` when the
connection drops, and `{"change": "resync", "box": {...}}` when a box changed more than the
feed can list and the watch skipped ahead — re-read that box. A resync is an event of its
own: reported by default (`--run-*` scripts run for it, `--exit-on-first` counts it) and left
out by `--events new`, so a script for new mail never runs on one; `ready` and `disconnected`
are written to stdout only and carry no `box`.

To drive a command per change, choose one of two behaviours — passing both is an error.
`--run-async` spawns the command and moves on, so a slow one never holds up the watch and
two can overlap; `--run-sync` waits for each and runs them in order. Both get the JSON on
stdin and the fields as `HEY_CHANGE`, `HEY_AT`, `HEY_BOX_ID`, `HEY_BOX_KIND`,
`HEY_BOX_NAME`, `HEY_POSTING_ID` and `HEY_THREAD_ID` — plus `HEY_NEW=1` for new mail, `HEY_NEW=0`
otherwise — and both take over stdout.

### Drafts

```bash
hey drafts --json                             # List drafts
```

### Calendars

```bash
hey calendars --json                          # List calendars (returns array of {id, name, kind})
hey recordings 123 --json                     # List events in calendar, from today onward
hey recordings 123 --starts-on 2026-01-01 --ends-on 2026-01-31 --json
```

`--starts-on` defaults to today, `--ends-on` to thirty days after it. Both want
`YYYY-MM-DD`; an unreadable date, or an `--ends-on` before `--starts-on`, is a usage error
rather than an empty result.

**Response format:** `hey recordings` returns recordings grouped by type (e.g. `{"Calendar::Event": [...], "Calendar::Habit": [...], "Calendar::Todo": [...]}`). Each recording has: `id`, `title`, `starts_at`, `ends_at`, `all_day`, `recurring`, `starts_at_time_zone`. Access a type with the built-in filter, e.g. `hey recordings 123 --quiet --jq '.["Calendar::Event"]'`. `--count` and `--ids-only` read across every type, with the types in name order.

### Todos

```bash
hey todo list --json                          # List all todos
hey todo add "Draft the quarterly report"     # Add a todo
hey todo add "Book the venue" --date 2026-09-04  # With a due date
hey todo complete 123                         # Mark complete
hey todo uncomplete 123                       # Mark incomplete
hey todo delete 123                           # Delete a todo
```

Todo IDs must be positive; `hey todo complete 0` or a negative ID is a usage error rather
than a request. `--date` wants `YYYY-MM-DD` and is validated before the request.

### Habits

```bash
hey habit create "Morning strength training" # Create with weights, blue, every day
hey habit create "Practice piano" --icon music --color green --days mon,wed,fri
hey habit edit 123 --name "Evening walk"      # Omitted fields remain unchanged
hey habit edit 123 --days 0,6                 # Sunday and Saturday
hey habit delete 123                          # Permanently delete habit and history
hey habit complete 123                        # Mark habit complete for today
hey habit complete 123 --date 2026-03-15      # Mark complete for specific date
hey habit uncomplete 123                      # Unmark habit for today
```

Habit IDs can be found via `hey recordings <calendar-id> --json`. Days accept full weekday names, common abbreviations, or `0` (Sunday) through `6` (Saturday).

### Time Tracking

```bash
hey timetrack start                           # Start timer
hey timetrack stop                            # Stop timer
hey timetrack current --json                  # Show current timer
hey timetrack list --json                     # List time entries
hey timetrack export > tracked-time.csv        # Write the complete CSV export
hey timetrack export -o tracked-time.csv --json # Save the CSV, return file metadata
hey timetrack categories --json               # List categories
hey timetrack category create "Client work"   # Create a category
hey timetrack category rename 123 "Planning"  # Rename a category
hey timetrack category delete 123              # Delete a category
```

Without `--output`, `hey timetrack export` writes CSV to stdout — redirect it to a file.
The output formatting flags cannot reshape a CSV, so `--json`, `--quiet`, `--markdown`,
`--ids-only`, `--count` and `--html` are refused with a usage error unless `--output` is
given, which returns file metadata for them to format.

### Journal

```bash
hey journal list --json                       # List journal entries
hey journal read 2026-03-15 --json            # Read entry by date
hey journal write "Shipped the pagination fix and paired with Jane on the cover art."
hey journal write 2026-03-15 "Retrospective: the migration took two days longer than planned."
hey journal write                             # Write entry via $EDITOR
```

Writing empty content **removes** the day's entry, and the command says "removed" rather
than "saved" — so never pass an empty string, and never save an empty `$EDITOR` buffer
unless removal is the intent. A day with no entry reads back as empty content, not an
error.

### Authentication

```bash
hey auth login                                # Log in (browser-based OAuth)
hey auth status                               # Check if authenticated
hey auth logout                               # Log out
hey login / hey logout                        # Shortcuts for the two above
hey setup                                     # First-run wizard: sign in + connect coding agents
HEY_NONINTERACTIVE=1 hey setup --json         # No prompts and no OAuth wait — but still
                                              # installs agent skills and records onboarding;
                                              # use `hey doctor` to inspect without changes.
                                              # (Without HEY_NONINTERACTIVE, a terminal on
                                              # stdin still starts browser sign-in.)
```

If a command fails with an auth error, run `hey auth status` to check, then `hey auth login` to re-authenticate.
