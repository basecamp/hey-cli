package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmailPostingActionsRejectNonEmailKindsBeforeSetup(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "move missing kind", args: []string{"move", "12345", "--to", "feed"}, want: "--kind is required"},
		{name: "move World post", args: []string{"move", "12345", "--to", "feed", "--kind", "world/post"}, want: "HEY World"},
		{name: "trash other kind", args: []string{"trash", "12345", "--kind", "calendar/event"}, want: "only manages email threads"},
		{name: "ignore World post", args: []string{"ignore", "12345", "--kind", "world/post"}, want: "HEY World"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HEY_TOKEN", "test-token")
			t.Setenv("HEY_NO_KEYRING", "1")
			t.Setenv("HEY_BASE_URL", "")
			tmpDir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", tmpDir)

			configDir := filepath.Join(tmpDir, "hey-cli")
			if err := os.MkdirAll(configDir, 0700); err != nil {
				t.Fatalf("create config directory: %v", err)
			}
			if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{"), 0600); err != nil {
				t.Fatalf("write invalid config: %v", err)
			}

			root := newRootCmd()
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatal("expected email kind validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "parse config") {
				t.Errorf("kind validation ran after root setup: %v", err)
			}
		})
	}
}
