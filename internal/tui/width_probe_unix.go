//go:build unix

package tui

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// calibrateWidths asks the terminal how wide it draws the probe clusters and adopts its
// answers. It runs before the Bubble Tea program owns the terminal, in raw mode so the
// reports are readable, and gives up quietly — keeping the pessimistic defaults — when
// either end is not a terminal or the answers do not arrive in time. The reads poll with
// a deadline rather than blocking, so a terminal that never answers cannot stall the TUI
// or leave a reader stealing its input.
func calibrateWidths(in, out *os.File) {
	inFd, outFd := int(in.Fd()), int(out.Fd()) //nolint:gosec // G115: fds fit in int
	if !term.IsTerminal(inFd) || !term.IsTerminal(outFd) {
		return
	}
	restore, err := term.MakeRaw(inFd)
	if err != nil {
		return
	}
	defer term.Restore(inFd, restore) //nolint:errcheck

	if _, err := out.WriteString(probeRequest()); err != nil {
		return
	}
	if probed, ok := deriveWidths(readCursorReports(in, len(widthProbes), 300*time.Millisecond)); ok {
		widths = probed
	}
}

func readCursorReports(in *os.File, count int, budget time.Duration) []int {
	deadline := time.Now().Add(budget)
	buf := make([]byte, 0, 256)
	chunk := make([]byte, 64)
	for {
		columns := parseCursorReports(buf)
		if len(columns) >= count {
			return columns[:count]
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return columns
		}
		fds := []unix.PollFd{{Fd: int32(in.Fd()), Events: unix.POLLIN}} //nolint:gosec // G115: fd fits in int32
		ready, err := unix.Poll(fds, int(remaining.Milliseconds())+1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || ready == 0 {
			return columns
		}
		n, err := in.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			return parseCursorReports(buf)
		}
	}
}
