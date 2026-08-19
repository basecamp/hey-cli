# hey-cli

```
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⣠⣄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣼⡿⠏⠻⣷⣄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣶⣶⣤⠀⠀⠀⣿⠃⠀⠀⠘⣿⣆⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢰⣿⠉⠹⣷⣄⠀⣿⡀⠀⠀⠀⠈⢿⣦⠀⠀⠀⠀⠀⠀⠀⠀⠀⢰⣶⣶⣶⣶⣶⠀⠀⠀⠀⠀⠀⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⡀⠀⠀⠀⠀⢠⣶⣶⣶⣶⣶⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⠀⠀⣿⡆⠀⠘⣿⣦⣿⡇⠀⠀⠀⠀⠘⣿⡆⠀⠀⢀⣀⣀⣀⡀⠀⠸⣿⣿⣿⣿⣿⠀⠀⠀⠀⠀⠀⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣧⠀⠀⠀⠀⣾⣿⣿⣿⣿⠃⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⣾⡿⣷⣄⢻⣧⠀⠀⠈⢿⣿⣷⡆⠀⠀⠀⠀⢸⣿⣠⣶⠿⠛⠛⠛⣿⣆⠀⢹⣿⣿⣿⣿⠀⠀⠀⠀⠀⠀⣿⣿⣿⣿⣿⡏⠉⠉⠉⠉⠉⠉⠙⠻⣿⣿⣿⣿⣆⠀⠀⣸⣿⣿⣿⣿⠃⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⣿⡇⠘⢿⣾⣿⡆⠀⠀⠈⢿⣿⣧⠀⠀⠀⠀⠀⣿⣿⠁⠀⠀⠀⠀⢸⣿⠀⠀⣿⣿⣿⣿⣄⣀⣀⣀⣀⣠⣿⣿⣿⣿⣿⣧⣀⣀⣀⣀⡀⠀⠀⠀⢹⣿⣿⣿⣿⡄⢰⣿⣿⣿⣿⠃⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⢸⣷⠀⠀⠻⣿⣿⡄⠀⠀⠈⢿⣿⡆⠀⠀⠀⢸⣿⣿⠀⠀⠀⠀⠀⢸⣿⠀⠀⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⠀⠀⠀⠀⢻⣿⣿⣿⣷⣿⣿⣿⣿⠏⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⢿⣇⠀⠀⠘⢿⣷⡀⠀⠀⠘⠻⣿⡀⠀⠀⣿⡏⣿⡇⠀⠀⠀⠀⢸⣿⠀⢀⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⠀⠀⠀⠀⠀⢻⣿⣿⣿⣿⣿⣿⠏⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⣾⡿⢿⣾⣿⣆⠀⠀⠈⢻⣷⡀⠀⠀⠀⠉⠀⠀⢀⣿⠃⢹⣧⠀⠀⠀⠀⣿⡇⠀⢸⣿⣿⣿⣿⠁⠀⠀⠀⠀⠈⣿⣿⣿⣿⣿⡏⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣿⣿⣿⣿⣿⡟⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⢹⣧⠀⠙⢿⣿⣆⠀⠀⠀⠹⠷⠀⠀⠀⠀⠀⠀⢸⣿⠀⢸⣿⠀⠀⠀⢸⣿⠀⠀⣿⣿⣿⣿⣿⠀⠀⠀⠀⠀⠀⣿⣿⣿⣿⣿⣇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣽⣿⣿⣿⣿⡇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⢿⣧⠀⠀⠙⢿⣧⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⠀⢸⣿⠀⠀⢀⣿⠇⠀⢸⣿⣿⣿⣿⣿⠀⠀⠀⠀⠀⠀⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⠀⠀⠀⣿⣿⣿⣿⣿⡇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠈⢻⣷⡀⠀⠀⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣿⣧⣾⡏⠀⠀⣼⡟⠀⠀⠸⣿⣿⣿⣿⡿⠀⠀⠀⠀⠀⠀⢿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⠀⠀⠀⢻⣿⣿⣿⣿⡇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠹⢿⣦⣀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠉⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠙⠻⣷⣦⣄⣀⡀⠀⣀⣀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠉⠛⠛⠛⠻⠟⠛⠃⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
```

