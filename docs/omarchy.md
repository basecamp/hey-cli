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

Idempotent; `--remove` reverses every piece but one — the bar plugin's `notify` setting,
which is the plugin's own (see its row) — and each step is reported separately; one failing
step does not stop the others.

| Piece | Where | Notes |
|---|---|---|
| Desktop entry | `~/.local/share/applications/HEY TUI.desktop` | Distinct from Omarchy's shipped `HEY.desktop` web app. Launches under app-id `org.omarchy.hey` |
| Menu row | marker block in `~/.config/omarchy/extensions/omarchy-menu.jsonc` | one root `HEY` row that focuses or launches the TUI; its guard is a PATH lookup, never network or `hey` itself. Becomes a submenu once there is more than one thing to open |
| Bar plugin | clone under `~/.config/omarchy/plugins/37signals.hey`, entry in `~/.config/omarchy/shell.json`'s bar layout | installed and enabled by **signing in with hey** (asked once — see below) or by this command, which also finishes an interrupted install and re-enables a plugin you disabled, verified against the running shell. `--notify` / `--no-notify` set or delete the entry's `notify` key, which the shell hot-reloads and the plugin reads to decide whether to toast. `--remove` disables the plugin and keeps its checkout. An earlier inline `hey-unread` module is removed on sight, its notify choice carried over |
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
engine, and the plugin composes its generic commands: `hey box view imbox --json` for the Imbox,
`hey watch` to know when to read it again, `hey screener list --count` for the Screener,
`hey seen` for marking, `hey account list` and `hey auth status` for the rest. The
division was settled when both sides turned out to have built a bar indicator without
knowing of the other — two HEY icons, two Imbox pollers, two product stances in one slot —
and the answer was: **the CLI is the engine, the plugin is the face.**

The plugin is an Omarchy `service` plugin as well as a bar widget. The shell instantiates
the service once, so one `hey watch` runs per shell however many monitors carry the bar,
and every bar widget reads the shared service. A watch event is a wake-up, not a delta —
any line on the watch's stdout re-reads `hey box view imbox`, debounced so a burst of changes
costs one read (plus one follow-up when changes land while a read is in flight, since that
read may predate them). `hey watch` says `ready` once every box is caught up and its
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
`notify` only gates the plugin's toasts; the watch runs on regardless, and nothing restarts.

### Installed at sign-in

Signing in with hey is what puts the plugin in the bar, with no hand-copied commands.
One idempotent routine (`internal/cmd/omarchy_plugin.go`), three entry points, one state
file, one lock:

- **Entry points.** An interactive OAuth sign-in — `hey auth login` / `hey login`,
  `requireAuth`'s "Sign in now?" (how `hey tui` and every data command sign in), the lite
  wizard — runs the routine in *ensure* mode and prints at most one stderr line; the full
  wizard runs it as Step 3; `hey setup omarchy` runs it in *force* mode. The automatic
  hooks never run for machine output, `HEY_NONINTERACTIVE`, a non-TTY, or
  `--token`/`--cookie` logins: a script installs with `hey setup omarchy`, which works in
  every output format and exits `setup_failed` on any incomplete outcome — an incomplete
  forced install is a failure, never a quiet skip.
- **Ordering, per mode.** Ensure: state gates → shell probe → consent → intent write →
  `omarchy plugin add` → verify → final write. Nothing is asked where installation is
  impossible, nothing is cloned before the shell is known up and the intent is on disk,
  and every incomplete outcome is non-fatal to the login. Force: the intent write comes
  first — recording acceptance, clearing a decline, a removal and the clone throttle,
  since the command is the explicit ask — so a crash leaves a pending install the *next
  explicit setup* finishes.
- **The marker**, `StateDir()/omarchy/bar-plugin.json`: `accepted_at` is consent, asked
  once and never re-asked; `pending_enable` + `installing_at` an unfinished install;
  `installed_at` history; `declined_at`/`removed_at` the user's no; `last_clone_*` a 24 h
  retry throttle for failed clones only — nothing else is throttled. A marker that cannot
  be read fails installation closed ("delete it, then run hey setup omarchy"); only
  `--remove` still disables then, since nothing can resurrect the plugin through a
  fail-closed install. The flock beside it (`bar-plugin.lock`) serializes every look and
  mutation; a held lock is a quiet skip at sign-in and a failure under force.
- **Ensure never enables an off-bar checkout.** A plugin the user disabled stays disabled
  whatever the marker says — pending included, where the sign-in owes the one-line notice
  "install incomplete — run hey setup omarchy" and nothing else. Ensure's only enabling
  mutation is `omarchy plugin add`, on fresh consent or a pending retry; re-enabling is
  force mode's alone (`omarchy plugin enable`, the reversal of `omarchy plugin disable`).
  The one other thing a sign-in may write is the one-time legacy migration: once the
  plugin is on the bar, the old inline `hey-unread` module leaves shell.json, its notify
  choice carried along.
