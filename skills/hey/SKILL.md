---
name: hey
description: |
  Interact with HEY via the HEY CLI. Read and send emails, manage contacts,
  boxes, labels, calendars, todos, habits, time tracking, and journal entries. Use for ANY
  HEY-related question or action.
triggers:
  # Direct invocations
  - hey
  - /hey
  # Email actions
  - hey accounts
  - hey boxes
  - hey box
  - hey labels
  - hey label
  - hey search
  - hey contacts
  - hey threads
  - hey reply
  - hey forward
  - hey compose
  - hey drafts
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

CLI for HEY: mailboxes, labels, email threads, contacts, replies, compose, calendars, todos, habits, time tracking, and journal entries.

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
hey boxes --jq '.data[] | {id, name}'
hey search "quarterly planning" --jq '.data[].id'
hey boxes --quiet --jq '.[].name'
```

## Quick Reference

| Task | Command |
|------|---------|
| List linked mail accounts | `hey accounts list --json` |
| Set default mail account | `hey accounts use <id\|all>` |
| Run once for one account | `hey --account <id> boxes --json` |
| Review trusted local settings | `hey config trusted-locals --json` |
| Trust this repository's settings | `hey config trust-local` (requires explicit user approval) |
| List mailboxes | `hey boxes --json` |
| List emails in a box | `hey box imbox --json` |
| List labels | `hey labels --json` |
| List emails with a label | `hey label <label_id> --all --json` |
| Add a label to a thread | `hey label add <id> --to <label_id>` |
| Create and add a label | `hey label create "Travel receipts" <id>` |
| Remove labels | `hey label remove <id> --from <label_id\|all>` |
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
| Reply to email | `hey reply <topic_id> -m "Thanks!"` |
| Forward email | `hey forward <topic_id> --to alice@example.com -m "For your review"` |
| Compose email | `hey compose --to user@example.com --subject "Hello"` |
| Compose with CC/BCC | `hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Hello"` |
| List drafts | `hey drafts --json` |
| List calendars | `hey calendars --json` |
| List calendar events | `hey recordings 123 --json` |
| List todos | `hey todo list --json` |
| Add todo | `hey todo add "Buy milk"` |
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
| Complete habit | `hey habit complete 123` |
| Uncomplete habit | `hey habit uncomplete 123` |
| Start time tracking | `hey timetrack start` |
| Stop time tracking | `hey timetrack stop` |
| Current timer | `hey timetrack current --json` |
| List time entries | `hey timetrack list --json` |
| List journal entries | `hey journal list --json` |
| Read journal entry | `hey journal read 2024-03-15 --json` |
| Write journal entry | `hey journal write "Today was great"` |
| Check auth status | `hey auth status` |
| Print access token | `hey auth token` |
| Launch TUI | `hey` (Ctrl+A switches linked mail accounts) |

## Decision Trees

### Reading Email

```
Want to read email?
├── Which mailbox? → hey boxes --json
├── List emails in box? → hey box <name|id> --json
├── List labels or labeled email? → hey labels --json / hey label <label_id> --json
├── Add, create, or remove a label? → hey label add|create|remove
├── Search threads and messages? → hey search <query> --json
├── Need available refinements? → hey search filters --json
├── List or view contacts? → hey contacts list --json / hey contacts show <id> --json
├── Read full thread? → hey threads <topic_id> --json
├── Mark as seen? → hey seen <id>
├── Mark as unseen? → hey unseen <id>
├── Move to another box? → hey move <id> --to <box>
├── Move to Trash? → hey trash <id>
├── Mark as spam? → hey spam <id>
├── Ignore future activity? → hey ignore <id>
├── Stop ignoring? → hey stop-ignoring <id>
└── Launch interactive UI? → hey (no args, launches TUI)
```

### Sending Email

```
Want to send email?
├── Reply to thread? → hey reply <topic_id> -m "message"
│   ├── Open editor? → hey reply <topic_id> (omit -m to open $EDITOR)
│   └── Attach files? → add --attach ./report.pdf (repeatable)
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
hey boxes --json                              # List all mailboxes
hey box imbox --json                          # List emails in Imbox (by name)
hey box 123 --json                            # List emails in box (by ID)
```

