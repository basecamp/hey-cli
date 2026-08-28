package mcpserver

import (
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basecamp/mcp/gateway"

	"github.com/basecamp/hey-cli/internal/version"
)

// Name identifies the server in the MCP initialize handshake. The version is
// the CLI's own: `hey mcp` is the CLI serving MCP, not a separate product.
const Name = "hey-cli"

// Config selects the served tool surface.
type Config struct {
	// ReadOnly drops every write action from the catalog and refuses write
	// dispatch outright.
	ReadOnly bool
	// Domains narrows the served domains by key ("boxes", "search", ...).
	// Empty means all. Unknown keys are a startup error — fail closed.
	Domains []string
}

// Server wraps the toolkit gateway serving hey's derived catalog, dispatching
// through the CLI's authenticated SDK client.
type Server struct {
	gw *gateway.Server
}

// New derives the catalog and hands it to the gateway, which applies the
// config's domain and read-only filters. Tool calls dispatch through api.
func New(api API, cfg Config) (*Server, error) {
	if api == nil {
		return nil, fmt.Errorf("mcpserver: API client is required")
	}

	cat, err := loadCatalog()
	if err != nil {
		return nil, fmt.Errorf("derive catalog: %w", err)
	}

	gw, err := gateway.New(cat.GatewayDomains(), gateway.Config{
		ReadOnly: cfg.ReadOnly,
		Domains:  cfg.Domains,
		Handler:  dispatcher{api: api}.handle,
	})
	if err != nil {
		return nil, err
	}

	return &Server{gw: gw}, nil
}

// Domains returns the served domains.
func (s *Server) Domains() []gateway.Domain {
	return s.gw.Domains()
}

// BuildMCPServer constructs the SDK MCP server with one gateway tool per
// served domain.
func (s *Server) BuildMCPServer(logger *slog.Logger) *mcp.Server {
	return s.gw.BuildMCPServer(&mcp.Implementation{Name: Name, Version: version.Version}, logger)
}
