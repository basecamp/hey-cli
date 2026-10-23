package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
)

// hey mcp's own credential (internal/auth/mcp.go): `hey auth login --mcp` signs in the
// HEY MCP (local) client on read write send, stored apart from the CLI's credential, and
// `hey mcp` serves on that alone.

var (
	// mcpAuthMgr manages hey mcp's credential. Built in the root pre-run beside authMgr.
	mcpAuthMgr *auth.Manager
	// errMCPClientIDConfig is why the configured local MCP client id was refused, if it was.
	errMCPClientIDConfig error
)

func initMCPAuth(httpClient *http.Client, configDir string) {
	clientID, _, err := config.MCPClientID(cfg.BaseURL)
	errMCPClientIDConfig = err
	mcpAuthMgr = auth.NewMCPManager(cfg.BaseURL, httpClient, configDir, clientID)
}

const mcpLoginCommand = "hey auth login --mcp"

// errMCPNotSignedIn is hey mcp run without its credential.
func errMCPNotSignedIn() *apierr.Error {
	err := apierr.ErrAuth("hey mcp has its own sign-in, separate from the CLI's, and it isn't signed in")
	err.Hint = "Run: " + mcpLoginCommand
	return err
}

func errMCPClientUnconfigured() *apierr.Error {
	err := apierr.ErrUsage("hey mcp's sign-in isn't available for this HEY yet: no HEY MCP (local) client id is configured")
	err.Hint = "Set HEY_MCP_CLIENT_ID, or mcp_client_ids in ~/.config/hey-cli/config.json, to the client id HEY provisioned"
	return err
}

// requireMCPCredentials loads hey mcp's credential, refusing when there is none. It never
// falls back to the CLI's credential: that one holds full, and serving hey mcp on it would
// undo what the scoped sign-in contains.
func requireMCPCredentials() (*auth.Credentials, error) {
	creds, err := mcpAuthMgr.StoredCredentials()
	if errors.Is(err, auth.ErrCredentialsNotFound) {
		return nil, errMCPNotSignedIn()
	}
	if err != nil {
		return nil, fmt.Errorf("could not read hey mcp's credentials: %w", err)
	}
	if creds.AccessToken == "" && creds.RefreshToken == "" {
		return nil, errMCPNotSignedIn()
	}
	return creds, nil
}

// mcpCanSend is whether hey mcp serves send_draft on creds: the grant holds send, and
// write, without which hey mcp serves reads only (and read, which write implies). The
// sign-in's local key is the principal send safety counts against, so a credential
// without one can't send.
func mcpCanSend(creds *auth.Credentials) bool {
	return auth.HasScope(creds.Scope, "write", "send") && creds.GrantKey != ""
}

// loginMCP runs `hey auth login --mcp`.
func loginMCP(cmd *cobra.Command, noBrowser bool) error {
	if errMCPClientIDConfig != nil {
		return apierr.ErrUsage(errMCPClientIDConfig.Error())
	}

	// A device code lives ten minutes; the loopback fallback waits five.
	ctx, cancel := context.WithTimeout(cmd.Context(), 12*time.Minute)
	defer cancel()

	method, err := mcpAuthMgr.LoginMCP(ctx, auth.LoginOptions{NoBrowser: noBrowser})
	if errors.Is(err, auth.ErrMCPClientUnconfigured) {
		return errMCPClientUnconfigured()
	}
	if err != nil {
		return apierr.ErrAuth(fmt.Sprintf("hey mcp sign-in failed: %v", err))
	}

	creds, err := requireMCPCredentials()
	if err != nil {
		return err
	}
	canSend := mcpCanSend(creds)
	line := fmt.Sprintf("Signed hey mcp in (%s).", creds.Scope)
	if !canSend {
		line += " hey mcp can't send on this sign-in (it needs write and send); sign in again and leave both checked to allow it."
	}
	return writeMutationLine(cmd, line, "hey mcp signed in",
		map[string]any{"method": method, "scope": creds.Scope, "can_send": canSend})
}

// logoutMCP runs `hey auth logout --mcp`: revoke the grant at HEY, then forget it here.
// The local copy goes even when the revocation can't be confirmed, and the warning says
// where to finish it.
func logoutMCP(cmd *cobra.Command) error {
	if _, err := mcpAuthMgr.StoredCredentials(); errors.Is(err, auth.ErrCredentialsNotFound) {
		return writeMutation(cmd, "hey mcp wasn't signed in", nil)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()
	revokeErr := mcpAuthMgr.Revoke(ctx)
	if revokeErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: couldn't revoke hey mcp's sign-in at HEY (%v); remove HEY MCP (local) in HEY's Connected Apps\n", revokeErr)
	}
	if err := mcpAuthMgr.Logout(); err != nil {
		return apierr.ErrAuth(fmt.Sprintf("could not clear hey mcp's credentials: %v", err))
	}
	return writeMutationLine(cmd, "hey mcp signed out.", "hey mcp signed out",
		map[string]any{"revoked": revokeErr == nil})
}

// mcpCredentialStatus is hey mcp's half of `hey auth status`. It never includes a token.
func mcpCredentialStatus() map[string]any {
	status := map[string]any{"authenticated": false}
	if mcpAuthMgr == nil {
		return status
	}
	if clientID := mcpAuthMgr.ClientID(); clientID != "" {
		status["client_id"] = clientID
	} else {
		status["client_configured"] = false
	}

	creds, err := mcpAuthMgr.StoredCredentials()
	if err != nil {
		if !errors.Is(err, auth.ErrCredentialsNotFound) {
			status["error"] = err.Error()
		}
		return status
	}
	if creds.AccessToken == "" && creds.RefreshToken == "" {
		return status
	}
	status["authenticated"] = true
	status["scope"] = creds.Scope
	status["can_send"] = mcpCanSend(creds)
	if creds.ExpiresAt > 0 {
		expiry := time.Unix(creds.ExpiresAt, 0)
		status["expires_at"] = expiry.Format(time.RFC3339)
		status["expired"] = time.Now().After(expiry)
	}
	if creds.RefreshToken != "" {
		status["refresh_available"] = true
	}
	if creds.GrantedAt > 0 {
		status["signed_in_at"] = time.Unix(creds.GrantedAt, 0).Format(time.RFC3339)
	}
	return status
}

// printMCPStatus is the styled line for hey mcp's credential.
func printMCPStatus(w io.Writer, status map[string]any) {
	if status["authenticated"] != true {
		fmt.Fprintf(w, "MCP:       Not signed in (run: %s)\n", mcpLoginCommand)
		return
	}
	line := fmt.Sprintf("MCP:       Signed in (%v)", status["scope"])
	if status["can_send"] != true {
		line += ", can't send"
	}
	if status["expired"] == true {
		line += ", token expired"
	}
	fmt.Fprintln(w, line)
}
