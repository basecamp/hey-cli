# CLI reference

Every command documents itself: `hey <command> --help` shows its usage, flags and
examples, and `hey commands` lists the whole catalog. This page is the narrative that the
help text does not carry: how the output formats fit together, how IDs flow from one
command to the next, and what HEY does with each write.

## Help and reference

The root help groups commands by the work they do and keeps the common output flags concise:

```bash
hey --help                # browse the command summary
hey compose --help        # see a command's usage, flags, and examples
hey commands              # list the complete executable command catalog
```

Cross-cutting references are available as help topics:

```bash
hey help output            # output formats, selectors, and jq filtering
hey help exit-codes        # stable process exit statuses
hey help environment       # supported HEY_* environment variables
hey help linked-accounts   # account selection and precedence
```

## Setup

`hey setup` runs the first-run wizard again at any time: browser sign-in, a check of who
you are signed in as, and connecting the coding agents it detects (Claude Code, Codex).
`--skip-agents` leaves agent integrations unchanged and `--skip-omarchy` leaves the
Omarchy integration unchanged. `--silent-success` keeps any required sign-in visible,
shows an installation spinner, and ends a successful run with `SETUP COMPLETE`; failure
guidance is still shown.

At a terminal, a logged-out data command (`hey box list`, say) offers to sign you in on
the spot. Piped or `--json` runs never prompt: they fail with `Not logged in` (exit 3) so
scripts and agents can handle it.

## Authentication

```bash
# Browser-based OAuth against HEY's own OAuth server (primary method)
hey auth login

# Or use a pre-generated token
hey auth login --token TOKEN

# Or use a browser session cookie
hey auth login --cookie COOKIE
```

Tokens refresh automatically on expiry. Credentials are stored in the system keyring (with file fallback at `~/.config/hey-cli/credentials.json`).

```bash
hey auth status   # check auth status
hey auth token    # print the bearer token for scripting (refuses a --cookie login)
hey auth refresh  # force token refresh
hey auth logout   # clear credentials
```

`hey login` and `hey logout` are top-level shortcuts for `hey auth login` and `hey auth logout`.

### Linked accounts

One HEY login exposes every mail account linked to that identity. List the available
filters, persist a default, or select one for a single invocation:

```bash
hey account list                 # list All Accounts and each linked account
hey account senders              # list sender IDs, addresses, account IDs, and defaults
hey account use 12345            # persist a linked account as the default mail filter
hey account use all              # return to All Accounts
hey --account 12345 boxes         # override the default for one invocation
HEY_ACCOUNT_ID=12345 hey search "quarterly planning"
```

The default is `all`. Selection precedence is `--account`, `HEY_ACCOUNT_ID`, trusted local
`.hey/config.json`, the global default for the active server, then All Accounts. Global
account defaults are stored separately for each server origin, so development and production
selections cannot affect one another. Explicit and persisted IDs are validated against the
signed-in identity before mail requests, so an unavailable account fails closed.

The first command that would use a repository-local server or account setting asks whether to
use it once, always trust its current values, or cancel. Non-interactive and JSON commands fail
closed until you explicitly run `hey config trust-local` from that directory. Changes to the
local server or account invalidate trust. Review trust with `hey config trusted-locals` and
remove it with `hey config untrust-local`.

Compose and contact creation use an individually selected account; replies and forwards use
the thread's account. Calendars, todos, habits, time tracking, and journal entries remain
identity-wide.

## Output formats

Structured data commands support `--json` for full output and `--jq '<expression>'` to
filter that output without an external `jq` binary. `--jq` implies `--json` and filters
the full success envelope; combine it with `--quiet` to filter result data directly.
Errors retain their complete structured envelope. Commands with dedicated raw output
(`hey auth token`, `hey shell-completion generate`, `hey setup`, `hey skill`, `hey tui`,
and `hey --version`) reject `--jq`.

Use `--base-url` to override the server URL and `--account <id|all>` to select a linked
mail account.

```bash
hey box list --jq '.data[] | {id, name}'
hey box list --quiet --jq '.[].id'
```

Listing commands also answer `--markdown` for a table, `--styled` to force the human
rendering when the output is piped, `--ids-only` for one ID per line, and `--count` for a
bare number. `--ids-only` and `--count` need list data, so they work on `hey box list`,
`hey box view`, `hey bundle view`, `hey label list`, `hey label view`, `hey collection list`, `hey collection view`,
`hey workflow list`, `hey workflow view`, `hey clip list`, `hey snippet list`, `hey draft list`, `hey search`, `hey contact list`, `hey contact threads`, `hey screener list`, `hey screener history`, `hey calendar list`,
`hey event list`, `hey event day`, `hey event week`, `hey todo list`, `hey habit list`,
`hey timetrack list` and `hey journal list`.
Both answer
for what was read — the first page unless `--all` or `--limit` reads more — and a notice
that more pages exist, where a command writes one, goes to stderr so the IDs on stdout stay
pipeable. `hey clip list --ids-only` and `--count` cover the newest page only because the
released SDK does not expose HEY's cursor for older clip pages.

`--html` writes the original HTML, for the commands that hold some: `hey thread read`,
`hey journal read`, and a contact's private note (`hey contact show` or `hey contact note show`). It is a format of
its own — it cannot be combined with the other output flags (`--stats` included: there is
no envelope to carry stats), every other command refuses it, and it is meant for a file or
a pipe: on a terminal it is refused with the redirect spelled out, since markup on a
terminal is neither readable nor safe.

