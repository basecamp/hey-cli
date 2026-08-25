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
