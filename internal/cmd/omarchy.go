package cmd

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/output"
)

// Omarchy integration: `hey setup omarchy` installs hey-cli into the desktop
// (launcher entry, menu rows, bar indicator, theme template) and `hey omarchy
// bar-status` is the command the bar indicator runs.
//
// Omarchy already ships HEY as a web app (SUPER+SHIFT+E, the mailto handler, a
// HEY.desktop). Everything here complements that under its own names and never
// edits the user's keybindings file — the binding is printed for them to paste.

//go:embed omarchy/hey.toml.tpl
var omarchyThemeTemplate string

const (
	omarchyAppID        = "org.omarchy.hey"
	omarchyDesktopName  = "HEY TUI"
	omarchyBarModuleID  = "hey-unread"
	omarchyMenuBegin    = "  // >>> hey-cli — managed by `hey setup omarchy`, do not edit between the markers"
	omarchyMenuEnd      = "  // <<< hey-cli"
	omarchyFocusCommand = "omarchy-launch-or-focus-tui --app-id=" + omarchyAppID + " hey tui"
	omarchyBarGlyph     = "" // nf-fa-envelope; verified to render in the bar's JetBrainsMono Nerd Font
	// omarchy-theme-refresh re-renders every template and retints every app; a
	// minute is generous.
	omarchyCommandTimeout = time.Minute
	// The hint spells the focus command out rather than using `{ tui = "hey tui" }`:
	// the lua helper shell-quotes that into one word and launch-or-focus-tui would
	// derive the app-id from it, never matching the window every other surface opens.
	omarchyKeybindHint = `Add a keybinding yourself — hey never edits ~/.config/hypr/bindings.lua:

  o.bind("SUPER + SHIFT + ALT + H", "HEY TUI", "` + omarchyFocusCommand + `")

SUPER+SHIFT+E still opens the HEY web app. To point it at the TUI instead:

  hl.unbind("SUPER + SHIFT + E")
  o.bind("SUPER + SHIFT + E", "HEY", "` + omarchyFocusCommand + `")
`
)

// omarchyEnv is everything the setup steps touch, injectable for tests.
type omarchyEnv struct {
	home        string
	omarchyPath string
	iconRoots   []string // icon theme roots searched for Omarchy's HEY icon
	run         func(name string, args ...string) error
	runOutput   func(name string, args ...string) (string, error)
}

func liveOmarchyEnv() omarchyEnv {
	home, _ := os.UserHomeDir()
	return omarchyEnv{
		home:        home,
		omarchyPath: os.Getenv("OMARCHY_PATH"),
		iconRoots:   []string{filepath.Join(home, ".local", "share", "icons"), "/usr/share/icons"},
		run: func(name string, args ...string) error {
			if _, err := exec.LookPath(name); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), omarchyCommandTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: fixed omarchy command names
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
			return cmd.Run()
		},
		runOutput: func(name string, args ...string) (string, error) {
			if _, err := exec.LookPath(name); err != nil {
				return "", err
			}
			ctx, cancel := context.WithTimeout(context.Background(), omarchyCommandTimeout)
			defer cancel()
			out, err := exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // G204: fixed omarchy command names
			return string(out), err
		},
	}
}

func (e omarchyEnv) detected() bool {
	if e.omarchyPath != "" {
		return true
	}
	info, err := os.Stat(filepath.Join(e.home, ".local", "state", "omarchy"))
	return err == nil && info.IsDir()
}

func (e omarchyEnv) configDir() string { return filepath.Join(e.home, ".config", "omarchy") }

func (e omarchyEnv) desktopPath() string {
	return filepath.Join(e.home, ".local", "share", "applications", omarchyDesktopName+".desktop")
}

func (e omarchyEnv) menuPath() string {
	return filepath.Join(e.configDir(), "extensions", "omarchy-menu.jsonc")
}

func (e omarchyEnv) shellPath() string { return filepath.Join(e.configDir(), "shell.json") }

func (e omarchyEnv) templatePath() string {
	return filepath.Join(e.configDir(), "themed", "hey.toml.tpl")
}

