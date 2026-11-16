# Read and find mail

Choose a box, find the intended message and read the whole thread in the requested format without silently substituting a different item or accepting a partial body.

## Sub-features

- Discover/open boxes by supported names and IDs.
- Search with criteria and distinguish matching, empty and refused queries.
- Read complete long threads, oldest first, with hydrated bodies and preserved text/URLs.
- CLI output variants and paging are additional acceptance cases, not implied by one read.

## How to get to it (user POV)

Use `hey box list`, `hey box view imbox`, `hey search` and `hey thread read <topic-id>`; direct `hey box imbox` is a separate supported compatibility path when the change touches it. Plural `hey boxes`/`hey threads` are not registered in the current root; do not copy stale examples. TUI box/search/thread navigation is in [TUI](tui.md).

## Driving it with the smoke harness

Preconditions: doctor passed, seeded readable mail for discovery/search, working Elasticsearch indexes. Long-thread cases send self-mail and need local routing/capture.

- **Discovery.** Run `"$VERIFY" TestBoxesList TestBoxImbox TestBoxByName TestBoxByID`. Inspect `tests/smoke/boxes_test.go` for seed assumptions before selecting. `TestBoxesList` uses canonical `box list`; the other cases use compatibility `box <name|id>` syntax. Add canonical `box view` and exact requested-kind assertions when that behavior is at issue.
- **Search.** Run `"$VERIFY" TestSearchFilters TestSearchFreeTextAndRefinements TestSearchRequiresCriteria`. Read `search_test.go`; a fixture missing from the index blocks the corresponding assertion. These baseline tests accept valid nonempty result rows; they do not establish exact fixture identity or a successful empty-result case. For a targeted regression, retain the expected topic ID and compare returned IDs, and add an explicit zero-match assertion if that is the report.
- **Bodies.** Run `"$VERIFY" --timeout 5m TestThreadsReadsALongThreadAsMarkdown TestThreadsFormats`. `threads_test.go` builds owned multi-entry threads, verifies bodies/formats and cleans them. CLI JSON/header-only success does not establish that the body on the reported entry was read.
- **A paging/body bug.** Add the exact cursor, entry count, body size or partial-read condition to a focused case. Inspect `docs/architecture/threads-and-replies.md` and `mail-sources-and-paging.md`; assert the user's final data, not an internal cursor implementation.

## Gotchas

Topic IDs identify threads; posting IDs identify box items and are used by mutations. Bundles are not ordinary topic rows. A box listing and search use different paging contracts. Index lag, missing seeded mail and truncation are not successful empty results. Do not mark a CLI body test as a TUI rendering proof or expand these read recipes into permission to file/spam existing mail.