Box names: `imbox`, `feedbox`, `trailbox`, `asidebox`, `laterbox`, `bubblebox`

**Response format:** `hey box` returns `{"box": {...}, "postings": [...]}`. The `postings` array is the API representation of the email threads in that box. Each item has: `id` (box item ID), `topic_id` (thread ID), `name` (subject), `seen` (read status), `created_at`, `contacts`, `summary`, `app_url`. Use `id` for `hey seen`, `hey unseen`, `hey move`, `hey label add`, `hey label remove`, `hey trash`, `hey spam`, `hey ignore`, and `hey stop-ignoring`. Use `topic_id` for `hey threads`, `hey reply`, and `hey forward`.

### Email - Labels

```bash
hey labels --json                              # List labels and stable IDs
hey label 789 --all --json                     # List every thread with a label
hey label add 12345 --to 789                   # Add an existing label
hey label create "Travel receipts" 12345       # Create and add a label
hey label remove 12345 --from 789              # Remove one label
hey label remove 12345 --from all              # Remove every label
```

Label mutations take box item IDs from `hey box`, `hey label`, or active `hey search` results. Label IDs come from `hey labels`. `hey label` returns `next_page` and `total_count`; pass `--page <next_page>` to continue or `--all` to fetch every page. HEY creates a label while adding it to at least one thread, so `label create` requires one or more thread item IDs.

### Email - Search

```bash
hey search "quarterly planning" --json         # Free-text search
hey search --from jane@example.com --date last_30_days --json  # Refined search
hey search --subject invoice --attachment pdf --all --json     # Search up to 100 pages
hey search filters --json                      # Available box, date, label, and attachment values
```

