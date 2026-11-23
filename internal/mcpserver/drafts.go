package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/mcp/catalog"
	"github.com/basecamp/mcp/gateway"
)

// Draft-first: every served operation that can deliver mail saves a draft
// instead, the rule hey-mcp-server holds its own dispatcher to (sending-safety
// D1). The entry's status is forced to "drafted", and the entry may carry only
// its recipients. That refuses a delivered status, scheduled delivery (a
// deferred send), and the send-side triage flags (mark_done, pop,
// scheduled_bubble_up) that would move the thread in the same request a send
// would. The body schema's typo guard checks only top-level names, so this
// allowlist is what holds the entry's shape.
//
// An update never carries scheduled_delivery, and HEY clears an existing
// schedule when an update omits it, so editing a scheduled draft unschedules
// it rather than letting it go out later.
//
// These actions never deliver. Delivery is send_draft (send.go), in its own hey_send
// tool, which asks the person to confirm each email. It is served only when hey mcp's
// sign-in can send, and listed only to a client that can be asked. A person can also
// send the draft from HEY or with `hey draft send <id>`.
// TestEveryServedOpenWorldOperationIsDraftFirst holds this list total: mapping
// another operation that can reach people outside the mailbox fails until it
// has a rule here.
var draftFirst = map[string]bool{
	"CreateMessage": true,
	"CreateReply":   true,
	"UpdateMessage": true,
}

// draftEntryKeys are the entry properties a draft save may carry.
var draftEntryKeys = map[string]bool{"addressed": true, "status": true}

const drafted = "drafted"

// howToSend is how a draft this server saved gets delivered, as every client is
// told it: it names no tool, since hey_send is listed only to a client that can be
// asked (server.go). The draft saves' notes name hey_send to a client it is listed.
const howToSend = "the person sends it with one press of their own: from its page in HEY, with `hey draft send <id>`, or, where this assistant can ask them to confirm a send, on that confirmation"

// draftOnly returns params with the entry forced to a draft, or an error
// naming what would have delivered. params is not modified.
func draftOnly(action string, params map[string]any) (map[string]any, error) {
	entry := map[string]any{}
	if raw, ok := params["entry"]; ok && raw != nil {
		given, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s saves a draft: entry must be an object", action)
		}
		for _, key := range slices.Sorted(maps.Keys(given)) {
			if !draftEntryKeys[key] {
				return nil, fmt.Errorf("%s saves a draft and doesn't take entry.%s: it never sends or schedules mail, and triage has its own actions (allowed: entry.%s). Nothing was sent to HEY. To deliver, save the draft, then %s",
					action, key, strings.Join(slices.Sorted(maps.Keys(draftEntryKeys)), ", entry."), howToSend)
			}
		}
		if status, ok := given["status"]; ok && status != drafted {
			return nil, fmt.Errorf("%s saves a draft: entry.status can only be %q, because it never sends mail. Nothing was sent to HEY. To deliver, save the draft, then %s",
				action, drafted, howToSend)
		}
		maps.Copy(entry, given)
	}
	entry["status"] = drafted

	out := maps.Clone(params)
	if out == nil {
		out = map[string]any{}
	}
	out["entry"] = entry
	return out, nil
}

// draftEditPath is GetMessageEdit's path, where keepSavedSender reads the
// identity a draft was saved as. TestDraftEditPathIsTheModels holds it to the
// catalog.
const draftEditPath = "/messages/{messageId}/edit.json"

