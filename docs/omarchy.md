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
| Bar plugin | the `37signals.hey` entry in `~/.config/omarchy/shell.json`'s bar layout | not installed by setup — `omarchy plugin add https://github.com/basecamp/omarchy-hey-plugin.git --enable` does that — but configured by it: `--notify` / `--no-notify` set or delete the entry's `notify` key, which the shell hot-reloads and the plugin passes to `hey watch`. An earlier inline `hey-unread` module is removed on sight, its notify choice carried over |
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

### The bar plugin

[`basecamp/omarchy-hey-plugin`](https://github.com/basecamp/omarchy-hey-plugin) is the
face: the HEY logo in the bar (tinted when there is unseen mail), the panel with its
account switcher, `New for you` / `Previously seen` tabs, the Screener count, mark-as-seen,
and the setup flow that installs `hey-cli` from the AUR and signs you in. hey-cli is the
engine, and the plugin composes its generic commands: `hey box imbox --json` for the Imbox,
`hey watch` to know when to read it again, `hey screener list --count` for the Screener,
`hey seen` for marking, `hey accounts list` and `hey auth status` for the rest. The
division was settled when both sides turned out to have built a bar indicator without
knowing of the other — two HEY icons, two Imbox pollers, two product stances in one slot —
and the answer was: **the CLI is the engine, the plugin is the face.**

The plugin is an Omarchy `service` plugin as well as a bar widget. The shell instantiates
the service once, so one `hey watch` runs per shell however many monitors carry the bar,
and every bar widget reads the shared service. A watch event is a wake-up, not a delta —
any line on the watch's stdout re-reads `hey box imbox`, debounced so a burst of changes
costs one read (plus one follow-up when changes land while a read is in flight, since that
read may predate them). `hey watch` says `ready` once its cursors are set and its
subscription is live, and again after every reconnect's catch-up; the plugin's read on that
line is what makes the picture gap-free — anything before the cursor is in the read,
anything after it is an event — rather than the order the two processes were started in.
It says `disconnected` when the cable drops, which is what the panel's live state follows,
and `resync` when a box changed more than the feed can list, which is just another reason
to re-read. `hey watch` catches up from its cursor on reconnect and skips ahead on that 409,
so a laptop back from suspend is current within seconds; the plugin's timer poll stays only
as a safety net.

The watch covers every box, not just the Imbox. A move out of the Imbox writes nothing in
the Imbox's own feed — it is an upsert in the box the thread went to — so an Imbox-only
watch would never see a thread leave. And the TUI's mutations come back over the cable like
anyone else's, within a second, which is why the TUI does not nudge the plugin over IPC.

When the watch exits with the `auth` error envelope the service shows the sign-in state and
stops restarting it until the next probe succeeds; any other exit restarts it on a backoff.

Settings flow the way every Omarchy bar widget's do: the plugin's manifest declares
`notify` (default off) alongside its refresh interval and thread limit, and the values
live as extra keys on the `{"id":"37signals.hey"}` entry in `shell.json`, hot-reloaded by
the shell. Three ways to flip a key, all equivalent: `hey setup omarchy --notify`, the
toggle in the panel header, or `omarchy bar set 37signals.hey notify true --json`. Flipping
`notify` restarts the watch with or without `--notify`; no shell restart.

### `hey watch --notify`

The toasts are `hey watch`'s: a generic flag on a generic command, Omarchy-aware through
notification hints. One read of a box's changes feed is one batch, and a batch sends **at
most one toast**.

- **The Imbox, unless told otherwise.** The plugin watches every box (it has to, to see a
  thread leave the Imbox), but HEY's attention model puts new mail in one place; `--notify`
  toasts the Imbox and `--notify-box` names another watched box, or several. A name that is
  not being watched is a usage error, not a toast nobody will see.
- **What counts as new**: a posting that is unseen, not muted, and whose `active_at` is
  later than the watch last recorded for it — or later than the watch's start, when it has
  no record. `active_at` moves on new mail only, not on a seen flip, a mute or a move, so
  reading a thread, marking it unseen again or moving it into the box never toasts, and a
  reply on a known thread does. A box's first read is its catch-up from the server's cursor
  — the box's last activity, not this moment — so it carries backlog, which the start-time
  rule keeps quiet, alongside anything that arrived while the watch was starting, which it
  toasts. The notifier records every posting the watch reads, in every box and whatever
  `--events` reports, so a thread known from a filtered-out or un-notified change is never
  mistaken for new when its next change is reported.
- **One toast, replaced not stacked.** `Sender — Subject` for one new thread, `N new in
  <box>` with the first few senders for more — `N new in Imbox` for the plugin. The
  daemon's printed id (`-p`) is kept in memory and passed back as `-r` for ten minutes, so
  the next batch replaces the toast on screen instead of stacking; after that a fresh
  toast, since ids are daemon-local and a shell restart may have handed the number to
  another application.
- **No state file.** What the notifier remembers — each thread's last activity, the last
  toast's id — lives and dies with the watch. No fingerprints, no lock, no reseeding when
  toasts are switched back on: a watch that starts is silent by construction.
- **DND is honoured.** The toast is `notify-send -a HEY -u low`: Omarchy's default app-name
  `omarchy-action` bypasses notification silencing, so identifying as HEY is what makes
  SUPER+CTRL+comma mute the toasts (into history) like any other app.
- **Omarchy hints, when Omarchy is there.** With `omarchy-launch-or-focus-tui` on PATH the
  toast carries `omarchy-glyph` (the bar's envelope) and `omarchy-exec` (the focus command)
  as hints the shell's notification daemon understands — the exec runs daemon-side, so it
  survives shell restarts — and every other daemon ignores. They are gated on detection
  so the argv is honest elsewhere.
- **It never fails the watch.** No `notify-send`: one notice on stderr and the watch runs
  without toasts. A failed send: a warning, and the next batch toasts again.
- **It composes.** `--box`, `--events`, `--exit-on-first` and `--run-*` all apply; the
  toast covers exactly what the watch reported, while the notifier's memory covers
  everything it read.

## Decisions

- **Indicator, not count.** Pending screener mail is not what people mean by "important",
  and a number is the attention treadmill HEY exists to end. The glyph lights or it does
  not.
- **Accent overlay, not a full hex port.** Replacing the ANSI palette wholesale would
  trade away the free adaptation terminals already provide, and basecamp-cli's full port
  is the cautionary precedent.
- **Complement the shipped web app, never replace it.** Distinct desktop name, printed
  keybinding, the mailto handler left alone.
- **No HTML scraping to feed widgets.** The plugin reads with `hey box imbox`, the same
  typed SDK read as everyone else.
- **CLI is the engine, plugin is the face.** The plugin owns rendering and settings; the
  CLI owns what needs HEY's semantics — the changes cursor and catching up after a
  disconnect, what counts as new, a toast that honors DND — where it is Go-tested once
  instead of re-derived in QML. The plugin's panel does show an unread *number* per
  account; the bar icon itself stays a glyph that lights or does not, and the toast never
  carries the unread total either — `N new in Imbox` is how many threads arrived in one
  batch, not how many are waiting. That divergence is the plugin's call and is recorded
  here rather than papered over.
- **Generic commands, Omarchy hints.** There is no `hey omarchy` group: the plugin
  composes `hey box`, `hey watch`, `hey screener` and `hey seen`, and a user can run any
  of them by hand. Omarchy-specific behaviour is carried as notification hints the shell
  understands and other daemons ignore, never as a subcommand. (A `hey omarchy poll` —
  one Imbox read that also diffed a fingerprint file to toast — was built and superseded
  before release: it was ten minutes stale by construction, and an Omarchy-named command
  that sounded watch-like sullied the CLI surface.)

## Follow-ups, in rough order

1. **mailto: handler** that opens a floating compose (`hey compose --mailto`), opt-in
   against the incumbent `omarchy-webapp-handler-hey`.
2. **Agent-triage digests**: `hey --json` feeding a system agent that emits sparse
   toasts instead of per-message noise.
3. **Upstream contributions**: a `default/themed/hey.toml.tpl` PR alongside
   `claude.json.tpl`; an Install-menu TUI row; possibly branching the mailto handler to
   the TUI when installed. (An AUR package already ships: `yay -S hey-cli`, published by
   the release workflow.)
4. **Deltas applied in the plugin**: every watch event already carries the full posting,
   but the plugin re-reads the Imbox per batch today. Applying events in place — reading
   only on `ready` and `resync` — would make an ordinary change cost no API read at all.
5. **`hey watch --notify` off Linux**: the flag is libnotify-only. `terminal-notifier` or
   `osascript` on macOS would make it the same one-liner there.

## Anti-features, recorded

- No unread **count** in the bar icon or the toast.
- No per-message notification firehose.
- No full hex theme port.
- No auto-editing `~/.config/hypr/bindings.lua`.
- No HTML scraping to feed widgets.
