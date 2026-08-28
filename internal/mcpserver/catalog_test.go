package mcpserver

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"testing"

	"github.com/basecamp/mcp/catalog"
)

func loadForTest(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("catalog must derive cleanly from the vendored model: %v", err)
	}
	return cat
}

func TestCatalogServesCuratedDomains(t *testing.T) {
	cat := loadForTest(t)

	tools := make([]string, 0, len(cat.Domains))
	for _, d := range cat.Domains {
		tools = append(tools, d.Tool)
		if len(d.Operations) == 0 {
			t.Errorf("domain %q has no operations", d.Key)
		}
	}
	want := []string{"hey_boxes", "hey_search", "hey_threads", "hey_contacts", "hey_todos", "hey_calendar", "hey_identity"}
	if len(tools) != len(want) {
		t.Fatalf("tools = %v, want %v", tools, want)
	}
	for i := range want {
		if tools[i] != want[i] {
			t.Fatalf("tools = %v, want %v", tools, want)
		}
	}
}

// TestCatalogUnmappedTagsArePinned fails when the SDK grows a tag nobody has
// decided about: adding a tag here (or mapping it in DomainSpecs) is the
// deliberate act.
func TestCatalogUnmappedTagsArePinned(t *testing.T) {
	cat := loadForTest(t)

	unmapped := make([]string, 0, len(cat.Unmapped))
	for tag := range cat.Unmapped {
		unmapped = append(unmapped, tag)
	}
	sort.Strings(unmapped)

	want := []string{
		"Attachments", "Bulk Reply", "Calendar Habits", "Calendar Journal",
		"Calendar Periods", "Calendar Time Tracks", "Clips",
		"Collections", "Folders", "Postings",
		"Publications", "Snippets", "Stickies", "Workflows",
	}
	got, _ := json.Marshal(unmapped)
	expected, _ := json.Marshal(want)
	if string(got) != string(expected) {
		t.Fatalf("unmapped tags = %s, want %s", got, expected)
	}
}

// TestCatalogModelProvenance keeps the vendored snapshot in lockstep with
// the hey-sdk release the CLI builds against: a go.mod bump without a
// snapshot refresh (or vice versa) fails here, so MCP never advertises
// routes from a different SDK version than the one linked in.
func TestCatalogModelProvenance(t *testing.T) {
	data, err := os.ReadFile("model/PROVENANCE.json")
	if err != nil {
		t.Fatalf("PROVENANCE.json: %v", err)
	}
	var provenance struct {
		Source string   `json:"source"`
		Commit string   `json:"commit"`
		Ref    string   `json:"ref"`
		Files  []string `json:"files"`
	}
	if err := json.Unmarshal(data, &provenance); err != nil {
		t.Fatalf("PROVENANCE.json: %v", err)
	}
	if provenance.Source != "github.com/basecamp/hey-sdk" {
		t.Errorf("provenance source = %q", provenance.Source)
	}
	if provenance.Commit == "" {
		t.Error("provenance commit is empty")
	}

	gomod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	match := regexp.MustCompile(`github\.com/basecamp/hey-sdk/go (v\S+)`).FindSubmatch(gomod)
	if match == nil {
		t.Fatal("hey-sdk dependency not found in go.mod")
	}
	if want := "go/" + string(match[1]); provenance.Ref != want {
		t.Errorf("provenance ref = %q, want %q (the hey-sdk version go.mod pins) — run scripts/sync-mcp-model.sh against that checkout", provenance.Ref, want)
	}
}
