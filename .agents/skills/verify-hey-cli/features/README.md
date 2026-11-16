# HEY runtime verification map

Choose a user journey, not a source-file count. These are starting recipes for a bug-specific proof, not permanent passed statuses. Read the actual tests before running them.

| Feature | User outcomes and entry points | Recipe |
| --- | --- | --- |
| [Local settings](settings.md) | CLI defaults, persistence, refusal and unchanged state | Local commands |
| [Read and find mail](mail.md) | Boxes/search and complete thread bodies, CLI formats | Existing live tests |
| [Drafts and replies](drafts-replies.md) | Save/edit/delete/send drafts; reply envelope/body preservation | Existing live tests plus bug-specific assertions |
| [Contacts](contacts.md) | Contact/alias edits, private notes, photo and visibility | Existing live tests |
| [Calendar events](calendar.md) | Event fields, all-day dates and repeating-day scopes | Existing live tests |
| [TUI interactions](tui.md) | Real terminal rendering, keys, modal state and live data | Attended/manual; no established unattended PTY recipe |

## Before a drive

Use the skill's Launch and Doctor sections. CLI live commands go through the real `tests/smoke` binary/OAuth setup. Fresh client state does not isolate linked accounts or backend data. No parallel live sessions on the same server/account. Source readers can work concurrently; app driving stays with one coordinator unless it has genuinely separate backends/accounts.

Each recipe has four sections: sub-features, every named user entry point, harness actions paired with expected observations, and gotchas. `tests/smoke/README.md`, command `--help`, `docs/cli.md` and `docs/tui.md` remain the detailed references. Source pointers help investigation; they are not frozen implementation specifications.

## Scope and missing journeys

This initial map is **not exhaustive HEY coverage**. Sending/forwarding/bulk operations beyond these fixtures, mail filing/spam/Screener, labels/collections/bundles/workflows, attachments/sharing, watch/reconnect, linked sender selection, profile/auth/setup/upgrade/MCP, todos/habits/journal/time tracking, and platform variants need their own reviewed cases. Existing tests are candidates, not permission to run them blindly. Some select existing resources, overwrite fixed dates or have incomplete cleanup.

If a report concerns an unmapped feature, inspect its public entry points and source, then add a concrete recipe and focused repro before claiming verification. Do not silently substitute a neighboring mapped feature. Do not group a whole subsystem under one successful listing to make coverage look complete.

## Maintenance

Check index hygiene; fan out read-only source readers per feature; reconcile their recipes; sweep recent changes for missing user paths; then have the coordinator drive every mapped feature at least once under its documented launch model. Structural checks do not replace this live pass.

The outcome is `clean` only when the required source/live coverage completed and nothing needs shipping; `changed` ships at most one skill-directory PR of proven corrections; `blocked` names unfinished coverage or an unsafe/unshippable correction. An unreachable path needs the attempted route and concrete prerequisite, not merely the word manual. With the current TUI gap, do not call a whole-map pass clean without actual PTY evidence or a concrete attempted route and unmet prerequisite.

Correct doc drift and skill-owned helper gaps here, re-driving helper fixes live. Record product regressions without editing away the expected behavior. A harness change outside this directory is a separate PR. Keep concise dated coverage/evidence notes outside the repository; do not commit a permanent verified flag or private run artifacts.
