package cmd

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/editor"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
)

type draftCommand struct {
	cmd *cobra.Command
}

var (
	messageSubjectInputRe = regexp.MustCompile(`(?s)<input[^>]+name="message\[subject\]"[^>]*>`)
	valueAttrRe           = regexp.MustCompile(`\svalue="([^"]*)"`)
)

func newDraftCommand() *draftCommand {
	draftCommand := &draftCommand{}
	draftCommand.cmd = &cobra.Command{
		Use:   "draft",
		Short: "Create, update, or delete drafts",
		Annotations: map[string]string{
			"agent_notes": "Use draft create/update/delete for draft-safe email work. These commands save drafts and do not send.",
		},
	}

	draftCommand.cmd.AddCommand(newDraftCreateCommand())
	draftCommand.cmd.AddCommand(newDraftUpdateCommand())
	draftCommand.cmd.AddCommand(newDraftDeleteCommand())

	return draftCommand
}

type draftCreateCommand struct {
	to       string
	cc       string
	bcc      string
	subject  string
	message  string
	threadID string
}

func newDraftCreateCommand() *cobra.Command {
	c := &draftCreateCommand{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a saved draft without sending",
		Example: `  hey draft create --to alice@example.com --subject "Hello" -m "Hi there"
  hey draft create --thread-id 12345 -m "Thanks, I'll take a look."`,
		RunE: c.run,
	}

	cmd.Flags().StringVar(&c.to, "to", "", "Recipient email address(es)")
	cmd.Flags().StringVar(&c.cc, "cc", "", "CC recipient email address(es)")
	cmd.Flags().StringVar(&c.bcc, "bcc", "", "BCC recipient email address(es)")
	cmd.Flags().StringVar(&c.subject, "subject", "", "Message subject")
	cmd.Flags().StringVarP(&c.message, "message", "m", "", "Draft body (or opens $EDITOR)")
	cmd.Flags().StringVar(&c.threadID, "thread-id", "", "Thread ID to draft a reply in")

	return cmd
}

func (c *draftCreateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	message, err := draftMessage(c.message)
	if err != nil {
		return err
	}

	req := draftFormRequest{
		Subject: c.subject,
		Content: message,
		To:      parseAddresses(c.to),
		CC:      parseAddresses(c.cc),
		BCC:     parseAddresses(c.bcc),
	}

	if c.threadID != "" {
		threadID, err := strconv.ParseInt(c.threadID, 10, 64)
		if err != nil {
			return output.ErrUsage(fmt.Sprintf("invalid thread ID: %s", c.threadID))
		}
		return createReplyDraft(cmd.Context(), cmd.OutOrStdout(), threadID, req)
	}

	if req.Subject == "" {
		return output.ErrUsageHint("--subject is required for new drafts", "hey draft create --to <email> --subject <subject> -m <message>")
	}

	return createMessageDraft(cmd.Context(), cmd.OutOrStdout(), req)
}

type draftUpdateCommand struct {
	to      string
	cc      string
	bcc     string
	subject string
	message string
}

func newDraftUpdateCommand() *cobra.Command {
	c := &draftUpdateCommand{}
	cmd := &cobra.Command{
		Use:     "update <draft-id>",
		Short:   "Replace a saved draft without sending",
		Example: `  hey draft update 12345 --to alice@example.com --subject "Hello" -m "Updated body"`,
		Args:    usageExactOneArg(),
		RunE:    c.run,
	}

	cmd.Flags().StringVar(&c.to, "to", "", "Recipient email address(es)")
	cmd.Flags().StringVar(&c.cc, "cc", "", "CC recipient email address(es)")
	cmd.Flags().StringVar(&c.bcc, "bcc", "", "BCC recipient email address(es)")
	cmd.Flags().StringVar(&c.subject, "subject", "", "Message subject (required)")
	cmd.Flags().StringVarP(&c.message, "message", "m", "", "Draft body (or opens $EDITOR)")

	return cmd
}