// defaultShellPath finds Omarchy's shipped shell.json: OMARCHY_PATH when set,
// else the per-user install, else the system one — OMARCHY_PATH is absent in
// non-login and agent environments, and the normal install is per-user.
func (e omarchyEnv) defaultShellPath() string {
	roots := []string{e.omarchyPath, filepath.Join(e.home, ".local", "share", "omarchy"), "/usr/share/omarchy"}
	for _, root := range roots {
		if root == "" {
			continue
		}
		path := filepath.Join(root, "config", "omarchy", "shell.json")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join("/usr/share/omarchy", "config", "omarchy", "shell.json")
}

// iconName picks the HEY icon Omarchy installs for its web app when present, and a
// standard freedesktop mail icon otherwise.
func (e omarchyEnv) iconName() string {
	for _, root := range e.iconRoots {
		matches, _ := filepath.Glob(filepath.Join(root, "hicolor", "*", "apps", "hey.*"))
		if len(matches) > 0 {
			return "hey"
		}
	}
	return "internet-mail"
}

// --- Steps ---

type omarchyStep struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // installed, unchanged, removed, absent, kept, failed
	Detail  string `json:"detail,omitempty"`
	Path    string `json:"path,omitempty"`
	failure error
}

type omarchySetup struct {
	env    omarchyEnv
	notify *bool // nil keeps the bar module's current exec as it is
}

func (s omarchySetup) apply() []omarchyStep {
	return []omarchyStep{
		s.installDesktop(),
		s.installMenu(),
		s.installBar(),
		s.installTemplate(),
	}
}

func (s omarchySetup) remove() []omarchyStep {
	steps := []omarchyStep{
		s.removeDesktop(),
		s.removeMenu(),
		s.removeBar(),
		s.removeTemplate(),
	}
	// The fingerprints go only once the module that uses them is gone: while a
	// failed bar removal leaves the poller scheduled, deleting them would make
	// its next tick reseed and swallow the mail that arrived in between.
	if steps[2].failure != nil {
		return append(steps, omarchyStep{Name: "poll state", Path: omarchyPollStatePath(), Status: "kept",
			Detail: "bar module still installed; fingerprints kept for its next tick"})
	}
	return append(steps, s.removePollState())
}

func stepResult(name, path string, changed bool, err error, installed, unchanged string) omarchyStep {
	step := omarchyStep{Name: name, Path: path}
	switch {
	case err != nil:
		step.Status, step.Detail, step.failure = "failed", err.Error(), err
	case changed:
		step.Status = installed
	default:
		step.Status = unchanged
	}
	return step
}

// Desktop entry: what `omarchy-tui-install` writes, under the app-id every other
// surface launches with so they all focus the same window.

func omarchyDesktopEntry(icon string) string {
	return fmt.Sprintf(`[Desktop Entry]
Version=1.0
Name=%s
Comment=HEY email, contacts and calendar in the terminal
Exec=xdg-terminal-exec --app-id=%s -e hey tui
Terminal=false
Type=Application
Icon=%s
StartupNotify=true
`, omarchyDesktopName, omarchyAppID, icon)
}

func (s omarchySetup) installDesktop() omarchyStep {
	path := s.env.desktopPath()
	changed, err := writeFileIfChanged(path, []byte(omarchyDesktopEntry(s.env.iconName())), 0o755)
	return stepResult("desktop entry", path, changed, err, "installed", "unchanged")
}

func (s omarchySetup) removeDesktop() omarchyStep {
	path := s.env.desktopPath()
	changed, err := removeFileIfPresent(path)
	return stepResult("desktop entry", path, changed, err, "removed", "absent")
}

// Menu: a marker-delimited block in the user's JSONC menu extension. The shell
// tolerates trailing commas and strips full-line // comments, so the block is
// inserted right after the opening brace with every row comma-terminated. One
// root row for now; it becomes a submenu when there is more than one thing to
// open. The guard is a PATH lookup — menu guards must never call hey itself.

// omarchyMenuBlock is the managed rows. The member key is hey-tui rather than
// hey so a user's own hey row can coexist instead of becoming a duplicate key.
func omarchyMenuBlock() string {
	row := fmt.Sprintf(`  "hey-tui": {"icon":"%s","label":"HEY","action":"%s","when":"command -v hey >/dev/null"},`,
		omarchyBarGlyph, omarchyFocusCommand)
	return omarchyMenuBegin + "\n" + row + "\n" + omarchyMenuEnd + "\n"
}

func (s omarchySetup) installMenu() omarchyStep {
	path := s.env.menuPath()
	current, err := os.ReadFile(path) //nolint:gosec // G304: fixed path under the user's config dir
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return stepResult("menu", path, false, err, "", "")
	}
	next, ok := insertMenuBlock(string(current), omarchyMenuBlock())
	if !ok {
		return stepResult("menu", path, false, errors.New("could not find the top-level object to extend"), "", "")
	}
	changed, err := writeFileIfChanged(path, []byte(next), 0o644)
	return stepResult("menu", path, changed, err, "installed", "unchanged")
}

