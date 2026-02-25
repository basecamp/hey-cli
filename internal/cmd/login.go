package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"hey-cli/internal/auth"
)

type loginCommand struct {
	cmd          *cobra.Command
	token        string
	clientID     string
	clientSecret string
	installID    string
}

func newLoginCommand() *loginCommand {
	loginCommand := &loginCommand{}
	loginCommand.cmd = &cobra.Command{
		Use:   "login",
		Short: "Authenticate with the HEY server",
		Long: `Authenticate with the HEY server.

Use --token to paste a pre-generated Bearer token (e.g. from generate_oauth_token.rb).
Without --token, performs interactive OAuth password grant (requires --client-id and --client-secret).`,
		RunE: loginCommand.run,
	}

	loginCommand.cmd.Flags().StringVar(&loginCommand.token, "token", "", "Pre-generated Bearer token")
	loginCommand.cmd.Flags().StringVar(&loginCommand.clientID, "client-id", "", "OAuth client ID")
	loginCommand.cmd.Flags().StringVar(&loginCommand.clientSecret, "client-secret", "", "OAuth client secret")
	loginCommand.cmd.Flags().StringVar(&loginCommand.installID, "install-id", "", "Installation ID (default: hey-cli)")

	return loginCommand
}

func (c *loginCommand) run(cmd *cobra.Command, args []string) error {
	if c.token != "" {
		cfg.AccessToken = c.token
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("could not save config: %w", err)
		}
		fmt.Println("Logged in with token.")
		return nil
	}

	if c.clientID == "" || c.clientSecret == "" {
		return fmt.Errorf("for OAuth login, provide --client-id and --client-secret (or use --token for manual token)")
	}

	cfg.ClientID = c.clientID
	cfg.ClientSecret = c.clientSecret
	if c.installID != "" {
		cfg.InstallID = c.installID
	} else {
		cfg.InstallID = "hey-cli"
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Email: ")
	email, _ := reader.ReadString('\n')
	email = strings.TrimSpace(email)

	fmt.Print("Password: ")
	passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("could not read password: %w", err)
	}
	fmt.Println()

	resp, err := auth.PasswordGrant(cfg, email, string(passwordBytes))
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	fmt.Printf("Logged in successfully. Token expires in %d seconds.\n", resp.ExpiresIn)
	return nil
}
