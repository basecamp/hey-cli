package cmd

// The HEY bar panel appears when you sign in with hey. omarchy.go installs hey
// into the desktop; this file installs the 37signals.hey bar plugin itself —
// the one piece that used to be a command the user copied.
//
// One idempotent routine, three entry points, one state file, one lock:
//
//   - Ensure mode (the sign-in hook, the wizard) asks once — "Put HEY in your
//     Omarchy bar?" — records the answer, and never enables a checkout that is
//     off the bar: a plugin the user disabled stays disabled, whatever the
//     marker says. Its only mutation is `omarchy plugin add`, on fresh consent
//     or a pending retry, and it never runs for machine output, HEY_NONINTERACTIVE,
//     a non-TTY, or a --token/--cookie login.
//   - Force mode (`hey setup omarchy`) records its intent before acting, so a
//     crash leaves a pending install the next explicit setup finishes — never
//     a quiet skip — and it never reports success unless the running shell
//     verified the plugin enabled. Explicit --json still installs.
//   - --remove writes its tombstone first — a removal that cannot be recorded
//     does not run — then disables. The checkout stays.
//
// The marker lives at StateDir()/omarchy/bar-plugin.json. accepted_at is
// consent, never re-asked; pending_enable + installing_at an unfinished
// install; declined_at/removed_at the user's no; last_clone_* a 24h retry
// throttle for failed clones only. Installation fails closed on a marker it
// cannot read; only --remove still disables then, since nothing can resurrect
// the plugin through a fail-closed install.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/gofrs/flock"

	"github.com/basecamp/hey-cli/internal/terminal"
	"github.com/basecamp/hey-cli/internal/tui"
	"github.com/basecamp/hey-cli/internal/version"
)

// omarchyBarPluginSource is what `omarchy plugin add` clones. A var only as a
// test seam — tests assign it directly; nothing overrides it at build or run
// time, so nothing but the public URL ever ships or installs.
var omarchyBarPluginSource = "https://github.com/basecamp/omarchy-hey-plugin.git"

// omarchyBarPluginInstallHint is the manual command, surviving only in the
// clone-failure remediation.
func omarchyBarPluginInstallHint() string {
	return "omarchy plugin add " + omarchyBarPluginSource + " --enable"
}

// omarchyCloneRetryInterval is how long a failed clone suppresses the sign-in
// hook's retry. Clone failures only: nothing else is throttled.
const omarchyCloneRetryInterval = 24 * time.Hour

// ensureOmarchyBarPluginAfterLogin puts HEY in the Omarchy bar after an
// interactive OAuth sign-in — the "signing in with hey puts HEY in your bar"
// promise. At most one stderr line, and never a failed login: every outcome
// here is advisory.
var ensureOmarchyBarPluginAfterLogin = func(w io.Writer) {
	if writer == nil || !writer.IsStyled() || !interactiveStdio() {
		return
	}
	env := liveOmarchyEnv()
	if !env.detected() {
		return
	}
	step := omarchySetup{env: env}.installBarPlugin()
	switch {
	case step.Status == "installed":
		fmt.Fprintln(w, "HEY is in your Omarchy bar now.")
	case step.Status == "failed":
		fmt.Fprintln(w, "Omarchy bar plugin not installed: "+step.Detail)
	case step.attempted:
		fmt.Fprintln(w, "Omarchy bar plugin: "+step.Detail)
	}
}

// confirmOmarchyPanel is the one question the sign-in hook may ask, once ever.
// A seam so tests can answer it.
var confirmOmarchyPanel = func() (bool, error) {
	return tui.Confirm("  Put HEY in your Omarchy bar?", true)
}

