package mcpserver

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basecamp/mcp/gateway"
)

// send_draft: delivery, confirmed by the person and single-use (sending-safety D2-D3).
//
// It is served in a tool of its own, hey_send, so its annotations can say what it is
// (destructive, open world) without marking every mail read so (D7). It is served only
// when hey mcp's credential holds send and read, never under --read-only, and is listed
// only to a client that can be asked (server.go): a client that can't answer an
// elicitation isn't offered a tool it can't complete.
//
// A call runs in two rounds. The first fetches the draft and asks the person to confirm
// it through MCP elicitation, with the draft's fingerprint signed into the request
// state. The prompt shows the draft and names whatever its text can't show
// (sendprompt.go). The second, carrying the person's answer and that state, fetches the
// draft again, refuses it if anything the fingerprint covers changed, claims the draft,
// and sends once, writing every delivered field from the snapshot it just verified. A
// client on the 2026-07-28 protocol carries the rounds itself (SEP-2322); for an older
// client the SDK elicits and calls back in the same request. A client that declares no
// elicitation and calls from a cached list gets a draft and a link (D3).
//
// How much a person's agent sends, to whom and when is the person's call once they've
// granted send: sending-safety D4 (caps, new recipients) and D6 (the exposure window)
// were withdrawn on 2026-10-31, and send_draft refuses no draft for what it holds. The
// claim is per process. Across processes (two MCP clients each running hey mcp), HEY's
// own draft lock delivers a draft once (haystack#9163); the claim keeps this process from
// asking HEY twice, or asking the person again about a draft it sent.
//
// What isn't here yet: send_at (scheduled delivery, which D2 also routes through
// send_draft). A draft that's already scheduled is refused.

// Drafts is the slice of hey-sdk send_draft drives. SendDraft must go through a client
// that never retries, because a retried send can deliver twice (D2.4).
type Drafts interface {
	GetEdit(ctx context.Context, draftID int64) (*generated.MessageEditState, error)
	SendDraft(ctx context.Context, draftID int64, draft hey.DraftContent) (*generated.SentMessage, error)
}

// SendConfig serves send_draft. Nil serves none.
type SendConfig struct {
	Drafts Drafts
	// GrantKey names hey mcp's sign-in this process started on. A confirmation is bound
	// to it, so an answer given under one sign-in isn't redeemed under another.
	GrantKey string
	// Origin is the HEY this runs against, e.g. https://app.hey.com: links point there.
	Origin string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// CurrentGrant answers the sign-in's key as stored now, renewing a token about to
	// expire first, so the send that may follow goes out on a live one. When it no
	// longer matches GrantKey (hey mcp was signed in again or out since this process
	// started, or a renewal narrowed the grant below sending), sends are refused until
	// hey mcp restarts. Nil skips the check.
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

	// claimTTL is how long a claimed draft stays claimed: well past the confirmation's
	// TTL and HEY's undo window (D2.3).
	claimTTL = 24 * time.Hour

	// bodyPromptCap is how much of the body the confirmation shows (D2).
	bodyPromptCap = 1500
	// headerCap bounds each single-line header field in the confirmation.
	headerCap = 200
)

type sender struct {
	cfg SendConfig
	// key signs confirmations. It lives as long as the process: both rounds of a send
	// reach the same hey mcp, and a confirmation that outlives a restart is asked again.
	key    []byte
	claims *sendClaims
}

func newSender(cfg SendConfig) (*sender, error) {
	switch {
	case cfg.Drafts == nil:
		return nil, errors.New("send_draft needs HEY's drafts")
	case cfg.GrantKey == "":
		return nil, errors.New("send_draft needs the sign-in it sends on")
	case cfg.Origin == "":
		return nil, errors.New("send_draft needs the HEY origin")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("make the confirmation key: %w", err)
	}
	return &sender{cfg: cfg, key: key, claims: newSendClaims()}, nil
}