func (s omarchySetup) removeMenu() omarchyStep {
	path := s.env.menuPath()
	current, err := os.ReadFile(path) //nolint:gosec // G304: fixed path under the user's config dir
	if errors.Is(err, os.ErrNotExist) {
		return stepResult("menu", path, false, nil, "", "absent")
	}
	if err != nil {
		return stepResult("menu", path, false, err, "", "")
	}
	next := stripMenuBlock(string(current))
	changed, err := writeFileIfChanged(path, []byte(next), 0o644)
	return stepResult("menu", path, changed, err, "removed", "absent")
}

// insertMenuBlock places block after the file's first structural `{`, replacing
// any earlier block. An empty file becomes a fresh object.
func insertMenuBlock(content, block string) (string, bool) {
	content = stripMenuBlock(content)
	if strings.TrimSpace(content) == "" {
		return "{\n" + block + "}\n", true
	}
	idx := structuralBraceIndex(content)
	if idx < 0 {
		return "", false
	}
	head := content[:idx+1]
	tail := strings.TrimLeft(content[idx+1:], " \t")
	if !strings.HasPrefix(tail, "\n") {
		tail = "\n" + tail
	}
	return head + "\n" + block + strings.TrimPrefix(tail, "\n"), true
}

// structuralBraceIndex is the index of the first `{` outside JSONC comments
// and strings, or -1 — a leading doc comment showing an object-shaped example
// must not be mistaken for the menu object itself.
func structuralBraceIndex(content string) int {
	// Only whitespace and comments may precede the root token; anything else
	// (an array root, a bare string) means the file is not a menu object.
	for i := 0; i < len(content); i++ {
		switch content[i] {
		case ' ', '\t', '\r', '\n':
		case '{':
			return i
		case '/':
			if i+1 >= len(content) {
				return -1
			}
			switch content[i+1] {
			case '/':
				i += 2
				for i < len(content) && content[i] != '\n' {
					i++
				}
			case '*':
				end := strings.Index(content[i+2:], "*/")
				if end < 0 {
					return -1
				}
				i += 2 + end + 1
			default:
				return -1
			}
		default:
			return -1
		}
	}
	return -1
}

func stripMenuBlock(content string) string {
	start := strings.Index(content, omarchyMenuBegin)
	if start < 0 {
		return content
	}
	end := strings.Index(content[start:], omarchyMenuEnd)
	if end < 0 {
		return content
	}
	after := content[start+end+len(omarchyMenuEnd):]
	after = strings.TrimPrefix(after, "\n")
	return content[:start] + after
}

// Bar: an inline command module in shell.json's bar layout. The shell hot-reloads
// the file, so the indicator appears as soon as it is written. Toast enablement
// lives in the module's exec string — no config key, visible where it acts,
// removed with --remove.

func omarchyBarExec(notify bool) string {
	if notify {
		return "hey omarchy bar-status --notify"
	}
	return "hey omarchy bar-status"
}

func omarchyBarModule(notify bool) map[string]any {
	return map[string]any{
		"id":       omarchyBarModuleID,
		"type":     "command",
		"exec":     omarchyBarExec(notify),
		"interval": 180,
		"tooltip":  "HEY",
		"onClick":  omarchyFocusCommand,
	}
}

func notifyDetail(notify bool) string {
	if notify {
		return "notifications on"
	}
	return "notifications off"
}