// installBarPlugin puts the 37signals.hey plugin in the bar, in ensure or
// force mode per s.forcePlugin. Everything runs under the marker lock, and
// nothing here prompts, clones or writes before its mode's gates say so.
func (s omarchySetup) installBarPlugin() omarchyStep {
	if !s.env.detected() || !filepath.IsAbs(s.env.home) {
		return s.pluginSkip("Omarchy not detected")
	}
	return s.underBarPluginLock(func() []omarchyStep {
		step := s.installBarPluginLocked()
		// The plugin replaces the inline hey-unread indicator earlier
		// releases installed, and one rule covers every path here: with
		// the legacy module still present and the replacement verifiably
		// live, a sign-in migrates — whatever else this run decided (a
		// fresh install, a quiet repair, already enabled, or a checkout
		// someone installed by hand). "installed" was just verified;
		// anything else earns the read-only probe first, so a layout entry
		// alone never costs a working panel; a failed run stays
		// fail-closed with no cleanup at all. Quiet and best-effort — a
		// failed cleanup costs nothing the next setup will not fix, and a
		// sign-in with no legacy module pays nothing.
		if step.Status != "failed" && s.legacyIndicatorPresent() {
			live := step.Status == "installed"
			if !live {
				probe, _, outcome := s.probeShellPlugins()
				live = outcome == probeAnswered && probe.present && probe.enabled
			}
			if live {
				_ = s.removeLegacyIndicator(true)
			}
		}
		return []omarchyStep{step}
	})[0]
}

// legacyIndicatorPresent reports whether shell.json still carries the old
// inline hey-unread module — the cheap check that keeps every ordinary
// sign-in from paying for a migration nobody needs.
func (s omarchySetup) legacyIndicatorPresent() bool {
	shell, err := s.loadShellConfig()
	if err != nil {
		return false
	}
	layout, err := existingBarLayout(shell)
	if err != nil {
		return false
	}
	present, _ := legacyIndicator(layout)
	return present
}

