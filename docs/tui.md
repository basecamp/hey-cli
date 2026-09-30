# The TUI

`hey tui` is the interactive app: Mail, Contacts, Calendar and Journal in one terminal
window, following HEY live. This page is the reference for how it starts, how to get
around it, and what each key does. Press `?` inside the app for the shortcut bar.

## Starting it

Run `hey tui` to launch the interactive terminal UI (it offers to sign you in first if
needed). Bare `hey` prints the help — or, logged out at a terminal, runs first-time setup.
For identities with multiple linked mail accounts, press Ctrl+A to switch between All
Accounts and individual email addresses.
Switching cancels requests from the previous account and reloads the active section;
Calendar and Journal remain identity-wide.

`hey tui --topic 123` starts on thread 123, and `hey tui --screener` starts in The
Screener. Desktop integrations can send either destination to an existing TUI with
`hey tui --topic 123 --remote` or `hey tui --screener --remote`; `--account` selects the
linked account before it opens. An integration that owns a dedicated TUI window uses the
same `--instance <name>` on both commands, keeping its remote requests separate from
manually launched TUIs.

## Getting around

The app has four sections: Mail, Contacts, Calendar and Journal. The context-sensitive
shortcut bar is visible by default; press `?` to hide or restore it, and the choice is
remembered across restarts. While it is hidden, `? help` at the left of the top bar says
how to bring it back.

Mail navigation includes HEY's boxes plus separate Labels and Collections tabs: Shift+L
opens Labels directly and Shift+K opens Collections. Previously Seen has its own tab after
the boxes on `9`, the web app's shortcut, showing the Imbox's already-read threads
newest-seen first with the usual thread actions available; Escape returns to the box you
were in. Every list keeps going: scroll towards the bottom of a box, label, collection or
search and the next threads are read in behind you, so there are no pages to step through.
PgUp and PgDn move a screen at a time through any list — a box, a search, a bundle, The
Screener or your contacts — keeping the cursor on the same row of the screen.

The thread actions use HEY's web shortcuts, in either letter case except `l`, whose
uppercase belongs to Labels:

