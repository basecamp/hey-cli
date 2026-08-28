package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/mcpserver"
)

// mcpTransport is a seam so tests can drive the server over in-memory
// transports instead of the process's stdin/stdout.
var mcpTransport = func() mcp.Transport { return &mcp.StdioTransport{} }

type mcpCommand struct {
	cmd      *cobra.Command
	readOnly bool
	domains  []string
}

func newMCPCommand() *mcpCommand {
	mcpCommand := &mcpCommand{}
	mcpCommand.cmd = &cobra.Command{
		Use:   "mcp",
		Short: "Serve HEY to MCP clients over stdio",
		Long: "Run an MCP (Model Context Protocol) server on stdin/stdout, serving HEY mail,\n" +
			"contacts, and todos as tools backed by your signed-in account.\n\n" +
			"Register it with an MCP client as a stdio server, e.g.:\n\n" +
			"  claude mcp add hey -- hey mcp",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			"agent_notes": "Long-running server; stdout speaks the MCP wire protocol. Not for interactive use.",
		},
		RunE: mcpCommand.run,
	}

	mcpCommand.cmd.Flags().BoolVar(&mcpCommand.readOnly, "read-only", false, "Serve only read-only actions")
	mcpCommand.cmd.Flags().StringSliceVar(&mcpCommand.domains, "domains", nil, "Narrow to specific domains (comma-separated; default all)")

	return mcpCommand
}

// mcpAPI is the dispatcher's view of the SDK, split by verb: reads ride the
// shared client's retry policy, while mutations go through a twin that never
// retries on 429/503. UpdateMessage delivers mail on PUT, and its contract
// forbids a transparent retry after an ambiguous first attempt — a duplicate
// send is irreversible. POST and PATCH never auto-retried, so sending PUT and
// DELETE through the no-retry twin makes every mutation single-shot. A 401
// still refreshes the token before the error surfaces, so a caller's own
// retry goes out with fresh credentials.
type mcpAPI struct {
	reads, writes *hey.Client
}

func (a mcpAPI) Get(ctx context.Context, path string) (*hey.Response, error) {
	return a.reads.Get(ctx, path)
}

func (a mcpAPI) Post(ctx context.Context, path string, body any) (*hey.Response, error) {
	return a.writes.Post(ctx, path, body)
}

func (a mcpAPI) Put(ctx context.Context, path string, body any) (*hey.Response, error) {
	return a.writes.Put(ctx, path, body)
}

func (a mcpAPI) Patch(ctx context.Context, path string, body any) (*hey.Response, error) {
	return a.writes.Patch(ctx, path, body)
}

func (a mcpAPI) Delete(ctx context.Context, path string) (*hey.Response, error) {
	return a.writes.Delete(ctx, path)
}

func (c *mcpCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	// The same account scoping the shared client got in the root command's
	// pre-run, applied to the no-retry twin.
	writes, err := clientForAccountSelection(cmd.Context(), newSDKClient(hey.WithMaxRetries(0)), cfg.AccountID)
	if err != nil {
		return err
	}

	srv, err := mcpserver.New(mcpAPI{reads: sdk, writes: writes}, mcpserver.Config{ReadOnly: c.readOnly, Domains: c.domains})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Log to stderr: stdout belongs to the MCP wire.
	logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
	session, err := srv.BuildMCPServer(logger).Connect(ctx, mcpTransport(), nil)
	if err != nil {
		return err
	}
	logger.Info("MCP server running on stdio", "tools", len(srv.Domains()), "read_only", c.readOnly)

	return session.Wait()
}