// underBarPluginLock runs fn with the plugin lock held — one acquisition
// spanning every marker look and every shell.json write, so a concurrent
// remove cannot interleave with an install's notify rewrite and restore a
// disabled entry. The gates and contention become the one plugin step fn
// never gets to produce.
func (s omarchySetup) underBarPluginLock(fn func() []omarchyStep) []omarchyStep {
	if s.env.stateDir == "" || !filepath.IsAbs(s.env.stateDir) {
		return []omarchyStep{s.pluginSkip("no state directory to record the bar plugin state in — set XDG_STATE_HOME or HOME")}
	}
	lock, err := acquireOmarchyPluginLock(s.env)
	if err != nil {
		return []omarchyStep{s.pluginSkip(omarchyLockDetail(err))}
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}

// installBarPluginLocked is the mode dispatch, the lock already held.
func (s omarchySetup) installBarPluginLocked() omarchyStep {
	marker, _, err := readOmarchyPluginMarker(s.env.markerPath())
	if err != nil {
		return s.pluginFailed(omarchyMarkerUnreadable(s.env, "hey setup omarchy"))
	}
	if s.forcePlugin {
		return s.forceInstall(marker)
	}
	return s.ensureInstall(marker)
}

// omarchyLockDetail keeps "wait for the other hey" for actual contention;
// any other acquisition failure (an unwritable state dir, a filesystem
// error) is its own problem and waiting will not fix it.
func omarchyLockDetail(err error) string {
	if errors.Is(err, errOmarchyLockHeld) {
		return errOmarchyLockHeld.Error()
	}
	return "could not take the bar plugin lock: " + err.Error()
}

// ensureInstall is the sign-in path: quiet wherever installation is
// impossible or unwanted, and never a resurrection — an off-bar checkout is
// not enabled here whatever the marker says, pending included. Only explicit
// `hey setup omarchy` re-enables.
func (s omarchySetup) ensureInstall(marker omarchyPluginMarker) omarchyStep {
	onBar := s.pluginOnBar()
	cloned := s.env.pluginCloned()
	switch {
	case onBar && marker.PendingEnable:
		// A crash between enable and the final write: finish the
		// bookkeeping — unless the shell says the plugin is off, which is
		// the user's doing and stays.
		probe, failStep, outcome := s.probeShellPlugins()
		if outcome != probeAnswered {
			return failStep
		}
		if !probe.present || !probe.enabled {
			return s.pluginNotice("install incomplete — run hey setup omarchy")
		}
		step := s.finalize(marker)
		if step.Status == "installed" {
			step.Status, step.Detail, step.attempted = "unchanged", "", false
		}
		return step
	case onBar:
		// Whoever put it there — even after a decline or a removal, an
		// enabled plugin is just there to configure.
		return s.pluginStep("unchanged", "")
	case marker.DeclinedAt != "" || marker.RemovedAt != "":
		return s.pluginStep("skipped", "declined or removed — hey setup omarchy installs it")
	case cloned && marker.PendingEnable:
		// Never enabled automatically — but an enable that succeeded
		// without a layout entry and crashed before the final write looks
		// exactly like this, so the read-only probe self-repairs it. A
		// checkout the shell reports disabled stays put.
		probe, failStep, outcome := s.probeShellPlugins()
		if outcome != probeAnswered {
			return failStep
		}
		if probe.present && probe.enabled {
			step := s.finalize(marker)
			if step.Status == "installed" {
				step.Status, step.Detail, step.attempted = "unchanged", "", false
			}
			return step
		}
		return s.pluginNotice("install incomplete — run hey setup omarchy")
	case cloned:
		return s.pluginStep("skipped", "the checkout is disabled or was installed by someone else")
	case marker.PendingEnable:
		// Consent stands; retry the clone without asking again, throttled.
		if marker.cloneThrottled() {
			return s.pluginStep("skipped", "waiting to retry a failed clone")
		}
		probe, failStep, outcome := s.probeShellPlugins()
		if outcome != probeAnswered {
			return failStep
		}
		if probe.present && probe.enabled {
			return s.finalize(marker) // a lost race: someone enabled it
		}
		if probe.present {
			return s.pluginNotice("install incomplete — run hey setup omarchy")
		}
		return s.addAndVerify(marker)
	case marker.InstalledAt != "":
		return s.pluginStep("skipped", "removed outside hey")
	default:
		return s.ensureFreshInstall(marker)
	}
}

// ensureFreshInstall is the first sign-in on a box with nothing recorded:
// probe the shell, ask once, put the intent on disk, then clone.
func (s omarchySetup) ensureFreshInstall(marker omarchyPluginMarker) omarchyStep {
	probe, failStep, outcome := s.probeShellPlugins()
	if outcome != probeAnswered {
		return failStep
	}
	if probe.present {
		// The shell knows the plugin but shell.json's layout does not spell
		// it out: someone else's install. Enabled means the bar already has
		// it; disabled means it is theirs to re-enable.
		if probe.enabled {
			return s.pluginStep("unchanged", "")
		}
		return s.pluginStep("skipped", "the checkout is disabled or was installed by someone else")
	}
	yes, err := confirmOmarchyPanel()
	if err != nil {
		// No answer (esc, no terminal): ask again another day.
		return s.pluginStep("skipped", "not confirmed")
	}
	if !yes {
		marker.DeclinedAt = omarchyNow()
		if writeErr := s.env.writeMarker(marker); writeErr != nil {
			return s.pluginFailed("could not record the bar plugin state in " + s.env.markerPath() + ": " + writeErr.Error())
		}
		return s.pluginStep("skipped", "declined — hey setup omarchy installs it")
	}
	now := omarchyNow()
	if marker.AcceptedAt == "" {
		marker.AcceptedAt = now
	}
	marker.PendingEnable = true
	marker.InstallingAt = now
	marker.CLIVersion = version.Version
	if writeErr := s.env.writeMarker(marker); writeErr != nil {
		return s.pluginFailed("could not record the bar plugin state in " + s.env.markerPath() + ": " + writeErr.Error())
	}
	return s.addAndVerify(marker)
}

// forceInstall is `hey setup omarchy`: intent on disk before anything runs —
// clearing a decline, a removal and the clone throttle, since the command is
// the explicit ask — so a crash leaves a pending install the next explicit
// setup finishes. Every incomplete outcome carries a failure.
func (s omarchySetup) forceInstall(marker omarchyPluginMarker) omarchyStep {
	now := omarchyNow()
	if marker.AcceptedAt == "" {
		marker.AcceptedAt = now
	}
	marker.PendingEnable = true
	marker.InstallingAt = now
	marker.CLIVersion = version.Version
	marker.DeclinedAt, marker.RemovedAt = "", ""
	marker.LastCloneError, marker.LastCloneAt = "", ""
	if err := s.env.writeMarker(marker); err != nil {
		return s.pluginFailed("could not record the bar plugin state in " + s.env.markerPath() + ": " + err.Error())
	}
	probe, failStep, outcome := s.probeShellPlugins()
	if outcome != probeAnswered {
		return failStep
	}
	switch {
	case probe.present && probe.enabled:
		step := s.finalize(marker)
		if step.Status == "installed" {
			step.Status, step.Detail = "unchanged", "already enabled"
		}
		return step
	case probe.present || s.env.pluginCloned():
		// The reversal of `omarchy plugin disable`; the verify below is the
		// guarantee either way.
		out, err := s.env.run("omarchy", "plugin", "enable", omarchyBarPluginID)
		if err != nil && !strings.Contains(strings.ToLower(out), "already") {
			return s.pluginFailed(firstOutputLine(out, err))
		}
		return s.verifyAndFinalize(marker)
	default:
		return s.addAndVerify(marker)
	}
}

// addAndVerify clones the plugin — the one mutation ensure mode is allowed —
// and believes only the shell about the result.
func (s omarchySetup) addAndVerify(marker omarchyPluginMarker) omarchyStep {
	out, err := s.env.run("omarchy", "plugin", "add", omarchyBarPluginSource, "--enable", "--yes")
	lower := strings.ToLower(out)
	cloneRefused := strings.Contains(lower, "failed to clone")
	failed := cloneRefused || (err != nil && !strings.Contains(lower, "already installed") && !strings.Contains(lower, "already used"))
	if failed {
		// Every failed add arms the retry throttle — a hang killed at the
		// timeout says no "failed to clone" but retrying it on every
		// sign-in would block each one for another minute.
		marker.LastCloneError = firstOutputLine(out, err)
		marker.LastCloneAt = omarchyNow()
		_ = s.env.writeMarker(marker) // best effort: the throttle is a convenience
		if cloneRefused {
			return s.pluginFailed("could not clone " + omarchyBarPluginSource + " — check the network, or run: " + omarchyBarPluginInstallHint())
		}
		return s.pluginFailed(firstOutputLine(out, err))
	}
	// Added — or a lost race already installed it; the verify decides.
	return s.verifyAndFinalize(marker)
}

// verifyAndFinalize reports installed only when the running shell lists the
// plugin enabled and the marker recorded it. Anything less keeps the pending
// intent and fails.
func (s omarchySetup) verifyAndFinalize(marker omarchyPluginMarker) omarchyStep {
	probe, _, outcome := s.probeShellPlugins()
	if outcome != probeAnswered || !probe.present || !probe.enabled {
		return s.pluginFailed("the shell did not enable the plugin — run hey setup omarchy")
	}
	return s.finalize(marker)
}

// finalize records a verified enable. A failed write here is a failure
// everywhere — never installed with a hidden error; the next run's
// crash-repair row finishes the marker.
func (s omarchySetup) finalize(marker omarchyPluginMarker) omarchyStep {
	marker.PendingEnable = false
	marker.InstallingAt = ""
	marker.InstalledAt = omarchyNow()
	marker.CLIVersion = version.Version
	marker.LastCloneError, marker.LastCloneAt = "", ""
	if err := s.env.writeMarker(marker); err != nil {
		return s.pluginFailed("enabled, but could not record the bar plugin state in " + s.env.markerPath() + ": " + err.Error())
	}
	step := s.pluginStep("installed", "installed and enabled")
	step.attempted = true
	return step
}

// removeBarPluginLocked is --remove's half, the lock already held: tombstone
// first, then disable. The checkout stays; deleting it is `omarchy plugin
// remove`, which hey never runs.
func (s omarchySetup) removeBarPluginLocked(onBar bool) omarchyStep {
	marker, _, err := readOmarchyPluginMarker(s.env.markerPath())
	if err != nil {
		// A marker we cannot read must not trap removal: installation is
		// already fail-closed on it, so nothing can resurrect the plugin.
		// Disable anyway, leave the marker as evidence, and exit nonzero
		// with a removal-shaped repair hint — pointing at a plain setup
		// here would re-enable what was just removed.
		step := s.disableBarPlugin(onBar)
		detail := omarchyMarkerUnreadable(s.env, "hey setup omarchy --remove")
		if step.Status == "failed" {
			detail = step.Detail + "; " + detail
		}
		return s.pluginFailed(detail)
	}
	marker.RemovedAt = omarchyNow()
	marker.PendingEnable = false
	marker.InstallingAt = ""
	if writeErr := s.env.writeMarker(marker); writeErr != nil {
		return s.pluginFailed("could not record the removal in " + s.env.markerPath() + ": " + writeErr.Error())
	}
	return s.disableBarPlugin(onBar)
}

func (s omarchySetup) disableBarPlugin(onBar bool) omarchyStep {
	if !onBar {
		// shell.json's layout is not the whole truth: an enabled plugin
		// needs no spelled-out entry, and an unreadable shell.json says
		// nothing — ask the shell before calling the plugin absent.
		probe, failStep, outcome := s.probeShellPlugins()
		switch {
		case outcome == probeNoCLI:
			// No omarchy CLI: nothing can be running, nothing to disable.
			return s.pluginStep("absent", "")
		case outcome != probeAnswered:
			return s.pluginFailed(failStep.Detail)
		case !probe.present || !probe.enabled:
			return s.pluginStep("absent", "")
		}
	}
	out, err := s.env.run("omarchy", "plugin", "disable", omarchyBarPluginID)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			// Omarchy itself is gone: a stale layout entry is not a running
			// plugin, and removal keeps working after uninstall.
			return s.pluginStep("absent", "")
		}
		return s.pluginFailed(firstOutputLine(out, err))
	}
	step := s.pluginStep("removed", "disabled; the checkout stays — omarchy plugin remove "+omarchyBarPluginID+" deletes it")
	step.attempted = true
	return step
}