Search refinements are `--required`, `--any`, `--none`, `--exact`, `--from`, `--to`, `--subject`, `--date`, `--in`, `--label`, and `--attachment`. `--page` selects one result page; `--all` fetches up to 100 pages from that point onward. When the cap is reached, the response notice provides the next `--page` value for continuation.

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
```

**ID note:** Every email thread returned by `hey box` or `hey label` has an `id` (its box item ID) and a `topic_id` (its thread ID). `hey seen`, `hey unseen`, `hey move`, `hey label add`, `hey label remove`, `hey trash`, `hey spam`, `hey ignore`, and `hey stop-ignoring` expect `id`. `hey threads`, `hey attachments`, `hey reply`, and `hey forward` expect `topic_id`. The `app_url` field also contains the thread ID as a fallback (e.g. `https://app.hey.com/topics/123` → `123`).

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
hey reply <topic_id> -m "Thanks!"             # Reply with inline message
hey reply <topic_id>                          # Reply via $EDITOR
hey reply <topic_id> -m "Attached." --attach ./diagram.png
hey forward <topic_id> --to alice@example.com                 # Forward the latest message
hey forward <topic_id> --to alice@example.com -m "Please review"  # Forward with a note
hey compose --to user@example.com --subject "Hello"         # Compose new (opens $EDITOR)
hey compose --to user@example.com --subject "Hi" -m "Body"  # With inline body
hey compose --to user@example.com --subject "Report" --attach ./report.pdf  # Attachment-only message
hey compose --to user@example.com --subject "Report" -m "Attached." --attach ./report.pdf --attach ./chart.png
hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Project update" -m "Body"  # With CC/BCC
hey compose --thread-id 12345 -m "msg"                       # Reply to an existing thread (no subject: it carries the thread's)
```

### Email - Seen/Unseen

```bash
hey seen 12345                                # Mark a thread as seen
hey seen 12345 67890                          # Mark multiple threads as seen
hey unseen 12345                              # Mark a thread as unseen
hey unseen 12345 67890                        # Mark multiple threads as unseen
```

Takes box item IDs (the `id` field from `hey box` output).

### Email - Moving Threads

```bash
hey move 12345 --to imbox                     # Move one thread
hey move 12345 67890 --to "paper trail"       # Move multiple threads
```

Takes box item IDs (the `id` field from `hey box --json`). `--to` accepts a box name, kind, or ID. Supported destinations are Imbox, The Feed, Set Aside, Reply Later, and Paper Trail. Bubble Up requires a scheduled date and is not supported by this command.

### Email - Trash and Spam

```bash
hey trash 12345                               # Move one thread to Trash
hey trash 12345 67890                         # Move multiple threads to Trash
hey spam 12345                                # Mark one thread as spam
hey spam 12345 67890                          # Mark multiple threads as spam
```

Takes box item IDs (the `id` field from `hey box --json`). Trashing a shared thread removes your access instead of deleting it for everyone. Marking a thread as spam moves it to Spam and trains HEY's filters.

### Email - Ignoring Threads

```bash
hey ignore 12345                              # Ignore one thread
hey ignore 12345 67890                        # Ignore multiple threads
hey stop-ignoring 12345                       # Stop ignoring one thread
hey stop-ignoring 12345 67890                 # Stop ignoring multiple threads
```

Takes box item IDs (the `id` field from `hey box --json`). Ignored threads remain in their box; new replies do not bring them back to your attention. `hey stop-ignoring` reverses the action.

### Email - Watching for changes

```bash
hey watch                         # Follow every box until interrupted
hey watch --box imbox             # Follow one box (repeatable, by name or ID)
hey watch --events added,deleted  # Only these changes (added, updated, deleted)
hey watch --exit-on-first         # Wait for one change, print it, exit
hey watch --timeout 30m           # Give up waiting after a while
hey watch --since 2026-03-15      # Report changes since then first, then follow
hey watch --run-sync ./triage.sh  # Run a command per change instead of printing
```

Long-running, and driven by a websocket rather than polling — never poll `hey box` in a
loop when this will do. Writes one JSON object per changed posting to stdout, one per
line, instead of the usual envelope: `{"change": "added", "at": ..., "box": {"id", "kind",
"name"}, "posting_id": ..., "thread_id": ..., "posting": {...}}`. Use `thread_id` with
`hey threads`. A deleted posting carries no `posting` or `thread_id`.

To drive a command per change, choose one of two behaviours — passing both is an error.
`--run-async` spawns the command and moves on, so a slow one never holds up the watch and
two can overlap; `--run-sync` waits for each and runs them in order. Both get the JSON on
stdin and the fields as `HEY_CHANGE`, `HEY_AT`, `HEY_BOX_ID`, `HEY_BOX_KIND`,
`HEY_BOX_NAME`, `HEY_POSTING_ID` and `HEY_THREAD_ID`, and both take over stdout.

### Drafts

```bash
hey drafts --json                             # List drafts
```

### Calendars

```bash
hey calendars --json                          # List calendars (returns array of {id, name, kind})
hey recordings 123 --json                     # List events in calendar
```

**Response format:** `hey recordings` returns recordings grouped by type (e.g. `{"Calendar::Event": [...], "Calendar::Habit": [...], "Calendar::Todo": [...]}`). Each recording has: `id`, `title`, `starts_at`, `ends_at`, `all_day`, `recurring`, `starts_at_time_zone`. Access a type with the built-in filter, e.g. `hey recordings 123 --quiet --jq '.["Calendar::Event"]'`.

### Todos

```bash
hey todo list --json                          # List all todos
hey todo add "Task description"                        # Add a todo
hey todo complete 123                         # Mark complete
hey todo uncomplete 123                       # Mark incomplete
hey todo delete 123                           # Delete a todo
```

### Habits

```bash
hey habit complete 123                        # Mark habit complete for today
hey habit complete 123 --date 2024-01-15      # Mark complete for specific date
hey habit uncomplete 123                      # Unmark habit for today
```

Habit IDs can be found via `hey recordings <calendar-id> --json`.

### Time Tracking

```bash
hey timetrack start                           # Start timer
hey timetrack stop                            # Stop timer
hey timetrack current --json                  # Show current timer
hey timetrack list --json                     # List time entries
```

### Journal

```bash
hey journal list --json                       # List journal entries
hey journal read 2024-03-15 --json            # Read entry by date
hey journal write "Today's entry"                     # Write entry inline
hey journal write                             # Write entry via $EDITOR
```

### Authentication

```bash
hey auth login                                # Log in (browser-based OAuth)
hey auth status                               # Check if authenticated
hey auth logout                               # Log out
```

If a command fails with an auth error, run `hey auth status` to check, then `hey auth login` to re-authenticate.
