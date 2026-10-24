# hey-cli

This file provides guidance to AI coding agents working with this repository.

## What is hey-cli?

hey-cli is a CLI and TUI interface for [HEY](https://hey.com). 
It allows users to read and send emails, manage their boxes, manage their calendars and journal entries.
The TUI is primarily intended for human use, while the CLI is primarily intended for use by AI agents and for scripting.

## Development commands

This project uses make. `make help` covers most targets -- though not all of them:
`install`, `check-toolchain` and `help` itself are defined but unlisted.

```bash
make build   # Builds ./bin/hey
make test    # Runs the tests
make lint    # Lints the code
make install # Installs the binary to /usr/local/bin/hey (not shown by make help)
```

## Git

History is linear: merge commits are disallowed in this repository. Bring a branch up to
date by rebasing it onto main, never by merging main into it.

## Architecture Overview

This is a Go project that uses:
- [spf13/cobra](https://github.com/spf13/cobra) for the CLI interface
- [charm.land/bubbletea/v2] for the TUI interface along with bubbles/v2 and lipgloss/v2 (these are new versions that recently came out and differ from the v1 versions!)

All API interactions go through the HEY SDK (`hey-sdk/go`), with typed service methods accessed via `internal/cmd/sdk.go` (e.g., `sdk.Boxes().List`, `sdk.Messages().Create`, `sdk.Calendars().GetRecordings`). Authentication and token refresh are handled via `internal/auth/`.

### Authentication

`internal/auth/` manages four methods: browser OAuth with PKCE (`hey auth login`, the
primary one), a stored bearer token (`--token`), a browser session cookie (`--cookie`), and
the `HEY_TOKEN` environment variable. The Manager refreshes tokens with a 5-minute buffer,
and the SDK reaches it through the bridge in `internal/cmd/sdk.go`.

Every data-access command calls `requireAuth()` before it makes an API call. Logged out,
it exits 3 with `Run: hey auth login`, or offers to sign in at an interactive terminal with
styled output. `HEY_NONINTERACTIVE=1` turns the offer off. The auth subcommands, `hey
login`/`hey logout`, `hey setup` and `hey doctor` work without authentication.

Coding-agent setup lives in `internal/harness` and `internal/cmd/setup_agent*.go`. Every
skill write goes through `claimSkillDir`, which marks a directory it creates
(`.managed-by-hey-cli`) and refuses a populated one without the marker. Do not add a write
path that bypasses the gate.

The setup wizard, the Omarchy bar plugin and the skill refresh hook are in
[docs/architecture/auth-and-setup.md](docs/architecture/auth-and-setup.md).

### State storage

Configuration (the base URL, the default linked account, the Imbox's cover, and the `onboarded` flag) is stored in `~/.config/hey-cli/config.json`; `onboarded` is read from the global file only, never from a repository's `.hey/config.json`. Credentials are stored in the system keyring (service name: `hey`) with automatic fallback to `~/.config/hey-cli/credentials.json` when the keyring is unavailable. Set `HEY_NO_KEYRING=1` to force file storage.

### CLI

The README is the front door, not the manual: install, first run, a dozen examples, and
links. Keep it that way. When you change, add or remove a command, the behaviour goes in
the command's `--help` text and its section of `docs/cli.md` (`docs/tui.md` for the TUI,
`docs/agents.md` for the skill and MCP, `docs/install.md` for installing and upgrading).
Touch the README only if one of its examples no longer runs or a command it names is gone.

New top-level commands and subcommands need the `.surface` snapshot updated
(`go test ./internal/cmd -run TestSurfaceSnapshot -update-surface`), and anything listed in
root help needs the byte-exact literal in `help_test.go` updated.

### Nothing scrapes a web page

HEY grew JSON for every endpoint the CLI reads,
the SDK dropped its own scrapers, and the last three scrapes here are gone. Do not answer
"the JSON looks incomplete" by parsing a web page — check whether the SDK already has a
typed operation, and if HEY does not serve JSON for what you need, add it to the SDK
rather than working around it here. `sdk.GetHTML` has no callers left; if you find yourself
reaching for it, that is the signal to stop.

### One line for a person, the envelope for everyone else

Every command that writes something confirms it through `writeMutation` (or
`writeMutationLine`, where the line carries an id or a name the summary does not).
It prints the sanitized line under `--styled` and hands `writeOK` the data and the
summary otherwise. Do not hand-roll the `if writer.IsStyled()` branch again: it was
copy-pasted about forty times, and three of the copies had drifted.

### TUI structure

The TUI uses the `sectionView` interface pattern. Each top-level section (Mail, Contacts, Calendar, Journal) implements `sectionView` and owns its data, fetch commands, key handling, rendering, and help bindings. The main model delegates to the active view.

Four files implement `sectionView` -- `mail.go`, `contacts.go`, `calendar.go` and
`journal.go`, one per section. `screener.go` implements it too, but The Screener is not a
section: ctrl+s from the mail list swaps it in as the active view, it captures every key
while it is open, and it asks to be closed again with `screenerClosedMsg`.
The rest of `internal/tui/` is shared infrastructure, starting with `tui.go` (model and
router) and `section_view.go` (the interface).

To add a new section: implement the `sectionView` interface in a new file, add a field and constructor call in `newModel`, and add a case in `switchSection`.

### Subsystems

Each subsystem below has a design doc in `docs/architecture/`. The rules here are the ones
a change is most likely to break. Read the doc before you change the subsystem.

#### Threads and replies

`Topics.Get` carries no bodies. `internal/threadload` reads them, within literal limits,
and reports per entry whether each body was read. A reply starts from
`mail.ReplyPrefillFromServer` and answers `mail.ReplyTarget`. Extend those two rather than
reimplement HEY's rules in a caller. Compose forms filter recipient suggestions in memory,
so a keystroke never waits on the network.
Details: [docs/architecture/threads-and-replies.md](docs/architecture/threads-and-replies.md).

#### Terminal safety

Nothing HEY serves reaches a terminal raw. Metadata goes through
`terminal.Sanitize`/`SanitizeLine`. A body is an `htmlutil.Markdown`, which only
`htmlutil.ToMarkdown` makes and only `markdown.Render` accepts. JSON goes through
`output.MarshalJSON`. `TestSinksAreSanitized` catches a direct write of a listed field to a
listed sink, and a new exemption in its manifest needs a reason. `sdkContactToModel` and
`sdkRecordingToModel` are deliberately left unsanitized, because their fields go back
through edit forms. Sanitize where they are shown.
Details: [docs/architecture/terminal-safety.md](docs/architecture/terminal-safety.md).

#### Email bodies

`htmlutil.ToMarkdown` converts a body once, at the edge, and everything downstream carries
Markdown. `Entry.BodyHTML` keeps the original HTML for `--html`. An inbound HTML email
hides its whole body in a Trix attachment with no `filename`. Do not treat that as a file.
Details: [docs/architecture/email-bodies.md](docs/architecture/email-bodies.md).

#### Mail sources and paging

`mail.ReadPage` is the one read for a box, a label, a collection, a bundle and a contact's
threads. A source knows its kind from `BoxKind`, never from its name. `Page.Cursor` is
opaque and differs by kind. A box pages by HEY's `next_history_url` on its own route,
dispatched on `BoxKind`. A label and a collection page by the geared_pagination cursor in
the `Link` header, which only the SDK's `*Page` methods keep. Search and contacts number
their pages instead. A live
re-read splices the top page in through `refreshHead`.
Details: [docs/architecture/mail-sources-and-paging.md](docs/architecture/mail-sources-and-paging.md).

#### Watching for changes

Both `hey watch` and the TUI take an Action Cable broadcast as a doorbell, not as the
change. `hey watch` then reads the change from the posting sync feed, whose cursor is the
source of truth, and its start, resync and "new mail" rules are subtle. The TUI just
re-reads the box's top page, and re-reads the box on screen after a reconnect. Read the
doc before you change `watch*.go` or `tui_watch.go`.
Details: [docs/architecture/watch.md](docs/architecture/watch.md).

#### Calendar and time tracking

A selection is a `Recording.key()`, not an id, because HEY serves a repeating event's virtual days with id 0.
Delete a repeating day through its occurrence. Tracked time is readable only through
`TimeTracks().ListPage`. An event update is not a partial, so send every field back.
Details: [docs/architecture/calendar.md](docs/architecture/calendar.md).

#### Imbox cover art

Covers use the ANSI-16 slots, never hex, with one palette per preset. Only the Imbox is
coverable, as `mail.Source.Coverable()` decides. The choice is stored locally on purpose.
Details: [docs/architecture/cover-art.md](docs/architecture/cover-art.md).

#### Inline images

The TUI draws images with Kitty's Unicode placeholders. Uploads are PNG, and image ids
come from `nextImageID` and are never reused.
Details: [docs/architecture/inline-images.md](docs/architecture/inline-images.md).

#### Upgrade command

`hey upgrade` hands Homebrew and Scoop installs to their package manager. On its own path
it trusts nothing it downloaded until sigstore verifies the release's checksums.
Its tests go through seam variables and never talk to GitHub or run a package manager.
Details: [docs/architecture/upgrade.md](docs/architecture/upgrade.md).

### API documentation

If you are unsure what the API endpoints are, what they expect or what they respond to you can read through the server implementation to understand how the API works.

The server is the `basecamp/haystack` repo. If you have it checked out (conventionally
`~/Work/basecamp/haystack/`), read it whenever you need to know how the API behaves.

If you don't understand how the routes are laid out you can call rails routes in that directory to get a list of all the routes and their corresponding controller actions.

### SDK

All API interactions must go through the HEY SDK (`hey-sdk/go`). There is no legacy client — the SDK is the only HTTP client.

If you need to call an endpoint that the SDK doesn't support yet, **add it to the SDK**.
The SDK is the `basecamp/hey-sdk` repo, conventionally checked out at
`~/Work/basecamp/hey-sdk`. To use your local changes:

1. Add a `replace` directive in `go.mod` pointing at your checkout. Give it a real path.
   The shell does **not** expand `~` in the middle of an argument, so
   `...hey-sdk/go=~/Work/...` reaches Go as a literal tilde and the command fails with
   `unversioned new path must be local directory`:

   ```bash
   go mod edit -replace github.com/basecamp/hey-sdk/go="$HOME/Work/basecamp/hey-sdk/go"
   ```
2. Implement and test your changes
3. **Call out that you made changes to the SDK** when you're done — your operator will review those changes and publish a new SDK release, then you can remove the `replace` directive and pin to the released version

Those paths are in the SDK repo, not this one. There, the hand-written service wrappers
in `go/pkg/hey/` (e.g. `messages.go`, `journal.go`, `postings.go`) are safe to edit
directly. The Smithy-generated code in `go/pkg/generated/` is not — update the Smithy
model and run `make smithy-build` then `make go-generate` from the SDK root. hey-cli has
neither target.

### Examples

When you add any kind of example make it realistic.

For emails, always use @example.com or @example.org domains to avoid accidentally sending emails to real people.
For names, use common names or fictional characters. For calendar events, use plausible titles and times. 
The goal is to make the examples feel authentic without risking privacy or confusion.
Never use abbreviations or placeholders like "Test Event", "User1", "a@ex.com". Instead, use full names and descriptive titles that reflect real-world usage.

### Unit Testing

Whenever you add, remove or change any functionality add/remove/change tests as well. Tests are located in the same package as the code they test, with filenames ending in `_test.go`. Run `make test` to run all tests.

### Smoke Testing

Smoke tests verify all CLI commands against a real HEY server. They live in `tests/smoke/` as a separate Go module and use a pre-compiled binary built by `make build`.

**What they test:** Every CLI command and its flags — boxes, box, compose, reply, threads, drafts, calendars, recordings, todo, journal, habit, timetrack, seen/unseen, config, auth, and all output format flags (--json, --quiet, --ids-only, --count, --markdown, --styled, --verbose, --stats). Browser-based cross-verification tests confirm CLI actions are visible in the browser and vice versa.

**Running:**

```bash
make test-smoke   # Builds binary then runs tests (requires dev server)
```

The dev server must be running at `http://app.hey.localhost:3003` (override with `HEY_SMOKE_BASE_URL`). Default login: `david@basecamp.com` / `secret123456` (override with `HEY_SMOKE_EMAIL` and `HEY_SMOKE_PASSWORD`).

**How they work:**

1. `TestMain` in `helpers_test.go` orchestrates setup: finds the binary, checks server reachability, launches headless Chrome via chromedp to log in and extract the `session_token` cookie, then authenticates the CLI with `hey auth login --cookie`.
2. All CLI invocations run in an isolated environment: temp `XDG_CONFIG_HOME`, `HEY_NO_KEYRING=1`, `HEY_BASE_URL` pointing to the dev server.
3. Helper functions (`hey()`, `heyOK()`, `heyJSON()`, `heyFail()`) run the binary and parse output. `dataAs[T]()` generically unmarshals response data.
4. Tests that depend on write operations (compose, todo add, journal write, reply, timetrack start) skip gracefully when the server returns errors, since the SDK's parameter format may not match the server's expectations. Set `HEY_SMOKE_STRICT=1` to turn every such skip into a failure — that is the release gate, where a skipped check is not a passed one.
5. Test data uses `uniqueID()` (nanosecond timestamps) to avoid collisions. Cleanup happens via `t.Cleanup()`.
6. `hey upgrade` is covered only as far as a dev build's `upgrade_required` refusal (and `hey version --json`). The live self-update against a real release runs in `.github/workflows/upgrade-smoke.yml`, never against the dev server.

**How to add a new test:**

1. Create or edit a `*_test.go` file in `tests/smoke/` (package `smoke_test`).
2. Use `heyJSON(t, "command", "args...")` for commands that should succeed and return JSON.
3. Use `heyFail(t, "command", "args...")` for commands that should fail.
4. For write operations that may fail server-side, use `hey(t, ...)` directly and skip on non-zero exit through the strict-aware helper: `if code != 0 { skipf(t, "... (exit %d): %s", code, stderr) }`. Never call `t.Skip` directly — `skipf` is what turns a skip into a failure under `HEY_SMOKE_STRICT=1`.
5. Use `dataAs[T](t, resp)` to unmarshal response data into typed structs.
6. For browser cross-verification, use `browserPageText(t, url)` to get page content.

### Running

To run the CLI use `make build` and then `./bin/hey`. This ensures that you and I are running the same version of the program.

## Code style

See `STYLE.md` for the Go conventions used here. Read it when writing or reviewing Go in
this repo -- it is not imported, so it does not sit in context for sessions that never
touch Go.
