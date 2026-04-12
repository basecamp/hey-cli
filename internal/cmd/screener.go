package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
)

// screenerItem represents a pending item in The Screener.
type screenerItem struct {
	PostingID string `json:"posting_id"`
	Sender    string `json:"sender"`
	Subject   string `json:"subject"`
}

type screenerCommand struct {
	cmd *cobra.Command
}

func newScreenerCommand() *screenerCommand {
	sc := &screenerCommand{}
	sc.cmd = &cobra.Command{
		Use:   "screener",
		Short: "Manage The Screener (pending sender approvals)",
		Annotations: map[string]string{
			"agent_notes": "List pending screener items and approve/deny senders. Use approve/deny/spam/feed/trail subcommands with posting ID.",
		},
		Example: `  hey screener
  hey screener approve 123456
  hey screener deny 123456
  hey screener spam 123456
  hey screener feed 123456
  hey screener trail 123456`,
		RunE: sc.list,
	}

	sc.cmd.AddCommand(&cobra.Command{
		Use:   "approve <posting-id>",
		Short: "Screen in a sender to Imbox",
		Args:  cobra.ExactArgs(1),
		RunE:  sc.approve,
	})
	sc.cmd.AddCommand(&cobra.Command{
		Use:   "deny <posting-id>",
		Short: "Screen out a sender",
		Args:  cobra.ExactArgs(1),
		RunE:  sc.deny,
	})
	sc.cmd.AddCommand(&cobra.Command{
		Use:   "spam <posting-id>",
		Short: "Mark a sender as spam",
		Args:  cobra.ExactArgs(1),
		RunE:  sc.markSpam,
	})
	sc.cmd.AddCommand(&cobra.Command{
		Use:   "feed <posting-id>",
		Short: "Screen in a sender to The Feed",
		Args:  cobra.ExactArgs(1),
		RunE:  sc.feed,
	})
	sc.cmd.AddCommand(&cobra.Command{
		Use:   "trail <posting-id>",
		Short: "Screen in a sender to Paper Trail",
		Args:  cobra.ExactArgs(1),
		RunE:  sc.trail,
	})

	return sc
}

func (sc *screenerCommand) list(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	items, feedBoxID, trailBoxID, err := fetchScreenerItems(cmd.Context())
	if err != nil {
		return err
	}
	_ = feedBoxID
	_ = trailBoxID

	if writer.IsStyled() {
		if len(items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "The Screener is empty.")
			return nil
		}
		table := newTable(cmd.OutOrStdout())
		table.addRow([]string{"Posting ID", "Sender", "Subject"})
		for _, item := range items {
			table.addRow([]string{item.PostingID, item.Sender, item.Subject})
		}
		table.print()
		fmt.Fprintf(cmd.OutOrStdout(), "\n%d pending\n", len(items))
		return nil
	}

	return writeOK(items,
		output.WithSummary(fmt.Sprintf("%d pending", len(items))),
		output.WithBreadcrumbs(
			output.Breadcrumb{
				Action:      "approve",
				Command:     "hey screener approve <id>",
				Description: "Screen in to Imbox",
			},
			output.Breadcrumb{
				Action:      "deny",
				Command:     "hey screener deny <id>",
				Description: "Screen out sender",
			},
		),
	)
}

func (sc *screenerCommand) approve(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	return patchClearance(cmd, args[0], url.Values{
		"status": {"approved"},
	}, "Screened in to Imbox")
}

func (sc *screenerCommand) deny(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	return patchClearance(cmd, args[0], url.Values{
		"status": {"denied"},
	}, "Screened out")
}

func (sc *screenerCommand) markSpam(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	return patchClearance(cmd, args[0], url.Values{
		"status": {"denied"},
		"spam":   {"true"},
	}, "Marked as spam")
}

func (sc *screenerCommand) feed(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	_, feedBoxID, _, err := fetchScreenerItems(cmd.Context())
	if err != nil {
		return err
	}
	if feedBoxID == "" {
		return fmt.Errorf("could not determine Feed box ID")
	}

	return patchClearance(cmd, args[0], url.Values{
		"status":             {"approved"},
		"designation_box_id": {feedBoxID},
	}, "Screened in to Feed")
}

func (sc *screenerCommand) trail(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	_, _, trailBoxID, err := fetchScreenerItems(cmd.Context())
	if err != nil {
		return err
	}
	if trailBoxID == "" {
		return fmt.Errorf("could not determine Paper Trail box ID")
	}

	return patchClearance(cmd, args[0], url.Values{
		"status":             {"approved"},
		"designation_box_id": {trailBoxID},
	}, "Screened in to Paper Trail")
}

