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

## Architecture Overview

This is a Go project that uses:
- [spf13/cobra](github.com/spf13/cobra) for the CLI interface
- [charm.land/bubbletea/v2] for the TUI interface along with bubbles/v2 and lipgloss/v2 (these are new versions that recently came out and differ from the v1 versions!)

All API interactions go through the HEY SDK (`hey-sdk/go`), with typed service methods accessed via `internal/cmd/sdk.go` (e.g., `sdk.Boxes().List`, `sdk.Messages().Create`, `sdk.Calendars().GetRecordings`). Authentication and token refresh are handled via `internal/auth/`.

### Authentication

Authentication supports four methods, all managed through `internal/auth/`:

1. **Browser-based OAuth with PKCE** (primary) — `hey auth login` opens a browser for OAuth authentication against HEY's own OAuth server (`/oauth/authorizations/new`), using PKCE (S256) for security. A local callback server on `127.0.0.1:8976` receives the authorization code, which is exchanged for access and refresh tokens at `/oauth/tokens`.
2. **Pre-generated bearer token** — `hey auth login --token TOKEN` stores a token directly.
3. **Browser session cookie** — `hey auth login --cookie COOKIE` uses an existing HEY.com session.
4. **Environment variable** — Set `HEY_TOKEN` to use a token without storing it.

The auth Manager (`internal/auth/auth.go`) proactively refreshes tokens with a 5-minute expiry buffer. The SDK uses the Manager to authenticate requests via a bridge in `internal/cmd/sdk.go`.

All data-access commands call `requireAuth()` before making API calls. Auth subcommands (`hey auth login`, `hey auth logout`, `hey auth status`) work without authentication.

### State storage

Configuration (base URL only) is stored in `~/.config/hey-cli/config.json`. Credentials are stored in the system keyring (service name: `hey`) with automatic fallback to `~/.config/hey-cli/credentials.json` when the keyring is unavailable. Set `HEY_NO_KEYRING=1` to force file storage.

### CLI

Remember to update the examples in the README when you change, add or remove CLI commands.

### HTML content

**Scraping is being removed, not extended.** HEY grew JSON for the endpoints the CLI reads,
the SDK dropped its scrapers in v0.4.0, and what is left here is on its way out. Do not
answer "the JSON looks incomplete" by parsing a web page — check whether the SDK already
has a typed operation, and if HEY does not serve JSON for what you need, add it there
rather than working around it here.

The journal used to be the example of this: a day with an entry answered 204, so the
content was scraped from the Trix input on `/calendar/days/{date}/journal_entry/edit`.
HEY answers the entry itself now, a 204 means the day is empty, and the scrape is gone.

What still reads HTML, and only until the typed reads replace it: `hey reply`, `hey topic`
and two TUI paths use `internal/htmlutil`'s `ParseTopicEntriesHTML` and
`ParseTopicAddressed` to find a thread's entries and recipients. `Topics.Get` carries the
topic's entries as of SDK v0.4.0, so this can go; `resolveThreadReply` in
`internal/cmd/thread_reply.go` is the one place to change.

`internal/htmlutil` also provides `ToMarkdown` (HTML→Markdown), `ToText` (HTML→plain text)
and `ExtractImageURLs`, which are presentation helpers rather than scrapers and are
staying. HEY uses the Trix editor with `<figure data-trix-attachment="{...}">` for
attachments — image URLs in those attributes are relative paths requiring authentication
via `sdk.Get`.

### Email bodies are Markdown

An email body reaches the CLI and the TUI as HEY's Trix HTML and is converted once, at the
edge, by `htmlutil.ToMarkdown`. Everything downstream — `models.Entry.Body`, `--json`, the
TUI viewport — carries Markdown, so links keep their URLs and headings, lists, quotes,
tables and code survive. `Entry.BodyHTML` keeps the original HTML for `--html` and for
`ExtractImageURLs`/`ExtractAttachments`, which need the attributes the Markdown drops.

`internal/markdown` renders that Markdown for a terminal: `Render(md, width)` wraps
glamour, and `LinkifyURLs` wraps bare URLs in OSC 8 so they stay clickable. The style in
`style.go` uses ANSI color slots rather than glamour's fixed palettes, for the same reason
`internal/tui/styles.go` does — the user's terminal theme wins. `Render` never fails
loudly: if glamour cannot be set up it returns the Markdown source, so a styling problem
costs formatting rather than the message.

`ToText` is still right where the result is not shown to anyone — the name tag
`hey bulk-reply` reads out of a draft, for instance.

#### Inbound email hides its body in an attachment

`/messages/{id}` serves `content` through haystack's `trix_html_for_rich_text_editing`,
which has to hand Trix something Trix can edit. An HTML email from outside HEY is not
that, so the whole body is wrapped in one `<figure data-trix-attachment>` whose JSON
carries no `filename` and no `url` — just `contentType: "text/html"` and a `content`
string holding the original markup inside `<shadow-content><template>…`.

Treat that figure as a file attachment and the body vanishes: the TUI then shows nothing
but `Entry.Summary`, HEY's ~105-character preview ending in an ellipsis, which reads as a
truncated email. `htmlutil` tells the two apart on `filename` and parses the embedded
markup instead, bounded by `embeddedContentDepthLimit` so a chain of attachments that
each embed another cannot recurse without end. `ExtractImageURLs` looks inside it too,
since that is where an inbound email's inline images are; `ExtractAttachments` does not
list it, because an embedded body is not a downloadable file.

The web app has the same content and renders it in a sandboxed iframe, which is why the
`srcdoc` scrape in `ParseTopicEntriesHTML` never hit this.

