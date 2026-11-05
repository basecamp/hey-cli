package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
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
					base[p.Name] = sentinel(p, 1000+i)
				}
			}
			for i, p := range op.Params {
				checked++
				params := clone(base)
				params[p.Name] = sentinel(p, 2000+i)
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
	if want := modelParameterCount(t, cat); checked != want {
		t.Errorf("checked %d parameters, but the served operations take %d in the model", checked, want)
	}
	// Nothing is refused before HEY is called: every sentinel is a value
	// the action takes, so a refusal here is a parameter the server won't
	// send.
	if len(refused) > 0 {
		t.Errorf("refused before calling HEY: %v", refused)
	}
}

// The catalog serves every operation and parameter the vendored model
// has, so neither can drop out of the surface and out of the test above
// together. The model is read here as plain JSON, apart from
// the toolkit's derivation.
func TestCatalogServesEveryModelParameter(t *testing.T) {
	cat := loadForTest(t)
	model := modelParameters(t)

	// Every model operation is served or left unmapped on purpose
	// (TestCatalogUnmappedTagsArePinned), so an operation can't drop out
	// with its parameters either.
	accounted := map[string]bool{}
	for _, domain := range cat.Domains {
		for _, op := range domain.Operations {
			accounted[op.ID] = true
		}
	}
	for _, ids := range cat.Unmapped {
		for _, id := range ids {
			accounted[id] = true
		}
	}
	for id := range model {
		if !accounted[id] {
			t.Errorf("the model's %s is neither served nor unmapped", id)
		}
	}

	for _, domain := range cat.Domains {
		for _, op := range domain.Operations {
			var served []string
			for _, p := range op.Params {
				served = append(served, p.In+":"+p.Name)
			}
			properties, _ := op.Body["properties"].(map[string]any)
			for name := range properties {
				served = append(served, "body:"+name)
			}
			sort.Strings(served)
			if !reflect.DeepEqual(served, model[op.ID]) {
				t.Errorf("%s serves %v; the model gives it %v", op.Action, served, model[op.ID])
			}
		}
	}
}

func modelParameterCount(t *testing.T, cat *catalog.Catalog) int {
	t.Helper()
	model := modelParameters(t)
	n := 0
	for _, domain := range cat.Domains {
		for _, op := range domain.Operations {
			n += len(model[op.ID])
		}
	}
	return n
}

// modelParameters reads each operation's path and query parameters and its
// JSON body's top-level properties from model/openapi.json, by operationId.
func modelParameters(t *testing.T) map[string][]string {
	t.Helper()
	data, err := os.ReadFile("model/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, methods := range doc.Paths {
		for _, raw := range methods {
			var op struct {
				ID     string `json:"operationId"`
				Params []struct {
					Name string `json:"name"`
					In   string `json:"in"`
				} `json:"parameters"`
				Body struct {
					Content map[string]struct {
						Schema struct {
							Ref        string         `json:"$ref"`
							Properties map[string]any `json:"properties"`
						} `json:"schema"`
					} `json:"content"`
				} `json:"requestBody"`
			}
			if json.Unmarshal(raw, &op) != nil || op.ID == "" {
				continue
			}
			var names []string
			for _, p := range op.Params {
				if p.In == "path" || p.In == "query" {
					names = append(names, p.In+":"+p.Name)
				}
			}
			schema := op.Body.Content["application/json"].Schema
			properties := schema.Properties
			if ref, ok := strings.CutPrefix(schema.Ref, "#/components/schemas/"); ok {
				properties = doc.Components.Schemas[ref].Properties
			}
			for name := range properties {
				names = append(names, "body:"+name)
			}
			sort.Strings(names)
			out[op.ID] = names
		}
	}
	return out
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
		want := wire(value)
		switch in {
		case "path":
			// Every path parameter fills its own segment: the path HEY got
			// is the operation's template with each one in its place.
			return path == expandPath(t, op, params)
		case "query":
			return query.Get(name) == want
		default:
			got, ok := body[name]
			return ok && reflect.DeepEqual(got, roundTrip(t, value))
		}
	}, ""
}

// sentinel is a value of the parameter's declared type, as an MCP client
// sends it from the advertised schema: JSON numbers decode as float64.
func sentinel(p catalog.Param, n int) any {
	switch p.Schema["type"] {
	case "integer", "number":
		return float64(n)
	case "boolean":
		return true
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

func expandPath(t *testing.T, op *catalog.Operation, params map[string]any) string {
	t.Helper()
	path := op.Path
	for _, p := range op.Params {
		if p.In == "path" {
			path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(wire(params[p.Name])))
		}
	}
	return path
}

// wire is how a path or query value reads on the wire, written out here
// rather than taken from dispatch.go, so the test doesn't share its bugs.
func wire(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatInt(int64(f), 10)
	}
	return fmt.Sprint(v)
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
