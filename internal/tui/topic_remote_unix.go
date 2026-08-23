//go:build !windows

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ErrNoRunningTUI means no active HEY TUI accepted a topic request.
var ErrNoRunningTUI = errors.New("no running HEY TUI")

const topicSocketFilename = "hey-tui.sock"

func topicSocketPath(instance string) string {
	name := topicSocketFilename
	if suffix := topicInstanceSuffix(instance); suffix != "" {
		name = "hey-tui-" + suffix + ".sock"
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, name)
	}
	uid := "user"
	if current, err := user.Current(); err == nil && current.Uid != "" {
		uid = current.Uid
	}
	return filepath.Join(os.TempDir(), "hey-tui-"+uid+"-"+name)
}

func topicInstanceSuffix(instance string) string {
	var suffix strings.Builder
	for _, char := range instance {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' {
			suffix.WriteRune(char)
		}
		if suffix.Len() == 32 {
			break
		}
	}
	return suffix.String()
}

// OpenTopicInRunningTUI sends a thread to the active named TUI.
func OpenTopicInRunningTUI(instance string, request TopicRequest) error {
	if request.TopicID <= 0 {
		return fmt.Errorf("topic ID must be positive")
	}
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	connection, err := dialer.DialContext(context.Background(), "unix", topicSocketPath(instance))
	if err != nil {
		return ErrNoRunningTUI
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return fmt.Errorf("send topic to HEY TUI: %w", err)
	}
	var response struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(connection, 1024)).Decode(&response); err != nil {
		return fmt.Errorf("read HEY TUI response: %w", err)
	}
	if !response.OK {
		return fmt.Errorf("HEY TUI refused the topic request")
	}
	return nil
}

func startTopicListener(instance string, send func(tea.Msg)) (net.Listener, error) {
	path := topicSocketPath(instance)
	dialer := net.Dialer{Timeout: 50 * time.Millisecond}
	if connection, err := dialer.DialContext(context.Background(), "unix", path); err == nil {
		_ = connection.Close()
		return nil, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale HEY TUI socket: %w", err)
	}
	listener, err := new(net.ListenConfig).Listen(context.Background(), "unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen for HEY TUI topics: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("protect HEY TUI socket: %w", err)
	}
	go serveTopicRequests(listener, send)
	return listener, nil
}

func serveTopicRequests(listener net.Listener, send func(tea.Msg)) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go handleTopicRequest(connection, send)
	}
}

func handleTopicRequest(connection net.Conn, send func(tea.Msg)) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	var request TopicRequest
	if err := json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&request); err != nil || request.TopicID <= 0 {
		_ = json.NewEncoder(connection).Encode(struct {
			OK bool `json:"ok"`
		}{})
		return
	}
	send(request)
	_ = json.NewEncoder(connection).Encode(struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func closeTopicListener(instance string, listener net.Listener) {
	if listener == nil {
		return
	}
	_ = listener.Close()
	_ = os.Remove(topicSocketPath(instance))
}