A thread is written as one HTML5 document, so a downstream tool can parse it rather than
split it: `<!doctype html>`, `<html lang="en">`, a `<head>` with `<meta charset="utf-8">`
and `<title>Thread N</title>`, then in `<body>` one
`<article id="entry-ID" data-entry-id="ID" data-created-at="…" data-body-state="…">` per
entry, oldest first. Each article opens with a `<header>` naming the sender, date and
non-empty To, CC and BCC lines (sanitized and HTML-escaped), then holds the entry's HTML
exactly as HEY served it. An entry without
a body holds only its header, and `data-body-state` says why — `bodyless` when HEY served
none, `over_limit` or `failed` when the load left it unread, `hydrated` when it was read
and was empty. A thread that could only be read in part is refused as for every other
format; with `--allow-partial` the document ends with the notice in an HTML comment
(`<!-- notice: … -->`) just before `</body>`, and the notice goes to stderr as well.

A single body — a journal entry, a contact's note — is written as a fragment instead: the
HTML as HEY served it, nothing for an empty one. A thread has entries to frame; one body
is what gets pasted into something else.

## Email

Resource commands use one noun-first family: `box`, `label`, `collection`, `workflow`,
`clip`, and `snippet`, with actions such as `list` and `view` underneath. The earlier
plural listing forms and direct detail forms remain supported for compatibility; `hey
commands --json` identifies each plural form with `compatibility_for` while the primary
help and examples show the canonical family. `list` and `view` are reserved action names
under `box`; a box with either display name is addressed explicitly (`hey box view list`)
or through the direct-form escape (`hey box -- list`).

```bash
hey box list                         # list mailboxes
hey box view imbox                   # list email threads in a box (by name or ID)
hey bundle view 456                  # list the unseen threads a bundle row groups
hey label list                       # list labels and their IDs
hey label view 789 --all             # list all email threads with a label
hey label add 12345 --to 789         # add a label to a thread
hey label create "Travel receipts" 12345  # create and add a label
hey label remove 12345 --from 789    # remove one label
hey label remove 12345 --from all    # remove every label
hey collection list                  # list collections and their IDs
hey collection view 321 --all        # list every thread in a collection
hey collection create "Kitchen remodel" --summary "Plans and decisions"
hey collection update 321 --name "Kitchen renovation"
hey collection add 987 --to 321      # add a topic ID to a collection
hey collection remove 987 --from 321 # remove a topic ID from a collection
hey set-aside view                   # list Set Aside threads with their group
hey set-aside group list             # list Set Aside groups and their thread counts
hey set-aside group view 42          # list the threads in a group
hey set-aside group create 12345 67890  # gather threads into a new group
hey set-aside group add 12345 --to 42   # file a thread into a group
hey set-aside group remove 12345     # take a thread out of its group
hey set-aside group delete 42        # break a group up (threads go to Previously Seen)
hey workflow list                    # list workflows, account IDs, and workflow IDs
hey workflow view 654                # list a workflow's stages and stage IDs
hey workflow create "Hiring" --account 12345
hey workflow update 654 --name "Recruiting"
hey workflow stage create 654       # add an Untitled stage
hey workflow stage update 654 321 --name "Interviewing"
hey workflow add 987 --to 654 --stage 321       # add a topic ID to a stage
hey workflow move 987 --workflow 654 --to 322   # move it to another stage
hey workflow remove 987 --from 654              # remove it from the workflow
hey clip list                                     # newest page of saved passages and source context
hey clip create 456 --content "The launch moves to Wednesday."
hey clip delete 44
hey snippet list                                  # list reusable email snippets
hey snippet create --name "Scheduling reply" --content "Tuesday works for me."
hey snippet update 44 --content "Wednesday works for me."
hey snippet delete 44
hey search "quarterly planning"    # search threads and matching messages
hey search --from jane@example.com --date last_30_days  # refine a search
hey search filters                 # list available refinement values
hey contact list                  # list contacts
hey contact show 12345            # view a contact and private note
hey contact threads 12345         # list every thread with a contact, seen and unseen
hey contact add --name "Jane Doe" --email jane@example.com
hey contact update 12345 --name "Jane Dawson"
hey contact hide 12345            # hide without permanently deleting
hey contact show-again 12345      # show a hidden contact again
hey contact bundle 12345          # group this contact's mail into one row
hey contact unbundle 12345        # list this contact's mail separately
hey contact note set 12345 "Prefers email"
hey contact note show 12345       # read the private note
hey contact note delete 12345
hey screener list                  # who is waiting to be screened
hey screener list --count          # just the number waiting
hey screener approve 91            # let a sender through
hey screener approve 91 --box "The Feed"  # let them through, into another box
hey screener deny 91 92            # turn several senders away
hey screener deny 91 --spam        # turn them away and mark what they sent as spam
hey screener history               # who has already been screened
hey screener clear                 # trash everything waiting, deciding no one
hey thread read 123                    # read a full email thread
hey thread read 123 --markdown         # the thread as one Markdown document
hey thread read 123 --html > 123.html  # HEY's original HTML, to a file
hey share 123                      # get a sharing link for a thread
hey unshare 123                    # turn off the sharing link
hey attachment list 123                # list files attached to the thread
hey attachment save 456:1         # save a file using its attachment ID
hey reply 123 -m "Friday works for me — I'll send an agenda."  # or omit -m for $EDITOR
hey reply 123 --to support@example.com -m "The replacement is on the way."
hey reply 123 --to support@example.com --replace-recipients --dry-run --json
hey reply 123 -m "Here is the wiring diagram." --attach ./diagram.png
hey bulk-reply preview 12345 67890  # inspect threads and exact To/CC/BCC recipients
hey bulk-reply send 12345 67890 -m "Thanks for the update."
hey bulk-reply undo 98765            # recall a delayed bulk reply
hey forward 123 --to alice@example.com -m "For your review"  # forward the latest message
hey compose --to alice@example.com --subject "Lunch plans"  # body from $EDITOR at a terminal, otherwise stdin
hey compose --to alice@example.com --subject "Q3 revenue report" -m "The numbers are attached." --attach ./report.pdf
hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Kitchen remodel timeline" -m "Cabinets land the week of the 14th."  # with CC/BCC
hey compose --to alice@example.com --subject "Sprint recap" -m "We **shipped** the pagination fix."
hey compose --to alice@example.com --subject "Newsletter draft" --message-html "<h1>March</h1><p>What we shipped.</p>"
hey compose --subject "Board update" -m "Numbers to follow." --draft  # save a draft instead of sending
hey compose --to alice@example.com --subject "Sprint recap" -m "Shipped." --no-name-tag  # leave your HEY name tag off
hey reply 123 -m "Drafting a longer answer." --draft  # save a reply draft
hey draft list                     # list drafts (--all and --page follow HEY's cursor)
hey draft show 12345               # read a draft back
hey draft edit 12345 --to alice@example.com --subject "Board update (v2)"
hey draft edit 12345 --from billing@example.org
hey compose --from billing@example.org --to alice@example.com --subject "Board update" -m "Numbers to follow." --draft
hey draft send 12345               # deliver it
hey draft delete 12345             # trash it
hey seen 12345                     # mark a thread as seen
hey unseen 12345 67890             # mark threads as unseen
hey move 12345 --to feed           # move a thread to another box
hey move 12345 67890 --to imbox    # remove Reply Later from seen threads
hey move 12345 67890 --to "paper trail"  # move multiple threads
hey bubble up 12345 --now          # bubble a thread up to the top of the Imbox
hey bubble up 12345 --on 2026-09-04  # bubble a thread up on a date
hey bubble up 12345 --weekend      # bubble a thread up Saturday morning
hey bubble list                    # list bubbled-up and scheduled threads
hey bubble pop 12345               # cancel a thread's bubble-up
hey trash 12345                    # move a thread to Trash
hey spam 12345                     # mark a thread as spam
hey ignore 12345                   # ignore future activity on a thread
hey stop-ignoring 12345            # resume attention for a thread
```

