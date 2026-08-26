package cmd

import (
	"fmt"
	"io"
)

// writeHTMLFragment writes one body exactly as HEY served it, with the CLI's
// trailing record newline. An absent body has no fragment and writes nothing.
func writeHTMLFragment(w io.Writer, body string) error {
	if body == "" {
		return nil
	}
	_, err := fmt.Fprintln(w, body)
	return err
}

// writeExactHTMLFragment writes one body byte-for-byte as HEY served it. Draft
// HTML is also the source for exact local exports, so it carries no record
// newline beyond any newline already present in the stored body.
func writeExactHTMLFragment(w io.Writer, body string) error {
	_, err := io.WriteString(w, body)
	return err
}