// --- The shell probe ---

type omarchyPluginProbe struct {
	present bool
	enabled bool
}

// omarchyShellDownPattern matches the omarchy CLI telling us there is no
// shell to talk to — an ssh session, a fresh tty, a stopped shell.
var omarchyShellDownPattern = regexp.MustCompile(`omarchy-shell is not running|not ready|OMARCHY_PATH is not set`)

// omarchyProbeOutcome distinguishes the ways a probe can not answer, because
// they demand different reactions: no CLI means nothing can be running at
// all, a down shell means try again from the desktop, and anything else
// fails closed.
type omarchyProbeOutcome int

const (
	probeAnswered omarchyProbeOutcome = iota
	probeNoCLI
	probeShellDown
	probeUnexpected
)

// probeShellPlugins asks the running shell for its plugin list. Anything but
// probeAnswered comes with the step to report.
func (s omarchySetup) probeShellPlugins() (omarchyPluginProbe, omarchyStep, omarchyProbeOutcome) {
	out, err := s.env.run("omarchy", "plugin", "list", "--json")
	if err != nil && errors.Is(err, exec.ErrNotFound) {
		return omarchyPluginProbe{}, s.pluginSkip("Omarchy CLI not found — update Omarchy"), probeNoCLI
	}
	if omarchyShellDownPattern.MatchString(out) {
		return omarchyPluginProbe{}, s.pluginSkip("the Omarchy shell is not running — sign in again from the desktop, or run hey setup omarchy there"), probeShellDown
	}
	trimmed := strings.TrimSpace(out)
	var plugins []struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	// Fail closed on anything but a clean array cleanly answered: a failing
	// command whose output happens to parse, or a JSON null (which
	// unmarshals into a nil slice without error), is not a plugin list.
	if err != nil || !strings.HasPrefix(trimmed, "[") || json.Unmarshal([]byte(trimmed), &plugins) != nil {
		return omarchyPluginProbe{}, s.pluginFailed("unexpected answer from omarchy plugin list"), probeUnexpected
	}
	var probe omarchyPluginProbe
	for _, plugin := range plugins {
		if plugin.ID == omarchyBarPluginID {
			probe.present, probe.enabled = true, plugin.Enabled
		}
	}
	return probe, omarchyStep{}, probeAnswered
}