- **The probe**, `omarchy plugin list --json`, distinguishes three failures: the omarchy
  CLI missing ("update Omarchy"), the shell not running (an ssh session — nothing is
  asked, cloned or throttled), and an answer that is not a plugin list (fail closed). Its
  `{id, enabled}` refines what shell.json's layout says. Installed is decided by the
  shell: the verify probe must list the plugin enabled, and the final write must land —
  "enabled, but could not record" is a failure everywhere, never a success with a hidden
  error; the next run's crash repair finishes the marker.
- **`--remove`** writes its tombstone first — a removal that cannot be recorded runs
  nothing — then `omarchy plugin disable 37signals.hey`. The checkout stays: `omarchy
  plugin remove` deletes it, and hey never runs that, nor `rm`.
- **The source is not configurable.** `omarchyBarPluginSource` is a package var only as a
  test seam; there is no flag, env var, or build override, so nothing but the public
  repository ever ships or installs.

### New mail is a watch event

What counts as new mail needs HEY's semantics and state across events, so the CLI decides
it once: every `added` and `updated` line `hey watch` writes carries `"new": true|false`,
and `--events new` selects the true ones. The rule:

- **New** is a posting that is unseen, not muted, and whose `active_at` is later than the
  watch last recorded for the thread — or later than the watch's start, when it has no
  record. That start is read off HEY's own clock (the `Date` header of one request,
  translated back to the moment the request was made), the clock every `active_at` is on,
  so a workstation running fast or slow neither calls the backlog new nor sits on new mail;
  whole seconds, rounded down, so the doubt falls on the side of calling mail a moment old
  new, and every box's cursor starts no later than that start, so mail that lands while the
  watch is starting up is read and is new. `active_at` moves on new mail only, not on a
  seen flip, a mute or a move, so reading a thread, marking it unseen again or moving it
  into a box is never new, and a reply on a known thread is. A box's first read is its
  catch-up from the server's cursor — the box's last activity, not this moment — so it
  carries backlog, which the start-time rule keeps out, alongside anything that arrived
  while the watch was starting, which is new.
- **Every posting the watch reads is recorded**, in every box and whatever `--events` or
  `--box` reports — `--box` picks what is reported, every box is followed — so a thread
  known from a filtered-out change, or from another box, is never mistaken for new when its
  next change is reported. A read is classified before it is
  recorded. There is no state file: the record lives and dies with the watch.
- **`--events` is a union.** `new` alone is new mail only — a `resync` is not, so a script
  for new mail never runs on one; `added,new` is every arrival plus new activity on known
  threads; the default `added,updated,deleted,resync` is every line, each saying whether it
  is new. `hey watch --box imbox --events new --exit-on-first` is "block until new mail", and
  a `--run-*` script sees `HEY_NEW=1` or `HEY_NEW=0`.
- **A skip-ahead sets a floor.** A box that answered 409 was never read across the gap, so
  the cursor it skips to — the box's last posting activity — becomes that box's floor:
  activity at or before it is never new there, on a thread the watch knows or one it does
  not, so a reply the watch missed and then a move while still unseen is not new mail. Mail
  after the floor is. The floor is the box's own; a gap thread moved to another box is
  measured there and may read as new once — the `resync` line is the cue to re-read.

### The plugin toasts

The toast is presentation, and a desktop's business: the plugin watches every box (it has
to, to see a thread leave the Imbox), reads the `new` lines whose box is the Imbox — HEY's
attention model puts new mail in one place — and sends the toast itself through
`omarchy-notification-send`, the shell's own wrapper over `notify-send`.

- **One toast per burst, replaced not stacked.** The new lines of one read arrive together;
  a short debounce collects them into one toast — `Sender — Subject` for one thread, `N new
  in Imbox` with the first few senders for more. The daemon's printed id (`-p`) is kept in
  memory and passed back as `-r` for ten minutes, so the next burst replaces the toast on
  screen instead of stacking; after that a fresh toast, since ids are daemon-local and a
  shell restart may have handed the number to another application.
- **DND is honoured.** The toast goes out under `--app-name HEY`: Omarchy's default
  app-name `omarchy-action` deliberately bypasses notification silencing, so identifying
  as HEY is what makes SUPER+CTRL+comma mute the toasts (into history) like any other app.
- **A single click opens its thread.** The plugin reads the topic id from the posting's
  `app_url`, sends it to the named Omarchy TUI with
  `hey tui --instance omarchy --topic <id> --remote`, then focuses the
  `org.omarchy.hey` window. When no TUI is running, the same click launches
  `hey tui --instance omarchy --topic <id>` directly. A grouped toast focuses the Imbox because it represents
  multiple threads rather than choosing one for the reader.
- **No persistent state file.** Nothing is remembered but the last toast's id, in the
  shell's memory. A running TUI listens for topic requests on a mode-0600, instance-named
  socket in `$XDG_RUNTIME_DIR/hey-cli`, with an owner-only temporary directory as the
  fallback, and removes it when the TUI exits; turning toasts on is nothing more than
  setting the plugin key.
