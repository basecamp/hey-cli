package mcpserver

import (
	"fmt"
	"net/http"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basecamp/mcp/gateway"

	"github.com/basecamp/hey-cli/internal/auth"
)

// A failed HEY call is answered in hey mcp's words, never in the text the
// hey-sdk client built from it: that text can carry the request's URL (a
// 404's "Resource not found: https://…"), and a network failure's the Go
// error beneath it.
//
//   - A 5xx, a timeout or a network failure is answered in the toolkit's
//     words (gateway.UpstreamFailure): HEY failed, a write may or may not
//     have been made, a send may or may not have gone out, and HEY's
//     request ID when it sent one.
//   - A 404 is answered in the toolkit's fixed words for it: the client's
//     text for it is the URL.
//   - Every other 4xx keeps its text, which is HEY's own words for the
//     person or the client's fixed sentence for the status: a 422's
//     reasons ("Subject can't be blank"), a 403's "Access denied", a 401's
//     "Authentication failed", a 429's "Try again later", another 4xx's
//     error or message from HEY's body.

// heyUpstream describes a failed HEY call for the toolkit.
func heyUpstream(err error, kind gateway.Kind, next string) gateway.Upstream {
	call := gateway.Upstream{Product: "HEY", Kind: kind, Next: next}
	if e := hey.AsError(err); e != nil {
		call.Status, call.RequestID = e.HTTPStatus, e.RequestID
		// The client's raw requests name a 404 by its code alone.
		if call.Status == 0 && e.Code == hey.CodeNotFound {
			call.Status = http.StatusNotFound
		}
		// The client wraps every transport failure as a network error, and
		// may redact the cause to a stand-in the chain can't name.
		call.Network = e.Code == hey.CodeNetwork
	}
	call.Refusals = call.Status == http.StatusNotFound
	return call
}

// heyUpstreamFailure answers err in the toolkit's words when it is HEY
// failing, not answering, or a 404, and is nil otherwise. A sign-in renewal
// that failed is answered in hey mcp's words for it: the request was never
// sent.
func heyUpstreamFailure(err error, kind gateway.Kind, next string) *mcp.CallToolResult {
	if err == nil {
		return nil
	}
	if reason, failed := refreshReason(err); failed {
		// Nothing reached HEY, so a write's or a draft's check-first step
		// doesn't apply; a send keeps its own, since the draft stays claimed.
		if kind != gateway.KindSend || next == "" {
			next = "Try again later."
		}
		return gateway.ErrorResult("Error: %s, so this request wasn't sent\nNext: %s", reason, next)
	}
	return gateway.UpstreamFailure(err, heyUpstream(err, kind, next))
}

// refreshReason says why renewing hey mcp's sign-in failed, when it did, by
// the token endpoint's status alone: its body is never shown.
func refreshReason(err error) (string, bool) {
	status, failed := auth.RefreshFailed(err)
	switch {
	case !failed:
		return "", false
	case status != 0:
		return fmt.Sprintf("hey mcp couldn't renew its sign-in: HEY answered HTTP %d", status), true
	default:
		// No status: HEY didn't answer, or its answer couldn't be used.
		return "hey mcp couldn't renew its sign-in with HEY", true
	}
}

// heyFailure answers a failed HEY call.
func heyFailure(err error, kind gateway.Kind) *mcp.CallToolResult {
	if answer := heyUpstreamFailure(err, kind, ""); answer != nil {
		return answer
	}
	return gateway.ErrorResult("%v", err)
}

// heyReason says why a HEY call failed, inside a sentence of hey mcp's that
// says what became of the request: the status HEY answered, or that it
// didn't answer, never the client's text for an upstream failure or a 404.
func heyReason(err error) string {
	if reason, failed := refreshReason(err); failed {
		return reason
	}
	call := heyUpstream(err, gateway.KindRead, "")
	switch {
	case call.Status >= 500 || call.Status == http.StatusNotFound:
		return fmt.Sprintf("HEY answered HTTP %d", call.Status)
	case gateway.UpstreamFailure(err, call) != nil:
		return "HEY didn't answer"
	}
	return err.Error()
}
