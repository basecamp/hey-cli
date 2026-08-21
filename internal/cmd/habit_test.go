package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	habitvalues "github.com/basecamp/hey-cli/internal/habit"
)

func TestHabitCreateCommand(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/calendar/habits.json" {
			t.Errorf("request = %s %s, want POST /calendar/habits.json", r.Method, r.URL.Path)
		}
		var body struct {
			Habit struct {
				Name  string  `json:"name"`
				Icon  string  `json:"icon"`
				Color string  `json:"color"`
				Days  []int32 `json:"days"`
			} `json:"calendar_habit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Habit.Name != "Morning strength training" || body.Habit.Icon != "weights" || body.Habit.Color != "blue" {
			t.Errorf("payload = %+v", body.Habit)
		}
		if got := body.Habit.Days; len(got) != 7 || got[0] != 0 || got[6] != 6 {
			t.Errorf("default days = %v, want 0 through 6", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":42,"title":"Morning strength training","type":"CalendarHabit","icon":"weights","color":"blue","days":[0,1,2,3,4,5,6]}`)
	}), "habit", "create", "Morning strength training")
	if err != nil {
		t.Fatalf("execute habit create: %v", err)
	}
	if response.Summary != "Habit created" {
		t.Errorf("summary = %q", response.Summary)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["id"] != float64(42) || data["title"] != "Morning strength training" {
		t.Errorf("structured data = %#v", response.Data)
	}
}

func TestHabitCreateParsesFriendlyDays(t *testing.T) {
	_, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		days := body["calendar_habit"]["days"].([]any)
		if len(days) != 3 || days[0] != float64(1) || days[1] != float64(3) || days[2] != float64(5) {
			t.Errorf("days = %v, want [1 3 5]", days)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":43,"title":"Practice piano","type":"CalendarHabit"}`)
	}), "habit", "create", "Practice piano", "--icon", "music", "--color", "green", "--days", "Friday, monday, WED")
	if err != nil {
		t.Fatalf("execute habit create: %v", err)
	}
}

func TestHabitEditSendsPartialPayload(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/calendar/habits/42.json" {
			t.Errorf("request = %s %s, want PATCH /calendar/habits/42.json", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if got := string(body); got != `{"calendar_habit":{"color":"gold","days":[0,6]}}` {
			t.Errorf("payload = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42,"title":"Morning strength training","type":"CalendarHabit","color":"gold","days":[0,6]}`)
	}), "habit", "edit", "42", "--color", "gold", "--days", "Saturday,0")
	if err != nil {
		t.Fatalf("execute habit edit: %v", err)
	}
	if response.Summary != "Habit updated" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestHabitDeleteCommand(t *testing.T) {
	response, err := runJSONCommand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/calendar/habits/42.json" {
			t.Errorf("request = %s %s, want DELETE /calendar/habits/42.json", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}), "habit", "delete", "42")
	if err != nil {
		t.Fatalf("execute habit delete: %v", err)
	}
	if response.Summary != "Habit deleted" {
		t.Errorf("summary = %q", response.Summary)
	}
}

func TestHabitFlagHelpListsAcceptedIconsAndColors(t *testing.T) {
	for _, command := range []*cobra.Command{newHabitCreateCommand().cmd, newHabitEditCommand().cmd} {
		if usage := command.Flags().Lookup("icon").Usage; !strings.Contains(usage, habitvalues.IconValues) {
			t.Errorf("%s icon help does not list all values: %q", command.Name(), usage)
		}
		if usage := command.Flags().Lookup("color").Usage; !strings.Contains(usage, habitvalues.ColorValues) {
			t.Errorf("%s color help does not list all values: %q", command.Name(), usage)
		}
	}
}

func TestHabitMutationValidationMakesNoRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "create name", args: []string{"habit", "create", ""}, want: "name is required"},
		{name: "create icon", args: []string{"habit", "create", "Read every day", "--icon", "walking"}, want: "icon must be one of"},
		{name: "create days", args: []string{"habit", "create", "Read every day", "--days", "funday"}, want: "invalid weekday"},
		{name: "edit ID", args: []string{"habit", "edit", "nope", "--color", "blue"}, want: "invalid habit ID"},
		{name: "edit color", args: []string{"habit", "edit", "42", "--color", "orange"}, want: "color must be one of"},
		{name: "edit changes", args: []string{"habit", "edit", "42"}, want: "provide at least one"},
		{name: "delete ID", args: []string{"habit", "delete", "0"}, want: "invalid habit ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			_, err := runJSONCommand(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
			}), tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if requests.Load() != 0 {
				t.Errorf("requests = %d, want 0", requests.Load())
			}
		})
	}
}
