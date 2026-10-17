# Email bodies

## Email bodies are Markdown

An email body reaches the CLI and the TUI as HEY's Trix HTML and is converted once, at the
edge, by `htmlutil.ToMarkdown` — for a thread's messages that edge is `mail.NewEntry`.
Everything downstream — `mail.Entry.Body`, `--json`, the TUI viewport — carries Markdown, so
links keep their URLs and headings, lists, quotes, tables and code survive.
`Entry.BodyHTML` keeps the original HTML for `--html` and for
`ExtractImageURLs`/`ExtractAttachments`, which need the attributes the Markdown drops.

`--html` is the one format that writes that HTML out, and it has two shapes. A thread
(`writeThreadHTML` in `internal/cmd/topic.go`) is an HTML5 document: `<!doctype html>`,
`<meta charset="utf-8">`, `<title>Thread N</title>`, then one
`<article id="entry-ID" data-entry-id data-kind data-created-at data-body-state>` per entry, oldest
first, with a `<header>` naming the sender, date and non-empty To, CC and BCC lines
(sanitized, then HTML-escaped) and the entry's HTML verbatim — or nothing, with
`data-body-state` saying why (`bodyless`,
`over_limit`, `failed`, or `hydrated` and empty). A partial thread needs `--allow-partial`
as everywhere else and then ends with `<!-- notice: … -->` before `</body>`
(`htmlCommentSafe` keeps a value from closing the comment), with the notice on stderr too.
A single body — `journal read`, `contacts show`, `contacts note show` (`writeNoteHTML`) —
is a fragment: the body as HEY served it, nothing for an empty one. The difference is
deliberate: a thread has entries to frame; one body gets pasted into something else.
`--stats` is refused with `--html` like every other selector, since there is no envelope to
carry stats. `writeThreadHTML` is exempt in the sink manifest because writing the body raw
*is* the format, and `validateHTMLFlag` keeps it off a terminal.

`internal/markdown` renders that Markdown for a terminal: `Render(md, width)` wraps
glamour, and `LinkifyURLs` wraps bare URLs in OSC 8 so they stay clickable. The style in
`style.go` uses ANSI color slots rather than glamour's fixed palettes, for the same reason
`internal/tui/styles.go` does — the user's terminal theme wins. `Render` never fails
loudly: if glamour cannot be set up it returns the Markdown source, so a styling problem
costs formatting rather than the message.

`ToText` is still right where the result is not shown to anyone — the name tag
`hey bulk-reply` reads out of a draft, for instance.

### Inbound email hides its body in an attachment

`/messages/{id}` serves `content` through haystack's `trix_html_for_rich_text_editing`,
which has to hand Trix something Trix can edit. An HTML email from outside HEY is not
that, so the whole body is wrapped in one `<figure data-trix-attachment>` whose JSON
carries no `filename` and no `url` — just `contentType: "text/html"` and a `content`
string holding the original markup inside `<shadow-content><template>…`.

Treat that figure as a file attachment and the body vanishes: the TUI then shows nothing
but `Entry.Summary`, HEY's ~105-character preview ending in an ellipsis, which reads as a
truncated email. `htmlutil` tells the two apart on `filename` and parses the embedded
markup instead, bounded by `embeddedContentDepthLimit` so a chain of attachments that
each embed another cannot recurse without end. `ExtractImageURLs` looks inside it too,
since that is where an inbound email's inline images are; `ExtractAttachments` does not
list it, because an embedded body is not a downloadable file.
