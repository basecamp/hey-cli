# Local CLI settings

Read defaults, persist an allowed setting across processes, and refuse an invalid value without modifying the saved state.

## Sub-features

- Empty-client defaults and configuration sources.
- Set/read persistence and invalid/insecure URL refusal.
- The `onboarded` setting, profiles, trusted-local precedence and account selection require their own cases when changed.

## How to get to it (user POV)

Use `hey config show`, `hey config set` and their documented JSON forms. This recipe does not log in or establish mail/server readiness.

## Driving it with an isolated CLI

Preconditions: `make build` passed. Use a fresh temporary client and keep evidence separately:

```bash
RUN=$(mktemp -d /tmp/hv-settings-XXXXXX)
STATE=$(mktemp -d /tmp/hv-client-XXXXXX)
trap 'rm -rf -- "$STATE"' EXIT
mkdir -p "$STATE"/{home,config,state,cache,work}
BIN="$PWD/bin/hey"
cli() { (cd "$STATE/work" && env -i PATH="$PATH" HOME="$STATE/home" \
  XDG_CONFIG_HOME="$STATE/config" XDG_STATE_HOME="$STATE/state" \
  XDG_CACHE_HOME="$STATE/cache" HEY_NO_KEYRING=1 OMARCHY_PATH= \
  "$BIN" "$@"); }
cli config show --json > "$RUN/defaults.json"
cli config set base_url http://app.hey.localhost:3003 --json > "$RUN/set.json"
cli config show --json > "$RUN/persisted.json"
cp "$STATE/config/hey-cli/config.json" "$RUN/persisted-file.json"
```

- **Defaults.** Inspect `defaults.json`: `base_url` is the default HEY origin and `account_id` is `all`, both with source `default`.
- **Persistence.** The new process reports the development URL from source `global`, matching the actual saved file.
- **Refusal.** Run `cli config set base_url not-a-url --json` and `cli config set base_url http://example.com --json`, saving stderr and each original nonzero status. Require the intended structured usage errors. Compare the file and a new `config show` with `persisted-file.json`; neither must change. Do not mask failure with a final successful printf.
- **Cleanup.** Remove only this exact `$STATE` after inspecting it; retain `$RUN` and confirm its files survived. No server requests or network-absence claim is established by this recipe.

## Gotchas

An inherited profile, base URL or local `.hey/config.json` can invalidate a defaults test, hence the empty environment and working directory. A config write is not evidence of an API call or authentication. Don't overwrite normal configuration, claim source precedence from values alone, or use successful validation to infer all profiles/linked accounts work.
