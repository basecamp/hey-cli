# Local development environment

Haystack is a closed-source service, not part of this repository. Public contributors are not expected to provision it or have internal access. They can build the CLI, run unit/offline harness tests and check isolated local configuration. Live integration checks require an explicitly authorized, prepared internal test environment; otherwise report them blocked. Never use a personal production HEY account as a substitute for the development mutation suite. Read `tests/smoke/README.md` for the current internal environment, OAuth, Chromium and sender prerequisites.

## Ownership and readiness

Obtain exclusive use of a prepared **development** identity/server. Record who owns the backend/capture and which linked account is permitted. Do not infer permission from an open port. A backend supplied by the operator/coordinator is a prerequisite, not a process the child agent may stop.

Default origin is `http://app.hey.localhost:3003`. Elasticsearch 8 uses 29208. Use the backend checkout or test environment supplied by its owner, without assuming a location on disk. If you are authorized to start it, confirm 3003 is unused, read that checkout's AGENTS/README and Ruby version, and use its `bin/dev` in a uniquely owned terminal. `bin/dev` includes Hotcell, required for photo processing. Use its configured Ruby through mise if the shell's Ruby differs. Stop that terminal with Ctrl+C after final cleanup, not by process name. Never run `bin/setup` on an existing checkout/database as a repair shortcut.

Do not capture raw browser/OAuth or server request logs; URLs/parameters can contain credentials or OAuth state. The smoke harness deliberately suppresses OAuth progress. Keep data evidence private too.

Set only the fixture inputs authorized for this run:

```bash
export HEY_SMOKE_BASE_URL=http://app.hey.localhost:3003
# HEY_SMOKE_EMAIL and HEY_SMOKE_ACCOUNT_ID come from the prepared test identity.
# Supply a nondefault test password through the environment/secret facility,
# never as an echoed command, process argument or committed file.
```

The harness defaults are in `tests/smoke/helpers_test.go`; do not assume they match the current seed. Its browser login and explicit loopback OAuth consent must work. Do not copy a browser cookie into the CLI or borrow an operator's token. Linked-account selection is `HEY_SMOKE_ACCOUNT_ID`, not an inherited `HEY_ACCOUNT_ID` or profile.

Run the skill doctor after establishing these prerequisites. Search also needs working indexes; a known fixture must be found by its exact ID, not a fallback to another result. Photo checks need Hotcell. A server answering `/up` does not establish these capabilities.

## Sending

Composing a self-thread sends real mail, including fixtures used by reply and thread tests. Before any such test, confirm the selected account has no external SMTP/Gmail/Outlook sender route and every applicable development SMTP relay points to local capture. An `example.org` recipient is not a delivery safeguard.

If you own capture startup and 1025/8025 are unused, a local Mailpit container can be started with loopback-only published ports. Give it a unique name and record the exact returned container ID. Do not replace another listener. Verify its HTTP API is ready, retain delivery observations privately, and stop only that recorded container after cleanup. The smoke README documents the capture URL and extra sender inputs for sender-selection tests; ordinary draft-only operations must not be confused with those tests.

Missing local routing/capture blocks sending, including self-thread setup. Do not use a dry-run or an arbitrary existing message as a substitute for the reported write path.

## Recovery

A failed normal test still runs its registered cleanup callbacks, but a setup write can fail before it registers one. In particular, `not_delivered` can retain a draft named by `meta.draft_id`; inspect error metadata and receipts, then recover that exact resource. Do not assume a normal return proves all fixtures were registered or removed. A timeout or killed process may not. The helper records its owned temporary root and fixture receipts. Recover exact IDs with a fresh isolated development login if the old state is gone; do not copy credentials from elsewhere. A successful trash/delete acknowledgement still needs independent readback. Search can lag; report that separately from the underlying state rather than claiming it found the resource.

After stopping only owned resources, confirm their processes/listeners are gone and that evidence survived. Document any retained hidden contact fixture, grant/session or unresolved resource. Do not describe incomplete cleanup as a passed run.
