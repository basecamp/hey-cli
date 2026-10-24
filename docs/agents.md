# Coding agents and MCP

hey-cli is built to be driven by an agent as readily as by a person: every command that returns
data answers `--json`, the exit codes are stable (`hey help exit-codes`), and `hey commands --json`
describes the whole surface. This page covers the two integrations that ship with it: an
agent skill for Claude Code and Codex, and an MCP server.

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
boxes, search, threads, contacts, todos, calendars, and your identity as tools
backed by your signed-in account — the same keychain-stored credentials every other command uses.
Register it with any MCP client as a stdio server:

```bash
claude mcp add hey -- hey mcp          # Claude Code
hey mcp --read-only                    # serve only read-only actions
hey mcp --domains boxes,search         # narrow the served tool surface
```

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

### Drafts, never sends

`hey mcp` never delivers mail. `create_message`, `create_reply` and `update_message`,
the three actions that can send on HEY's API, always save a draft: the entry's status is
forced to `drafted`, and the entry takes only its recipients (`entry.addressed`). A
call that asks to send (any other `entry.status`), schedule (`entry.scheduled_delivery`
and its date and hour) or triage the thread in the same request (`mark_done`, `pop`,
`scheduled_bubble_up`) is refused before anything reaches HEY. Updating a draft that was
scheduled clears its schedule rather than letting it go out later. A save comes back as
`{"drafted": true, "draft_id": ..., "location": ..., "note": ...}`. HEY acknowledges a
draft with 204 and a Location. Any other answer is reported as an error that says the
mail may have been sent, so check HEY before trying again. A save that fails with no
answer from HEY (a dropped connection, a timeout, a 5xx) may still have been saved, and
its error says to check Drafts before retrying.

There is no send action yet. A person reviews the draft and sends it from HEY, or with
`hey draft send <draft_id>`. Earlier releases delivered mail when a call omitted
`entry.status`. An MCP client that relied on that now gets a draft. `describe` for these
actions serves the draft-only schema, and the `hey_threads` tool description says the same.
`--read-only` still drops every write action, drafts included.
