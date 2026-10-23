package mcpserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basecamp/mcp/gateway"
)

// send_draft: delivery, confirmed by the person and single-use (sending-safety D2-D6).
//
// It is served in a tool of its own, hey_send, so its annotations can say what it is
// (destructive, open world) without marking every mail read so (D7). It is served only
// when hey mcp's credential holds send and read, and never under --read-only.
//
// A call runs in two rounds. The first fetches the draft, applies the recipient and
// exposure policy (D4, D6), and asks the person to confirm through MCP elicitation, with
// the draft's fingerprint signed into the request state. The second, carrying the
// person's answer and that state, fetches the draft again, refuses it if anything the
// fingerprint covers changed, applies the policy again, claims the draft and charges the
// caps in one locked update of the state file, and sends once, writing every delivered
// field from the snapshot it just verified. A client on the 2026-07-28 protocol carries
// the rounds itself (SEP-2322); for an older client the SDK elicits and calls back in
// the same request. A client that declares no elicitation gets a draft and a link (D3).
//
// What isn't here yet: send_at (scheduled delivery, which D2 also routes through
// send_draft). A draft that's already scheduled is refused.

// Drafts is the slice of hey-sdk send_draft drives. GetEdit, Identity and Search read;
// SendDraft must go through a client that never retries, because a retried send can
// deliver twice (D2.4).
type Drafts interface {
	GetEdit(ctx context.Context, draftID int64) (*generated.MessageEditState, error)
	SendDraft(ctx context.Context, draftID int64, draft hey.DraftContent) (*generated.SentMessage, error)
	Identity(ctx context.Context) (*generated.Identity, error)
	Search(ctx context.Context, params hey.SearchParams) (*generated.AdvancedSearchResult, error)
}

// Grant is the security principal on stdio: hey mcp's sign-in (D4).
type Grant struct {
	// Key names the sign-in; caps and exposure are counted per key.
	Key string
	// GrantedAt is when it was made. Only mail sent before it makes a recipient known.
	GrantedAt time.Time
}

// SendConfig serves send_draft. Nil serves none.
type SendConfig struct {
	Drafts Drafts
	State  *SendState
	Grant  Grant
	// Origin is the HEY this runs against, e.g. https://app.hey.com: links point there,
	// and claims are keyed by it.
	Origin string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// CurrentGrant answers the sign-in's key as stored now. When it no longer matches
	// Grant.Key (hey mcp was signed in again or out since this process started, and the
	// token the client authenticates with may be the new one), sends and exposing reads
	// are refused until hey mcp restarts, so caps and exposure never count against a
	// sign-in other than the one the requests ride. Nil skips the check.
	CurrentGrant func(context.Context) (string, error)
}

const (
	sendToolName   = "hey_send"
	sendDraftName  = "send_draft"
	confirmInputID = "confirm_send"

	// confirmKind and confirmMethod mark a signed state as a send_draft confirmation,
	// so no other validator accepts it (basecamp-mcp-server's confirm.go).
	confirmKind   = "send_draft"
	confirmMethod = "tools/call\x00send_draft"

	// confirmTTL: a send confirmation should not outlive the person's attention (D2).
	confirmTTL = 5 * time.Minute

	// D4's caps. Starting values to tune from beta usage.
	maxRecipients         = 20
	maxSendsPerDay        = 50
	maxNewAddressesPerDay = 10

	// bodyPromptCap is how much of the body the confirmation shows (D2).
	bodyPromptCap = 1500
	// headerCap bounds each single-line header field in the confirmation.
	headerCap = 200
)

type sender struct {
	cfg SendConfig
	key []byte
}