// sendClaims claims each draft id once per process (D2.3). A claim is never released,
// even when HEY refuses the send, so there's no rollback to get wrong.
type sendClaims struct {
	mu     sync.Mutex
	claims map[int64]time.Time
}

func newSendClaims() *sendClaims {
	return &sendClaims{claims: map[int64]time.Time{}}
}

// claimed reports whether a draft was claimed within claimTTL.
func (c *sendClaims) claimed(id int64, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.claims[id]
	return ok && now.Sub(at) < claimTTL
}

// claim takes a draft id, or reports it already taken.
func (c *sendClaims) claim(id int64, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, at := range c.claims {
		if now.Sub(at) >= claimTTL {
			delete(c.claims, k)
		}
	}
	if _, taken := c.claims[id]; taken {
		return false
	}
	c.claims[id] = now
	return true
}

const sendDescription = "Send a saved draft: the only way hey mcp delivers mail. " +
	"Call it as soon as the person asks to send a draft. It asks them to confirm in this app, showing who it goes to (To, Cc and Bcc), the subject, attachments, links, images and the body, and naming anything it can't show, so don't show the draft again or ask them again first. " +
	"Only their answer to that confirmation sends it: a yes in the chat doesn't. " +
	"The confirmation is bound to that exact draft, so a draft changed after it was approved isn't sent, and each draft is sent once. " +
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
	if !canElicitForm(req) {
		return s.notSent(draftID, "This MCP client can't show a confirmation, so hey mcp can't send from it."), nil
	}

	if req.Params.RequestState == "" {
		return s.ask(ctx, draftID)
	}
	return s.confirmAndSend(ctx, draftID, req.Params.RequestState, req.Params.InputResponses)
}

// ask is the first round: review the draft and ask the person to confirm it.
func (s *sender) ask(ctx context.Context, draftID int64) (*mcp.CallToolResult, error) {
	// A draft this process sent isn't put to the person again.
	if s.claims.claimed(draftID, s.cfg.Now()) {
		return alreadySent(), nil
	}
	snap, refusal := s.review(ctx, draftID)
	if refusal != nil {
		return refusal, nil
	}

	state := s.sign(confirmState{
		Kind: confirmKind, Method: confirmMethod,
		Grant: s.cfg.GrantKey, Origin: s.cfg.Origin, Draft: draftID,
		Fingerprint: snap.fingerprint,
		Expires:     s.cfg.Now().Add(confirmTTL).Unix(),
	})

	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{confirmInputID: confirmElicitation(s.prompt(snap))},
		RequestState:  state,
	}, nil
}

func alreadySent() *mcp.CallToolResult {
	return gateway.ErrorResult("not sent: hey mcp already sent this draft, or a send of it is in progress or failed; it won't send it again for 24 hours. Check Sent in HEY")
}

