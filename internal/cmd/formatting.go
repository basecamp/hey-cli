package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

var colorDisabled bool

func init() {
	_, noColor := os.LookupEnv("NO_COLOR")
	colorDisabled = noColor || !term.IsTerminal(int(os.Stdout.Fd()))
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

			pad := t.columnWidths[i] - len(cell)
			fmt.Printf("%s%s  ", cellStyle.format(cell), strings.Repeat(" ", pad))
		}
		fmt.Println()
	}
}

func (t *table) updateColumnWidths(row []string) {
	for i, cell := range row {
		if len(cell) > t.columnWidths[i] {
			t.columnWidths[i] = len(cell)
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

func printRawJSON(data []byte) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		os.Stdout.Write(data)
		fmt.Println()
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func readStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("could not read from stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func extractMutationInfo(data []byte) string {
	var obj map[string]interface{}
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

func limitJSONArray(data []byte, limit int) []byte {
	if limit <= 0 {
		return data
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return data
	}
	if len(arr) <= limit {
		return data
	}
	result, err := json.Marshal(arr[:limit])
	if err != nil {
		return data
	}
	return result
}