`hey thread read` reads a whole thread, oldest entry first, however many pages HEY serves it in — within limits it states: a hundred pages past the first, two thousand entries, as many bodies, 64 MiB of retained thread data and two minutes in all. The byte budget covers entry-index metadata, message bodies and metadata, the recipient identities retained for thread output, and inbound delivery addresses and resolved contact identities retained for JSON. A thread that could only be read in part — a body HEY would not serve, a limit reached — is refused rather than passed off as whole; `--allow-partial` takes what was read, with a `notice` saying what is missing and each entry's `body_state` saying whether its body was `hydrated`, `bodyless` (HEY served none), `over_limit` or `failed`. Each entry whose message was read carries `recipients`, with `to`, `cc` and `bcc` contact lists; the object is absent when the message was not read, while a known-empty line is `[]`. `creator` is the entry's author — the external sender for inbound mail, your own contact for mail you sent; if you sent from a different address (an alias, custom domain or external account), the hydrated entry also has `sender` with that address, and the displayed From uses it. A missing `sender` does not mean the same sender was verified: the index-only formats and entries whose message was not read have no sender data. In JSON, an inbound entry also carries `received_via`: every exact account address HEY recorded the message arriving through, including plus tags and catch-all aliases. These are delivery records, not the visible To/CC/BCC recipients. A record's `contact` is optional and is omitted when HEY did not resolve one; the whole field is omitted for sent or generated messages and whenever the message was not read. `--count` and `--ids-only` read the entry index and no messages, so only a truncated index can make them partial. `--markdown` writes the thread as one Markdown document — a heading per entry naming the sender, date and ID, then the body — which is the shape to hand an agent or a notes app. `hey attachment list` reads the bodies in every format, since that is where attachment metadata lives, and answers a partial thread the same way. `hey reply` answers the thread's latest entry and addresses the reply the way HEY does: it asks HEY for the reply's recipients — everyone that entry was addressed to, its sender moved onto the To line, and your own addresses, aliases and catch-alls excluded — falling back to the latest message's metadata for a send when that read is unavailable.

Repeatable `hey reply --to`, `--cc` and `--bcc` flags add recipients to that envelope; comma-separated addresses also work. An explicitly named address moves to that line instead of appearing twice. `--replace-recipients` discards HEY's prefill and requires at least one explicit address. `--dry-run` needs no body, does not read the original message body, uploads nothing and sends nothing; its JSON data reports the account, thread, entry, subject, resolved sender, and final To, CC and BCC lists. If HEY's envelope prefill is unavailable, a dry run refuses to guess the original recipient lists; use `--replace-recipients` with explicit addresses to preview a complete replacement instead.

Email bodies come back as Markdown. `hey thread read` and the TUI render that Markdown for the terminal — headings, emphasis, lists, quotes, tables and code survive, and links keep their URLs and stay clickable where the terminal supports it. `--json` carries the same Markdown in `body`, so an agent reading a thread sees the structure a human sees rather than a flattened wall of text. `--html` keeps HEY's original body HTML and frames each entry with its From, To, CC and BCC headers.

