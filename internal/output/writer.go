package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/term"
)

type Format int

const (
	FormatAuto Format = iota
	FormatJSON
	FormatStyled
	FormatQuiet
	FormatIDs
	FormatCount
	FormatMarkdown
)

type Options struct {
	Format Format
	Stdout io.Writer
	Stderr io.Writer
}

type Writer struct {
	opts Options
}

func New(opts Options) *Writer {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	return &Writer{opts: opts}
}

func (w *Writer) EffectiveFormat() Format {
	if w.opts.Format != FormatAuto {
		return w.opts.Format
	}
	if isTTY() {
		return FormatStyled
	}
	return FormatJSON
}

func (w *Writer) IsStyled() bool {
	return w.EffectiveFormat() == FormatStyled
}

func (w *Writer) OK(data any, opts ...ResponseOption) error {
	format := w.EffectiveFormat()

	switch format {
	case FormatQuiet:
		return w.writeQuiet(data)
	case FormatIDs:
		return w.writeIDs(data)
	case FormatCount:
		return w.writeCount(data)
	case FormatMarkdown:
		return w.writeMarkdown(data)
	default:
		return w.writeJSON(data, opts...)
	}
}

func (w *Writer) Err(err error) {
	e := AsError(err)
	format := w.EffectiveFormat()

	if format == FormatStyled {
		msg := "Error: " + e.Message
		if e.Hint != "" {
			msg += "\n" + e.Hint
		}
		fmt.Fprintln(w.opts.Stderr, msg)
		return
	}

	resp := ErrorResponse{
		OK:    false,
		Error: e.Message,
		Code:  e.Code,
		Hint:  e.Hint,
	}
	enc := json.NewEncoder(w.opts.Stderr)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
}

func (w *Writer) writeJSON(data any, opts ...ResponseOption) error {
	resp := Response{OK: true, Data: data}
	for _, opt := range opts {
		opt(&resp)
	}

	enc := json.NewEncoder(w.opts.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

func (w *Writer) writeQuiet(data any) error {
	enc := json.NewEncoder(w.opts.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func (w *Writer) writeIDs(data any) error {
	items, ok := toSlice(data)
	if !ok {
		return ErrUsage("--ids-only requires list data")
	}
	extracted := 0
	for _, item := range items {
		if id := extractID(item); id != "" {
			fmt.Fprintln(w.opts.Stdout, id)
			extracted++
		}
	}
	if len(items) > 0 && extracted == 0 {
		return ErrUsage("--ids-only: no 'id' field found in results")
	}
	return nil
}

func (w *Writer) writeCount(data any) error {
	items, ok := toSlice(data)
	if !ok {
		return ErrUsage("--count requires list data")
	}
	fmt.Fprintln(w.opts.Stdout, len(items))
	return nil
}

func (w *Writer) writeMarkdown(data any) error {
	items, ok := toSlice(data)
	if !ok {
		// Single item: render as key-value pairs
		m := toMap(data)
		if m == nil {
			return w.writeQuiet(data)
		}
		keys := sortedKeys(m)
		for _, k := range keys {
			fmt.Fprintf(w.opts.Stdout, "**%s:** %v\n", k, m[k])
		}
		return nil
	}

	if len(items) == 0 {
		fmt.Fprintln(w.opts.Stdout, "(no results)")
		return nil
	}

	first := toMap(items[0])
	if first == nil {
		return w.writeQuiet(data)
	}

	headers := sortedKeys(first)

	// Header row
	var sb strings.Builder
	sb.WriteString("|")
	for _, h := range headers {
		sb.WriteString(" ")
		sb.WriteString(h)
		sb.WriteString(" |")
	}
	sb.WriteString("\n|")
	for range headers {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")

	// Data rows
	for _, item := range items {
		m := toMap(item)
		sb.WriteString("|")
		for _, h := range headers {
			v := ""
			if m != nil {
				if val, ok := m[h]; ok {
					v = fmt.Sprintf("%v", val)
				}
			}
			sb.WriteString(" ")
			sb.WriteString(v)
			sb.WriteString(" |")
		}
		sb.WriteString("\n")
	}

	fmt.Fprint(w.opts.Stdout, sb.String())
	return nil
}

func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func toSlice(data any) ([]any, bool) {
	switch v := data.(type) {
	case []any:
		return v, true
	default:
		// Use JSON round-trip for typed slices
		b, err := json.Marshal(data)
		if err != nil {
			return nil, false
		}
		var arr []any
		if err := json.Unmarshal(b, &arr); err != nil {
			return nil, false
		}
		return arr, true
	}
}

func toMap(item any) map[string]any {
	if m, ok := item.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(item)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func extractID(item any) string {
	m, ok := item.(map[string]any)
	if !ok {
		b, err := json.Marshal(item)
		if err != nil {
			return ""
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return ""
		}
	}
	if id, ok := m["id"]; ok {
		switch v := id.(type) {
		case float64:
			return strconv.FormatInt(int64(v), 10)
		case json.Number:
			return v.String()
		case string:
			return v
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

func TruncationNotice(shown, total int) string {
	if total <= shown {
		return ""
	}
	return fmt.Sprintf("Showing %d of %d results. Use --all to see everything.", shown, total)
}

func TruncationNoticeWithCmd(shown, total int, cmd string) string {
	if total <= shown {
		return ""
	}
	return fmt.Sprintf("Showing %d of %d results. %s", shown, total, cmd)
}

func FormatFromFlags(jsonFlag, quiet, idsOnly, count, markdown, styled, agent bool) Format {
	switch {
	case count:
		return FormatCount
	case idsOnly:
		return FormatIDs
	case quiet:
		return FormatQuiet
	case jsonFlag || agent:
		return FormatJSON
	case markdown:
		return FormatMarkdown
	case styled:
		return FormatStyled
	default:
		return FormatAuto
	}
}

func NormalizeJSONNumbers(data []byte) (any, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}