// keepSavedSender gives an update_message that leaves out acting_sender_id the
// identity the draft was saved as: its sender, else its creator
// (hey.DraftSentAs). HEY revises a draft from the whole request, and a revision
// without acting_sender_id drops the draft's sender, so a draft saved from a
// shared or alternate address would quietly move to the account's own. A
// sender the caller chose is left as it is. params is not modified.
func keepSavedSender(ctx context.Context, api API, action string, params map[string]any) (map[string]any, error) {
	if given, ok := params["acting_sender_id"]; ok && given != nil {
		return params, nil
	}
	id, err := scalarString(params["messageId"])
	if _, given := params["messageId"]; !given || err != nil {
		// buildRequest names the missing or unusable path parameter.
		return params, nil //nolint:nilerr // reported by buildRequest instead
	}
	unread := func(why string) error {
		return fmt.Errorf("%s keeps the address a draft sends from, and couldn't read draft %s's: %s. Nothing was changed. Pass acting_sender_id to choose it",
			action, id, why)
	}
	resp, err := api.Get(ctx, strings.ReplaceAll(draftEditPath, "{messageId}", url.PathEscape(id)))
	if err != nil {
		return nil, unread(heyReason(err))
	}
	var edit generated.MessageEditState
	var page struct {
		Postings any `json:"postings"`
	}
	if json.Unmarshal(resp.Data, &edit) != nil || json.Unmarshal(resp.Data, &page) != nil {
		return nil, unread("HEY's answer was unreadable")
	}
	// HEY answers the edit page of a sent or deleted draft by redirecting to
	// the Imbox, which the client follows; only the draft's own answer names
	// the identity it was saved as.
	if strconv.FormatInt(edit.Id, 10) != id || page.Postings != nil {
		return nil, unread("it isn't a draft any more: it was sent or deleted")
	}
	sentAs := hey.DraftSentAs(&edit).Id
	if sentAs == 0 {
		return nil, unread("HEY named no sender for it")
	}
	out := maps.Clone(params)
	out["acting_sender_id"] = sentAs
	return out, nil
}

// savedAsDraft reports whether HEY acknowledged a draft save: 204 and a
// Location naming the draft (haystack's respond_with_location_url). Anything
// else is unconfirmed. Delivery answers the sent message, and an entry HEY
// didn't keep as a draft answers a redirect that net/http follows to a 200.
func savedAsDraft(resp *hey.Response) bool {
	return resp.StatusCode == http.StatusNoContent && resp.Headers.Get("Location") != ""
}

// unconfirmedDraft is the error for a save HEY didn't acknowledge as a draft.
func unconfirmedDraft(action string, resp *hey.Response) string {
	missing := ""
	if resp.StatusCode == http.StatusNoContent {
		missing = " with no Location"
	}
	return fmt.Sprintf("%s may not have saved a draft: HEY answered HTTP %d%s, not the 204 and Location a saved draft gets, so it may have been sent. Check the thread and Sent in HEY before trying again.",
		action, resp.StatusCode, missing)
}

// refusedByHEY reports whether err is HEY declining the request outright: a
// 4xx answer other than 408, which HEY sends without acting on it. Anything
// else (a transport failure, a timeout, a 5xx) leaves open whether the save
// landed.
func refusedByHEY(err error) bool {
	status := heyUpstream(err, gateway.KindWrite, "").Status
	return status >= 400 && status < 500 && status != http.StatusRequestTimeout
}

// unsettledDraft is the error for a draft save whose outcome is unknown: the
// request may have reached HEY, but no answer says whether it acted.
func unsettledDraft(action string, err error) string {
	return fmt.Sprintf("%s may have saved a draft: %s. HEY may have acted before the request failed. %s",
		action, heyReason(err), unsettledDraftNext)
}

// unsettledDraftNext is what to do after a draft save whose outcome is
// unknown.
const unsettledDraftNext = "Check the thread and Drafts in HEY before trying again; a retry could save a second copy."

// draftPath is a draft save's Location path, …/messages/{entry_id}, as the
// SDK's own draft calls read it.
var draftPath = regexp.MustCompile(`(?:^|/)messages/(\d+)/?$`)

// draftID reads the draft's entry id, the id `hey draft send` takes, out of
// a draft save's Location, or "" when it doesn't name one.
func draftID(location string) string {
	u, err := url.Parse(location)
	if err != nil {
		return ""
	}
	if m := draftPath.FindStringSubmatch(u.Path); m != nil {
		return m[1]
	}
	return ""
}

// draftLink is the page that opens a saved draft in HEY, or "" when its
// Location names none. The Location is the draft's resource,
// <origin>/messages/{entry_id}, which in a browser opens its thread; the
// draft's page is that URL's /edit (haystack's edit_message_url, which HEY's
// Drafts listing serves as edit_url), on the origin HEY answered from. A
// relative Location names no origin, so it gives no link rather than a guess.
func draftLink(location string) string {
	u, err := url.Parse(location)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return ""
	}
	m := draftPath.FindStringSubmatch(u.Path)
	if m == nil || strings.TrimSuffix(u.Path, "/") != "/messages/"+m[1] {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/messages/" + m[1] + "/edit"
}

