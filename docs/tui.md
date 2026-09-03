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
remembered across restarts.

Mail navigation includes HEY's boxes plus separate Labels and Collections tabs: Shift+L
opens Labels directly and Shift+K opens Collections. Previously Seen has its own tab after
the boxes on `9`, the web app's shortcut, showing the Imbox's already-read threads
newest-seen first with the usual thread actions available; Escape returns to the box you
were in. Every list keeps going: scroll towards the bottom of a box, label, collection or
search and the next threads are read in behind you, so there are no pages to step through.

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
| `e` / `u` | mark seen / unseen |
| `i` | move to the Imbox |
| `l` | move to Reply Later |
| `a` | move to Set Aside |
| `d` | move to The Feed |
| `p` | move to Paper Trail |
| `t` | trash |
| `!` | mark as spam |
| `-` / `+` | ignore / stop ignoring |
| Space | select the thread for a bulk action |
| Ctrl+B | preview every bulk-reply recipient, then write one reply to every selected thread |
| Ctrl+U | recall a delayed bulk reply while HEY's undo window is open |
| Ctrl+S | open The Screener |
| Ctrl+R | re-read the list |
| Ctrl+A | switch linked account |
| Ctrl+V | choose an Imbox cover |

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
scroll, the same way the mail list does.

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

Thread attachments always appear with their filename, media type, and size. Use `[` and `]` to select an attachment, `s` to save it without replacing an existing file, and `o` to download and open it in an external application. Attachments never open automatically. Kitty and Ghostty can show inline images. Foot and other terminals use visible text markers.

## Contacts

Press Shift+O to open Contacts. Use Enter to view a contact, `a` to add, `e` to edit, `n` to edit the private note, `x` twice to delete a note, `h` to hide, and `u` to show the most recently hidden contact again. Escape or `q` goes back.

## Calendar

Press Shift+C to open Calendar, then `c` to manage time track categories. Create a category with `n`, rename the selected category with Enter or `r`, and press `x` twice to delete it. Time tracks in a deleted category become uncategorized.

In Calendar, press `a` to create a habit. Habits visible in the current calendar range can be selected with `[` and `]`, edited with `e`, and deleted by pressing `x` twice. Habit forms use Tab to move between fields and Ctrl+S to save.
