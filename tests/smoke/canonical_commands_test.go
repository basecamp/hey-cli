package smoke_test

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

func TestCanonicalResourceCommandsMatchCompatibilityForms(t *testing.T) {
	t.Run("box", func(t *testing.T) {
		assertSameCommandEnvelope(t, []string{"box", "imbox", "--limit", "2"}, []string{"box", "view", "imbox", "--limit", "2"}, false)
	})

	t.Run("label", func(t *testing.T) {
		labels := dataAs[[]smokeFolder](t, heyJSON(t, "label", "list"))
		if len(labels) == 0 {
			skipf(t, "no labels available for canonical label view validation")
		}
		id := strconv.FormatInt(labels[0].ID, 10)
		assertSameCommandResponse(t, []string{"label", id, "--limit", "2"}, []string{"label", "view", id, "--limit", "2"})
	})

	t.Run("collection", func(t *testing.T) {
		collections := dataAs[[]smokeCollection](t, heyJSON(t, "collection", "list"))
		if len(collections) == 0 {
			skipf(t, "no collections available for canonical collection view validation")
		}
		id := strconv.FormatInt(collections[0].ID, 10)
		assertSameCommandResponse(t, []string{"collection", id, "--limit", "2"}, []string{"collection", "view", id, "--limit", "2"})
	})

	t.Run("workflow", func(t *testing.T) {
		workflows := dataAs[[]smokeWorkflow](t, heyJSON(t, "workflow", "list"))
		if len(workflows) == 0 {
			skipf(t, "no workflows available for canonical workflow view validation")
		}
		id := strconv.FormatInt(workflows[0].ID, 10)
		assertSameCommandResponse(t, []string{"workflow", id}, []string{"workflow", "view", id})
	})
}

func assertSameCommandResponse(t *testing.T, compatibility, canonical []string) {
	t.Helper()
	assertSameCommandEnvelope(t, compatibility, canonical, true)
}

func assertSameCommandEnvelope(t *testing.T, compatibility, canonical []string, compareData bool) {
	t.Helper()
	oldResponse := heyJSON(t, compatibility...)
	newResponse := heyJSON(t, canonical...)

	if compareData {
		var oldData, newData any
		if err := json.Unmarshal(oldResponse.Data, &oldData); err != nil {
			t.Fatalf("decode compatibility data: %v", err)
		}
		if err := json.Unmarshal(newResponse.Data, &newData); err != nil {
			t.Fatalf("decode canonical data: %v", err)
		}
		if !reflect.DeepEqual(newData, oldData) {
			t.Errorf("canonical %v data differs from compatibility %v", canonical, compatibility)
		}
	} else if len(oldResponse.Data) == 0 || len(newResponse.Data) == 0 {
		t.Errorf("canonical or compatibility response omitted data")
	}
	if newResponse.Summary != oldResponse.Summary || newResponse.Notice != oldResponse.Notice || !reflect.DeepEqual(newResponse.Breadcrumbs, oldResponse.Breadcrumbs) {
		t.Errorf("canonical %v envelope differs from compatibility %v", canonical, compatibility)
	}
}
