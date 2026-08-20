package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

type mailAccountChoice struct {
	id    int64
	label string
}

type mailAccountsLoadedMsg struct {
	accounts            []mailAccountChoice
	selected            int
	loaded              bool
	selectedUnavailable bool
	err                 error
}

type mailAccountSwitchedMsg struct {
	requestID uint64
	account   mailAccountChoice
	client    *hey.Client
	err       error
}

type viewGenerationMsg struct {
	generation uint64
	msg        tea.Msg
}

func loadMailAccounts(ctx context.Context, client *hey.Client, selected string) tea.Cmd {
	return func() tea.Msg {
		accounts := []mailAccountChoice{{label: "All Accounts"}}
		if client == nil {
			return mailAccountsLoadedMsg{accounts: accounts}
		}
		identity, err := client.Identity().GetIdentity(ctx)
		if err != nil {
			return mailAccountsLoadedMsg{err: err}
		}
		if identity == nil {
			return mailAccountsLoadedMsg{err: fmt.Errorf("HEY returned no identity data")}
		}
		for _, account := range identity.Accounts {
			if !tuiAccountAccessible(account) {
				continue
			}
			label := tuiAccountEmail(identity.AllUsers, account.Id)
			if label == "" {
				label = account.Name
			}
			if label == "" {
				label = fmt.Sprintf("Account %d", account.Id)
			}
			accounts = append(accounts, mailAccountChoice{id: account.Id, label: terminalSafeAttachmentText(label)})
		}

		selectedIndex := 0
		selectedUnavailable := false
		if id, err := strconv.ParseInt(selected, 10, 64); err == nil && id > 0 {
			selectedUnavailable = true
			for index, account := range accounts {
				if account.id == id {
					selectedIndex = index
					selectedUnavailable = false
					break
				}
			}
		}
		return mailAccountsLoadedMsg{
			accounts:            accounts,
			selected:            selectedIndex,
			loaded:              true,
			selectedUnavailable: selectedUnavailable,
		}
	}
}

func switchMailAccount(ctx context.Context, root *hey.Client, account mailAccountChoice, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		if root == nil {
			return mailAccountSwitchedMsg{requestID: requestID, account: account, err: fmt.Errorf("mail account switching is unavailable")}
		}
		if account.id == 0 {
			return mailAccountSwitchedMsg{requestID: requestID, account: account, client: root}
		}
		client, err := root.ForAccount(ctx, account.id)
		return mailAccountSwitchedMsg{requestID: requestID, account: account, client: client, err: err}
	}
}

func tuiAccountAccessible(account generated.Account) bool {
	return account.Status == "active" ||
		(account.Status == "inactive" && (account.Purpose == "work" || account.Purpose == "domains"))
}

func tuiAccountEmail(users []generated.User, accountID int64) string {
	for _, user := range users {
		if user.AccountId == accountID {
			return user.Contact.EmailAddress
		}
	}
	return ""
}

func renderMailAccountPicker(m *model) string {
	var content strings.Builder
	content.WriteString(m.styles.title.Render("Select mail account"))
	content.WriteString("\n\n")
	for index, account := range m.mailAccounts {
		prefix := "  "
		style := m.styles.entryBody
		if index == m.mailAccountCursor {
			prefix = "› "
			style = m.styles.entryFrom
		}
		content.WriteString(prefix + style.Render(account.label) + "\n")
	}
	if m.mailAccountSwitching {
		content.WriteString("\n" + m.styles.entryDate.Render("Switching account…"))
	}
	if m.mailAccountErr != "" {
		content.WriteString("\n" + m.styles.entryDate.Render("Error: "+terminalSafeAttachmentText(m.mailAccountErr)))
	}
	return content.String()
}
