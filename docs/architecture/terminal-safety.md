# Terminal safety

Everything HEY serves was written by somebody else, and a terminal acts on what it is
handed. The model has three layers, and it is worth knowing which one a change touches:

- **Metadata** — a sender, a subject, a filename, a box name, a habit title — goes through
  `terminal.Sanitize`/`SanitizeLine` (`internal/terminal`), which strips escape
  sequences, C0/C1 controls and the `Bidi_Control` set. Its confusable policy (in the
  package doc) strips the format characters that draw nothing (zero width space, word
  joiner, soft hyphen, BOM, …) and combining marks past eight on a base, keeps ZWJ/ZWNJ
  where they join (emoji families, Persian, Devanagari), drops a byte that is not
  UTF-8, leaves NBSP and friends alone, and does not detect homoglyphs — a
  URL-shaped link label is shown beside its destination by `htmlutil.ToMarkdown`
  instead. `markdown.Render` runs the same pass over a whole body, destinations
  included, so `htmlutil` sizes a code delimiter on the content as that pass leaves it
  (`backtickRun`), or a zero width space between two backtick runs would join them
  into a closing delimiter. It is applied once where it
  covers the most: the CLI's `table.addRow` sanitizes its cells, and the TUI's read-only
  models (`mail.NewPosting`, `mail.NewEntry`, `searchMatchToPosting`) are sanitized as
  they are built. The
  editable ones — `sdkContactToModel`, `sdkRecordingToModel` — are deliberately **not**:
  their fields go back through the edit forms, and sanitizing them would rewrite a name
  on an unrelated save. Every view of a contact or a recording sanitizes what it shows. `TestSinksAreSanitized` (`internal/cmd/
  sink_manifest_test.go`, manifest in `testdata/sink_manifest.txt`) fails on a direct
  write of a listed field to a listed sink without a sanitizer. That check is syntactic —
  it does not follow a value through a local variable — so it is an inventory of the
  direct pattern, not a taint analysis; treat a new exemption as something to justify in
  the manifest.
- **Bodies** are `htmlutil.Markdown`, a sealed type only `htmlutil.ToMarkdown` can make
  (unexported field, no constructor, marshals as the string it holds) and the only thing
  `markdown.Render` accepts — so a string from anywhere else cannot reach glamour by
  looking like Markdown. `ToMarkdown` serializes each context on its own terms: prose escapes metacharacters and writes every `&` as `&amp;` (the live
  bug this closed was `&amp;#27;[31m` decoding twice, once in the HTML parser and once in
  the Markdown renderer, into a red terminal); inline code and fences are verbatim inside
  delimiters longer than any run they hold; destinations percent-encode what would end
  them, keep a query string's `&`, and link only http, https, mailto and relative paths.
  Prose goes through `terminal.Sanitize` first and is written in that form; code and destinations strip controls and are measured through the sanitizer (`backtickRun`, `needsPadding`). `markdown.Render` then strips controls and bidi controls from the body, rewrites the
  spans glamour decodes so that its extra entity decode is the identity (and spells
  `\~` and `\=`, which glamour does not unescape, as character references), finds every
  bare link itself, by GFM's rules, before glamour wraps, and hands it over as an
  autolink (`wholeBareLinks` — glamour's linkify matches the escaped source and starts
  only after a space, `*`, `_`, `~` or `(`, so it stopped at `\_`, opened `&amp;`, and
  left `<https://…>` to `LinkifyURLs`, which sees wrapped output), keeps every
  hyperlink's destination ASCII (non-ASCII percent-encoded — the shared ANSI parser
  reads its UTF-8 as C1 controls — and decoded again in the text shown), shows a
  document nested past twenty levels unrendered rather than handing glamour something
  exponential, and checks its own output: anything but SGR and OSC 8 to an allowed scheme strips all
  styling rather than guessing. That last check is a backstop, not the guarantee — an
  injected SGR is byte-identical to one of ours.
- **JSON** is lossless and escapes the C1 controls (`output.MarshalJSON`), which
  `encoding/json` would otherwise write raw. A `--jq` string result on a pipe stays raw,
  as `jq -r` writes it.

`terminal.Sanitize` keeps `\n` and `\t`, because a body, a jq result and a Markdown cell
carry them on purpose. `terminal.SanitizeLine` is for where only one line fits — a table
cell, a mutation confirmation — and turns those two into spaces so nothing can move what
comes after them. Strip a sequence; do not deface it. Replacing the ESC byte with U+FFFD
leaves the payload on screen as debris of somebody else's choosing, and `runewidth` then
measures that debris when it lays out a table, so the column width is theirs to pick.

The Markdown in `--json` is standard CommonMark; what a conformant renderer shows for it
is the literal text and URL the email held — less, in prose, what `terminal.Sanitize`
removes, since prose is written in its sanitized form so that the renderer's own pass
over the body cannot uncover a block marker the escaping did not see (`\u200b# heading`);
the HTML keeps the original. That is what the htmlutil tests assert
through goldmark. Fuzz targets hold the invariants: `FuzzToMarkdownTerminalSafety` in
htmlutil and `FuzzContainment` in markdown.