func (c *draftUpdateCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	draftID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid draft ID: %s", args[0]))
	}
	if c.subject == "" {
		return output.ErrUsageHint("--subject is required", "hey draft update <draft-id> --to <email> --subject <subject> -m <message>")
	}

	message, err := draftMessage(c.message)
	if err != nil {
		return err
	}

	return updateDraft(cmd.Context(), cmd.OutOrStdout(), draftID, draftFormRequest{
		Subject: c.subject,
		Content: message,
		To:      parseAddresses(c.to),
		CC:      parseAddresses(c.cc),
		BCC:     parseAddresses(c.bcc),
	})
}

type draftDeleteCommand struct{}

func newDraftDeleteCommand() *cobra.Command {
	c := &draftDeleteCommand{}
	return &cobra.Command{
		Use:     "delete <draft-id>",
		Short:   "Delete a saved draft",
		Example: `  hey draft delete 12345`,
		Args:    usageExactOneArg(),
		RunE:    c.run,
	}
}

func (c *draftDeleteCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	draftID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid draft ID: %s", args[0]))
	}

	return deleteDraft(cmd.Context(), cmd.OutOrStdout(), draftID)
}

type draftFormRequest struct {
	Subject string
	Content string
	To      []string
	CC      []string
	BCC     []string
}

type draftResponse struct {
	ID      int64  `json:"id"`
	URL     string `json:"url"`
	EditURL string `json:"edit_url"`
	Subject string `json:"subject,omitempty"`
}

func createMessageDraft(ctx context.Context, w io.Writer, draft draftFormRequest) error {
	senderID, err := sdk.DefaultSenderID(ctx)
	if err != nil {
		return convertSDKError(err)
	}

	values := draftValues(senderID, draft)
	resp, err := submitDraftForm(ctx, "POST", "/messages", values)
	if err != nil {
		return err
	}

	return writeDraftSaved(w, resp, "Draft created")
}

func createReplyDraft(ctx context.Context, w io.Writer, threadID int64, draft draftFormRequest) error {
	senderID, err := sdk.DefaultSenderID(ctx)
	if err != nil {
		return convertSDKError(err)
	}

	topicResp, err := sdk.GetHTML(ctx, fmt.Sprintf("/topics/%d", threadID))
	if err != nil {
		return convertSDKError(err)
	}
	addressed := htmlutil.ParseTopicAddressed(string(topicResp.Data))

	entriesResp, err := sdk.GetHTML(ctx, fmt.Sprintf("/topics/%d/entries", threadID))
	if err != nil {
		return convertSDKError(err)
	}
	entries := htmlutil.ParseTopicEntriesHTML(string(entriesResp.Data))
	if len(entries) == 0 {
		return output.ErrNotFound("entries for thread", fmt.Sprintf("%d", threadID))
	}

	latestEntryID := entries[len(entries)-1].ID
	if draft.Subject == "" {
		subject, err := defaultReplySubject(ctx, latestEntryID)
		if err != nil {
			return err
		}
		draft.Subject = subject
	}
	if len(draft.To) == 0 && len(draft.CC) == 0 && len(draft.BCC) == 0 {
		draft.To = addressed.To
		draft.CC = addressed.CC
		draft.BCC = addressed.BCC
	}

	values := draftValues(senderID, draft)
	resp, err := submitDraftForm(ctx, "POST", fmt.Sprintf("/entries/%d/replies", latestEntryID), values)
	if err != nil {
		return err
	}

	return writeDraftSaved(w, resp, "Reply draft created")
}

func defaultReplySubject(ctx context.Context, entryID int64) (string, error) {
	resp, err := sdk.GetHTML(ctx, fmt.Sprintf("/entries/%d/replies/new", entryID))
	if err != nil {
		return "", convertSDKError(err)
	}
	subject := parseMessageSubject(string(resp.Data))
	if subject == "" {
		return "", output.ErrAPI(0, "could not determine reply draft subject")
	}
	return subject, nil
}

