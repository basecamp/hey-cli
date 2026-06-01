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
	"time"

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
	messageContentInputRe = regexp.MustCompile(`(?s)<input[^>]+name="message\[content\]"[^>]*>`)
	csrfMetaRe            = regexp.MustCompile(`(?s)<meta[^>]+name="csrf-token"[^>]*>`)
	optionRe              = regexp.MustCompile(`(?s)<option[^>]*\bselected\b[^>]*>`)
	valueAttrRe           = regexp.MustCompile(`\svalue="([^"]*)"`)
	contentAttrRe         = regexp.MustCompile(`\scontent="([^"]*)"`)
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
		Short:   "Update a saved draft without sending",
		Example: `  hey draft update 12345 --to alice@example.com --subject "Hello" -m "Updated body"`,
		Args:    usageExactOneArg(),
		RunE:    c.run,
	}

	cmd.Flags().StringVar(&c.to, "to", "", "Recipient email address(es)")
	cmd.Flags().StringVar(&c.cc, "cc", "", "CC recipient email address(es)")
	cmd.Flags().StringVar(&c.bcc, "bcc", "", "BCC recipient email address(es)")
	cmd.Flags().StringVar(&c.subject, "subject", "", "Message subject")
	cmd.Flags().StringVarP(&c.message, "message", "m", "", "Draft body (or opens $EDITOR)")

	return cmd
}

func (c *draftUpdateCommand) run(cmd *cobra.Command, args []string) error {
	draftID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid draft ID: %s", args[0]))
	}
	flags := cmd.Flags()
	if !flags.Changed("subject") && !flags.Changed("to") && !flags.Changed("cc") && !flags.Changed("bcc") && !flags.Changed("message") {
		return output.ErrUsageHint("No update fields specified", "hey draft update <draft-id> --subject <subject> -m <message>")
	}

	if err := requireAuth(); err != nil {
		return err
	}

	existing, err := loadMessageDraft(cmd.Context(), draftID)
	if err != nil {
		return err
	}
	draft := existing.Request

	if flags.Changed("subject") {
		draft.Subject = c.subject
	}
	if flags.Changed("to") {
		draft.To = parseAddresses(c.to)
	}
	if flags.Changed("cc") {
		draft.CC = parseAddresses(c.cc)
	}
	if flags.Changed("bcc") {
		draft.BCC = parseAddresses(c.bcc)
	}

	if flags.Changed("message") {
		message, err := draftMessageWithInitial(c.message, draft.Content, flags.Changed("message"))
		if err != nil {
			return err
		}
		draft.Content = message
	}

	return updateDraft(cmd.Context(), cmd.OutOrStdout(), draftID, draft, existing.CSRFToken)
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

type draftFormState struct {
	Request    draftFormRequest
	CSRFToken  string
	HasSubject bool
	HasContent bool
	HasTo      bool
	HasCC      bool
	HasBCC     bool
}

func createMessageDraft(ctx context.Context, w io.Writer, draft draftFormRequest) error {
	senderID, err := sdk.DefaultSenderID(ctx)
	if err != nil {
		return convertSDKError(err)
	}
	csrfToken, err := loadCSRFToken(ctx, "/messages/new")
	if err != nil {
		return err
	}

	values := draftValues(senderID, draft)
	resp, err := submitDraftForm(ctx, "POST", "/messages", values, csrfToken)
	if err != nil {
		return err
	}

	return writeDraftSaved(w, resp, "Draft created")
}

func createReplyDraft(ctx context.Context, w io.Writer, threadID int64, draft draftFormRequest) error {
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
	if len(draft.To) == 0 && len(draft.CC) == 0 && len(draft.BCC) == 0 {
		draft.To = addressed.To
		draft.CC = addressed.CC
		draft.BCC = addressed.BCC
	}

	return createReplyDraftForEntry(ctx, w, latestEntryID, draft)
}

func createReplyDraftForEntry(ctx context.Context, w io.Writer, latestEntryID int64, draft draftFormRequest) error {
	senderID, err := sdk.DefaultSenderID(ctx)
	if err != nil {
		return convertSDKError(err)
	}

	replyForm, err := loadReplyDraftForm(ctx, latestEntryID)
	if err != nil {
		return err
	}
	if draft.Subject == "" {
		draft.Subject = replyForm.Request.Subject
	}

	values := draftValues(senderID, draft)
	resp, err := submitDraftForm(ctx, "POST", fmt.Sprintf("/entries/%d/replies", latestEntryID), values, replyForm.CSRFToken)
	if err != nil {
		return err
	}

	return writeDraftSaved(w, resp, "Reply draft created")
}

func loadReplyDraftForm(ctx context.Context, entryID int64) (draftFormState, error) {
	resp, err := sdk.GetHTML(ctx, fmt.Sprintf("/entries/%d/replies/new", entryID))
	if err != nil {
		return draftFormState{}, convertSDKError(err)
	}
	state := parseDraftForm(string(resp.Data))
	if !state.HasSubject || state.Request.Subject == "" {
		return draftFormState{}, output.ErrAPI(0, "could not determine reply draft subject")
	}
	if state.CSRFToken == "" {
		return draftFormState{}, output.ErrAPI(0, "could not determine reply draft authenticity token")
	}
	return state, nil
}

func loadMessageDraft(ctx context.Context, draftID int64) (draftFormState, error) {
	resp, err := sdk.GetHTML(ctx, fmt.Sprintf("/messages/%d/edit", draftID))
	if err != nil {
		return draftFormState{}, convertSDKError(err)
	}
	state := parseDraftForm(string(resp.Data))
	if !state.HasSubject || !state.HasContent || !state.HasTo || !state.HasCC || !state.HasBCC {
		return draftFormState{}, output.ErrAPI(0, "could not parse draft edit form")
	}
	if state.CSRFToken == "" {
		return draftFormState{}, output.ErrAPI(0, "could not determine draft authenticity token")
	}
	return state, nil
}