func newSender(cfg SendConfig) (*sender, error) {
	switch {
	case cfg.Drafts == nil || cfg.State == nil:
		return nil, errors.New("send_draft needs HEY's drafts and a state file")
	case cfg.Grant.Key == "":
		return nil, errors.New("send_draft needs a sign-in to key its state on")
	case cfg.Origin == "":
		return nil, errors.New("send_draft needs the HEY origin")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	key, err := cfg.State.ConfirmKey(context.Background())
	if err != nil {
		return nil, err
	}
	return &sender{cfg: cfg, key: key}, nil
}

const sendDescription = "Send a saved draft: the only way hey mcp delivers mail. " +
	"Each send asks the person to confirm it in this app, showing who it goes to (To, Cc and Bcc), the subject, attachments and the body. " +
	"The confirmation is bound to that exact draft, so a draft changed after it was approved isn't sent, and each draft can be sent through hey mcp once. " +
	"A client that can't show a confirmation gets the draft's link in HEY instead, and nothing is sent: saying yes in the chat doesn't send it. " +
	"Drafts to someone this account hasn't emailed before are capped per day, and refused for a while after reading mail that may be from an unscreened sender. " +
	"A draft that is already scheduled isn't sent here. Actions: send_draft (params: draft_id, the id create_message or create_reply returned), describe."

func (s *sender) tool() *mcp.Tool {
	destructive, openWorld := true, true
	return &mcp.Tool{
		Name:        sendToolName,
		Title:       "Send a HEY draft",
		Description: sendDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []any{sendDraftName, gateway.DescribeAction}},
				"params": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"draft_id": map[string]any{"type": []any{"integer", "string"}, "description": "The draft's id, from create_message, create_reply or list_drafts."},
					},
				},
			},
			"required": []any{"action"},
		},
		Annotations: &mcp.ToolAnnotations{
			Title:           "Send a HEY draft",
			ReadOnlyHint:    false,
			DestructiveHint: &destructive,
			IdempotentHint:  false,
			OpenWorldHint:   &openWorld,
		},
	}
}

func (s *sender) handle(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		Action string         `json:"action"`
		Params map[string]any `json:"params"`
	}
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return gateway.ErrorResult("invalid arguments: %v", err), nil
		}
	}
	switch args.Action {
	case gateway.DescribeAction:
		return gateway.JSONResult(map[string]any{
			"action":      sendDraftName,
			"description": sendDescription,
			"params":      map[string]any{"draft_id": "integer (required)"},
		})
	case sendDraftName:
	default:
		return gateway.ErrorResult("unknown action %q in %s (actions: %s, %s)", args.Action, sendToolName, sendDraftName, gateway.DescribeAction), nil
	}

	draftID, err := draftIDParam(args.Params)
	if err != nil {
		return gateway.ErrorResult("%v", err), nil
	}
	if err := s.checkGrant(ctx); err != nil {
		return gateway.ErrorResult("not sent: %v", err), nil
	}

	// A host that can't show the person a confirmation can't send (D3). There is no
	// "I confirm" parameter: a value the model supplies is what an injected
	// instruction would supply.
	if !canElicitForm(req.Session) {
		return s.notSent(draftID, "This MCP client can't show a confirmation, so hey mcp can't send from it."), nil
	}

	if req.Params.RequestState == "" {
		return s.ask(ctx, draftID)
	}
	return s.confirmAndSend(ctx, draftID, req.Params.RequestState, req.Params.InputResponses)
}

// ask is the first round: review the draft and ask the person to confirm it.
func (s *sender) ask(ctx context.Context, draftID int64) (*mcp.CallToolResult, error) {
	snap, outcome, refusal := s.review(ctx, draftID)
	if refusal != nil {
		return refusal, nil
	}

	state := s.sign(confirmState{
		Kind: confirmKind, Method: confirmMethod,
		Grant: s.cfg.Grant.Key, Origin: s.cfg.Origin, Draft: draftID,
		Fingerprint: snap.fingerprint,
		Expires:     s.cfg.Now().Add(confirmTTL).Unix(),
	})

	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{confirmInputID: confirmElicitation(s.prompt(snap, outcome))},
		RequestState:  state,
	}, nil
}

