package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/itchyny/gojq"
	"golang.org/x/term"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
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
	// FormatHTML is the raw writer behind --html: the original HTML of the one thing a
	// command reads, written to a pipe or a file. It carries no envelope, so OK refuses
	// it; the commands that support it write the HTML themselves.
	FormatHTML
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

// RequestedFormat reports the format the caller asked for by flag, FormatAuto when
// they asked for nothing and the writer is left to read the terminal.
func (w *Writer) RequestedFormat() Format {
	return w.opts.Format
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
	data = normalizeData(data)
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
	case FormatHTML:
		return apierr.ErrUsage("--html is not supported by this output")
	default:
		return w.writeJSON(data, opts...)
	}
}

// normalizeData settles what a typed nil means to the envelope. `omitempty` drops a
// nil interface but neither a nil slice nor an interface holding a nil pointer, so an
// empty listing would otherwise marshal as `"data": null` and the documented
// `--jq '.data[]'` recipe would fail on it, and a mutation the API answered with
// nothing would report `"data": null` rather than leaving the key out.
func normalizeData(data any) any {
	value := reflect.ValueOf(data)
	switch {
	case value.Kind() == reflect.Slice && value.IsNil():
		return reflect.MakeSlice(value.Type(), 0, 0).Interface()
	case value.Kind() == reflect.Pointer && value.IsNil():
		return nil
	default:
		return data
	}
}

func (w *Writer) Err(err error) {
	e := apierr.AsError(err)
	format := w.EffectiveFormat()

	// An --html run is a person redirecting to a file: the error reads as text on
	// stderr, as it does on a terminal, rather than as a JSON envelope. Either way the
	// message and hint may have come from the server, so they are sanitized first.
	if format == FormatStyled || format == FormatHTML {
		msg := "Error: " + terminal.Sanitize(e.Message)
		if e.Hint != "" {
			msg += "\n" + terminal.Sanitize(e.Hint)
		}
		fmt.Fprintln(w.opts.Stderr, msg)
		return
	}

	resp := ErrorResponse{
		OK:    false,
		Error: e.Message,
		Code:  e.Code,
		Hint:  e.Hint,
		Meta:  e.Meta,
	}
	_ = writeIndentedJSON(w.opts.Stderr, resp)
}

func (w *Writer) writeJSON(data any, opts ...ResponseOption) error {
	resp := Response{OK: true, Data: data}
	for _, opt := range opts {
		opt(&resp)
	}
	return writeIndentedJSON(w.opts.Stdout, resp)
}

// writeIndentedJSON writes one value the way json.Encoder would, indented and
// newline-terminated, with the C1 controls escaped on the way.
func writeIndentedJSON(out io.Writer, v any) error {
	data, err := MarshalIndentJSON(v)
	if err != nil {
		return err
	}
	_, err = out.Write(append(data, '\n'))
	return err
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

// writeJQResult writes one jq result. A string result is written raw, as `jq -r`
// writes it: on a terminal it is sanitized first, and on a pipe it is the value
// itself, control characters included, because the bytes are the value and the
// consumer is a program. Anything else is JSON, with the C1 controls escaped.
func (w *Writer) writeJQResult(result any, tty bool) error {
	if tty {
		result = sanitizeJSONValue(result)
	}
	if text, ok := result.(string); ok {
		_, err := fmt.Fprintln(w.opts.Stdout, text)
		return err
	}

	if err := writeIndentedJSON(w.opts.Stdout, result); err != nil {
		return ErrJQRuntime(fmt.Errorf("encode result: %w", err))
	}
	return nil
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
		return terminal.Sanitize(value)
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
		if terminal.Sanitize(key) == key {
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

func (w *Writer) writeQuiet(data any) error {
	return writeIndentedJSON(w.opts.Stdout, data)
}

func (w *Writer) writeIDs(data any) error {
	items, ok := toSlice(data)
	if !ok {
		return apierr.ErrUsage("--ids-only requires list data")
	}
	extracted := 0
	for _, item := range items {
		if id := extractID(item); id != "" {
			fmt.Fprintln(w.opts.Stdout, id)
			extracted++
		}
	}
	if len(items) > 0 && extracted == 0 {
		return apierr.ErrUsage("--ids-only: no 'id' field found in results")
	}
	return nil
}

func (w *Writer) writeCount(data any) error {
	items, ok := toSlice(data)
	if !ok {
		return apierr.ErrUsage("--count requires list data")
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
	value = terminal.Sanitize(value)
	value = escapeMarkdownTablePipes(value)
	value = strings.ReplaceAll(value, "\t", " ")
	return strings.ReplaceAll(value, "\n", "<br>")
}

func escapeMarkdownTablePipes(value string) string {
	var escaped strings.Builder
	for index := 0; index < len(value); {
		start := index
		for index < len(value) && value[index] == '\\' {
			index++
		}
		if index < len(value) && value[index] == '|' {
			escaped.WriteString(strings.Repeat(`\`, 2*(index-start)+1))
			escaped.WriteByte('|')
			index++
			continue
		}
		escaped.WriteString(value[start:index])
		if index < len(value) {
			escaped.WriteByte(value[index])
			index++
		}
	}
	return escaped.String()
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
