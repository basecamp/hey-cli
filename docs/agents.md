# Coding agents and MCP

hey-cli is built to be driven by an agent as readily as by a person: every command that returns
data answers `--json`, the exit codes are stable (`hey help exit-codes`), and `hey commands --json`
describes the whole surface. This page covers the integrations that ship with it: an
agent skill for Claude Code and Codex, an MCP server, and `hey local`, which asks the HEY
app on your computer to act for you.

## Agent skill and Claude Code plugin

hey-cli ships with an embedded agent skill so your coding agent can work with HEY on your
behalf, and a Claude Code plugin (`hey@37signals` from the `basecamp/claude-plugins`
marketplace). The setup wizard connects every detected agent automatically; these commands
manage the integrations on their own:

```bash
hey setup claude    # install the skill and the hey@37signals plugin for Claude Code
hey setup codex     # install the shared skill for Codex
hey skill install   # install the skill only (~/.agents/skills/hey, linked for detected agents)
hey setup agents             # non-interactive: skill + a single detected agent (the installer uses this)
hey setup agents --remove    # remove HEY's managed skills and Claude Code plugin
hey doctor                   # check skill and plugin health per detected agent
```

`hey setup agents` never prompts and never guesses: with several agents detected it installs
the skill only and lists the `hey setup <agent>` choices. `HEY_SETUP_AGENT=claude|codex|all|none`
picks explicitly. `HEY_NONINTERACTIVE=1` disables interactive sign-in for harnesses that
run hey under a pseudo-terminal. The installed skill is refreshed automatically the first
time a new hey release runs.

Codex discovers the shared `~/.agents/skills/hey` skill directly. hey-cli does
not also copy it to `~/.codex/skills/hey`, which would make Codex list the same
skill twice. Setup and version refresh remove that legacy path only when its
ownership marker proves an older hey-cli created it; user-authored files are
left untouched.

hey only ever writes skill directories it owns: each one it creates carries a
`.managed-by-hey-cli` marker, and install, replacement and automatic refresh all refuse a
`hey` skill directory (or symlink) without it — a hand-authored skill at one of those paths
is never overwritten or claimed. `hey doctor` flags an unmanaged baseline and how to adopt it.

### Keychain access from agents

hey keeps your login in the system keyring, which on a Mac is the Keychain. Two things can
keep an agent's commands from reading it, and signing in again fixes neither:

- **Codex's sandbox** hides the keyring from the commands it sandboxes, so a saved login
  reads as missing there; hey's hint says so when `CODEX_SANDBOX` is set. Let Codex run hey
  outside its sandbox: approve its escalation prompt, or allow read-only hey commands with
  `prefix_rule`s in a file under `~/.codex/rules/`.
- **A process outside your Mac's desktop session**, such as one started over SSH, is refused
  by the Keychain: hey fails with `exit status 36` (`errSecInteractionNotAllowed`) and a hint
  that a new login won't help. Codex runs its commands and MCP servers from a background
  server that keeps running after you quit Codex. If that server was started from such a
  session, every hey command and `hey mcp` fails this way, sandboxed or not, and a rules
  file can't help. Quit Codex, run `codex app-server daemon restart` in Terminal on the Mac,
  and start Codex again; `codex --no-daemon` runs without the background server.

`hey mcp` reads the login before it answers the MCP handshake, so a client that can't use
it reports only that the connection closed. The reason is on the server's stderr, which
Codex keeps in its log.

## MCP server

`hey mcp` runs an MCP (Model Context Protocol) server on stdin/stdout, serving HEY
boxes, search, threads, contacts, todos, calendars, and your identity as tools.
Sign it in once, then register it with any MCP client as a stdio server:

```bash
hey auth login --mcp                   # hey mcp's own sign-in (read, write and send)
claude mcp add hey -- hey mcp          # Claude Code
hey mcp --read-only                    # serve only read-only actions
hey mcp --domains boxes,search         # narrow the served tool surface
```

### Its own sign-in

`hey mcp` doesn't use the CLI's credential. `hey auth login --mcp` signs it in as its own
app, "HEY MCP (local)" in HEY's Connected Apps, asking to read, write and send but nothing
more: a model driving `hey mcp` can't reach your settings, forwarding, trash or HEY World,
which the CLI's own sign-in can. The credential is kept apart from the CLI's, in its own
keychain item (or its own entry in `credentials.json` when there is no keychain), and it
refreshes as that app. `HEY_TOKEN` never stands in for it. Without it, `hey mcp` exits and
says to run `hey auth login --mcp`; it doesn't fall back to the CLI's credential, even
read-only, because that one holds full access and `--read-only` is hey mcp's own rule,
not HEY's.