// confirmAndSend is the second round: the person's answer, then the checks again, the
// claim, and the one send.
func (s *sender) confirmAndSend(ctx context.Context, draftID int64, rawState string, responses mcp.InputResponseMap) (*mcp.CallToolResult, error) {
	state, err := s.verify(rawState)
	if err != nil {
		return gateway.ErrorResult("the confirmation for this send isn't valid (%v), so nothing was sent; call send_draft again to ask again", err), nil
	}
	if state.Draft != draftID || state.Grant != s.cfg.Grant.Key || state.Origin != s.cfg.Origin {
		return gateway.ErrorResult("the confirmation was for a different draft or sign-in, so nothing was sent; call send_draft again"), nil
	}

	answer, _ := responses[confirmInputID].(*mcp.ElicitResult)
	if answer == nil || answer.Action != "accept" || answer.Content["confirm"] != true {
		return s.notSent(draftID, "The send wasn't approved."), nil
	}

	// The approval covers content, never a policy decision: the policy runs again.
	snap, outcome, refusal := s.review(ctx, draftID)
	if refusal != nil {
		return refusal, nil
	}
	if !hmac.Equal([]byte(snap.fingerprint), []byte(state.Fingerprint)) {
		return gateway.ErrorResult("not sent: the draft changed since you approved it (its recipients, sender, subject, body or attachments differ). Call send_draft again to review it as it is now"), nil
	}

	if err = s.cfg.State.claim(ctx, s.cfg.Origin, draftID, s.cfg.Grant.Key, outcome.newList, outcome.unseen, s.cfg.Now()); err != nil {
		var refused *refusalError
		if errors.As(err, &refused) {
			return gateway.ErrorResult("%s", refused.reason), nil
		}
		return gateway.ErrorResult("not sent: hey mcp couldn't record the send (%v), so it didn't risk sending it twice. Send it from HEY", err), nil
	}

	// From here the draft is claimed for good: the claim is never released, even when
	// HEY refuses, so there's no rollback to get wrong (D2.3).
	sent, err := s.cfg.Drafts.SendDraft(ctx, draftID, snap.content())
	if err != nil {
		if refusedByHEY(err) {
			return gateway.ErrorResult("HEY refused the send: %v. The draft is still a draft, and hey mcp won't send it again for 24 hours; send it from HEY: %s", err, s.editURL(draftID)), nil
		}
		return gateway.ErrorResult("the send may have gone out: %v. Check Sent in HEY before doing anything else; hey mcp won't send this draft again", err), nil
	}

	// HEY refuses a delivery it won't make (usually the account's sending limit) by
	// keeping the draft and redirecting to its edit page, which reads as a message with
	// an id and no thread. Only an edit page served says it's still a draft (as
	// mail.ConfirmDelivered reads it for hey draft send).
	if sent != nil && sent.Id != 0 && sent.TopicId == 0 {
		if edit, editErr := s.cfg.Drafts.GetEdit(ctx, sent.Id); editErr == nil && edit != nil {
			return gateway.ErrorResult("not sent: HEY kept the draft instead of sending it, usually because the account reached its sending limit. hey mcp won't send this draft again for 24 hours; send it from HEY when the limit allows: %s", s.editURL(draftID)), nil
		}
	}

	result := map[string]any{"sent": true, "draft_id": draftID, "note": "Sent through HEY's undo window. The person approved it in this app."}
	if sent != nil {
		if sent.Id != 0 {
			result["message_id"] = sent.Id
		}
		if sent.TopicId != 0 {
			result["topic_id"] = sent.TopicId
		}
		result["delayed"] = sent.Delayed
	}
	return gateway.JSONResult(result)
}

