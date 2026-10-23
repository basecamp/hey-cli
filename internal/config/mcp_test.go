package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMCPClientIDResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HEY_MCP_CLIENT_ID", "")

	id, source, err := MCPClientID("https://app.hey.com")
	if err != nil || id != DefaultMCPClientID || source != SourceDefault {
		t.Errorf("no config: %q %q %v, want the default", id, source, err)
	}

	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	global := `{"base_url":"https://app.hey.com","mcp_client_ids":{"https://app.hey.com/":"ProdLocalMcp000000000001","http://app.hey.localhost:3003":"DevLocalMcp0000000000001"}}`
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(global), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, source, _ := MCPClientID("https://app.hey.com"); id != "ProdLocalMcp000000000001" || source != SourceGlobal {
		t.Errorf("production: %q %q", id, source)
	}
	if id, _, _ := MCPClientID("http://app.hey.localhost:3003"); id != "DevLocalMcp0000000000001" {
		t.Errorf("dev: %q, want the id configured for its origin", id)
	}

	t.Setenv("HEY_MCP_CLIENT_ID", "EnvLocalMcp0000000000001")
	if id, source, _ := MCPClientID("https://app.hey.com"); id != "EnvLocalMcp0000000000001" || source != SourceEnv {
		t.Errorf("env: %q %q, want the environment to win", id, source)
	}

	t.Setenv("HEY_MCP_CLIENT_ID", "not an id&scope=full")
	if _, _, err := MCPClientID("https://app.hey.com"); err == nil {
		t.Error("a malformed HEY_MCP_CLIENT_ID was accepted")
	}
}

func TestConflictingSpellingsOfOneOriginAreRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HEY_MCP_CLIENT_ID", "")
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	global := `{"mcp_client_ids":{"https://app.hey.com":"FirstLocalMcp00000000001","https://APP.HEY.COM:443":"OtherLocalMcp00000000001"}}`
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(global), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MCPClientID("https://app.hey.com"); err == nil {
		t.Error("two ids for one origin resolved to one of them")
	}
}
