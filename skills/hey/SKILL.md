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

## Agent Invariants

**MUST follow these rules:**

1. **Choose the right structured output** — use `--jq '<expression>'` to filter or extract fields and `--json` for the full response. Never pipe to an external `jq`; `--jq` is built in and implies `--json`.
2. **Reuse stored authentication** — run the requested data command; it uses stored credentials and refreshes expiring OAuth tokens automatically. If it returns an auth error, follow the one-command macOS/Codex retry in [Authentication](#authentication) before you report the task as blocked. Use `hey auth status --json` when an explicit authentication check is needed (it reports stored credentials and does not contact HEY). Never run `hey auth login` unattended; use it only for interactive recovery with the user present.
3. **HTML output** — `--html` writes the original HTML of `hey thread read`, `hey journal read`, and a contact's private note (`hey contact show` or `hey contact note show`) to a pipe or file; a terminal is refused
4. **Linked mail accounts share one login** — use `hey account list --json`, then `--account <id|all>` when a task must target one account
5. **Local HEY configuration requires human trust** — never run `hey config trust-local` without the user's explicit approval

## Output Filtering

`--jq` filters the full JSON success envelope, so result data is under `.data`. String results print as plain text; objects and arrays print as formatted JSON. Use `--quiet --jq` when the expression should run against result data directly. Errors retain their complete structured envelope. Commands with dedicated raw output (`auth token`, `shell-completion generate`, `setup`, `skill`, `tui`, `--version`, and `timetrack export` without `--output`) reject `--jq`. `hey watch` writes one raw JSON event per line with no envelope and does not apply `--jq`.

```bash
hey box list --jq '.data[] | {id, name}'
hey search "quarterly planning" --jq '.data[].topic_id'
hey box list --quiet --jq '.[].name'
```

An empty result is an empty array rather than `null`, so `.data[]` is safe to run against a
listing that found nothing.

For the two commonest shapes there is no need for an expression at all: `--ids-only` prints
one ID per line and `--count` prints a bare number on stdout. Both answer for what was read,
which is the first page unless `--all` (or `--limit`) reads more, and not every listing warns
on stderr that more pages exist — `hey screener list --count` is the exception and counts
the whole queue. Both need list data, so they work on the listings: every `list` command,
the thread listings (`hey box view`, `hey label view`, `hey collection view`, `hey set-aside view`,
`hey set-aside group view`, `hey bundle view`, `hey contact threads`, `hey bubble list`),
`hey workflow view`, `hey search`, `hey screener history`, `hey event day`, `hey event week`,
`hey timetrack categories`, `hey attachment list` and `hey thread read` (its entries, read
from the index without fetching bodies). On the thread listings they count and list the
postings, not the box, label or contact around them.

## Quick Reference

| Task | Command |
|------|---------|
| List linked mail accounts | `hey account list --json` |
| List sender addresses and IDs | `hey account senders --json` |
| Set default mail account | `hey account use <id\|all>` |
| Run once for one account | `hey --account <id> box list --json` |
| Review trusted local settings | `hey config trusted-locals --json` |
| Trust this repository's settings | `hey config trust-local` (requires explicit user approval) |
| List mailboxes | `hey box list --json` |
| List emails in a box | `hey box view imbox --json` |
| List labels | `hey label list --json` |
| List emails with a label | `hey label view <label_id> --all --json` |
| Add a label to a thread | `hey label add <box_item_id> --to <label_id>` |
| Create and add a label | `hey label create "Travel receipts" <box_item_id>` |
| Remove labels | `hey label remove <box_item_id> --from <label_id\|all>` |
| List collections | `hey collection list --json` |
| List collection threads | `hey collection view <collection_id> --all --json` |
| Create a collection | `hey collection create "Kitchen remodel"` |
| Update a collection | `hey collection update <collection_id> --name "Kitchen renovation"` |
| Add a thread to a collection | `hey collection add <topic_id> --to <collection_id>` |
| Remove a thread from a collection | `hey collection remove <topic_id> --from <collection_id>` |
| List Set Aside threads with their group | `hey set-aside view --all --json` |
| List Set Aside groups | `hey set-aside group list --json` |
| List a group's threads | `hey set-aside group view <group_id> --json` |
| Gather threads into a new group | `hey set-aside group create <box_item_id> <box_item_id>` |
| File threads into a group | `hey set-aside group add <box_item_id> --to <group_id>` |
| Take threads out of their group | `hey set-aside group remove <box_item_id>` |
| Delete a group (its threads go to Previously Seen) | `hey set-aside group delete <group_id>` |
| List workflows | `hey workflow list --json` |
| View workflow stages | `hey workflow view <workflow_id> --json` |
| Put threads in a workflow stage | `hey workflow add <topic_id> --to <workflow_id> --stage <stage_id>` |
| Move threads to another stage | `hey workflow move <topic_id> --workflow <workflow_id> --to <stage_id>` |
| Take threads out of a workflow | `hey workflow remove <topic_id> --from <workflow_id>` |
| List clips | `hey clip list --json` |
| Clip a passage from a message | `hey clip create <entry_id> --content "The launch moves to Wednesday."` |
| List snippets | `hey snippet list --json` |
| Create a snippet | `hey snippet create --name "Scheduling reply" --content "Tuesday works for me."` |
| Search email | `hey search "quarterly planning" --json` |
| List search filters | `hey search filters --json` |
| List contacts | `hey contact list --json` |
| Find a screened-in contact by email | `hey contact list --all --jq '.data[] \| select(.email_address == "jane@example.com") \| .id'` (see Contacts for what it misses) |
| View contact without its threads | `hey contact show <contact_id> --jq '.data \| del(.postings)'` |
| List every thread with a contact | `hey contact threads <contact_id> --json` |
| Add contact | `hey contact add --name "Jane Doe" --email jane@example.com` |
| Edit contact | `hey contact update <contact_id> --name "Jane Dawson"` |
| Hide contact | `hey contact hide <contact_id>` |
| Show contact again | `hey contact show-again <contact_id>` |
| Bundle a contact's mail | `hey contact bundle <contact_id>` |
| List a contact's mail separately | `hey contact unbundle <contact_id>` |
| List a bundle's unseen threads | `hey bundle view <box_item_id> --json` |
| Read private contact note | `hey contact note show <contact_id> --json` |
| Replace private contact note | `hey contact note set <contact_id> "Prefers email"` (replaces the whole note) |
| Delete private contact note | `hey contact note delete <contact_id>` |
| Read email thread | `hey thread read <topic_id> --json` |
| Get a sharing link | `hey share <topic_id>` |
| Turn off a sharing link | `hey unshare <topic_id>` |
| Reply to email | `hey reply <topic_id> -m "Friday works for me."` |
| Forward email | `hey forward <topic_id> --to alice@example.com -m "For your review"` |
| Compose email | `hey compose --to alice@example.com --subject "Lunch plans" -m "Are you free Friday?"` |
| Compose with CC/BCC | `hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Kitchen remodel timeline" -m "Cabinets land the week of the 14th."` |
| List drafts | `hey draft list --json` (`--all`/`--page` follow the cursor) |
| Draft an email for human review | `hey compose --to alice@example.com --subject "Lunch plans" -m "Free Friday?" --draft` |
| Draft a reply for human review | `hey reply <topic_id> -m "Drafting this." --draft` |
| Read a draft back | `hey draft show <draft_id> --json` |
| Change a draft | `hey draft edit <draft_id> --to alice@example.com --subject "New subject"` |
| Change a draft sender | `hey draft edit <draft_id> --from billing@example.org` |
| Compose from a selected address | `hey compose --from billing@example.org --to alice@example.com --subject "Board update" -m "Numbers to follow." --draft` |
| Send a draft | `hey draft send <draft_id>` |
| Trash drafts | `hey draft delete <draft_id>...` |
| Who is waiting in The Screener | `hey screener list --json` (clearance IDs) |
| Number waiting | `hey screener list --count` |
| Let a sender through | `hey screener approve <clearance_id>` |
| Turn a sender away | `hey screener deny <clearance_id>` |
| Who was already screened | `hey screener history --json` |
| Preview a bulk reply | `hey bulk-reply preview <box_item_id> <box_item_id> --json` |
| Send a bulk reply | `hey bulk-reply send <box_item_id> <box_item_id> -m "Thanks for the update."` |
| Recall a bulk reply | `hey bulk-reply undo <delivery_id>` |
| List calendars | `hey calendar list --json` |
| List calendar events | `hey event list --json` |
| Today's schedule, recurrences expanded | `hey event day --json` |
| Add a calendar event | `hey event add "Design review" --starts-on 2026-09-02 --start-time 14:00` |
| List todos | `hey todo list --json` |
| Add todo | `hey todo add "Draft the quarterly report"` |
| Complete todo | `hey todo complete 123` |
| Uncomplete todo | `hey todo uncomplete 123` |
| Delete todo | `hey todo delete 123` |
| Wait for new mail | `hey watch --box imbox --events new --exit-on-first` |
| Follow every change | `hey watch` |
| Mark as seen | `hey seen <box_item_id>` |
| Mark as unseen | `hey unseen <box_item_id>` |
| Move email threads | `hey move <box_item_id> --to feed` |
| Remove Reply Later | `hey move <box_item_id> --to imbox` |
| Bubble a thread up now | `hey bubble up <box_item_id> --now` |
| Bubble a thread up on a date | `hey bubble up <box_item_id> --on 2026-09-04` |
| Bubble a thread up this weekend | `hey bubble up <box_item_id> --weekend` |
| List bubbled-up and scheduled threads | `hey bubble list --json` |
| Cancel a bubble-up | `hey bubble pop <box_item_id>` |
| Move email threads to Trash | `hey trash <box_item_id>` |
| Mark email threads as spam | `hey spam <box_item_id>` |
| Ignore email threads | `hey ignore <box_item_id>` |
| Stop ignoring email threads | `hey stop-ignoring <box_item_id>` |
| List habits | `hey habit list --json` |
| Create habit | `hey habit create "Morning strength training"` |
| Edit habit | `hey habit edit 123 --days mon,wed,fri` |
| Delete habit | `hey habit delete 123` |
| Complete habit | `hey habit complete 123` |
| Uncomplete habit | `hey habit uncomplete 123` |
| Start time tracking | `hey timetrack start` |
| Stop time tracking | `hey timetrack stop` (`--category "Client work"` files it) |
| Current timer | `hey timetrack current --json` |
| List time entries | `hey timetrack list --json` |
| Edit a completed time entry | `hey timetrack edit <id> --end 2026-08-22T17:30` |
| Delete a time entry | `hey timetrack delete <id>` |
| Export completed time entries | `hey timetrack export > tracked-time.csv` (`--json`/`--quiet`/`--markdown` need `--output`) |
| Save a time tracking export | `hey timetrack export --output tracked-time.csv --json` |
| List time track categories | `hey timetrack categories --json` |
| Create time track category | `hey timetrack category create "Client work"` |
| List journal entries | `hey journal list --json` |
| Read journal entry | `hey journal read 2024-03-15 --json` |
| Write journal entry | `hey journal write "Shipped the pagination fix."` (whitespace-only content removes the entry) |
| Check auth status | `hey auth status --json` |
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
├── See Set Aside with its groups? → hey set-aside view --all --json / hey set-aside group list --json
├── Group, regroup, or ungroup Set Aside threads? → hey set-aside group create|add|remove (delete sends the group's threads to Previously Seen)
├── Search threads and messages? → hey search <query> --json
├── Need available refinements? → hey search filters --json
├── List or view contacts? → hey contact list --json / hey contact show <contact_id> --json
├── Find a contact by email? → hey contact list --all --jq (see Contacts)
├── Every thread with a contact? → hey contact threads <contact_id> --json
├── A row with kind "bundle"? → hey bundle view <box_item_id> --json (its unseen threads)
├── Read full thread? → hey thread read <topic_id> --json
├── Get a sharing link? → hey share <topic_id>
├── Turn off the sharing link? → hey unshare <topic_id>
├── Mark as seen? → hey seen <box_item_id>
├── Mark as unseen? → hey unseen <box_item_id>
├── Move to another box? → hey move <box_item_id> --to <box>
├── Remove or unmark Reply Later? → hey move <box_item_id> --to imbox
├── Move to Trash? → hey trash <box_item_id>
├── Mark as spam? → hey spam <box_item_id>
├── Ignore future activity? → hey ignore <box_item_id>
├── Stop ignoring? → hey stop-ignoring <box_item_id>
├── Who is waiting to be screened? → hey screener list --json
├── Screen a sender in or out? → hey screener approve|deny <clearance_id>
└── Launch interactive UI? → hey tui
```

### Sending Email

```
Want to send email?
├── Reply to thread? → hey reply <topic_id> -m "message"
│   ├── Open editor? → hey reply <topic_id> (with no message: $EDITOR at a terminal, otherwise stdin)
│   └── Attach files? → add --attach ./report.pdf (repeatable)
├── Reply to many threads at once? → hey bulk-reply preview <box_item_id>... first, then send
│   └── Sent by mistake? → hey bulk-reply undo <delivery_id> (while the window is open)
├── Forward latest message? → hey forward <topic_id> --to <email>
│   └── Add a note? → add -m "note"
├── Compose new? → hey compose --to <email> --subject "Subject" -m "Body"
│   ├── With files? → add --attach ./report.pdf (repeatable; body is optional)
│   ├── With CC? → add --cc <email>
│   └── With BCC? → add --bcc <email>
├── List files in a thread? → hey attachment list <topic_id> --json
│   └── Save one? → hey attachment save <attachment_id> [--output <path>]
├── Draft instead of sending (human reviews in HEY)? → add --draft to compose or reply; the answer carries the draft id
│   ├── Read it back? → hey draft show <draft_id> --json
│   ├── Change it? → hey draft edit <draft_id> --subject/--to/--cc/--bcc/-m (flags replace; omitted fields are kept)
│   ├── Deliver it? → hey draft send <draft_id> (recipients required)
│   └── Discard it? → hey draft delete <draft_id>
└── Check drafts? → hey draft list --json
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

**Response format:** `hey box view --json` returns the box itself — `id`, `kind`, `name`, `app_url`, `next_history_url`, `next_page` — with a `postings` array of the email threads in it. Each posting has `id` (box item ID), `topic_id` (thread ID), `kind` (`bundle` for a bundle row), `name` (subject), `created_at` and `app_url`; a thread row also has `contacts`, `summary` and `visible_entry_count`. `seen` is present only when true — an unseen posting omits it, so test `.seen != true`. Use `id` for the box item commands (see the ID note under Threads) and `topic_id` for `hey thread read`, `hey reply`, `hey forward`, `hey share` and `hey attachment list`.

**Unmatched IDs are not reported.** HEY checks only that *some* box item ID in a call is yours. `hey trash`, `hey spam`, `hey label add|remove|create` and `hey bubble up|pop` answer `not_found` only when none match; `hey seen`, `hey unseen`, `hey move`, `hey ignore`, `hey stop-ignoring` and `hey set-aside group create|add|remove` never check (`group create` with no matching ID leaves an empty group). Every ID that does match is changed, so a mixed batch is a partial success reported as a whole one, a typo is silently skipped, and a `topic_id` that happens to equal one of your box item IDs acts on that unrelated thread (the reverse, a box item ID given to `hey thread read`, can likewise read an unrelated thread). Confirm rather than trust the envelope: `hey box view <box> --json --all --jq '{notice, next_page: .data.next_page, match: [.data.postings[] | select(.id == <id>) | {id, topic_id, seen}]}'` for a mark, or the destination box for a move. `--all` reads up to 101 pages; an empty `match` with `next_page` still set means the box is larger than that, so continue with `--page <next_page>` rather than calling it a non-match.

A posting whose `kind` is `bundle` rolls one sender's mail into a single row and can **omit `topic_id`**: a bundle names its sender rather than a thread, and its `name` joins the bundled subjects with `•`. It carries a `topic_id` only while it holds exactly one unseen thread, and `hey thread read` reads that thread as usual. Never substitute the bundle row's `id` for a thread (`hey thread read <id>` answers `not_found`, with a hint naming the bundle). Instead, `hey bundle view <id>` lists the bundle's unseen threads, each with its `topic_id`, and answers the bundled `contact`; `hey contact threads <contact_id>` lists every thread with that sender, seen and unseen — the only place a bundle's mail is listed once it has been read through. `hey move` refuses a bundle row; change grouping with `hey contact bundle|unbundle <contact_id>`.

`next_page` is the cursor `--page` takes, and it is the cursor inside `next_history_url` — `--page` accepts either. `--all` reads up to 101 pages; if `next_page` is still set, continue with `--page`.

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

Label mutations take box item IDs from `hey box view`, `hey label view`, or `hey search` results that carry an `id`. Label IDs come from `hey label list`. `hey label view` returns `next_page` and `total_count`; pass `--page <next_page>` to continue or `--all` to fetch every page. `label create` files at least one thread as it creates the label, so it needs one or more of your box item IDs; a name you already use (in any case) is refused — use `label add` for an existing label.

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

### Email - Set Aside groups

```bash
hey set-aside view --all --json                # Set Aside threads, each with box_group_id when grouped
hey set-aside group list --json                # Groups with thread_count
hey set-aside group view 42 --all --json       # Threads in one group; pages like a box
hey set-aside group create 12345 67890         # New group from box item IDs; answers the group id
hey set-aside group add 12345 --to 42          # File threads into a group (moves them out of another)
hey set-aside group remove 12345               # Ungroup; threads stay in Set Aside
hey set-aside group delete 42                  # Dissolve the group; its threads go to Previously Seen
```

Groups have no name in HEY: a group is its ID and the threads in it. Group commands take posting `id` values (box item IDs), not `topic_id`. HEY's group index answers IDs alone, so `group list` reads each group once for its count; `group view` returns `next_page` and `total_count` and takes `--page` and `--all`. HEY removes a group itself once its last thread leaves, so `group view` or `group delete` on it answers `not_found`. `group create` and `group add` move threads into Set Aside if they are elsewhere and mark them seen, which also clears a bubble-up. To clear an overflowing Set Aside without losing threads, prefer `group create`/`group add` over `group delete`: deleting a group moves its threads to Previously Seen in the Imbox.

### Email - Workflows, clips and snippets

```bash
hey workflow list --json                                   # Workflows and their IDs
hey workflow view 123 --json                               # Stages, in position order, with IDs
hey workflow add 501 --to 123 --stage 456                  # Put threads (topic IDs) in a stage
hey workflow move 501 --workflow 123 --to 789              # Move them to another stage
hey workflow remove 501 --from 123                         # Take them out of the workflow
hey workflow create "Hiring"                               # Also: update <id> --name, delete <id>
hey workflow stage create 123                              # Adds an Untitled stage; rename with stage update
hey workflow stage update 123 456 --name "Interviewing"
hey clip create 987 --content "The launch moves to Wednesday."  # Save a passage from an entry
hey clip delete 44
hey snippet create --name "Scheduling reply" --content "Tuesday works for me."
hey snippet update 44 --content "Wednesday works for me."
hey snippet delete 44
```

Workflow membership commands take `topic_id`. `hey clip create` takes an entry ID — an
entry's `id` from `hey thread read` — and the passage must appear in that message's text;
`hey clip list` reads only the newest page of clips.

### Email - Search

```bash
hey search "quarterly planning" --json         # Free-text search
hey search --from jane@example.com --date last_30_days --json  # Refined search
hey search --subject invoice --attachment pdfs --all --json    # Search up to 100 pages
hey search filters --json                      # Available box, date, label, and attachment values
```

Search refinements are `--required`, `--any`, `--none`, `--exact`, `--from`, `--to`, `--subject`, `--date`, `--in`, `--label`, and `--attachment`. `--page` selects one result page; `--all` fetches up to 100 pages from that point onward. When the cap is reached, the response notice provides the next `--page` value for continuation.

`--in`, `--date`, `--label` and `--attachment` accept only the values `hey search filters` lists: boxes are `imbox`, `feed`, `papertrail`, `trash`; dates are `last_7_days`, `last_30_days`, `last_90_days` or a year (`hey search filters` lists 2020 to this year; the CLI refuses anything but `20xx`); attachment kinds are `any`, `images`, `pdfs`, `calendar_invites`, `documents`, `spreadsheets`, `presentations`, `media`, `zip_files`. The kinds are plural — `--attachment pdfs`, not `pdf`. An unrecognized `--in`, `--date` or `--attachment` is refused as a usage error naming the values it accepts, before anything is sent; `--label` is not checked, so read `hey search filters` when unsure of a label.

**Response format:** `data` contains one item per matching thread. Each result has `id` (box item ID for organization actions), `topic_id` (thread ID for `hey thread read`, `hey reply`, and `hey forward`), `subject`, `updated_at`, and `messages` containing the matching message IDs, senders, dates, and summaries. A result omits `id` when you have no box item for its thread.

### Contacts

```bash
hey contact list --json                       # List contacts
hey contact list --page 2 --json              # List another page
hey contact list --all --jq '.data[] | select(.email_address == "jane@example.com") | .id'  # Find a screened-in contact by email
hey contact show 12345 --jq '.data | del(.postings)'  # Details, aliases, and private note, without its threads
hey contact threads 12345 --all --json        # Every thread with the contact, seen and unseen
hey contact add --name "Jane Doe" --email jane@example.com
hey contact add --name "Jane Doe" --email jane@example.com --alias jane.doe@example.org
hey contact update 12345 --name "Jane Dawson"
hey contact update 12345 --alias=              # Clear aliases
hey contact hide 12345                         # Hide from lists and autocomplete
hey contact show-again 12345                   # Reverse hiding
hey contact bundle 12345                       # Group this contact's mail into one row
hey contact unbundle 12345                     # List this contact's mail separately
hey contact note show 12345 --json
hey contact note set 12345 "Prefers email"
echo "Prefers email; call only for urgent deliveries" | hey contact note set 12345
hey contact note set 12345 --note-html "<p><strong>Prefers email</strong></p>"
hey contact note delete 12345
```

`hey contact list` returns each contact's ID, name, primary email address and update timestamp. It lists only non-HEY senders you have screened in: other HEY users, senders still in (or denied by) The Screener, aliases and hidden contacts are left out, so an address missing there does not mean HEY has no contact for it. It has no search: to find a contact by email, read every page with `--all` (up to 100 pages; the notice names the `--page` to continue from) and filter with `--jq` as above. That matches primary addresses only — a contact's aliases are on `hey contact show`.

`hey contact show` adds aliases, screening status, the private note, and `postings`: a page of the contact's threads, which can run to tens of kilobytes. Drop it with `--jq '.data | del(.postings)'`, read just the note with `hey contact note show`, or page through every thread with `hey contact threads`. Contact updates preserve omitted fields. Supplying `--alias` replaces the complete alias list, and `--alias=` clears it.

HEY hides contacts instead of permanently deleting them. A hidden contact leaves contact lists, autocomplete, and search results while remaining available by ID; `show-again` reverses the action. A contact that is hidden, an alias, or another HEY user cannot be edited: `contact update`, `contact note set` and `contact note delete` answer `not_found`. Bundling groups a contact's mail into one row without merging or deleting the underlying threads; `unbundle` lists those threads separately again. HEY bundles only a contact with no box preference or one sent to the Paper Trail; for any other, `bundle` answers success and changes nothing.

**A contact note is written whole.** `hey contact note set` replaces the entire note, so to add a detail, read the note, change it, and write all of it back. It takes Markdown — positional, `--note`, stdin, or `$EDITOR` (which opens on the existing note) — or raw HTML with `--note-html`; an empty note is refused, and `hey contact note delete` clears one without touching the contact. `hey contact note show --json` answers `note` as plain text, which loses formatting (bold is dropped, list items become `•`), and `note_html` with the formatting intact. To append without losing formatting, extend `note_html` and write it back as HTML:

```bash
note=$(hey contact note show 12345 --jq '.data.note_html')
hey contact note set 12345 --note-html "$note<p>Moved to the Lisbon office in March.</p>"
```

Notes are private to the HEY account that holds the contact, so they are reached only through the user's own login. With linked accounts, `--account <id>` (from `hey account list`) selects whose contacts a command reads and writes.

### Email - Threads

```bash
hey thread read <topic_id> --json                 # Read full email thread
hey thread read <topic_id> --html > thread.html  # Original HTML, to a file or pipe
hey thread read <topic_id> --allow-partial --json # Accept a thread that could only be read in part
hey share <thread_id>                         # Get a sharing link
hey unshare <thread_id>                       # Turn off the sharing link
```

`hey thread read` returns every entry in the thread, oldest first. Each entry's `body` is
**Markdown**, converted from HEY's Trix HTML at the edge, so headings, lists, quotes,
tables and code survive and links keep their URLs — read it as structure rather than as
flattened text. `creator` is the entry's author — the external sender for inbound mail,
your own contact for mail you sent. `sender` appears only on a hydrated message you sent
from a different address (an alias, custom domain or external account); use
`sender.email_address` for its From, and `creator.email_address` otherwise.
An entry whose message was read also carries `recipients`, with `to`, `cc` and `bcc` contact lists; a known-empty
line is `[]`, while an entry whose message was not hydrated omits the object. In JSON, an inbound entry also carries `received_via`:
every exact account address HEY recorded it arriving through, including aliases and plus
tags. These delivery records are distinct from visible To/CC/BCC recipients. Each one's
resolved `contact` is optional; `received_via` is omitted for sent/generated messages and
when the message was not hydrated. `--html` returns the original body HTML framed by From,
To, CC and BCC header rows, and is refused on a terminal — redirect it. A thread that could
only be read in part is refused unless `--allow-partial` is given, and then the notice says
what is missing. Use `hey reply` to have HEY work out reply addressing.

`hey share` returns a URL that shows the entire thread and future emails or replies sent to it. Anyone with the link can open it. `hey unshare` turns off the sharing link.

**ID note:** Every email thread has two IDs: an `id` (its box item ID) and a `topic_id` (its thread ID). `hey seen`, `hey unseen`, `hey move`, `hey label add`, `hey label remove`, `hey trash`, `hey spam`, `hey ignore`, `hey stop-ignoring`, `hey bubble up|pop`, `hey set-aside group`, `hey bulk-reply` and `hey bundle view` expect `id`. `hey thread read`, `hey share`, `hey unshare`, `hey attachment list`, `hey reply`, `hey forward`, `hey compose --thread-id`, `hey collection add|remove`, and `hey workflow add|move|remove` expect `topic_id`. Passing the wrong one is not redirected: depending on the command it answers `not_found`, is silently skipped, or acts on an unrelated thread whose ID happens to match — see "Unmatched IDs are not reported" under Boxes.

`hey box view --json`, `hey label view --json`, `hey collection view --json`, `hey bundle view --json`, `hey contact threads --json` and `hey search --json` all carry both, with two exceptions: a bundle posting can omit `topic_id` (see the Boxes section), and a search result omits `id` when you have no box item for its thread, so it cannot be used with the box item commands above.

### Email - Attachments

```bash
hey attachment list <topic_id> --json               # List files in every message
hey attachment save 67890:1                    # Save using a returned ID
hey attachment save 67890:1 --output ./reports # Save into ./reports if that directory exists
hey attachment save 67890:1 --output ./report.pdf --force
```

Direct attachment IDs combine the message ID and position, so `67890:1` identifies the first direct attachment in message `67890`. Named downloadable files inside embedded HTML, including named inline images, use opaque IDs scoped to their message. Pass the ID returned by `hey attachment list` to `hey attachment save`. Saving uses the original filename unless `--output` names a destination; `--output` is a directory only if one already exists there, otherwise it is the file name. Existing files are preserved unless `--force` is set.

### Email - Reply, Forward & Compose

```bash
hey reply <topic_id> -m "Friday works for me — I'll send an agenda."  # Inline message
hey reply <topic_id>                          # $EDITOR at a terminal; otherwise the body is read from stdin
hey reply <topic_id> --to support@example.com -m "The replacement is on the way."
hey reply <topic_id> --to support@example.com --replace-recipients --dry-run --json
hey reply <topic_id> -m "Here is the wiring diagram." --attach ./diagram.png
hey forward <topic_id> --to alice@example.com                 # Forward the latest message
hey forward <topic_id> --to alice@example.com -m "Please review before Thursday."
hey compose --to alice@example.com --subject "Lunch plans"    # $EDITOR at a terminal; otherwise stdin
hey compose --to alice@example.com --subject "Lunch plans" -m "Are you free Friday?"
hey compose --to alice@example.com --subject "Q3 revenue report" --attach ./report.pdf  # No body text
hey compose --to alice@example.com --subject "Q3 revenue report" -m "The numbers are attached." --attach ./report.pdf --attach ./chart.png
hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Kitchen remodel timeline" -m "Cabinets land the week of the 14th."
hey compose --thread-id 12345 -m "Confirmed — see you then."  # Reply into an existing thread (no subject: it carries the thread's)
hey compose --to alice@example.com --subject "Sprint recap" -m "We **shipped** the pagination fix."
hey compose --to alice@example.com --subject "Newsletter draft" --message-html "<h1>March</h1><p>What we shipped.</p>"
```

`hey reply` answers the thread's **latest** entry. HEY addresses the reply the way its own
web app does: everyone that entry was addressed to, plus whoever wrote it, on the To line,
minus your own addresses. If HEY's prefill is unavailable or names no one (a thread with only
yourself), a send falls back to the message's own recipients, which can include you.
Repeatable `--to`, `--cc` and `--bcc` flags add or move explicit recipients. Use
`--replace-recipients` to discard HEY's prefill. Run `--dry-run --json` first when an
agent changes the envelope; it does not read the original message body and reports the
resolved sender and final recipients without sending. If HEY's envelope prefill is
unavailable, use `--replace-recipients` with explicit addresses because a dry run will not
guess the original lists. A reply HEY cannot address is refused unless an explicit
recipient makes it addressable.

Message bodies, drafts, journal entries, snippets and contact notes are Markdown by
default — `-m`, `--content`, `--note`, positional content, stdin, and `$EDITOR` alike — and
are converted to rich text on the way out (clip passages, event notes and time track notes
are plain text). To
send raw HTML instead, use the flag's HTML twin: `--message-html` on `compose`, `reply`,
`forward`, `draft edit` and `bulk-reply send`; `--content-html` on `journal write` and
`snippet create`/`update`; `--note-html` on `contact note set`. Each pair is mutually
exclusive. A fenced code block's language (` ```ruby `) survives the conversion for the
languages HEY highlights — Ruby, Python, JavaScript, TypeScript, Go, Rust, Java, C#, C++,
PHP, Swift, HTML and CSS; any other is dropped.

### Email - The Screener

```bash
hey screener list --json                      # Who is waiting to be screened
hey screener list --count                     # Just the number waiting (cheap)
hey screener approve 91                       # Let a sender through, into the Imbox
hey screener approve 91 --box "The Feed"      # Let them through, into another box
hey screener approve 91 --seen                # Deliver what they sent, marked read (see below)
hey screener deny 91 92                       # Turn several senders away
hey screener deny 91 --spam                   # Turn away as spam
hey screener history --json                   # Who has already been decided
hey screener clear                            # Trash everything waiting, deciding no one
```

The Screener is where first-time senders wait. `hey screener list` returns **clearance
IDs** — not contact IDs and not posting IDs — with the sender, what they sent, and a
`topic_id` for reading the thread before deciding. `--count` is a far cheaper request than
the queue and prints a bare number.

Approving delivers everything that sender has waiting; denying hides it. Either is
reversible with the opposite command. `--box` and `--seen` apply to one sender at a time;
several IDs go through HEY's bulk endpoint, which takes neither. `--seen` reliably marks the
mail read only when the sender has a single waiting thread; with several, HEY files them
after answering and they can arrive unread. `--spam` marks what they sent as spam, and with
a single ID also trains HEY's filter, which is harder to undo than a plain deny.
**`hey screener clear` moves everything waiting to Trash** (a shared thread loses your access
instead) without approving or denying anyone; those senders are screened again on their next
email. It is queued, so `list` can lag briefly. Confirm with the user before running it.

### Email - Bulk reply

```bash
hey bulk-reply preview 12345 67890 --json     # Read-only: threads and exact recipients
hey bulk-reply send 12345 67890 -m "Thanks for the update — noted."
hey bulk-reply undo 98765                     # Recall a delayed bulk reply
```

Takes box item IDs, which must be positive and unique. **Always run `preview` first** — it
resolves each posting to the entry a reply would answer and shows the exact To/CC/BCC, so
the blast radius is visible before anything sends. `send` resolves the selection again and
skips threads with no replyable entry, then returns the reply count, delivery ID and delayed
state, plus the undo URL and command when HEY delayed it (Undo Send on). `undo` works only
within HEY's 15-second window. IDs that are not your box items are skipped:
`preview` reports them in `meta.skipped_count`, and `send` with none left is a usage error.

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
hey move 12345 --to feed                      # Move one thread
hey move 12345 67890 --to "paper trail"       # Move multiple threads
hey move 12345 67890 --to imbox               # Remove Reply Later from threads
```

Takes box item IDs (the `id` field from `hey box view --json`). `--to` accepts a box name, kind, or ID. Supported destinations are Imbox, The Feed, Set Aside, Reply Later, and Paper Trail. Moving to any of them but Imbox marks the threads seen, and a thread that cannot be replied to is silently not moved to Reply Later. Reply Later is a box, not an independent flag: moving a Reply Later thread to Imbox removes Reply Later, preserves its seen state, and leaves a seen thread in Previously Seen. It does not return the thread to the box it occupied before Reply Later. Bubble Up goes through `hey bubble` instead. A bundle row is refused — moving it would unbundle that sender's mail; use `hey contact bundle|unbundle <contact_id>`.

### Email - Bubble Up

```bash
hey bubble up 12345 --now                     # Bubble a thread up to the top of the Imbox
hey bubble up 12345 67890 --now               # Bubble multiple threads up
hey bubble up 12345 --on 2026-09-04           # Bubble a thread up on a date
hey bubble up 12345 --tomorrow                # Bubble a thread up tomorrow at 08:00 UTC
hey bubble up 12345 --weekend                 # Bubble a thread up the next Saturday at 08:00 UTC
hey bubble up 12345 --next-week               # Bubble a thread up next Monday at 08:00 UTC
hey bubble list                               # List bubbled-up and scheduled threads
hey bubble pop 12345                          # Cancel a bubble-up: the thread moves to the Imbox, seen
```

Takes box item IDs (the `id` field from `hey box view --json`). `hey bubble up` requires exactly one of `--now`, `--on`, `--tomorrow`, `--weekend`, and `--next-week`. `--on` takes a YYYY-MM-DD date; HEY bubbles the threads up at 08:00 UTC that day, or at 18:00 UTC when the date is today by your local clock. HEY schedules in UTC, so for a reader far from UTC "morning" may be the night before or the afternoon. `hey bubble pop` does not return a thread to its original box; it moves it to the Imbox and marks it seen.

`hey bubble list --json` answers two buckets: `bubbled_up`, the threads back in the Imbox after bubbling up, and `scheduled`, the threads waiting in Bubble Up — each scheduled row carries `bubble_up_schedule.bubble_up_at`, and `surprise_me` when HEY picked the time. Use `id` with `hey bubble pop`, `topic_id` with `hey thread read`.

### Email - Trash and Spam

```bash
hey trash 12345                               # Move one thread to Trash
hey trash 12345 67890                         # Move multiple threads to Trash
hey spam 12345                                # Mark one thread as spam
hey spam 12345 67890                          # Mark multiple threads as spam
```

Takes box item IDs (the `id` field from `hey box view --json`). Trashing a shared thread removes your access instead of deleting it for everyone. Marking a thread as spam moves it to Spam and trains HEY's filters. A bundle row is not a thread, so trashing or spamming one answers success and changes nothing; act on the threads from `hey bundle view` or `hey contact threads` instead.

### Email - Ignoring Threads

```bash
hey ignore 12345                              # Ignore one thread
hey ignore 12345 67890                        # Ignore multiple threads
hey stop-ignoring 12345                       # Stop ignoring one thread
hey stop-ignoring 12345 67890                 # Stop ignoring multiple threads
```

Takes box item IDs (the `id` field from `hey box view --json`). Ignoring marks a thread seen; it stays in its box, and new replies do not bring it back to your attention. While a thread is ignored, `hey unseen` has no effect on it. `hey stop-ignoring` resumes notifications but leaves the thread seen.

### Email - Watching for changes

```bash
hey watch                         # Follow every box and calendar until interrupted
hey watch --box imbox             # Report one box's changes (repeatable, by name or ID); every box is followed
hey watch --events added,deleted  # Only these mail changes (added, updated, deleted, new, resync)
hey watch --events recording_added,recording_updated  # Only calendar recordings as they change
hey watch --box imbox --events new  # New mail only: unseen, unmuted, active since the watch began
hey watch --box imbox --events new --exit-on-first  # Block until new mail lands, print it, exit
hey watch --exit-on-first         # Wait for one change of any kind, print it, exit
hey watch --timeout 30m           # Give up waiting after a while
hey watch --since 2026-03-15      # Report changes since then first, then follow
hey watch --run-sync ./triage.sh  # Run a command per change instead of printing
```

Long-running, and driven by a websocket rather than polling — never poll `hey box` in a
loop when this will do. Piped or with `--json`, it writes one JSON object per change to
stdout, one per line, instead of the usual envelope (at a terminal, one text line each): `{"change": "added", "at": ..., "box": {"id", "kind",
"name"}, "posting_id": ..., "thread_id": ..., "new": true|false, "posting": {...}}`. Use
`thread_id` with `hey thread read` (absent for a bundle row that names no single thread). `new` is on every `added` and `updated` line and says whether
the posting is new mail — unseen, not muted, and active since the watch last saw the thread,
or since the watch began for a thread it has not seen; the backlog a watch starts with is
never new, nor is reading, muting or moving a thread, and a reply on a known thread is.
`--events new` selects the new ones, alone or in a union with the other three. A deleted
posting carries no `posting`, `thread_id` or `new`. Three more lines
describe the watch itself: `{"change": "ready"}` once every box and calendar is caught up and the subscription
is live (again after every reconnect's catch-up), `{"change": "disconnected"}` when the
connection drops, and `{"change": "resync", "box": {...}}` when a box changed more than the
feed can list and the watch skipped ahead — re-read that box. A resync is an event of its
own: reported by default (`--run-*` scripts run for it, `--exit-on-first` counts it) and left
out by `--events new`, so a script for new mail never runs on one. `ready` and `disconnected`
carry no `box`, are written only when no `--run-*` command is given, and never count for
`--exit-on-first` — expect a `ready` line before the change it waits for.

The calendars are followed too, unless `--box` or an `--events` list naming only mail changes
scopes the watch to mail. A calendar line names its `calendar` (`id`, `name`) where a mail
line names its box: `recording_added`, `recording_updated` and `recording_deleted` (any
calendar recording — events, todos, habits and their completions, journal entries, time
tracks, countdowns, day titles) carry `recording_id` and `recording_type`, and all but a
deletion the `recording`; `calendar_added`, `calendar_updated` and `calendar_deleted` are
calendars coming and going, and `calendar_resync` is a calendar's feed skipping ahead.

To drive a command per change, choose one of two behaviours — passing both is an error.
`--run-async` spawns the command and moves on, so a slow one never holds up the watch and
two can overlap; `--run-sync` waits for each and runs them in order. Both get the JSON on
stdin and the fields as `HEY_CHANGE`, `HEY_AT`, `HEY_BOX_ID`, `HEY_BOX_KIND`,
`HEY_BOX_NAME`, `HEY_POSTING_ID` and `HEY_THREAD_ID` (or `HEY_CALENDAR_ID`, `HEY_CALENDAR_NAME`,
`HEY_RECORDING_ID` and `HEY_RECORDING_TYPE` for a calendar change) — plus `HEY_NEW=1` for new
mail, `HEY_NEW=0` otherwise — and both take over stdout.

### Drafts

```bash
hey compose --subject "Board update" -m "Numbers to follow." --draft   # save instead of sending; answers the draft id
hey compose --to alice@example.com --subject "Sprint recap" -m "Shipped." --no-name-tag  # leave the sender's HEY name tag off
hey reply <topic_id> -m "Drafting this." --draft  # save a reply draft, addressed like a real reply
hey draft list --json                             # List drafts; --all and --page follow the next_page cursor
hey draft show <draft_id> --json                  # The draft's editable state; body is Markdown
hey draft edit <draft_id> --to alice@example.com  # Each flag replaces its field; omitted flags keep the draft's
hey draft send <draft_id>                         # Deliver now (through HEY's undo window)
hey draft delete <draft_id> [<draft_id>...]       # Trash drafts
```

This is the review-before-send lane: an agent prepares the email as a draft, a person
reviews and sends it from any HEY app (or the agent sends it later with `hey draft send`).
A draft needs no recipients until it is sent; `--draft` on `hey compose` lifts the
recipient requirement.

**An edit is a revision, not a patch.** The CLI reads the draft first and resends the
whole of it, so an omitted flag keeps that field. `--to`/`--cc`/`--bcc` replace their
entire recipient kind; an explicit empty value (`--cc ""`) clears it. Any scheduled
delivery is preserved through edits.

**Scheduled deliveries.** Scheduling is done in a HEY app for now; the CLI has no flag to
set one (HEY's API schedules only to a whole hour). A draft scheduled in an app stays
in `hey draft list` until it goes out, `hey draft show` reports `scheduled_delivery_at`
in UTC like every HEY timestamp, an edit preserves the schedule untouched — refusing the
rare schedule it could not keep exact rather than moving it — and `hey draft delete`
trashes the draft, which stops the delivery while it stays trashed.

### Calendars

```bash
hey calendar list --json                          # Each calendar's id and kind; name (absent on the personal calendar); owned, personal and external only when true
```

Everything a calendar holds is a recording, and each kind has its own command: `hey event`,
`hey todo`, `hey journal`, `hey habit`, `hey timetrack`. Those that read a calendar's
window — `hey event list`, `hey todo list`, `hey journal list` — share `--calendar`,
`--starts-on`, `--ends-on`, `--limit` and `--all`. Both dates want `YYYY-MM-DD`; an
unreadable one, or an `--ends-on` before `--starts-on`, is a usage error rather than an
empty result. Naming only `--starts-on` moves the whole window rather than reading up to
the default end.

### Events

```bash
hey event list --json                        # Every calendar, today through the next 30 days
hey event list --calendar 123 --starts-on 2026-01-01 --ends-on 2026-01-31 --json
hey event day --json                         # Today as HEY draws it, recurrences expanded
hey event day 2026-09-02 --json              # One day
hey event week 2026-09-02 --json             # The week that day falls in
hey event add "Design review" --starts-on 2026-09-02 --start-time 14:00 --end-time 15:00
hey event add "Sarah's birthday" --starts-on 2026-09-02   # No time given, so all day
hey event add "Standup" --calendar 123 --start-time 09:15 --repeat every_weekday --remind 10m
hey event edit 4821 --title "Design review (moved)"
hey event edit 4821 --occurrence 4821_2026-09-15 --apply-to current --start-time 15:00 --json   # That day alone
hey event edit 4821 --occurrence 4821_2026-09-15 --apply-to future --repeat every_week --repeat-times 8 --title "Design review (v2)" --allow-plain-notes --json
hey event delete 4821                                                   # The whole event, a series included
hey event delete 4821 --occurrence 4821_2026-09-15 --apply-to current    # That day alone
```

Without `--calendar`, `list` reads every calendar and `add` files on HEY's default: the
first ordinary calendar the user owns that is not a subscription — never Maybe or the
personal calendar. Pass `--calendar` to file anywhere else. A repeating event lists once as its series, plus a row for each day HEY has
written out on its own.

**"What's on my schedule today?" is `hey event day`, not `list`.** A day or a week is the
span as HEY draws it: a repeating event is expanded into the occurrences inside it, each
carrying that day's own times and an `occurrence_id`. A virtual occurrence has the series
in `id` and `parent_id`; a day HEY has written out on its own keeps its own event ID in
`id` and `recording_id`, with the series in `parent_id`. That own ID edits the day alone;
deleting one day always goes through the series with `--occurrence` (below).
Styled period tables that contain occurrences print `Series ID`, `Occurrence ID` and
`Recording ID` beside `ID`. The period covers the calendars switched on in HEY,
so `day` and `week` take no `--calendar` — only `--limit` and `--all`.

**Response format:** a flat array of events. Each has `id`, `title`, `starts_at`, `ends_at`
and `calendar`; `all_day` and `recurring` appear only when true, the zones only on timed
events, and `description` (the notes, as plain text), `location`, `url`, `attached_entry`
and `reminders` when set. Occurrences carry `parent_id` and `occurrence_id`, and in `day`
and `week` a written-out day also carries `recording_id`. `--count` and `--ids-only` read
that array directly.

**Editing is a replacement, not a patch.** `hey event edit` reads the event first and
sends back the notes, location, link, attached email, reminders and time zones it is not
changing, because HEY clears whatever a write omits. Two things still cannot survive it:
notes come back as plain text, so their formatting is flattened, and a countdown is a
recording of its own that the whole-event edit does not read back, so an edit removes one
unless `--countdown` names it again. The event is looked for within a year either side of
today and refused rather than written blind if it is not found — pass the day it starts
(`hey event edit 4821 2026-09-02`) for one outside that; `--calendar` only limits which
calendars are read.

**An id alone edits the whole series; one day of it is `--occurrence` plus `--apply-to`.**
`--occurrence` takes the `occurrence_id` from `day` or `week` exactly as served
(`<series id>_<YYYY-MM-DD>`, naming the series the positional id names) and `--apply-to`
is required with it: `current` changes that day alone, `future` changes that day and every
one after it — HEY's own two choices. `--apply-to` without `--occurrence`, any other value,
a malformed or mismatched occurrence id, or `--repeat`/`--repeat-until`/`--repeat-times`
with `current` are usage errors, refused before anything is read. The day is read on its
own date, so leave `[date]` out or name that day. A `future` edit starts a new series and
requires `--repeat` to state its complete schedule; combine a preset with `--repeat-times`
or `--repeat-until` for a finite series, naming what remains from the edited day, or use
`--repeat custom` without either limit to copy an existing opaque schedule. A custom rule
with `COUNT` can restart its full count because HEY cannot expose how many occurrences remain.
HEY accepts the new series' submitted start even when it overlaps an earlier occurrence,
so choose its date and time deliberately; its last day cannot precede its first day. A
virtual day of an opaque custom schedule takes its exact time from HEY's Day view and is
refused if that view no longer serves it. A realized custom day is refused for `future`,
because HEY does not serve the rule's authoritative occurrence boundary; split from a
virtual occurrence, edit that day alone, or edit the whole series. A realized preset occurrence moved away from its
series time is also refused: move it back with a `current` edit first. HEY cancels
children from the moved time but truncates the parent at the occurrence identifier, so
splitting directly can lose neighboring realized edits. A preset `--repeat` alone means
forever. HEY gives
the new series a new id, and the answer is still
the day edited — read `day` or `week` again before editing it further.

An occurrence edit keeps everything it is not told to change — that day's own schedule,
zones, notes, location, link, attached email, reminders, circle and countdown, from the day
itself where HEY has already written it out — and refuses what it cannot keep. A countdown
owned by the day is read back and re-sent; an inherited series countdown stays inherited
for `current` and is copied to the replacement series for `future`. Only `--countdown 0`
removes it, and not from one day of a series that has one, since HEY shows the day the
series' countdown regardless — use `future` or edit the series. Notes HEY serves only as
plain text, so an edit that
would send notes back as text is refused unless `--allow-plain-notes` accepts the loss or
`--notes` replaces them; an event with no notes needs neither. A `future` edit builds the
new series from the series' own guest list and sends invitations, so a day whose guests
differ from the series' is refused until `--invite` names the new list. With
`--occurrence`, `--calendar` is only the calendar the day moves to: the day is read over
every calendar, and a day already moved elsewhere stays there. A day HEY has written out
lists in `day`/`week` with its own event ID in `id` and `recording_id`, plus the series in
`parent_id`; `edit <id>` changes that day alone. Use the series id as the positional id
with `--occurrence`.

**Deleting follows the same rule.** `hey event delete <id>` deletes the whole event, a
series included. One day is `hey event delete <series id> --occurrence <occurrence_id>
--apply-to current`; `future` deletes that day and every one after it, which from the
series' first day is the whole series. A written-out day's own ID is refused with a usage
error naming the occurrence command, because deleting that ID alone lets HEY draw the day
again from the series. The check reads the event a year either side of today, so a
written-out day further out than that is not caught — use the occurrence form for it.
`future` is refused from a written-out day moved off its series time or on an opaque
custom schedule, as for edits. An attached email you cannot read is
not served and is detached by any
edit, whole event or one day — nothing client-side can keep it. HEY answers not-found for
a date that is not a day of the series and for a series you cannot edit alike. The JSON
envelope is the one every mutation writes: `summary` (`Occurrence updated` or `Occurrence
and the following updated`) and `data` holding the recording HEY answered.

On `add`, no `--start-time` means all-day, a `--start-time` with no `--end-time` runs an
hour, and clock times are read in `--time-zone`, defaulting to the machine's zone. On
`edit`, an end or zone not named keeps the event's own.

### Todos

```bash
hey todo list --json                          # Todos on the personal calendar, 4 years back to 1 year ahead
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
hey habit list --json                        # List habits and their IDs
hey habit list --date 2026-09-02 --json      # The habits in that date's week
hey habit create "Morning strength training" # Create with weights, blue, every day
hey habit create "Practice piano" --icon music --color green --days mon,wed,fri
hey habit edit 123 --name "Evening walk"      # Omitted fields remain unchanged
hey habit edit 123 --days 0,6                 # Sunday and Saturday
hey habit delete 123                          # Permanently delete habit and history
hey habit complete 123                        # Mark habit complete for today
hey habit complete 123 --date 2026-03-15      # Mark complete for specific date
hey habit uncomplete 123                      # Unmark habit for today
```

Habit IDs come from `hey habit list --json`, which reads the week a date falls in. A week
lists each habit once, whatever weekday it runs on; a week that has not started yet lists
none. Days accept full
weekday names, common abbreviations, or `0` (Sunday) through `6` (Saturday).

### Time Tracking

```bash
hey timetrack start                           # Start timer
hey timetrack stop                            # Stop timer
hey timetrack stop --category "Client work"  # Stop and file under a category (created if missing)
hey timetrack current --json                  # Show current timer
hey timetrack list --json                     # First page of completed tracks, most recently ended first (--all for every page)
hey timetrack list --category 42 --json       # Only one category, by ID
hey timetrack edit 1042 --start 2026-08-22T09:00 --end 2026-08-22T11:15
hey timetrack edit 1042 --category "Client work" --notes "Invoice review"
hey timetrack delete 1042                     # Delete a time entry
hey timetrack export > tracked-time.csv        # Write the complete CSV export
hey timetrack export -o tracked-time.csv --json # Save the CSV, return file metadata (--force to replace a file)
hey timetrack categories --json               # List categories
hey timetrack category create "Client work"   # Create a category
hey timetrack category rename 123 "Planning"  # Rename a category
hey timetrack category delete 123              # Delete a category
```

`list` leaves out the running track; read it with `current`. `edit` changes only the fields
it is given, takes `YYYY-MM-DDTHH:MM` in the local zone (or a full RFC 3339 instant), and
completes the track, so it is for finished tracks. A category is a title: `--category` files
under it and HEY creates it if missing. A blank `--category` is refused — once filed, a track can only be moved to another category.

Without `--output`, `hey timetrack export` writes CSV to stdout — redirect it to a file.
The output formatting flags cannot reshape a CSV, so they are refused with a usage error
unless `--output` is given; with it, `--json`, `--quiet` and `--markdown` format the file's
metadata (`--ids-only` and `--count` still fail, as it is not a list). `--html` is never
accepted. An existing file needs `--force`.

### Journal

```bash
hey journal list --json                       # Entries on the personal calendar, 4 years back to 1 year ahead
hey journal read 2026-03-15 --json            # Read entry by date
hey journal write "Shipped the pagination fix and paired with Jane on the cover art."
hey journal write 2026-03-15 "Retrospective: the migration took two days longer than planned."
hey journal write                             # $EDITOR at a terminal; otherwise the entry is read from stdin
```

Content that trims to nothing — a whitespace-only argument, `--content` or stdin, or an
emptied `$EDITOR` buffer — **removes** the day's entry, and the command says "removed"
rather than "saved"; only do that when removal is the intent. (A literal `""` is treated as
no content and falls through to stdin or `$EDITOR`.) A day with no entry is not an error:
`--json` answers `ok` with the summary "No journal entry for <date>" and no `data`.

### Authentication

Data commands use the credentials HEY already stores and refresh expiring OAuth tokens automatically. Run the requested data command without a login preflight. Use `hey auth status --json` when the user asks for authentication status or when an explicit authentication check helps diagnose a failure; it reports whether credentials are available without changing them.

If a data command returns exit code 3 with `"code": "auth"`, or fails with `could not read stored credentials`, authentication is unavailable to that process. On macOS under Codex, the sandbox can hide credentials that the HEY CLI stores in Keychain. Before reporting the task as blocked, use the harness's normal approval flow to retry `hey auth status --json` once with elevated sandbox permission. Limit the escalation to this one read-only command. If the response's `data.authenticated` is `true`, rerun only the exact `hey` command the user requested through a separate one-command approval. If `data.authenticated` is `false` or the status command fails, report the task as blocked and tell the user to run `hey auth login`; do not run it for them unattended.

Never run the macOS `security` command to read Keychain contents, print or copy credentials, or move credentials into a file; never disable the sandbox globally; never set `HEY_NO_KEYRING=1` as an authentication workaround.

Piped, machine-output and non-TTY commands do not prompt for sign-in. When an agent harness runs commands under a PTY, set `HEY_NONINTERACTIVE=1` so a missing login returns the same actionable auth error instead of opening an interactive prompt.

```bash
hey auth status --json                         # Inspect stored authentication without changing it
HEY_NONINTERACTIVE=1 hey box list              # Under a PTY, even styled output never prompts
hey auth login                                 # Interactive browser recovery, with the user present
hey auth logout                                # Log out
hey login / hey logout                         # Shortcuts for the two above
hey setup omarchy                              # Omarchy only: put HEY in the bar. The interactive
                                               # sign-in offer never fires for agents (non-TTY,
                                               # machine output), so this command is the way
hey setup                                      # First-run wizard: sign in + connect coding agents
HEY_NONINTERACTIVE=1 hey setup --json          # No prompts and no OAuth wait — but still
                                               # installs shell completions, agent skills and
                                               # the Claude Code plugin, and records onboarding;
                                               # use `hey doctor` to inspect without changes.
                                               # (Without HEY_NONINTERACTIVE, a terminal on
                                               # stdin still starts browser sign-in.)
```

Run `hey auth login` only when the user is present and explicitly asks to authenticate.

### Sender selection

`account senders` lists configured sender IDs and addresses within `--account`.
`compose --from <email-or-id>` (new messages only, not with `--thread-id`) sends directly as that sender and applies its active
Name Tag unless `--no-name-tag` is set; `--draft` saves without sending.
`draft edit --from` stays in the draft's account and preserves the existing body,
including signatures. Replace the body explicitly when needed. `draft show`
includes From. These commands do not persist a default.
