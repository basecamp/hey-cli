package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
)

type accountsCommand struct {
	cmd *cobra.Command
}

type accountListItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email,omitempty"`
	Purpose string `json:"purpose,omitempty"`
	Status  string `json:"status,omitempty"`
	Active  bool   `json:"active"`
}

func newAccountsCommand() *accountsCommand {
	accountsCommand := &accountsCommand{}
	accountsCommand.cmd = &cobra.Command{
		Use:   "accounts",
		Short: "List and select linked mail accounts",
		Long:  "List the mail accounts linked to the current HEY identity or choose the default mail filter.",
		Annotations: map[string]string{
			"agent_notes": "Linked accounts share the current login. Use list to discover IDs and use <id|all> to persist the default mail filter; --account overrides it for one invocation.",
		},
	}
	accountsCommand.cmd.AddCommand(newAccountsListCommand())
	accountsCommand.cmd.AddCommand(newAccountsUseCommand())
	return accountsCommand
}

func newAccountsListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List linked mail accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			identity, err := rootSDK.Identity().GetIdentity(cmd.Context())
			if err != nil {
				return convertSDKError(err)
			}
			if identity == nil {
				return output.ErrAPI(0, "HEY returned no identity data")
			}

			accounts := linkedAccountList(identity, cfg.AccountID)
			notice := unavailableAccountNotice(accounts, cfg.AccountID)
			if writer.IsStyled() {
				table := newTable(cmd.OutOrStdout())
				table.addRow([]string{"ID", "Email", "Name", "Purpose", "Status", "Active"})
				for _, account := range accounts {
					active := ""
					if account.Active {
						active = "yes"
					}
					table.addRow([]string{
						account.ID,
						terminalSafeText(account.Email),
						terminalSafeText(account.Name),
						terminalSafeText(account.Purpose),
						terminalSafeText(account.Status),
						active,
					})
				}
				table.print()
				if notice != "" {
					fmt.Fprintln(cmd.OutOrStdout(), notice)
				}
				return nil
			}
			return writeOK(accounts,
				output.WithSummary(fmt.Sprintf("%d mail account filters", len(accounts))),
				output.WithNotice(notice),
			)
		},
	}
}

func newAccountsUseCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "use <id|all>",
		Short:   "Set the default linked mail account",
		Example: "  hey accounts use 12345\n  hey accounts use all",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			overrideSource := cfg.SourceOf("account_id")
			if err := cfg.SetFromFlag("account_id", args[0]); err != nil {
				return err
			}
			if cfg.AccountID != config.AllAccounts {
				if err := requireAuth(); err != nil {
					return err
				}
				id, _ := strconv.ParseInt(cfg.AccountID, 10, 64)
				if _, err := rootSDK.ForAccount(cmd.Context(), id); err != nil {
					return convertSDKError(err)
				}
			}
			if err := cfg.SaveAccountID(cfg.AccountID); err != nil {
				return err
			}

			label := cfg.AccountID
			if label == config.AllAccounts {
				label = "All Accounts"
			}
			notice := accountOverrideNotice(overrideSource)
			if writer.IsStyled() {
				fmt.Fprintf(cmd.OutOrStdout(), "Default mail account: %s\n", label)
				if notice != "" {
					fmt.Fprintln(cmd.OutOrStdout(), notice)
				}
				return nil
			}
			return writeOK(map[string]string{"account_id": cfg.AccountID},
				output.WithSummary(fmt.Sprintf("Default mail account: %s", label)),
				output.WithNotice(notice),
			)
		},
	}
}

func unavailableAccountNotice(accounts []accountListItem, selected string) string {
	if selected == config.AllAccounts {
		return ""
	}
	for _, account := range accounts {
		if account.Active {
			return ""
		}
	}
	return fmt.Sprintf("Configured account %s is unavailable; run `hey accounts use all` or select an available ID.", selected)
}

func accountOverrideNotice(source config.Source) string {
	switch source {
	case config.SourceLocal:
		return "Saved the global default; local .hey/config.json remains higher precedence."
	case config.SourceEnv:
		return "Saved the global default; HEY_ACCOUNT_ID remains higher precedence."
	case config.SourceFlag:
		return "Saved the global default; --account controls only this invocation."
	default:
		return ""
	}
}

func linkedAccountList(identity *generated.Identity, selected string) []accountListItem {
	accounts := []accountListItem{{
		ID:     config.AllAccounts,
		Name:   "All Accounts",
		Active: selected == config.AllAccounts,
	}}
	for _, account := range identity.Accounts {
		if !linkedAccountAccessible(account) {
			continue
		}
		id := strconv.FormatInt(account.Id, 10)
		accounts = append(accounts, accountListItem{
			ID:      id,
			Name:    account.Name,
			Email:   linkedAccountEmail(identity.AllUsers, account.Id),
			Purpose: account.Purpose,
			Status:  account.Status,
			Active:  selected == id,
		})
	}
	return accounts
}

func linkedAccountAccessible(account generated.Account) bool {
	return account.Status == "active" ||
		(account.Status == "inactive" && (account.Purpose == "work" || account.Purpose == "domains"))
}

func linkedAccountEmail(users []generated.User, accountID int64) string {
	for _, user := range users {
		if user.AccountId == accountID {
			return user.Contact.EmailAddress
		}
	}
	return ""
}
