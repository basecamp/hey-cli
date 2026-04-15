---
name: hey
description: |
  Interact with HEY email via the HEY CLI. Read and send emails, manage boxes,
  calendars, todos, habits, time tracking, and journal entries. Use for ANY
  HEY-related question or action.
triggers:
  # Direct invocations
  - hey
  - /hey
  # Email actions
  - hey boxes
  - hey box
  - hey threads
  - hey reply
  - hey compose
  - hey drafts
  # Calendar actions
  - hey calendars
  - hey recordings
  # Todos
  - hey todo
  # Events
  - hey event
  # Seen/unseen
  - hey seen
  - hey unseen
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
  - compose email
  - list mailboxes
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

CLI for HEY email: mailboxes, email threads, replies, compose, calendars, todos, habits, time tracking, and journal entries.

## Agent Invariants

**MUST follow these rules:**

1. **Always use `--json`** for structured, predictable output
2. **Authentication required** for all data commands — run `hey auth login` first
3. **HTML output** is available via `--html` for commands that return HTML content

## Quick Reference

| Task | Command |
|------|---------|
| List mailboxes | `hey boxes --json` |
| List emails in a box | `hey box imbox --json` |
| Read email thread | `hey threads <topic_id> --json` |
| Reply to email | `hey reply <topic_id> -m "Thanks!"` |
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
| List events | `hey event list --json` |
| Create event | `hey event create --title "Sync" --date 2024-06-15 --start 09:00 --end 10:00` |
| Edit event | `hey event edit 123 --title "Updated"` |
| Delete event | `hey event delete 123` |
| Mark as seen | `hey seen 12345` |
| Mark as unseen | `hey unseen 12345` |
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
| Launch TUI | `hey` |

## Decision Trees

### Reading Email

```
Want to read email?
├── Which mailbox? → hey boxes --json
├── List emails in box? → hey box <name|id> --json
├── Read full thread? → hey threads <topic_id> --json
├── Mark as seen? → hey seen <posting-id>
├── Mark as unseen? → hey unseen <posting-id>
└── Launch interactive UI? → hey (no args, launches TUI)
```

### Sending Email

```
Want to send email?
├── Reply to thread? → hey reply <topic_id> -m "message"
│   └── Open editor? → hey reply <topic_id> (omit -m to open $EDITOR)
├── Compose new? → hey compose --to <email> --subject "Subject"
│   ├── With body? → hey compose --to <email> --subject "Subject" -m "Body"
│   ├── With CC? → add --cc <email>
│   └── With BCC? → add --bcc <email>
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

**Response format:** `hey box` returns `{"box": {...}, "postings": [...]}`. Each posting has: `id` (posting ID), `topic_id` (topic ID), `name` (subject), `seen` (read status), `created_at`, `contacts`, `summary`, `app_url`. Use `topic_id` for `hey threads` and `hey reply`.

### Email - Threads

```bash
hey threads <topic_id> --json                 # Read full email thread
hey threads <topic_id> --html                 # Read with raw HTML content
```

**ID note:** `hey box` returns postings with an `id` (posting ID) and a `topic_id` (topic ID). `hey threads` and `hey reply` expect the **topic ID** — use `topic_id` directly. The `app_url` field also contains the topic ID as a fallback (e.g. `https://app.hey.com/topics/123` → `123`).

### Email - Reply & Compose

```bash
hey reply <topic_id> -m "Thanks!"             # Reply with inline message
hey reply <topic_id>                          # Reply via $EDITOR
hey compose --to user@example.com --subject "Hello"         # Compose new (opens $EDITOR)
hey compose --to user@example.com --subject "Hi" -m "Body"  # With inline body
hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Project update" -m "Body"  # With CC/BCC
hey compose --subject "Update" --thread-id 12345 -m "msg"   # Post to existing thread
```

### Email - Seen/Unseen

```bash
hey seen 12345                                # Mark posting as seen
hey seen 12345 67890                          # Mark multiple postings as seen
hey unseen 12345                              # Mark posting as unseen
hey unseen 12345 67890                        # Mark multiple postings as unseen
```

Takes posting IDs (the `id` field from `hey box` output).

### Drafts

```bash
hey drafts --json                             # List drafts
```

### Calendars

```bash
hey calendars --json                          # List calendars (returns array of {id, name, kind})
hey recordings 123 --json                     # List events in calendar
```

**Response format:** `hey recordings` returns recordings grouped by type (e.g. `{"Calendar::Event": [...], "Calendar::Habit": [...], "Calendar::Todo": [...]}`). Each recording has: `id`, `title`, `starts_at`, `ends_at`, `all_day`, `recurring`, `starts_at_time_zone`. Access by type key in jq, e.g. `.["Calendar::Event"]`.

### Todos

```bash
hey todo list --json                          # List all todos
hey todo add "Task description"                        # Add a todo
hey todo complete 123                         # Mark complete
hey todo uncomplete 123                       # Mark incomplete
hey todo delete 123                           # Delete a todo
```

### Events

```bash
hey event list --json                         # List events (personal calendar by default)
hey event list --calendar 123 --limit 10 --json  # List events in a specific calendar
hey event create --title "Team sync" --date 2024-06-15 --start 09:00 --end 10:00
hey event create --title "Holiday" --date 2024-06-15 --all-day
hey event create --title "Review" --date 2024-06-15 --start 14:00 --end 15:00 --reminder 30m --reminder 1h
hey event edit 123 --title "Updated standup"  # Edit any subset of fields
hey event delete 123                          # Delete an event
```

Reminder durations: `30m`, `1h`, `2d`, `1w` (repeat `--reminder` for multiple). Timezone defaults to local; override with `--timezone` (IANA name).

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
