# Live CLI smoke tests

Run `HEY_SMOKE_STRICT=1 make test-smoke` from the repository root against dev
Haystack. The suite builds `bin/hey`, signs in with headless Chrome, and uses
isolated CLI configuration and credentials. Chrome approves the URL from
`hey auth login --no-browser`, exercising the real OAuth/PKCE callback and token
exchange. The dev server must have the built-in HEY CLI OAuth client registered
with loopback redirects enabled; the suite does not register or bypass it.
Browser cookies stay in browser helpers and are never used to authenticate the CLI.
OAuth progress, codes and tokens are kept out of setup logs. Strict mode turns
skips into failures.
The default server is `http://app.hey.localhost:3003`; see the root `AGENTS.md` for
login and environment overrides. The full SMTP/browser suite can exceed five
minutes; `make test-smoke` gives the complete run a ten-minute deadline.

## Harness regressions and cleanup

The harness has offline regression tests that need no server, browser, login or SMTP:

```bash
cd tests/smoke
HEY_SMOKE_OFFLINE=1 go test -race -count=1 -timeout 1m -run '^TestHarness'
```

CI runs these separately because this directory is its own Go module. Offline
mode refuses any other selector, so ordinary feature tests cannot masquerade as
live proof. The regressions fake the existing command boundary, not a HEY HTTP
server. They check exact fixture identity, missing fixtures, failed/no-op cleanup,
incomplete readbacks, credential-evidence exclusion and setup-failure teardown.

Draft, owned-thread, contact and event cleanup callbacks check command failures
and read back the resulting state before credentials are removed. Event cleanup
reads all calendars, including switched-off ones, over the fixture's date window.
Thread setup
polls for the exact created topic and retries transient reads. If delivery never
arrives, cleanup makes one final lookup instead of repeating the full wait.
Contact fixtures and their aliases are retained hidden, with notes and uploaded
photos removed; alias pages redirect to the primary contact, not an independent
view of each alias's hidden flag. The owned HOME/config/state/cache/work directory
is removed on normal returns, including browser/OAuth setup and test failures.
A killed or timed-out process can still leave state and fixtures behind; record
the run's exact paths and fixture IDs for recovery.

`HEY_SMOKE_EVIDENCE_DIR` optionally retains `commands.jsonl` for allowlisted data
commands invoked through the shared `hey()` helper, including the test name,
argv, stdout, stderr and status. Leading-flag invocations, unknown commands,
auth/token commands and browser/OAuth progress are not recorded. This is not a
complete transcript of direct subprocess calls. Use an absolute path outside
disposable client state. Keep it private: mail bodies and sharing URLs can be
present. On POSIX, existing directories/files with group or public permissions
are refused rather than written.

Do not run the full suite on a shared identity just to broaden coverage: journal
tests overwrite fixed dates and some other tests still select existing mail/habits
or lack complete cleanup. These changes do not repair every smoke test's cleanup.
Confirm exclusive development ownership and local-only SMTP/capture before sending.

