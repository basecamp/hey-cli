package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

func TestCanonicalListCommandsMatchCompatibilityForms(t *testing.T) {
	tests := []struct {
		name          string
		handler       func(*testing.T) http.Handler
		compatibility []string
		canonical     []string
	}{
		{
			name: "boxes",
			handler: func(*testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/boxes.json" {
						http.NotFound(w, r)
						return
					}
					_, _ = io.WriteString(w, `[{"id":1,"kind":"imbox","name":"Imbox"}]`)
				})
			},
			compatibility: []string{"boxes", "--limit", "1"},
			canonical:     []string{"box", "list", "--limit", "1"},
		},
		{
			name: "labels",
			handler: func(*testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/my/navigation.json" {
						http.NotFound(w, r)
						return
					}
					_, _ = io.WriteString(w, `{"items":[{"title":"Labels","menu_items":[{"title":"Receipts","app_url":"/folders/12"}]}]}`)
				})
			},
			compatibility: []string{"labels", "--limit", "1"},
			canonical:     []string{"label", "list", "--limit", "1"},
		},
		{
			name: "collections",
			handler: func(*testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/collections.json" {
						http.NotFound(w, r)
						return
					}
					_, _ = io.WriteString(w, `[{"id":12,"name":"Kitchen remodel"}]`)
				})
			},
			compatibility: []string{"collections", "--limit", "1"},
			canonical:     []string{"collection", "list", "--limit", "1"},
		},
		{
			name: "workflows",
			handler: func(t *testing.T) http.Handler {
				return workflowHandler(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/autocompletable/accounts/1/workflows" {
						_, _ = io.WriteString(w, `[["101","Home projects","Personal"]]`)
						return
					}
					if r.URL.Path == "/autocompletable/accounts/2/workflows" {
						_, _ = io.WriteString(w, `[]`)
						return
					}
					http.NotFound(w, r)
				})
			},
			compatibility: []string{"workflows", "--limit", "1"},
			canonical:     []string{"workflow", "list", "--limit", "1"},
		},
		{
			name: "clips",
			handler: func(*testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/clips.json" {
						http.NotFound(w, r)
						return
					}
					_, _ = io.WriteString(w, clipsJSON)
				})
			},
			compatibility: []string{"clips"},
			canonical:     []string{"clip", "list"},
		},
		{
			name: "snippets",
			handler: func(*testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/snippets.json" {
						http.NotFound(w, r)
						return
					}
					_, _ = io.WriteString(w, snippetsJSON)
				})
			},
			compatibility: []string{"snippets"},
			canonical:     []string{"snippet", "list"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := withJSONContentType(tt.handler(t))
			compatibility, err := runJSONCommand(t, handler, tt.compatibility...)
			if err != nil {
				t.Fatalf("compatibility form: %v", err)
			}
			canonical, err := runJSONCommand(t, handler, tt.canonical...)
			if err != nil {
				t.Fatalf("canonical form: %v", err)
			}
			if !reflect.DeepEqual(canonical, compatibility) {
				t.Errorf("canonical response = %#v, compatibility response = %#v", canonical, compatibility)
			}
		})
	}
}

func TestCanonicalViewCommandsMatchCompatibilityForms(t *testing.T) {
	tests := []struct {
		name          string
		handler       http.Handler
		compatibility []string
		canonical     []string
	}{
		{
			name: "box",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/imbox.json" {
					http.NotFound(w, r)
					return
				}
				_, _ = io.WriteString(w, `{"id":1,"kind":"imbox","name":"Imbox","postings":[]}`)
			}),
			compatibility: []string{"box", "imbox", "--limit", "1"},
			canonical:     []string{"box", "view", "imbox", "--limit", "1"},
		},
		{
			name: "label",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/folders/12.json" {
					http.NotFound(w, r)
					return
				}
				_, _ = io.WriteString(w, `{"id":12,"name":"Receipts","postings":[]}`)
			}),
			compatibility: []string{"label", "12", "--limit", "1"},
			canonical:     []string{"label", "view", "12", "--limit", "1"},
		},
		{
			name: "collection",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/collections/12.json" {
					http.NotFound(w, r)
					return
				}
				_, _ = io.WriteString(w, `{"id":12,"name":"Kitchen remodel","postings":[]}`)
			}),
			compatibility: []string{"collection", "12", "--limit", "1"},
			canonical:     []string{"collection", "view", "12", "--limit", "1"},
		},
		{
			name: "workflow",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/workflows/8801.json" {
					http.NotFound(w, r)
					return
				}
				_, _ = io.WriteString(w, `{"id":8801,"name":"Hiring","stages":[]}`)
			}),
			compatibility: []string{"workflow", "8801"},
			canonical:     []string{"workflow", "view", "8801"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := withJSONContentType(tt.handler)
			compatibility, err := runJSONCommand(t, handler, tt.compatibility...)
			if err != nil {
				t.Fatalf("compatibility form: %v", err)
			}
			canonical, err := runJSONCommand(t, handler, tt.canonical...)
			if err != nil {
				t.Fatalf("canonical form: %v", err)
			}
			if !reflect.DeepEqual(canonical, compatibility) {
				t.Errorf("canonical response = %#v, compatibility response = %#v", canonical, compatibility)
			}
		})
	}
}

