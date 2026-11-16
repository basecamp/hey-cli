# Contacts, notes and photos

Read a contact, edit its identity/aliases, preserve private notes, and manage the uploaded photo and contact visibility.

## Sub-features

- List/show a known contact and read its threads.
- Create/edit an owned contact and alias; persist a private note.
- Upload/remove a photo and hide/show a contact.

## How to get to it (user POV)

Use `hey contact list/show/threads/add/update`, `hey contact note`, `hey contact photo`, `hey contact hide`, `hey contact show-again`, `hey contact deliver`, `hey contact bundle` and `hey contact unbundle`. TUI contact list/detail/forms are a separate surface under [TUI](tui.md).

## Driving it with the smoke harness

Preconditions: doctor passed, a linked development account with known contact/thread fixtures. Photo processing requires Haystack `bin/dev` including Hotcell.

- **Reads.** Run `"$VERIFY" TestContactsListAndShow TestContactThreads`. Inspect the seed choices in `tests/smoke/contacts_test.go`; these are not proof of every contact/filter/paging path.
- **Writes.** Run `"$VERIFY" TestContactLifecycleAndPrivateNote TestContactPhoto`. The tests create unique contacts, separately read aliases/plain notes, compare uploaded/restored avatar URLs, and observe final hidden pages during cleanup. Name edits and reveal/bundle/delivery actions are mostly checked through responses, not independent persisted readbacks; the lifecycle changes bundle state and delivery destinations on the owned fixture before cleanup restores/hides it. Formatted-note preservation and visibility after reveal need extra assertions.
- **Validation.** Run `"$VERIFY" TestContactCommandsValidateInput` for the existing refusal cases. Add an exact assertion for the report's field or transition; a success summary alone is insufficient.
- **Cleanup.** Inspect cleanup readbacks before reporting pass. Only the owned fixture's denied clearance is restored to reach the hidden notice. Notes/photos are cleared; the contact and aliases remain hidden, not deleted.

## Gotchas

Alias pages redirect to the primary contact, so they do not independently prove each alias's stored hidden flag. Screened Out pages have different notices. An avatar URL can change when initials change; photo-removal assertions need a valid baseline for the current identity. Never reveal/edit a pre-existing person's contact as a convenient fixture. Model tests do not prove photo rendering in a terminal.
