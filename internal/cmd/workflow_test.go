package cmd

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

const workflowIdentityJSON = `{
	"id":1,
	"accounts":[
		{"id":1,"name":"Personal","purpose":"home","status":"active"},
		{"id":2,"name":"Work","purpose":"work","status":"active"}
	],
	"all_users":[],
	"senders":[]
}`

func TestWorkflowsCommandListsEveryLinkedAccount(t *testing.T) {
	response, err := runJSONCommand(t, workflowHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/autocompletable/accounts/1/workflows":
			_, _ = io.WriteString(w, `[["101","Home projects","Personal"]]`)
		case "/autocompletable/accounts/2/workflows":
			_, _ = io.WriteString(w, `[["202","Hiring","Work"],["203","Sales pipeline","Work"]]`)
		default:
			http.NotFound(w, r)
		}
	}), "workflows")
	if err != nil {
		t.Fatalf("execute workflows: %v", err)
	}
	if response.Summary != "3 workflows" {
		t.Errorf("summary = %q", response.Summary)
	}
	items := response.Data.([]any)
	if len(items) != 3 {
		t.Fatalf("items = %#v", items)
	}
	first := items[0].(map[string]any)
	if first["id"] != float64(101) || first["account_id"] != float64(1) || first["account_name"] != "Personal" {
		t.Errorf("first workflow = %#v", first)
	}
}

func TestWorkflowsCommandHonorsSelectedAccount(t *testing.T) {
	var listed []string
	response, err := runJSONCommand(t, workflowHandler(t, func(w http.ResponseWriter, r *http.Request) {
		listed = append(listed, r.URL.Path)
		if r.URL.Path != "/autocompletable/accounts/2/workflows" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `[["202","Hiring","Work"]]`)
	}), "workflows", "--account", "2")
	if err != nil {
		t.Fatalf("execute account-scoped workflows: %v", err)
	}
	if len(listed) != 1 || len(response.Data.([]any)) != 1 {
		t.Errorf("listed = %v, response = %#v", listed, response)
	}
}

func TestWorkflowsCommandLimitAndFormats(t *testing.T) {
	handler := workflowHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/autocompletable/accounts/") {
			_, _ = io.WriteString(w, `[["101","Home projects","Personal"],["102","Travel plans","Personal"]]`)
			return
		}
		http.NotFound(w, r)
	})

	response, err := runJSONCommand(t, handler, "workflows", "--limit", "1")
	if err != nil {
		t.Fatalf("execute workflows --limit: %v", err)
	}
	if response.Summary != "1 workflow" || response.Notice != "Showing 1 of 4 results. Use --all to see everything." {
		t.Errorf("response = %#v", response)
	}

	ids, err := runFormattedCommand(t, handler, []string{"--ids-only"}, "workflows", "--limit", "1", "--all")
	if err != nil || ids != "101\n102\n101\n102\n" {
		t.Errorf("ids = %q, err = %v", ids, err)
	}
	count, err := runFormattedCommand(t, handler, []string{"--count"}, "workflows")
	if err != nil || count != "4\n" {
		t.Errorf("count = %q, err = %v", count, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "workflows", "--limit", "1")
	if err != nil || !strings.Contains(markdown, "| account_id |") || !strings.Contains(markdown, "Home projects") {
		t.Errorf("markdown = %q, err = %v", markdown, err)
	}
	styled, err := runStyledCommand(t, handler, "workflows", "--limit", "1")
	if err != nil || !strings.Contains(styled, "Account ID") || !strings.Contains(styled, "Home projects") {
		t.Errorf("styled = %q, err = %v", styled, err)
	}
}

func TestWorkflowsCommandPreservesEmptyList(t *testing.T) {
	response, err := runJSONCommand(t, workflowHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	}), "workflows")
	if err != nil {
		t.Fatalf("execute workflows: %v", err)
	}
	items, ok := response.Data.([]any)
	if !ok || len(items) != 0 || response.Summary != "0 workflows" {
		t.Errorf("response = %#v", response)
	}
}