Writing is Markdown too, for message bodies, drafts, journal entries, snippets and contact notes: `-m`, `--content`, `--note`, positional content, stdin, and `$EDITOR` (which opens prefilled with the existing entry or note as Markdown). Every such flag has a raw-HTML twin — `--message-html`, `--content-html`, `--note-html` — for sending markup verbatim; each pair is mutually exclusive. The TUI's compose and bulk-reply forms convert Markdown the same way, and the compose editor renders it live as you type — `**bold**` turns bold, markers and all. A fenced code block's language (` ```ruby `) is carried the way HEY's own editor stores it, so the web app syntax-highlights it — for the languages HEY highlights (Ruby, Python, JavaScript, TypeScript, Go, Rust, Java, C#, C++, PHP, Swift, HTML, CSS); any other is dropped. Clip passages, event notes and time track notes are plain text.

Drafts are the review-before-send lane: `hey compose --draft` (and `hey reply --draft`) saves instead of sending — recipients optional on a draft — and answers the draft's ID. `hey draft show` reads it back with the body as Markdown, `hey draft edit` revises it (each flag replaces its field; what is not flagged is kept, by reading the draft and resending the whole of it, since a revision is not a patch on HEY's side), `hey draft send` delivers through HEY's undo window, and `hey draft delete` trashes it. Scheduling a delivery is done in a HEY app for now — the CLI has no flag for it, and HEY's API schedules only to a whole hour — and a schedule set there survives CLI edits untouched. A draft prepared here is reviewed and sent from any HEY app, which is the workflow this is for: an agent writes, a person decides.

`hey share <thread_id>` gets a sharing link for a thread. Anyone with the link can see the entire thread and future emails or replies sent to it. `hey unshare <thread_id>` turns off the sharing link.

Search accepts free text plus `--required`, `--any`, `--none`, `--exact`, `--from`, `--to`, `--subject`, `--date`, `--in`, `--label`, and `--attachment`. `--in`, `--date`, `--label` and `--attachment` take one of the values `hey search filters` lists — the attachment kinds are `any`, `images`, `pdfs`, `calendar_invites`, `documents`, `spreadsheets`, `presentations`, `media` and `zip_files`, so it is `--attachment pdfs` rather than `pdf`, and an unrecognized `--in`, `--date` or `--attachment` is refused with the values it accepts before anything is sent. Use `--page` for one page or `--all` to fetch up to 100 pages; capped searches report the next page for continuation. Search results include `topic_id` for reading the thread and the matching message summaries. Results also include `id` for organization actions, except when you have no box item for the thread.

Contact updates preserve omitted name, email, and alias fields. Supplying `--alias` replaces the complete alias list; `--alias=` clears it. Contact notes accept positional content, `--note`, stdin, or `$EDITOR`. HEY hides contacts rather than permanently deleting them; hidden contacts leave lists, autocomplete, and search, and can be shown again by ID. Bundling groups a contact's mail into one row without merging or deleting the underlying threads; unbundling lists those threads separately again. HEY bundles only a contact with no box preference or one sent to the Paper Trail; for any other, bundling answers success and changes nothing. A contact that is hidden, an alias, or another HEY user cannot be edited: updating it or setting or deleting its note answers `not_found`. `hey contact list` holds only non-HEY senders you have screened in — other HEY users, senders still in or denied by The Screener, aliases and hidden contacts are left out.

The Screener is where first-time senders wait. `hey screener list` returns clearance IDs — not contact IDs — with the sender and the subject of what they sent, plus `topic_id` for reading the thread before deciding. `--count` asks for the number alone, which is a far cheaper request than the queue, and prints it as a bare number like every other command's `--count`, so `n=$(hey screener list --count)` reads it directly. Approving delivers everything the sender has waiting; denying hides it. Either is reversible with the opposite command, and `hey screener history` shows what was already decided. Both listings page the way `hey box view` does: `--all` follows HEY's cursor to the end of the queue (up to 100 pages), and a single-page or capped read reports `next_page` in its JSON meta, which `--page <next_page>` continues from — the cursor is opaque, so a page *number* does not name a position. `hey screener list` also reports `total_count`, the whole queue's size, next to what the read returned. `--box` and `--seen` approve one sender at a time; several IDs go through HEY's bulk endpoint, which takes neither. `--seen` reliably marks the mail read only for a sender with one waiting thread. `--spam` marks what they sent as spam and, for a single ID, trains HEY's filter, which is harder to undo than denying. `hey screener clear` moves everything waiting to Trash (a shared thread loses your access instead) without approving or denying anyone — those senders reappear on their next email.

`hey bulk-reply preview` is read-only and resolves each posting to its latest replyable entry. `hey bulk-reply send` resolves the selection again, skips threads without a replyable entry, keeps HEY's server-provided name tag, and returns the exact reply count, delivery ID, delayed state, undo URL, and undo command. Posting IDs must be positive and unique. The message can come from `-m`, stdin, or `$EDITOR`; `--attach` is repeatable.

A new message from `hey compose` — sent or saved with `--draft` — ends with the sender's HEY name tag, appended the way HEY's own compose form does; HEY puts the tag into the form rather than onto the saved message, so the CLI carries it itself. `--no-name-tag` leaves it off. A reply does not carry one yet.

Choose a new message's sender with `compose --from <email-or-sender-id>`. Discover
addresses and IDs with `hey account senders`; `--account` filters the listing and
limits sender selection. In All Accounts, an ambiguous address requires a sender
ID or a specific account. Unknown and unavailable senders fail before reading
stdin, opening the editor, or uploading attachments.
The selected sender's active Name Tag is appended unless `--no-name-tag` is set.
`--from` is currently only for new messages, not `compose --thread-id` replies.

With `--from`, compose sends directly as the selected configured sender.
`--draft` saves with that sender instead, ready for `draft show`, `draft edit`,
`draft send`, or review in a HEY app.

`draft edit --from <email-or-sender-id>` changes the sender within the draft's
existing account. As with other field flags, the omitted body remains byte-for-byte
intact, including any existing signature or Name Tag. Use a body flag to replace
it when needed. `draft show` includes the selected From address in both styled
and JSON output. No command here persists a default sender.

`--attach` is repeatable on `hey compose`, `hey reply`, and `hey bulk-reply send`, and attachment-only messages are supported. The CLI validates and uploads every file before sending the email. `hey attachment list <thread-id>` returns every named downloadable file, including named inline images. Direct files keep stable message-and-position IDs such as `456:1`; files inside embedded HTML receive opaque IDs scoped to their message. Pass either returned ID to `hey attachment save`. Saving uses the original filename by default, accepts `--output` for a file or directory, and preserves existing files unless `--force` is set.

Organization actions take the `id` values returned by `hey box view --json`, `hey label view --json`, or `hey search --json`. Reading, replying to, and forwarding a thread take its `topic_id` instead, which `hey box view --json`, `hey label view --json`, `hey collection view --json` and `hey search --json` all carry alongside `id`. `hey box view` also returns `next_page` and accepts `--page <next_page>` to continue a box listing; it keeps `next_history_url` for the sync clients that read it, and `--page` accepts that URL as readily as the cursor inside it. Label IDs come from `hey label list`; `hey label view` returns `next_page` and `total_count`, accepts `--page <next_page>` for continuation, and supports `--all` for complete traversal. `hey label create` files at least one thread as it creates the label, so it needs one or more of your box item IDs; a name you already use (in any case) is refused.

Collection IDs come from `hey collection list`. `hey collection view` returns both each posting `id` and its `topic_id`, plus `next_page` and `total_count`. Collection membership commands take `topic_id`; posting organization commands continue to take `id`. Creating a collection returns a confirmed mutation, and `hey collection list` provides its ID for subsequent commands. Collection updates accept a non-empty name, summary, or both.

Set Aside groups have no name in HEY; a group is its ID and its threads. `hey set-aside view` lists the same threads as `hey box view set-aside` and adds each thread's `box_group_id` (a `Group` column in the styled table). HEY's group index answers with IDs alone, so `hey set-aside group list` reads each group once for its thread count. `hey set-aside group view <group-id>` lists a group's threads with `next_page` and `total_count`, accepts `--page <next_page>` and `--all` like the other listings, and answers `not_found` for a group that is gone; HEY removes a group itself once its last thread leaves it. `group create`, `group add` and `group remove` take posting `id` values; `group view`, `group add --to` and `group delete` take a group ID from `group list`. `hey set-aside group create` and `hey set-aside group add` move threads into Set Aside if they are elsewhere, and then complete the move the way `hey move --to set-aside` does: the thread is marked seen, which also clears a bubble-up, since HEY keeps "bubbled up" as a seen state rather than a flag and a thread cannot be set aside and bubbled up at once. A group HEY refuses fails the command before any thread is changed. `hey set-aside group remove` leaves threads in Set Aside outside any group, while `hey set-aside group delete` sends the group's threads to Previously Seen, which is what HEY does when a group is dissolved in the web app.

Workflow IDs come from `hey workflow list`, which includes the linked account ID for each workflow. `hey workflow view <id>` returns stages in position order; `--ids-only` and `--count` apply to those stages. Creating a workflow needs one linked mail account, selected with `--account` when more than one is available. HEY creates new stages as `Untitled`, so create the stage, read its ID with `hey workflow view <id>`, then rename it. Workflow membership commands take `topic_id`. Adding a thread creates its workflow membership before selecting the requested stage; if stage selection fails, the thread remains in the workflow's first stage and the command reports the error.

Clips are passages saved from existing email entries. `hey clip list` lists the selected account's newest page with each clip's source entry and thread context; its JSON `notice` and the data-only formats' stderr make that boundary explicit because the released SDK does not expose HEY's cursor for older pages. `hey clip create <entry-id> --content <text>` verifies that the passage is source-backed by text carried in the entry, including embedded inbound email bodies. It accepts whitespace differences while preserving the supplied text exactly for HEY's web UI; passages are capped at 64 KiB and source-message validation at 1 MiB. HEY's web UI remains authoritative for stylesheet-driven visibility. HEY assigns a created clip to its source entry's account and resolves deletion by identity-owned clip ID across linked accounts; `--account` selects list presentation. `hey clip delete <clip-id>` removes it. Clip content is plain text; the source entry ID comes from `hey thread read --json`.

Snippets are named reusable email content, separate from clips saved out of received messages. `hey snippet list` lists both plain text and HEY's rich-text HTML; `hey snippet create`, `update`, and `delete` manage them. A create requires a non-empty name and content. Updates change whichever non-empty fields are supplied, while omitted fields stay as they are. In the TUI, Ctrl+T opens the picker from new-message, reply, and forward forms and inserts the snippet's plain-text representation at the current body cursor without replacing the draft.

`hey box view <name|id>`, `hey label view <id>` and `hey collection view <id>` list the same postings and answer the same formats: `--json`, `--styled`, `--markdown`, `--ids-only`, and `--count`. The data-only formats print the pagination notice and any `next_page` cursor on stderr, so the IDs on stdout stay pipeable. `--json` differs only in what wraps the postings: a box answers with HEY's box payload, a label and a collection with the source and its `total_count`.

Move destinations are Imbox, The Feed, Set Aside, Reply Later, or Paper Trail. Moving to any of them but Imbox marks the threads seen, and a thread that cannot be replied to is silently not moved to Reply Later. Reply Later is a box rather than a separate flag: moving a Reply Later thread to Imbox removes Reply Later, preserves its seen state, and leaves a seen thread in Previously Seen. It does not return the thread to the box it occupied before Reply Later. A bundle row is refused rather than moved: it stands in for one sender's whole stream in the box they are delivered to, so moving it leaves nothing there for their next email to join and it arrives unbundled instead. Group or ungroup a sender with `hey contact bundle` and `hey contact unbundle`, and read a bundle with `hey bundle view`. Bubble Up has its own commands: `hey bubble up` raises a thread right away with `--now`, on a date with `--on` (HEY resurfaces it at 08:00 UTC that day, or 18:00 UTC when the date is today), or at 08:00 UTC tomorrow, the next Saturday, or next Monday with `--tomorrow`, `--weekend`, and `--next-week`; `hey bubble pop` cancels one, moving the thread to the Imbox and marking it seen rather than returning it to its original box. `hey bubble list` shows both buckets — the threads back in the Imbox after bubbling up and the ones still scheduled, each with when it resurfaces. Trashing a shared thread removes your access instead of deleting it for everyone. Ignoring marks a thread seen and leaves it in its box; while it is ignored `hey unseen` has no effect, and `hey stop-ignoring` resumes notifications but leaves it seen.

## Watching for changes

```bash
hey watch                               # follow every box and calendar, a line of JSON per change
hey watch --box imbox --events added    # only new postings in the Imbox (calendars off)
hey watch --label 789 --events new      # labeled new mail, or a known thread that gains the label (calendars off)
hey watch --events recording_added,recording_updated,recording_deleted   # calendar changes only
hey watch --box imbox --exit-on-first   # block until something lands, then exit
hey watch --since 2026-08-18T09:00:00Z  # catch up from a time first, then follow
hey watch --box imbox --events new      # new mail only: unseen, unmuted, active since the watch began
hey watch --box imbox --events new --exit-on-first   # block until new mail
hey watch --box imbox --events new --run-async 'notify-send -a HEY "New mail in HEY"'
hey watch --run-sync ./triage.sh        # one at a time, waiting for each
```

Runs until interrupted, printing changes as they happen, one line each:

```json
{"change":"added","at":"2026-08-18T09:14:22.031Z","box":{"id":24088,"kind":"imbox","name":"Imbox"},"posting_id":98765,"thread_id":54321,"new":true,"posting":{}}
```

Every `added` and `updated` line says whether the posting is new mail: unseen, not muted,
and active since the watch last saw the thread — or since the watch began, for a thread it
has not seen, so a box's backlog is never new. Reading, muting or moving a thread is not new
activity; a reply on a known thread is. `--events new` selects the new ones, alone or in a
union with `added`, `updated`, `deleted` and `resync` — the default is everything but `new`,
and `new` alone leaves a `resync` out, so a script for new mail never runs on one. `--box`
picks the boxes whose changes are reported; every box is followed regardless, so what is new
is judged across all of them — a reply in The Feed and then a move into the Imbox is not.
`--label` keeps only postings already filed under that label (id or name from
`hey label list`), and each reported line includes `label` `{id,name}`. With `--events new`,
that means new mail that already carries the label, and a thread this watch already saw
without the label that later gains it when the feed returns an update with the folder. A
late tag on a thread the watch has never seen still needs `--events updated` (or `updated`
alongside `new`). The one-liner above is what any desktop does with it; on Omarchy the bar
plugin reads the same lines and sends one batched, replacing toast instead.

The calendars are followed too, by default. A changed event, todo, habit or journal entry
is a `recording_added`, `recording_updated` or `recording_deleted` line naming its
calendar — `{"change":"recording_added","calendar":{"id":512,"name":"Household"},
"recording_id":88001,"recording_type":"Calendar::Event","recording":{}}` — and a calendar
arriving, changing or leaving is `calendar_added`, `calendar_updated` or
`calendar_deleted`. A calendar whose feed fell too far behind is skipped ahead and says so
with `calendar_resync`, the way a box says `resync`. The email-specific flags switch the
calendars off: `--box` or `--label` scopes the watch to mail, and an `--events` list naming
only mail changes does the same.

A change can drive a command instead of being printed, and there's a choice to make
between two behaviours — pass one or the other, not both. `--run-async` spawns the
command per change and moves on, so a slow one never holds up the watch and two can
overlap. `--run-sync` waits for each and runs them in order, so they never overlap and a
slow one delays the next.

Both hand the JSON to the command on its stdin, and the same fields as `HEY_CHANGE`,
`HEY_AT`, `HEY_BOX_ID`, `HEY_BOX_KIND`, `HEY_BOX_NAME`, `HEY_LABEL_ID`, `HEY_LABEL_NAME`,
`HEY_POSTING_ID` and `HEY_THREAD_ID`, with `HEY_NEW=1` for new mail and `HEY_NEW=0`
otherwise — and on a
calendar line `HEY_CALENDAR_ID`, `HEY_CALENDAR_NAME`, `HEY_RECORDING_ID` and
`HEY_RECORDING_TYPE` instead of the box and posting fields. Both also take over
stdout, so the JSON isn't printed as well.

## Calendars

```bash
hey calendar list                      # list calendars and their IDs
```

`hey calendar list` names the account each calendar belongs to when there is more than one, since
that is what tells two calendars of the same name apart.

Everything a calendar holds is a recording, and each kind has its own command: `hey event`,
`hey todo`, `hey journal`, `hey habit` and `hey timetrack`. The listings that read a
calendar's window share their flags — `--calendar`, `--starts-on`, `--ends-on`, `--limit`
and `--all`. Dates want `YYYY-MM-DD`, and an unreadable one or an `--ends-on` before
`--starts-on` is a usage error rather than an empty result. Naming only `--starts-on` moves
the whole window rather than reading up to the default end.

### Events

```bash
hey event list                    # every calendar, today through the next 30 days
hey event list --calendar 123 --starts-on 2026-01-01 --ends-on 2026-01-31
hey event list --count            # how many events in the window

hey event day                     # today as HEY draws it, recurrences expanded
hey event day 2026-09-02          # one day
hey event week 2026-09-02         # the week that day falls in

hey event add "Design review" --starts-on 2026-09-02 --start-time 14:00 --end-time 15:00
hey event add "Sarah's birthday" --starts-on 2026-09-02     # no time, so all day
hey event add "Standup" --start-time 09:15 --repeat every_weekday --remind 10m
hey event edit 4821 --title "Design review (moved)"
hey event edit 4821 --starts-on 2026-09-04 --start-time 15:00
hey event edit 4821 --occurrence 4821_2026-09-15 --apply-to current --start-time 15:00   # that day alone
hey event edit 4821 --occurrence 4821_2026-09-15 --apply-to future --repeat every_week --repeat-times 8 --location "Studio, 3rd floor" --allow-plain-notes
hey event delete 4821                                                    # the whole series
hey event delete 4821 --occurrence 4821_2026-09-15 --apply-to current    # that day alone
hey event delete 4821 --occurrence 4821_2026-09-15 --apply-to future     # that day and every one after it
```

Without `--calendar`, `hey event list` reads every calendar and `hey event add` files on
the calendar HEY uses by default: the first ordinary calendar you own that is not a
subscription — never Maybe or the personal calendar.
The list follows every page HEY serves. Within each calendar HEY orders recordings by
newest start time, not creation time, so use the ID returned by `hey event add` for a follow-up
edit or delete rather than choosing an event by its position in the list. A repeating event
lists once as the series it is stored as, plus a row for each day HEY has written out on its own.

`hey event day` and `hey event week` read a span the way HEY's own views draw it: a
repeating event is expanded into the occurrences that fall inside it, each carrying that
day's own times and an `occurrence_id`. A virtual occurrence carries the series in `id`
and `parent_id`. A day HEY has written out on its own keeps its own event ID in both `id`
and `recording_id`, while `parent_id` remains the series; that own ID is what `hey event
edit` acts on for that day alone. Deleting one day, written out or not, takes its
`occurrence_id` instead: `hey event delete <series id> --occurrence <occurrence_id>
--apply-to current` (see below). A period covers the calendars
switched on in HEY, the same set
the app draws, so `day` and `week` take no `--calendar` — only `--limit`
and `--all`. With no date they read the account's own today, whatever zone the machine
runs in.

An event with no `--start-time` is an all-day event, and a `--start-time` with no
`--end-time` runs for an hour. Clock times are read in `--time-zone`, which defaults to the
machine's own zone; without one HEY would read them as UTC.

`hey event edit` changes only the flags you name, but that is this command's doing rather
than HEY's: an event write is a replacement, so the edit reads the event first and sends
back the notes, location, link, attached email, reminders and time zones it is not
changing. Two things cannot survive the round trip. HEY serves notes back as plain text, so
saving flattens their formatting; and a countdown is a recording of its own that this edit
does not read back, so an edit removes one unless `--countdown` names it again. An event that cannot be read is refused rather
than written blind. It is looked for within a year either side of today; pass the day it
starts (`hey event edit 4821 2026-09-02`) for one outside that — `--calendar` only limits
which calendars are read.

An id on its own changes the whole event, a repeating series included. One day of a
series is changed with `--occurrence`, which takes the `occurrence_id` that `hey event
day` and `hey event week` serve — `<series id>_<YYYY-MM-DD>`, byte for byte, naming the
series the positional id names — together with `--apply-to`, which is required with it
and is the choice HEY's own form puts to you: `current` changes that day alone, `future`
changes it and every day after it. `--apply-to` without `--occurrence` is a usage error,
as is any other value. The day is read on its own date rather than searched for, so
`[date]` can be left out or must name it. A change to `--repeat`, `--repeat-until` or
`--repeat-times` cannot apply to one day, so `current` refuses those flags. A `future`
edit starts a new series and requires `--repeat` to state its complete schedule. Combine
a preset with `--repeat-times` or `--repeat-until` for a finite series, naming how many
occurrences remain from the edited day; use `--repeat custom` without either limit to
copy an existing opaque schedule. A custom rule with `COUNT` can restart its full count on
the replacement because HEY cannot expose how many occurrences remain. HEY accepts the new
series' submitted start even when it overlaps an earlier occurrence, so choose its date and
time deliberately; its last day cannot precede its first day. A virtual day of an opaque
custom schedule takes its exact time from HEY's Day view and is refused if that view no longer
serves it. A realized custom day is refused for `future`, because HEY does not serve the rule's
authoritative occurrence boundary; split from a later virtual occurrence, edit that day alone,
or edit the whole series. A realized preset occurrence that was moved away from its series time is also
refused: move it back with a `current` edit first. Haystack currently cancels realized children
from the moved time but truncates the parent at the occurrence identifier, so splitting it
directly can remove an earlier edit or leave a following one behind. A preset `--repeat` alone
means the new series continues forever.
HEY splits the series there
— the days from this one on become a new series with
a new id, the old series stops the day before, and the answer is still the day you edited
— so read the day or the week again for the new series id before editing it further.

An occurrence edit keeps more than a whole-event edit does, and refuses what it cannot
keep. It sends back the day's own schedule and zones, notes, location, link, attached
email, reminders and circle, taking them from the day itself where HEY has already written
that day out on its own. Styled `day` and `week` tables that contain occurrences print
`Series ID`, `Occurrence ID` and `Recording ID` columns beside `ID`. A day like that
lists its own event ID in `id` and `recording_id`, with the series in `parent_id`; `hey
event edit <id>` acts on that day alone. The series id is what `--occurrence` takes
beside its `occurrence_id`. A countdown owned by the day is read back and sent again. An
inherited series countdown is left inherited by a `current` edit, while a `future` edit
reads it from the day the series began and copies it to the replacement series. It
survives unless `--countdown 0` removes it; a countdown whose length cannot be read back
stops the edit and says so. One day of a series with a countdown cannot lose it alone:
HEY shows a day the series' countdown whenever it has none of its own, so `--countdown 0`
with `current` is refused there, and `future` or an edit of the series is where it comes
off. Notes are still served only as plain text and nothing can tell formatted notes from
plain ones, so an occurrence edit that would send notes back as text is refused unless
`--allow-plain-notes` accepts the loss or `--notes` replaces them; an event with no notes
needs neither. A whole-event edit accepts `--allow-plain-notes` too, and it changes nothing
there. A `future` edit records the new series from the series' own guest list and sends
the invitations, so a day whose guest list had come to differ from the series' is refused
until `--invite` names the new series' list. The day is read over every calendar, so with
`--occurrence` the `--calendar` flag is only the calendar the day is moved to, and a day
already moved to another calendar stays there through a `future` edit. HEY answers the
write with not-found both for a date that is not a day of the series and for a series you
cannot edit.

One thing no edit can keep, whole event or one day: an attached email you cannot read is
left out of what HEY serves, indistinguishable from none, and HEY clears the attachment
whether the write sends an empty entry id or no entry id at all. Editing such an event
detaches the email; only HEY can change that.

`hey event delete <id>` deletes the whole event, a repeating series included, and an event
on a shared calendar is deleted for everybody on it. One day of a series is deleted the
way it is edited: `--occurrence` takes the `occurrence_id` that `hey event day` and `hey
event week` serve, naming the series the positional id names, and `--apply-to` is required
with it. `current` deletes that day alone, and HEY keeps the rest of the series by writing
the day into its exceptions. `future` deletes that day and every one after it, stopping the
series the day before — from the series' first day, that is the whole series. A day HEY has
written out on its own is deleted the same way as one it has not.

What does not delete such a day is its own ID. HEY deletes that recording and nothing else:
the day is not written into the series' exceptions, so HEY draws it again from the series,
and the delete would report success having undone the day's edit. So `hey event delete
<id>` reads the event first — over every calendar, a year either side of today — and
refuses a written-out day with a usage error naming the `--occurrence` command that
deletes it. An ID that read does not find is deleted as before.

A `future` delete from a written-out day is refused where its boundary cannot be trusted,
for the reason a `future` edit is: HEY stops the series at the day's occurrence date but
cancels the written-out days from the day's actual start. A day moved off its series time,
or any written-out day of an opaque custom schedule, is refused; delete that day alone with
`current`, then delete from the next day with `future`. HEY answers not-found both for a
date that is not a day of the series and for a series you cannot delete from.

### Todos

```bash
hey todo list                      # list todos
hey todo list --starts-on 2026-01-01 --ends-on 2026-01-31
hey todo add "Buy milk"            # create a todo
hey todo complete 1                # mark done
hey todo uncomplete 1              # mark undone
hey todo delete 1                  # delete
```

### Habits

```bash
hey habit list                      # list habits and their IDs
hey habit list --date 2026-09-02    # the habits in that date's week
hey habit create "Morning strength training"  # create every day with weights and blue defaults
hey habit create "Practice piano" --icon music --color green --days mon,wed,fri
hey habit edit 1 --name "Evening strength training"  # edit only the supplied fields
hey habit edit 1 --days 0,6         # Sunday and Saturday (names also work)
hey habit delete 1                  # permanently delete the habit and its history
hey habit complete 1                # mark habit done (today or --date YYYY-MM-DD)
hey habit uncomplete 1              # undo habit completion
```

`hey habit list` reads the week a date falls in. A week lists each habit once, whatever
weekday it runs on; a week that has not started yet lists none. Weekdays use `0` for Sunday through `6` for
Saturday; full names and common abbreviations are accepted too.

### Time tracking

```bash
hey timetrack start                # start tracking
hey timetrack stop                 # stop tracking
hey timetrack stop --category "Client work"   # stop and file it in one request
hey timetrack current              # show active track
hey timetrack list                 # completed tracks, newest first
hey timetrack list --all --json    # every page
hey timetrack list --category 42   # only one category's tracks
hey timetrack edit 1042 --end 2026-08-22T17:30
hey timetrack edit 1042 --category "Client work" --notes "Invoice review"
hey timetrack delete 1042
hey timetrack export > tracked-time.csv
hey timetrack export --output tracked-time.csv
hey timetrack categories           # list categories
hey timetrack category create "Client work"
hey timetrack category rename 123 "Planning"
hey timetrack category delete 123
```

`hey timetrack list` reads HEY's tracked time index: completed tracks, newest-ended first,
with the day, the times, how long each took, the category and the notes. The track that is
running is not in it — `hey timetrack current` is where that lives. One page arrives by
default; `--limit` reads on until it has that many and `--all` reads the lot. `--category`
takes an ID from `hey timetrack categories`.

`hey timetrack edit` changes only the fields whose flags you give. `--start` and `--end`
want `YYYY-MM-DDTHH:MM` in your own time zone, or a full RFC 3339 instant when the zone
matters. A category is given as a title, and HEY creates the category if it has none by
that title — on `edit` and on `stop --category` alike. There is no clearing a category that
way: a blank title leaves the track filed where it was. Editing a track completes it, so
`edit` is for tracks that have already finished.

The time tracking export contains every completed entry, newest first, with Start, End, Duration, Category, and Notes columns. Ongoing time tracking is excluded. `--output` preserves an existing file unless `--force` is set.

Without `--output` the CSV goes to stdout as CSV, so redirecting it to a file is the whole
recipe. The output formatting flags have nothing to reshape there and are refused rather
than ignored. With `--output`, `--json`, `--quiet` and `--markdown` format the file's
metadata; `--ids-only` and `--count` still fail, since that is not a list, and `--html` is
never accepted.

### Journal

```bash
hey journal list                   # list entries
hey journal list --starts-on 2026-01-01 --ends-on 2026-01-31
hey journal read                   # read today's entry (or pass YYYY-MM-DD)
hey journal write "..."            # write today's entry (omit content: $EDITOR at a terminal, else stdin)
```

Saving an empty buffer in `$EDITOR` removes the day's entry, and `hey journal write` says
so rather than reporting a save. An empty day answers with an empty entry, so if the read
that pre-fills the editor fails for any other reason the command stops there instead of
opening a blank buffer over an entry it could not see.
