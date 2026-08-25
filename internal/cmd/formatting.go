package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/markdown"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// dateLayout is how HEY writes and reads a calendar day.
const dateLayout = "2006-01-02"

var colorDisabled bool

func init() {
	_, noColor := os.LookupEnv("NO_COLOR")
	colorDisabled = noColor || !term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec // G115: fd fits in int on all supported platforms
}

type table struct {
	w            io.Writer
	columnWidths map[int]int
	rows         [][]string
}

func newTable(w io.Writer) *table {
	return &table{
		w:            w,
		columnWidths: map[int]int{},
		rows:         [][]string{},
	}
}

// addRow adds a row, sanitizing every cell on the way in: a table is where server
// data lands most often, and a cell is one line by construction.
func (t *table) addRow(row []string) {
	cells := make([]string, len(row))
	for i, cell := range row {
		cells[i] = terminal.SanitizeLine(cell)
	}
	t.updateColumnWidths(cells)
	t.rows = append(t.rows, cells)
}

func (t *table) print() {
	for rownum, row := range t.rows {
		for i, cell := range row {
			cellStyle := plain
			if rownum == 0 {
				cellStyle = italic
			}
			if rownum > 0 && i == 0 {
				cellStyle = bold
			}

			pad := max(t.columnWidths[i]-runewidth.StringWidth(cell), 0)
			fmt.Fprintf(t.w, "%s%s  ", cellStyle.format(cell), strings.Repeat(" ", pad))
		}
		fmt.Fprintln(t.w)
	}
}

func (t *table) updateColumnWidths(row []string) {
	for i, cell := range row {
		w := runewidth.StringWidth(cell)
		if w > t.columnWidths[i] {
			t.columnWidths[i] = w
		}
	}
}

type style string

const (
	plain       style = ""
	bold        style = "1;34"
	italic      style = "3;94"
	italicMuted style = "3;90"
	success     style = "32"
	warning     style = "33"
	muted       style = "90"
)

func (s style) format(value string) string {
	if s == plain || colorDisabled {
		return value
	}
	return "\033[" + string(s) + "m" + value + "\033[0m"
}

func markdownSafeText(value string) string {
	const punctuation = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
	var safe strings.Builder
	for _, r := range terminal.SanitizeLine(value) {
		if strings.ContainsRune(punctuation, r) {
			safe.WriteByte('\\')
		}
		safe.WriteRune(r)
	}
	return safe.String()
}

func truncate(s string, maxWidth int) string {
	if runewidth.StringWidth(s) <= maxWidth {
		return s
	}
	return runewidth.Truncate(s, maxWidth, "...")
}

func threadNoun(count int) string {
	if count == 1 {
		return "thread"
	}
	return "threads"
}

// stdinIsTerminal and stdoutIsTerminal are seam variables (house pattern:
// askLocalConfigTrust) so tests can simulate an interactive terminal.
var stdinIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) //nolint:gosec // G115: fd fits in int
}

var stdoutIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec // G115: fd fits in int
}

// stdoutWidth reports the width to wrap prose to, staying comfortable to read
// on wide terminals and falling back to 80 columns when stdout is not one.
func stdoutWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd())) //nolint:gosec // G115: fd fits in int
	if err != nil || width <= 0 {
		return markdown.DefaultWidth
	}
	return min(width-2, 100)
}

var stderrIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stderr.Fd())) //nolint:gosec // G115: fd fits in int
}

// interactiveStdio reports whether the CLI may prompt: stdin, stdout AND
// stderr are terminals — prompts render on stderr so stdout stays data, and
// an invisible prompt must never sit waiting for input — and the
// HEY_NONINTERACTIVE escape hatch is not engaged.
var interactiveStdio = func() bool {
	return stdinIsTerminal() && stdoutIsTerminal() && stderrIsTerminal() && !config.NonInteractiveEnv()
}

func readStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", apierr.ErrUsage(fmt.Sprintf("could not read from stdin: %v", err))
	}
	return strings.TrimSpace(string(data)), nil
}

func isDateArg(s string) bool {
	_, err := time.Parse(dateLayout, s)
	return err == nil
}

// parseDateArg reads a YYYY-MM-DD date, naming the flag or argument it came from so a
// typo is reported where the user typed it rather than answered with a 404.
func parseDateArg(label, value string) (time.Time, error) {
	parsed, err := time.Parse(dateLayout, value)
	if err != nil {
		return time.Time{}, apierr.ErrUsageHint(
			fmt.Sprintf("invalid %s: %s", label, value),
			"dates are YYYY-MM-DD, for example 2026-01-31")
	}
	return parsed, nil
}

func extractMutationInfo(data []byte) string {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return ""
	}

	type field struct {
		apiKey      string
		displayName string
	}

	fields := []field{
		{apiKey: "id", displayName: "id"},
		{apiKey: "topic_id", displayName: "thread_id"},
		{apiKey: "entry_id", displayName: "entry_id"},
	}

	var parts []string
	for _, f := range fields {
		v, ok := obj[f.apiKey]
		if !ok || v == nil {
			continue
		}
		switch v := v.(type) {
		case float64:
			parts = append(parts, fmt.Sprintf("%s: %d", f.displayName, int64(v)))
		default:
			parts = append(parts, fmt.Sprintf("%s: %v", f.displayName, v))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
