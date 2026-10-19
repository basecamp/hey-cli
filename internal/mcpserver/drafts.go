package mcpserver

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/mcp/catalog"
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
// MCP has no way to deliver mail yet: there is no send_draft action. A person
// sends the draft from HEY or with `hey draft send <id>`.
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

// howToSend is how a person delivers a draft this server saved.
const howToSend = "a person sends it from HEY or with `hey draft send <id>`"

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
				return nil, fmt.Errorf("%s saves a draft and doesn't take entry.%s: hey mcp never sends or schedules mail, and triage has its own actions (allowed: entry.%s). Nothing was sent to HEY. To deliver, save the draft, then %s",
					action, key, strings.Join(slices.Sorted(maps.Keys(draftEntryKeys)), ", entry."), howToSend)
			}
		}
		if status, ok := given["status"]; ok && status != drafted {
			return nil, fmt.Errorf("%s saves a draft: entry.status can only be %q, because hey mcp never sends mail. Nothing was sent to HEY. To deliver, save the draft, then %s",
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

// draftResult is what a confirmed draft save answers, so the model doesn't
// report a send that didn't happen and knows how the draft gets delivered.
func draftResult(resp *hey.Response) map[string]any {
	location := resp.Headers.Get("Location")
	result := map[string]any{"status": resp.StatusCode, "location": location, "drafted": true}
	if id := draftID(location); id != "" {
		result["draft_id"] = id
		result["note"] = "Saved as a draft, not sent. hey mcp doesn't deliver mail: a person reviews and sends it from HEY, or with `hey draft send " + id + "`."
	} else {
		result["note"] = "Saved as a draft, not sent. hey mcp doesn't deliver mail: " + howToSend + "."
	}
	return result
}

// draftDoc leads the description of each draft-first operation, so describe
// tells the caller what this server does rather than what the wire can do.
const draftDoc = "Through hey mcp this only saves a draft: entry.status is always \"drafted\", and entry takes only addressed. It never delivers or schedules mail, and a request to send, schedule or triage is refused before HEY is contacted. Updating a scheduled draft clears its schedule. To deliver, " + howToSend + ". HEY's description of the endpoint follows; its delivery and scheduling paths aren't reachable here.\n\n"

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
			op.Body = body
			op.Summary = strings.TrimSuffix(op.Summary, ".") + " (saved as a draft, never sent)"
			op.Doc = draftDoc + op.Doc
		}
	}
	return nil
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
