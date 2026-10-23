package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

// DefaultMCPClientID is the client id of "HEY MCP (local)", the OAuth row `hey mcp`
// signs in as (haystack#9041), for every HEY environment.
//
// TODO(provision): empty until the production row exists. Jeremy provisions production
// first with `bin/rails oauth:provision_local_mcp_client`, which prints a generated
// id; beta runs on production's database and shares that row, and staging and dev are
// then provisioned with CLIENT_ID=<that id>, so this one constant serves them all, as
// the CLI's own id does. Until it is set, `hey auth login --mcp` needs HEY_MCP_CLIENT_ID
// or a configured id (MCPClientID).
const DefaultMCPClientID = ""

// mcpClientIDPattern is what haystack's provisioning accepts for a client id: letters
// and digits.
var mcpClientIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

// MCPClientID resolves the local MCP client id for the HEY at baseURL, and where it came
// from: HEY_MCP_CLIENT_ID, then the global config's "mcp_client_ids" entry for that
// origin, then DefaultMCPClientID. It is read from the global config only, never a
// repository's .hey/config.json, so a checkout can't point hey mcp's sign-in at another
// client. An empty id means none is configured.
func MCPClientID(baseURL string) (string, Source, error) {
	if env := os.Getenv("HEY_MCP_CLIENT_ID"); env != "" {
		if !mcpClientIDPattern.MatchString(env) {
			return "", SourceEnv, fmt.Errorf("HEY_MCP_CLIENT_ID must be letters and digits")
		}
		return env, SourceEnv, nil
	}

	origin, err := serverOrigin(baseURL)
	if err != nil {
		return "", SourceDefault, err
	}
	ids, err := globalMCPClientIDs()
	if err != nil {
		return "", SourceDefault, err
	}
	if id, ok := ids[origin]; ok && id != "" {
		if !mcpClientIDPattern.MatchString(id) {
			return "", SourceGlobal, fmt.Errorf("mcp_client_ids for %s must be letters and digits", origin)
		}
		return id, SourceGlobal, nil
	}
	return DefaultMCPClientID, SourceDefault, nil
}

// globalMCPClientIDs reads the global config's "mcp_client_ids", keyed by origin.
func globalMCPClientIDs() (map[string]string, error) {
	path := globalConfigPath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the fixed user configuration file
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read config %s: %w", path, err)
	}
	var file struct {
		MCPClientIDs map[string]string `json:"mcp_client_ids"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("could not parse config %s: %w", path, err)
	}
	ids := make(map[string]string, len(file.MCPClientIDs))
	for origin, id := range file.MCPClientIDs {
		normalized, originErr := serverOrigin(origin)
		if originErr != nil {
			return nil, fmt.Errorf("could not parse mcp_client_ids origin %s in %s: %w", origin, path, originErr)
		}
		if existing, duplicate := ids[normalized]; duplicate && existing != id {
			return nil, fmt.Errorf("conflicting mcp_client_ids for %s in %s", normalized, path)
		}
		ids[normalized] = id
	}
	return ids, nil
}