// checkGrant refuses when the stored sign-in isn't the one this process started on.
func (s *sender) checkGrant(ctx context.Context) error {
	if s.cfg.CurrentGrant == nil {
		return nil
	}
	current, err := s.cfg.CurrentGrant(ctx)
	if err != nil {
		return fmt.Errorf("hey mcp couldn't read its sign-in (%w)", err)
	}
	if current != s.cfg.Grant.Key {
		return errors.New("hey mcp was signed in again, or signed out, since it started; restart it")
	}
	return nil
}

// markExposed records an exposing read against this process's sign-in, refusing when
// the stored sign-in has changed under it.
func (s *sender) markExposed(ctx context.Context, action string) error {
	if err := s.checkGrant(ctx); err != nil {
		return err
	}
	return s.cfg.State.MarkExposed(ctx, s.cfg.Grant.Key, action, s.cfg.Now())
}

// notSent is D3's answer: not an error, a draft and where to send it.
func (s *sender) notSent(draftID int64, why string) *mcp.CallToolResult {
	url := s.editURL(draftID)
	result, _ := gateway.JSONResult(map[string]any{
		"sent":     false,
		"draft_id": draftID,
		"edit_url": url,
		"note":     why + " Draft saved but not sent. Review and send it in HEY: " + url,
	})
	return result
}

// heyHost is the host of the HEY this sender runs against.
func (s *sender) heyHost() string {
	u, err := url.Parse(s.cfg.Origin)
	if err != nil {
		return ""
	}
	return u.Host
}

func (s *sender) editURL(draftID int64) string {
	return fmt.Sprintf("%s/messages/%d/edit", strings.TrimRight(s.cfg.Origin, "/"), draftID)
}

func draftIDParam(params map[string]any) (int64, error) {
	raw, ok := params["draft_id"]
	if !ok {
		return 0, errors.New("send_draft needs params.draft_id, the id create_message or create_reply returned")
	}
	var id int64
	switch v := raw.(type) {
	case float64:
		id = int64(v)
		if float64(id) != v {
			id = 0
		}
	case string:
		id, _ = strconv.ParseInt(v, 10, 64)
	}
	if id <= 0 {
		return 0, fmt.Errorf("params.draft_id must be a draft's id, got %v", raw)
	}
	for key := range params {
		if key != "draft_id" {
			return 0, fmt.Errorf("send_draft takes only params.draft_id, not %q", key)
		}
	}
	return id, nil
}

// canElicitForm reports whether the client declared form elicitation. A client that
// declares the capability with neither mode is read as form, as the SDK does.
func canElicitForm(session *mcp.ServerSession) bool {
	if session == nil {
		return false
	}
	params := session.InitializeParams()
	if params == nil || params.Capabilities == nil || params.Capabilities.Elicitation == nil {
		return false
	}
	caps := params.Capabilities.Elicitation
	return caps.Form != nil || caps.URL == nil
}

func confirmElicitation(message string) *mcp.ElicitParams {
	return &mcp.ElicitParams{
		Mode:    "form",
		Message: message,
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"confirm": map[string]any{
					"type":        "boolean",
					"title":       "Send this email",
					"description": "Check to send it now. Anything else, or declining, sends nothing.",
				},
			},
			"required": []any{"confirm"},
		},
	}
}

// --- the draft as reviewed ---

// draftSnapshot is a draft as send_draft fetched it, and what it would deliver.
type draftSnapshot struct {
	id             int64
	from           generated.Contact
	actingSenderID int64
	to, cc, bcc    []generated.Contact
	subject        string
	rawContent     string
	body           reviewedBody
	scheduled      time.Time
	fingerprint    string
}

// content is the delivery request: every field from the verified snapshot, so an edit
// landing after the final fetch is overwritten by what was approved (D2). HEY replaces
// the whole recipient set when addressed is present (haystack Entry::Addressed#address),
// so a Bcc added concurrently is dropped rather than delivered.
func (d *draftSnapshot) content() hey.DraftContent {
	return hey.DraftContent{
		Subject:        d.subject,
		Content:        d.rawContent,
		To:             emails(d.to),
		CC:             emails(d.cc),
		BCC:            emails(d.bcc),
		ActingSenderID: d.actingSenderID,
	}
}