Child CLIs ignore inherited `HEY_*` settings and receive only the harness's
origin, account and keyring settings. Set `HEY_SMOKE_ACCOUNT_ID` to scope the
suite to a particular linked development account; an ordinary `HEY_ACCOUNT_ID`
from your shell does not select it. `HEY_BINARY` may be absolute or relative to
`tests/smoke` (the Go test process's working directory).

## Development prerequisites

A server answering HTTP is not enough for the full suite:

- Start Haystack with `bin/dev`, not a bare Rails server. It starts the image-processing
  cell and passes `HOTCELL_ROOT` to Rails. Without that connection, photo uploads can
  succeed but fetching the resulting avatar fails.
- Run the Elasticsearch version Haystack configures. Its default development endpoint
  is Elasticsearch 8 on port `29208`; Elasticsearch 7 on `29207` is a different service.
  For an existing checkout, start the required service with
  `~/Work/basecamp/docker-dev/setup --wait elastic8`. Search and contact listing need
  its indexes too; resolve any indexing error from Haystack setup rather than treating
  HTTP readiness as proof that search works. Do not reset databases to repair search.
- Provision the local SMTP capture and custom-domain sender described below, and pass
  all three `HEY_SMOKE_FROM`, `HEY_SMOKE_SENDER_ACCOUNT`, and
  `HEY_SMOKE_MAILPIT_URL` values to the full strict run. Missing sender fixtures are a
  strict failure, not evidence of a command regression.

Keep strict mode enabled. These prerequisites do not justify skipping a failing search,
image or delivery assertion. Haystack's `bin/setup` can discard uncommitted schema dumps;
read its instructions before rerunning it.

## Cookie-removal verification

The opt-in `verification` build tag adds a local HTTPS regression check. Supply a
**disposable** pending clearance and its contact in `HEY_VERIFY_CLEARANCE_ID` and
`HEY_VERIFY_CONTACT_ID`. The contact's name must be `Morgan Shaw`.
Create these only in the development database and remove them afterward.

With Haystack exposed at `https://app.hey.localhost:<port>` and its certificate
trusted by both the CLI and the isolated test browser:

```bash
make build
(cd tests/smoke && HEY_SMOKE_STRICT=1 go test -tags verification -v -count=1 -run '^TestCookieRemovalLive')
```

Set `HEY_SMOKE_BASE_URL` to that HTTPS origin. The check first proves a cookie-only
write without fetch metadata is refused, then approves, denies and approves the
fixture through the OAuth-authenticated CLI, reading each result independently in
the browser. The fixture is left approved for explicit cleanup. It also establishes
a real `hey watch` subscription. This proves WebSocket sign-in, not end-to-end
mail-change delivery or TUI rendering; reconnect/header rotation is checked in
`internal/cable`.

## Sender selection

`TestSenderSelection` checks new messages, `reply`, and `compose --thread-id`, by
email and sender ID, both direct sends and draft → send. It checks dry runs,
account-boundary refusals, unchanged defaults and recipients, the browser's saved
From selection, saved messages, and the actual SMTP `From` header after Undo Send.

It requires:

- A non-default custom-domain sender, explicitly selected with `HEY_SMOKE_FROM`.
- A different default sender in that account, plus another accessible linked
  account for the account-boundary checks.
- Local SMTP capture using [Mailpit](https://mailpit.axllent.org/), with its HTTP URL
  in `HEY_SMOKE_MAILPIT_URL`.
- `HEY_SMOKE_SENDER_ACCOUNT` when the chosen address belongs to multiple accounts.

**Use development only.** Confirm both senders use local SMTP, not an external
SMTP/Gmail/Outlook integration. Haystack's development relay settings normally
point to `localhost:1025`. The test addresses recipients at `example.com` and
`example.org`; no mail should leave the machine. It does not change the identity's
default sender.

For example, start a temporary local capture:

```bash
docker run -d --rm --name hey-sender-mailpit \
  -p 127.0.0.1:1025:1025 -p 127.0.0.1:8025:8025 axllent/mailpit
```

The seeded `david@hey.com` user is linked to `david@basecamp.com`. To provision a
local configured sender in both accounts, run this in the **Haystack** checkout:

```bash
bin/rails runner - <<'RUBY'
abort "development only" unless Rails.env.development?
user = User.find_by!(canonical_email_address: "david@hey.com")
user.identity.users.each do |linked_user|
  contact = linked_user.account.contacts.find_by(email_address: "billing@example.org")
  unless contact
    contact = linked_user.account.contacts.create!(
      name: "Billing", email_address: "billing@example.org", contactable: Extenzion.new)
    contact.extenzion.accept(linked_user.contact)
  end
  abort "sender must be a local extension" unless contact.extenzion? && contact.external_accounts.empty?
end
puts "HEY_SMOKE_SENDER_ACCOUNT=#{user.account_id}"
RUBY
```

This adds development fixture data, not server code. Start Haystack with `bin/dev`,
then use the account ID printed above:

```bash
export HEY_SMOKE_FROM=billing@example.org
export HEY_SMOKE_SENDER_ACCOUNT=88690000 # use the ID printed by your checkout
export HEY_SMOKE_MAILPIT_URL=http://127.0.0.1:8025
HEY_SMOKE_STRICT=1 make test-smoke
```

For just sender verification:

```bash
make build
(cd tests/smoke && HEY_SMOKE_STRICT=1 go test -v -count=1 -run '^TestSenderSelection$')
```

The sender test trashes its uniquely named threads on cleanup and removes unsent
drafts if a check fails. The configured senders remain available for subsequent
runs. Captured mail stays in Mailpit until the capture is removed. Stop the capture
with `docker stop hey-sender-mailpit`.

A passing capture check proves the generated SMTP message uses the selected From.
It does not prove public delivery, SPF, DKIM, or DMARC.