func (s omarchySetup) installBar() omarchyStep {
	path := s.env.shellPath()
	shell, err := s.loadShellConfig()
	if err != nil {
		return stepResult("bar indicator", path, false, err, "", "")
	}
	layout, err := s.barLayout(shell)
	if err != nil {
		return stepResult("bar indicator", path, false, err, "", "")
	}
	module := barLayoutModule(layout, omarchyBarModuleID)
	notify := s.notify != nil && *s.notify
	if module == nil {
		if notify {
			// Enabling toasts with stale fingerprints around would toast the
			// accumulated diff; if they cannot be dropped, do not enable.
			if _, err := removeFileIfPresent(omarchyPollStatePath()); err != nil {
				return stepResult("bar indicator", path, false, fmt.Errorf("cannot drop stale poll state: %w", err), "", "")
			}
		}
		right, ok := layout["right"].([]any)
		if raw, present := layout["right"]; present && raw != nil && !ok {
			return stepResult("bar indicator", path, false, fmt.Errorf("shell.json: bar.layout.right is %T, not a list", raw), "", "")
		}
		layout["right"] = append([]any{omarchyBarModule(notify)}, right...)
		changed, err := writeJSONFile(path, shell)
		step := stepResult("bar indicator", path, changed, err, "installed", "unchanged")
		if err == nil && s.notify != nil {
			step.Detail = notifyDetail(notify)
		}
		return step
	}
	// An existing module is reconciled field by field, keeping its section and
	// position, so a re-run after an upgrade picks up a changed exec, click
	// command or interval; only the notify choice is preserved when the caller
	// did not state one.
	exec, _ := module["exec"].(string)
	wasNotifying := strings.HasSuffix(exec, " --notify")
	if s.notify == nil {
		notify = wasNotifying
	}
	if notify && !wasNotifying {
		// Turning toasts (back) on: drop any stale fingerprints so the first
		// tick reseeds from the current Imbox instead of toasting whatever
		// accumulated while they were off. If they cannot be dropped, fail
		// rather than enable a poller that would toast the backlog.
		if _, err := removeFileIfPresent(omarchyPollStatePath()); err != nil {
			return stepResult("bar indicator", path, false, fmt.Errorf("cannot drop stale poll state: %w", err), "", "")
		}
	}
	desired := omarchyBarModule(notify)
	changed := !sameJSON(module, desired)
	if changed {
		clear(module)
		for key, value := range desired {
			module[key] = value
		}
		if _, err := writeJSONFile(path, shell); err != nil {
			return stepResult("bar indicator", path, false, err, "", "")
		}
	}
	step := stepResult("bar indicator", path, changed, nil, "installed", "unchanged")
	if s.notify != nil {
		step.Detail = notifyDetail(notify)
	}
	return step
}

// sameJSON compares two values by their JSON encoding, which is what makes a
// decoded float64(180) and a literal 180 read as equal.
func sameJSON(a, b any) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(left, right)
}

func (s omarchySetup) removeBar() omarchyStep {
	path := s.env.shellPath()
	shell, err := s.loadShellConfig()
	if err != nil {
		return stepResult("bar indicator", path, false, err, "", "")
	}
	bar, _ := shell["bar"].(map[string]any)
	layout, _ := bar["layout"].(map[string]any)
	if barLayoutModule(layout, omarchyBarModuleID) == nil {
		return stepResult("bar indicator", path, false, nil, "", "absent")
	}
	for section, entries := range layout {
		list, ok := entries.([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(list))
		for _, entry := range list {
			// Only the map form is ours: install treats string-form entries as
			// unowned, so removal must too.
			if _, isMap := entry.(map[string]any); !isMap || barEntryID(entry) != omarchyBarModuleID {
				kept = append(kept, entry)
			}
		}
		layout[section] = kept
	}
	// Install may have seeded the layout from Omarchy's defaults just to hold our
	// module. If what remains is exactly the current defaults, drop it so the user
	// goes back to inheriting future default-layout changes.
	if defaults, defErr := s.defaultBarLayout(); defErr == nil && sameJSON(layout, defaults) {
		delete(bar, "layout")
		if len(bar) == 0 {
			delete(shell, "bar")
		}
	}
	changed, err := writeJSONFile(path, shell)
	return stepResult("bar indicator", path, changed, err, "removed", "absent")
}

// loadShellConfig reads the user's shell.json. The shell ignores any config
// without `"version": 1` (shell.qml warns and falls back to the defaults), so a
// missing file starts from that marker and a version-less file is refused rather
// than edited into a config the shell would keep ignoring.
func (s omarchySetup) loadShellConfig() (map[string]any, error) {
	data, err := os.ReadFile(s.env.shellPath())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{"version": 1}, nil
	}
	if err != nil {
		return nil, err
	}
	shell, err := decodeJSONObject(data)
	if err != nil {
		return nil, fmt.Errorf("shell.json is not plain JSON: %w", err)
	}
	if shell == nil {
		return nil, errors.New("shell.json is not a JSON object")
	}
	if version, ok := shell["version"].(json.Number); !ok || version.String() != "1" {
		return nil, errors.New(`shell.json has no "version": 1, so the shell ignores it; add the version and re-run`)
	}
	return shell, nil
}

