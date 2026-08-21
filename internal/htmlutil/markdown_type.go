package htmlutil

// Markdown is Markdown that ToMarkdown produced: every context serialized on its own
// terms, no control character left in it. It is the only kind of Markdown
// markdown.Render accepts, and nothing but ToMarkdown can make one — the field is
// unexported and there is no constructor — so a body from anywhere else cannot reach a
// terminal renderer by being a string that happens to look like Markdown. It marshals to
// JSON as the string it holds, which is what --json carries.
type Markdown struct {
	text string
}

// String is the Markdown text, for a sink that writes Markdown rather than rendering it.
func (m Markdown) String() string { return m.text }

// IsEmpty reports Markdown with nothing in it; IsZero is the same, for `omitzero`.
func (m Markdown) IsEmpty() bool { return m.text == "" }

func (m Markdown) IsZero() bool { return m.text == "" }

// MarshalText writes the Markdown as a JSON string.
func (m Markdown) MarshalText() ([]byte, error) { return []byte(m.text), nil }