A CLI and TUI for [HEY](https://hey.com).

*Read and send emails, manage boxes, calendars, todos, habits, time tracking, and journal entries — all from your terminal.*

## Install

Requires Go 1.26+. Use [mise](https://mise.jdx.dev) to install the correct version:

```bash
mise install       # install Go 1.26
make install       # build and install into /usr/local/bin/hey
```

## Authentication

```bash
# Browser-based OAuth via Launchpad (primary method)
hey auth login

# Or use a pre-generated token
hey auth login --token TOKEN

# Or use a browser session cookie
hey auth login --cookie COOKIE
```

Tokens refresh automatically on expiry. Credentials are stored in the system keyring (with file fallback at `~/.config/hey-cli/credentials.json`).

```bash
hey auth status   # check auth status
hey auth token    # print access token for scripting
hey auth refresh  # force token refresh
hey auth logout   # clear credentials
```

## TUI

Run `hey` to launch the interactive terminal UI.

Navigate between mailboxes and email threads. Use `/` to search, Enter to open a thread, `r` to reply, `f` to forward, `m` to move, `t` to trash, `s` to mark as spam, `-` to ignore, `+` to stop ignoring, and Escape or `q` to go back. Search results retain the matching-message summary; use `n` and `p` to move between result pages.

## CLI Commands

All commands support `--json` for raw JSON output and `--base-url` to override the server URL.

### Email

```bash
hey boxes                          # list mailboxes
hey box imbox                      # list email threads in a box (by name or ID)
hey search "quarterly planning"    # search threads and matching messages
hey search --from jane@example.com --date last_30_days  # refine a search
hey search filters                 # list available refinement values
hey threads 123                    # read a full email thread
hey reply 123 -m "Thanks!"        # reply to a thread (or omit -m to open $EDITOR)
hey forward 123 --to alice@example.com -m "For your review"  # forward the latest message
hey compose --to user@example.com --subject "Hello"  # compose a new message
hey compose --to user@example.com --cc bob@example.com --bcc carol@example.org --subject "Hello"  # with CC/BCC
hey drafts                         # list drafts
hey move 12345 --to feed           # move a thread to another box
hey move 12345 67890 --to "paper trail"  # move multiple threads
hey trash 12345                    # move a thread to Trash
hey spam 12345                     # mark a thread as spam
hey ignore 12345                   # ignore future activity on a thread
hey stop-ignoring 12345            # resume attention for a thread
```

Search accepts free text plus `--required`, `--any`, `--none`, `--exact`, `--from`, `--to`, `--subject`, `--date`, `--in`, `--label`, and `--attachment`. Use `--page` for one page or `--all` to fetch every page. Search results include `id` for organization actions, `topic_id` for reading the thread, and the matching message summaries.

Organization actions take the `id` values returned by `hey box --json` or `hey search --json`. Move destinations are Imbox, The Feed, Set Aside, Reply Later, or Paper Trail. Bubble Up requires a scheduled date and is not available through `hey move`. Trashing a shared thread removes your access instead of deleting it for everyone. Ignored threads remain in their box and can be restored with `hey stop-ignoring`.

### Calendars

```bash
hey calendars                      # list calendars
hey recordings 1 --starts-on 2026-01-01 --ends-on 2026-01-31  # list events in a calendar
```

### Todos

```bash
hey todo list                      # list todos
hey todo add "Buy milk"            # create a todo
hey todo complete 1                # mark done
hey todo uncomplete 1              # mark undone
hey todo delete 1                  # delete
```

### Habits

```bash
hey habit complete 1               # mark habit done (today or --date YYYY-MM-DD)
hey habit uncomplete 1             # undo habit completion
```

### Time tracking

```bash
hey timetrack start                # start tracking
hey timetrack stop                 # stop tracking
hey timetrack current              # show active track
hey timetrack list                 # list all tracks
```

### Journal

```bash
hey journal list                   # list entries
hey journal read                   # read today's entry (or pass YYYY-MM-DD)
hey journal write "..."            # write today's entry (or omit content for $EDITOR)
```

## Agent Skill

hey-cli ships with an embedded agent skill so your agent can interact with HEY on your behalf.

```bash
hey skill install   # install the skill globally for your agent
```

## Development

```bash
make build   # build binary
make test    # run tests
make lint    # run golangci-lint
make clean   # remove build artifacts
```

## License

This project is licensed under the MIT License. See [LICENSE.md](LICENSE.md) for details.