func loadCSRFToken(ctx context.Context, path string) (string, error) {
	resp, err := sdk.GetHTML(ctx, path)
	if err != nil {
		return "", convertSDKError(err)
	}
	token := parseCSRFToken(string(resp.Data))
	if token == "" {
		return "", output.ErrAPI(0, "could not determine draft authenticity token")
	}
	return token, nil
}

func updateDraft(ctx context.Context, w io.Writer, draftID int64, draft draftFormRequest, csrfToken string) error {
	senderID, err := sdk.DefaultSenderID(ctx)
	if err != nil {
		return convertSDKError(err)
	}

	values := draftValues(senderID, draft)
	values.Set("_method", "patch")

	resp, err := submitDraftForm(ctx, "POST", fmt.Sprintf("/messages/%d", draftID), values, csrfToken)
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
	csrfToken, err := loadCSRFToken(ctx, fmt.Sprintf("/messages/%d/edit", draftID))
	if err != nil {
		return err
	}
	values := url.Values{}
	values.Set("_method", "delete")
	values.Set("status", "drafted")

	if _, err := submitDraftForm(ctx, "POST", fmt.Sprintf("/messages/%d", draftID), values, csrfToken); err != nil {
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

func submitDraftForm(ctx context.Context, method, path string, values url.Values, csrfToken string) (draftResponse, error) {
	if csrfToken != "" {
		values.Set("authenticity_token", csrfToken)
	}
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
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}

	client := httpClient
	if client == nil {
		client = http.DefaultClient
	}
	start := time.Now()
	resp, err := client.Do(req)
	trackDraftRequest(time.Since(start))
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

func trackDraftRequest(duration time.Duration) {
	if sdkStats == nil {
		return
	}
	sdkStats.requestCount.Add(1)
	sdkStats.totalLatency.Add(int64(duration))
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
	subject, _ := parseMessageSubjectField(pageHTML)
	return subject
}

func parseMessageSubjectField(pageHTML string) (string, bool) {
	input := messageSubjectInputRe.FindString(pageHTML)
	if input == "" {
		return "", false
	}
	match := valueAttrRe.FindStringSubmatch(input)
	if match == nil {
		return "", false
	}
	return html.UnescapeString(match[1]), true
}

func parseDraftForm(pageHTML string) draftFormState {
	subject, hasSubject := parseMessageSubjectField(pageHTML)
	content, hasContent := parseMessageContentField(pageHTML)
	to, hasTo := parseSelectedAddressesField(pageHTML, "entry[addressed][directly][]")
	cc, hasCC := parseSelectedAddressesField(pageHTML, "entry[addressed][copied][]")
	bcc, hasBCC := parseSelectedAddressesField(pageHTML, "entry[addressed][blindcopied][]")
	return draftFormState{
		Request: draftFormRequest{
			Subject: subject,
			Content: content,
			To:      to,
			CC:      cc,
			BCC:     bcc,
		},
		CSRFToken:  parseCSRFToken(pageHTML),
		HasSubject: hasSubject,
		HasContent: hasContent,
		HasTo:      hasTo,
		HasCC:      hasCC,
		HasBCC:     hasBCC,
	}
}

func parseMessageContent(pageHTML string) string {
	content, _ := parseMessageContentField(pageHTML)
	return content
}

func parseMessageContentField(pageHTML string) (string, bool) {
	input := messageContentInputRe.FindString(pageHTML)
	if input == "" {
		return "", false
	}
	match := valueAttrRe.FindStringSubmatch(input)
	if match == nil {
		return "", false
	}
	return html.UnescapeString(match[1]), true
}

func parseSelectedAddresses(pageHTML, fieldName string) []string {
	addresses, _ := parseSelectedAddressesField(pageHTML, fieldName)
	return addresses
}

func parseSelectedAddressesField(pageHTML, fieldName string) ([]string, bool) {
	selectRe := regexp.MustCompile(`(?s)<select[^>]+name="` + regexp.QuoteMeta(fieldName) + `"[^>]*>(.*?)</select>`)
	match := selectRe.FindStringSubmatch(pageHTML)
	if match == nil {
		return nil, false
	}

	var addresses []string
	for _, option := range optionRe.FindAllString(match[1], -1) {
		valueMatch := valueAttrRe.FindStringSubmatch(option)
		if valueMatch == nil {
			return nil, false
		}
		addresses = append(addresses, html.UnescapeString(valueMatch[1]))
	}
	return addresses, true
}

func parseCSRFToken(pageHTML string) string {
	meta := csrfMetaRe.FindString(pageHTML)
	if meta == "" {
		return ""
	}
	match := contentAttrRe.FindStringSubmatch(meta)
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
	return draftMessageWithInitial(inline, "", inline != "")
}

func draftMessageWithInitial(inline, initial string, inlineChanged bool) (string, error) {
	if inline != "" {
		return inline, nil
	}
	if inlineChanged {
		return "", nil
	}
	if !stdinIsTerminal() {
		message, err := readStdin()
		if err != nil {
			return "", err
		}
		if message == "" {
			if initial != "" {
				return initial, nil
			}
			return "", output.ErrUsage("no message provided (use -m or --message to provide inline, or pipe to stdin)")
		}
		return message, nil
	}

	message, err := editor.Open(initial)
	if err != nil {
		return "", output.ErrAPI(0, fmt.Sprintf("could not open editor: %v", err))
	}
	if message == "" {
		return "", output.ErrUsage("empty message, aborting")
	}
	return message, nil
}