// fetchScreenerItems parses the /clearances HTML page to extract pending items
// and the feed/trail box IDs from form hidden inputs.
func fetchScreenerItems(ctx context.Context) ([]screenerItem, string, string, error) {
	body, err := authenticatedGet(ctx, "/clearances")
	if err != nil {
		return nil, "", "", err
	}

	var items []screenerItem
	var feedBoxID, trailBoxID string

	// Extract emails as posting identifiers
	// Pattern: forms POSTing to /clearances/<id>
	postingIDs := regexp.MustCompile(`action="/clearances/(\d+)"`).FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	var uniqueIDs []string
	for _, m := range postingIDs {
		if !seen[m[1]] {
			seen[m[1]] = true
			uniqueIDs = append(uniqueIDs, m[1])
		}
	}

	// Extract emails
	emailRe := regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	allEmails := emailRe.FindAllString(body, -1)

	// Filter out the user's own emails (they appear in nav/header)
	senderEmails := []string{}
	for _, e := range allEmails {
		lower := strings.ToLower(e)
		if lower != "erik.dahl@hey.com" && lower != "erik@parrotapp.com" {
			senderEmails = append(senderEmails, e)
		}
	}

	// Extract subjects
	subjectRe := regexp.MustCompile(`<span[^>]*>([^<]*(?:Re:|Fwd:|We'|Your |Hi |Hello|Thank|Welcome|Confirm|Order|Invoice|Receipt|Upgrade)[^<]*)</span>`)
	subjects := subjectRe.FindAllStringSubmatch(body, -1)
	var subjectTexts []string
	for _, s := range subjects {
		text := strings.TrimSpace(s[1])
		if text != "" {
			subjectTexts = append(subjectTexts, text)
		}
	}

	// Extract feed/trail box IDs from hidden form inputs
	feedRe := regexp.MustCompile(`data-clearances-target="feedboxButton"[^<]*<input[^>]*name="designation_box_id"[^>]*value="(\d+)"`)
	if m := feedRe.FindStringSubmatch(body); m != nil {
		feedBoxID = m[1]
	}
	// Alternative: find feedbox button form
	if feedBoxID == "" {
		// Look for the pattern: feedboxButton form has designation_box_id
		feedForms := regexp.MustCompile(`feedboxButton.*?designation_box_id.*?value="(\d+)"`).FindStringSubmatch(body)
		if feedForms != nil {
			feedBoxID = feedForms[1]
		}
	}

	trailRe := regexp.MustCompile(`data-clearances-target="trailboxButton"[^<]*<input[^>]*name="designation_box_id"[^>]*value="(\d+)"`)
	if m := trailRe.FindStringSubmatch(body); m != nil {
		trailBoxID = m[1]
	}
	if trailBoxID == "" {
		trailForms := regexp.MustCompile(`trailboxButton.*?designation_box_id.*?value="(\d+)"`).FindStringSubmatch(body)
		if trailForms != nil {
			trailBoxID = trailForms[1]
		}
	}

	// Build items: match posting IDs with senders and subjects
	for i, id := range uniqueIDs {
		item := screenerItem{PostingID: id}
		if i < len(senderEmails) {
			item.Sender = senderEmails[i]
		}
		if i < len(subjectTexts) {
			item.Subject = subjectTexts[i]
		}
		items = append(items, item)
	}

	return items, feedBoxID, trailBoxID, nil
}

// patchClearance sends a PATCH request to /clearances/<id> with the given form values.
func patchClearance(cmd *cobra.Command, postingID string, values url.Values, successMsg string) error {
	ctx := cmd.Context()
	values.Set("_method", "patch")

	reqURL := cfg.BaseURL + "/clearances/" + postingID
	req, err := http.NewRequestWithContext(ctx, "PATCH", reqURL, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if err := authMgr.AuthenticateRequest(ctx, req); err != nil {
		return err
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 302 && resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "%s (posting %s)\n", successMsg, postingID)
		return nil
	}

	return writeOK(map[string]string{
		"posting_id": postingID,
		"action":     successMsg,
	})
}

// authenticatedGet makes an authenticated GET request and returns the response body as a string.
func authenticatedGet(ctx context.Context, path string) (string, error) {
	reqURL := cfg.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html")

	if err := authMgr.AuthenticateRequest(ctx, req); err != nil {
		return "", err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("unexpected status %d for %s", resp.StatusCode, path)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(bodyBytes), nil
}