func TestBoxListIsReservedAndViewStillOpensABoxNamedList(t *testing.T) {
	listHandler := withJSONContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/boxes.json" {
			t.Errorf("box list requested %s, want /boxes.json", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `[{"id":17,"kind":"custom","name":"list"}]`)
	}))
	listed, err := runJSONCommand(t, listHandler, "box", "list")
	if err != nil {
		t.Fatal(err)
	}
	if listed.Summary != "1 mailboxes" {
		t.Errorf("box list summary = %q", listed.Summary)
	}

	viewHandler := withJSONContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/boxes.json":
			_, _ = io.WriteString(w, `[{"id":17,"kind":"custom","name":"list"}]`)
		case "/boxes/17.json":
			_, _ = io.WriteString(w, `{"id":17,"kind":"custom","name":"list","postings":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	viewed, err := runJSONCommand(t, viewHandler, "box", "view", "list")
	if err != nil {
		t.Fatal(err)
	}
	if viewed.Summary != "0 threads in list" {
		t.Errorf("box view list summary = %q", viewed.Summary)
	}

	compatibility, err := runJSONCommand(t, viewHandler, "box", "--", "list")
	if err != nil {
		t.Fatal(err)
	}
	if compatibility.Summary != viewed.Summary {
		t.Errorf("box -- list summary = %q, want %q", compatibility.Summary, viewed.Summary)
	}
}

func withJSONContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

func TestAgentHelpPrefersCanonicalFamilies(t *testing.T) {
	root := newRootCmd()
	var output bytes.Buffer
	root.SetOut(&output)
	printAgentHelp(root)

	var info struct {
		Subcommands []struct {
			Name string `json:"name"`
		} `json:"subcommands"`
	}
	if err := json.Unmarshal(output.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, command := range info.Subcommands {
		names[command.Name] = true
	}
	for _, canonical := range []string{"box", "label", "collection", "workflow", "clip", "snippet"} {
		if !names[canonical] {
			t.Errorf("agent help is missing canonical family %s", canonical)
		}
	}
	for _, compatibility := range []string{"boxes", "labels", "collections", "workflows", "clips", "snippets"} {
		if names[compatibility] {
			t.Errorf("agent help promotes compatibility command %s", compatibility)
		}
	}
}

func TestCompatibilityCommandsNameTheirCanonicalForms(t *testing.T) {
	wantCommands := map[string]string{
		"boxes":       "box list",
		"labels":      "label list",
		"collections": "collection list",
		"workflows":   "workflow list",
		"clips":       "clip list",
		"snippets":    "snippet list",
	}
	wantUsages := map[string]string{
		"box":        "box <name|id>",
		"label":      "label <id>",
		"collection": "collection <id>",
		"workflow":   "workflow <id>",
	}
	catalog := walkCommands(newRootCmd(), "")
	for _, entry := range catalog {
		path, _ := entry["path"].(string)
		canonical, marked := entry["compatibility_for"].(string)
		if expected, ok := wantCommands[path]; ok {
			if !marked || canonical != expected {
				t.Errorf("%s compatibility_for = %q, want %q", path, canonical, expected)
			}
			delete(wantCommands, path)
		} else if marked {
			t.Errorf("unexpected compatibility command %s -> %s", path, canonical)
		}

		if expected, ok := wantUsages[path]; ok {
			if usage, _ := entry["compatibility_usage"].(string); usage != expected {
				t.Errorf("%s compatibility_usage = %q, want %q", path, usage, expected)
			}
			delete(wantUsages, path)
		}
	}
	for path := range wantCommands {
		t.Errorf("command catalog is missing compatibility command %s", path)
	}
	for path := range wantUsages {
		t.Errorf("command catalog is missing compatibility usage for %s", path)
	}

	flattened := flattenCommandCatalog(catalog)
	for _, want := range []string{"box list", "box view", "label list", "label view", "collection list", "collection view", "workflow list", "workflow view", "clip list", "snippet list"} {
		found := false
		for _, entry := range flattened {
			if entry["path"] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("flattened command catalog is missing %s", want)
		}
	}
}