// decodeJSONObject decodes with UseNumber so a user's opaque numeric settings —
// an integer past float64's exact range, say — survive the round trip that
// rewriting the file implies, instead of being silently rounded.
func decodeJSONObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	// One value and nothing after it: a file with trailing data is not plain
	// JSON, and rewriting it would silently drop whatever followed.
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after the top-level object")
	}
	return object, nil
}

// barLayout returns the user's bar layout, seeding it from Omarchy's default layout
// when the user has never customized the bar — the shell treats a missing layout
// as "use the defaults", so adding one module means spelling the rest out too.
func (s omarchySetup) barLayout(shell map[string]any) (map[string]any, error) {
	// A present value of the wrong type is someone's configuration, not an
	// absence: refuse to replace it.
	bar, ok := shell["bar"].(map[string]any)
	if raw, present := shell["bar"]; present && raw != nil && !ok {
		return nil, fmt.Errorf("shell.json: bar is %T, not an object", raw)
	}
	if bar == nil {
		bar = map[string]any{}
		shell["bar"] = bar
	}
	layout, ok := bar["layout"].(map[string]any)
	if raw, present := bar["layout"]; present && raw != nil && !ok {
		return nil, fmt.Errorf("shell.json: bar.layout is %T, not an object", raw)
	}
	if ok {
		return layout, nil
	}
	layout, err := s.defaultBarLayout()
	if err != nil {
		return nil, fmt.Errorf("no bar layout in shell.json to extend: %w", err)
	}
	bar["layout"] = layout
	return layout, nil
}

// defaultBarLayout reads the bar layout Omarchy ships as its default.
func (s omarchySetup) defaultBarLayout() (map[string]any, error) {
	data, err := os.ReadFile(s.env.defaultShellPath())
	if err != nil {
		return nil, err
	}
	defaults, err := decodeJSONObject(data)
	if err != nil {
		return nil, fmt.Errorf("default shell.json: %w", err)
	}
	defaultBar, _ := defaults["bar"].(map[string]any)
	layout, ok := defaultBar["layout"].(map[string]any)
	if !ok {
		return nil, errors.New("default shell.json has no bar layout")
	}
	return layout, nil
}

// barLayoutModule finds our inline module map by id. String-form entries are
// not ours — setup always writes maps — so they are ignored here.
func barLayoutModule(layout map[string]any, id string) map[string]any {
	for _, entries := range layout {
		list, _ := entries.([]any)
		for _, entry := range list {
			if module, ok := entry.(map[string]any); ok && barEntryID(module) == id {
				return module
			}
		}
	}
	return nil
}

func barEntryID(entry any) string {
	switch v := entry.(type) {
	case string:
		return v
	case map[string]any:
		id, _ := v["id"].(string)
		return id
	}
	return ""
}

// Theme template: lets theme authors override the overlay per theme. The TUI reads
// colors.toml directly when no hey.toml is rendered, so this step is optional.

// omarchyTemplateMarker is the first line of every template hey ships. A
// hey.toml.tpl without it was written by someone else — the user, another
// installer — and is theirs: install keeps it and remove leaves it alone.
const omarchyTemplateMarker = "# hey-cli accent overlay"

// templateIsOurs reports whether the template at path carries hey's marker. A
// file that exists but cannot be read is an error, not an absence: the
// ownership guard must never be bypassed by an unreadable foreign file.
func templateIsOurs(path string) (ours, exists bool, err error) {
	current, err := os.ReadFile(path) //nolint:gosec // G304: fixed path under the user's config dir
	if errors.Is(err, os.ErrNotExist) {
		// A dangling symlink reads as missing but is still the user's link;
		// ownership cannot be established, so it is foreign, not absent.
		if info, lerr := os.Lstat(path); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			return false, true, nil
		}
		return false, false, nil
	}
	if err != nil {
		return false, true, err
	}
	return strings.HasPrefix(string(current), omarchyTemplateMarker), true, nil
}

func (s omarchySetup) installTemplate() omarchyStep {
	path := s.env.templatePath()
	ours, exists, err := templateIsOurs(path)
	if err != nil {
		return stepResult("theme template", path, false, err, "", "")
	}
	if exists && !ours {
		return omarchyStep{Name: "theme template", Path: path, Status: "kept",
			Detail: "existing template not written by hey; left as is"}
	}
	changed, err := writeFileIfChanged(path, []byte(omarchyThemeTemplate), 0o644)
	if err == nil && changed {
		if refreshErr := s.env.run("omarchy-theme-refresh"); refreshErr != nil {
			return omarchyStep{Name: "theme template", Path: path, Status: "installed",
				Detail: "rendered on the next theme switch (omarchy-theme-refresh unavailable)"}
		}
	}
	return stepResult("theme template", path, changed, err, "installed", "unchanged")
}

