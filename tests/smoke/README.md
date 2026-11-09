# Live CLI smoke tests

Run `HEY_SMOKE_STRICT=1 make test-smoke` from the repository root against dev
Haystack. The suite builds `bin/hey`, signs in with headless Chrome, and uses
isolated CLI configuration and credentials. Strict mode turns skips into failures.
The default server is `http://app.hey.localhost:3003`; see the root `AGENTS.md` for
login and environment overrides.

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