// draftResult is what a confirmed draft save answers, so the model doesn't
// report a send that didn't happen and knows how the draft gets delivered.
// sendable is whether this client is offered hey_send (server.go): where it is,
// the person's ask to send is answered with send_draft at once, since its
// confirmation shows them what goes out; where it isn't, the note names the
// draft's page in HEY.
func draftResult(resp *hey.Response, sendable bool) map[string]any {
	location := resp.Headers.Get("Location")
	result := map[string]any{"status": resp.StatusCode, "location": location, "drafted": true}
	id := draftID(location)
	if id == "" {
		// Without a draft_id there's nothing to pass send_draft, whatever the client.
		result["note"] = "Saved as a draft, not sent. The person sends it from Drafts in HEY."
		return result
	}
	result["draft_id"] = id
	link := draftLink(location)
	if link != "" {
		result["app_url"] = link
	}
	switch {
	case sendable:
		result["note"] = "Saved as a draft, not sent. The person can review or revise it as they like. When they ask to send it, call hey_send's send_draft with draft_id " + id + " right away: its confirmation shows them what goes out, so don't show the draft or ask them again first."
	case link != "":
		result["note"] = "Saved as a draft, not sent. hey mcp can't send it from this client: the person sends it from its page in HEY, " + link + ", or with `hey draft send " + id + "`."
	default:
		result["note"] = "Saved as a draft, not sent. hey mcp can't send it from this client: the person sends it from HEY or with `hey draft send " + id + "`."
	}
	return result
}

// draftDoc leads the description of each draft-first operation, so describe
// tells the caller what this server does rather than what the wire can do.
const draftDoc = "Through hey mcp this only saves a draft: entry.status is always \"drafted\", and entry takes only addressed. It never delivers or schedules mail, and a request to send, schedule or triage is refused before HEY is contacted. Updating a scheduled draft clears its schedule. Updating a draft without acting_sender_id keeps the address it sends from. To deliver, " + howToSend + ". HEY's description of the endpoint follows; its delivery and scheduling paths aren't reachable here.\n\n"

// holdToDrafts rewrites each draft-first operation in the catalog to
// describe the draft-only contract: its summary (which the tool description
// lists) says so, its doc leads with draftDoc, and its body schema's entry
// offers only addressed and a status that can only be "drafted". Describe
// serves the operation as is, so this is what a client reading the schema
// sees.
func holdToDrafts(cat *catalog.Catalog) error {
	for _, d := range cat.Domains {
		for _, op := range d.Operations {
			if !draftFirst[op.ID] {
				continue
			}
			body, err := draftBody(op.Body)
			if err != nil {
				return fmt.Errorf("%s: %w", op.ID, err)
			}
			if op.ID == "UpdateMessage" {
				// The model reuses CreateMessage's body, which requires
				// acting_sender_id; an update may leave it out to keep the
				// address the draft sends from (keepSavedSender).
				dropRequired(body, "acting_sender_id")
			}
			op.Body = body
			op.Summary = strings.TrimSuffix(op.Summary, ".") + " (saved as a draft, never sent)"
			op.Doc = draftDoc + op.Doc
		}
	}
	return nil
}

// dropRequired takes name out of a JSON schema's required list.
func dropRequired(schema map[string]any, name string) {
	required, _ := schema["required"].([]any)
	kept := make([]any, 0, len(required))
	for _, r := range required {
		if r != name {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		delete(schema, "required")
		return
	}
	schema["required"] = kept
}

// draftBody returns a copy of body whose entry allows only draftEntryKeys,
// with status pinned to "drafted".
func draftBody(body map[string]any) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	props, _ := out["properties"].(map[string]any)
	entry, _ := props["entry"].(map[string]any)
	entryProps, _ := entry["properties"].(map[string]any)
	if entryProps == nil {
		return nil, fmt.Errorf("body has no entry properties to hold to a draft")
	}
	for key := range entryProps {
		if !draftEntryKeys[key] {
			delete(entryProps, key)
		}
	}
	entryProps["status"] = map[string]any{
		"type":        "string",
		"enum":        []any{drafted},
		"description": "Always \"drafted\": hey mcp saves drafts and never delivers. Omit it or pass \"drafted\".",
	}
	entry["additionalProperties"] = false
	return out, nil
}
