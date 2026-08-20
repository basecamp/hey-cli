package markdown

import "charm.land/glamour/v2/ansi"

// terminalStyle styles rendered Markdown with ANSI colors rather than the
// fixed 256-color palettes glamour ships, so email bodies pick up the user's
// terminal theme the same way internal/tui/styles.go does.
//
// The numbers are ANSI slots, not colors: 12 is bright blue (emphasis), 14 is
// bright cyan (links), 11 is bright yellow (code). Secondary text is Faint
// rather than bright black, for the reason styleMuted gives in
// internal/tui/styles.go: plenty of themes render bright black almost
// invisibly, while a dimmed foreground stays legible everywhere.
//
// Headings do not yet follow the TUI's themed accent (colorPrimary), which
// arrived with the Omarchy theme overlay. Doing that means handing the color in
// from the TUI, since this package cannot import it.
var terminalStyle = ansi.StyleConfig{
	Document: ansi.StyleBlock{
		Margin: uintPointer(0),
	},
	// Quotes carry no color of their own: glamour styles the padding it adds
	// out to the wrap width, and a colored run of spaces cannot be trimmed
	// away afterwards.
	BlockQuote: ansi.StyleBlock{
		Indent:      uintPointer(1),
		IndentToken: stringPointer("│ "),
	},
	List: ansi.StyleList{
		LevelIndent: 2,
	},
	Heading: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockSuffix: "\n",
			Color:       stringPointer("12"),
			Bold:        boolPointer(true),
		},
	},
	// No "## " prefixes: a heading in an email is prose, not a document
	// outline, and leftover hash marks read as markup that failed to render.
	H1: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{Underline: boolPointer(true)},
	},
	Strikethrough: ansi.StylePrimitive{
		CrossedOut: boolPointer(true),
	},
	Emph: ansi.StylePrimitive{
		Italic: boolPointer(true),
	},
	Strong: ansi.StylePrimitive{
		Bold: boolPointer(true),
	},
	HorizontalRule: ansi.StylePrimitive{
		Faint:  boolPointer(true),
		Format: "\n────────\n",
	},
	Item:        ansi.StylePrimitive{BlockPrefix: "• "},
	Enumeration: ansi.StylePrimitive{BlockPrefix: ". "},
	Task: ansi.StyleTask{
		Ticked:   "[✓] ",
		Unticked: "[ ] ",
	},
	Link: ansi.StylePrimitive{
		Color:     stringPointer("14"),
		Underline: boolPointer(true),
	},
	LinkText: ansi.StylePrimitive{
		Color: stringPointer("12"),
	},
	Image: ansi.StylePrimitive{
		Color:     stringPointer("14"),
		Underline: boolPointer(true),
	},
	ImageText: ansi.StylePrimitive{
		Faint:  boolPointer(true),
		Format: "Image: {{.text}} →",
	},
	Code: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: " ",
			Suffix: " ",
			Color:  stringPointer("11"),
		},
	},
	CodeBlock: ansi.StyleCodeBlock{
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Faint: boolPointer(true)},
			Margin:         uintPointer(2),
		},
	},
	Table: ansi.StyleTable{
		CenterSeparator: stringPointer("┼"),
		ColumnSeparator: stringPointer("│"),
		RowSeparator:    stringPointer("─"),
	},
	DefinitionDescription: ansi.StylePrimitive{BlockPrefix: "\n  "},
}

func stringPointer(value string) *string { return &value }

func boolPointer(value bool) *bool { return &value }

func uintPointer(value uint) *uint { return &value }
