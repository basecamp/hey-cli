# Real terminal interactions

Verify what a person sees and does in the running TUI: loaded data, focus, keys, forms, cancellation and resulting state—not just a model update or equivalent CLI command.

## Sub-features

- Mail sources/search/thread navigation and compose/reply forms.
- Contacts, calendar and journal sections; modal/help/account state.
- Loading/error/reconnect and terminal rendering at the reported size.

## How to get to it (user POV)

Start `hey tui` in a PTY with its own development login. Destination routes include `--topic`, `--screener`, global `--account` and `--remote`; read their actual help and `docs/tui.md` when the report uses them. Follow `docs/tui.md` for actual keys and the report's section/form entry point. The CLI entry points in the other recipes are not substitutes.

## Driving it with an owned PTY

Preconditions: an isolated, explicitly authorized development login, a healthy local backend and a uniquely owned terminal/session. No unattended live PTY journey has been established by this skill. Do not borrow the operator's credentials to fill that gap.

- **Prepare.** Read `docs/tui.md` and the relevant TUI section/model tests. Confirm the development origin, build, private HOME/XDG paths and allowed account before starting. Use the available control-cli/PTY tool; do not invent its commands if it is absent.
- **Ready.** Require the intended section and actual loaded data, not a spinner, login offer or error pane. Capture the terminal size and initial rendering.
- **Drive.** Deliver the report's keys through the PTY. Capture each action and resulting pane separately. Check focus, the exact displayed value and the persisted/server state for mutations; also exercise cancellation/refusal where relevant.
- **Finish.** Stop only the owned terminal/TUI process, clean owned server resources before removing its login, and retain pane captures/action transcripts. Report CLI, model and live TUI results separately.

## Gotchas

Autonomous bug fixing of a TUI-only issue is blocked until an authorized unattended login/driver or attended session is available. Record the concrete missing tool/auth/fixture and route attempted. Never mark it passed because a CLI command works or a Go model test passes. Once a PTY journey is established, split it into concrete section/interaction recipes with real commands and captures; don't turn this generic checklist into a claim of full TUI coverage.
