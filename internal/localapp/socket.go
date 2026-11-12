package localapp

import (
	"errors"
	"os"
	"path/filepath"
)

// SocketEnv overrides where the door is looked for, as --socket does for one command.
const SocketEnv = "HEY_LOCAL_SOCKET"

// ErrNoRuntimeDir is a session with no XDG_RUNTIME_DIR, the one folder the app opens its
// door in. The app refuses to open one without it, so there is nothing to guess at.
var ErrNoRuntimeDir = errors.New("XDG_RUNTIME_DIR is not set to an absolute path")

// SocketPath is where the door is: the flag's path, else HEY_LOCAL_SOCKET's, else
// $XDG_RUNTIME_DIR/hey/agent.sock, where the app opens it.
func SocketPath(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if env := os.Getenv(SocketEnv); env != "" {
		return env, nil
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(runtime) {
		return "", ErrNoRuntimeDir
	}
	return filepath.Join(runtime, "hey", "agent.sock"), nil
}