- **Mail text never reads as an option.** A subject or sender can start with a dash, and
  `notify-send` parses one wherever it appears; a leading word joiner (U+2060) makes the
  argument a plain positional and is invisible on screen.

Any other desktop gets the same stream with its own face: `hey watch --box imbox --events
new --run-async 'notify-send -a HEY "New mail in HEY"'` is the one-liner.

## Decisions

- **Sign-in with hey installs the face.** hey-cli observes a sign-in only on its own
  surfaces — the web app is not visible from the host, and there is no HEY desktop app —
  so the promise is "signing in with hey puts HEY in your bar", not "wherever you log
  in". Consent is interactive, asked once, and remembered; ensure never enables an
  off-bar checkout, so a plugin you disable is never resurrected by a sign-in; the
  automatic hooks never run for scripts, and `hey setup omarchy` is the explicit,
  verified, fail-loud path. The location-independent version is Omarchy's to ship (see
  Follow-ups).
- **Indicator, not count.** Pending screener mail is not what people mean by "important",
  and a number is the attention treadmill HEY exists to end. The glyph lights or it does
  not.
- **Accent overlay, not a full hex port.** Replacing the ANSI palette wholesale would
  trade away the free adaptation terminals already provide, and basecamp-cli's full port
  is the cautionary precedent.
- **Complement the shipped web app, never replace it.** Distinct desktop name, printed
  keybinding, the mailto handler left alone.
- **No HTML scraping to feed widgets.** The plugin reads with `hey box view imbox`, the same
  typed SDK read as everyone else.
- **CLI is the engine, plugin is the face.** The plugin owns rendering and settings — the
  toast included; the CLI owns what needs HEY's semantics — the changes cursor and catching
  up after a disconnect, what counts as new mail — where it is Go-tested once instead of
  re-derived in QML. The plugin's panel does show an unread *number* per
  account; the bar icon itself stays a glyph that lights or does not, and the toast never
  carries the unread total either — `N new in Imbox` is how many threads arrived in one
  batch, not how many are waiting. That divergence is the plugin's call and is recorded
  here rather than papered over.
- **Generic commands; the face presents.** There is no `hey omarchy` group and nothing
  desktop-shaped in the CLI at all: the plugin composes `hey box`, `hey watch`, `hey
  screener` and `hey seen`, and a user can run any of them by hand. What counts as new
  mail is a watch event — HEY semantics, decided once — and what to do about it (a toast
  under the shell's app-name, a glyph, a click that opens `hey tui --topic`) is the plugin's.
  (Two earlier cuts were built and superseded before release: a `hey omarchy poll` — one
  Imbox read that also diffed a fingerprint file to toast — that was ten minutes stale by
  construction and an Omarchy-named command on a general CLI; and a `hey watch --notify`
  that sent the toast itself through `notify-send` with Omarchy hints, which bundled a
  desktop's presentation into a generic command and scoped the Imbox with a flag of its
  own, where `--events new` and the plugin's own read of the box do the same without
  either.)

## Shell completions under mise

Omarchy installs hey through mise, lazily: the install step writes a small wrapper onto
`~/.local/bin/hey` that resolves the tool with mise and execs it on first use. Nothing
about that registers completions. mise has no per-tool post-install hook, `mise activate`
emits no completion code, and mise scans no directory of its own for a tool's completions
— so where the AUR package drops a script into `/usr/share/bash-completion/completions`,
a mise install drops nothing anywhere, and the user has no way of knowing they are missing
anything.

`hey shell-completion install` is the hey-cli side of that: it writes the script into
`~/.local/share/bash-completion/completions/hey`, which the loader Omarchy sources from
`default/bash/shell` lazy-loads by command name, with no rc change and nothing to source.
`hey setup` runs it, so a first `hey` settles it, and `hey doctor` reports it.

Two things it does that a hand-redirected `hey shell-completion generate bash` does not. It **points the
script at the resolved binary** rather than at the word the user typed: cobra asks
`${words[0]}` for completions, which through the wrapper means a `mise use -g` round trip
on every press of Tab. And it **refuses to overwrite a completion file hey did not
write**, on the same terms as the agent skill directories — the marker is a comment on the
file's second line, since zsh needs `#compdef` on the first.

The remaining half belongs in the Omarchy repo, not here: its lazily-created wrapper (or
the first-run path around it) is the natural place to call `hey shell-completion install`, the
way the AUR package's completions arrive without asking. hey-cli cannot install
completions for a binary that has not been fetched yet.

## Follow-ups, in rough order

0. **The Omarchy-side counterpart**: ship `37signals.hey` with Omarchy itself, so the
   panel is on the bar before hey-cli is installed — its own button already drives
   "Install HEY CLI…" and "Sign in to HEY…", which is the only way "the panel appears
   wherever you log in" becomes true (a web-app login is not observable from the host).
   A recommendation for the Omarchy team, not built here.
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

## Anti-features, recorded

- No unread **count** in the bar icon or the toast.
- No per-message notification firehose.
- No full hex theme port.
- No auto-editing `~/.config/hypr/bindings.lua`.
- No HTML scraping to feed widgets.
