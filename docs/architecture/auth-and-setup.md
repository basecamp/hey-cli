# Authentication, setup and agent integration

Authentication supports four methods, all managed through `internal/auth/`:

1. **Browser-based OAuth with PKCE** (primary) — `hey auth login` opens a browser for OAuth authentication against HEY's own OAuth server (`/oauth/authorizations/new`), using PKCE (S256) for security. A local callback server on an ephemeral `127.0.0.1` port (RFC 8252 §7.3) receives the authorization code, which is exchanged for access and refresh tokens at `/oauth/tokens`.
2. **Pre-generated bearer token** — `hey auth login --token TOKEN` stores a token directly.
3. **Browser session cookie** — `hey auth login --cookie COOKIE` uses an existing HEY.com session.
4. **Environment variable** — Set `HEY_TOKEN` to use a token without storing it.

The auth Manager (`internal/auth/auth.go`) proactively refreshes tokens with a 5-minute expiry buffer. The SDK uses the Manager to authenticate requests via a bridge in `internal/cmd/sdk.go`.

All data-access commands call `requireAuth()` before making API calls. At an interactive
terminal with styled output, `requireAuth()` offers to sign in on the spot ("Not logged in.
Sign in now?" → OAuth → the command continues); piped, machine-output or declined runs get
`Error: Not logged in` / `Run: hey auth login` with exit code 3. Auth subcommands (`hey auth
login`, `hey auth logout`, `hey auth status`), the `hey login`/`hey logout` shortcuts,
`hey setup` and `hey doctor` work without authentication.

Bare `hey` at an interactive terminal runs the setup wizard when logged out
(`runSetupWizard` in `internal/cmd/setup.go` — welcome, OAuth sign-in, signed-in identity,
coding-agent setup, summary) and stops at the summary; `hey setup --skip-agents`
and `--skip-omarchy` leave those integrations unchanged. In every other case bare `hey`
prints help. The TUI lives at `hey tui` (plus the hidden `hey hey`). `config.json`'s
`onboarded` flag only trims a later logged-out run to the sign-in step. `HEY_NONINTERACTIVE=1`
disables interactive sign-in regardless of TTY detection; detected agent setup continues
without prompting.

On Omarchy, the full `hey setup` wizard installs and enables the `37signals.hey` bar plugin
without prompting. Other interactive OAuth sign-ins — `hey auth login`/`hey login`, the
lite wizard, or `requireAuth`'s prompt — offer it once and remember the answer in
`StateDir()/omarchy/bar-plugin.json`, serialized by a flock next to it. The sign-in hook
never runs for machine output, `HEY_NONINTERACTIVE`, a non-TTY, or `--token`/`--cookie`
logins, and it never re-enables a plugin that is off the bar. The full wizard and explicit
`hey setup omarchy` do re-enable it; the explicit command installs in every output format
and fails loudly (`setup_failed`) on any incomplete outcome. `--remove` writes its
tombstone first, disables, and keeps the checkout. Details and the state model are in
docs/omarchy.md.

Coding-agent integration lives in `internal/harness` (agent registry, Claude Code / Codex
detection, plugin and skill health checks) and `internal/cmd/setup_agent*.go` (`hey setup
claude|codex|agents`). Claude Code gets the `hey@37signals` plugin from `basecamp/claude-plugins`
plus a skill link; Codex discovers the shared `~/.agents` skill directly. `HEY_SETUP_AGENT`
selects the target for `hey setup agents`; `hey setup agents --remove` uninstalls the
Claude plugin and removes only hey-cli-managed skill files. `hey doctor` reports per-agent diagnostics, and a
`PersistentPostRunE` hook (`skill_refresh.go`) re-syncs installed skill copies once per release
version change. Ownership is explicit: every skill write goes through `claimSkillDir`, which
marks a directory it creates (`.managed-by-hey-cli`) and refuses a populated one without the
marker; removal (`removeExistingSkillLink`) touches only our canonical symlink or a marked
copy; refresh writes only marked, non-symlinked files. Do not add a write path that bypasses
the gate.

New top-level commands and subcommands need the `.surface` snapshot updated
(`go test ./internal/cmd -run TestSurfaceSnapshot -update-surface`), and anything listed in
root help needs the byte-exact literal in `help_test.go` updated.