func TestWorkflowCommandReturnsStagesInPositionOrder(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/workflows/8801.json" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":8801,"name":"Hiring","app_url":"/workflows/8801","stages":[{"id":5512,"name":"Applied"},{"id":5513,"name":"Interviewing"}]}`)
	}), "workflow", "8801")
	if err != nil {
		t.Fatalf("execute workflow: %v", err)
	}
	if response.Summary != "Workflow 8801 with 2 stages" {
		t.Errorf("summary = %q", response.Summary)
	}
	detail := response.Data.(map[string]any)
	stages := detail["stages"].([]any)
	if stages[0].(map[string]any)["id"] != float64(5512) || stages[1].(map[string]any)["name"] != "Interviewing" {
		t.Errorf("stages = %#v", stages)
	}
}

func TestWorkflowCommandOutputFormats(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":8801,"name":"Hiring","stages":[{"id":5512,"name":"Applied"},{"id":5513,"name":"Interviewing"}]}`)
	})
	ids, err := runFormattedCommand(t, handler, []string{"--ids-only"}, "workflow", "8801")
	if err != nil || ids != "5512\n5513\n" {
		t.Errorf("ids = %q, err = %v", ids, err)
	}
	count, err := runFormattedCommand(t, handler, []string{"--count"}, "workflow", "8801")
	if err != nil || count != "2\n" {
		t.Errorf("count = %q, err = %v", count, err)
	}
	markdown, err := runFormattedCommand(t, handler, []string{"--markdown"}, "workflow", "8801")
	if err != nil || !strings.Contains(markdown, "# Hiring") || !strings.Contains(markdown, "| 5512 | Applied |") {
		t.Errorf("markdown = %q, err = %v", markdown, err)
	}
	styled, err := runStyledCommand(t, handler, "workflow", "8801")
	if err != nil || !strings.Contains(styled, "Workflow: Hiring (8801)") || !strings.Contains(styled, "Interviewing") {
		t.Errorf("styled = %q, err = %v", styled, err)
	}
}

func TestWorkflowMarkdownEscapesMetadata(t *testing.T) {
	cmd := newWorkflowCommand().cmd
	var output strings.Builder
	cmd.SetOut(&output)
	if err := writeWorkflowMarkdown(cmd, workflowDetailView{
		ID:   8801,
		Name: `[Hiring](https://example.invalid)`,
		Stages: []workflowStageView{{
			ID:   5512,
			Name: `[Applied](https://example.invalid)`,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "[Hiring](") || !strings.Contains(output.String(), `\[Hiring\]\(https\:\/\/example\.invalid\)`) {
		t.Errorf("unsafe Markdown output: %q", output.String())
	}
}

func TestWorkflowMarkdownSurfacesWriteFailure(t *testing.T) {
	cmd := newWorkflowCommand().cmd
	cmd.SetOut(failingWriter{})
	if err := writeWorkflowMarkdown(cmd, workflowDetailView{ID: 8801, Name: "Hiring"}); err == nil {
		t.Fatal("expected the write failure")
	}
}

func TestWorkflowStyledOutputSanitizesNames(t *testing.T) {
	dangerous := "Hiring\x1b[31m\nspoofed"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":8801,"name":"Hiring\u001b[31m\nspoofed","stages":[{"id":5512,"name":"Applied\u001b[2J"}]}`)
	})
	styled, err := runStyledCommand(t, handler, "workflow", "8801")
	if err != nil {
		t.Fatalf("styled workflow: %v", err)
	}
	if strings.Contains(styled, "\x1b[31m") || strings.Contains(styled, "\nspoofed") || !strings.Contains(styled, "Hiring spoofed") {
		t.Errorf("unsafe styled output for %q: %q", dangerous, styled)
	}
}

func TestWorkflowCreateUsesTheOnlyLinkedAccount(t *testing.T) {
	oneAccountIdentity := `{
		"id":1,
		"accounts":[{"id":1,"name":"Personal","purpose":"home","status":"active"}],
		"all_users":[],
		"senders":[]
	}`
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/identity.json":
			_, _ = io.WriteString(w, oneAccountIdentity)
		case "/workflows":
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("workflow[name]") != "Hiring" || r.Form.Get("account_id") != "1" {
				t.Errorf("form = %v", r.Form)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}), "workflow", "create", "Hiring")
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if response.Summary != `Workflow "Hiring" created` || response.Data != nil {
		t.Errorf("response = %#v", response)
	}
}

func TestWorkflowCreateRequiresAnAccountWhenSeveralAreAvailable(t *testing.T) {
	var writes atomic.Int32
	_, err := runJSONCommand(t, workflowHandler(t, func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
	}), "workflow", "create", "Hiring")
	if err == nil || !strings.Contains(err.Error(), "requires one linked mail account") {
		t.Fatalf("error = %v", err)
	}
	if writes.Load() != 0 {
		t.Errorf("writes = %d", writes.Load())
	}
}