// confirmAndSend is the second round: the person's answer, then the checks again, the
// claim, and the one send.
func (s *sender) confirmAndSend(ctx context.Context, draftID int64, rawState string, responses mcp.InputResponseMap) (*mcp.CallToolResult, error) {
	state, err := s.verify(rawState)
	if err != nil {
		return gateway.ErrorResult("the confirmation for this send isn't valid (%v), so nothing was sent; call send_draft again to ask again", err), nil
	}
	if state.Draft != draftID || state.Grant != s.cfg.GrantKey || state.Origin != s.cfg.Origin {
		return gateway.ErrorResult("the confirmation was for a different draft or sign-in, so nothing was sent; call send_draft again"), nil
	}

	answer, _ := responses[confirmInputID].(*mcp.ElicitResult)
	if answer == nil || answer.Action != "accept" || answer.Content["confirm"] != true {
		return s.notSent(draftID, "The send wasn't approved."), nil
	}

	snap, refusal := s.review(ctx, draftID)
	if refusal != nil {
		return refusal, nil
	}
	if !hmac.Equal([]byte(snap.fingerprint), []byte(state.Fingerprint)) {
		return gateway.ErrorResult("not sent: the draft changed since you approved it (its recipients, sender, subject, body or attachments differ). Call send_draft again to review it as it is now"), nil
	}

	// The review's reads may have refreshed the token, and a refresh can narrow the
	// grant. Check again just before claiming, so a send HEY would refuse doesn't
	// block the draft for a day.
	if err = s.checkGrant(ctx); err != nil {
		return gateway.ErrorResult("not sent: %v", err), nil
	}
	if !s.claims.claim(draftID, s.cfg.Now()) {
		return alreadySent(), nil
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
	if current != s.cfg.GrantKey {
		return errors.New("hey mcp was signed in again, or signed out, since it started, or its sign-in can no longer send; restart it")
	}
	return nil
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

// clientRequest is any request that says what its client can do: a 2026-07-28 request
// carries it in _meta, an older one has it from the session's initialize.
type clientRequest interface {
	ClientCapabilities() *mcp.ClientCapabilities
}

// canElicitForm reports whether the client can be asked: it declared form elicitation.
// A client that declares the capability with neither mode is read as form, as the SDK
// does.
func canElicitForm(req mcp.Request) bool {
	r, ok := req.(clientRequest)
	if !ok {
		return false
	}
	caps := r.ClientCapabilities()
	if caps == nil || caps.Elicitation == nil {
		return false
	}
	return caps.Elicitation.Form != nil || caps.Elicitation.URL == nil
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
	body           reviewedBody
	scheduled      time.Time
	fingerprint    string
}

// content is the delivery request: every field from the verified snapshot, so an edit
// landing after the final fetch is overwritten by what was approved (D2). HEY replaces
// the whole recipient set when addressed is present (haystack Entry::Addressed#address),
// so a Bcc added concurrently is dropped rather than delivered. The body is the one the
// fingerprint hashed, attachments rebuilt from their identity (sendbody.go).
func (d *draftSnapshot) content() hey.DraftContent {
	return hey.DraftContent{
		Subject:        d.subject,
		Content:        d.body.delivered,
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

func snapshotOf(edit *generated.MessageEditState) (*draftSnapshot, error) {
	body, err := analyzeBody(edit.Content)
	if err != nil {
		return nil, err
	}
	snap := &draftSnapshot{
		id:        edit.Id,
		from:      edit.Sender,
		to:        edit.Addressed.Directly,
		cc:        edit.Addressed.Copied,
		bcc:       edit.Addressed.Blindcopied,
		subject:   edit.Subject,
		body:      body,
		scheduled: edit.ScheduledDeliveryAt,
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

// review fetches the draft, returning a refusal as the call's result when the draft
// can't be sent here: one HEY no longer serves for editing, one already scheduled, or
// one with no recipients. Nothing the draft holds refuses it; the prompt names what it
// can't show.
func (s *sender) review(ctx context.Context, draftID int64) (*draftSnapshot, *mcp.CallToolResult) {
	refuse := func(format string, a ...any) (*draftSnapshot, *mcp.CallToolResult) {
		return nil, gateway.ErrorResult(format, a...)
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
	snap, err := snapshotOf(edit)
	if err != nil {
		return refuse("not sent: %v", err)
	}

	recipients := snap.recipients()
	switch {
	case !snap.scheduled.IsZero():
		return refuse("not sent: this draft is already scheduled; change or send it in HEY: %s", s.editURL(draftID))
	case len(recipients) == 0:
		return refuse("not sent: the draft has no recipients; add them with update_message (entry.addressed)")
	}
	for _, r := range recipients {
		if normalizedAddress(r.EmailAddress) == "" {
			return refuse("not sent: a recipient has no email address")
		}
	}
	return snap, nil
}

// --- the signed confirmation state ---

// confirmState is what the request state carries between the rounds: the draft's
// fingerprint, bound to the sign-in and the HEY, with an expiry. It is signed with the
// process's key, because a 2026-07-28 client holds it between the rounds.
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