func (s omarchySetup) removeTemplate() omarchyStep {
	path := s.env.templatePath()
	ours, exists, err := templateIsOurs(path)
	if err != nil {
		return stepResult("theme template", path, false, err, "", "")
	}
	if exists && !ours {
		return omarchyStep{Name: "theme template", Path: path, Status: "kept",
			Detail: "existing template not written by hey; left as is"}
	}
	changed, err := removeFileIfPresent(path)
	if err == nil && changed {
		if refreshErr := s.env.run("omarchy-theme-refresh"); refreshErr != nil {
			return omarchyStep{Name: "theme template", Path: path, Status: "removed",
				Detail: "rendered hey.toml stays until the next theme switch (omarchy-theme-refresh unavailable)"}
		}
	}
	return stepResult("theme template", path, changed, err, "removed", "absent")
}

// Poll state: the new-mail fingerprint file bar-status --notify keeps. Setup
// never creates it, but --remove takes it out with everything else.

func (s omarchySetup) removePollState() omarchyStep {
	path := omarchyPollStatePath()
	changed, err := removeFileIfPresent(path)
	return stepResult("poll state", path, changed, err, "removed", "absent")
}

// --- File helpers ---

// writeFileIfChanged writes via a temp file and rename, the way Omarchy's own
// config mutators do, so an interrupted write can never leave a half-truncated
// shell.json or menu behind. A symlink (a dotfiles repo, say) is followed so
// the target is replaced rather than the link, and an existing file keeps its
// mode: perm applies to files created from nothing.
func writeFileIfChanged(path string, data []byte, perm os.FileMode) (bool, error) {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) { //nolint:gosec // G304: caller-controlled config path
		return false, nil
	}
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	} else if info, lerr := os.Lstat(path); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
		// A dangling link cannot be followed, and renaming over it would
		// silently turn the user's link into a plain file.
		return false, fmt.Errorf("%s is a symlink whose target is missing: %w", path, err)
	}
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone already after a successful rename
	writeErr := tmp.Chmod(perm)
	if writeErr == nil {
		_, writeErr = tmp.Write(data)
	}
	if closeErr := tmp.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return false, writeErr
	}
	return true, os.Rename(tmp.Name(), path)
}

func removeFileIfPresent(path string) (bool, error) {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func writeJSONFile(path string, value any) (bool, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return false, err
	}
	return writeFileIfChanged(path, buf.Bytes(), 0o644)
}

// --- hey setup omarchy ---

type setupOmarchyCommand struct {
	cmd      *cobra.Command
	remove   bool
	notify   bool
	noNotify bool
	env      omarchyEnv
}

func newSetupOmarchyCommand() *setupOmarchyCommand {
	setupOmarchyCommand := &setupOmarchyCommand{env: liveOmarchyEnv()}
	setupOmarchyCommand.cmd = &cobra.Command{
		Use:   "omarchy",
		Args:  cobra.NoArgs,
		Short: "Install hey into the Omarchy desktop",
		Long: `Install hey into the Omarchy desktop: a launcher entry, rows in the SUPER+SPACE
menu, an unread indicator on the bar, and a theme template so themes can tune the
TUI's accent colors. Every piece is idempotent and --remove takes them all out again.

--notify also toasts new Imbox mail on the indicator's poll — at most one toast per
interval, replaced rather than stacked, silenced by the notification DND toggle.
--no-notify turns the toasts back off; a plain re-run leaves them as they are.

Theming needs none of this: on Omarchy the TUI already follows the active theme.`,
		Example: `  hey setup omarchy
  hey setup omarchy --notify
  hey setup omarchy --remove`,
		Annotations: map[string]string{
			"agent_notes": "Only meaningful on Omarchy Linux. Writes to ~/.config/omarchy and ~/.local/share/applications; never edits Hyprland keybindings.",
		},
		RunE: setupOmarchyCommand.run,
	}
	setupOmarchyCommand.cmd.Flags().BoolVar(&setupOmarchyCommand.remove, "remove", false, "Remove everything hey setup omarchy installed")
	setupOmarchyCommand.cmd.Flags().BoolVar(&setupOmarchyCommand.notify, "notify", false, "Toast new Imbox mail when the bar indicator polls")
	setupOmarchyCommand.cmd.Flags().BoolVar(&setupOmarchyCommand.noNotify, "no-notify", false, "Turn new-mail toasts back off")
	setupOmarchyCommand.cmd.MarkFlagsMutuallyExclusive("notify", "no-notify")
	setupOmarchyCommand.cmd.MarkFlagsMutuallyExclusive("notify", "remove")
	setupOmarchyCommand.cmd.MarkFlagsMutuallyExclusive("no-notify", "remove")
	return setupOmarchyCommand
}

