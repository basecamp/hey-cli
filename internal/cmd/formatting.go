package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

var colorDisabled bool

func init() {
	_, noColor := os.LookupEnv("NO_COLOR")
	colorDisabled = noColor || !term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec // G115: fd fits in int on all supported platforms
}

type table struct {
	columnWidths map[int]int
	rows         [][]string
}

func newTable() *table {
	return &table{
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
			fmt.Printf("%s%s  ", cellStyle.format(cell), strings.Repeat(" ", pad))
		}
		fmt.Println()
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

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printRawJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		_, _ = os.Stdout.Write(data)
		fmt.Println()
		return nil //nolint:nilerr // fallback to raw output when JSON is invalid
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
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

func readStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("could not read from stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func extractMutationInfo(data []byte) string {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return ""
	}

	var parts []string
	for _, key := range []string{"id", "topic_id", "entry_id"} {
		v, ok := obj[key]
		if !ok || v == nil {
			continue
		}
		switch v := v.(type) {
		case float64:
			parts = append(parts, fmt.Sprintf("%s: %d", key, int64(v)))
		default:
			parts = append(parts, fmt.Sprintf("%s: %v", key, v))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