// pluginOnBar reads shell.json's layout for the plugin's entry — the fact the
// shell itself acts on. Unreadable means unknown, and the probe refines it.
func (s omarchySetup) pluginOnBar() bool {
	shell, err := s.loadShellConfig()
	if err != nil {
		return false
	}
	layout, err := existingBarLayout(shell)
	if err != nil {
		return false
	}
	return barLayoutModule(layout, omarchyBarPluginID) != nil
}

// --- Step shapes ---

// pluginStep is a quiet outcome: no line at sign-in, no issue in the wizard.
func (s omarchySetup) pluginStep(status, detail string) omarchyStep {
	return omarchyStep{Name: "bar plugin", Path: s.env.markerPath(), Status: status, Detail: detail}
}

// pluginNotice is the one skip that owes the user a line: consent stands but
// the install is unfinished, and only an explicit setup finishes it.
func (s omarchySetup) pluginNotice(detail string) omarchyStep {
	step := s.pluginStep("skipped", detail)
	step.attempted = true
	if s.forcePlugin {
		step.failure = errors.New(detail)
	}
	return step
}

// pluginSkip cannot proceed here: quiet at sign-in, a failure under
// `hey setup omarchy`, where an incomplete forced install is never a quiet
// skip.
func (s omarchySetup) pluginSkip(detail string) omarchyStep {
	step := s.pluginStep("skipped", detail)
	if s.forcePlugin {
		step.failure = errors.New(detail)
		step.attempted = true
	}
	return step
}

