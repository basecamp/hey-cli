package mcpserver

import (
	"fmt"
	"log/slog"
	"slices"

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
	// dispatch outright. Without it, the operations that can deliver mail
	// still only save drafts (drafts.go).
	ReadOnly bool
	// Domains narrows the served domains by key ("boxes", "search", ...).
	// Empty means all. Unknown keys are a startup error — fail closed.
	Domains []string
	// Send is set when the sign-in can send. It serves send_draft in its own hey_send
	// tool (send.go) when the threads domain is served and the server isn't read-only,
	// and either way records which reads expose the sign-in (exposure.go). Nil serves no
	// way to send and records nothing.
	Send *SendConfig
}

// Server wraps the toolkit gateway serving hey's derived catalog, dispatching
// through the CLI's authenticated SDK client.
type Server struct {
	gw     *gateway.Server
	sender *sender
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
	if err = holdToDrafts(cat); err != nil {
		return nil, fmt.Errorf("hold sending operations to drafts: %w", err)
	}

	// Exposure is recorded whenever the sign-in can send, whether or not this process
	// serves hey_send: another hey mcp on the same sign-in may, and it reads the same
	// state file.
	var send *sender
	d := dispatcher{api: api}
	if cfg.Send != nil {
		if send, err = newSender(*cfg.Send); err != nil {
			return nil, fmt.Errorf("serve send_draft: %w", err)
		}
		d = dispatcher{api: api, markExposed: send.markExposed}
	}
	if cfg.ReadOnly || !servesDomain(cfg.Domains, "threads") {
		send = nil
	}

	gw, err := gateway.New(cat.GatewayDomains(), gateway.Config{
		ReadOnly: cfg.ReadOnly,
		Domains:  cfg.Domains,
		Handler:  d.handle,
	})
	if err != nil {
		return nil, err
	}

	return &Server{gw: gw, sender: send}, nil
}

// servesDomain reports whether a domain selection includes key; empty selects all.
func servesDomain(selected []string, key string) bool {
	return len(selected) == 0 || slices.Contains(selected, key)
}

// ServesSend reports whether send_draft is served.
func (s *Server) ServesSend() bool {
	return s.sender != nil
}

// Domains returns the served domains.
func (s *Server) Domains() []gateway.Domain {
	return s.gw.Domains()
}

// BuildMCPServer constructs the SDK MCP server with one gateway tool per
// served domain.
func (s *Server) BuildMCPServer(logger *slog.Logger) *mcp.Server {
	server := s.gw.BuildMCPServer(&mcp.Implementation{Name: Name, Version: version.Version}, logger)
	if s.sender != nil {
		server.AddTool(s.sender.tool(), s.sender.handle)
	}
	return server
}
