package smoke_test

import (
	"encoding/json"
	"testing"
)

// The smoke binary is a dev build (VERSION=dev from make build), so upgrade
// must refuse with the structured upgrade_required error before touching the
// network. The live self-update path is exercised by the native-upgrade job
// in .github/workflows/upgrade-smoke.yml, not here.
func TestUpgradeRefusesDevBuild(t *testing.T) {
	_, stderr := heyFail(t, "upgrade", "--json")

	var resp ErrorResponse
	if err := json.Unmarshal([]byte(stderr), &resp); err != nil {
		t.Fatalf("stderr is not a JSON error envelope: %v\nraw: %s", err, stderr)
	}
	if resp.OK {
		t.Errorf("expected ok=false, got %s", stderr)
	}
	if resp.Code != "upgrade_required" {
		t.Errorf("expected code upgrade_required, got %q (%s)", resp.Code, resp.Error)
	}
}

func TestVersionJSON(t *testing.T) {
	resp := heyJSON(t, "version")
	data := dataAs[map[string]string](t, resp)

	for _, key := range []string{"version", "commit", "date", "go", "source"} {
		if data[key] == "" {
			t.Errorf("expected %q in version data, got %v", key, data)
		}
	}
	if data["version"] != "dev" || data["source"] != "dev" {
		t.Errorf("smoke binary should be a dev build, got %v", data)
	}
}
