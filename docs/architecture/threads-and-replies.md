# Threads, replies and recipients

## HTML content

**Nothing scrapes a web page any more.** HEY grew JSON for every endpoint the CLI reads,
the SDK dropped its own scrapers, and the last three scrapes here are gone. Do not answer
"the JSON looks incomplete" by parsing a web page — check whether the SDK already has a
typed operation, and if HEY does not serve JSON for what you need, add it to the SDK
rather than working around it here. `sdk.GetHTML` has no callers left; if you find yourself
reaching for it, that is the signal to stop.

Two facts about the typed reads are worth keeping,
because both were mis-stated here before:

- **`Topics.Get` does not carry bodies.** It carries the topic's first page of entries as
  summaries; the SDK says so at `generated/client.gen.go`. A body comes from
  `Messages().Get` per entry. `internal/threadload` is the one place that read is done
  for the CLI: it walks `Topics().GetEntriesPage` newest first and then fans out over
  `Messages().Get`, within literal limits (`threadload.DefaultLimits`: pages, entries,
  message requests, retries, concurrency, retained bytes, deadline), and reports per
  entry whether the body was `hydrated`, `bodyless`, `not_requested`, `over_limit` or
  `failed`. Hydration and retained message metadata are the caller's decisions:
  `hey thread read --count` and `--ids-only` read the index only, JSON thread output
  retains both ordinary To/CC/BCC recipients and inbound `received_via` delivery records,
  HTML retains only the recipients it renders, and body-only callers such as the TUI and
  `hey attachment list` retain neither kind. Delivery records keep HEY's exact account
  address (including plus tags and catch-all aliases) and an optional identity-only
  contact; all of those retained strings are charged atomically with the message against
  the byte budget. A thread that could be read only in part is refused without
  `--allow-partial`; with it the notice says what is missing (`threadNotice` in
  `internal/cmd/thread_source.go`). The package takes a `Source` interface rather than the
  SDK; `threadload.NewSDKSource` is the adapter both the CLI and the TUI hand it, and the TUI's `mailView.fetchTopic` reads through it
  too — every page, oldest first, unread bodies marked and a notice for a partial read —
  and then fetches inline images within `imageBudget` (`internal/tui/image_budget.go`:
  requests, bytes, deadline, de-dup, clean HEY blob paths only — every request is charged
  to the count whatever it answers, and each fetch is told what is left of the bytes so an
  oversized image stops on the wire). A thread read only in part keeps its notice on
  screen for as long as it is open (`mailView.threadNotice`) and is not marked seen by
  being opened; the seen key still is. HEY serves the entry index newest first; the
  loader admits newest first and reverses once, so a thread reads oldest first.

  The entry index is geared_pagination like every other list here, so its page is a cursor
  out of the `Link` header and a number is answered with the first page forever —
  `Topics().GetEntries` throws that header away, which is what `GetEntriesPage` exists
  for.

  The SDK bounds its own reads: its transport caps JSON and HTML response bodies —
  success and error alike, decompressed — at `hey.DefaultMaxResponseBodyBytes` (16 MiB;
  `hey.WithMaxResponseBodyBytes` to change it, and there is no opt-out — a zero or
  negative value means the default), leaving blobs to the SDK's own streaming and caps.
  An oversized *error* response keeps its status: the refusal arrives as the `*hey.Error`
  for the status wrapping `hey.ErrResponseTooLarge`, which is why
  `internal/threadload/sdk.go` classifies a failed message read by status before size —
  an oversized 500 is still systemic and an oversized 404 is still just a missing
  message; only an oversized success is `over_limit`.
- **A reply starts from HEY's prefill, with a local fallback.** The SDK's
  `Entries().NewReply` (`GET /entries/{id}/replies/new.json`) answers how a reply starts
  out: its "Re: …" subject and its recipients, with the entry's sender moved onto the To
  line (haystack's `directly_address_sender`) *and* the acting user's own addresses,
  aliases, catch-alls and redelivery contacts removed — the exclusion this CLI cannot
  compute locally. Both reply paths — `hey reply` in `internal/cmd/thread_reply.go`,
  and the TUI's reply form via `loadReplyContext` in `internal/tui/compose.go` — ask
  the shared `mail.ReplyPrefillFromServer` (`internal/mail/reply_prefill.go`) first and
  fall back to their local computation (`recipientsForReplyTo` plus the derived subject)
  on a failed read or an empty recipient answer, which a thread with yourself produces;
  the prefill's subject survives that recipient fallback. Extend
  `mail.ReplyPrefillFromServer` rather than reimplementing HEY's exclusion rules in
  each caller.

  The entry a reply — or a forward — answers is not the thread's last entry but HEY's
  `Topic#last_replyable_entry`: `mail.ReplyTarget` in `internal/mail/reply_target.go`,
  which every reply and forward path asks. On HEY for Domains a thread also holds notes
  (`kind: "comment"`) and share notices (`kind: "access_notice"`), visible to everyone
  with access to it and never emailed; answering one addresses the reply to the teammate
  who wrote it. Only `message`, `announcement` and `sign_up_message` are replyable, and a
  thread with none is refused. The text formats and the TUI head a note or share notice
  by what it is (`mail.InternalEntryLabel`) rather than with a From line.

`internal/htmlutil` provides `ToMarkdown` (HTML→Markdown), `ToText` (HTML→plain text),
`ExtractImageURLs` and `ExtractAttachments`, which are presentation helpers rather than
scrapers and are staying. `ToMarkdown` is also where email content stops being trusted:
see [terminal-safety.md](terminal-safety.md). The CLI's output shapes
are local to the commands that print them, mail's shapes are `internal/mail`'s `Source`,
`Posting` and `Entry`, and `Contact`, `Calendar` and `Recording` are plain view types
declared next to the Contacts and Calendar sections that read them.
HEY uses the Trix editor with `<figure data-trix-attachment="{...}">` for
attachments — image URLs in those attributes are relative paths requiring authentication
via `sdk.Get`.

## Recipients are suggested from HEY's composer list

The To, Cc and Bcc fields of every compose form suggest people from
`Contacts().Addressable` — `/autocompletable/contacts/addressable`, the list HEY's web
composer uses, asked for `include_self` as the web asks. It is not `Contacts().List`: that
is a paged search index with no groups, no "Everyone at" and no recently-addressed order.
HEY answers the whole list with no query, so `mailView.loadRecipients` reads it in the
background whenever a composer opens (the SDK's ETag cache makes a repeat a 304) and
`matchRecipients` filters it in memory on every keystroke, against lower-cased copies made
once when the list arrives. Keep it that way: a keystroke must never wait on the network.
The finished list is routed to Mail from any section (`tui.go`), because it can arrive
after the composer has sent. A group is matched by its name only, and picking it writes its
addresses, since HEY takes addresses rather than a group's name.

Esc on a composer holding more than it opened with asks before it closes, and `s` keeps
the message as a HEY draft — `Messages().CreateDraft`, or `Entries().CreateReplyDraft` for a
reply — which is the TUI's version of the web composer never losing a message.
