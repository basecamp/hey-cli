package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
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
	writeShell(t, env, pluginShellJSON)
	setup := omarchySetup{env: env}

	first := setup.apply()
	for _, name := range []string{"desktop entry", "menu", "theme template"} {
		if status := statuses(first)[name]; status != "installed" {
			t.Errorf("%s: first run = %q, want installed", name, status)
		}
	}
	// The plugin entry is already there and nothing asked for a change: the
	// step reports the current notify setting without rewriting shell.json.
	if bar := stepNamed(first, "bar plugin"); bar.Status != "unchanged" || bar.Detail != "notifications off" {
		t.Errorf("bar plugin: first run = %q %q, want unchanged / notifications off", bar.Status, bar.Detail)
	}
	if readText(t, env.shellPath()) != pluginShellJSON {
		t.Error("a plain run must not rewrite shell.json")
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
	writeShell(t, env, pluginShellJSON)
	on := true
	setup := omarchySetup{env: env, notify: &on}

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
	if entry := pluginEntry(t, env); entry["notify"] != true {
		t.Fatalf("--notify should set notify on the plugin entry, got %v", entry)
	}

	removed := statuses(omarchySetup{env: env}.remove())
	for name, status := range removed {
		switch name {
		case "bar indicator":
			if status != "absent" {
				t.Errorf("no legacy module was installed, got %q", status)
			}
		case "bar plugin":
			if status != "kept" {
				t.Errorf("the plugin's notify setting is the plugin's own, got %q", status)
			}
		default:
			if status != "removed" {
				t.Errorf("%s: remove = %q, want removed", name, status)
			}
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
	// The plugin entry is the user's (omarchy plugin add wrote it), and its
	// notify setting is set as readily from the panel as from here: removal
	// cannot tell a preference it wrote from one it didn't, so it leaves it.
	entry := pluginEntry(t, env)
	if entry == nil {
		t.Fatal("remove must not delete the plugin's layout entry")
	}
	if entry["notify"] != true {
		t.Errorf("remove must leave the plugin's notify setting alone, got %v", entry)
	}

	again := statuses(omarchySetup{env: env}.remove())
	for name, status := range again {
		if name == "bar plugin" {
			if status != "kept" {
				t.Errorf("second remove: bar plugin = %q, want kept", status)
			}
			continue
		}
		if status != "absent" {
			t.Errorf("%s: second remove = %q, want absent", name, status)
		}
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
	if steps["bar plugin"] != "failed" {
		t.Errorf("a shell.json we cannot round-trip must fail rather than be rewritten, got %q", steps["bar plugin"])
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
	if steps := statuses(omarchySetup{env: env}.apply()); steps["bar plugin"] != "failed" {
		t.Errorf("a null shell.json must fail the bar step, got %q", steps["bar plugin"])
	}

	// A version-less object is ignored by the shell; refuse to edit it.
	if err := os.WriteFile(env.shellPath(), []byte(`{"bar":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := omarchySetup{env: env}.apply()
	for _, step := range steps {
		if step.Name == "bar plugin" {
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
		if steps := statuses(omarchySetup{env: env}.apply()); steps["bar plugin"] != "failed" {
			t.Errorf("%s: a wrong-typed value must fail, not be replaced, got %q", shell, steps["bar plugin"])
		}
		if readText(t, env.shellPath()) != shell {
			t.Errorf("%s: the file must be left untouched", shell)
		}
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
	typed := apierr.AsError(err)
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
	// 2^53+1 is not representable as a float64; a naive round trip would round it.
	writeShell(t, env, `{"version":1,"bar":{"layout":{"left":[],"center":[],"right":[{"id":"37signals.hey"}]}},"custom":{"token":9007199254740993}}`)
	on := true

	if steps := statuses(omarchySetup{env: env, notify: &on}.apply()); steps["bar plugin"] != "installed" {
		t.Fatalf("bar step = %q", steps["bar plugin"])
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

// pluginShellJSON is a shell.json whose bar layout carries the 37signals.hey
// plugin entry, as `omarchy plugin add --enable` writes it.
const pluginShellJSON = `{"version":1,"bar":{"layout":{"left":[{"id":"omarchy.menu"}],"center":[{"id":"omarchy.clock"}],"right":[{"id":"37signals.hey"},{"id":"omarchy.tray"}]}}}`

// legacyShellJSON is what an earlier `hey setup omarchy` wrote: the layout
// seeded from Omarchy's defaults with the inline hey-unread module in front.
const legacyShellJSON = `{"version":1,"bar":{"layout":{"left":[{"id":"omarchy.menu"}],"center":[{"id":"omarchy.clock"}],"right":[
  {"id":"hey-unread","type":"command","exec":"hey omarchy bar-status --notify","interval":180,"tooltip":"HEY","onClick":"omarchy-launch-or-focus-tui --app-id=org.omarchy.hey hey tui"},
  {"id":"omarchy.tray"},{"id":"omarchy.power"}]}}}`

func writeShell(t *testing.T, env omarchyEnv, content string) {
	t.Helper()
	if err := os.MkdirAll(env.configDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.shellPath(), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readShell(t *testing.T, env omarchyEnv) map[string]any {
	t.Helper()
	var shell map[string]any
	if err := json.Unmarshal([]byte(readText(t, env.shellPath())), &shell); err != nil {
		t.Fatal(err)
	}
	return shell
}

// pluginEntry returns the 37signals.hey layout entry, or nil.
func pluginEntry(t *testing.T, env omarchyEnv) map[string]any {
	t.Helper()
	bar, _ := readShell(t, env)["bar"].(map[string]any)
	layout, _ := bar["layout"].(map[string]any)
	return barLayoutModule(layout, omarchyBarPluginID)
}

func stepNamed(steps []omarchyStep, name string) omarchyStep {
	for _, step := range steps {
		if step.Name == name {
			return step
		}
	}
	return omarchyStep{}
}

func TestOmarchySetupSkipsTheBarWithoutThePlugin(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	on := true

	// No shell.json at all: nothing to configure, and nothing is created —
	// setup never adds a layout entry, the plugin install does.
	bar := stepNamed(omarchySetup{env: env, notify: &on}.apply(), "bar plugin")
	if bar.Status != "skipped" || !strings.Contains(bar.Detail, omarchyBarPluginInstall) {
		t.Errorf("without the plugin the bar step should say how to install it, got %q %q", bar.Status, bar.Detail)
	}
	if _, err := os.Stat(env.shellPath()); !os.IsNotExist(err) {
		t.Error("setup must not create a shell.json just to skip")
	}

	// A layout without the plugin entry is the same thing.
	writeShell(t, env, defaultShellJSON)
	if bar := stepNamed(omarchySetup{env: env}.apply(), "bar plugin"); bar.Status != "skipped" {
		t.Errorf("a layout without the plugin should be skipped, got %q", bar.Status)
	}
	if readText(t, env.shellPath()) != defaultShellJSON {
		t.Error("skipping must leave shell.json untouched")
	}
}

func TestOmarchySetupNotifyTogglesThePluginSetting(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	writeShell(t, env, pluginShellJSON)
	on, off := true, false

	bar := stepNamed(omarchySetup{env: env, notify: &on}.apply(), "bar plugin")
	if bar.Status != "installed" || bar.Detail != "notifications on" || pluginEntry(t, env)["notify"] != true {
		t.Errorf("--notify should set notify on the entry, got %q %q %v", bar.Status, bar.Detail, pluginEntry(t, env))
	}
	if bar := stepNamed(omarchySetup{env: env, notify: &on}.apply(), "bar plugin"); bar.Status != "unchanged" {
		t.Errorf("--notify twice should be idempotent, got %q", bar.Status)
	}
	bar = stepNamed(omarchySetup{env: env}.apply(), "bar plugin")
	if bar.Status != "unchanged" || bar.Detail != "notifications on" || pluginEntry(t, env)["notify"] != true {
		t.Errorf("a plain re-run must leave notifications as they are, got %q %q", bar.Status, bar.Detail)
	}
	bar = stepNamed(omarchySetup{env: env, notify: &off}.apply(), "bar plugin")
	if _, has := pluginEntry(t, env)["notify"]; bar.Status != "installed" || bar.Detail != "notifications off" || has {
		t.Errorf("--no-notify should delete the key, got %q %q %v", bar.Status, bar.Detail, pluginEntry(t, env))
	}
	if bar := stepNamed(omarchySetup{env: env, notify: &off}.apply(), "bar plugin"); bar.Status != "unchanged" {
		t.Errorf("--no-notify twice should be idempotent, got %q", bar.Status)
	}
	// Only a JSON true counts, as in the plugin's own setting() read.
	writeShell(t, env, `{"version":1,"bar":{"layout":{"right":[{"id":"37signals.hey","notify":"yes"}]}}}`)
	if bar := stepNamed(omarchySetup{env: env}.apply(), "bar plugin"); bar.Detail != "notifications off" {
		t.Errorf("a non-boolean notify is off, got %q", bar.Detail)
	}
}

func TestOmarchySetupFindsThePluginInAnySection(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	writeShell(t, env, `{"version":1,"bar":{"layout":{"left":[{"id":"omarchy.menu"}],"center":[{"id":"37signals.hey"},{"id":"omarchy.clock"}],"right":[]}}}`)
	on := true

	if bar := stepNamed(omarchySetup{env: env, notify: &on}.apply(), "bar plugin"); bar.Status != "installed" {
		t.Fatalf("bar step = %q %q", bar.Status, bar.Detail)
	}
	center := readShell(t, env)["bar"].(map[string]any)["layout"].(map[string]any)["center"].([]any)
	if entry := center[0].(map[string]any); entry["id"] != "37signals.hey" || entry["notify"] != true {
		t.Errorf("the entry should be updated in place: %v", center)
	}
}

func TestOmarchySetupRemovesTheLegacyBarModule(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	writeShell(t, env, legacyShellJSON)

	// No plugin yet: the legacy module goes, reported as its own step, and the
	// plugin step says what to install — and how to keep the toasts the module
	// had on. The layout equalled the defaults once the module was out, so it
	// goes too and the user is back to inheriting Omarchy's defaults.
	steps := omarchySetup{env: env}.apply()
	if legacy := stepNamed(steps, "bar indicator"); legacy.Status != "removed" || !strings.Contains(legacy.Detail, "hey-unread") {
		t.Errorf("legacy removal should be its own step, got %q %q", legacy.Status, legacy.Detail)
	}
	if bar := stepNamed(steps, "bar plugin"); bar.Status != "skipped" || !strings.Contains(bar.Detail, omarchyBarPluginInstall) || !strings.Contains(bar.Detail, "--notify") {
		t.Errorf("the plugin step should say how to install and how to keep notifying, got %q %q", bar.Status, bar.Detail)
	}
	if shell := readShell(t, env); shell["bar"] != nil {
		t.Errorf("a defaults-seeded layout should be dropped with the module: %v", shell)
	}
	steps = omarchySetup{env: env}.apply()
	if stepNamed(steps, "bar indicator").Name != "" || stepNamed(steps, "bar plugin").Status != "skipped" {
		t.Errorf("once migrated there is no legacy step and the plugin step skips, got %v", statuses(steps))
	}

	// With the plugin present and silent on notify, the legacy module's
	// --notify carries over; a string-form entry sharing its id is unowned
	// and survives.
	writeShell(t, env, `{"version":1,"bar":{"layout":{"left":[],"center":[],"right":[
	  {"id":"hey-unread","type":"command","exec":"hey omarchy bar-status --notify","interval":180},
	  {"id":"37signals.hey"},"hey-unread",{"id":"omarchy.tray"}]}}}`)
	steps = omarchySetup{env: env}.apply()
	if bar := stepNamed(steps, "bar plugin"); bar.Status != "installed" || bar.Detail != "notifications on" {
		t.Errorf("the notify choice should move to the plugin, got %q %q", bar.Status, bar.Detail)
	}
	right := readShell(t, env)["bar"].(map[string]any)["layout"].(map[string]any)["right"].([]any)
	if len(right) != 3 || right[0].(map[string]any)["notify"] != true || right[1] != "hey-unread" || barEntryID(right[2]) != "omarchy.tray" {
		t.Errorf("only the map-form legacy module goes and notify lands on the plugin: %v", right)
	}

	// An explicit plugin setting, or an explicit flag, wins over the legacy
	// module's choice.
	writeShell(t, env, `{"version":1,"bar":{"layout":{"right":[{"id":"hey-unread","type":"command","exec":"hey omarchy bar-status --notify"},{"id":"37signals.hey","notify":false}]}}}`)
	if bar := stepNamed(omarchySetup{env: env}.apply(), "bar plugin"); bar.Detail != "notifications off" || bar.Status != "unchanged" {
		t.Errorf("an explicit plugin setting is kept, and the legacy removal is the other step's news, got %q %q", bar.Status, bar.Detail)
	}
	off := false
	writeShell(t, env, `{"version":1,"bar":{"layout":{"right":[{"id":"hey-unread","type":"command","exec":"hey omarchy bar-status --notify"},{"id":"37signals.hey"}]}}}`)
	if bar := stepNamed(omarchySetup{env: env, notify: &off}.apply(), "bar plugin"); bar.Detail != "notifications off" {
		t.Errorf("--no-notify wins over the legacy choice, got %q", bar.Detail)
	}
	if _, has := pluginEntry(t, env)["notify"]; has {
		t.Error("--no-notify must not leave a notify key behind")
	}

	// --remove takes the legacy module out as well.
	writeShell(t, env, legacyShellJSON)
	if legacy := stepNamed(omarchySetup{env: env}.remove(), "bar indicator"); legacy.Status != "removed" {
		t.Errorf("remove should take the legacy module out, got %q", legacy.Status)
	}
	if strings.Contains(readText(t, env.shellPath()), "hey-unread") {
		t.Error("legacy module still present after remove")
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
	writeShell(t, env, legacyShellJSON)

	if legacy := stepNamed(omarchySetup{env: env}.apply(), "bar indicator"); legacy.Status != "removed" {
		t.Fatalf("legacy step = %q %q", legacy.Status, legacy.Detail)
	}
	if shell := readShell(t, env); shell["bar"] != nil {
		t.Errorf("the per-user omarchy tree should supply the defaults the layout is compared to: %v", shell)
	}
}

func TestSetupOmarchyIgnoresRepositoryLocalConfig(t *testing.T) {
	root := newRootCmd()
	command, _, _ := root.Find([]string{"setup", "omarchy"})
	if !commandIgnoresLocalConfig(command) {
		t.Error("setup omarchy edits fixed desktop paths only and must ignore a checkout's config")
	}
	if boxes, _, _ := root.Find([]string{"boxes"}); commandIgnoresLocalConfig(boxes) {
		t.Error("ordinary commands keep honouring repository-local config")
	}
}
