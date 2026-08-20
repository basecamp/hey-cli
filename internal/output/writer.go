package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/itchyny/gojq"
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
	Format   Format
	Stdout   io.Writer
	Stderr   io.Writer
	JQFilter string
}

type Writer struct {
	opts Options
	jq   *gojq.Code
}

func New(opts Options) *Writer {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	w := &Writer{opts: opts}
	if opts.JQFilter != "" {
		w.jq, _ = compileJQ(opts.JQFilter)
	}
	return w
}

func (w *Writer) EffectiveFormat() Format {
	if w.opts.Format != FormatAuto {
		return w.opts.Format
	}
	if isTTY(w.opts.Stdout) {
		return FormatStyled
	}
	return FormatJSON
}

func (w *Writer) IsStyled() bool {
	return w.EffectiveFormat() == FormatStyled
}

func (w *Writer) OK(data any, opts ...ResponseOption) error {
	format := w.EffectiveFormat()
	if w.opts.JQFilter != "" {
		resp := Response{OK: true, Data: data}
		for _, opt := range opts {
			opt(&resp)
		}
		if format == FormatQuiet {
			return w.writeJQ(resp.Data)
		}
		return w.writeJQ(resp)
	}

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
	_ = enc.Encode(resp)
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

func (w *Writer) writeJQ(target any) error {
	code := w.jq
	if code == nil {
		var err error
		code, err = compileJQ(w.opts.JQFilter)
		if err != nil {
			return ErrJQValidation(err)
		}
	}

	raw, err := json.Marshal(target)
	if err != nil {
		return ErrJQRuntime(fmt.Errorf("encode input: %w", err))
	}
	input, err := NormalizeJSONNumbers(raw)
	if err != nil {
		return ErrJQRuntime(fmt.Errorf("decode input: %w", err))
	}

	iter := code.Run(input)
	for {
		result, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, ok := result.(error); ok {
			return ErrJQRuntime(err)
		}
		if err := w.writeJQResult(result, isTTY(w.opts.Stdout)); err != nil {
			return err
		}
	}
}

func (w *Writer) writeJQResult(result any, tty bool) error {
	if tty {
		result = sanitizeJSONValue(result)
	}
	if text, ok := result.(string); ok {
		_, err := fmt.Fprintln(w.opts.Stdout, text)
		return err
	}

	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ErrJQRuntime(fmt.Errorf("encode result: %w", err))
	}
	_, err = fmt.Fprintln(w.opts.Stdout, string(raw))
	return err
}

func compileJQ(filter string) (*gojq.Code, error) {
	query, err := gojq.Parse(filter)
	if err != nil {
		return nil, err
	}
	return gojq.Compile(query, gojq.WithEnvironLoader(os.Environ))
}

// ValidateJQFilter confirms that a built-in jq expression is ready to run.
func ValidateJQFilter(filter string) error {
	if filter == "" {
		return nil
	}
	if _, err := compileJQ(filter); err != nil {
		return ErrJQValidation(err)
	}
	return nil
}

func sanitizeJSONValue(value any) any {
	switch value := value.(type) {
	case string:
		return sanitizeTerminal(value)
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = sanitizeJSONValue(item)
		}
		return result
	case map[string]any:
		return sanitizeJSONMap(value)
	default:
		return value
	}
}

func sanitizeJSONMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	var changed []string
	for key, item := range value {
		if sanitizeTerminal(key) == key {
			result[key] = sanitizeJSONValue(item)
		} else {
			changed = append(changed, key)
		}
	}

	sort.Strings(changed)
	for _, key := range changed {
		quoted := strconv.Quote(key)
		name := quoted[1 : len(quoted)-1]
		for {
			if _, exists := result[name]; !exists {
				break
			}
			name = strconv.Quote(name)
		}
		result[name] = sanitizeJSONValue(value[key])
	}
	return result
}

func sanitizeTerminal(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		default:
			return r
		}
	}, value)
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
			fmt.Fprintf(w.opts.Stdout, "**%s:** %s\n", markdownCell(k), markdownCell(fmt.Sprintf("%v", m[k])))
		}
		return nil
	}

	if len(items) == 0 {
		fmt.Fprintln(w.opts.Stdout, "(no results)")
		return nil
	}

	if toMap(items[0]) == nil {
		return w.writeQuiet(data)
	}

	headerSet := make(map[string]any)
	for _, item := range items {
		for key := range toMap(item) {
			headerSet[key] = nil
		}
	}
	headers := sortedKeys(headerSet)

	// Header row
	var sb strings.Builder
	sb.WriteString("|")
	for _, h := range headers {
		sb.WriteString(" ")
		sb.WriteString(markdownCell(h))
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
			sb.WriteString(markdownCell(v))
			sb.WriteString(" |")
		}
		sb.WriteString("\n")
	}

	fmt.Fprint(w.opts.Stdout, sb.String())
	return nil
}

func markdownCell(value string) string {
	value = sanitizeTerminal(value)
	value = strings.ReplaceAll(value, "|", `\|`)
	value = strings.ReplaceAll(value, "\t", " ")
	return strings.ReplaceAll(value, "\n", "<br>")
}

func isTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(int(f.Fd())) //nolint:gosec // G115: fd fits in int on all supported platforms
	}
	return false
}

func toSlice(data any) ([]any, bool) {
	switch v := data.(type) {
	case []any:
		return v, true
	default:
		// Use JSON round-trip for typed slices; UseNumber preserves integer precision
		b, err := json.Marshal(data)
		if err != nil {
			return nil, false
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var arr []any
		if err := dec.Decode(&arr); err != nil {
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
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
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
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
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
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}
