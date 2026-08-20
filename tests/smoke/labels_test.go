package smoke_test

import (
	"strconv"
	"strings"
	"testing"
)

type smokeFolder struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func TestLabelsAndLabel(t *testing.T) {
	response := heyJSON(t, "labels")
	folders := dataAs[[]smokeFolder](t, response)
	if response.Summary == "" {
		t.Error("labels response omitted summary")
	}
	if len(folders) == 0 {
		t.Skip("no labels available for label detail validation")
	}

	folder := folders[0]
	detail := heyJSON(t, "label", strconv.FormatInt(folder.ID, 10), "--limit", "2")
	data := dataAs[struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}](t, detail)
	if data.ID != folder.ID || data.Name != folder.Name {
		t.Errorf("label detail = %+v, want %+v", data, folder)
	}

	html := fetchHTML(t, baseURL+"/folders")
	if !strings.Contains(html, folder.Name) {
		t.Errorf("folder page does not contain %q", folder.Name)
	}
}

func TestLabelMutationValidation(t *testing.T) {
	for _, args := range [][]string{
		{"label", "add", "101"},
		{"label", "add", "invalid", "--to", "12"},
		{"label", "create", "Receipts", "invalid"},
		{"label", "remove", "101"},
		{"label", "remove", "invalid", "--from", "all"},
	} {
		heyFail(t, args...)
	}
}
