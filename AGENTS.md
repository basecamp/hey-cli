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
make install # Installs the binary to /usr/local/bin/hey-cli or /usr/bin/hey-cli depending on the system
```

## Architecture Overview

This is a Go project that uses:
- [spf13/cobra](github.com/spf13/cobra) for the CLI interface
- [charm.land/bubbletea/v2] for the TUI interface along with bubbles/v2 and lipgloss/v2 (these are new versions that recently came out and differ from the v1 versions!)

All API interactions go through typed methods on `internal/client.Client` (e.g., `ListBoxes`, `GetEntry`, `ListCalendars`), organized into domain-specific files (`boxes.go`, `entries.go`, `calendars.go`, `todos.go`, `journal.go`, `habits.go`, `time_tracks.go`). Both CLI commands (`internal/cmd/`) and the TUI (`internal/tui/`) call these methods instead of using raw HTTP methods with hardcoded paths. Authentication and token refresh are handled via `internal/auth/`.

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

## Code style

@STYLE.md