func TestWorkflowMutationCommands(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		method      string
		path        string
		wantSummary string
		check       func(*testing.T, url.Values)
	}{
		{
			name: "update", args: []string{"workflow", "update", "8801", "--name", "Recruiting"},
			method: http.MethodPatch, path: "/workflows/8801", wantSummary: `Workflow 8801 renamed to "Recruiting"`,
			check: func(t *testing.T, form url.Values) {
				if form.Get("workflow[name]") != "Recruiting" {
					t.Errorf("form = %v", form)
				}
			},
		},
		{name: "delete", args: []string{"workflow", "delete", "8801"}, method: http.MethodDelete, path: "/workflows/8801", wantSummary: "Workflow 8801 deleted"},
		{name: "create stage", args: []string{"workflow", "stage", "create", "8801"}, method: http.MethodPost, path: "/workflows/8801/stages", wantSummary: "Untitled stage added to workflow 8801"},
		{
			name: "update stage", args: []string{"workflow", "stage", "update", "8801", "5512", "--name", "Applied"},
			method: http.MethodPatch, path: "/workflows/8801/stages/5512", wantSummary: `Stage 5512 renamed to "Applied"`,
			check: func(t *testing.T, form url.Values) {
				if form.Get("workflow_stage[name]") != "Applied" {
					t.Errorf("form = %v", form)
				}
			},
		},
		{name: "delete stage", args: []string{"workflow", "stage", "delete", "8801", "5512"}, method: http.MethodDelete, path: "/workflows/8801/stages/5512", wantSummary: "Stage 5512 deleted from workflow 8801"},
		{
			name: "move", args: []string{"workflow", "move", "4471829", "--workflow", "8801", "--to", "5513"},
			method: http.MethodPatch, path: "/topics/4471829/workflows/8801/stagings", wantSummary: "1 thread moved to workflow 8801 stage 5513",
			check: func(t *testing.T, form url.Values) {
				if form.Get("workflow_staging[workflow_stage_id]") != "5513" {
					t.Errorf("form = %v", form)
				}
			},
		},
		{name: "remove", args: []string{"workflow", "remove", "4471829", "--from", "8801"}, method: http.MethodDelete, path: "/topics/4471829/workflows/8801/stagings", wantSummary: "1 thread removed from workflow 8801"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method || r.URL.Path != tt.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tt.method, tt.path)
					http.NotFound(w, r)
					return
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if tt.check != nil {
					tt.check(t, r.Form)
				}
				w.WriteHeader(http.StatusNoContent)
			}), tt.args...)
			if err != nil {
				t.Fatalf("execute %v: %v", tt.args, err)
			}
			if response.Summary != tt.wantSummary || response.Data != nil {
				t.Errorf("response = %#v", response)
			}
		})
	}
}

func TestWorkflowAddCreatesAndMovesTheStaging(t *testing.T) {
	var requests atomic.Int32
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		if r.URL.Path != "/topics/4471829/workflows/8801/stagings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if request == 1 {
			if r.Method != http.MethodPost {
				t.Errorf("first method = %s", r.Method)
			}
		} else {
			if r.Method != http.MethodPatch {
				t.Errorf("second method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("workflow_staging[workflow_stage_id]") != "5512" {
				t.Errorf("form = %v", r.Form)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}), "workflow", "add", "4471829", "--to", "8801", "--stage", "5512")
	if err != nil {
		t.Fatalf("add to workflow: %v", err)
	}
	if requests.Load() != 2 || response.Summary != "1 thread added to workflow 8801 stage 5512" {
		t.Errorf("requests = %d, response = %#v", requests.Load(), response)
	}
}

func TestWorkflowAddSurfacesStageSelectionError(t *testing.T) {
	var requests atomic.Int32
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "invalid stage", http.StatusUnprocessableEntity)
	}), "workflow", "add", "4471829", "--to", "8801", "--stage", "9999")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "validation") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 2 {
		t.Errorf("requests = %d, want create and stage selection", requests.Load())
	}
}

func TestWorkflowCommandValidation(t *testing.T) {
	tests := [][]string{
		{"workflow", "0"},
		{"workflow", "update", "bad", "--name", "Hiring"},
		{"workflow", "update", "1"},
		{"workflow", "create", "   "},
		{"workflow", "stage", "create", "-1"},
		{"workflow", "stage", "update", "1", "0", "--name", "Applied"},
		{"workflow", "stage", "update", "1", "2"},
		{"workflow", "stage", "delete", "1", "bad"},
		{"workflow", "add", "501", "--stage", "2"},
		{"workflow", "add", "501", "--to", "1"},
		{"workflow", "add", "501", "501", "--to", "1", "--stage", "2"},
		{"workflow", "move", "501", "--to", "2"},
		{"workflow", "move", "bad", "--workflow", "1", "--to", "2"},
		{"workflow", "remove", "501"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
			}), args...)
			if err == nil {
				t.Fatalf("expected %v to fail", args)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d", requests.Load())
			}
		})
	}
}

func TestWorkflowCommandSurfacesAPIErrors(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}), "workflow", "8801")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("error = %v", err)
	}
}

func workflowHandler(t *testing.T, fallback http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/identity.json" {
			_, _ = io.WriteString(w, workflowIdentityJSON)
			return
		}
		fallback(w, r)
	}
}
