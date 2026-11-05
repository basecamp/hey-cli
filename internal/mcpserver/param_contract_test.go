package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basecamp/mcp/catalog"
	"github.com/basecamp/mcp/gateway"
)

// Every parameter an action takes reaches HEY's request under the name the
// operation declares: none is accepted and dropped, or refused.
// The 2026-11-05 HEY MCP dogfood found list_contacts' query coming back
// ignored. Here it reaches HEY as q, and the search ignoring it is
// haystack's contacts#index (basecamp/haystack#8893); this test holds the
// server's half.
//
// Each parameter is sent alone, beside the operation's required path and
// query parameters, with a value no other parameter has. A path parameter
// must fill its segment, a query parameter its key, and a body property
// the body's top level, where the gateway puts every remaining parameter.
func TestEveryParameterReachesTheRequest(t *testing.T) {
	cat := loadForTest(t)
	checked := 0
	refused := map[string]bool{}
	for _, domain := range cat.Domains {
		for _, op := range domain.Operations {
			base := map[string]any{}
			for i, p := range op.Params {
				if p.In == "path" || (p.In == "query" && p.Required) {
					base[p.Name] = sentinel(p.Name, 1000+i)
				}
			}
			for i, p := range op.Params {
				checked++
				params := clone(base)
				params[p.Name] = sentinel(p.Name, 2000+i)
				if reached, refusal := send(t, domain, op, params); refusal != "" {
					refused[domain.Key+"."+op.Action+"."+p.Name] = true
				} else if !reached(p.In, p.Name, params[p.Name]) {
					t.Errorf("%s.%s: %s parameter %s=%v reached nothing HEY reads", domain.Key, op.Action, p.In, p.Name, params[p.Name])
				}
			}
			properties, _ := op.Body["properties"].(map[string]any)
			n := 0
			for name, schema := range properties {
				n++
				checked++
				params := clone(base)
				params[name] = bodySentinel(schema, 3000+n)
				want := params[name]
				if draftFirst[op.ID] && name == "entry" {
					// A draft-first action takes only the entry keys a draft
					// keeps (drafts.go), and sends status drafted with them.
					entry := map[string]any{}
					for k, v := range params[name].(map[string]any) {
						if draftEntryKeys[k] && k != "status" {
							entry[k] = v
						}
					}
					params[name] = entry
					want = clone(entry)
					want.(map[string]any)["status"] = drafted
				}
				if reached, refusal := send(t, domain, op, params); refusal != "" {
					refused[domain.Key+"."+op.Action+"."+name] = true
				} else if !reached("body", name, want) {
					t.Errorf("%s.%s: body property %s=%v reached nothing HEY reads", domain.Key, op.Action, name, want)
				}
			}
		}
	}
	if checked < 100 {
		t.Errorf("checked %d parameters; the catalog has more than that", checked)
	}
	// Nothing is refused before HEY is called: every sentinel is a value
	// the action takes, so a refusal here is a parameter the server won't
	// send.
	if len(refused) > 0 {
		t.Errorf("refused before calling HEY: %v", refused)
	}
}

type reachFunc func(in, name string, value any) bool

// send dispatches one call and returns how to find a parameter in the
// request HEY got, or the in-band error when HEY wasn't called.
func send(t *testing.T, domain *catalog.Domain, op *catalog.Operation, params map[string]any) (reachFunc, string) {
	t.Helper()
	api := &fakeAPI{}
	res, err := dispatcher{api: api}.handle(context.Background(), domain, gateway.Operation{Action: op.Action}, params)
	if err != nil {
		t.Fatalf("%s: %v", op.Action, err)
	}
	// An error after HEY was called (a draft save HEY didn't acknowledge
	// as one, from this fake's 200) still made the request.
	if res.IsError && api.method == "" {
		text := "refused"
		if len(res.Content) > 0 {
			if c, ok := res.Content[0].(*mcp.TextContent); ok {
				text = c.Text
			}
		}
		return nil, text
	}
	path, rawQuery, _ := strings.Cut(api.path, "?")
	query, _ := url.ParseQuery(rawQuery)
	body := decoded(t, api.body)
	return func(in, name string, value any) bool {
		want := fmt.Sprint(value)
		switch in {
		case "path":
			return strings.Contains(path+"/", "/"+url.PathEscape(want)+"/") || strings.Contains(path, "/"+url.PathEscape(want)+".")
		case "query":
			return query.Get(name) == want
		default:
			got, ok := body[name]
			return ok && reflect.DeepEqual(got, roundTrip(t, value))
		}
	}, ""
}

func sentinel(name string, n int) any {
	if strings.HasSuffix(name, "Id") || strings.HasSuffix(name, "_id") || strings.HasSuffix(name, "_ids") {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("s%d", n)
}

// bodySentinel builds a value of the property's type; an object carries a
// sentinel in each of its own properties, or one of its own.
func bodySentinel(schema any, n int) any {
	s, _ := schema.(map[string]any)
	switch s["type"] {
	case "integer", "number":
		return float64(n)
	case "boolean":
		return true
	case "array":
		return []any{bodySentinel(s["items"], n)}
	case "object":
		out := map[string]any{}
		if props, ok := s["properties"].(map[string]any); ok && len(props) > 0 {
			i := 0
			for k, v := range props {
				i++
				out[k] = bodySentinel(v, n*10+i)
			}
			return out
		}
		out["k"] = fmt.Sprintf("s%d", n)
		return out
	}
	return fmt.Sprintf("s%d", n)
}

func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func roundTrip(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func decoded(t *testing.T, body any) map[string]any {
	t.Helper()
	if body == nil {
		return nil
	}
	m, _ := roundTrip(t, body).(map[string]any)
	return m
}