func updateDraft(ctx context.Context, w io.Writer, draftID int64, draft draftFormRequest) error {
	senderID, err := sdk.DefaultSenderID(ctx)
	if err != nil {
		return convertSDKError(err)
	}

	values := draftValues(senderID, draft)
	values.Set("_method", "patch")

	resp, err := submitDraftForm(ctx, "POST", fmt.Sprintf("/messages/%d", draftID), values)
	if err != nil {
		return err
	}
	if resp.ID == 0 {
		resp.ID = draftID
		resp.URL = fmt.Sprintf("%s/messages/%d", strings.TrimRight(cfg.BaseURL, "/"), draftID)
		resp.EditURL = resp.URL + "/edit"
	}

	return writeDraftSaved(w, resp, "Draft updated")
}

func deleteDraft(ctx context.Context, w io.Writer, draftID int64) error {
	values := url.Values{}
	values.Set("_method", "delete")
	values.Set("status", "drafted")

	if _, err := submitDraftForm(ctx, "POST", fmt.Sprintf("/messages/%d", draftID), values); err != nil {
		return err
	}

	if writer.IsStyled() {
		fmt.Fprintf(w, "Draft %d deleted.\n", draftID)
		return nil
	}

	return writeOK(map[string]int64{"id": draftID}, output.WithSummary("Draft deleted"))
}

func draftValues(senderID int64, draft draftFormRequest) url.Values {
	values := url.Values{}
	values.Set("acting_sender_id", fmt.Sprintf("%d", senderID))
	values.Set("entry[status]", "drafted")
	values.Set("message[subject]", draft.Subject)
	values.Set("message[content]", draft.Content)
	for _, to := range draft.To {
		values.Add("entry[addressed][directly][]", to)
	}
	for _, cc := range draft.CC {
		values.Add("entry[addressed][copied][]", cc)
	}
	for _, bcc := range draft.BCC {
		values.Add("entry[addressed][blindcopied][]", bcc)
	}
	return values
}

func submitDraftForm(ctx context.Context, method, path string, values url.Values) (draftResponse, error) {
	reqURL := strings.TrimRight(cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, strings.NewReader(values.Encode()))
	if err != nil {
		return draftResponse{}, err
	}
	if err := authMgr.AuthenticateRequest(ctx, req); err != nil {
		return draftResponse{}, output.ErrAuth(err.Error())
	}
	req.Header.Set("User-Agent", "hey-cli")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return draftResponse{}, output.ErrNetwork(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return draftResponse{}, output.ErrAPI(resp.StatusCode, msg)
	}

	location := resp.Header.Get("Location")
	return draftResponseFromLocation(location), nil
}

func draftResponseFromLocation(location string) draftResponse {
	if location == "" {
		return draftResponse{}
	}
	location = strings.TrimRight(location, "/")
	id, _ := strconv.ParseInt(location[strings.LastIndex(location, "/")+1:], 10, 64)
	return draftResponse{
		ID:      id,
		URL:     location,
		EditURL: location + "/edit",
	}
}

func parseMessageSubject(pageHTML string) string {
	input := messageSubjectInputRe.FindString(pageHTML)
	if input == "" {
		return ""
	}
	match := valueAttrRe.FindStringSubmatch(input)
	if match == nil {
		return ""
	}
	return html.UnescapeString(match[1])
}

func writeDraftSaved(w io.Writer, resp draftResponse, summary string) error {
	if writer.IsStyled() {
		if resp.ID > 0 {
			fmt.Fprintf(w, "%s: %d\n", summary, resp.ID)
		} else {
			fmt.Fprintln(w, summary+".")
		}
		return nil
	}
	return writeOK(resp, output.WithSummary(summary))
}

func draftMessage(inline string) (string, error) {
	if inline != "" {
		return inline, nil
	}
	if !stdinIsTerminal() {
		message, err := readStdin()
		if err != nil {
			return "", err
		}
		if message == "" {
			return "", output.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
		}
		return message, nil
	}

	message, err := editor.Open("")
	if err != nil {
		return "", output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
	}
	if message == "" {
		return "", output.ErrUsage("empty message, aborting")
	}
	return message, nil
}
