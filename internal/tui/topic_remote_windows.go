//go:build windows

package tui

import (
	"errors"
	"net"

	tea "charm.land/bubbletea/v2"
)

// ErrNoRunningTUI means no active HEY TUI accepted a topic request.
var ErrNoRunningTUI = errors.New("no running HEY TUI")

// OpenTopicInRunningTUI reports that remote topic opening is unavailable.
func OpenTopicInRunningTUI(string, TopicRequest) error {
	return ErrNoRunningTUI
}

func startTopicListener(string, func(tea.Msg)) (net.Listener, error) {
	return nil, nil
}

func closeTopicListener(string, net.Listener) {}
