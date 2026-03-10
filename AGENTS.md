# hey-cli

This file provides guidance to AI coding agents working with this repository.

## What is hey-cli?

hey-cli is a CLI and TUI interface for [HEY](https://hey.com). 
It allows users to read and send emails, mange their boxes, manage their calendars and journal entries.
The TUI is primarily intended for human use, while the CLI is primarily intended for use by AI agents and for scripting.

## Development commands

This project uses make.

```bash
make build   # Builds the project into a binary located at ./bin/hey
make test    # Runs the tests
make lint    # Lints the code
make clean   # Cleans the build artifacts
make install # Installs the binary to /usr/local/bin/hey
```

## Architecture Overview

This is a Go project that uses:
- [spf13/cobra](github.com/spf13/cobra) for the CLI interface
- [charm.land/bubbletea/v2] for the TUI interface along with bubbles/v2 and lipgloss/v2 (these are new versions that recently came out and differ from the v1 versions!)

Most API interactions go through the HEY SDK (`hey-sdk/go`), with typed service methods accessed via `internal/cmd/sdk.go` (e.g., `sdk.Boxes().List`, `sdk.Messages().Create`, `sdk.Calendars().GetRecordings`). A legacy `internal/client.Client` remains for two gap operations where the SDK lacks body content: `GetTopicEntries` (HTML-scraped topic entries for `hey threads` and TUI) and `GetJournalEntry` (HTML-scraped journal fallback when the JSON API returns 204). Authentication and token refresh are handled via `internal/auth/`.

### Authentication

Authentication supports three methods, all managed through `internal/auth/`:

1. **Browser-based OAuth with PKCE** (primary) — `hey auth login` opens a browser for OAuth authentication against HEY's own OAuth server (`/oauth/authorizations/new`), using PKCE (S256) for security. A local callback server on `127.0.0.1:8976` receives the authorization code, which is exchanged for access and refresh tokens at `/oauth/tokens`.
2. **Pre-generated bearer token** — `hey auth login --token TOKEN` stores a token directly.
3. **Browser session cookie** — `hey auth login --cookie COOKIE` uses an existing HEY.com session.
4. **Environment variable** — Set `HEY_TOKEN` to use a token without storing it.

The auth Manager (`internal/auth/auth.go`) proactively refreshes tokens with a 5-minute expiry buffer. The API client (`internal/client/`) uses the Manager to authenticate requests: `Authorization: Bearer <token>` or `Cookie: session_token=<cookie>` (bearer token takes precedence).

All data-access commands call `requireAuth()` before making API calls. Auth subcommands (`hey auth login`, `hey auth logout`, `hey auth status`) work without authentication.

### State storage

Configuration (base URL only) is stored in `~/.config/hey-cli/config.json`. Credentials are stored in the system keyring (service name: `hey`) with automatic fallback to `~/.config/hey-cli/credentials.json` when the keyring is unavailable. Set `HEY_NO_KEYRING=1` to force file storage.

### CLI

Remember to update the examples in the README when you change, add or remove CLI commands.

### HTML content

Some HEY API endpoints return 204 or incomplete data via JSON, but the full HTML content is available by scraping the edit page (e.g., `/calendar/days/{date}/journal_entry/edit` contains the Trix editor hidden input with full HTML). When an API endpoint returns incomplete data, check the corresponding web page for the full content. The `internal/htmlutil` package provides `ToText` (HTML→plain text) and `ExtractImageURLs` shared by both CLI and TUI. HEY uses Trix editor with `<figure data-trix-attachment="{...}">` for attachments — image URLs in those attributes are relative paths requiring authentication via `client.Get`.

### Inline images in the TUI

The TUI renders inline images using the Kitty graphics protocol's Unicode Placeholder extension (`internal/tui/kitty.go`). This works because Bubble Tea's cell-based renderer corrupts raw APC escape sequences, but Unicode placeholders are regular text that survives rendering. The approach has three steps:

1. **Upload** — image data is sent to the terminal via `tea.Raw()` with `a=t` (transmit only) and `q=2` (suppress response), then a virtual placement is created with `U=1`.
2. **Display** — U+10EEEE placeholder characters with combining diacritics (encoding row/column) are placed in the viewport content. The image ID is encoded in the foreground color.
3. **Sizing** — `image.DecodeConfig` reads dimensions without full decoding; terminal cell count accounts for ~2:1 height:width cell ratio.

This works in Kitty and Ghostty. Other terminals show the text content normally (placeholders are invisible).

### API documentation

If you are unsure what the API endpoints are, what they expect or what they respond to you can read through the server implementation to understand how the API works.

The server code is located at `~/Work/basecamp/haystack/`, feel free to read it whenever you need information about the API.

If you don't understand how the routes are laid out you can call rails routes in that directory to get a list of all the routes and their corresponding controller actions.

### Testing

Whenever you add, remove or change any functionality add/remove/change tests as well. Tests are located in the same package as the code they test, with filenames ending in `_test.go`. Run `make test` to run all tests.

### Running

To run the cli use `make build` and then `./bin/hey`. This ensures that you and I are running the same version of the program.

## Code style

@STYLE.md