func (d *draftSnapshot) recipients() []generated.Contact {
	return slices.Concat(d.to, d.cc, d.bcc)
}

func emails(contacts []generated.Contact) []string {
	out := make([]string, 0, len(contacts))
	for _, c := range contacts {
		out = append(out, c.EmailAddress)
	}
	return out
}

func normalizedAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

func snapshotOf(edit *generated.MessageEditState, heyHost string) (*draftSnapshot, error) {
	body, err := analyzeBody(edit.Content, heyHost)
	if err != nil {
		return nil, err
	}
	snap := &draftSnapshot{
		id:         edit.Id,
		from:       edit.Sender,
		to:         edit.Addressed.Directly,
		cc:         edit.Addressed.Copied,
		bcc:        edit.Addressed.Blindcopied,
		subject:    edit.Subject,
		rawContent: edit.Content,
		body:       body,
		scheduled:  edit.ScheduledDeliveryAt,
	}
	// HEY omits sender when the draft is sent as its creator contact, and the identity
	// the draft was saved as rides the send (cmd's draftContentFrom does the same).
	if snap.from.Id == 0 {
		snap.from = edit.Creator
	}
	snap.actingSenderID = snap.from.Id

	sorted := func(contacts []generated.Contact) []string {
		addresses := emails(contacts)
		for i := range addresses {
			addresses[i] = normalizedAddress(addresses[i])
		}
		slices.Sort(addresses)
		return addresses
	}
	fingerprint, err := json.Marshal(map[string]any{
		"v":            1,
		"draft_id":     snap.id,
		"from_id":      snap.actingSenderID,
		"from":         normalizedAddress(snap.from.EmailAddress),
		"to":           sorted(snap.to),
		"cc":           sorted(snap.cc),
		"bcc":          sorted(snap.bcc),
		"subject":      snap.subject,
		"attachments":  body.attachments,
		"body_sha256":  body.hash,
		"mode":         "now",
		"scheduled_at": nil,
	})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(fingerprint)
	snap.fingerprint = hex.EncodeToString(sum[:])
	return snap, nil
}

// reviewOutcome is what the policy found, for the prompt.
type reviewOutcome struct {
	// unseen: the confirmation can't show everything the draft transmits.
	unseen       bool
	newList      []string
	newAddresses map[string]bool
	exposed      bool
	exposedBy    string
}

