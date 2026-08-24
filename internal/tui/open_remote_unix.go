//go:build unix

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gofrs/flock"
)

// ErrNoRunningTUI means no active HEY TUI accepted an open request.
var ErrNoRunningTUI = errors.New("no running HEY TUI")

const (
	tuiRuntimeDirname = "hey-cli"
	tuiSocketFilename = "hey-tui.sock"
)

type ownedTUIListener struct {
	net.Listener
	lock       *flock.Flock
	path       string
	socketInfo os.FileInfo
	closeOnce  sync.Once
	closeErr   error
}

func tuiSocketPath(instance string) (string, error) {
	name, err := tuiSocketName(instance)
	if err != nil {
		return "", err
	}
	runtimeDir, err := tuiRuntimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(runtimeDir, name), nil
}

func tuiSocketName(instance string) (string, error) {
	if instance == "" {
		return tuiSocketFilename, nil
	}
	if len(instance) > 32 {
		return "", fmt.Errorf("TUI instance must be at most 32 characters")
	}
	for _, char := range instance {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return "", fmt.Errorf("TUI instance may contain only letters, numbers, hyphens, and underscores")
	}
	return "hey-tui-" + instance + ".sock", nil
}

func tuiRuntimeDir() (string, error) {
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		if !filepath.IsAbs(runtimeDir) {
			return "", fmt.Errorf("XDG_RUNTIME_DIR must be an absolute path")
		}
		if err := validatePrivateDirectory(runtimeDir); err != nil {
			return "", fmt.Errorf("protect HEY TUI runtime directory: %w", err)
		}
		return ensurePrivateDirectory(filepath.Join(runtimeDir, tuiRuntimeDirname))
	}

	tempDir := os.TempDir()
	info, err := os.Lstat(tempDir)
	if err != nil {
		return "", fmt.Errorf("inspect temporary directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("temporary directory is not a directory")
	}
	if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
		return "", fmt.Errorf("temporary directory is writable by other users without the sticky bit")
	}
	return ensurePrivateDirectory(filepath.Join(tempDir, fmt.Sprintf("%s-%d", tuiRuntimeDirname, os.Getuid())))
}

func ensurePrivateDirectory(path string) (string, error) {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) { // #nosec G703 -- path is a fixed child of an absolute, validated private runtime directory
		return "", fmt.Errorf("create HEY TUI runtime directory: %w", err)
	}
	if err := validatePrivateDirectory(path); err != nil {
		return "", fmt.Errorf("validate HEY TUI runtime directory: %w", err)
	}
	return path, nil
}

func validatePrivateDirectory(path string) error {
	info, err := os.Lstat(path) // #nosec G703 -- inspecting the absolute runtime path is the validation gate before any socket use
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a directory", path)
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s must have mode 0700", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s must be owned by the current user", path)
	}
	return nil
}

// OpenInRunningTUI sends a destination to the active named TUI.
func OpenInRunningTUI(instance string, request OpenRequest) error {
	if !request.valid() {
		return fmt.Errorf("open request must identify one destination")
	}
	path, err := tuiSocketPath(instance)
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	connection, err := dialer.DialContext(context.Background(), "unix", path)
	if err != nil {
		return ErrNoRunningTUI
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return fmt.Errorf("send open request to HEY TUI: %w", err)
	}
	var response struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(connection, 1024)).Decode(&response); err != nil {
		return fmt.Errorf("read HEY TUI response: %w", err)
	}
	if !response.OK {
		return fmt.Errorf("HEY TUI refused the open request")
	}
	return nil
}

func startOpenListener(instance string, send func(tea.Msg)) (net.Listener, error) {
	path, err := tuiSocketPath(instance)
	if err != nil {
		return nil, err
	}
	ownershipLock := flock.New(path + ".lock")
	locked, err := ownershipLock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock HEY TUI open listener: %w", err)
	}
	if !locked {
		return nil, errors.New("a HEY TUI with this instance is already running")
	}
	releaseLock := true
	defer func() {
		if releaseLock {
			_ = ownershipLock.Unlock()
		}
	}()

	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale HEY TUI socket: %w", removeErr)
	}
	listener, err := new(net.ListenConfig).Listen(context.Background(), "unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen for HEY TUI open requests: %w", err)
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		_ = listener.Close()
		return nil, fmt.Errorf("HEY TUI open listener is not a Unix socket")
	}
	unixListener.SetUnlinkOnClose(false)
	if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("protect HEY TUI socket: %w", chmodErr)
	}
	socketInfo, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("inspect HEY TUI socket: %w", err)
	}

	owned := &ownedTUIListener{
		Listener:   listener,
		lock:       ownershipLock,
		path:       path,
		socketInfo: socketInfo,
	}
	releaseLock = false
	go serveOpenRequests(owned, send)
	return owned, nil
}

func (l *ownedTUIListener) Close() error {
	l.closeOnce.Do(func() {
		l.closeErr = l.Listener.Close()
		if info, err := os.Lstat(l.path); err == nil && os.SameFile(l.socketInfo, info) {
			if err := os.Remove(l.path); err != nil && l.closeErr == nil {
				l.closeErr = err
			}
		}
		if err := l.lock.Unlock(); err != nil && l.closeErr == nil {
			l.closeErr = err
		}
	})
	return l.closeErr
}

func serveOpenRequests(listener net.Listener, send func(tea.Msg)) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go handleOpenRequest(connection, send)
	}
}

func handleOpenRequest(connection net.Conn, send func(tea.Msg)) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	var request OpenRequest
	if err := json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&request); err != nil || !request.valid() {
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

func closeOpenListener(listener net.Listener) {
	if listener != nil {
		_ = listener.Close()
	}
}
