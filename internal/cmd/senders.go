package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/spf13/cobra"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// configuredSenders is shared by discovery and selection: the same address
// list is offered to the caller and accepted by --from.
func configuredSenders(identity *generated.Identity, accountID int64) []generated.Sender {
	senders := []generated.Sender{}
	for _, sender := range identity.Senders {
		if sender.Id <= 0 || sender.AccountId <= 0 || strings.TrimSpace(sender.EmailAddress) == "" {
			continue
		}
		if accountID != 0 && sender.AccountId != accountID {
			continue
		}
		for _, account := range identity.Accounts {
			if account.Id == sender.AccountId && linkedAccountAccessible(account) {
				senders = append(senders, sender)
				break
			}
		}
	}
	return senders
}

func resolveSender(senders []generated.Sender, from string) (generated.Sender, error) {
	from = strings.TrimSpace(from)
	id, _ := strconv.ParseInt(from, 10, 64)
	var matches []generated.Sender
	for _, sender := range senders {
		if from != "" && ((id > 0 && sender.Id == id) || strings.EqualFold(sender.EmailAddress, from)) {
			matches = append(matches, sender)
		}
	}
	if len(matches) != 1 {
		return generated.Sender{}, apierr.ErrUsageHint("--from must identify exactly one configured sender in this account selection", "hey account senders --json")
	}
	return matches[0], nil
}

func selectedSender(ctx context.Context, from string, accountID int64) (*hey.Client, generated.Sender, error) {
	if selected, scoped := sdk.AccountID(); scoped {
		if accountID != 0 && selected != accountID {
			return nil, generated.Sender{}, apierr.ErrUsage("the draft belongs to a different account than --account")
		}
		accountID = selected
	}
	identity, err := rootSDK.Identity().GetIdentity(ctx)
	if err != nil {
		return nil, generated.Sender{}, apierr.FromSDK(err)
	}
	if identity == nil {
		return nil, generated.Sender{}, apierr.ErrAPI(0, "HEY returned no identity data")
	}
	sender, err := resolveSender(configuredSenders(identity, accountID), from)
	if err != nil {
		return nil, generated.Sender{}, err
	}
	client, err := rootSDK.ForAccount(ctx, sender.AccountId)
	if err != nil {
		return nil, generated.Sender{}, apierr.FromSDK(err)
	}
	return client, sender, nil
}

type accountSenderItem struct {
	ID        int64  `json:"id"`
	AccountID int64  `json:"account_id"`
	Email     string `json:"email"`
	Default   bool   `json:"default"`
}

func newAccountSendersCommand() *cobra.Command {
	return &cobra.Command{
		Use:         "senders",
		Short:       "List configured sender addresses",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"agent_notes": "Lists sender IDs, account IDs, email addresses, and account-default flags. --account limits the list; use an email or sender ID with compose --from or draft edit --from. Does not change the default."},
		Example:     "  hey account senders\n  hey account senders --account 12345 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireAuth(); err != nil {
				return err
			}
			identity, err := rootSDK.Identity().GetIdentity(cmd.Context())
			if err != nil {
				return apierr.FromSDK(err)
			}
			if identity == nil {
				return apierr.ErrAPI(0, "HEY returned no identity data")
			}
			client, err := clientForAccountSelection(cmd.Context(), rootSDK, cfg.AccountID)
			if err != nil {
				return err
			}
			accountID, _ := client.AccountID()
			items := []accountSenderItem{}
			for _, sender := range configuredSenders(identity, accountID) {
				items = append(items, accountSenderItem{ID: sender.Id, AccountID: sender.AccountId, Email: sender.EmailAddress, Default: sender.Default})
			}
			if writer.IsStyled() {
				table := newTable(cmd.OutOrStdout())
				table.addRow([]string{"ID", "Account", "Email", "Default"})
				for _, item := range items {
					table.addRow([]string{strconv.FormatInt(item.ID, 10), strconv.FormatInt(item.AccountID, 10), terminal.SanitizeLine(item.Email), strconv.FormatBool(item.Default)})
				}
				table.print()
				return nil
			}
			return writeOK(items, output.WithSummary(fmt.Sprintf("%d sender addresses", len(items))))
		},
	}
}

