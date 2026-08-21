package output

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// encoding/json escapes the C0 controls in a string as \u00XX but writes the C1
// controls — U+0080 through U+009F — as the raw UTF-8 bytes they are. A terminal
// reading the JSON, or a tool that prints a field out of it, gets an 8-bit CSI or
// an 8-bit string terminator, which some terminals honor. Escaping them is lossless:
// a JSON decoder yields the identical string, and jq sees exactly what it saw
// before. Only the bytes a reader sees raw change.

// MarshalJSON is json.Marshal with the C1 controls escaped.
func MarshalJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return escapeC1(data), nil
}

// MarshalIndentJSON is json.MarshalIndent with the C1 controls escaped, indented the
// way every envelope is.
func MarshalIndentJSON(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return escapeC1(data), nil
}

// escapeC1 rewrites every UTF-8 encoded U+0080–U+009F as a \u escape. Those two-byte
// sequences can only occur inside a JSON string, since everything structural is ASCII,
// so the rewrite needs no knowledge of where the strings are.
func escapeC1(data []byte) []byte {
	if !bytes.ContainsFunc(data, func(r rune) bool { return r >= 0x80 && r <= 0x9f }) {
		return data
	}
	escaped := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == 0xc2 && i+1 < len(data) && data[i+1] >= 0x80 && data[i+1] <= 0x9f {
			escaped = fmt.Appendf(escaped, `\u%04x`, rune(data[i+1]))
			i++
			continue
		}
		escaped = append(escaped, data[i])
	}
	return escaped
}