// review fetches the draft and applies the policy, returning a refusal as the call's
// result when the draft can't be sent here.
func (s *sender) review(ctx context.Context, draftID int64) (*draftSnapshot, *reviewOutcome, *mcp.CallToolResult) {
	refuse := func(format string, a ...any) (*draftSnapshot, *reviewOutcome, *mcp.CallToolResult) {
		return nil, nil, gateway.ErrorResult(format, a...)
	}

	edit, err := s.cfg.Drafts.GetEdit(ctx, draftID)
	if err != nil {
		var apiErr *hey.Error
		if errors.As(err, &apiErr) && (apiErr.HTTPStatus == http.StatusNotFound || apiErr.HTTPStatus == http.StatusUnprocessableEntity) {
			return refuse("not sent: %d isn't a draft HEY can send (it may have been sent, trashed or deleted)", draftID)
		}
		return refuse("not sent: couldn't read draft %d: %v", draftID, err)
	}
	// HEY serves a message's edit state only while it's a live draft (haystack
	// MessagesController#ensure_revisable_entry: drafted and not delayed), so reading it
	// is the check that it hasn't been trashed or delivered.
	if edit == nil || edit.Id != draftID {
		return refuse("not sent: %d isn't a draft HEY can send", draftID)
	}
	snap, err := snapshotOf(edit, s.heyHost())
	if err != nil {
		return refuse("not sent: %v", err)
	}

	recipients := snap.recipients()
	switch {
	case !snap.scheduled.IsZero():
		return refuse("not sent: this draft is already scheduled; change or send it in HEY: %s", s.editURL(draftID))
	case len(recipients) == 0:
		return refuse("not sent: the draft has no recipients; add them with update_message (entry.addressed)")
	case len(recipients) > maxRecipients:
		return refuse("not sent: hey mcp sends to at most %d recipients across To, Cc and Bcc, and this draft has %d. Send it from HEY: %s", maxRecipients, len(recipients), s.editURL(draftID))
	case snap.body.unverifiable != "":
		return refuse("not sent: the draft's body has an attachment hey mcp can't verify (%s), so the confirmation couldn't show what it sends. Send it from HEY: %s", snap.body.unverifiable, s.editURL(draftID))
	case snap.body.remote != "":
		return refuse("not sent: the draft's body has a reference a mail client loads by itself (%s), which can carry mailbox data to whoever hosts it. Send it from HEY: %s", snap.body.remote, s.editURL(draftID))
	}
	for _, r := range recipients {
		if normalizedAddress(r.EmailAddress) == "" {
			return refuse("not sent: a recipient has no email address")
		}
	}

	newAddresses := s.newAddresses(ctx, snap)
	exposed, by, err := s.cfg.State.exposure(ctx, s.cfg.Grant.Key, s.cfg.Now())
	if err != nil {
		// Without the marker the server can't know, so it treats the grant as exposed
		// (D6), and the claim would need the same file anyway.
		return refuse("not sent: hey mcp couldn't read its send state (%v). Send it from HEY", err)
	}
	if exposed && len(newAddresses) > 0 {
		return refuse("not sent: this conversation read mail that may be from an unscreened sender (%s) in the last %d minutes, and the draft is addressed to someone you haven't emailed before (%s). Send it from HEY: %s",
			by, int(exposureWindow/time.Minute), newAddresses[0], s.editURL(draftID))
	}

	// Measured on the escaped text the prompt shows: escapes lengthen it, and what
	// falls past the cut is unseen however short the raw text was.
	unseen := !snap.body.lossless || utf8.RuneCountInString(inertBody(snap.body.text)) > bodyPromptCap
	if unseen && (len(newAddresses) > 0 || exposed) {
		return refuse("not sent: the draft can't be fully reviewed here (it has an attachment, more than %d characters, or formatting that can hide text), and it goes to someone you haven't emailed before or this conversation read unscreened mail. Send it from HEY: %s",
			bodyPromptCap, s.editURL(draftID))
	}

	sends, charged, err := s.cfg.State.budget(ctx, s.cfg.Grant.Key, s.cfg.Now())
	if err != nil {
		return refuse("not sent: hey mcp couldn't read its send state (%v). Send it from HEY", err)
	}
	if sends >= maxSendsPerDay {
		return refuse("not sent: hey mcp sends at most %d messages a day on one sign-in, and this one has reached it. Send it from HEY: %s", maxSendsPerDay, s.editURL(draftID))
	}
	uncharged := 0
	for _, address := range newAddresses {
		if !charged[address] {
			uncharged++
		}
	}
	if len(charged)+uncharged > maxNewAddressesPerDay {
		return refuse("not sent: hey mcp sends to at most %d new addresses a day on one sign-in, and this draft would go past it. Send it from HEY: %s", maxNewAddressesPerDay, s.editURL(draftID))
	}

	outcome := &reviewOutcome{unseen: unseen, newList: newAddresses, newAddresses: map[string]bool{}, exposed: exposed, exposedBy: by}
	for _, address := range newAddresses {
		outcome.newAddresses[address] = true
	}
	return snap, outcome, nil
}

// --- known recipients (D4) ---