func (s omarchySetup) pluginFailed(detail string) omarchyStep {
	step := s.pluginStep("failed", detail)
	step.failure = errors.New(detail)
	step.attempted = true
	return step
}

func stepNamed(steps []omarchyStep, name string) omarchyStep {
	for _, step := range steps {
		if step.Name == name {
			return step
		}
	}
	return omarchyStep{}
}

// --- The marker and its lock ---

// omarchyPluginMarker is the bar plugin's state file. Timestamps are RFC3339;
// only what is set is written.
type omarchyPluginMarker struct {
	AcceptedAt     string `json:"accepted_at,omitempty"`
	PendingEnable  bool   `json:"pending_enable,omitempty"`
	InstallingAt   string `json:"installing_at,omitempty"`
	InstalledAt    string `json:"installed_at,omitempty"`
	CLIVersion     string `json:"cli_version,omitempty"`
	DeclinedAt     string `json:"declined_at,omitempty"`
	RemovedAt      string `json:"removed_at,omitempty"`
	LastCloneError string `json:"last_clone_error,omitempty"`
	LastCloneAt    string `json:"last_clone_at,omitempty"`
}

func (m omarchyPluginMarker) cloneThrottled() bool {
	if m.LastCloneAt == "" {
		return false
	}
	at, err := time.Parse(time.RFC3339, m.LastCloneAt)
	return err == nil && time.Since(at) < omarchyCloneRetryInterval
}

func readOmarchyPluginMarker(path string) (omarchyPluginMarker, bool, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed path under hey's own state dir
	if errors.Is(err, os.ErrNotExist) {
		return omarchyPluginMarker{}, false, nil
	}
	if err != nil {
		return omarchyPluginMarker{}, false, err
	}
	var marker omarchyPluginMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return omarchyPluginMarker{}, true, err
	}
	return marker, true, nil
}

func (e omarchyEnv) writeMarker(marker omarchyPluginMarker) error {
	if err := os.MkdirAll(filepath.Dir(e.markerPath()), 0o700); err != nil {
		return err
	}
	return writeJSONFile(e.markerPath(), marker)
}