### TUI structure

The TUI uses the `sectionView` interface pattern. Each top-level section (Mail, Calendar, Journal) implements `sectionView` and owns its data, fetch commands, key handling, rendering, and help bindings. The main model delegates to the active view.

Four files implement `sectionView` -- `mail.go`, `contacts.go`, `calendar.go` and
`journal.go`, one per section. `screener.go` implements it too, but The Screener is not a
section: ctrl+s from the mail list swaps it in as the active view, it captures every key
while it is open, and it asks to be closed again with `screenerClosedMsg`.
The rest of `internal/tui/` is shared infrastructure: `tui.go` (model and
router), `section_view.go` (the interface), plus `nav.go`, `content.go`, `help.go`,
`styles.go`, `loading.go`, `kitty.go`, `html.go` and `calendar_views.go`. Read the directory rather than a
table here.

To add a new section: implement the `sectionView` interface in a new file, add a field and constructor call in `newModel`, and add a case in `switchSection`.

### Inline images in the TUI

The TUI renders inline images using the Kitty graphics protocol's Unicode Placeholder extension (`internal/tui/kitty.go`). This works because Bubble Tea's cell-based renderer corrupts raw APC escape sequences, but Unicode placeholders are regular text that survives rendering. The approach has three steps:

1. **Upload** — image data is sent to the terminal via `tea.Raw()` with `a=t` (transmit only) and `q=2` (suppress response), then a virtual placement is created with `U=1`.
2. **Display** — U+10EEEE placeholder characters with combining diacritics (encoding row/column) are placed in the viewport content. The image ID is encoded in the foreground color.
3. **Sizing** — `image.DecodeConfig` reads dimensions without full decoding; terminal cell count accounts for ~2:1 height:width cell ratio.

This works in Kitty and Ghostty. Other terminals show the text content normally (placeholders are invisible).

### Watching for changes over Action Cable

`hey watch` is told when a box changed instead of polling for it.
`internal/cable` dials HEY's cable server with [actioncable-go](github.com/basecamp/actioncable-go),
authorizing the upgrade request with the same credentials the SDK sends on an API request
(`HEY_CABLE_URL` overrides the endpoint). `internal/cmd/watch.go` subscribes to
haystack's `Postings::ChangesChannel`, which broadcasts only `{change, account_id, box_id,
box_kind, posting_ids, at}` — a doorbell, not the change itself.

The change is then read through `Postings().AllChanges`, the same incremental sync feed the
mail clients use, starting from the cursor in the box's `posting_changes_url`. That is what
makes a reconnect safe: the cursor, not the notification, is the source of truth, so a
missed broadcast costs nothing, and a 409 means catch up in full instead. A read that
fails leaves the cursor where it was and is retried on a doubling backoff, so a change
isn't lost with the notification that announced it, and a subscription that closes without
the watch being interrupted is an error rather than a quiet exit.

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
4. Tests that depend on write operations (compose, todo add, journal write, reply, timetrack start) skip gracefully when the server returns errors, since the SDK's parameter format may not match the server's expectations.
5. Test data uses `uniqueID()` (nanosecond timestamps) to avoid collisions. Cleanup happens via `t.Cleanup()`.
6. `hey upgrade` is covered only as far as a dev build's `upgrade_required` refusal (and `hey version --json`). The live self-update against a real release runs in `.github/workflows/upgrade-smoke.yml`, never against the dev server.

**How to add a new test:**

1. Create or edit a `*_test.go` file in `tests/smoke/` (package `smoke_test`).
2. Use `heyJSON(t, "command", "args...")` for commands that should succeed and return JSON.
3. Use `heyFail(t, "command", "args...")` for commands that should fail.
4. For write operations that may fail server-side, use `hey(t, ...)` directly and skip on non-zero exit: `if code != 0 { t.Skipf("... (exit %d): %s", code, stderr) }`.
5. Use `dataAs[T](t, resp)` to unmarshal response data into typed structs.
6. For browser cross-verification, use `browserPageText(t, url)` to get page content.

### Upgrade command

`hey upgrade` (`internal/cmd/upgrade.go`, `upgrade_selfupdate.go`, `release.go`) self-updates
installer/tarball installs under `$HOME` and delegates Homebrew and Scoop installs to their
package manager; system packages, Nix and `go install` builds are refused with guidance, as is
any build whose version is not a semantic version (`dev` included). The native path trusts
nothing it downloaded until the release's `checksums.txt.bundle` verifies with sigstore-go
against the Sigstore public-good trusted root (TUF-cached under `config.CacheDir()/sigstore-tuf`),
with the signing identity pinned to
`https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v<version>` and
the GitHub Actions OIDC issuer. Only then is the archive's SHA-256 checked against the verified
`checksums.txt`, the binary extracted with hardening (no links, no nested paths, size caps),
probed with `--version`, and swapped in behind a `flock` with the old binary preserved until
the installed one is confirmed.

The verification tests in `upgrade_selfupdate_test.go` run hermetically against real release
fixtures in `internal/cmd/testdata/selfupdate/` (see its README for provenance — they are from
basecamp-cli until hey-cli has a signed release of its own). Everything else goes through the
seam variables (`releaseFetcher`, `bundleVerifier`, `binaryVersionProber`, …) so no test talks
to GitHub or runs a package manager.

### Running

To run the CLI use `make build` and then `./bin/hey`. This ensures that you and I are running the same version of the program.

## Code style

See `STYLE.md` for the Go conventions used here. Read it when writing or reviewing Go in
this repo -- it is not imported, so it does not sit in context for sessions that never
touch Go.
