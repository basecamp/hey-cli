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

**macOS / Linux / WSL2**

```bash
curl -fsSL https://hey.com/install-cli | bash
```

**Windows (PowerShell)**

```powershell
irm https://hey.com/install-cli.ps1 | iex
```

On Windows 11 with Smart App Control, see [Troubleshooting](#windows-smart-app-control-and-smartscreen) if the install is blocked.

Both scripts download the release for your platform, verify its SHA-256 checksum, and — when `cosign` is installed — verify the release's keyless Sigstore signature (cosign v3 as-is, v2.6+ with `--new-bundle-format=true`; older versions skip signature verification with a warning). Set `HEY_VERSION` to pin a release and `HEY_BIN_DIR` to choose the install directory.

<details>
<summary>Other installation methods</summary>

**Homebrew (macOS / Linux):**
```bash
brew install --cask basecamp/tap/hey
```

**Arch Linux / Omarchy (AUR):**
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

## Upgrading

```bash
hey upgrade
hey upgrade 0.2.0-rc.1   # target a specific release, e.g. a prerelease
```

Upgrading only ever moves forward: a requested version at or below the installed one is a
no-op, and package-manager installs always follow their manager's own version (a pinned
version is refused there).

What happens depends on how hey was installed:

- **Installer script / tarball** (a binary under your home directory, e.g. `~/.local/bin` or `~/bin`): upgrades in place. hey downloads the release for your platform, verifies its Sigstore signature (the keyless `checksums.txt.bundle` published by the release pipeline, identity-pinned to the release workflow and tag) and SHA-256 checksum, swaps the executable transactionally, and confirms the installed binary reports the new version. On failure the previous binary is restored; in the worst case — restoration itself fails mid-swap — the error names the preserved backup file next to the binary so you can put it back by hand.
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

### Linked accounts

One HEY login exposes every mail account linked to that identity. List the available
filters, persist a default, or select one for a single invocation:

```bash
hey accounts list                 # list All Accounts and each linked account
hey accounts use 12345            # persist a linked account as the default mail filter
hey accounts use all              # return to All Accounts
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

Run `hey tui` to launch the interactive terminal UI. Bare `hey` prints the help. For
identities with multiple linked mail accounts, press Ctrl+A to switch between All Accounts
and individual email addresses.
Switching cancels requests from the previous account and reloads the active section;
Calendar and Journal remain identity-wide.

Navigate between Mail, Contacts, Calendar, and Journal. Mail navigation includes HEY boxes plus separate Labels and Collections tabs. Press Shift+L or Shift+K to choose one. Every list keeps going: scroll towards the bottom of a box, label, or collection and the next threads are read in behind you, so there are no pages to step through. Use `/` to search, Enter to open a thread, `r` to reply, `f` to forward, `m` to move, `g` to manage labels, `k` to add or remove the selected thread from collections, `t` to trash, `s` to mark as spam, `-` to ignore, and `+` to stop ignoring. Select threads with Space and press `b` to preview every bulk-reply recipient before writing and sending one reply to all selected threads. A delayed bulk reply can be recalled with `u` while HEY's undo window remains open. Search results retain the matching-message summary and keep going as you scroll, like every other list.

The mail list follows the server. HEY tells the TUI when a box changed over the same
Action Cable connection `hey watch` uses, and the box on screen is read again a moment
later, keeping your place in the list and anything you had selected. A change that arrives
while a form or a picker is open waits for it to close. Press Ctrl+R to read the box again
yourself; if the connection goes away for good, the list says so and Ctrl+R is how you
catch up.

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
`v` to peek, `v` again to close it.

Set `HEY_COVER` to `blobs`, `grid`, `peace`, `terrazzo`, `topo` or `waves` — the same six
covers, redrawn as characters, so they work in any terminal rather than only the ones that
can show images. HEY does not serve a box's cover over its API yet, so for now the choice
is `HEY_COVER` rather than the one you made on the web.

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
hey boxes --jq '.data[] | {id, name}'
hey boxes --quiet --jq '.[].id'
```

