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

**Omarchy**

```bash
omarchy-mise-install github:basecamp/hey-cli hey
```

**macOS / Linux / WSL2**

```bash
curl -fsSL https://hey.com/install-cli | bash
```

Or with mise:

```bash
MISE_MINIMUM_RELEASE_AGE=0 mise use -g github:basecamp/hey-cli
```

The override selects the current release immediately; mise otherwise waits for a new
release to be 24 hours old.

**Windows (PowerShell)**

```powershell
irm https://hey.com/install-cli.ps1 | iex
```

On Windows 11 with Smart App Control, see [Troubleshooting](#windows-smart-app-control-and-smartscreen) if the install is blocked.

## Getting started

```bash
hey
```

The first time you run `hey` at a terminal it walks you through setup: it signs you in
with browser-based OAuth, confirms your identity, and connects your detected coding agents
(Claude Code, Codex). Run `hey setup --skip-agents` to leave
agent integrations unchanged, or add `--skip-omarchy` to leave Omarchy unchanged.
`hey setup --silent-success` keeps required authentication visible, shows an installation
spinner, and ends with `SETUP COMPLETE`; failure guidance remains visible. After that,
`hey tui` opens the app and
bare `hey` prints the help. `hey setup` reruns the wizard any time; `hey login` and
`hey logout` are shortcuts for `hey auth login` and `hey auth logout`.

Logged-out data commands at a terminal (`hey box list`, say) offer to sign you in on the spot.
Piped or `--json` runs never prompt: they fail with `Not logged in` (exit 3) so scripts and
agents can handle it.

Both scripts download the release for your platform, verify its SHA-256 checksum, and — when `cosign` is installed — verify the release's keyless Sigstore signature (cosign v3 as-is, v2.6+ with `--new-bundle-format=true`; older versions skip signature verification with a warning). Set `HEY_VERSION` to pin a release and `HEY_BIN_DIR` to choose the install directory.

<details>
<summary>Other installation methods</summary>

**mise:**
```bash
MISE_MINIMUM_RELEASE_AGE=0 mise use -g github:basecamp/hey-cli
```

**Homebrew (macOS / Linux):**
```bash
brew install --cask basecamp/tap/hey
```

**Arch Linux (AUR):**
```bash
yay -S hey-cli
```

**Linux (deb/rpm/apk):**
```bash
# Download from https://github.com/basecamp/hey-cli/releases/latest
sudo apt install ./hey-cli_*_linux_amd64.deb                 # Debian/Ubuntu
sudo dnf install ./hey-cli_*_linux_amd64.rpm                 # Fedora/RHEL
sudo apk add --allow-untrusted ./hey-cli_*_linux_amd64.apk   # Alpine
```
Arm64: substitute `arm64` for `amd64` in the filename. Verify the SHA-256 checksum from `checksums.txt` before installing unsigned Alpine packages.

**Scoop (Windows):**
```powershell
scoop bucket add basecamp https://github.com/basecamp/homebrew-tap
scoop install hey
```

**Nix:**
```bash
nix profile install github:basecamp/hey-cli
```

**Go install:**
```bash
go install github.com/basecamp/hey-cli/cmd/hey@latest
```

**From source** (requires Go 1.26+; [mise](https://mise.jdx.dev) installs the right version):
```bash
mise install       # install Go 1.26
make install       # build and install into /usr/local/bin/hey
```

**GitHub Release:** download from [Releases](https://github.com/basecamp/hey-cli/releases). Every release ships `checksums.txt` and a keyless Sigstore signature `checksums.txt.bundle`, verifiable with:
```bash
cosign verify-blob --bundle checksums.txt.bundle \
  --certificate-identity "https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v<VERSION>" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
```
That command is for cosign v3. With cosign v2.6–v2.x add `--new-bundle-format=true`; older versions cannot verify the bundle.

</details>

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

## Upgrading

```bash
hey upgrade
hey upgrade 0.2.0-rc.1   # target a specific release, e.g. a prerelease
```

Upgrading only ever moves forward: a requested version at or below the installed one is a
no-op. Mise and native installs can select a specific version; Homebrew and Scoop always
follow their manager's own version and refuse a pinned upgrade.

What happens depends on how hey was installed:

- **Installer script / tarball** (a binary under your home directory, e.g. `~/.local/bin` or `~/bin`): upgrades in place. hey downloads the release for your platform, verifies its Sigstore signature (the keyless `checksums.txt.bundle` published by the release pipeline, identity-pinned to the release workflow and tag) and SHA-256 checksum, swaps the executable transactionally, and confirms the installed binary reports the new version. On failure the previous binary is restored; in the worst case — restoration itself fails mid-swap — the error names the preserved backup file next to the binary so you can put it back by hand.
- **mise**: delegates to mise, updates the configuration that selected the running binary, then verifies normal mise resolution selects the new version.
- **Homebrew / Scoop**: delegates to `brew upgrade --cask basecamp/tap/hey` / `scoop update hey`, then verifies the manager-installed binary actually reports the new version.
- **System packages** (apt/dnf/apk, AUR, Nix) and **`go install` builds**: never touched. `hey upgrade` exits nonzero with upgrade guidance for that install method (the exact command where it can be known, e.g. `go install` or `yay -S hey-cli`; otherwise which package manager to use).

`hey upgrade` exits 0 only when there is no update, or the update was applied *and confirmed*. Every other outcome is a structured failure (`"ok": false` in JSON) with one of these codes:

| Code | Meaning |
|---|---|
| `upgrade_required` | An update exists but hey won't apply it for this install method (or this is not a release build) — the hint carries the right next step |
| `upgrade_incomplete` | The package manager exited 0 but the binary still reports the old version |
| `upgrade_unverified` | The upgrade may have worked, but the installed version could not be confirmed |
| `upgrade_failed` | The update check, download, signature/checksum verification, or executable swap failed — the previous binary remains installed (or the error names the preserved backup if restoration also failed) |

`hey version` prints the installed version; `hey version --json` adds the commit, build date, Go version and build source (`release`, `go install` or `dev`). `hey doctor` warns when a newer release is available.

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

## TUI

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

Navigate between Mail, Contacts, Calendar, and Journal. The context-sensitive shortcut bar is visible by default; press `?` to hide or restore it, and the choice is remembered across restarts. Mail navigation includes HEY boxes plus separate Labels and Collections tabs; Shift+L opens Labels directly and Shift+K opens Collections. Previously Seen has its own tab after the boxes — `9`, the web app's shortcut — showing the Imbox's already-read threads newest-seen first, with the usual thread actions available; Escape returns to the box you were in. Every list keeps going: scroll towards the bottom of a box, label, or collection and the next threads are read in behind you, so there are no pages to step through. The mail actions use HEY's web shortcuts in either letter case (except `l`, whose uppercase belongs to Labels): `/` or `s` searches, `r` replies, `f` forwards, `v` moves, `b` manages labels, `n` adds or removes the selected thread from collections, `e` marks seen, `u` marks unseen, `i` moves to the Imbox, `l` moves to Reply Later, `a` moves to Set Aside, `d` moves to The Feed, `p` moves to Paper Trail, and `t` trashes. Press `!` to mark as spam, `-` to ignore, and `+` to stop ignoring. Select threads with Space and press Ctrl+B to preview every bulk-reply recipient before writing and sending one reply to all selected threads. A delayed bulk reply can be recalled with Ctrl+U while HEY's undo window remains open. Search results retain the matching-message summary and keep going as you scroll, like every other list. While writing a new message, reply, or forward, press Ctrl+T to open the searchable Snippets picker. HEY never chooses a default: Enter inserts the selected snippet at the body cursor, Escape returns without changing the draft, and the picker can be reopened to insert another snippet.

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

Press Ctrl+S from the mail list to open The Screener. When senders are waiting, the mail
list says so above the threads. In The Screener, `y` screens the selected sender in and `n`
screens them out, Tab moves to Screener History and back, `X` clears the whole Screener
after a confirmation, and Escape or `q` returns to mail. Both lists keep going as you
scroll, the same way the mail list does.

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

Thread attachments always appear with their filename, media type, and size. Use `[` and `]` to select an attachment, `s` to save it without replacing an existing file, and `o` to download and open it in an external application. Attachments never open automatically. Kitty and Ghostty can show inline images. Foot and other terminals use visible text markers.

Press Shift+O to open Contacts. Use Enter to view a contact, `a` to add, `e` to edit, `n` to edit the private note, `x` twice to delete a note, `h` to hide, and `u` to show the most recently hidden contact again. Escape or `q` goes back.

Press Shift+C to open Calendar, then `c` to manage time track categories. Create a category with `n`, rename the selected category with Enter or `r`, and press `x` twice to delete it. Time tracks in a deleted category become uncategorized.

In Calendar, press `a` to create a habit. Habits visible in the current calendar range can be selected with `[` and `]`, edited with `e`, and deleted by pressing `x` twice. Habit forms use Tab to move between fields and Ctrl+S to save.

## CLI Commands

Structured data commands support `--json` for full output and `--jq '<expression>'` to
filter that output without an external `jq` binary. `--jq` implies `--json` and filters
the full success envelope; combine it with `--quiet` to filter result data directly.
Errors retain their complete structured envelope. Commands with dedicated raw output
(`auth token`, `completion`, `skill`, `tui`, and `--version`) reject `--jq`.

Use `--base-url` to override the server URL and `--account <id|all>` to select a linked
mail account.

```bash
hey box list --jq '.data[] | {id, name}'
hey box list --quiet --jq '.[].id'
```

Listing commands also answer `--markdown` for a table, `--styled` to force the human
rendering when the output is piped, `--ids-only` for one ID per line, and `--count` for a
bare number. `--ids-only` and `--count` need list data, so they work on `hey box list`,
`hey box view`, `hey label list`, `hey label view`, `hey collection list`, `hey collection view`,
`hey workflow list`, `hey workflow view`, `hey clip list`, `hey snippet list`, `hey draft list`, `hey search`, `hey contact list`, `hey screener list`, `hey screener history`, `hey calendar list`,
`hey event list`, `hey todo list`, `hey habit list`, `hey timetrack list` and
`hey journal list`.
The
data-only formats print any pagination notice on stderr, so the IDs on stdout stay
pipeable. `hey clip list --ids-only` and `--count` cover the newest page only because the
released SDK does not expose HEY's cursor for older clip pages.

`--html` writes the original HTML, for the commands that hold some: `hey thread read`,
`hey draft show`, `hey journal read`, `hey contact show` and `hey contact note show`. It
is a format of its own — it cannot be combined with the other output flags (`--stats`
included: there is no envelope to carry stats), every other command refuses it, and it is
meant for a file or a pipe: on a terminal it is refused with the redirect spelled out,
since markup on a terminal is neither readable nor safe.

A thread is written as one HTML5 document, so a downstream tool can parse it rather than
split it: `<!doctype html>`, `<html lang="en">`, a `<head>` with `<meta charset="utf-8">`
and `<title>Thread N</title>`, then in `<body>` one
`<article id="entry-ID" data-entry-id="ID" data-created-at="…" data-body-state="…">` per
entry, oldest first. Each article opens with a `<header>` naming the sender and the date
(HTML-escaped) and then holds the entry's HTML exactly as HEY served it. An entry without
a body holds only its header, and `data-body-state` says why — `bodyless` when HEY served
none, `over_limit` or `failed` when the load left it unread, `hydrated` when it was read
and was empty. A thread that could only be read in part is refused as for every other
format; with `--allow-partial` the document ends with the notice in an HTML comment
(`<!-- notice: … -->`) just before `</body>`, and the notice goes to stderr as well.

A single body — a draft, journal entry, or contact note — is written as a fragment
instead: the HTML as HEY served it, nothing for an empty one. A draft fragment includes
the complete editable body, including attachment markup. A thread has entries to frame;
one body is what gets pasted into something else.

### Email

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
hey screener clear                 # empty the queue without deciding
hey thread read 123                    # read a full email thread
hey thread read 123 --markdown         # the thread as one Markdown document
hey thread read 123 --html > 123.html  # HEY's original HTML, to a file
hey share 123                      # get a sharing link for a thread
hey unshare 123                    # turn off the sharing link
hey attachment list 123                # list files attached to the thread
hey attachment save 456:1         # save a file using its attachment ID
hey reply 123 -m "Friday works for me — I'll send an agenda."  # or omit -m for $EDITOR
hey reply 123 -m "Here is the wiring diagram." --attach ./diagram.png
hey bulk-reply preview 12345 67890  # inspect threads and exact To/CC/BCC recipients
hey bulk-reply send 12345 67890 -m "Thanks for the update."
hey bulk-reply undo 98765            # recall a delayed bulk reply
hey forward 123 --to alice@example.com -m "For your review"  # forward the latest message
hey compose --to alice@example.com --subject "Lunch plans"  # body from $EDITOR
hey compose --to alice@example.com --subject "Q3 revenue report" -m "The numbers are attached." --attach ./report.pdf
hey compose --to alice@example.com --cc bob@example.com --bcc carol@example.org --subject "Kitchen remodel timeline"  # with CC/BCC
hey compose --to alice@example.com --subject "Sprint recap" -m "We **shipped** the pagination fix."
hey compose --to alice@example.com --subject "Newsletter draft" --message-html "<h1>March</h1><p>What we shipped.</p>"
hey compose --subject "Client invoice" --message-html-file ./invoice-email.html --attach ./invoice.pdf --draft
hey compose --subject "Board update" -m "Numbers to follow." --draft  # save a draft instead of sending
hey reply 123 -m "Drafting a longer answer." --draft  # save a reply draft
hey draft list                     # list drafts (--all and --page follow HEY's cursor)
hey draft show 12345               # read a draft back
hey draft show 12345 --jq '.data.attachments'  # filename plus available type/size metadata
hey draft show 12345 --html > draft.html  # complete stored HTML, including attachment markup
hey draft export 12345 --output ./draft-12345  # HTML, safe JSON manifest, and downloaded attachments
hey draft edit 12345 --to alice@example.com --subject "Board update (v2)"
hey draft edit 12345 --message-html-file ./revised-message.html
hey draft send 12345               # deliver it
hey draft delete 12345             # trash it
hey seen 12345                     # mark a thread as seen
hey unseen 12345 67890             # mark threads as unseen
hey move 12345 --to feed           # move a thread to another box
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

`hey thread read` reads a whole thread, oldest entry first, however many pages HEY serves it in — within limits it states: a hundred pages past the first, two thousand entries, as many bodies, 64 MiB of content and two minutes in all. A thread that could only be read in part — a body HEY would not serve, a limit reached — is refused rather than passed off as whole; `--allow-partial` takes what was read, with a `notice` saying what is missing and each entry's `body_state` saying whether its body was `hydrated`, `bodyless` (HEY served none), `over_limit` or `failed`. `--count` and `--ids-only` read the entry index and no bodies, so only a truncated index can make them partial. `--markdown` writes the thread as one Markdown document — a heading per entry naming the sender, date and ID, then the body — which is the shape to hand an agent or a notes app. `hey attachment list` reads the bodies in every format, since that is where attachment metadata lives, and answers a partial thread the same way. `hey reply` answers the thread's latest entry and addresses the reply the way HEY does: it asks HEY for the reply's recipients — everyone that entry was addressed to, its sender moved onto the To line, and your own addresses, aliases and catch-alls excluded — falling back to computing them from the entry when that read is unavailable.

Email bodies come back as Markdown. `hey thread read` and the TUI render that Markdown for the terminal — headings, emphasis, lists, quotes, tables and code survive, and links keep their URLs and stay clickable where the terminal supports it. `--json` carries the same Markdown in `body`, so an agent reading a thread sees the structure a human sees rather than a flattened wall of text. `--html` still returns HEY's original HTML.

Writing is Markdown too, everywhere text goes in: `-m`, `--content`, `--note`, positional content, stdin, and `$EDITOR` (which opens prefilled with the existing entry or note as Markdown). Every such flag has a raw-HTML twin — `--message-html`, `--content-html`, `--note-html` — for sending markup verbatim. Email commands that accept `--message-html` (`compose`, `reply`, `forward`, `bulk-reply send`, and `draft edit`) also accept `--message-html-file <path>` and read the file directly as raw HTML, which avoids shell quoting and argument-size problems for generated messages. Markdown, inline HTML, and HTML-file inputs are mutually exclusive. Compose, reply, and draft edit preserve the file bytes exactly; forward and bulk reply use their existing HTML-prefix join, which trims surrounding whitespace before appending quoted or Name Tag content. The TUI's compose and bulk-reply forms convert Markdown the same way, and the compose editor renders it live as you type — `**bold**` turns bold, markers and all. A fenced code block's language (` ```ruby `) is carried the way HEY's own editor stores it, so the web app syntax-highlights it.

Drafts are the review-before-send lane: `hey compose --draft` (and `hey reply --draft`) saves instead of sending — recipients optional on a draft — and answers the draft's ID. `hey draft show` reads it back with the body as Markdown. Its structured output has an `attachments` array with each downloadable file's name and available content type/byte size, while the styled view lists the same safe metadata; internal download URLs and signed IDs stay private. `--html` writes the byte-exact stored HTML fragment, including attachment markup, to a file or pipe. `hey draft export <id> --output <directory>` reads the same exact draft into a private local bundle: byte-exact `draft.html`, safe `draft.json`, and downloaded originals under `attachments/`, with collision-safe filenames, actual byte counts, and SHA-256 hashes. The command stages and verifies the whole bundle before publishing it, never changes HEY, and preserves any existing destination. `--force` only replaces a complete export of the same draft and refuses a directory with unrecognized files; it uses atomic directory exchange where the platform supports it and otherwise repairs an interrupted replacement on the next invocation before proceeding. `hey draft edit` revises the draft (each flag replaces its field; what is not flagged is kept, by reading the draft and resending the whole of it, since a revision is not a patch on HEY's side), and `--message-html-file` replaces the complete body with the exact HTML read from a file. `hey draft send` delivers through HEY's undo window, and `hey draft delete` trashes it. Scheduling a delivery is done in a HEY app for now — the API cannot yet name an exact instant — and a schedule set there survives CLI edits untouched. A draft prepared here is reviewed and sent from any HEY app, which is the workflow this is for: an agent writes, a person decides.

`hey share <thread_id>` gets a sharing link for a thread. Anyone with the link can see the entire thread and future emails or replies sent to it. `hey unshare <thread_id>` turns off the sharing link.

Search accepts free text plus `--required`, `--any`, `--none`, `--exact`, `--from`, `--to`, `--subject`, `--date`, `--in`, `--label`, and `--attachment`. `--in`, `--date`, `--label` and `--attachment` take one of the values `hey search filters` lists — the attachment kinds are `any`, `images`, `pdfs`, `calendar_invites`, `documents`, `spreadsheets`, `presentations`, `media` and `zip_files`, so it is `--attachment pdfs` rather than `pdf`, and an unrecognized `--in`, `--date` or `--attachment` is refused with the values it accepts before anything is sent. Use `--page` for one page or `--all` to fetch up to 100 pages; capped searches report the next page for continuation. Search results include `topic_id` for reading the thread and the matching message summaries. Results with an active box item also include `id` for organization actions.

Contact updates preserve omitted name, email, and alias fields. Supplying `--alias` replaces the complete alias list; `--alias=` clears it. Contact notes accept positional content, `--note`, stdin, or `$EDITOR`. HEY hides contacts rather than permanently deleting them; hidden contacts leave lists, autocomplete, and search, and can be shown again by ID. Bundling groups a contact's mail into one row without merging or deleting the underlying threads; unbundling lists those threads separately again. HEY applies bundling when the contact's current delivery setting supports bundles.

The Screener is where first-time senders wait. `hey screener list` returns clearance IDs — not contact IDs — with the sender and the subject of what they sent, plus `topic_id` for reading the thread before deciding. `--count` asks for the number alone, which is a far cheaper request than the queue, and prints it as a bare number like every other command's `--count`, so `n=$(hey screener list --count)` reads it directly. Approving delivers everything the sender has waiting; denying hides it. Either is reversible with the opposite command, and `hey screener history` shows what was already decided. Both listings page the way `hey box view` does: `--all` follows HEY's cursor to the end of the queue (up to 100 pages), and a single-page or capped read reports `next_page` in its JSON meta, which `--page <next_page>` continues from — the cursor is opaque, so a page *number* does not name a position. `hey screener list` also reports `total_count`, the whole queue's size, next to what the read returned. `--box` and `--seen` approve one sender at a time; several IDs go through HEY's bulk endpoint, which takes neither. `--spam` also trains HEY's filter, which is harder to undo than denying. `hey screener clear` empties the queue without deciding anything — those senders reappear on their next email.

`hey bulk-reply preview` is read-only and resolves each posting to its latest replyable entry. `hey bulk-reply send` resolves the selection again, skips threads without a replyable entry, keeps HEY's server-provided name tag, and returns the exact reply count, delivery ID, delayed state, undo URL, and undo command. Posting IDs must be positive and unique. The message can come from `-m`, stdin, `$EDITOR`, inline `--message-html`, or `--message-html-file`; `--attach` is repeatable.

`--attach` is repeatable on `hey compose`, `hey reply`, and `hey bulk-reply send`, and attachment-only messages are supported. The CLI validates and uploads every file before sending the email. `hey attachment list <thread-id>` returns stable message-and-position IDs such as `456:1`; pass an ID to `hey attachment save`. Saving uses the original filename by default, accepts `--output` for a file or directory, and preserves existing files unless `--force` is set.

Organization actions take the `id` values returned by `hey box view --json`, `hey label view --json`, or `hey search --json`. Reading, replying to, and forwarding a thread take its `topic_id` instead, which `hey box view --json`, `hey label view --json`, `hey collection view --json` and `hey search --json` all carry alongside `id`. `hey box view` also returns `next_page` and accepts `--page <next_page>` to continue a box listing; it keeps `next_history_url` for the sync clients that read it, and `--page` accepts that URL as readily as the cursor inside it. Label IDs come from `hey label list`; `hey label view` returns `next_page` and `total_count`, accepts `--page <next_page>` for continuation, and supports `--all` for complete traversal. HEY creates a label while adding it to at least one thread, so `hey label create` requires thread item IDs.

Collection IDs come from `hey collection list`. `hey collection view` returns both each posting `id` and its `topic_id`, plus `next_page` and `total_count`. Collection membership commands take `topic_id`; posting organization commands continue to take `id`. Creating a collection returns a confirmed mutation, and `hey collection list` provides its ID for subsequent commands. Collection updates accept a non-empty name, summary, or both.

Workflow IDs come from `hey workflow list`, which includes the linked account ID for each workflow. `hey workflow view <id>` returns stages in position order; `--ids-only` and `--count` apply to those stages. Creating a workflow needs one linked mail account, selected with `--account` when more than one is available. HEY creates new stages as `Untitled`, so create the stage, read its ID with `hey workflow view <id>`, then rename it. Workflow membership commands take `topic_id`. Adding a thread creates its workflow membership before selecting the requested stage; if stage selection fails, the thread remains in the workflow's first stage and the command reports the error.

Clips are passages saved from existing email entries. `hey clip list` lists the selected account's newest page with each clip's source entry and thread context; its JSON `notice` and the data-only formats' stderr make that boundary explicit because the released SDK does not expose HEY's cursor for older pages. `hey clip create <entry-id> --content <text>` verifies that the passage is source-backed by text carried in the entry, including embedded inbound email bodies. It accepts whitespace differences while preserving the supplied text exactly for HEY's web UI; passages are capped at 64 KiB and source-message validation at 1 MiB. HEY's web UI remains authoritative for stylesheet-driven visibility. HEY assigns a created clip to its source entry's account and resolves deletion by identity-owned clip ID across linked accounts; `--account` selects list presentation. `hey clip delete <clip-id>` removes it. Clip content is plain text; the source entry ID comes from `hey thread read --json`.

Snippets are named reusable email content, separate from clips saved out of received messages. `hey snippet list` lists both plain text and HEY's rich-text HTML; `hey snippet create`, `update`, and `delete` manage them. A create requires a non-empty name and content. Updates change whichever non-empty fields are supplied, while omitted fields stay as they are. In the TUI, Ctrl+T opens the picker from new-message, reply, and forward forms and inserts the snippet's plain-text representation at the current body cursor without replacing the draft.

`hey box view <name|id>`, `hey label view <id>` and `hey collection view <id>` list the same postings and answer the same formats: `--json`, `--styled`, `--markdown`, `--ids-only`, and `--count`. The data-only formats print the pagination notice and any `next_page` cursor on stderr, so the IDs on stdout stay pipeable. `--json` differs only in what wraps the postings: a box answers with HEY's box payload, a label and a collection with the source and its `total_count`.

Move destinations are Imbox, The Feed, Set Aside, Reply Later, or Paper Trail. Bubble Up has its own commands: `hey bubble up` raises a thread right away with `--now`, on a date with `--on` (HEY resurfaces it at its morning hour of that day, or its evening hour when the date is today), or at the morning hour of tomorrow, Saturday, or next Monday with `--tomorrow`, `--weekend`, and `--next-week`; `hey bubble pop` cancels one. `hey bubble list` shows both buckets — the threads back in the Imbox after bubbling up and the ones still scheduled, each with when it resurfaces. Trashing a shared thread removes your access instead of deleting it for everyone. Ignored threads remain in their box and can be restored with `hey stop-ignoring`.

### Watching for changes

```bash
hey watch                               # follow every box and calendar, a line of JSON per change
hey watch --box imbox --events added    # only new postings in the Imbox (calendars off)
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
is judged across all of them — a reply in The Feed and then a move into the Imbox is not. The
one-liner above is what any desktop does with it; on Omarchy the bar plugin reads the same
lines and sends one batched, replacing toast instead.

The calendars are followed too, by default. A changed event, todo, habit or journal entry
is a `recording_added`, `recording_updated` or `recording_deleted` line naming its
calendar — `{"change":"recording_added","calendar":{"id":512,"name":"Household"},
"recording_id":88001,"recording_type":"Calendar::Event","recording":{}}` — and a calendar
arriving, changing or leaving is `calendar_added`, `calendar_updated` or
`calendar_deleted`. A calendar whose feed fell too far behind is skipped ahead and says so
with `calendar_resync`, the way a box says `resync`. The email-specific flags switch the
calendars off: `--box` scopes the watch to mail, and an `--events` list naming only mail
changes does the same.

A change can drive a command instead of being printed, and there's a choice to make
between two behaviours — pass one or the other, not both. `--run-async` spawns the
command per change and moves on, so a slow one never holds up the watch and two can
overlap. `--run-sync` waits for each and runs them in order, so they never overlap and a
slow one delays the next.

Both hand the JSON to the command on its stdin, and the same fields as `HEY_CHANGE`,
`HEY_AT`, `HEY_BOX_ID`, `HEY_BOX_KIND`, `HEY_BOX_NAME`, `HEY_POSTING_ID` and
`HEY_THREAD_ID`, with `HEY_NEW=1` for new mail and `HEY_NEW=0` otherwise — and on a
calendar line `HEY_CALENDAR_ID`, `HEY_CALENDAR_NAME`, `HEY_RECORDING_ID` and
`HEY_RECORDING_TYPE` instead of the box and posting fields. Both also take over
stdout, so the JSON isn't printed as well.

### Calendars

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
hey event list                    # every calendar, from today onward
hey event list --calendar 123 --starts-on 2026-01-01 --ends-on 2026-01-31
hey event list --count            # how many events in the window

hey event add "Design review" --starts-on 2026-09-02 --start-time 14:00 --end-time 15:00
hey event add "Sarah's birthday" --starts-on 2026-09-02     # no time, so all day
hey event add "Standup" --start-time 09:15 --repeat every_weekday --remind 10m
hey event edit 4821 --title "Design review (moved)"
hey event edit 4821 --starts-on 2026-09-04 --start-time 15:00
hey event delete 4821
```

Without `--calendar`, `hey event list` reads every calendar and `hey event add` files on
the first one it can — the personal calendar is in the list HEY serves but refuses events.
The list follows every page HEY serves. Within each calendar HEY orders recordings by
newest start time, not creation time, so use the ID returned by `hey event add` for a follow-up
edit or delete rather than choosing an event by its position in the list. A repeating event
lists once as the series it is stored as, not once per day it falls on.

An event with no `--start-time` is an all-day event, and a `--start-time` with no
`--end-time` runs for an hour. Clock times are read in `--time-zone`, which defaults to the
machine's own zone; without one HEY would read them as UTC.

`hey event edit` changes only the flags you name, but that is this command's doing rather
than HEY's: an event write is a replacement, so the edit reads the event first and sends
back the notes, location, link, attached email, reminders and time zones it is not
changing. Two things cannot survive the round trip. HEY serves notes back as plain text, so
saving flattens their formatting; and a countdown is not served at all, so an edit removes
one unless `--countdown` names it again. An event that cannot be read is refused rather
than written blind — pass the day it starts (`hey event edit 4821 2026-09-02`) or
`--calendar` to look somewhere narrower.

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

`hey habit list` reads the week a date falls in, because habits are not in a calendar's
recordings listing — that carries only their completions. A week lists every habit exactly
once, whatever weekday each is scheduled for. Weekdays use `0` for Sunday through `6` for
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
than ignored: `--json`, `--quiet`, `--markdown`, `--ids-only`, `--count` and `--html` all
need `--output`, which returns file metadata they can format.

### Journal

```bash
hey journal list                   # list entries
hey journal list --starts-on 2026-01-01 --ends-on 2026-01-31
hey journal read                   # read today's entry (or pass YYYY-MM-DD)
hey journal write "..."            # write today's entry (or omit content for $EDITOR)
```

Saving an empty buffer in `$EDITOR` removes the day's entry, and `hey journal write` says
so rather than reporting a save. An empty day answers with an empty entry, so if the read
that pre-fills the editor fails for any other reason the command stops there instead of
opening a blank buffer over an entry it could not see.

## Omarchy

On [Omarchy](https://omarchy.org) the TUI follows the active theme with no setup: it lays
the theme's `accent`, `selection`, `muted`, `foreground` and `red` over its ANSI palette,
read from `~/.local/state/omarchy/current/theme/`, and restyles live when you run
`omarchy theme set`. Set `HEY_THEME=/path/to/file.toml` to use your own overlay anywhere
— an explicitly chosen file is trusted as written — or `NO_COLOR=1` to turn color off.

```bash
omarchy-mise-install github:basecamp/hey-cli hey
hey setup                    # sign in, connect detected agents, and put HEY in your bar
hey setup omarchy            # the explicit desktop-only path: installs in every output format, fails loudly
hey setup omarchy --notify   # toast new mail from the bar plugin (--no-notify turns it off)
hey setup omarchy --remove   # disable the bar plugin (checkout kept) and remove the desktop pieces
```

The bar gets the [HEY plugin](https://github.com/basecamp/omarchy-hey-plugin): the HEY
logo lights when the Imbox has unseen mail and opens a panel of recent threads. hey-cli is
its engine — the plugin reads the Imbox with `hey box view imbox` and runs `hey watch` so the
bar is live: a thread you archive in the TUI, on your phone or in the web app leaves the
panel within a second, and after a disconnect the watch catches up from where it left off.

The full `hey setup` wizard installs and enables the plugin automatically. Other interactive
sign-ins — `hey auth login` or any command's sign-in prompt — offer it once and remember
the answer. Those sign-in hooks never fire in scripts: machine output (`--json` and friends),
`HEY_NONINTERACTIVE`, a non-TTY, and `--token`/`--cookie` logins all skip it.
`hey setup omarchy` is the desktop-only path for scripts: it works in every output format,
verifies the running shell, and fails loudly when installation is incomplete. A plugin you
disable stays disabled until `hey setup` or `hey setup omarchy` enables it again; `--remove`
disables it and keeps the checkout.

Setup installs a `HEY TUI` launcher entry, a `HEY` row in the SUPER+SPACE menu, and a
`hey.toml.tpl` theme template so theme authors can tune the overlay. It prints the
`bindings.lua` snippet for a keybinding rather than editing your file. Omarchy's shipped
HEY web app, its SUPER+SHIFT+E binding and the mailto handler are left untouched.

`--notify` turns on new-mail toasts, off by default, by flipping the plugin's `notify`
setting (the panel's toggle and `omarchy bar set 37signals.hey notify true --json` do the
same). The plugin runs `hey watch` and toasts its new-mail lines for the Imbox itself — at
most one notification per burst of changes, `Sender — Subject` for one new thread, `N new
in Imbox` for more, replacing the previous toast rather than stacking, and clicking it
focuses the TUI. Omarchy's notification silencing (SUPER+CTRL+comma) mutes them like any
other app. See [docs/omarchy.md](docs/omarchy.md) for the details and what is planned next.

## AI agent integration

hey-cli ships with an embedded agent skill so your coding agent can work with HEY on your
behalf, and a Claude Code plugin (`hey@37signals` from the `basecamp/claude-plugins`
marketplace). The setup wizard connects every detected agent automatically; these commands
manage the integrations on their own:

```bash
hey setup claude    # install the skill and the hey@37signals plugin for Claude Code
hey setup codex     # install the skill for Codex
hey skill install   # install the skill only (~/.agents/skills/hey, linked for detected agents)
hey setup agents             # non-interactive: skill + a single detected agent (the installer uses this)
hey setup agents --remove    # remove HEY's managed skills and Claude Code plugin
hey doctor                   # check skill and plugin health per detected agent
```

`hey setup agents` never prompts and never guesses: with several agents detected it installs
the skill only and lists the `hey setup <agent>` choices. `HEY_SETUP_AGENT=claude|codex|all|none`
picks explicitly. `HEY_NONINTERACTIVE=1` disables interactive sign-in for harnesses that
run hey under a pseudo-terminal. The installed skill is refreshed automatically the first
time a new hey release runs.

hey only ever writes skill directories it owns: each one it creates carries a
`.managed-by-hey-cli` marker, and install, replacement and automatic refresh all refuse a
`hey` skill directory (or symlink) without it — a hand-authored skill at one of those paths
is never overwritten or claimed. `hey doctor` flags an unmanaged baseline and how to adopt it.

## Troubleshooting

```bash
hey doctor           # Check CLI health and diagnose issues
hey config show      # Current settings and where each one came from
hey config set base_url https://app.hey.com
hey version --json   # Installed version, commit, build date and build source
hey upgrade          # Upgrade to the latest release (see Upgrading)
hey commands --json  # Every command, subcommand and flag, for an agent to read
hey shell-completion install  # Install completions for your shell (bash, zsh, fish)
hey shell-completion generate zsh   # Print the script instead, to place yourself
```

### Shell completions

Installs from a package manager (AUR, deb, rpm, Homebrew, Scoop) already carry
completions system-wide. Every other install — mise, the installer script, a
downloaded tarball — registers them nowhere, so:

```bash
hey shell-completion install         # takes the shell from $SHELL
hey shell-completion install fish    # or name it
```

The file lands where your shell already looks: `~/.local/share/bash-completion/completions/hey`,
`~/.config/fish/completions/hey.fish`, and for zsh a directory on your `fpath` —
falling back to `~/.local/share/zsh/site-functions/_hey` and telling you the one
line that puts it there. `hey setup` installs them too, so a first run usually
settles this without being asked. `hey doctor` reports whether they are
installed, current, and reachable.

Re-running is a no-op once the file matches. Like the agent skills, hey only
writes completion files it wrote: each carries a `managed by hey-cli` line on
its second line, and a file without it is never overwritten — move it aside, or
pass `--force`.

Installed through **mise** (which is how [Omarchy](https://omarchy.org) installs
hey), the binary is reached through a shim that re-resolves the tool on every
call. The installed script asks the resolved binary directly, so pressing Tab
does not pay for that.

### Windows: Smart App Control and SmartScreen

To check whether your installed binary is signed:

```powershell
Get-AuthenticodeSignature (Get-Command hey).Source
```

**Smart App Control** (Windows 11) blocks unsigned executables no matter where
they were downloaded from, and it has no per-app exceptions — this applies to
the PowerShell installer, Scoop installs, and manual downloads alike. If it
blocks an unsigned `hey.exe`, two options:

1. **Use WSL2 (preferred).** Install the Linux build inside WSL2 — Smart App
   Control doesn't apply there and your Windows security setup is untouched:
   `wsl --install`, then inside the WSL terminal:
   `curl -fsSL https://hey.com/install-cli | bash`
2. **Turn Smart App Control off** (Windows Security → App & browser control →
   Smart App Control settings) **and leave it off while using the unsigned
   build.** Because there are no per-app exceptions, turning it back on
   re-blocks `hey.exe` on its next run — only re-enable after upgrading to a
   signed build. Windows 11 with the March/April 2026 updates can re-enable
   Smart App Control from Windows Security without a reset; on older builds
   re-enabling requires resetting Windows, so prefer WSL2 there.

**SmartScreen** (without Smart App Control) may warn on first run of an
unrecognized executable — choose "More info" → "Run anyway" if you downloaded
the release from this repository.

## Development

```bash
make build      # build binary
make test       # run tests
make coverage   # run cross-package coverage and enforce the 70.8% floor
make lint       # run golangci-lint
make clean      # remove build artifacts
```

`make coverage` writes `coverage.out`, `coverage.func.txt`, and `coverage.packages.txt`, then prints a concise package summary and the lowest-covered functions.

## License

This project is licensed under the MIT License. See [LICENSE.md](LICENSE.md) for details.