| Key | Action |
|---|---|
| `/` or `s` | search |
| `r` | reply |
| `f` | forward |
| `v` | move |
| `b` | manage labels |
| `n` | add to or remove from a collection |
| `e` / `u` | mark seen / unseen — every selected thread when any are selected (Imbox only, as in HEY's web app), otherwise the one under the cursor |
| `i` | move to the Imbox |
| `l` | move to Reply Later |
| `a` | move to Set Aside |
| `d` | move to The Feed |
| `p` | move to Paper Trail |
| `t` | trash — every selected thread when any are selected, otherwise the one under the cursor. Not offered when it would target a bundle |
| `!` | mark as spam |
| `-` / `+` | ignore / stop ignoring |
| Space | select the thread for a bulk action (`e`, `u`, `t` or Ctrl+B) |
| Escape | clear the selection |
| Ctrl+B | preview every bulk-reply recipient, then write one reply to every selected thread |
| Ctrl+U | recall a delayed bulk reply while HEY's undo window is open |
| Ctrl+S | open The Screener |
| Ctrl+R | re-read the list |
| Ctrl+A | switch linked account |
| Ctrl+V | choose an Imbox cover |

A thread opens on its latest message; `k` steps back through the ones before it and `j` forward again.

Sending a reply or a forward from a thread closes the thread and returns you to the list you opened it from — the Imbox, a search, a bundle. A send that fails keeps the form open.

While reading a thread, the From header shows the actual send-as address when HEY records one separately from the account user.

While reading a thread, links can be selected without a mouse. Tab selects the next link in document order and Shift+Tab selects the previous one; both wrap at the ends. A fixed row above the shortcut bar shows the selected destination without moving the thread. A destination too long for one row wraps onto as many rows as it needs, so you always see all of it. Press Enter to open it; opening stays unavailable until the terminal is tall enough to show the whole destination. Press Escape once to clear the selection, and Escape again to leave the thread. A thread with no selectable links keeps the normal global Tab focus behavior. Existing OSC 8 mouse links remain available.

Most of those keep working while you are reading a thread, the way the web app's topic
toolbar stays live: `r`, `f`, `v`, `b`, `u`, `i`, `l`, `a`, `d`, `p` and `t` all act on
the thread on screen rather than on the list behind it. Filing a thread leaves it open in
the box it landed in, so the next key files it on from there; `t` closes it, because a
trashed thread is not somewhere you file out of. Where you opened the thread from does
not come into it: a bundle, a contact's threads, a label and a search all work, because
every thread carries the box it is in and files out of that one rather than out of the
list you found it through. Filing takes the row out of the list you were reading. Only a
thread opened by its id has no row behind it, and says so instead.

While any threads are selected, the help bar counts them and offers only what acts on
all of them: `e`, `u`, `t` and Ctrl+B. The other thread actions — moving, filing, labels,
collections, spam, ignoring, reply and forward — work on one thread at a time, so they
are refused with a notice rather than quietly acting on the thread under the cursor;
press Escape to clear the selection first. Escape still closes an open form, cancels a
thread that is loading and leaves an open thread before it clears a selection, and on
Previously Seen the first Escape clears the selection and the next one leaves. A bulk
action lets go of the threads it acted on once HEY has answered, and keeps them selected if
the request fails, so it can be tried again; a thread you select while it is on its way stays
selected.

`e` and `u` act on a selection when HEY's web app would offer its bulk Seen and Unseen
buttons, and the help bar shows them only then. Every selected thread has to be in the
Imbox — each one's own box counts, so a label or a collection that mixes boxes does not
qualify — and none of them may be one you are ignoring. `e` also needs a thread that is not
seen yet, and `u` one that is; a bubbled-up thread is not a seen one. When they act, they
send every selected thread, and otherwise a notice says why nothing happened. `e` on a
selected bundle marks every unseen thread in it seen, as it does on the row itself.

If every selected thread leaves the list while you are looking at it — filed away from
another device, or covered when the cover comes down — the next thread action is refused
with a notice rather than falling back on the thread under the cursor, which you never chose.
Moving the cursor, or pressing Escape, means you are aiming again.

A bundle row cannot be trashed, and `t` leaves the help bar whenever the rows it would
act on include one. A bundle stands for one sender's whole stream rather than for a thread,
and HEY trashes a thread — so the server quietly does nothing with it while the list would
report it gone. HEY's own web app hides Trash on a bundle for the same reason. Deselect the
bundle to trash the rest of the selection; `hey contact unbundle` is what stops a sender
being grouped.

While writing a new message, reply or forward, Ctrl+T opens the searchable Snippets
picker. HEY never chooses a default: Enter inserts the selected snippet at the body
cursor, Escape returns without changing the draft, and the picker can be reopened to
insert another.

## Live updates

The mail list follows the server. HEY tells the TUI when a box changed over the same
Action Cable connection `hey watch` uses, and the box on screen is read again a moment
later, keeping your place in the list and anything you had selected. A change that arrives
while a form or a picker is open waits for it to close. A standing status below the header
appears in every section while the network is offline or live updates are reconnecting.
The TUI retries the connection, clears the status when it returns, and catches up the box
on screen; Ctrl+R remains available whenever you want to read it yourself.

The Calendar and Journal sections keep up the same way while they are on screen: HEY
rings each calendar's own update stream when something on it is written, and the span or
list you are looking at is read again a moment later, keeping your selection. A change
that arrives while a form or a picker is open waits for it to close, and a calendar
shared with you after the section opened is picked up by a slow background check.

The Screener keeps up too. When a first-time sender writes, the count above the threads
changes on its own, and if you have The Screener open the new sender appears in the queue
without moving your place in it.

## The Screener

Press Ctrl+S from the mail list to open The Screener. When senders are waiting, the mail
list says so above the threads. In The Screener, `y` screens the selected sender in and `n`
screens them out, Tab moves to Screener History and back, `X` clears the whole Screener
after a confirmation, and Escape or `q` returns to mail. Both lists keep going as you
scroll, the same way the mail list does. Once you've dealt with everyone waiting — the
last sender screened in or out, or the whole Screener cleared — it closes on its own and
takes you to the Imbox, as the web app does.

Space opens a bigger preview of the selected sender's most recent email: the whole message,
not just the first line the list has room for. Arrow keys and PgUp/PgDn scroll it, `y` and
`n` answer for that sender straight from it, and Space or Escape closes it again. Opening a
preview only reads the email; it doesn't screen the sender or tell them anything.

## Imbox cover art

The Imbox can wear cover art, the way the HEY web app does: everything you have already
read goes under it, so the box ends at what still wants your attention instead of trailing
off into a month of receipts. The divider stays and says how much is under there — press
`x` to peek, `x` again to close it, or `9` to open Previously Seen on its own screen.

Press Ctrl+V to choose one: `blobs`, `grid`, `peace`, `terrazzo`, `topo` or `waves`, the
same six covers redrawn as characters, so they work in any terminal rather than only the
ones that can show images. The picker draws whichever you have highlighted. They are
painted in your terminal's own colors, so a cover matches your theme and follows it when
you switch.

Your choice is remembered in `~/.config/hey-cli/config.json`, on this machine. It is not
the cover you picked on the web: HEY keeps that one server-side but serves it to nobody, so
the iOS and Android apps each keep their own local choice too, and this is the same.

## Attachments

Thread attachments always appear with their filename, media type, and size. Use `[` and `]` to select an attachment, `s` to save it without replacing an existing file, and Enter to download and open it in an external application — once Tab has selected a link, Enter opens the link instead, and Escape clears it. Attachments never open automatically. Kitty and Ghostty can show inline images. In the message itself, a file is marked by its kind: 📷 an image, 🎬 a video, 🎵 audio, 📄 a PDF or document, 📎 anything else. An image shows by name rather than by its URL, unless its name looks like a URL, when the real destination is shown beside it; an image on the web is a link you can select with Tab, and the row above the shortcut bar shows its whole destination.

## Contacts

Press `o` (or Shift+O) to open Contacts — the letter underlined in its tab. Use Enter to view a contact, `a` to add, `e` to edit, `n` to edit the private note, `x` twice to delete a note, `h` to hide, and `u` to show the most recently hidden contact again. Escape or `q` goes back.

The private note is shown formatted and edited as Markdown, the way `hey contact note set` takes it, so a note written in HEY keeps its bold, italics, lists, links and headings when you edit it here. Press ctrl+s to save it. A note holding an attachment or other formatting Markdown cannot preserve is shown but refused by the editor rather than losing that content; edit it in HEY or use `hey contact note set --note-html`.

## Calendar

Press Shift+C to open Calendar, then `c` to manage time track categories. Create a category with `n`, rename the selected category with Enter or `r`, and press `x` twice to delete it. Time tracks in a deleted category become uncategorized.

A new event's times are written in your HEY account's time zone, the one HEY's web app and
`hey event add` use: the Starts and Ends rows name it, and the event is saved in it. The
Calendar reads the zone each time it opens; if the account has none or it cannot be read,
the form opens on `Local` instead, and a form opened before the read answers takes the zone
when it does, unless you have already edited a date or a time, chosen a zone, switched
All day, or pressed Ctrl+S — any save, even one that was refused or failed, keeps the form
as you saw it. The zone list still offers `Local` (your machine's clock) and the zones in
your machine's zone database, or a shortlist of common zones where that database cannot be
listed. An event with both ends on `Local` is saved without a zone; a `Local` end beside one
in a named zone is written in that zone, at the same moment, since HEY keeps a zone for both
ends or neither. An edit keeps the
event's own zone, and an event saved without one stays on `Local`; an all-day event given a
time takes the account's zone. Times are read as HEY reads them, on `Local` too: a time the
clocks skip moves on to one that exists. A time you leave showing what it opened with keeps
the moment it had, even if you typed at it and took it back. Saving refuses one HEY would put
somewhere else — at the second of two moments as the clocks go back, or with seconds — rather
than moving the event: choose another time, or press Ctrl+S again to save it where HEY reads it.
The tracked-time form keeps an unchanged time the same way.

In Calendar, press `a` to create a habit. Habits visible in the current calendar range can be selected with `[` and `]`, edited with `e`, and deleted by pressing `x` twice. Habit forms use Tab to move between fields and Ctrl+S to save.