func (c *setupOmarchyCommand) run(cmd *cobra.Command, args []string) error {
	// List-only formats cannot render the step report; refuse them before any
	// file is touched rather than failing after a successful install.
	if format := writer.EffectiveFormat(); format == output.FormatIDs || format == output.FormatCount {
		return apierr.ErrUsageHint("hey setup omarchy reports steps, not a list", "use --json for machine-readable output")
	}
	// Every path is under the home directory; without one they would resolve
	// relative to the working directory, and --remove would delete from there.
	if !filepath.IsAbs(c.env.home) {
		return &apierr.Error{Code: "setup_failed", Message: "cannot resolve an absolute home directory", Hint: "set HOME to an absolute path and run hey setup omarchy again"}
	}
	// Detection gates installation only: removal operates on fixed user paths
	// and must still work after Omarchy itself is gone.
	if !c.remove && !c.env.detected() {
		return apierr.ErrUsageHint("Omarchy not detected", "hey setup omarchy needs ~/.local/state/omarchy or OMARCHY_PATH")
	}

	setup := omarchySetup{env: c.env}
	if c.notify || c.noNotify {
		setup.notify = &c.notify
	}
	var steps []omarchyStep
	if c.remove {
		steps = setup.remove()
	} else {
		steps = setup.apply()
	}

	var failures []string
	for _, step := range steps {
		if step.failure != nil {
			failures = append(failures, step.Name+": "+step.Detail)
		}
	}

	if writer.IsStyled() {
		w := cmd.OutOrStdout()
		for _, step := range steps {
			detail := step.Path
			if step.Detail != "" {
				detail = step.Detail
			}
			fmt.Fprintf(w, "%-16s %-10s %s\n", step.Name, step.Status, detail)
		}
		if !c.remove {
			fmt.Fprintln(w)
			fmt.Fprint(w, omarchyKeybindHint)
		}
		if len(failures) > 0 {
			return fmt.Errorf("%d step(s) failed", len(failures))
		}
		return nil
	}

	summary := "Omarchy integration installed"
	if c.remove {
		summary = "Omarchy integration removed"
	}
	data := map[string]any{"steps": steps}
	if !c.remove {
		data["keybind_hint"] = omarchyKeybindHint
	}
	if len(failures) > 0 {
		// An operational failure, not a usage error: some steps already changed
		// files, and they ride along in the error meta so a scripting caller can
		// see which pieces landed and which did not.
		return &apierr.Error{
			Code:    "setup_failed",
			Message: strings.Join(failures, "; "),
			Hint:    "fix the paths above and run hey setup omarchy again",
			Meta:    map[string]any{"steps": steps},
		}
	}
	return writeOK(data, output.WithSummary(summary))
}

// --- hey omarchy bar-status ---

type omarchyCommand struct {
	cmd *cobra.Command
}

func newOmarchyCommand() *omarchyCommand {
	omarchyCommand := &omarchyCommand{}
	omarchyCommand.cmd = &cobra.Command{
		Use:    "omarchy",
		Short:  "Commands the Omarchy desktop integration runs",
		Hidden: true,
	}
	omarchyCommand.cmd.AddCommand(newOmarchyBarStatusCommand().cmd)
	return omarchyCommand
}

type omarchyBarStatusCommand struct {
	cmd    *cobra.Command
	notify bool
	env    omarchyEnv
}

func newOmarchyBarStatusCommand() *omarchyBarStatusCommand {
	omarchyBarStatusCommand := &omarchyBarStatusCommand{env: liveOmarchyEnv()}
	omarchyBarStatusCommand.cmd = &cobra.Command{
		Use:   "bar-status",
		Short: "Print the bar indicator for unread Imbox mail",
		Long: `Print a Waybar-style JSON module when the Imbox has unread mail and nothing when
it does not. Never fails: when hey is logged out or offline the indicator simply
stays dark, because a bar is no place for an error message.

With --notify, also toast newly unseen Imbox mail via omarchy-notification-send —
at most one toast per run, replacing the previous one rather than stacking.`,
		Args: cobra.NoArgs,
		RunE: omarchyBarStatusCommand.run,
	}
	omarchyBarStatusCommand.cmd.Flags().BoolVar(&omarchyBarStatusCommand.notify, "notify", false, "Toast new unseen Imbox mail")
	return omarchyBarStatusCommand
}

