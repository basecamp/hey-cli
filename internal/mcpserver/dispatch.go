package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basecamp/mcp/catalog"
	"github.com/basecamp/mcp/gateway"
)

// API is the slice of the hey-sdk client the dispatcher drives. *hey.Client
// satisfies it; the client carries auth, account scoping, retry, and base
// URL resolution, so the dispatcher only assembles paths and bodies. Retry
// policy is the implementation's to choose per verb — the CLI wires reads
// through a retrying client and every mutation through one that never
// retries, because a retried PUT can deliver a message twice.
type API interface {
	Get(ctx context.Context, path string) (*hey.Response, error)
	Post(ctx context.Context, path string, body any) (*hey.Response, error)
	Put(ctx context.Context, path string, body any) (*hey.Response, error)
	Patch(ctx context.Context, path string, body any) (*hey.Response, error)
	Delete(ctx context.Context, path string) (*hey.Response, error)
}

// The CLI hands its *hey.Client straight to New.
var _ API = (*hey.Client)(nil)

// dispatcher turns catalog operations into hey-sdk requests.
//
// Calling convention: the tool call's params object carries the operation's
// path and query parameters by name, and every remaining entry becomes a
// request body property. The describe action serves the schema for all
// three. Failures are in-band isError results per MCP convention.
type dispatcher struct {
	api API
}

func (d dispatcher) handle(ctx context.Context, dom gateway.Domain, op gateway.Operation, params map[string]any) (*mcp.CallToolResult, error) {
	domain, ok := dom.(*catalog.Domain)
	if !ok {
		return gateway.ErrorResult("internal error: domain %q is not a catalog domain", dom.Name()), nil
	}
	full, ok := domain.Operation(op.Action)
	if !ok {
		return gateway.ErrorResult("internal error: action %q not in domain %q", op.Action, dom.Name()), nil
	}

	path, body, err := buildRequest(full, params)
	if err != nil {
		return gateway.ErrorResult("%v", err), nil
	}

	resp, err := d.call(ctx, full.Method, path, body)
	if err != nil {
		return gateway.ErrorResult("%v", err), nil
	}
	if len(bytes.TrimSpace(resp.Data)) == 0 {
		result := map[string]any{"status": resp.StatusCode}
		// A draft save answers 204 with the saved entry's path in Location;
		// without it the caller would have no way to address what it just
		// created.
		if location := resp.Headers.Get("Location"); location != "" {
			result["location"] = location
		}
		return gateway.JSONResult(result)
	}
	if fields := nextCursorFields(resp.Headers, resp.Data); len(fields) > 0 {
		result := make(map[string]any, len(fields)+1)
		for name, value := range fields {
			result[name] = value
		}
		result["results"] = resp.Data
		if wrapped, err := json.Marshal(result); err == nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(wrapped)}}}, nil
		}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(resp.Data)}}}, nil
}

// nextCursorFields extracts the next-read cursor from a geared_pagination Link
// header, falling back to a next_history_url in the response body (how box
// reads carry theirs). HEY pages by cursor, not number — a numeric page is
// answered with the first page forever — so when a listing has more, the
// result is wrapped as {"next_page": cursor, "results": ...} and the caller
// passes the cursor back as the action's page parameter. The changes feed's
// last page names the next incremental poll instead: its Link URL carries
// since (and v) rather than page, surfaced as {"next_since": ..., "next_v": ...}
// for the caller to pass back as the action's since and v parameters.
func nextCursorFields(headers http.Header, data []byte) map[string]string {
	for _, link := range headers.Values("Link") {
		for part := range strings.SplitSeq(link, ",") {
			if !strings.Contains(part, `rel="next"`) {
				continue
			}
			start := strings.Index(part, "<")
			end := strings.Index(part, ">")
			if start < 0 || end <= start+1 {
				continue
			}
			if fields := linkCursorFields(part[start+1 : end]); len(fields) > 0 {
				return fields
			}
		}
	}
	if cursor := bodyNextCursor(data); cursor != "" {
		return map[string]string{"next_page": cursor}
	}
	return nil
}

// linkCursorFields reads the cursor out of one rel="next" URL: a page cursor
// while the read has more pages now, or — on the changes feed's last page —
// the since (and v) where the next incremental poll resumes.
func linkCursorFields(rawURL string) map[string]string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	query := u.Query()
	if page := query.Get("page"); page != "" {
		return map[string]string{"next_page": page}
	}
	if since := query.Get("since"); since != "" {
		fields := map[string]string{"next_since": since}
		if v := query.Get("v"); v != "" {
			fields["next_v"] = v
		}
		return fields
	}
	return nil
}

