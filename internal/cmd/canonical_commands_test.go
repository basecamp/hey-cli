package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

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

func TestCanonicalFamiliesShowHelpWithoutArguments(t *testing.T) {
	tests := []struct {
		family             string
		compatibilityUsage string
	}{
		{family: "box", compatibilityUsage: "hey box <name|id>"},
		{family: "label", compatibilityUsage: "hey label <id>"},
		{family: "collection", compatibilityUsage: "hey collection <id>"},
		{family: "workflow", compatibilityUsage: "hey workflow <id>"},
	}
	for _, tt := range tests {
		t.Run(tt.family, func(t *testing.T) {
			server := quietServer(t)
			stdout, _, err := runAuthCommand(t, t.TempDir(), server.URL, "", false, tt.family)
			if err != nil {
				t.Fatalf("hey %s: %v", tt.family, err)
			}
			for _, want := range []string{"COMMANDS", "hey " + tt.family + " list", "hey " + tt.family + " view"} {
				if !strings.Contains(stdout, want) {
					t.Errorf("hey %s output is missing %q:\n%s", tt.family, want, stdout)
				}
			}
			if strings.Contains(stdout, tt.compatibilityUsage) {
				t.Errorf("hey %s help promotes compatibility usage %q:\n%s", tt.family, tt.compatibilityUsage, stdout)
			}
			if strings.Contains(stdout, "  hey "+tt.family+" [flags]\n") {
				t.Errorf("hey %s help presents the compatibility runner as canonical:\n%s", tt.family, stdout)
			}
		})
	}
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
	wantUsages := map[string]string{
		"box":        "box <name|id>",
		"label":      "label <id>",
		"collection": "collection <id>",
		"workflow":   "workflow <id>",
	}
	catalog := walkCommands(newRootCmd(), "")
	for _, entry := range catalog {
		path, _ := entry["path"].(string)
		if expected, ok := wantUsages[path]; ok {
			if usage, _ := entry["compatibility_usage"].(string); usage != expected {
				t.Errorf("%s compatibility_usage = %q, want %q", path, usage, expected)
			}
			delete(wantUsages, path)
		}
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
