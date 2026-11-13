# Before-and-after bug proof

The report defines the acceptance case. A green neighboring test does not.

1. Read the matching feature recipe, public command help and actual assertions. State the user's condition, entry point and expected result in one sentence each. For shared code, include the other callers/surfaces the patch can affect.
2. Read the subsystem design doc before changing Go. Build the baseline from the relevant revision and run the real reported path. Prefer a new focused `Test...` in `tests/smoke`, package `smoke_test`, using the existing `hey`/`heyJSON` helpers and fixture cleanup. No new HTTP/auth client is needed. Use only owned fixture IDs, never a fallback to existing mail/contact/calendar data.
3. Arrange the precondition through supported fixture setup, then act through the public CLI. Distinguish fixture setup from the operation under test. In a draft report, a self-thread setup send does not mean the draft sent mail. A preview is not a persisted draft readback.
4. Add the assertion that would fail for the reported defect. For example, recipient loss must inspect the saved draft's To/Cc/Bcc arrays; `is_reply`, body text or a successful command cannot prove those recipients survived. Keep a positive control when useful so a broken login or setup cannot masquerade as the expected failure.
5. Run the exact named case with the helper. For an expected red run, capture its nonzero status without stopping your overall investigation, then inspect `events.jsonl`, `output.log`, `result.json` and receipts. Require that the named test ran, fixture setup succeeded, and the failure is the reported mismatch—not auth, missing seeds, skipped writes or cleanup. Confirm teardown even on red.
6. Fix the product, rebuild, and repeat the **same** repro. Preserve the red evidence and use a new run for green. A cheap local unit regression complements this runtime proof. Do not delete/relax the assertion or replace it with an unrelated happy path to get green.
7. Exercise the relevant neighboring mapped paths. CLI and TUI are separate obligations when both changed. Report anything not reached honestly. Keep source/test changes separate from skill maintenance corrections.

## Harness and evidence use

From the repo root, after `make build`:

```bash
VERIFY="$PWD/.agents/skills/verify-hey-cli/scripts/smoke"
# Replace the selection with the actual named case you added, not a guessed regex.
"$VERIFY" TestReplyDraft
```

The helper accepts exact Go test names, not regexes. The name above is an existing baseline, not the recipient-loss repro. A fresh agent must write/select the real case and report why its assertions address the report. It may add a scratch case in its own worktree for investigation; keep the useful regression in the product PR, not as hidden skill-only coverage.

To compare separately built artifacts, use `--binary` with each absolute binary path. Keep their build commands/source revisions outside the skill and record deliberate fault injection as such. Don't present an injected fault as a discovered product bug, a stub server as live HEY, or a metadata label as evidence of source provenance.

## Hand-off record

Return the condition, source/artifact identities, test name and actual assertions, red failure and green observation, independent side-effect/readback evidence, cleanup result and remaining gaps. An unfamiliar verifier should be able to repeat it from the recipe and repository without this conversation or private implementation notes. Missing access/state is blocked; a missing test assertion is work to do, not a reason to declare pass.
