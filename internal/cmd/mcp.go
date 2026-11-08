package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
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
			"recipients, subject and body and naming anything it can't show, and sends exactly\n" +
			"what you approved, once. It is served only when the sign-in was granted sending,\n" +
			"and listed only to a client that can show a confirmation; any other client is\n" +
			"pointed at the draft's page in HEY. --read-only drops every write action, drafts\n" +
			"and sending included.\n\n" +
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

// AccountID is the account hey mcp was started on, when it was started on one: its
// clients then send that account as every request's account filter, whatever a
// tool call asked for (mcpserver's pinnedAccount).
func (a mcpAPI) AccountID() (int64, bool) {
	return a.reads.AccountID()
}

var _ interface{ AccountID() (int64, bool) } = mcpAPI{}

func (c *mcpCommand) run(cmd *cobra.Command, args []string) error {
	// hey mcp serves on its own credential, never the CLI's (mcp_auth.go).
	creds, err := requireMCPCredentials()
	if err != nil {
		return err
	}
	// This process authenticates only as the sign-in it starts on: the tools it serves
	// follow that sign-in's grant, and a confirmation is bound to it.
	mcpAuthMgr.PinGrant(creds.GrantKey)

	ctx := cmd.Context()
	// The tools are chosen once, from the grant's scope, so renew a token about to
	// expire first: a renewal can narrow the grant, and the tools should follow what it
	// granted. A renewal that fails here (HEY unreachable) leaves the stored grant; the
	// first request reports it.
	if _, renewErr := mcpAuthMgr.AccessToken(ctx); renewErr == nil {
		if creds, err = requireMCPCredentials(); err != nil {
			return err
		}
	}
	reads, err := mcpClientForAccountSelection(ctx, newMCPSDKClient(), cfg.AccountID)
	if err != nil {
		return err
	}
	writes, err := mcpClientForAccountSelection(ctx, newMCPSDKClient(hey.WithMaxRetries(0)), cfg.AccountID)
	if err != nil {
		return err
	}

	// The tools follow the grant: without write, nothing that writes is served; without
	// send, no hey_send.
	readOnly := c.readOnly || !auth.HasScope(creds.Scope, "write")
	serverCfg := mcpserver.Config{ReadOnly: readOnly, Domains: c.domains}
	canSend := mcpCanSend(creds)
	if canSend && !readOnly {
		origin, originErr := config.Origin(cfg.BaseURL)
		if originErr != nil {
			return originErr
		}
		// The delivery goes through a client of its own that can't resend: the SDK
		// answers a 401 by refreshing and resending once, and a send is one request
		// (D2.4). The token is refreshed before the send instead.
		sends, sendsErr := mcpClientForAccountSelection(ctx, newMCPSDKClient(hey.WithMaxRetries(0), hey.WithAuthStrategy(onceAuthStrategy{mgr: mcpAuthMgr})), cfg.AccountID)
		if sendsErr != nil {
			return sendsErr
		}
		serverCfg.Send = &mcpserver.SendConfig{
			Drafts:   mcpDrafts{reads: reads, sends: sends},
			GrantKey: creds.GrantKey,
			Origin:   origin,
			CurrentGrant: func(ctx context.Context) (string, error) {
				// Renew a token about to expire now, before the claim, rather than
				// after it: a renewal can narrow the grant, and the send then must
				// not go ahead. A token that can't be made live refuses too, so a
				// draft that certainly wasn't sent isn't claimed.
				if _, tokenErr := mcpAuthMgr.AccessToken(ctx); tokenErr != nil {
					return "", tokenErr
				}
				stored, loadErr := mcpAuthMgr.StoredCredentials()
				if loadErr != nil {
					return "", loadErr
				}
				// A refresh that narrowed the grant below sending is a different
				// sign-in as far as sending goes: refuse before claiming.
				if !mcpCanSend(stored) {
					return "", nil
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
		logger.Info("send_draft isn't served: hey mcp's sign-in wasn't granted sending. To allow it, run `hey auth login --mcp` again and turn sending on.")
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
// through one that never retries or resends, because a second request can deliver twice.
type mcpDrafts struct {
	reads, sends *hey.Client
}

// onceAuthStrategy authenticates like cliAuthStrategy but offers the SDK no refresh,
// so a 401 surfaces instead of being answered with a resend.
type onceAuthStrategy struct {
	mgr *auth.Manager
}

func (a onceAuthStrategy) Authenticate(ctx context.Context, req *http.Request) error {
	return a.mgr.AuthenticateRequest(ctx, req)
}

func (d mcpDrafts) GetEdit(ctx context.Context, draftID int64) (*generated.MessageEditState, error) {
	return d.reads.Messages().GetEdit(ctx, draftID)
}

func (d mcpDrafts) SendDraft(ctx context.Context, draftID int64, draft hey.DraftContent) (*generated.SentMessage, error) {
	// The token was renewed just before the claim (CurrentGrant), so this one request
	// goes out on a live one.
	return d.sends.Messages().SendDraft(ctx, draftID, draft)
}

// mcpClientForAccountSelection scopes a client to the account hey is set to use, as
// every command does. hey mcp's sign-in reaches only the accounts the person chose when
// they connected it, so the account may be one it was never given, which HEY answers as
// an account that isn't there. Say which account it is and how to reach it, rather than
// a bare not_found.
func mcpClientForAccountSelection(ctx context.Context, client *hey.Client, accountID string) (*hey.Client, error) {
	scoped, err := clientForAccountSelection(ctx, client, accountID)
	var cliErr *apierr.Error
	if errors.As(err, &cliErr) && cliErr.Code == apierr.CodeNotFound {
		return nil, &apierr.Error{
			Code:       apierr.CodeNotFound,
			Message:    fmt.Sprintf("hey mcp's sign-in wasn't given access to account %s, the one hey is set to use", accountID),
			Hint:       "Run `hey auth login --mcp` to choose the accounts it can reach, or " + mcpAccountRemedy(cfg.SourceOf("account_id")) + ".",
			HTTPStatus: cliErr.HTTPStatus,
			Cause:      err,
		}
	}
	return scoped, err
}

// mcpAccountRemedy says how to point hey mcp at the accounts its sign-in was given,
// for wherever the account selection came from: `hey account use` saves only the
// global default, which a flag, HEY_ACCOUNT_ID or a local .hey/config.json outranks.
func mcpAccountRemedy(source config.Source) string {
	switch source {
	case config.SourceFlag:
		return "pass --account all (or the id of an account it was given)"
	case config.SourceEnv:
		return "set HEY_ACCOUNT_ID to all (or the id of an account it was given)"
	case config.SourceLocal:
		return "change account_id in this directory's .hey/config.json, or run hey mcp --account all"
	default:
		return "run `hey account use all` (or `hey account use <id>` for an account it was given)"
	}
}