### Email

```bash
hey boxes                          # list mailboxes
hey box imbox                      # list email threads in a box (by name or ID)
hey labels                          # list labels and their IDs
hey label 789 --all                 # list all email threads with a label
hey label add 12345 --to 789        # add a label to a thread
hey label create "Travel receipts" 12345  # create and add a label
hey label remove 12345 --from 789   # remove one label
hey label remove 12345 --from all   # remove every label
hey collections                     # list collections and their IDs
hey collection 321 --all            # list every thread in a collection
hey collection create "Kitchen remodel" --summary "Plans and decisions"
hey collection update 321 --name "Kitchen renovation"
hey collection add 987 --to 321      # add a topic ID to a collection
hey collection remove 987 --from 321 # remove a topic ID from a collection
hey search "quarterly planning"    # search threads and matching messages
hey search --from jane@example.com --date last_30_days  # refine a search
hey search filters                 # list available refinement values
hey contacts list                  # list contacts
hey contacts show 12345            # view a contact and private note
hey contacts add --name "Jane Doe" --email jane@example.com
hey contacts update 12345 --name "Jane Dawson"
hey contacts hide 12345            # hide without permanently deleting
hey contacts show-again 12345      # show a hidden contact again
hey contacts bundle 12345          # group this contact's mail into one row
hey contacts unbundle 12345        # list this contact's mail separately
hey contacts note set 12345 "Prefers email"
hey contacts note delete 12345
hey screener list                  # who is waiting to be screened
hey screener list --count          # just the number waiting
hey screener approve 91            # let a sender through
hey screener approve 91 --box "The Feed"  # let them through, into another box
hey screener deny 91 92            # turn several senders away
hey screener deny 91 --spam        # turn them away and mark what they sent as spam
hey screener history               # who has already been screened
hey screener clear                 # empty the queue without deciding
hey threads 123                    # read a full email thread
hey share 123                      # get a sharing link for a thread
hey unshare 123                    # turn off the sharing link
hey attachments 123                # list files attached to the thread
hey attachments save 456:1         # save a file using its attachment ID
hey reply 123 -m "Thanks!"        # reply to a thread (or omit -m to open $EDITOR)
hey reply 123 -m "Attached." --attach ./diagram.png
hey bulk-reply preview 12345 67890  # inspect threads and exact To/CC/BCC recipients
hey bulk-reply send 12345 67890 -m "Thanks for the update."
hey bulk-reply undo 98765            # recall a delayed bulk reply
hey forward 123 --to alice@example.com -m "For your review"  # forward the latest message
hey compose --to user@example.com --subject "Hello"  # compose a new message
hey compose --to user@example.com --subject "Report" -m "Attached." --attach ./report.pdf
hey compose --to user@example.com --cc bob@example.com --bcc carol@example.org --subject "Hello"  # with CC/BCC
hey drafts                         # list drafts
hey move 12345 --to feed           # move a thread to another box
hey move 12345 67890 --to "paper trail"  # move multiple threads
hey trash 12345                    # move a thread to Trash
hey spam 12345                     # mark a thread as spam
hey ignore 12345                   # ignore future activity on a thread
hey stop-ignoring 12345            # resume attention for a thread
```

Email bodies come back as Markdown. `hey threads` and the TUI render that Markdown for the terminal — headings, emphasis, lists, quotes, tables and code survive, and links keep their URLs and stay clickable where the terminal supports it. `--json` carries the same Markdown in `body`, so an agent reading a thread sees the structure a human sees rather than a flattened wall of text. `--html` still returns HEY's original HTML.

`hey share <thread_id>` gets a sharing link for a thread. Anyone with the link can see the entire thread and future emails or replies sent to it. `hey unshare <thread_id>` turns off the sharing link.

