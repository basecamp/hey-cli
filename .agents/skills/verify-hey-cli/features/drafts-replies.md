# Drafts and reply envelopes

Save unfinished mail without delivering it, resume/edit it, and preserve a reply's thread, subject, body and intended To/Cc/Bcc recipients.

## Sub-features

- Create, list, read, edit and delete a draft.
- Refuse a send with no recipients without losing the draft.
- Save a reply draft on its intended thread; retain the envelope and body.
- Send an owned draft only with confirmed local capture; remove its draft and clean the resulting thread.

## How to get to it (user POV)

The live recipes target the server API, not app profiles. Server entry points are `hey compose --draft`, `hey compose --thread-id <topic-id> --draft`, `hey reply <topic-id> --draft`, and `hey draft list/show/edit/send/delete`. Explicit reply recipient overrides use `--to`, `--cc`, `--bcc` and `--replace-recipients`; do not confuse replacement with merging HEY's prefill. App profiles reject `compose --thread-id` and reply recipient overrides; use their supported `hey reply <topic-id> --draft` path and verify it separately. TUI compose/reply forms are a separate entry point under [TUI](tui.md).

## Driving it with the smoke harness

Preconditions: skill doctor passed; self-thread/send cases have local-only routing and capture. Read `tests/smoke/draft_lifecycle_test.go` and the command's `--help`.

- **Lifecycle.** Run `"$VERIFY" TestDraftLifecycle TestDraftDelete TestDraftSendRefusesWithoutRecipients`. The assertions check persisted subject/body/recipients and list membership, refusal, removal after send/delete, and owned-thread cleanup. Read them before claiming a particular field was exercised. The refusal test checks the error, not the retained draft before cleanup; add that readback for a draft-loss report.
- **Reply baseline.** Run `"$VERIFY" TestReplyDraft`. It creates its own topic, saves a reply draft and checks `is_reply`, body and listing. It does **not** prove which thread owns the draft, or Cc/Bcc/subject preservation. `draft show` has no thread/entry identifier; a linkage report needs an independent observed association, not `is_reply`.
- **Specific bug.** Add a focused case following [bug reproduction](../references/bug-reproduction.md). Use `findSmokeThread`, `heyJSON`, `draftData`, `draftState` and `registerDraftCleanup` from the existing package. Drive the reported reply command; read `hey draft show <draft-id>` and assert the exact missing/changed fields. For an explicit-Cc report, assert the saved `cc` array, not a preview's array or only `is_reply`.
- **Side effects.** Confirm the owned draft appears and is removed; check local capture when asserting delivery/non-delivery. A reply setup self-send is separate from the draft action and must not be counted as draft delivery.

## Gotchas

Never fall back to an unrelated Imbox thread. Keep the topic/draft IDs returned by the operation and register cleanup before later assertions. A draft is not a dry run. Refused/unsupported writes skip in non-strict mode, which is why verification forces strict mode. Fixtures with missing IDs fail and need recovery from their recorded subject/command. A failed setup compose can also retain a draft in `meta.draft_id` on `not_delivered`; that path is not cleaned by `findSmokeThread`. Read the error metadata/receipts, recover that exact draft through an isolated development login, and mark cleanup blocked until its absence is confirmed. Existing send and replacement-envelope tests do not automatically cover the save-draft branch.
