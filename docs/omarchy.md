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
| Bar indicator | inline command module `hey-unread` in `~/.config/omarchy/shell.json` | runs `hey omarchy bar-status` every 3 minutes; click focuses or launches the TUI |
| Theme template | `~/.config/omarchy/themed/hey.toml.tpl` | renders `hey.toml` into every theme so theme authors can override the overlay; triggers `omarchy-theme-refresh` |
| Keybinding | printed, never written | `o.bind("SUPER + SHIFT + ALT + H", "HEY TUI", "omarchy-launch-or-focus-tui --app-id=org.omarchy.hey hey tui")`; SUPER+SHIFT+E keeps opening the web app unless you `hl.unbind` it. Spelled out rather than `{ tui = "hey tui" }` because the lua helper quotes that into one word and the app-id derived from it would never match |

Every surface — launcher, menu, bar click, keybinding — uses the same app-id
(`org.omarchy.hey`) so they all focus one window. That is why the desktop entry is tiled
rather than `TUI.float`: the float class is shared by every floating TUI, and
focus-or-launch would grab whichever one was open.

If the user's `shell.json` has no `bar.layout` yet, the default layout from
`$OMARCHY_PATH/config/omarchy/shell.json` is copied in first; the shell treats a missing
layout as "use the defaults", so adding one module means spelling out the rest. A
`shell.json` that is not plain JSON is left alone and the step reports failure.

### `hey omarchy bar-status`

Hidden command the bar module runs. Prints
`{"text":"","tooltip":"Unread in Imbox","class":"active"}` when the Imbox has unread
mail and nothing otherwise (the `text` is the nf-fa-envelope glyph U+F0E0, which most
browsers render as nothing — it is not empty). HEY orders Imbox postings unseen-first,
so one page decides: any unread mail is on page 1. Logged out or offline also prints nothing and exits 0 — a bar
is no place for an error message. Credentials come from the keyring or the
`credentials.json` fallback exactly as for any other command, so it works from the
shell's headless context; token refresh happens in-process.

## Decisions

- **Indicator, not count.** Pending screener mail is not what people mean by "important",
  and a number is the attention treadmill HEY exists to end. The glyph lights or it does
  not.
- **Accent overlay, not a full hex port.** Replacing the ANSI palette wholesale would
  trade away the free adaptation terminals already provide, and basecamp-cli's full port
  is the cautionary precedent.
- **Complement the shipped web app, never replace it.** Distinct desktop name, printed
  keybinding, the mailto handler left alone.
- **No HTML scraping to feed widgets.** The indicator uses the same typed SDK read as
  `hey box imbox`.

## Follow-ups, in rough order

1. **New-mail toasts** via `omarchy-notification-send --glyph --exec`, default-off,
   sharing one poller with the bar so the Imbox is fetched once per interval.
2. **mailto: handler** that opens a floating compose (`hey compose --mailto`), opt-in
   against the incumbent `omarchy-webapp-handler-hey`.
3. **Agent-triage digests**: `hey --json` feeding a system agent that emits sparse
   toasts instead of per-message noise.
4. **Upstream contributions**: a `default/themed/hey.toml.tpl` PR alongside
   `claude.json.tpl`; an Install-menu TUI row; possibly branching the mailto handler to
   the TUI when installed. (An AUR package already ships: `yay -S hey-cli`, published by
   the release workflow.)
5. **Shell plugin graduation** for the bar widget: `manifest.json`, a settings panel,
   IPC refresh when a thread is archived from the TUI instead of waiting for the next
   poll.

## Anti-features, recorded

- No unread **count**.
- No per-message notification firehose.
- No full hex theme port.
- No auto-editing `~/.config/hypr/bindings.lua`.
- No HTML scraping to feed widgets.
