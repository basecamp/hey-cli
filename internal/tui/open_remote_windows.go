//go:build windows

package tui

import (
	"errors"
	"net"

	tea "charm.land/bubbletea/v2"
)

// ErrNoRunningTUI means no active HEY TUI accepted an open request.
var ErrNoRunningTUI = errors.New("no running HEY TUI")

// OpenInRunningTUI reports that remote opening is unavailable.
func OpenInRunningTUI(string, OpenRequest) error {
	return ErrNoRunningTUI
}

func startOpenListener(string, func(tea.Msg)) (net.Listener, error) {
	return nil, nil
}

func closeOpenListener(net.Listener) {}
