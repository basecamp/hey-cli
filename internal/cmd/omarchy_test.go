package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/output"
)

const defaultShellJSON = `{
  "version": 1,
  "bar": {
    "position": "top",
    "layout": {
      "left": [{"id": "omarchy.menu"}],
      "center": [{"id": "omarchy.clock"}],
      "right": [{"id": "omarchy.tray"}, {"id": "omarchy.power"}]
    }
  }
}
`

// testOmarchyEnv fakes an Omarchy install: a home dir, an OMARCHY_PATH with the
// default shell.json, and a recorder for the commands setup would run.
func testOmarchyEnv(t *testing.T) (omarchyEnv, *[]string) {
	t.Helper()
	home := t.TempDir()
	omarchyPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(omarchyPath, "config", "omarchy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(omarchyPath, "config", "omarchy", "shell.json"), []byte(defaultShellJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	var ran []string
	env := omarchyEnv{
		home:        home,
		omarchyPath: omarchyPath,
		run: func(name string, args ...string) error {
			ran = append(ran, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
	}
	return env, &ran
}

func statuses(steps []omarchyStep) map[string]string {
	out := make(map[string]string, len(steps))
	for _, step := range steps {
		out[step.Name] = step.Status
	}
	return out
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestOmarchySetupInstallsEverythingOnce(t *testing.T) {
	env, ran := testOmarchyEnv(t)
	setup := omarchySetup{env: env}

	first := statuses(setup.apply())
	for _, name := range []string{"desktop entry", "menu", "bar indicator", "theme template"} {
		if first[name] != "installed" {
			t.Errorf("%s: first run = %q, want installed", name, first[name])
		}
	}

	desktop := readText(t, env.desktopPath())
	if !strings.Contains(desktop, "Exec=xdg-terminal-exec --app-id=org.omarchy.hey -e hey tui") {
		t.Errorf("desktop entry should launch under the shared app id:\n%s", desktop)
	}
	if !strings.Contains(desktop, "Icon=internet-mail") {
		t.Errorf("without a hey icon installed the entry should fall back:\n%s", desktop)
	}

	menu := readText(t, env.menuPath())
	if !strings.Contains(menu, `"hey-tui"`) || !strings.HasPrefix(menu, "{\n"+omarchyMenuBegin) {
		t.Errorf("menu block not written:\n%s", menu)
	}

	var shell map[string]any
	if err := json.Unmarshal([]byte(readText(t, env.shellPath())), &shell); err != nil {
		t.Fatal(err)
	}
	right := shell["bar"].(map[string]any)["layout"].(map[string]any)["right"].([]any)
	if barEntryID(right[0]) != "hey-unread" || barEntryID(right[1]) != "omarchy.tray" {
		t.Errorf("bar module should lead the right section seeded from defaults: %v", right)
	}
	if module := right[0].(map[string]any); module["exec"] != "hey omarchy bar-status" || module["type"] != "command" {
		t.Errorf("bar module malformed: %v", module)
	}

	if readText(t, env.templatePath()) != omarchyThemeTemplate {
		t.Error("theme template not written")
	}
	if len(*ran) != 1 || (*ran)[0] != "omarchy-theme-refresh" {
		t.Errorf("template install should refresh the theme once, ran %v", *ran)
	}

	second := statuses(setup.apply())
	for name, status := range second {
		if status != "unchanged" {
			t.Errorf("%s: second run = %q, want unchanged", name, status)
		}
	}
	if len(*ran) != 1 {
		t.Errorf("an unchanged template must not refresh again, ran %v", *ran)
	}
}

func TestOmarchySetupRemoveReversesEveryPiece(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	setup := omarchySetup{env: env}

	menuBefore := "{\n  // my rows\n  \"notes\": {\"icon\":\"\",\"label\":\"Notes\",\"action\":\"omarchy-launch-editor ~/notes\"},\n}\n"
	if err := os.MkdirAll(filepath.Dir(env.menuPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.menuPath(), []byte(menuBefore), 0o644); err != nil {
		t.Fatal(err)
	}
	setup.apply()

	if menu := readText(t, env.menuPath()); !strings.Contains(menu, `"notes"`) || !strings.Contains(menu, `"hey-tui"`) {
		t.Errorf("install should keep the user's rows alongside ours:\n%s", menu)
	}

	removed := statuses(setup.remove())
	for name, status := range removed {
		if status != "removed" {
			t.Errorf("%s: remove = %q, want removed", name, status)
		}
	}
	if _, err := os.Stat(env.desktopPath()); !os.IsNotExist(err) {
		t.Error("desktop entry still present")
	}
	if _, err := os.Stat(env.templatePath()); !os.IsNotExist(err) {
		t.Error("theme template still present")
	}
	if menu := readText(t, env.menuPath()); menu != menuBefore {
		t.Errorf("menu should be restored byte for byte:\n%s", menu)
	}
	if shell := readText(t, env.shellPath()); strings.Contains(shell, "hey-unread") {
		t.Errorf("bar module still present:\n%s", shell)
	}

	again := statuses(setup.remove())
	for name, status := range again {
		if status != "absent" {
			t.Errorf("%s: second remove = %q, want absent", name, status)
		}
	}
}

func TestOmarchySetupKeepsExistingBarLayout(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	if err := os.MkdirAll(env.configDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	custom := `{"version":1,"bar":{"layout":{"left":[{"id":"omarchy.menu"}],"center":[],"right":["omarchy.audio"]}},"idle":{"lock":600}}`
	if err := os.WriteFile(env.shellPath(), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	omarchySetup{env: env}.apply()

	var shell map[string]any
	if err := json.Unmarshal([]byte(readText(t, env.shellPath())), &shell); err != nil {
		t.Fatal(err)
	}
	if shell["idle"].(map[string]any)["lock"] != float64(600) {
		t.Error("unrelated settings must survive")
	}
	right := shell["bar"].(map[string]any)["layout"].(map[string]any)["right"].([]any)
	if len(right) != 2 || barEntryID(right[1]) != "omarchy.audio" {
		t.Errorf("string-form entries must be kept: %v", right)
	}
}

func TestOmarchySetupRejectsNonJSONShellConfig(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	if err := os.MkdirAll(env.configDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.shellPath(), []byte("{ // comment\n}"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := statuses(omarchySetup{env: env}.apply())
	if steps["bar indicator"] != "failed" {
		t.Errorf("a shell.json we cannot round-trip must fail rather than be rewritten, got %q", steps["bar indicator"])
	}
	if steps["menu"] != "installed" {
		t.Error("one failing step must not stop the others")
	}
}

func TestOmarchySetupRejectsMalformedShellConfig(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	if err := os.MkdirAll(env.configDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	// Valid JSON, not an object: must fail, not panic.
	if err := os.WriteFile(env.shellPath(), []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}
	if steps := statuses(omarchySetup{env: env}.apply()); steps["bar indicator"] != "failed" {
		t.Errorf("a null shell.json must fail the bar step, got %q", steps["bar indicator"])
	}

	// A version-less object is ignored by the shell; refuse to edit it.
	if err := os.WriteFile(env.shellPath(), []byte(`{"bar":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := omarchySetup{env: env}.apply()
	for _, step := range steps {
		if step.Name == "bar indicator" {
			if step.Status != "failed" || !strings.Contains(step.Detail, "version") {
				t.Errorf("a version-less shell.json must fail with a version hint, got %q %q", step.Status, step.Detail)
			}
		}
	}
}

func TestOmarchySetupRefusesWrongTypedBarFields(t *testing.T) {
	for _, shell := range []string{
		`{"version":1,"bar":"top"}`,
		`{"version":1,"bar":{"layout":[]}}`,
		`{"version":1,"bar":{"layout":{"right":"omarchy.tray"}}}`,
		`{"version":1,"bar":{}} {"trailing":true}`,
	} {
		env, _ := testOmarchyEnv(t)
		if err := os.MkdirAll(env.configDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(env.shellPath(), []byte(shell), 0o644); err != nil {
			t.Fatal(err)
		}
		if steps := statuses(omarchySetup{env: env}.apply()); steps["bar indicator"] != "failed" {
			t.Errorf("%s: a wrong-typed value must fail, not be replaced, got %q", shell, steps["bar indicator"])
		}
		if readText(t, env.shellPath()) != shell {
			t.Errorf("%s: the file must be left untouched", shell)
		}
	}
}

func TestBarStatusIgnoresRepositoryLocalConfig(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".hey"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".hey", "config.json"), []byte(`{"base_url":"https://untrusted.example.com"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	server := imboxServer(t, `[]`)
	defer server.Close()

	// An untrusted checkout must neither fail the poller at the trust gate
	// nor point it at the checkout's server.
	out, err := runBarStatus(t, server.URL, true)
	if err != nil || out != "" {
		t.Errorf("the poller must stay dark and silent from an untrusted checkout, got %q, %v", out, err)
	}
	if cfg.UntrustedLocalConfig() != nil || cfg.SourceOf("base_url") != config.SourceFlag {
		t.Errorf("the poller must load global configuration only, got base_url from %v", cfg.SourceOf("base_url"))
	}

	root := newRootCmd()
	command, _, _ := root.Find([]string{"setup", "omarchy"})
	if !commandIgnoresLocalConfig(command) {
		t.Error("setup omarchy edits fixed desktop paths only and must ignore a checkout's config too")
	}
	if boxes, _, _ := root.Find([]string{"boxes"}); commandIgnoresLocalConfig(boxes) {
		t.Error("ordinary commands keep honouring repository-local config")
	}
}

func TestSetupOmarchyRemoveIgnoresMalformedLocalConfig(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".hey"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".hey", "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("OMARCHY_PATH", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"setup", "omarchy", "--remove", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("a malformed checkout config must not block a filesystem-only command, got %v", err)
	}
}

func TestSetupOmarchyFailsWithoutAHomeDirectory(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("OMARCHY_PATH", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", "")
	cwd := t.TempDir()
	t.Chdir(cwd)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"setup", "omarchy", "--remove", "--json"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("without a home directory setup must refuse to touch anything, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".config")); !os.IsNotExist(err) {
		t.Error("nothing may be created relative to the working directory")
	}
}

func TestOmarchySetupSeedsVersionAndRestoresDefaultsOnRemove(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	setup := omarchySetup{env: env}

	setup.apply()
	var shell map[string]any
	if err := json.Unmarshal([]byte(readText(t, env.shellPath())), &shell); err != nil {
		t.Fatal(err)
	}
	if shell["version"] != float64(1) {
		t.Errorf(`a freshly created shell.json needs "version": 1 or the shell ignores it: %v`, shell)
	}

	removed := setup.remove()
	if steps := statuses(removed); steps["bar indicator"] != "removed" {
		for _, step := range removed {
			t.Logf("%s: %s %s", step.Name, step.Status, step.Detail)
		}
		t.Fatalf("bar step = %q, want removed", steps["bar indicator"])
	}
	var after map[string]any // a fresh map: Unmarshal into a non-nil map merges keys
	if err := json.Unmarshal([]byte(readText(t, env.shellPath())), &after); err != nil {
		t.Fatal(err)
	}
	if _, has := after["bar"]; has {
		t.Errorf("removing from a defaults-seeded layout should restore inheriting the defaults: %v", after)
	}
}

func TestOmarchySetupReconcilesStaleBarModule(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	if err := os.MkdirAll(env.configDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := `{"version":1,"bar":{"layout":{"left":[],"center":[],"right":[
	  {"id":"hey-unread","type":"command","exec":"hey omarchy bar-status --old-flag","interval":60,
	   "tooltip":"HEY","onClick":"omarchy-launch-or-focus-tui --app-id=org.omarchy.hey hey"},
	  {"id":"omarchy.tray"}]}}}`
	if err := os.WriteFile(env.shellPath(), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	if steps := statuses(omarchySetup{env: env}.apply()); steps["bar indicator"] != "installed" {
		t.Errorf("a stale module should be rewritten, got %q", steps["bar indicator"])
	}
	var shell map[string]any
	if err := json.Unmarshal([]byte(readText(t, env.shellPath())), &shell); err != nil {
		t.Fatal(err)
	}
	layout := shell["bar"].(map[string]any)["layout"].(map[string]any)
	module := barLayoutModule(layout, omarchyBarModuleID)
	if module["exec"] != "hey omarchy bar-status" || module["onClick"] != omarchyFocusCommand || module["interval"] != float64(180) {
		t.Errorf("stale fields should be reconciled: %v", module)
	}
	if right := layout["right"].([]any); len(right) != 2 || barEntryID(right[1]) != "omarchy.tray" {
		t.Errorf("module position and neighbours must be kept: %v", right)
	}
	if neighbour := barLayoutModule(layout, "omarchy.tray"); neighbour == nil || neighbour["id"] != "omarchy.tray" {
		t.Errorf("other inline modules must be findable too: %v", neighbour)
	}
	if again := statuses(omarchySetup{env: env}.apply()); again["bar indicator"] != "unchanged" {
		t.Errorf("reconciled module must be stable, got %q", again["bar indicator"])
	}
	// A string-form entry that happens to share our id is not ours: neither
	// found by the module lookup nor deleted on removal.
	layout["left"] = append(layout["left"].([]any), "hey-unread")
	if barLayoutModule(layout, omarchyBarModuleID)["type"] != "command" {
		t.Error("the string-form entry must not shadow the managed map")
	}
	withString, err := json.Marshal(shell)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.shellPath(), withString, 0o644); err != nil {
		t.Fatal(err)
	}
	setup := omarchySetup{env: env}
	if steps := statuses(setup.remove()); steps["bar indicator"] != "removed" {
		t.Errorf("remove should still find the managed map, got %q", steps["bar indicator"])
	}
	if err := json.Unmarshal([]byte(readText(t, env.shellPath())), &shell); err != nil {
		t.Fatal(err)
	}
	left := shell["bar"].(map[string]any)["layout"].(map[string]any)["left"].([]any)
	if len(left) != 1 || left[0] != "hey-unread" {
		t.Errorf("a string-form entry sharing our id is unowned and must survive removal: %v", left)
	}
}

func TestOmarchyDefaultShellPathFallsBackToUserTree(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	env.omarchyPath = "" // no OMARCHY_PATH, as in a non-login or agent shell
	userTree := filepath.Join(env.home, ".local", "share", "omarchy", "config", "omarchy")
	if err := os.MkdirAll(userTree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userTree, "shell.json"), []byte(defaultShellJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	if steps := statuses(omarchySetup{env: env}.apply()); steps["bar indicator"] != "installed" {
		t.Errorf("the per-user omarchy tree should seed the layout, got %q", steps["bar indicator"])
	}
}

func TestOmarchyRemoveTemplateReportsDeferredRender(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	env.run = func(name string, args ...string) error { return errors.New("not on PATH") }
	setup := omarchySetup{env: env}

	setup.apply()
	var removed omarchyStep
	for _, step := range setup.remove() {
		if step.Name == "theme template" {
			removed = step
		}
	}
	if removed.Status != "removed" || !strings.Contains(removed.Detail, "next theme switch") {
		t.Errorf("a failed refresh on removal must be reported, got %q %q", removed.Status, removed.Detail)
	}
}

func TestSetupOmarchyRemoveWorksWithoutOmarchy(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("OMARCHY_PATH", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"setup", "omarchy", "--remove", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("removal must work after Omarchy itself is gone, got %v", err)
	}
	if !strings.Contains(buf.String(), "absent") {
		t.Errorf("steps should report absent: %s", buf.String())
	}
}

func TestSetupOmarchyRejectsPositionalArgs(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("OMARCHY_PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"setup", "omarchy", "remove"})
	if err := root.Execute(); err == nil {
		t.Fatal("a positional argument must be rejected, not silently ignored")
	}
}

func TestSetupOmarchyFailureCarriesStepsInMeta(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	omarchyPath := t.TempDir()
	t.Setenv("OMARCHY_PATH", omarchyPath)
	shellDir := filepath.Join(home, ".config", "omarchy")
	if err := os.MkdirAll(shellDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A version-less shell.json fails the bar step while the others succeed.
	if err := os.WriteFile(filepath.Join(shellDir, "shell.json"), []byte(`{"bar":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"setup", "omarchy", "--json"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a failing step must fail the command")
	}
	typed := output.AsError(err)
	if typed.Meta == nil || typed.Meta["steps"] == nil {
		t.Errorf("a partial failure must carry the per-step results for scripting callers, got %+v", typed)
	}
	if typed.Code != "setup_failed" {
		t.Errorf("an operational failure must not be reported as a usage error, got code %q", typed.Code)
	}
}

func TestOmarchySetupFailsOnUnreadableTemplate(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything")
	}
	env, _ := testOmarchyEnv(t)
	if err := os.MkdirAll(filepath.Dir(env.templatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.templatePath(), []byte("# someone else's template\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	setup := omarchySetup{env: env}
	if steps := statuses(setup.apply()); steps["theme template"] != "failed" {
		t.Errorf("an unreadable template must fail the step, not be treated as absent, got %q", steps["theme template"])
	}
	if steps := statuses(setup.remove()); steps["theme template"] != "failed" {
		t.Errorf("remove must not delete a template whose ownership it cannot read, got %q", steps["theme template"])
	}
	if _, err := os.Stat(env.templatePath()); err != nil {
		t.Error("the unreadable template must survive")
	}
}

func TestWriteFileIfChangedFollowsSymlinksAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "shell.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "shell.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if changed, err := writeFileIfChanged(link, []byte("new"), 0o644); err != nil || !changed {
		t.Fatalf("write through the link: changed=%v err=%v", changed, err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink must survive the write")
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Errorf("the link target should hold the new content, got %q", data)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o600 {
		t.Errorf("an existing file keeps its mode, got %v", info.Mode().Perm())
	}

	fresh := filepath.Join(dir, "fresh.json")
	if _, err := writeFileIfChanged(fresh, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(fresh); info.Mode().Perm() != 0o644 {
		t.Errorf("a new file takes the requested mode, got %v", info.Mode().Perm())
	}

	dangling := filepath.Join(dir, "dangling.json")
	if err := os.Symlink(filepath.Join(dir, "gone", "menu.jsonc"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFileIfChanged(dangling, []byte("x"), 0o644); err == nil {
		t.Error("a dangling symlink must be refused, not replaced by a plain file")
	}
	if info, err := os.Lstat(dangling); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("the dangling symlink must survive the refused write")
	}
}

func TestSetupOmarchyRefusesListFormatsBeforeTouchingFiles(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OMARCHY_PATH", t.TempDir())

	for _, flag := range []string{"--ids-only", "--count"} {
		root := newRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"setup", "omarchy", flag})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "not a list") {
			t.Errorf("%s must be refused up front, got %v", flag, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".local", "share", "applications", "HEY TUI.desktop")); !os.IsNotExist(err) {
			t.Errorf("%s must be refused before any file is written", flag)
		}
	}
}

func TestSetupOmarchyIsNotBlockedByAnUntrustedLocalConfig(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".hey"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".hey", "config.json"), []byte(`{"base_url":"https://untrusted.example.com"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("OMARCHY_PATH", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"setup", "omarchy", "--remove", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("an untrusted checkout config must not block setup omarchy in a non-TTY, got %v", err)
	}
	if parent, _, _ := root.Find([]string{"setup"}); !commandUsesRuntimeConfig(parent) || commandIgnoresLocalConfig(parent) {
		t.Error("plain hey setup signs in and still uses runtime config")
	}
}

func TestOmarchySetupPreservesLargeIntegersInShellConfig(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	if err := os.MkdirAll(env.configDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// 2^53+1 is not representable as a float64; a naive round trip would round it.
	shell := `{"version":1,"bar":{"layout":{"left":[],"center":[],"right":[]}},"custom":{"token":9007199254740993}}`
	if err := os.WriteFile(env.shellPath(), []byte(shell), 0o644); err != nil {
		t.Fatal(err)
	}

	if steps := statuses(omarchySetup{env: env}.apply()); steps["bar indicator"] != "installed" {
		t.Fatalf("bar step = %q", steps["bar indicator"])
	}
	if out := readText(t, env.shellPath()); !strings.Contains(out, "9007199254740993") {
		t.Errorf("an unrelated large integer must survive the rewrite exactly:\n%s", out)
	}
}

func TestSetupOmarchyRejectsARelativeHome(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("OMARCHY_PATH", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", ".")
	cwd := t.TempDir()
	t.Chdir(cwd)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"setup", "omarchy", "--remove", "--json"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("a relative HOME must be refused, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".config")); !os.IsNotExist(err) {
		t.Error("nothing may be touched relative to the working directory")
	}
}

func TestOmarchySetupKeepsDanglingTemplateSymlink(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	if err := os.MkdirAll(filepath.Dir(env.templatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(env.home, "dotfiles", "gone", "hey.toml.tpl"), env.templatePath()); err != nil {
		t.Fatal(err)
	}

	setup := omarchySetup{env: env}
	if steps := statuses(setup.apply()); steps["theme template"] != "kept" {
		t.Errorf("a dangling template link is the user's, got %q", steps["theme template"])
	}
	if steps := statuses(setup.remove()); steps["theme template"] != "kept" {
		t.Errorf("remove must not delete a link whose ownership it cannot read, got %q", steps["theme template"])
	}
	if info, err := os.Lstat(env.templatePath()); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("the dangling symlink must survive")
	}
}

func TestBarStatusStaysDarkOnAMalformedGlobalConfig(t *testing.T) {
	server := imboxServer(t, `[{"id": 1, "name": "Invoice #4021", "seen": false}]`)
	defer server.Close()
	t.Setenv("HEY_TOKEN", "test-token")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(configHome, "hey-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "hey-cli", "config.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"omarchy", "bar-status", "--base-url", server.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a broken global config must not make the poller exit nonzero, got %v", err)
	}
	if buf.String() != "" {
		t.Errorf("with no trustworthy configuration the poller must stay dark, not light against a guessed server: %q", buf.String())
	}

	root = newRootCmd()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"boxes"})
	if err := root.Execute(); err == nil {
		t.Error("ordinary commands still report a broken global config")
	}
}

func TestOmarchySetupKeepsForeignTemplate(t *testing.T) {
	env, ran := testOmarchyEnv(t)
	foreign := "# my own hey theme template\naccent = \"#ff00ff\"\n"
	if err := os.MkdirAll(filepath.Dir(env.templatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.templatePath(), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	setup := omarchySetup{env: env}
	if steps := statuses(setup.apply()); steps["theme template"] != "kept" {
		t.Errorf("a template hey did not write must be kept, got %q", steps["theme template"])
	}
	if readText(t, env.templatePath()) != foreign {
		t.Error("foreign template was overwritten")
	}
	if steps := statuses(setup.remove()); steps["theme template"] != "kept" {
		t.Errorf("remove must leave a foreign template alone, got %q", steps["theme template"])
	}
	if readText(t, env.templatePath()) != foreign {
		t.Error("foreign template was deleted")
	}
	if len(*ran) != 0 {
		t.Errorf("no theme refresh should run for a kept template, ran %v", *ran)
	}
}

func TestInsertMenuBlockSkipsBracesInComments(t *testing.T) {
	content := "// A row looks like {\"icon\": \"x\"}.\n/* or a block: { nested } */\n{\n  \"mine\": {},\n}\n"
	next, ok := insertMenuBlock(content, omarchyMenuBlock())
	if !ok {
		t.Fatal("insert failed")
	}
	if !strings.HasPrefix(next, "// A row looks like") {
		t.Errorf("leading comments must be preserved above the block:\n%s", next)
	}
	if !strings.Contains(next, "*/\n{\n"+omarchyMenuBegin) {
		t.Errorf("block must land after the structural brace, not a commented one:\n%s", next)
	}
	if _, ok := insertMenuBlock("// only a comment with { in it", omarchyMenuBlock()); ok {
		t.Error("a file with no structural brace should be refused")
	}
	if _, ok := insertMenuBlock("[\n  {\"rows\": {}}\n]\n", omarchyMenuBlock()); ok {
		t.Error("an array root must be refused rather than having the block inserted into a nested object")
	}
	if _, ok := insertMenuBlock("  // leading comment\n  {\"mine\": {}}\n", omarchyMenuBlock()); !ok {
		t.Error("a commented object root is still an object root")
	}
}

func TestInsertMenuBlockReplacesStaleBlock(t *testing.T) {
	stale := "{\n" + omarchyMenuBegin + "\n  \"hey\": {\"label\":\"old\"},\n" + omarchyMenuEnd + "\n  \"mine\": {},\n}\n"
	next, ok := insertMenuBlock(stale, omarchyMenuBlock())
	if !ok {
		t.Fatal("insert failed")
	}
	if strings.Count(next, omarchyMenuBegin) != 1 || strings.Contains(next, `"old"`) {
		t.Errorf("stale block should be replaced:\n%s", next)
	}
	if !strings.Contains(next, `"mine"`) {
		t.Errorf("user rows lost:\n%s", next)
	}
	if _, ok := insertMenuBlock("not json at all", omarchyMenuBlock()); ok {
		t.Error("a file with no object should be refused")
	}
}

func TestSetupOmarchyRequiresOmarchy(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("OMARCHY_PATH", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"setup", "omarchy", "--json"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "Omarchy not detected") {
		t.Fatalf("expected detection error, got %v", err)
	}
}

func imboxServer(t *testing.T, postings string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/imbox.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": 1, "name": "Imbox", "kind": "inbox", "postings": ` + postings + `}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func runBarStatus(t *testing.T, serverURL string, authenticated bool) (string, error) {
	t.Helper()
	if authenticated {
		t.Setenv("HEY_TOKEN", "test-token")
	} else {
		t.Setenv("HEY_TOKEN", "")
	}
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"omarchy", "bar-status", "--base-url", serverURL})
	err := root.Execute()
	return buf.String(), err
}

func TestBarStatusLightsOnUnread(t *testing.T) {
	server := imboxServer(t, `[{"id": 1, "name": "Lunch on Thursday?", "seen": true}, {"id": 2, "name": "Invoice #4021", "seen": false}]`)
	defer server.Close()

	out, err := runBarStatus(t, server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	var module map[string]any
	if err := json.Unmarshal([]byte(out), &module); err != nil {
		t.Fatalf("output is not JSON: %q", out)
	}
	if module["class"] != "active" || module["text"] != omarchyBarGlyph {
		t.Errorf("unexpected module: %v", module)
	}
}

func TestBarStatusSilentWhenClear(t *testing.T) {
	server := imboxServer(t, `[{"id": 1, "name": "Lunch on Thursday?", "seen": true}]`)
	defer server.Close()

	out, err := runBarStatus(t, server.URL, true)
	if err != nil || out != "" {
		t.Errorf("clear imbox should print nothing and succeed, got %q, %v", out, err)
	}
}

func TestBarStatusSilentWhenUnauthenticatedOrOffline(t *testing.T) {
	server := imboxServer(t, `[]`)
	defer server.Close()

	out, err := runBarStatus(t, server.URL, false)
	if err != nil || out != "" {
		t.Errorf("logged out should print nothing and succeed, got %q, %v", out, err)
	}

	server.Close()
	out, err = runBarStatus(t, server.URL, true)
	if err != nil || out != "" {
		t.Errorf("offline should print nothing and succeed, got %q, %v", out, err)
	}
}