The sign-in uses a device code where HEY offers one (it prints a link and a code to check),
and the browser otherwise. If you uncheck sending on HEY's page, `hey mcp` saves drafts
but serves no way to send them; sign in again to allow it. Without write it serves only
reads. `hey auth status` shows both sign-ins, and `hey auth logout --mcp` revokes hey mcp's
at HEY and removes it, leaving the CLI signed in.

The app's client id is the same in every HEY. `HEY_MCP_CLIENT_ID`, or `mcp_client_ids`
in `~/.config/hey-cli/config.json` keyed by HEY origin
(`{"mcp_client_ids": {"https://app.hey.com": "..."}}`), overrides it. A repository's
`.hey/config.json` can't set it.

Each domain is one gateway tool (`hey_boxes`, `hey_search`, `hey_threads`,
`hey_contacts`, `hey_todos`, `hey_calendar`, `hey_identity`) dispatching actions
derived from the HEY SDK's API model; call an action named `describe` for any
action's parameter schema. Listings with more pages come back as
`{"next_page": cursor, "results": ...}` — pass the cursor back as the action's
`page` parameter. The posting-changes feed's last page comes back as
`{"next_since": ..., "next_v": ..., "results": ...}` — the cursor for the next
incremental poll, passed back as the action's `since` and `v` parameters.
Mutations are never retried automatically: a 429/503 on a write surfaces to
the caller rather than risking a duplicate delivery, so retry a failed write
yourself once you know it did not land. Logs go to stderr — stdout carries
the MCP wire protocol.

### Drafts first, sends confirmed

`create_message`, `create_reply` and `update_message`, the three actions that can send
on HEY's API, always save a draft: the entry's status is
forced to `drafted`, and the entry takes only its recipients (`entry.addressed`). A
call that asks to send (any other `entry.status`), schedule (`entry.scheduled_delivery`
and its date and hour) or triage the thread in the same request (`mark_done`, `pop`,
`scheduled_bubble_up`) is refused before anything reaches HEY. Updating a draft that was
scheduled clears its schedule rather than letting it go out later. A save comes back as
`{"drafted": true, "draft_id": ..., "location": ..., "app_url": ..., "note": ...}`.
`location` is HEY's answer, the draft's resource, which in a browser opens its thread;
`app_url` is the page that opens the draft (`/messages/<id>/edit` on the Location's
origin), left out when the Location is relative or names anything but a draft. HEY acknowledges a
draft with 204 and a Location. Any other answer is reported as an error that says the
mail may have been sent, so check HEY before trying again. A save that fails with no
answer from HEY (a dropped connection, a timeout, a 5xx) may still have been saved, and
its error says to check Drafts before retrying.

Delivery is one action in a tool of its own: `hey_send`'s `send_draft`, with the draft's
id. Each call asks you to confirm the email in your MCP client (an MCP elicitation),
showing the sending address, To, Cc and Bcc (a dash when empty), the subject, attachments,
images and the body's first 1,500 characters, with links' targets beside their text and
every control and invisible character escaped so a display name can't forge a line.
Whatever it can't show (the rest of a long body, what attachments hold, images, hidden
parts or formatting the text leaves out) is named on a `Not shown here` line beside the
draft's link in HEY. What you approve is what goes: the draft is fetched again after you
answer, and if its recipients, sender, subject, body or attachments changed it isn't
sent. Each draft is sent through a `hey mcp` process once; another attempt in the next 24
hours, even after a failure, is refused, and the send is never retried, so an unclear
outcome says to check Sent.

So when you ask your agent to send a draft, it calls `send_draft` straight away: the
confirmation is where you check what goes out, and saying yes in the chat doesn't send
anything. You can still have it show or revise the draft first as you like.

