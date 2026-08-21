# hey-cli on Omarchy

hey-cli should feel like an installed app on [Omarchy](https://omarchy.org), the way btop
and lazydocker do, rather than a command you remember to type. This page records what
the integration does, the decisions behind it, and the landscape that was mapped but
deliberately left for later.

## What ships

### Live theming (zero setup)

The TUI styles with ANSI-16 colors, so Omarchy's terminal retint on every theme switch
already restyles a running `hey` for free. On top of that the TUI lays an **accent
overlay** read from the active theme:

| Source, first match wins | Keys read |
|---|---|
| `NO_COLOR` | disables color entirely |
| `HEY_THEME=/path/to/file.toml` | any of the keys below |
| `~/.local/state/omarchy/current/theme/hey.toml` | `mode`, `accent`, `selection`, `muted`, `foreground`, `error`, plus the gate's reference keys below |
| `~/.local/state/omarchy/current/theme/colors.toml` | `mode`, `accent`, `selection`, `muted`, `foreground`, `red`; `bright_foreground` wins over `foreground` for emphasis, and `background`, `blue`, `bright_blue` feed the accent readability gate |
| ANSI defaults | — |

Only the keys a file provides override the defaults, and two of them are gated, because
a theme's `accent` is a UI tint that does not always work as a text highlight:

- **accent** is used when it is visibly distinct from the emphasis text (kanagawa's accent
  *is* its foreground) and at least as readable on the background as the theme's bright
  blue (osaka-jade's jade is dimmer than its mint). Otherwise the cursor row keeps bright
  blue — the theme's own, which is what the terminal's ANSI 12 already is.
- **selection** tints the cursor row only when the chosen accent reads on it at ≥ 4.5:1;
  rose-pine and miasma get the accent row with no tint rather than mud.

When the accent is rejected, the cursor row falls back to plain ANSI bright blue rather
than the theme's hex, so it always matches the palette the surrounding text renders in.
That distinction matters in foot: a running foot window keeps the ANSI palette it opened
with until a new window is opened, so after a theme switch the ANSI-16 body of the TUI
shows the old theme while the overlay colors are new. Judge a theme in a fresh window.

The measurements behind both thresholds are in the PR that introduced them. A theme (or
user) that disagrees can override any of it: `~/.config/omarchy/themes/<theme>/hey.toml`
overlays the official theme — e.g. osaka-jade's all-green palette gains a real highlight
with `accent = "#F7E8B2"`, its own selection-foreground cream. Overridden accent and
selection values still pass the same readability gates, because once Omarchy renders the
theme the TUI cannot tell a hand-written value from a machine-derived one. The escape
hatch is `HEY_THEME`: a file the user points at explicitly is trusted as written and
skips both gates.

The TUI watches `~/.local/state/omarchy/current/` and restyles the frame after
`omarchy theme set`. The watch sits on the parent because `omarchy-theme-set` swaps the
whole `theme/` directory with an atomic `mv` — a watch inside it would die with the old
inode. Cached viewports (thread, calendar grid, contact detail, bulk-reply preview) are
re-rendered rather than recolored, because Kitty inline-image placeholders encode their
image IDs as foreground colors.

When no theme file states a `mode`, the TUI asks the terminal for its background color
and picks black instead of bright white for emphasized text on light backgrounds.

One-shot CLI output (`hey box`, tables) keeps inheriting the terminal palette and is
not themed — that is the point of ANSI.

### `hey setup omarchy`

Idempotent; `--remove` reverses every piece; each step is reported separately and one
failing step does not stop the others.

| Piece | Where | Notes |
|---|---|---|
| Desktop entry | `~/.local/share/applications/HEY TUI.desktop` | Distinct from Omarchy's shipped `HEY.desktop` web app. Launches under app-id `org.omarchy.hey` |
| Menu row | marker block in `~/.config/omarchy/extensions/omarchy-menu.jsonc` | one root `HEY` row that focuses or launches the TUI; its guard is a PATH lookup, never network or `hey` itself. Becomes a submenu once there is more than one thing to open |
| Bar plugin | the `37signals.hey` entry in `~/.config/omarchy/shell.json`'s bar layout | not installed by setup — `omarchy plugin add https://github.com/basecamp/omarchy-hey-plugin.git --enable` does that — but configured by it: `--notify` / `--no-notify` set or delete the entry's `notify` key, which the shell hot-reloads and the plugin passes to `hey omarchy poll`. An earlier inline `hey-unread` module is removed on sight, its notify choice carried over |
| Theme template | `~/.config/omarchy/themed/hey.toml.tpl` | renders `hey.toml` into every theme so theme authors can override the overlay; triggers `omarchy-theme-refresh` |
| Keybinding | printed, never written | `o.bind("SUPER + SHIFT + ALT + H", "HEY TUI", "omarchy-launch-or-focus-tui --app-id=org.omarchy.hey hey tui")`; SUPER+SHIFT+E keeps opening the web app unless you `hl.unbind` it. Spelled out rather than `{ tui = "hey tui" }` because the lua helper quotes that into one word and the app-id derived from it would never match |

Every surface — launcher, menu, bar click, keybinding — uses the same app-id
(`org.omarchy.hey`) so they all focus one window. That is why the desktop entry is tiled
rather than `TUI.float`: the float class is shared by every floating TUI, and
focus-or-launch would grab whichever one was open.

Setup never adds a bar module of its own any more, so it never has to seed a layout. The
one place it still reads Omarchy's default layout is the legacy removal: the old install
copied the defaults in just to hold its module, and if removing the module leaves exactly
the current defaults, the layout goes too and the user is back to inheriting them. A
`shell.json` that is not plain JSON is left alone and the step reports failure.

### `hey omarchy poll`

The engine under the bar plugin, and the one command the plugin depends on for the Imbox.
`hey omarchy poll --limit N --json` answers with the same `data` `hey box imbox --json`
does — `{"ok":true,"data":{...box, "postings":[...]}}`, the box and its postings newest
first, byte-identical for the same Imbox, cut to `--limit` with `next_history_url` cleared
when the cut dropped postings — so the plugin's parser did not change. The envelope
around it is the poll's own: a summary, a truncation notice that says to raise `--limit`
rather than pass `--all`, and none of `hey box`'s breadcrumbs. Errors are errors: logged out is the `auth` envelope the plugin turns into its
sign-in button, a global config that cannot be loaded is `config_error`, and a later page
that cannot be fetched fails the poll rather than handing the panel a short list to replace
its last complete one. The one thing that never fails the command is the toast.

`--notify` makes the same read diff the unseen Imbox postings against a fingerprint file
(`~/.local/state/hey-cli/omarchy-poll.json`) and send **at most one toast per poll** via
`omarchy-notification-send` — sparse notices, never a per-message firehose. One Imbox fetch
serves the panel, the icon and the toasts.

- **What counts as new**: an unseen posting not fingerprinted yet, or one whose
  `visible_entry_count` grew (a new reply on a known thread). Fingerprints avoid
  `updated_at` (it churns) and `seen` (it flips on read). Muted threads are fingerprinted
  but never toast.
- **First run seeds silently.** No state file means write the fingerprints and toast
  nothing — never toast the backlog. The fingerprints carry the identity they were taken
  for — server, account filter and the signed-in user's id — so after
  `hey accounts use`, a base URL change, or signing in as someone else by any route
  (login, logout, `HEY_TOKEN`) the next poll reseeds silently instead of toasting the
  other identity's backlog.
- **A poll without `--notify` forgets the seed.** Toasts can be turned off by any route —
  `hey setup omarchy --no-notify`, the plugin's own toggle, `omarchy bar set` — and every
  one of them means the plugin starts polling without the flag. Those polls delete the
  fingerprint file, so turning toasts back on always starts from a silent seed, whichever
  route turned them on. (`hey setup omarchy --notify` drops the file too, belt and braces.)
- **The whole unseen set is read when seeding.** HEY sorts Imbox postings unseen-first,
  so the poll follows pages while they are all-unseen and stops at the first seen
  posting. A seed (first run, or a new identity) reads them all, so no pre-existing
  thread can later surface as new; a steady-state poll stops at ten pages, because new
  mail always lands on page 1 and older threads are already fingerprinted. Pages read
  for the panel's `--limit` and pages read for the toasts are the same pages; whichever
  consumer wants more decides. Fingerprints prune to the postings still unseen once the
  snapshot is complete; a truncated snapshot (cap reached, a page fetch failed) keeps
  absent fingerprints instead.
- **One toast, replaced not stacked.** `Sender — Subject` for one new thread, `N new in
  Imbox` with the first few senders for more. The daemon's printed id (`-r <id> -p`, the
  `omarchy-display-text-size` pattern) is cached so the next poll replaces the on-screen
  toast instead of stacking; a stale id after a shell restart just makes a fresh toast.
- **One toast across monitors.** The shell builds its bar once per monitor, so a
  two-monitor desktop runs two plugin instances and two concurrent polls. The diff and the
  send happen under a `flock` on the sidecar `omarchy-poll.json.lock` next to the state
  file, so the second poll reads the fingerprints the first one just wrote and finds
  nothing new. The sidecar is never unlinked while polls run (only `--remove` takes it
  out) — a fresh inode would be a fresh lock.
- **DND is honored.** The toast passes `--app-name HEY` deliberately: omarchy's default
  app-name `omarchy-action` bypasses notification silencing, so identifying as HEY is
  what makes SUPER+CTRL+comma mute the toasts (into history) like any other app.
- **Clicking focuses the TUI** via the shared `omarchy-launch-or-focus-tui` exec hint,
  which the shell runs itself so it survives shell restarts. The plugin's panel is one
  click away on the bar; the toast takes you to the thread's home.
- **A failed send is silent and retried**: it produces no error beyond the envelope the
  panel already got, and the undelivered postings keep their previous fingerprints so
  the toast comes back on the next poll.

### The bar plugin

[`basecamp/omarchy-hey-plugin`](https://github.com/basecamp/omarchy-hey-plugin) is the
face: the HEY logo in the bar (tinted when there is unseen mail), the panel with its
account switcher, `New for you` / `Previously seen` tabs, the Screener count, mark-as-seen,
and the setup flow that installs `hey-cli` from the AUR and signs you in. hey-cli is the
engine: `hey omarchy poll` for the Imbox and the toasts, `hey screener list --count` for
the Screener, `hey seen` for marking, `hey accounts list` and `hey auth status` for the
rest. The division was settled when both sides turned out to have built a bar indicator
without knowing of the other — two HEY icons, two Imbox pollers, two product stances in
one slot — and the answer was: **the CLI is the engine, the plugin is the face; one Imbox
fetch serves the panel, the icon and the toasts.**

Settings flow the way every Omarchy bar widget's do: the plugin's manifest declares
`notify` (default off) alongside its refresh interval and thread limit, and the values
live as extra keys on the `{"id":"37signals.hey"}` entry in `shell.json`, hot-reloaded by
the shell. Three ways to flip a key, all equivalent: `hey setup omarchy --notify`, the
toggle in the panel header, or `omarchy bar set 37signals.hey notify true --json`.

Freshness is pushed, not polled: after every posting mutation in `hey tui` — seen, trash,
spam, mute, every box move — and after every Screener decision, the TUI runs
`omarchy-shell -q 37signals.hey refresh` (3 s timeout, output discarded, a no-op off
Omarchy), so the icon goes dark the moment you archive the last unread thread instead of
at the next ten-minute poll. The plugin broadcasts that refresh to every per-monitor
instance and coalesces one that arrives mid-fetch into a follow-up, so a burst of
mutations costs a handful of IPC calls, not a fetch storm, and none of them is lost. CLI
mutations (`hey seen`, `hey move`) do not push yet; agents driving the CLI are a follow-up.

## Decisions

- **Indicator, not count.** Pending screener mail is not what people mean by "important",
  and a number is the attention treadmill HEY exists to end. The glyph lights or it does
  not.
- **Accent overlay, not a full hex port.** Replacing the ANSI palette wholesale would
  trade away the free adaptation terminals already provide, and basecamp-cli's full port
  is the cautionary precedent.
- **Complement the shipped web app, never replace it.** Distinct desktop name, printed
  keybinding, the mailto handler left alone.
- **No HTML scraping to feed widgets.** The poll uses the same typed SDK read as
  `hey box imbox`.
- **CLI is the engine, plugin is the face.** The plugin owns rendering and settings; the
  CLI owns what needs HEY's semantics — pagination, what counts as new, a toast that
  honors DND — where it is Go-tested once instead of re-derived in QML. The plugin's panel
  does show an unread *number* per account; the bar icon itself stays a glyph that lights
  or does not, and the toast never carries the unread total either — `N new in Imbox` is
  how many threads arrived since the last poll, not how many are waiting. That divergence
  is the plugin's call and is recorded here rather than papered over.

## Follow-ups, in rough order

1. **mailto: handler** that opens a floating compose (`hey compose --mailto`), opt-in
   against the incumbent `omarchy-webapp-handler-hey`.
2. **Agent-triage digests**: `hey --json` feeding a system agent that emits sparse
   toasts instead of per-message noise.
3. **Upstream contributions**: a `default/themed/hey.toml.tpl` PR alongside
   `claude.json.tpl`; an Install-menu TUI row; possibly branching the mailto handler to
   the TUI when installed. (An AUR package already ships: `yay -S hey-cli`, published by
   the release workflow.)
4. **Plugin singletons**: the shell builds a bar per monitor, so a two-monitor desktop
   polls twice. The toasts are safe (one `flock`), the refresh is broadcast, but a single
   polling leader per shell would halve the API load; that is plugin architecture and
   lives in `omarchy-hey-plugin`.
5. **Push from the CLI too**: `hey seen` and `hey move` could nudge the plugin the way
   the TUI does, so an agent archiving mail keeps the bar honest.

## Anti-features, recorded

- No unread **count** in the bar icon or the toast.
- No per-message notification firehose.
- No full hex theme port.
- No auto-editing `~/.config/hypr/bindings.lua`.
- No HTML scraping to feed widgets.
