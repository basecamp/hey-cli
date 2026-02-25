package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

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
