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

Navigate between mailboxes, postings, and full email threads. Use Enter to drill in, Escape/Backspace to go back, and `/` to filter lists.

## CLI Commands

All commands support `--json` for raw JSON output and `--base-url` to override the server URL.

### Email

```bash
hey boxes                          # list mailboxes
hey box imbox                      # list postings in a box (by name or ID)
hey threads 123                    # read a full email thread
hey reply 123 -m "Thanks!"        # reply to a thread (or omit -m to open $EDITOR)
hey compose --to user@example.com --subject "Hello"  # compose a new message
hey compose --to user@example.com --cc bob@example.com --bcc carol@example.org --subject "Hello"  # with CC/BCC
hey drafts                         # list drafts
```

### Calendars

```bash
hey calendars                      # list calendars
hey recordings 1 --starts-on 2026-01-01 --ends-on 2026-01-31  # list events in a calendar
```

### Events

```bash
hey event list                     # list events (personal calendar by default)
hey event list --calendar <id-or-name> --limit 10  # name matches owned calendars case-insensitively
hey event create --title "Team sync" --date 2026-06-15 --start 09:00 --end 10:00
hey event create --title "Holiday" --date 2026-06-15 --all-day
hey event create --title "Review" --date 2026-06-15 --start 14:00 --end 15:00 --reminder 30m --reminder 1h
hey event edit 123 --title "Updated standup"
hey event delete 123
```

Reminder durations accept `30m`, `1h`, `2d`, `1w`.

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
