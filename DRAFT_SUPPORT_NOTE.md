# Draft Support Implementation Note

## Authorship

Authored by Mike Gyi with Codex 5.5.

## Intent

The goal of this work is to make HEY draft workflows scriptable from the CLI while keeping Basecamp's CLI taste as the reference point.

The public Basecamp CLI treats drafts as part of the normal creation flow, for example with a `--draft` option on a create command. This HEY CLI change follows that shape by adding draft mode to the existing mail commands:

```sh
hey compose --draft --to person@example.com --subject "Hello" -m "Draft body"
hey reply 123 --draft -m "Thanks!"
```

Explicit draft commands are still included because they are useful for agent and scripting workflows:

```sh
hey draft create --to person@example.com --subject "Hello" -m "Draft body"
hey draft update 123 --subject "Updated subject" -m "Updated body"
hey draft delete 123
```

## Implementation Constraint

The current HEY SDK surface does not expose first-class draft create/update/delete methods. The implementation therefore uses the authenticated HEY web form flow for draft mutations, with CSRF parsing and fail-closed form validation around the fields needed by the CLI.

If HEY later exposes official draft endpoints in the SDK, the CLI command surface should stay the same and only the internal draft implementation should move to the official API.