Search accepts free text plus `--required`, `--any`, `--none`, `--exact`, `--from`, `--to`, `--subject`, `--date`, `--in`, `--label`, and `--attachment`. Use `--page` for one page or `--all` to fetch up to 100 pages; capped searches report the next page for continuation. Search results include `topic_id` for reading the thread and the matching message summaries. Results with an active box item also include `id` for organization actions.

Contact updates preserve omitted name, email, and alias fields. Supplying `--alias` replaces the complete alias list; `--alias=` clears it. Contact notes accept positional content, `--note`, stdin, or `$EDITOR`. HEY hides contacts rather than permanently deleting them; hidden contacts leave lists, autocomplete, and search, and can be shown again by ID. Bundling groups a contact's mail into one row without merging or deleting the underlying threads; unbundling lists those threads separately again. HEY applies bundling when the contact's current delivery setting supports bundles.

The Screener is where first-time senders wait. `hey screener list` returns clearance IDs — not contact IDs — with the sender and the subject of what they sent, plus `topic_id` for reading the thread before deciding. `--count` asks for the number alone, which is a far cheaper request than the queue. Approving delivers everything the sender has waiting; denying hides it. Either is reversible with the opposite command, and `hey screener history` shows what was already decided. `--box` and `--seen` approve one sender at a time; several IDs go through HEY's bulk endpoint, which takes neither. `--spam` also trains HEY's filter, which is harder to undo than denying. `hey screener clear` empties the queue without deciding anything — those senders reappear on their next email.

`hey bulk-reply preview` is read-only and resolves each posting to its latest replyable entry. `hey bulk-reply send` resolves the selection again, skips threads without a replyable entry, keeps HEY's server-provided name tag, and returns the exact reply count, delivery ID, delayed state, undo URL, and undo command. Posting IDs must be positive and unique. The message can come from `-m`, stdin, or `$EDITOR`; `--attach` is repeatable.

`--attach` is repeatable on `hey compose`, `hey reply`, and `hey bulk-reply send`, and attachment-only messages are supported. The CLI validates and uploads every file before sending the email. `hey attachments <topic_id>` returns stable message-and-position IDs such as `456:1`; pass an ID to `hey attachments save`. Saving uses the original filename by default, accepts `--output` for a file or directory, and preserves existing files unless `--force` is set.

Organization actions take the `id` values returned by `hey box --json`, `hey label --json`, or `hey search --json`. Label IDs come from `hey labels`; `hey label` returns `next_page` and `total_count`, accepts `--page <next_page>` for continuation, and supports `--all` for complete traversal. HEY creates a label while adding it to at least one thread, so `hey label create` requires thread item IDs.

Collection IDs come from `hey collections`. `hey collection` returns both each posting `id` and its `topic_id`, plus `next_page` and `total_count`. Collection membership commands take `topic_id`; posting organization commands continue to take `id`. Creating a collection returns a confirmed mutation, and `hey collections` provides its ID for subsequent commands. Collection updates accept a non-empty name, summary, or both.

Move destinations are Imbox, The Feed, Set Aside, Reply Later, or Paper Trail. Bubble Up requires a scheduled date and is not available through `hey move`. Trashing a shared thread removes your access instead of deleting it for everyone. Ignored threads remain in their box and can be restored with `hey stop-ignoring`.

### Watching for changes

```bash
hey watch                               # follow every box, a line of JSON per change
hey watch --box imbox --events added    # only new postings in the Imbox
hey watch --box imbox --exit-on-first   # block until something lands, then exit
hey watch --since 2026-08-18T09:00:00Z  # catch up from a time first, then follow
hey watch --run-async 'notify-send "New mail in $HEY_BOX_KIND"'
hey watch --run-sync ./triage.sh        # one at a time, waiting for each
```

Runs until interrupted, printing changes as they happen, one line each:

```json
{"change":"added","at":"2026-08-18T09:14:22.031Z","box":{"id":24088,"kind":"imbox","name":"Imbox"},"posting_id":98765,"thread_id":54321,"posting":{}}
```