// configDegraded is set by the root pre-run when the global configuration could
// not be loaded and a config-ignoring command went on with the defaults. The
// poller then stays dark: lighting the indicator against a guessed server would
// be a lie, and an error is not an option either.
var configDegraded bool

func (c *omarchyBarStatusCommand) run(cmd *cobra.Command, args []string) error {
	if configDegraded || !authMgr.IsAuthenticated() {
		return nil
	}
	// The omarchy command is exempt from pre-run account scoping so a failed
	// selection (offline, account gone) can never surface as an error here;
	// the configured account still applies when it can be selected.
	if err := selectConfiguredAccount(cmd.Context()); err != nil {
		return nil //nolint:nilerr // a bar is no place for an error message
	}
	// The indicator only needs to know whether anything is unseen, which the
	// first page answers. Toasts need the unseen set: capped on a steady-state
	// tick (new mail always lands on page 1), exhaustive when seeding — a first
	// run or a new identity — so no pre-existing thread can later read as new.
	pages, identity, notify := 1, "", c.notify
	if notify {
		var ok bool
		if identity, ok = omarchyPollIdentity(cmd.Context()); !ok {
			notify = false
		} else if state, existed := loadOmarchyPollState(); !existed || state.Identity != identity {
			pages = unseenSeedPageCap
		} else {
			pages = unseenPageCap
		}
	}
	unseen, complete, ok := unseenImboxPostings(cmd.Context(), pages)
	if !ok {
		return nil
	}
	if notify {
		notifyNewMail(c.env, identity, unseen, complete)
	}
	if len(unseen) == 0 {
		return nil
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), omarchyBarModuleJSON())
	return err
}

// omarchyBarModuleJSON is the Waybar-style module the bar shows when mail is
// unread. The "active" class is what the shell's command widget highlights.
func omarchyBarModuleJSON() string {
	module, _ := json.Marshal(map[string]string{ // fixed strings cannot fail to marshal
		"text":    omarchyBarGlyph,
		"tooltip": "Unread in Imbox",
		"class":   "active",
	})
	return string(module)
}

// Page limits for following an all-unseen Imbox. A steady-state tick stops at
// ten pages — three hundred unseen threads — because new mail always lands on
// page 1 and older threads are already fingerprinted. Seeding reads the whole
// unseen set (bounded only as the box command is) so that no pre-existing
// thread can later surface as new; it happens once per identity.
var (
	unseenPageCap = 10
	// maxPostingPages counts the initial page, as `hey box` does, so a box whose
	// unseen set spans exactly the cap still finds its closing seen page and
	// seeds completely.
	unseenSeedPageCap = maxPostingPages
)

// unseenImboxPostings returns the unseen Imbox postings, whether they are the
// complete unseen set, and whether the fetch succeeded. HEY orders Imbox
// postings unseen-first, so a page holding any seen posting (or nothing at
// all) closes the unseen set; while a page is all unseen the next one is
// fetched, up to maxPages. Unknown (offline, server error) counts as clear:
// the indicator stays dark rather than lying either way loudly, and the notify
// fingerprints stay untouched.
func unseenImboxPostings(ctx context.Context, maxPages int) (unseen []generated.Posting, complete, ok bool) {
	imbox, err := sdk.Boxes().GetImbox(ctx, nil)
	if err != nil || imbox == nil {
		return nil, false, false
	}
	source := mail.Source{Kind: mail.KindBox, ID: imbox.Id, BoxKind: hey.BoxKindImbox}
	page := mail.Page{Postings: imbox.Postings, Cursor: imbox.NextHistoryUrl}
	for pages := 1; ; pages++ {
		seenOnPage := false
		for _, posting := range page.Postings {
			if posting.Seen {
				seenOnPage = true
			} else {
				unseen = append(unseen, posting)
			}
		}
		if seenOnPage || len(page.Postings) == 0 || page.Cursor == "" {
			return unseen, true, true
		}
		if pages >= maxPages {
			return unseen, false, true
		}
		// A page that cannot be fetched leaves what was read as a truncated
		// snapshot: still enough to light the bar, not enough to prune by.
		if page, err = mail.ReadPage(ctx, sdk, source, page.Cursor); err != nil {
			return unseen, false, true
		}
	}
}
