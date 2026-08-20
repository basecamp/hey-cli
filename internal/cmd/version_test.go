package cmd

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func runVersionCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	isolateCommandEnv(t)
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"version"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func TestVersionStyledPrintsFullLine(t *testing.T) {
	stubVersion(t, "1.2.3")
	out, err := runVersionCommand(t, "--styled")
	mustNoError(t, err)
	if strings.TrimSpace(out) != "hey version 1.2.3" {
		t.Errorf("styled output = %q", out)
	}
}

func TestVersionJSONEnvelope(t *testing.T) {
	stubVersion(t, "dev")
	out, err := runVersionCommand(t, "--json")
	mustNoError(t, err)

	var resp struct {
		OK   bool              `json:"ok"`
		Data map[string]string `json:"data"`
	}
	mustNoError(t, json.Unmarshal([]byte(out), &resp))
	if !resp.OK {
		t.Fatalf("ok=false: %s", out)
	}
	want := map[string]string{
		"version": "dev",
		"go":      runtime.Version(),
		"source":  "dev",
	}
	for k, v := range want {
		if resp.Data[k] != v {
			t.Errorf("data[%q] = %q, want %q", k, resp.Data[k], v)
		}
	}
	for _, k := range []string{"commit", "date"} {
		if _, ok := resp.Data[k]; !ok {
			t.Errorf("data missing %q", k)
		}
	}
}

func TestVersionJSONReportsReleaseSource(t *testing.T) {
	stubVersion(t, "1.2.3")
	out, err := runVersionCommand(t, "--json")
	mustNoError(t, err)
	assertContains(t, out, `"source": "release"`)
}