A change can drive a command instead of being printed, and there's a choice to make
between two behaviours — pass one or the other, not both. `--run-async` spawns the
command per change and moves on, so a slow one never holds up the watch and two can
overlap. `--run-sync` waits for each and runs them in order, so they never overlap and a
slow one delays the next.

Both hand the JSON to the command on its stdin, and the same fields as `HEY_CHANGE`,
`HEY_AT`, `HEY_BOX_ID`, `HEY_BOX_KIND`, `HEY_BOX_NAME`, `HEY_POSTING_ID` and
`HEY_THREAD_ID`. Both also take over stdout, so the JSON isn't printed as well.

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
hey habit create "Morning strength training"  # create every day with weights and blue defaults
hey habit create "Practice piano" --icon music --color green --days mon,wed,fri
hey habit edit 1 --name "Evening strength training"  # edit only the supplied fields
hey habit edit 1 --days 0,6         # Sunday and Saturday (names also work)
hey habit delete 1                  # permanently delete the habit and its history
hey habit complete 1                # mark habit done (today or --date YYYY-MM-DD)
hey habit uncomplete 1              # undo habit completion
```

Habit IDs come from calendar recordings. Weekdays use `0` for Sunday through `6` for Saturday; full names and common abbreviations are accepted too.

### Time tracking

```bash
hey timetrack start                # start tracking
hey timetrack stop                 # stop tracking
hey timetrack current              # show active track
hey timetrack list                 # list all tracks
hey timetrack export > tracked-time.csv
hey timetrack export --output tracked-time.csv
hey timetrack categories           # list categories
hey timetrack category create "Client work"
hey timetrack category rename 123 "Planning"
hey timetrack category delete 123
```

The time tracking export contains every completed entry, newest first, with Start, End, Duration, Category, and Notes columns. Ongoing time tracking is excluded. `--output` preserves an existing file unless `--force` is set.

### Journal

```bash
hey journal list                   # list entries
hey journal read                   # read today's entry (or pass YYYY-MM-DD)
hey journal write "..."            # write today's entry (or omit content for $EDITOR)
```

## Omarchy

On [Omarchy](https://omarchy.org) the TUI follows the active theme with no setup: it lays
the theme's `accent`, `selection`, `muted`, `foreground` and `red` over its ANSI palette,
read from `~/.local/state/omarchy/current/theme/`, and restyles live when you run
`omarchy theme set`. Set `HEY_THEME=/path/to/file.toml` to use your own overlay anywhere
— an explicitly chosen file is trusted as written — or `NO_COLOR=1` to turn color off.

```bash
yay -S hey-cli               # hey-cli is on the AUR
hey setup omarchy            # install into the desktop
hey setup omarchy --notify   # also toast new Imbox mail (--no-notify turns it off)
hey setup omarchy --remove   # take it all out again
```

Setup installs a `HEY TUI` launcher entry, a `HEY` row in the SUPER+SPACE menu, a bar
indicator that lights when the Imbox has unread mail (no count, by design), and a
`hey.toml.tpl` theme template so theme authors can tune the overlay. It prints the
`bindings.lua` snippet for a keybinding rather than editing your file. Omarchy's shipped
HEY web app, its SUPER+SHIFT+E binding and the mailto handler are left untouched.

`--notify` turns on new-mail toasts, off by default: the bar indicator's poll also sends
at most one notification per interval — `Sender — Subject` for one new thread, `N new in
Imbox` for more — replacing the previous toast rather than stacking, and clicking it
focuses the TUI. Omarchy's notification silencing (SUPER+CTRL+comma) mutes them like any
other app. See [docs/omarchy.md](docs/omarchy.md) for the details and what is planned next.

## Agent Skill

hey-cli ships with an embedded agent skill so your agent can interact with HEY on your behalf.

```bash
hey skill install   # install the skill globally for your agent
```

## Troubleshooting

```bash
hey doctor           # Check CLI health and diagnose issues
hey version --json   # Installed version, commit, build date and build source
hey upgrade          # Upgrade to the latest release (see Upgrading)
```

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
