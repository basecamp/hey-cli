package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
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
			"contacts, and todos as tools.\n\n" +
			"hey mcp has its own sign-in, separate from the CLI's: run `hey auth login --mcp`\n" +
			"first. It signs in as \"HEY MCP (local)\", which can read, write and send but\n" +
			"can't reach settings, forwarding, trash or HEY World, and shows in HEY's Connected\n" +
			"Apps on its own. hey mcp never uses the CLI's credential or HEY_TOKEN.\n\n" +
			"create_message, create_reply and update_message only save drafts, and a request\n" +
			"to send, schedule or triage in the same call is refused before HEY is contacted.\n" +
			"Updating a scheduled draft clears its schedule. hey_send's send_draft is the one\n" +
			"way to deliver: it asks you to confirm each email in your MCP client, showing its\n" +
			"recipients, subject and body, and sends exactly what you approved, once. A client\n" +
			"that can't show a confirmation gets the draft's link in HEY instead. send_draft\n" +
			"is served only when the sign-in was granted sending. --read-only drops every\n" +
			"write action, drafts and sending included.\n\n" +
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
// send is irreversible. The dispatcher holds it to a draft save (drafts.go),
// but a save HEY didn't confirm as a draft may still have sent, so it stays
// single-shot. POST and PATCH never auto-retried, so sending PUT and
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
	// hey mcp serves on its own credential, never the CLI's (mcp_auth.go).
	creds, err := requireMCPCredentials()
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	reads, err := clientForAccountSelection(ctx, newMCPSDKClient(), cfg.AccountID)
	if err != nil {
		return err
	}
	writes, err := clientForAccountSelection(ctx, newMCPSDKClient(hey.WithMaxRetries(0)), cfg.AccountID)
	if err != nil {
		return err
	}

	// The tools follow the grant: without write, nothing that writes is served; without
	// send, no hey_send.
	readOnly := c.readOnly || !auth.HasScope(creds.Scope, "write")
	serverCfg := mcpserver.Config{ReadOnly: readOnly, Domains: c.domains}
	// Send safety is configured whenever the sign-in can send, read-only or not: this
	// process records exposure for any other hey mcp on the same sign-in that sends.
	canSend := mcpCanSend(creds)
	if canSend {
		state, stateErr := mcpserver.OpenSendState(config.StateDir())
		if stateErr != nil {
			return stateErr
		}
		// An undated sign-in makes no recipient known: only mail sent before it does.
		var grantedAt time.Time
		if creds.GrantedAt > 0 {
			grantedAt = time.Unix(creds.GrantedAt, 0)
		}
		serverCfg.Send = &mcpserver.SendConfig{
			Drafts: mcpDrafts{reads: reads, writes: writes},
			State:  state,
			Grant:  mcpserver.Grant{Key: creds.GrantKey, GrantedAt: grantedAt},
			Origin: strings.TrimRight(cfg.BaseURL, "/"),
			CurrentGrant: func(context.Context) (string, error) {
				stored, loadErr := mcpAuthMgr.StoredCredentials()
				if loadErr != nil {
					return "", loadErr
				}
				return stored.GrantKey, nil
			},
		}
	}

	srv, err := mcpserver.New(mcpAPI{reads: reads, writes: writes}, serverCfg)
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
	logger.Info("MCP server running on stdio", "tools", len(srv.Domains()), "read_only", readOnly, "send_draft", srv.ServesSend())
	if !canSend {
		logger.Info("send_draft isn't served: hey mcp's sign-in wasn't granted sending. To allow it, run `hey auth login --mcp` again and leave sending checked.")
	}

	return session.Wait()
}

// newMCPSDKClient builds a client like the CLI's, authenticated with hey mcp's own
// credential instead. It keeps no response cache: the cache is the CLI credential's, and
// what one credential fetched isn't the other's to read.
func newMCPSDKClient(extra ...hey.ClientOption) *hey.Client {
	uncached := *sdkClientCfg
	uncached.CacheEnabled = false
	uncached.CacheDir = ""
	opts := append(append([]hey.ClientOption{}, sdkClientOpts...), hey.WithAuthStrategy(&cliAuthStrategy{mgr: mcpAuthMgr}))
	return hey.NewClient(&uncached, nil, append(opts, extra...)...)
}

// mcpDrafts is send_draft's view of the SDK: reads through the retrying client, the send
// through the one that never retries, because a retried send can deliver twice.
type mcpDrafts struct {
	reads, writes *hey.Client
}

func (d mcpDrafts) GetEdit(ctx context.Context, draftID int64) (*generated.MessageEditState, error) {
	return d.reads.Messages().GetEdit(ctx, draftID)
}

func (d mcpDrafts) SendDraft(ctx context.Context, draftID int64, draft hey.DraftContent) (*generated.SentMessage, error) {
	return d.writes.Messages().SendDraft(ctx, draftID, draft)
}

func (d mcpDrafts) Identity(ctx context.Context) (*generated.Identity, error) {
	return d.reads.Identity().GetIdentity(ctx)
}

func (d mcpDrafts) Search(ctx context.Context, params hey.SearchParams) (*generated.AdvancedSearchResult, error) {
	return d.reads.Search().Search(ctx, params)
}