// bodyNextCursor finds a next_history_url at the top level of the response —
// or one envelope down, for the nested wire variant box reads may use — and
// returns its page cursor.
func bodyNextCursor(data []byte) string {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(data, &body); err != nil {
		return ""
	}
	if raw, ok := body["next_history_url"]; ok {
		return pageParamJSON(raw)
	}
	if len(body) == 1 {
		for _, raw := range body {
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(raw, &nested); err == nil {
				if inner, ok := nested["next_history_url"]; ok {
					return pageParamJSON(inner)
				}
			}
		}
	}
	return ""
}

func pageParamJSON(raw json.RawMessage) string {
	var next string
	if err := json.Unmarshal(raw, &next); err != nil {
		return ""
	}
	return pageParam(next)
}

// pageParam returns the page query parameter of a pagination URL.
func pageParam(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("page")
}

func (d dispatcher) call(ctx context.Context, method, path string, body any) (*hey.Response, error) {
	switch method {
	case "GET":
		return d.api.Get(ctx, path)
	case "POST":
		return d.api.Post(ctx, path, body)
	case "PUT":
		return d.api.Put(ctx, path, body)
	case "PATCH":
		return d.api.Patch(ctx, path, body)
	case "DELETE":
		// No catalog DELETE takes a body; buildRequest already rejected
		// stray params for body-less operations.
		return d.api.Delete(ctx, path)
	default:
		return nil, fmt.Errorf("internal error: unsupported method %s", method)
	}
}

// buildRequest resolves the operation's path template and query string from
// params and gathers the remaining entries into the request body. Missing
// path parameters, stray parameters, and non-scalar path or query values are
// errors pointing at the describe action.
func buildRequest(op *catalog.Operation, params map[string]any) (string, any, error) {
	consumed := map[string]bool{}

	path := op.Path
	for _, p := range op.Params {
		if p.In != "path" {
			continue
		}
		raw, ok := params[p.Name]
		if !ok {
			return "", nil, fmt.Errorf("missing required path parameter %q for action %q (describe the action for its schema)", p.Name, op.Action)
		}
		value, err := scalarString(raw)
		if err != nil {
			return "", nil, fmt.Errorf("path parameter %q: %w", p.Name, err)
		}
		path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(value))
		consumed[p.Name] = true
	}

	query := url.Values{}
	for _, p := range op.Params {
		if p.In != "query" {
			continue
		}
		raw, ok := params[p.Name]
		if !ok {
			if p.Required {
				return "", nil, fmt.Errorf("missing required query parameter %q for action %q (describe the action for its schema)", p.Name, op.Action)
			}
			continue
		}
		value, err := scalarString(raw)
		if err != nil {
			return "", nil, fmt.Errorf("query parameter %q: %w", p.Name, err)
		}
		query.Set(p.Name, value)
		consumed[p.Name] = true
	}

	body := map[string]any{}
	for name, value := range params {
		if consumed[name] {
			continue
		}
		if op.Body == nil {
			return "", nil, fmt.Errorf("unknown parameter %q for action %q (describe the action for its schema)", name, op.Action)
		}
		if !bodyAllows(op, name) {
			return "", nil, fmt.Errorf("unknown parameter %q for action %q (describe the action for its body schema)", name, op.Action)
		}
		body[name] = value
	}

	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	if len(body) == 0 && (op.Body == nil || !op.BodyRequired) {
		return path, nil, nil
	}
	return path, body, nil
}

// bodyAllows reports whether the operation's body schema accepts a property
// named name. A schema without declared properties passes everything
// through; otherwise unknown names are rejected unless additionalProperties
// allows them. This is a typo guard, not schema validation: types, required
// properties, and nested constraints are the server's to enforce, and its
// errors come back in-band.
func bodyAllows(op *catalog.Operation, name string) bool {
	properties, ok := op.Body["properties"].(map[string]any)
	if !ok {
		return true
	}
	if _, ok := properties[name]; ok {
		return true
	}
	if extra, present := op.Body["additionalProperties"]; present {
		allowed, isBool := extra.(bool)
		return !isBool || allowed
	}
	return false
}

// scalarString renders a JSON-decoded path or query value for the wire.
func scalarString(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("must be a string, number, or boolean, got %T", value)
	}
}
