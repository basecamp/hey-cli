package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

func TestEmailActionsRejectWorldPostsBeforeAnyRequest(t *testing.T) {
	tests := []struct {
		command string
		args    []string
	}{
		{command: "seen", args: []string{"12345", "--kind", "world/post"}},
		{command: "unseen", args: []string{"12345", "--kind", "world/post"}},
		{command: "move", args: []string{"12345", "--to", "feed", "--kind", "world/post"}},
		{command: "trash", args: []string{"12345", "--kind", "world/post"}},
		{command: "spam", args: []string{"12345", "--kind", "world/post"}},
		{command: "ignore", args: []string{"12345", "--kind", "world/post"}},
		{command: "stop-ignoring", args: []string{"12345", "--kind", "world/post"}},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			t.Setenv("HEY_TOKEN", "test-token")
			t.Setenv("HEY_NO_KEYRING", "1")
			t.Setenv("HEY_BASE_URL", "")
			tmpDir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", tmpDir)
			t.Setenv("XDG_STATE_HOME", tmpDir)
			t.Setenv("XDG_CACHE_HOME", tmpDir)

			root := newRootCmd()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			args := append([]string{tt.command, "--json", "--base-url", server.URL}, tt.args...)
			root.SetArgs(args)

			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), "cannot act on a HEY World post") {
				t.Fatalf("error = %v, want HEY World rejection", err)
			}
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || !strings.Contains(cliErr.Hint, "hey world delete <token> --confirm") {
				t.Fatalf("error = %#v, want separate World deletion guidance", err)
			}
			if requests.Load() != 0 {
				t.Fatalf("HEY World rejection made %d requests", requests.Load())
			}
		})
	}
}
