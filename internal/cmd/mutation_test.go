package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMutationStyledOutputIsOneSanitizedLine(t *testing.T) {
	unsafeName := "Ryan" + string(rune(0x1b)) + "]2;owned" + string(rune(0x07)) + "\nSinger"
	stdout, err := runStyledCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 88, "name": unsafeName, "email_address": "ryan@example.com",
		})
	}), "contact", "add", "--name", "Ryan Singer", "--email", "ryan@example.com")
	if err != nil {
		t.Fatalf("execute styled contacts add: %v", err)
	}

	want := "Contact added: Ryan Singer <ryan@example.com> (#88)\n"
	if stdout != want {
		t.Errorf("styled output = %q, want %q", stdout, want)
	}
}

func TestMutationEnvelopeCarriesSummaryAndBreadcrumbs(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id": 88}`)
	}), "contact", "hide", "88")
	if err != nil {
		t.Fatalf("execute contacts hide: %v", err)
	}

	if response.Summary != "Contact hidden" {
		t.Errorf("summary = %q, want %q", response.Summary, "Contact hidden")
	}
	if len(response.Breadcrumbs) != 1 || response.Breadcrumbs[0].Action != "show_again" {
		t.Errorf("breadcrumbs = %+v, want the show_again breadcrumb", response.Breadcrumbs)
	}
	if response.Data == nil {
		t.Error("data is missing, want the hidden contact's id")
	}
}

// A mutation the API answered with no content must leave `data` out rather than
// report `"data": null`, which the documented --jq recipes cannot walk.
func TestMutationOmitsDataTheAPIDidNotSend(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "todo", "add", "Buy milk")
	if err != nil {
		t.Fatalf("execute todo add: %v", err)
	}

	if response.Summary != "Todo created" {
		t.Errorf("summary = %q, want %q", response.Summary, "Todo created")
	}
	if response.Data != nil {
		t.Errorf("data = %v, want it left out", response.Data)
	}
}

func TestTodoCommandsRejectNonPositiveIDs(t *testing.T) {
	for _, args := range [][]string{
		{"todo", "complete", "--", "-1"},
		{"todo", "uncomplete", "0"},
		{"todo", "delete", "0"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := runJSONCommand(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Errorf("%v reached the API", args)
			}), args...)
			if err == nil {
				t.Fatalf("%v was accepted, want a usage error", args)
			}
			if !strings.Contains(err.Error(), "invalid todo ID") {
				t.Errorf("error = %v, want it to name the invalid todo ID", err)
			}
		})
	}
}