func (c *composeCommand) composeFrom(cmd *cobra.Command, message string, to, cc, bcc []string) error {
	ctx := cmd.Context()
	client, sender, err := selectedSender(ctx, c.from, 0)
	if err != nil {
		return err
	}
	message, err = attachFilesWithClient(ctx, client, message, c.attachments)
	if err != nil {
		return err
	}
	if !c.noNameTag && sender.NameTag != "" {
		message += "<br>" + sender.NameTag
	}
	content := hey.DraftContent{Subject: c.subject, Content: message, To: to, CC: cc, BCC: bcc, ActingSenderID: sender.Id}
	id, err := client.Messages().CreateDraft(ctx, content)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if c.draft {
		return writeDraftSaved(cmd, id, len(c.attachments))
	}

	// The released SDK's send-now method cannot select a sender. Its draft
	// route can. Verify the saved state before delivering that exact draft.
	edit, err := client.Messages().GetEdit(ctx, id)
	if err != nil {
		return fmt.Errorf("draft %d saved; verification failed; not sent: %w", id, apierr.FromSDK(err))
	}
	if edit == nil || edit.Id != id || edit.IsReply || !edit.ScheduledDeliveryAt.IsZero() {
		return apierr.ErrUsage(fmt.Sprintf("draft %d saved but its state could not be verified; not sent", id))
	}
	actual := draftContentFrom(edit)
	if actual.ActingSenderID == 0 {
		actual.ActingSenderID = edit.Creator.Id
	}
	if !sameSenderDraft(content, actual) {
		return apierr.ErrUsageHint(fmt.Sprintf("draft %d saved but readback differs; not sent", id), fmt.Sprintf("hey draft show %d", id))
	}
	// Keep delivery single-attempt even if the SDK operation policy changes.
	delivery, err := newSDKClient(hey.WithMaxRetries(0)).ForAccount(ctx, sender.AccountId)
	if err != nil {
		return fmt.Errorf("draft %d saved; delivery client unavailable; not sent: %w", id, apierr.FromSDK(err))
	}
	if err := delivery.Messages().SendDraft(ctx, id, actual); err != nil {
		return fmt.Errorf("delivery of draft %d was not confirmed; inspect its state before retrying: %w", id, apierr.FromSDK(err))
	}
	return writeMutation(cmd, sentWithAttachmentsSummary("Message sent", len(c.attachments)), map[string]any{"id": id})
}

func sameSenderDraft(expected, actual hey.DraftContent) bool {
	return expected.ActingSenderID == actual.ActingSenderID &&
		expected.Subject == actual.Subject && sameSavedMessageHTML(expected.Content, actual.Content) &&
		slices.Equal(expected.To, actual.To) && slices.Equal(expected.CC, actual.CC) &&
		slices.Equal(expected.BCC, actual.BCC) && actual.Schedule == nil
}

// sameSavedMessageHTML allows HEY's lossless HTML envelope, not plain-text
// equality: the links, formatting, and attachments must survive too.
func sameSavedMessageHTML(expected, actual string) bool {
	if expected == actual {
		return true
	}
	nodes, err := html.ParseFragment(strings.NewReader(actual), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return false
	}
	nodes = significantHTMLNodes(nodes)
	if len(nodes) != 1 {
		return false
	}
	node := nodes[0]
	if node.Type == html.ElementNode && node.Data == "div" && len(node.Attr) == 0 {
		var children []*html.Node
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			children = append(children, child)
		}
		children = significantHTMLNodes(children)
		if len(children) != 1 {
			return false
		}
		node = children[0]
	}
	if node.Type != html.ElementNode || node.Data != "figure" {
		return false
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.TextNode || strings.TrimSpace(child.Data) != "" {
			return false
		}
	}
	var attachment struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
		Filename    string `json:"filename"`
		URL         string `json:"url"`
	}
	found := false
	for _, attr := range node.Attr {
		if attr.Key == "data-trix-attachment" {
			if found || json.Unmarshal([]byte(attr.Val), &attachment) != nil {
				return false
			}
			found = true
		}
	}
	if !found || attachment.ContentType != "text/html" || attachment.Filename != "" || attachment.URL != "" {
		return false
	}
	return attachment.Content == "<shadow-content><template>"+expected+"</template></shadow-content>"
}

func significantHTMLNodes(nodes []*html.Node) []*html.Node {
	var significant []*html.Node
	for _, node := range nodes {
		if node.Type == html.TextNode && strings.TrimSpace(node.Data) == "" {
			continue
		}
		significant = append(significant, node)
	}
	return significant
}

// The account comes from the existing draft, never from the requested sender.
// Changing From changes that field only; existing signatures are authored body.
func draftSenderAccount(edit *generated.MessageEditState) (int64, error) {
	accountID := edit.Creator.AccountId
	if accountID <= 0 {
		accountID = edit.Sender.AccountId
	}
	if accountID <= 0 || (edit.Sender.AccountId > 0 && edit.Sender.AccountId != accountID) {
		return 0, apierr.ErrUsage("draft does not identify one account; sender unchanged")
	}
	return accountID, nil
}