// newAddresses returns the draft's recipients that aren't known, normalized and in
// order. Known means history the agent can't manufacture: mail this account sent to the
// address before the sign-in existed. Contact records don't count, since a model could
// create one. Anything send_draft can't prove known, a failed lookup included, is new.
func (s *sender) newAddresses(ctx context.Context, snap *draftSnapshot) []string {
	var addresses []string
	for _, r := range snap.recipients() {
		address := normalizedAddress(r.EmailAddress)
		if !slices.Contains(addresses, address) {
			addresses = append(addresses, address)
		}
	}

	known := s.knownAddresses(ctx, addresses)
	var fresh []string
	for _, address := range addresses {
		if !known[address] {
			fresh = append(fresh, address)
		}
	}
	return fresh
}

// knownAddresses looks each address up in HEY's search for a message to it from one of
// this identity's addresses, delivered before the sign-in. The search only narrows: each
// match is checked here against the entry's own creator, time and recipients, so a
// search that matches loosely can't make an address known.
func (s *sender) knownAddresses(ctx context.Context, addresses []string) map[string]bool {
	known := map[string]bool{}
	if s.cfg.Grant.GrantedAt.IsZero() || len(addresses) == 0 {
		return known
	}
	identity, err := s.cfg.Drafts.Identity(ctx)
	if err != nil || identity == nil {
		return known
	}
	mine := map[string]bool{}
	if address := normalizedAddress(identity.PrimaryContact.EmailAddress); address != "" {
		mine[address] = true
	}
	for _, sender := range identity.Senders {
		if address := normalizedAddress(sender.EmailAddress); address != "" {
			mine[address] = true
		}
	}

	for _, address := range addresses {
		result, err := s.cfg.Drafts.Search(ctx, hey.SearchParams{To: address})
		if err != nil || result == nil {
			continue
		}
		known[address] = sentBefore(result, address, mine, s.cfg.Grant.GrantedAt)
	}
	return known
}

func sentBefore(result *generated.AdvancedSearchResult, address string, mine map[string]bool, before time.Time) bool {
	for _, match := range result.Matches {
		for _, entry := range match.Entries {
			// ActiveAt is when the entry went out; CreatedAt would count a draft made
			// before the sign-in and sent after it. No delivery time, no proof.
			if !mine[normalizedAddress(entry.Creator.EmailAddress)] || entry.ActiveAt.IsZero() || !entry.ActiveAt.Before(before) {
				continue
			}
			for _, r := range slices.Concat(entry.Addressed.Directly, entry.Addressed.Copied, entry.Addressed.Blindcopied) {
				if normalizedAddress(r.EmailAddress) == address {
					return true
				}
			}
		}
	}
	return false
}

// --- the signed confirmation state ---

// confirmState is what the request state carries between the rounds: the draft's
// fingerprint, bound to the sign-in and the HEY, with an expiry. It is signed with the
// installation's key, because a 2026-07-28 client holds it between the rounds.
type confirmState struct {
	Kind        string `json:"k"`
	Method      string `json:"m"`
	Grant       string `json:"g"`
	Origin      string `json:"o"`
	Draft       int64  `json:"d"`
	Fingerprint string `json:"f"`
	Expires     int64  `json:"e"`
}

func (s *sender) sign(state confirmState) string {
	// A struct of strings and integers always marshals.
	payload, _ := json.Marshal(state)
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *sender) verify(raw string) (*confirmState, error) {
	encoded, signature, ok := strings.Cut(raw, ".")
	if !ok {
		return nil, errors.New("malformed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("malformed")
	}
	given, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return nil, errors.New("malformed")
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	if !hmac.Equal(given, mac.Sum(nil)) {
		return nil, errors.New("signature doesn't match")
	}
	var state confirmState
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, errors.New("malformed")
	}
	if state.Kind != confirmKind || state.Method != confirmMethod {
		return nil, errors.New("not a send confirmation")
	}
	if !s.cfg.Now().Before(time.Unix(state.Expires, 0)) {
		return nil, fmt.Errorf("it expired; a confirmation lasts %d minutes", int(confirmTTL/time.Minute))
	}
	return &state, nil
}
