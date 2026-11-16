---
name: verify-hey-cli
description: Reproduce and verify HEY CLI or TUI behavior against local development Haystack. Use before and after a bug fix, for PR runtime proof, or to maintain the user-facing feature map. Uses the existing smoke harness, owned fixtures and private evidence; unit tests alone are not live proof.
---

# Verify HEY CLI

Work from the hey-cli repository root. Read [the feature index](features/README.md), then the relevant recipe and its actual test assertions. This is an operating guide, not another test suite or a command inventory.

## Launch

For local configuration, follow [settings](features/settings.md); no server is needed.

Public contributors can run local CLI checks, unit tests and offline harness regressions without backend access. Live integration journeys use `tests/smoke` and require an authorized internal test environment: Haystack is closed source and is not supplied by this repository. Unavailable access is a blocker, not a provisioning task or permission to use a production account. Read the smoke [README](../../../tests/smoke/README.md) and [development setup](references/development.md) first. They require prepared local Haystack, exclusive development-account/server ownership, Chromium and the registered loopback OAuth client. Sending additionally requires confirmed local-only sender routing and SMTP capture. Do not reset/reseed an existing database, use production/beta, copy operator credentials or stop somebody else's server. A private client directory does not isolate the database.

```bash
make build
export VERIFY="$PWD/.agents/skills/verify-hey-cli/scripts/smoke"
```

The helper pins this build for one invocation. Rebuild after product or SDK changes. It records the source revision, tracked working-tree diff hash and binary hash; an explicit `--binary` is an artifact comparison, not proof it was built from the current checkout.

The smoke harness signs in through the real browser/OAuth flow. Each invocation gets fresh HOME/config/state/cache/work and finishes resource cleanup/readbacks before removing credentials. Do not use a bare `hey`, inherited profiles, a stored bearer token or an operator's normal authenticated session.

## Doctor

After taking ownership and establishing the setup above, require actual authenticated reads, not just HTTP `/up`:

```bash
"$VERIFY" TestAuthStatus TestBoxesList TestCalendarList
```

Require exit zero, every named test passed, and `result.json` with `ok: true`. This doctor covers authentication/mail/calendar discovery, not search indexing, SMTP routing, image processing or TUI rendering. Check the relevant prerequisite in the feature recipe too. Browser sign-in creates a server session/grant and can create a notification; it is not side-effect-free.

Each fresh invocation also repeats TestMain's server-reachability, browser-login and real OAuth setup checks before any test. Doctor each new live environment and after anything surprising. Diagnose a failed doctor before driving mutations. A healthy process with a wedged UI still needs a reset/relaunch. If the environment cannot supply the required state safely, record the attempted path and blocker; never replace the journey with an easier listing.

## Drive

For a regression, follow [the bug-reproduction procedure](references/bug-reproduction.md). First reproduce the reported behavior, then repeat that same check after the fix. Existing smoke cases are building blocks, not substitutes for a missing assertion.

```bash
"$VERIFY" TestDraftLifecycle TestReplyDraft
```

Name tests explicitly; do not pass a broad regex or run the entire mutation suite. Inspect fixtures and cleanup before selecting a case. Each invocation returns its evidence directory. `--binary /absolute/path/to/hey` compares a separately built revision; `--tags verification` compiles tagged cases only after their extra prerequisites are arranged. Do not select credential-output or logout tests through this evidence path.

The helper forces strict, uncached runs and verifies named tests actually ran without failed/skipped subtests. It does not decide whether those assertions prove the bug, choose a safe sender or grant account/server ownership.

For a TUI change, use [the TUI recipe](features/tui.md). A CLI command or Bubble Tea model test is not a rendering/key-interaction check. Unattended PTY driving is not established here; report that limitation rather than claiming CLI success covers the TUI.

## Evidence

Keep each run's private directory outside the checkout. It contains the binary hash and source metadata, Go JSON events, readable output, original exit status, audited result and allowlisted data-command receipts. Never record credentials, OAuth URLs/state or secret-valued arguments; do not upload private mail evidence to a PR without review.

Record the feature, reported condition, exact public entry point, fixture identity, action, expected outcome and observed outcome. A mutation needs a second read of the stored value and the feature's independent file/browser/delivery observation. An acknowledgement alone is not proof. Verify what `--dry-run` or `--draft` actually skips; a draft is a real server write.

Report **passed / failed / blocked / not attempted** per acceptance case, separately for CLI, model and live TUI. A skipped/missing test, wrong surface or inconclusive result is not a pass. A passing map/reference check is not behavior coverage. Record baseline and candidate separately, including setup differences; do not claim a comparison between unlike scenarios.

## Cleanup

Cleanup is part of the result, including after expected failures. Checked smoke callbacks cover the established fixture paths, not every legacy test. They retain hidden contacts/aliases and trash mail rather than permanently deleting it. They do not revoke all OAuth grants or remove sign-in notifications; do not mass-delete somebody else's sessions/grants.

The helper removes its owned temporary files after its process group exits, including Chrome residue, unless interrupted smoke client state needs recovery; its exact path is retained in `result.json`. A killed suite can bypass resource cleanup: inspect its exact fixture receipts and recover those resources with an isolated development login before calling teardown proven. Do not glob-delete other runs' state. Retain failure evidence and use a fresh run after fixing the environment or helper.

Stop a backend/capture only if this run started it, by its dedicated terminal or recorded container ID. Inspect that its ports/processes stopped. Never kill by process name. Confirm evidence still exists after cleanup; `/tmp` survives cleanup but not reboot/tmp cleaners. Retain reviewed evidence elsewhere when needed.

## Helpers

```bash
# From the repository root; POSIX/Linux/macOS, Python 3, git, ps and cached Go dependencies.
.agents/skills/verify-hey-cli/scripts/smoke TestReplyDraft
.agents/skills/verify-hey-cli/scripts/test-smoke
```

The helper runs the existing Go harness; it is not a second HTTP/auth client. Local settings use the public binary directly as documented in their recipe.

To maintain this guide, use [the index's maintenance contract](features/README.md#maintenance). Keep recipes tied to observable behavior, not every implementation symbol. A maintenance run edits only this skill's directory. A needed fix in `tests/smoke` or product code is a separate change, not permission to copy that code here or weaken the expected behavior.