`hey_send` is served only when the sign-in was granted sending, never under `--read-only`,
and is listed only to an MCP client that can show a confirmation. A client that can't is
told to send each draft from its page in HEY (the draft save's `app_url`) or with
`hey draft send <id>`, and a call to `send_draft` from it answers with that link, as
`edit_url`, and sends nothing. `send_draft` also refuses a draft that is already
scheduled, has no recipients, or has a recipient with no address. Nothing
else a draft holds refuses it: how much your agent sends, and to whom, is yours to decide
once you've granted sending.

Earlier releases delivered mail when a call omitted `entry.status`; these actions now
save drafts only. `describe` for the draft actions serves the draft-only schema, and the
`hey_threads` tool description says the same. A person can still send any draft from HEY
or with `hey draft send <draft_id>`.

## The HEY app on your computer: `hey local`

`hey local` talks to the HEY desktop app running on your computer rather than to HEY's
servers. The app runs its own commands over the mail it holds, as you would in its window,
so `hey local` needs no sign-in of its own and nothing it reads is marked seen.

The app lets agents in only while "Let agents use HEY" is on in its Settings (General). It
is off by default. While it is on, the app listens on a Unix socket at
`$XDG_RUNTIME_DIR/hey/agent.sock`, owner-only, and turns away a connection from any other
user account. With the setting off or the app closed there is no socket, and `hey local`
fails with exit 6 and says to turn it on. `--socket <path>` or `HEY_LOCAL_SOCKET` points it
at another socket; with neither, and no absolute `XDG_RUNTIME_DIR`, it asks for one rather
than guess.

```bash
hey local tools                        # what the app offers now, and what each does
hey local imbox                        # the Imbox, marking nothing seen
hey local screener list                # who waits in The Screener, and who was decided
hey local screener deny 42             # screen sender 42 out; undo with approve
hey local drafts                       # the app's drafts, and which it would send now
hey local write --to marcus@example.com --subject "Stint plan" -m "Saturday works"
hey local reply 801 -m "Saturday works"  # a reply to Imbox thread 801, as a draft
hey local send 1731280000123           # ask to send a draft: you confirm it in HEY
hey local call screener.decide --arg sender=42 --arg decision=in
```

Each command the app offers names its effect, and the effect decides what happens:

| Effect | What happens | Commands |
|--------|--------------|----------|
| `read` | Runs, and changes nothing | `imbox.list`, `screener.list`, `drafts.list` |
| `reversible` | Runs at once, and can be undone | `screener.decide`, `draft.write`, `draft.reply` |
| `reaches_people` | Waits in the HEY window until you press Send there | `message.send` |

Nothing that can't be undone, like emptying Trash or Spam, is offered at all. A send never
goes from `hey local`: the app shows you the sending address, recipients, subject, body
and files exactly as they would go, and sends only when you press Send in its window.
`hey local send` then exits 9 and says "Waiting in HEY": neither done (0) nor failed.
It takes the HEY app's own id for a draft, not the id `hey draft list` shows: the id
`hey local write` or `hey local reply` answered, or one `hey local drafts` lists (its `id`,
or its `made_as`, the id it was written as before HEY named it). Its JSON envelope carries `meta.status: "pending"`, the confirmation's id and what you were
shown. No command or flag here can confirm it.

So an agent can carry a message end to end: `hey local write` (or `hey local reply` with a
thread from the Thread column of `hey local imbox`) saves a draft and answers its id, and
`hey local send <id>` asks HEY to send it, which waits for you. `--ids-only` prints the draft's
id. Writing sends nothing, and
each run writes one more draft: nothing here edits or deletes one, so a wrong draft is yours
to delete in HEY. The message is plain text, above your name tag; Cc, Bcc, files and a
schedule are yours to add in HEY. A reply goes to everyone the thread's last message went
to, as HEY's Reply does, beside any draft you have on the thread, never over it.
`hey local drafts` marks each draft HEY would send now as `sendable`; a scheduled one sends
on its own at its time.

The draft commands' refusals say what happened, and nothing was saved or waits:

| Refusal | Exit | Means |
|---------|------|-------|
| `not_an_address` | 1 | Pieces of `--to` read as no address; the message names them |
| `not_saved` | 1 or 7 | Past what HEY takes of one message: too many people, too many files, or too large. 1 for `write`, whose words and `--to` are yours to change; 7 for `reply`, whose recipients, files and quote come from the thread |
| `not_a_sender` | 7 | The draft goes from an address this account no longer sends from; change From in HEY, or write a new draft |
| `disabled` | 1, or 7 when HEY would send none | `send` of a draft HEY won't send now (scheduled, to nobody, a file still uploading, or a forward); the hint lists those it would |
| `unavailable` | 7 | `reply` of a thread the app couldn't read, or a draft `write` or `reply` saved that went before HEY listed it: check `hey local drafts` before writing it again |

`hey local call <command>` runs any command `hey local tools` lists. Arguments come as a
JSON object, as `--arg name=value` pairs, or both, the pairs over the object; a value is
read as JSON where it is JSON, so `sender=42` is a number and `decision=out` a word.
A command the app refuses fails with its reason in the message, and in the JSON error's
`meta.refusal`, which lists the ids it would take where an id was wrong.
A command cut off by signing out of the app or switching accounts while it ran also exits 7,
with `session_ended` in `meta.refusal`: it may have been done, so check in HEY before running
it again.

The wire is plain MCP: JSON-RPC 2.0, one message per line, `initialize`, then `tools/list`
and `tools/call`. The app's door is built for Linux first. A Mac has no socket yet: there,
HEY's sign-in sits behind a Keychain prompt, which a socket any program can reach would let
it skip, so the Mac app will first ask you to approve each agent and check the signature
of the program calling.