// omarchyMarkerUnreadable is the fail-closed remediation; command matches
// the operation in flight, so following it never undoes what was asked.
func omarchyMarkerUnreadable(env omarchyEnv, command string) string {
	return "bar plugin state unreadable at " + env.markerPath() + " — delete it, then run " + command
}

// errOmarchyLockHeld is genuine contention — another hey holds the lock —
// as opposed to a failure to reach the lock at all.
var errOmarchyLockHeld = errors.New("another hey is setting up the bar")

// acquireOmarchyPluginLock serializes every look at the marker and every
// mutation behind it. Non-blocking: a held lock is another hey's turn.
func acquireOmarchyPluginLock(env omarchyEnv) (*flock.Flock, error) {
	if err := os.MkdirAll(filepath.Dir(env.lockPath()), 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(env.lockPath())
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, errOmarchyLockHeld
	}
	return lock, nil
}

func (e omarchyEnv) pluginDir() string {
	return filepath.Join(e.configDir(), "plugins", omarchyBarPluginID)
}

// pluginCloned is the checkout fact, read without a subprocess: `omarchy
// plugin add` clones under ~/.config/omarchy/plugins/<id> with a manifest.
func (e omarchyEnv) pluginCloned() bool {
	_, err := os.Stat(filepath.Join(e.pluginDir(), "manifest.json"))
	return err == nil
}

func (e omarchyEnv) markerPath() string {
	return filepath.Join(e.stateDir, "omarchy", "bar-plugin.json")
}

func (e omarchyEnv) lockPath() string {
	return filepath.Join(e.stateDir, "omarchy", "bar-plugin.lock")
}

// omarchyRoot is the install root exported to a child as OMARCHY_PATH when
// our own environment lacks it — the same walk defaultShellPath does.
func (e omarchyEnv) omarchyRoot() string {
	roots := []string{e.omarchyPath, filepath.Join(e.home, ".local", "share", "omarchy"), "/usr/share/omarchy"}
	for _, root := range roots {
		if root == "" {
			continue
		}
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
	}
	return ""
}

func omarchyNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func firstOutputLine(out string, err error) string {
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return terminal.SanitizeLine(line)
		}
	}
	if err != nil {
		return err.Error()
	}
	return "no output"
}

// --- The subprocess runner ---

// omarchyOutputLimit caps what a subprocess can hand back; the rest is
// drained so the child never blocks on a full pipe.
const omarchyOutputLimit = 64 << 10

// omarchyCommandTimeout bounds every omarchy subprocess. omarchy-theme-refresh
// re-renders every template and retints every app, and a plugin add clones
// over the network; a minute is generous. A var so tests can shorten it.
var omarchyCommandTimeout = time.Minute

// runOmarchyCommand runs an omarchy command and answers its combined output.
// A package var so command-level tests can intercept every subprocess.
var runOmarchyCommand = func(omarchyRoot, name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), omarchyCommandTimeout)
	defer cancel()
	// Ctrl-C must reach the isolated process group: the child sits outside
	// the terminal's foreground group, so translate an interrupt into the
	// same cancellation the timeout uses — Cancel kills the whole group and
	// hey survives to report the aborted step.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed omarchy command names
	// The omarchy CLI needs OMARCHY_PATH to find the shell; non-login and
	// agent environments lack it, so export the root we detected ourselves.
	if omarchyRoot != "" && os.Getenv("OMARCHY_PATH") == "" {
		cmd.Env = append(os.Environ(), "OMARCHY_PATH="+omarchyRoot)
	}
	output := &cappedOutput{limit: omarchyOutputLimit}
	cmd.Stdout, cmd.Stderr = output, output
	// A plugin add spawns git; on timeout the whole process group dies, and
	// WaitDelay unblocks Wait if a grandchild still holds the pipes.
	setOmarchyProcessGroup(cmd)
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	return output.String(), err
}

// cappedOutput keeps the first limit bytes and swallows the rest, so a chatty
// child runs to EOF instead of blocking on a full pipe.
type cappedOutput struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedOutput) Write(p []byte) (int, error) {
	if remaining := c.limit - c.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			c.buf.Write(p[:remaining])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedOutput) String() string { return c.buf.String() }
