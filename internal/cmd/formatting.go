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

	"github.com/basecamp/hey-cli/internal/output"
)

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

func (t *table) addRow(row []string) {
	t.updateColumnWidths(row)
	t.rows = append(t.rows, row)
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
	plain  style = ""
	bold   style = "1;34"
	italic style = "3;94"
)

func (s style) format(value string) string {
	if s == plain || colorDisabled {
		return value
	}
	return "\033[" + string(s) + "m" + value + "\033[0m"
}

func truncate(s string, maxWidth int) string {
	if runewidth.StringWidth(s) <= maxWidth {
		return s
	}
	return runewidth.Truncate(s, maxWidth, "...")
}

func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) //nolint:gosec // G115: fd fits in int
}

func stdoutIsTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec // G115: fd fits in int
}

func readStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", output.ErrUsage(fmt.Sprintf("could not read from stdin: %v", err))
	}
	return strings.TrimSpace(string(data)), nil
}

func isDateArg(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
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
