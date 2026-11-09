package mcpserver

import (
	"context"
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
	// tool (send.go) when the threads domain is served and the server isn't read-only.
	// Nil serves no way to send.
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

	cat, err := loadCatalog(cfg.ReadOnly)
	if err != nil {
		return nil, fmt.Errorf("derive catalog: %w", err)
	}
	if err = holdToDrafts(cat); err != nil {
		return nil, fmt.Errorf("hold sending operations to drafts: %w", err)
	}

	var send *sender
	if cfg.Send != nil && !cfg.ReadOnly && servesDomain(cfg.Domains, "threads") {
		if send, err = newSender(*cfg.Send); err != nil {
			return nil, fmt.Errorf("serve send_draft: %w", err)
		}
	}

	gw, err := gateway.New(cat.GatewayDomains(), gateway.Config{
		ReadOnly: cfg.ReadOnly,
		Domains:  cfg.Domains,
		Handler:  dispatcher{api: api}.handle,
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
// served domain, and hey_send when send_draft is served.
func (s *Server) BuildMCPServer(logger *slog.Logger) *mcp.Server {
	server := s.gw.BuildMCPServer(&mcp.Implementation{Name: Name, Version: version.Version}, logger)
	if s.sender != nil {
		server.AddTool(s.sender.tool(), s.sender.handle)
		server.AddReceivingMiddleware(offerSendToClientsThatCanAsk)
	}
	return server
}

// offerSendToClientsThatCanAsk lists hey_send only to a client that can be asked,
// since send_draft sends only on the person's answer to its confirmation: a client
// that can't answer one isn't offered a tool it can't complete, and its draft saves
// name the draft's page in HEY instead. It also tells each tool call whether its
// client is offered hey_send (sendable), so a draft save's note says the same.
//
// Only the list changes. A call from a client whose list is cached still runs, and
// gets the draft's link with nothing sent (sending-safety D3).
func offerSendToClientsThatCanAsk(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		asks := canElicitForm(req)
		if method == "tools/call" {
			ctx = context.WithValue(ctx, sendableKey{}, asks)
		}
		res, err := next(ctx, method, req)
		if list, ok := res.(*mcp.ListToolsResult); ok && err == nil && !asks {
			list.Tools = slices.DeleteFunc(slices.Clone(list.Tools), func(t *mcp.Tool) bool { return t.Name == sendToolName })
		}
		return res, err
	}
}

type sendableKey struct{}

// sendable reports whether this tool call's client is offered hey_send.
func sendable(ctx context.Context) bool {
	asks, _ := ctx.Value(sendableKey{}).(bool)
	return asks
}
