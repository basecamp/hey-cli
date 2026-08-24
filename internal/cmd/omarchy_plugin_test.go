package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
)

const (
	pluginListAbsent   = `[]`
	pluginListEnabled  = `[{"id":"omarchy.clock","enabled":true},{"id":"37signals.hey","enabled":true}]`
	pluginListDisabled = `[{"id":"omarchy.clock","enabled":true},{"id":"37signals.hey","enabled":false}]`
)

func stubConfirmOmarchyPanel(t *testing.T, answer bool, err error) *int {
	t.Helper()
	calls := 0
	orig := confirmOmarchyPanel
	confirmOmarchyPanel = func() (bool, error) {
		calls++
		return answer, err
	}
	t.Cleanup(func() { confirmOmarchyPanel = orig })
	return &calls
}

// stubOmarchyRun intercepts every live omarchy subprocess (liveOmarchyEnv's
// runner), recording each command and answering with fn.
func stubOmarchyRun(t *testing.T, fn func(name string, args ...string) (string, error)) *[]string {
	t.Helper()
	var ran []string
	orig := runOmarchyCommand
	runOmarchyCommand = func(_, name string, args ...string) (string, error) {
		ran = append(ran, strings.Join(append([]string{name}, args...), " "))
		return fn(name, args...)
	}
	t.Cleanup(func() { runOmarchyCommand = orig })
	return &ran
}

func omarchyUnavailable(string, ...string) (string, error) { return "", exec.ErrNotFound }

func stubOmarchyAfterLogin(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := ensureOmarchyBarPluginAfterLogin
	ensureOmarchyBarPluginAfterLogin = func(io.Writer) { calls++ }
	t.Cleanup(func() { ensureOmarchyBarPluginAfterLogin = orig })
	return &calls
}

func stubLoginInteractively(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	orig := loginInteractively
	loginInteractively = func(io.Writer) error {
		calls++
		return err
	}
	t.Cleanup(func() { loginInteractively = orig })
	return &calls
}

func clonePluginCheckout(t *testing.T, env omarchyEnv) {
	t.Helper()
	if err := os.MkdirAll(env.pluginDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.pluginDir(), "manifest.json"), []byte(`{"id":"37signals.hey","version":"0.4.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedMarker(t *testing.T, env omarchyEnv, marker omarchyPluginMarker) {
	t.Helper()
	if err := env.writeMarker(marker); err != nil {
		t.Fatal(err)
	}
}

func readMarkerFile(t *testing.T, env omarchyEnv) (omarchyPluginMarker, bool) {
	t.Helper()
	marker, exists, err := readOmarchyPluginMarker(env.markerPath())
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	return marker, exists
}

func markerBytes(t *testing.T, env omarchyEnv) string {
	t.Helper()
	data, err := os.ReadFile(env.markerPath())
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// mutationCommands filters the recorder down to commands that change
// anything: the plugin list probe and a theme refresh are reads or
// unrelated.
func mutationCommands(ran []string) []string {
	var out []string
	for _, command := range ran {
		if !strings.HasPrefix(command, "omarchy plugin list") && command != "omarchy-theme-refresh" {
			out = append(out, command)
		}
	}
	return out
}

// --- Ensure mode: the transition table, row by row ---

func TestOmarchyPluginFreshInstallAsksOnceThenAddsAndVerifies(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	confirms := stubConfirmOmarchyPanel(t, true, nil)
	var ran []string
	enabled := false
	env.run = func(name string, args ...string) (string, error) {
		command := strings.Join(append([]string{name}, args...), " ")
		ran = append(ran, command)
		switch {
		case strings.HasPrefix(command, "omarchy plugin list"):
			if enabled {
				return pluginListEnabled, nil
			}
			return pluginListAbsent, nil
		case strings.HasPrefix(command, "omarchy plugin add"):
			// The intent must be durable before anything is cloned.
			marker, exists := readMarkerFile(t, env)
			if !exists || !marker.PendingEnable || marker.AcceptedAt == "" {
				t.Errorf("intent not on disk before the clone: %+v (exists=%v)", marker, exists)
			}
			enabled = true
			clonePluginCheckout(t, env)
			writeShell(t, env, pluginShellJSON)
			return "Installed 37signals.hey", nil
		default:
			return "", nil
		}
	}

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "installed" || !step.attempted {
		t.Fatalf("step = %q %q attempted=%v", step.Status, step.Detail, step.attempted)
	}
	if *confirms != 1 {
		t.Errorf("consent asked %d times, want 1", *confirms)
	}
	if want := "omarchy plugin add " + omarchyBarPluginSource + " --enable --yes"; !contains(ran, want) {
		t.Errorf("commands = %v, want %q", ran, want)
	}
	marker, _ := readMarkerFile(t, env)
	if marker.PendingEnable || marker.InstalledAt == "" || marker.AcceptedAt == "" || marker.InstallingAt != "" {
		t.Errorf("marker after install = %+v", marker)
	}

	// The next sign-in finds it on the bar and does nothing at all.
	countBefore := len(ran)
	second := omarchySetup{env: env}.installBarPlugin()
	if second.Status != "unchanged" || second.attempted || len(ran) != countBefore {
		t.Errorf("second ensure = %q, ran %v", second.Status, ran[countBefore:])
	}
}

func TestOmarchyPluginSourceSeam(t *testing.T) {
	orig := omarchyBarPluginSource
	omarchyBarPluginSource = "https://example.org/omarchy-hey-plugin-fork.git"
	t.Cleanup(func() { omarchyBarPluginSource = orig })
	env, ran, replies := testOmarchyEnvScripted(t, nil)
	stubConfirmOmarchyPanel(t, true, nil)
	replies["omarchy plugin add"] = omarchyReply{out: "failed to clone"}

	step := omarchySetup{env: env}.installBarPlugin()
	if !contains(*ran, "omarchy plugin add https://example.org/omarchy-hey-plugin-fork.git --enable --yes") {
		t.Errorf("commands = %v", *ran)
	}
	if !strings.Contains(step.Detail, "https://example.org/omarchy-hey-plugin-fork.git") {
		t.Errorf("the remediation must name the source actually used: %q", step.Detail)
	}
}

func TestOmarchyPluginEnsureAsksOnceAndRemembersNo(t *testing.T) {
	env, ran, _ := testOmarchyEnvScripted(t, nil)
	confirms := stubConfirmOmarchyPanel(t, false, nil)

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "skipped" || step.attempted {
		t.Fatalf("declined = %q attempted=%v", step.Status, step.attempted)
	}
	marker, _ := readMarkerFile(t, env)
	if marker.DeclinedAt == "" {
		t.Fatalf("the decline must be recorded: %+v", marker)
	}
	countAfterFirst := len(*ran)

	if second := (omarchySetup{env: env}).installBarPlugin(); second.Status != "skipped" || second.attempted {
		t.Errorf("second = %q attempted=%v", second.Status, second.attempted)
	}
	if *confirms != 1 {
		t.Errorf("asked %d times, want 1", *confirms)
	}
	if len(*ran) != countAfterFirst {
		t.Errorf("a remembered no runs nothing, ran %v", (*ran)[countAfterFirst:])
	}
}

func TestOmarchyPluginEnsureCanceledConsentAsksAgainNextTime(t *testing.T) {
	// Esc is not a no: nothing is recorded, so the next sign-in asks again.
	env, _, _ := testOmarchyEnvScripted(t, nil)
	confirms := stubConfirmOmarchyPanel(t, false, errors.New("canceled"))

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "skipped" || step.attempted {
		t.Fatalf("canceled = %q attempted=%v", step.Status, step.attempted)
	}
	if _, exists := readMarkerFile(t, env); exists {
		t.Error("a canceled prompt must record nothing")
	}
	if second := (omarchySetup{env: env}).installBarPlugin(); second.Status != "skipped" {
		t.Errorf("second = %q", second.Status)
	}
	if *confirms != 2 {
		t.Errorf("asked %d times, want every time until answered", *confirms)
	}
}

func TestOmarchyPluginEnsureNeverResurrectsADisabledPlugin(t *testing.T) {
	// A verified install whose final write failed leaves pending_enable set;
	// the user then disables the plugin. The next sign-in owes a notice and
	// nothing else — only explicit hey setup omarchy re-enables.
	env, ran, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: pluginListDisabled},
	})
	clonePluginCheckout(t, env)
	seedMarker(t, env, omarchyPluginMarker{AcceptedAt: "2026-08-24T00:00:00Z", PendingEnable: true, InstallingAt: "2026-08-24T00:00:00Z"})
	confirms := stubConfirmOmarchyPanel(t, true, nil)

	// Checkout present, off the bar (disable removed the layout entry).
	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "skipped" || !step.attempted || !strings.Contains(step.Detail, "hey setup omarchy") {
		t.Fatalf("step = %q %q attempted=%v", step.Status, step.Detail, step.attempted)
	}
	if got := mutationCommands(*ran); len(got) != 0 {
		t.Errorf("ensure must not enable an off-bar checkout: %v", got)
	}
	if *confirms != 0 {
		t.Error("nothing to ask: consent already stands")
	}

	// The same with the layout entry still present but the shell reporting
	// the plugin disabled: the probe's answer wins and nothing is enabled.
	writeShell(t, env, pluginShellJSON)
	step = omarchySetup{env: env}.installBarPlugin()
	if step.Status != "skipped" || !step.attempted {
		t.Fatalf("disabled per the shell = %q %q", step.Status, step.Detail)
	}
	if got := mutationCommands(*ran); len(got) != 0 {
		t.Errorf("no mutation may run: %v", got)
	}
	marker, _ := readMarkerFile(t, env)
	if !marker.PendingEnable {
		t.Error("the pending intent stays for hey setup omarchy to finish")
	}
}

func TestOmarchyPluginEnsurePendingClonedFinalizesWhenTheShellSaysEnabled(t *testing.T) {
	// An enable that succeeded without a layout entry, crashed before the
	// final write: the checkout and the pending marker both exist, and the
	// read-only probe self-repairs the bookkeeping without enabling a thing.
	env, ran, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: pluginListEnabled},
	})
	clonePluginCheckout(t, env)
	seedMarker(t, env, omarchyPluginMarker{AcceptedAt: "2026-08-24T00:00:00Z", PendingEnable: true})

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "unchanged" || step.attempted {
		t.Fatalf("self-repair should be quiet, got %q %q attempted=%v", step.Status, step.Detail, step.attempted)
	}
	marker, _ := readMarkerFile(t, env)
	if marker.PendingEnable || marker.InstalledAt == "" {
		t.Errorf("marker = %+v", marker)
	}
	if got := mutationCommands(*ran); len(got) != 0 {
		t.Errorf("the repair is read-only: %v", got)
	}
}

func TestOmarchyPluginCrashRepairMigratesTheLegacyIndicator(t *testing.T) {
	// A first sign-in that crashed after the enable: the quiet self-repair
	// still owes the migration, or a legacy user keeps two icons forever.
	env, ran, replies := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: pluginListEnabled},
	})
	clonePluginCheckout(t, env)
	writeShell(t, env, legacyShellJSON)
	seedMarker(t, env, omarchyPluginMarker{AcceptedAt: "2026-08-24T00:00:00Z", PendingEnable: true})
	replies["omarchy bar set"] = omarchyReply{}

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "unchanged" || step.attempted {
		t.Fatalf("repair should stay quiet, got %q %q attempted=%v", step.Status, step.Detail, step.attempted)
	}
	if strings.Contains(readText(t, env.shellPath()), "hey-unread") {
		t.Error("the crash repair must migrate the legacy indicator too")
	}
	if !contains(*ran, "omarchy bar set "+omarchyBarPluginID+" notify true --json") {
		t.Errorf("the toast choice must ride along: %v", *ran)
	}
}

func TestSetupOmarchyKeepsLegacyWhenTheCarryFails(t *testing.T) {
	// The legacy module is the only copy of the toast choice: the explicit
	// configure flow must not delete it while the carry has not landed.
	env, _, replies := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: pluginListEnabled},
	})
	clonePluginCheckout(t, env)
	writeShell(t, env, legacyShellJSON)
	replies["omarchy bar set"] = omarchyReply{out: "no shell", err: errors.New("exit status 1")}

	steps := omarchySetup{env: env, forcePlugin: true}.configureBarPlugin()
	if bar := stepNamed(steps, "bar plugin"); bar.Status != "failed" {
		t.Fatalf("a failed carry must fail the step, got %q %q", bar.Status, bar.Detail)
	}
	if stepNamed(steps, "bar indicator").Status == "removed" {
		t.Error("the legacy module must survive a failed carry")
	}
	if !strings.Contains(readText(t, env.shellPath()), "hey-unread") {
		t.Error("the toasting module is the only copy of the choice — it stays")
	}
}

func TestOmarchyPluginEnsureFinishesTheMarkerAfterACrash(t *testing.T) {
	env, ran, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: pluginListEnabled},
	})
	clonePluginCheckout(t, env)
	writeShell(t, env, pluginShellJSON)
	seedMarker(t, env, omarchyPluginMarker{AcceptedAt: "2026-08-24T00:00:00Z", PendingEnable: true})

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "unchanged" || step.attempted {
		t.Fatalf("crash repair should be quiet, got %q attempted=%v", step.Status, step.attempted)
	}
	marker, _ := readMarkerFile(t, env)
	if marker.PendingEnable || marker.InstalledAt == "" {
		t.Errorf("marker = %+v", marker)
	}
	if got := mutationCommands(*ran); len(got) != 0 {
		t.Errorf("verify and write only: %v", got)
	}
}

func TestOmarchyPluginEnsureQuietRows(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, env omarchyEnv)
	}{
		{"declined", func(t *testing.T, env omarchyEnv) {
			seedMarker(t, env, omarchyPluginMarker{DeclinedAt: "2026-08-01T00:00:00Z"})
		}},
		{"removed", func(t *testing.T, env omarchyEnv) {
			seedMarker(t, env, omarchyPluginMarker{RemovedAt: "2026-08-01T00:00:00Z"})
		}},
		{"installed then deleted outside hey", func(t *testing.T, env omarchyEnv) {
			seedMarker(t, env, omarchyPluginMarker{AcceptedAt: "2026-08-01T00:00:00Z", InstalledAt: "2026-08-01T00:00:00Z"})
		}},
		{"someone else's disabled clone", func(t *testing.T, env omarchyEnv) {
			clonePluginCheckout(t, env)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, ran, _ := testOmarchyEnvScripted(t, nil)
			confirms := stubConfirmOmarchyPanel(t, true, nil)
			tc.setup(t, env)
			before := markerBytes(t, env)

			step := omarchySetup{env: env}.installBarPlugin()
			if step.Status != "skipped" || step.attempted {
				t.Errorf("step = %q %q attempted=%v", step.Status, step.Detail, step.attempted)
			}
			if *confirms != 0 {
				t.Error("no prompt on a quiet row")
			}
			if len(*ran) != 0 {
				t.Errorf("no subprocess on a quiet row: %v", *ran)
			}
			if after := markerBytes(t, env); after != before {
				t.Errorf("no write on a quiet row:\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}

func TestOmarchyPluginEnsureOnBarIsUnchanged(t *testing.T) {
	env, ran, _ := testOmarchyEnvScripted(t, nil)
	writeShell(t, env, pluginShellJSON)

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "unchanged" || step.attempted {
		t.Fatalf("step = %q", step.Status)
	}
	if len(*ran) != 0 {
		t.Errorf("an on-bar plugin needs no subprocess: %v", *ran)
	}
	if _, exists := readMarkerFile(t, env); exists {
		t.Error("an on-bar plugin needs no write")
	}
}

func TestOmarchyPluginEnsureRetryIsThrottledAfterACloneFailure(t *testing.T) {
	env, ran, replies := testOmarchyEnvScripted(t, nil)
	confirms := stubConfirmOmarchyPanel(t, true, nil)
	replies["omarchy plugin add"] = omarchyReply{out: "failed to clone " + omarchyBarPluginSource, err: errors.New("exit status 1")}

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "failed" || !strings.Contains(step.Detail, "could not clone") || !strings.Contains(step.Detail, omarchyBarPluginInstallHint()) {
		t.Fatalf("clone failure = %q %q", step.Status, step.Detail)
	}
	marker, _ := readMarkerFile(t, env)
	if !marker.PendingEnable || marker.LastCloneAt == "" {
		t.Fatalf("marker = %+v", marker)
	}

	// Within the throttle: quiet, no prompt, no clone.
	countBefore := len(*ran)
	step = omarchySetup{env: env}.installBarPlugin()
	if step.Status != "skipped" || step.attempted || len(*ran) != countBefore {
		t.Errorf("throttled retry = %q attempted=%v, ran %v", step.Status, step.attempted, (*ran)[countBefore:])
	}

	// Past it: the clone retries, and consent is not asked again.
	marker.LastCloneAt = time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	seedMarker(t, env, marker)
	addRan := false
	base := env.run
	env.run = func(name string, args ...string) (string, error) {
		if strings.HasPrefix(strings.Join(append([]string{name}, args...), " "), "omarchy plugin add") {
			addRan = true
			replies["omarchy plugin list"] = omarchyReply{out: pluginListEnabled}
			return "Installed", nil
		}
		return base(name, args...)
	}
	step = omarchySetup{env: env}.installBarPlugin()
	if step.Status != "installed" || !addRan {
		t.Fatalf("retry past the throttle = %q, addRan=%v", step.Status, addRan)
	}
	if *confirms != 1 {
		t.Errorf("consent asked %d times, want once ever", *confirms)
	}
}

func TestOmarchyPluginEnsureShellDownIsQuiet(t *testing.T) {
	env, ran, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: "omarchy-shell is not running (start it from the desktop)", err: errors.New("exit status 1")},
	})
	confirms := stubConfirmOmarchyPanel(t, true, nil)

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "skipped" || step.attempted || !strings.Contains(step.Detail, "shell is not running") {
		t.Fatalf("step = %q %q attempted=%v", step.Status, step.Detail, step.attempted)
	}
	if *confirms != 0 {
		t.Error("no prompt with no shell")
	}
	if got := mutationCommands(*ran); len(got) != 0 {
		t.Errorf("no clone with no shell: %v", got)
	}
	if _, exists := readMarkerFile(t, env); exists {
		t.Error("nothing may be recorded before consent")
	}

	// A pending install stays pending, untouched and unthrottled.
	seedMarker(t, env, omarchyPluginMarker{AcceptedAt: "2026-08-24T00:00:00Z", PendingEnable: true})
	before := markerBytes(t, env)
	step = omarchySetup{env: env}.installBarPlugin()
	if step.Status != "skipped" || step.attempted {
		t.Errorf("pending with the shell down = %q attempted=%v", step.Status, step.attempted)
	}
	if markerBytes(t, env) != before {
		t.Error("the pending intent must be untouched")
	}
}

func TestOmarchyPluginEnsureMissingCLISaysUpdateOmarchy(t *testing.T) {
	env, _, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {err: exec.ErrNotFound},
	})
	stubConfirmOmarchyPanel(t, true, nil)

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "skipped" || !strings.Contains(step.Detail, "update Omarchy") {
		t.Fatalf("a missing omarchy CLI must be distinct: %q %q", step.Status, step.Detail)
	}
}

func TestOmarchyPluginProbeFailsClosedOnAnUnexpectedAnswer(t *testing.T) {
	for _, reply := range []omarchyReply{
		{out: "I am not JSON"},
		{out: "null"}, // unmarshals into a nil slice without error — still not a list
		{out: "[]", err: errors.New("exit status 1")}, // a failing command whose output happens to parse
	} {
		env, _, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
			"omarchy plugin list": reply,
		})
		stubConfirmOmarchyPanel(t, true, nil)

		step := omarchySetup{env: env}.installBarPlugin()
		if step.Status != "failed" || !strings.Contains(step.Detail, "unexpected answer") {
			t.Fatalf("reply %q/%v: step = %q %q", reply.out, reply.err, step.Status, step.Detail)
		}
	}
}

func TestOmarchyPluginVerifyFailureKeepsThePendingIntent(t *testing.T) {
	env, _, replies := testOmarchyEnvScripted(t, nil)
	stubConfirmOmarchyPanel(t, true, nil)
	replies["omarchy plugin add"] = omarchyReply{out: "Installed"}
	// The verify re-probes and still sees nothing enabled.

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "failed" || !strings.Contains(step.Detail, "did not enable") {
		t.Fatalf("step = %q %q", step.Status, step.Detail)
	}
	marker, _ := readMarkerFile(t, env)
	if !marker.PendingEnable {
		t.Errorf("pending must be kept: %+v", marker)
	}
}

func TestOmarchyPluginFinalWriteFailureIsAFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	env, _, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: pluginListEnabled},
	})
	clonePluginCheckout(t, env)
	writeShell(t, env, pluginShellJSON)
	seedMarker(t, env, omarchyPluginMarker{AcceptedAt: "2026-08-24T00:00:00Z", PendingEnable: true})
	stateDir := filepath.Dir(env.markerPath())
	if err := os.WriteFile(env.lockPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "failed" || !strings.Contains(step.Detail, "enabled, but could not record") {
		t.Fatalf("a hidden write failure is never installed: %q %q", step.Status, step.Detail)
	}
}

func TestOmarchyPluginLockContention(t *testing.T) {
	env, ran, _ := testOmarchyEnvScripted(t, nil)
	stubConfirmOmarchyPanel(t, true, nil)
	if err := os.MkdirAll(filepath.Dir(env.lockPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(env.lockPath())
	locked, err := lock.TryLock()
	if err != nil || !locked {
		t.Fatalf("take the lock: %v %v", locked, err)
	}
	defer func() { _ = lock.Unlock() }()

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "skipped" || step.attempted || !strings.Contains(step.Detail, "another hey") {
		t.Fatalf("ensure under contention = %q %q", step.Status, step.Detail)
	}
	force := omarchySetup{env: env, forcePlugin: true}.installBarPlugin()
	if force.failure == nil {
		t.Error("force under contention is a failure")
	}
	// The notify rewrite shares the lock: a held lock means no shell.json
	// write either, so a racing remove cannot be undone by an install.
	on := true
	steps := omarchySetup{env: env, forcePlugin: true, notify: &on}.configureBarPlugin()
	if stepNamed(steps, "bar plugin").failure == nil {
		t.Error("configureBarPlugin under contention is a failure")
	}
	if len(*ran) != 0 {
		t.Errorf("no subprocess under contention: %v", *ran)
	}
	if markerBytes(t, env) != "" {
		t.Error("no state may be claimed under contention")
	}
}

func TestOmarchyPluginRefusesABadStateDir(t *testing.T) {
	for _, stateDir := range []string{"", "relative/state"} {
		env, ran, _ := testOmarchyEnvScripted(t, nil)
		stubConfirmOmarchyPanel(t, true, nil)
		env.stateDir = stateDir

		step := omarchySetup{env: env}.installBarPlugin()
		if step.Status != "skipped" || step.attempted {
			t.Errorf("stateDir %q: ensure = %q", stateDir, step.Status)
		}
		force := omarchySetup{env: env, forcePlugin: true}.installBarPlugin()
		if force.failure == nil {
			t.Errorf("stateDir %q: force must fail", stateDir)
		}
		if len(*ran) != 0 {
			t.Errorf("stateDir %q: nothing may run: %v", stateDir, *ran)
		}
	}
}

func TestOmarchyPluginMalformedMarkerFailsClosed(t *testing.T) {
	env, ran, _ := testOmarchyEnvScripted(t, nil)
	confirms := stubConfirmOmarchyPanel(t, true, nil)
	if err := os.MkdirAll(filepath.Dir(env.markerPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.markerPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, force := range []bool{false, true} {
		step := omarchySetup{env: env, forcePlugin: force}.installBarPlugin()
		if step.Status != "failed" || !strings.Contains(step.Detail, "state unreadable") || !strings.Contains(step.Detail, "hey setup omarchy") {
			t.Errorf("force=%v: %q %q", force, step.Status, step.Detail)
		}
	}
	if *confirms != 0 || len(*ran) != 0 {
		t.Errorf("fail closed means no prompt and no command: %d %v", *confirms, *ran)
	}
	if markerBytes(t, env) != "{not json" {
		t.Error("the marker must be left as evidence")
	}

	// --remove is the exception: it still disables, leaves the marker, and
	// fails with a removal-shaped repair hint — a plain setup would
	// re-enable what was just removed.
	writeShell(t, env, pluginShellJSON)
	step := omarchySetup{env: env, forcePlugin: true}.removeBarPluginLocked(true)
	if step.Status != "failed" || !strings.Contains(step.Detail, "state unreadable") || !strings.Contains(step.Detail, "hey setup omarchy --remove") {
		t.Fatalf("remove with a bad marker = %q %q", step.Status, step.Detail)
	}
	if !contains(*ran, "omarchy plugin disable "+omarchyBarPluginID) {
		t.Errorf("remove must still disable: %v", *ran)
	}
	if markerBytes(t, env) != "{not json" {
		t.Error("remove must leave the malformed marker untouched")
	}
}

func TestOmarchyPluginSignInInstallMigratesTheLegacyIndicator(t *testing.T) {
	// A migrating user signs in: the new plugin lands and the inline
	// hey-unread module leaves with it — never two icons and two pollers —
	// and its toast choice survives even with no layout entry to hold it.
	env, _ := testOmarchyEnv(t)
	stubConfirmOmarchyPanel(t, true, nil)
	writeShell(t, env, legacyShellJSON)
	var ran []string
	enabled := false
	barSetFails := false
	env.run = func(name string, args ...string) (string, error) {
		command := strings.Join(append([]string{name}, args...), " ")
		ran = append(ran, command)
		switch {
		case strings.HasPrefix(command, "omarchy plugin list"):
			if enabled {
				return pluginListEnabled, nil
			}
			return pluginListAbsent, nil
		case strings.HasPrefix(command, "omarchy plugin add"):
			enabled = true
			return "Installed", nil
		case strings.HasPrefix(command, "omarchy bar set") && !barSetFails:
			// The CLI materializes the entry by rewriting shell.json, the
			// legacy module still in place.
			writeShell(t, env, `{"version":1,"bar":{"layout":{"left":[{"id":"omarchy.menu"}],"center":[{"id":"omarchy.clock"}],"right":[
  {"id":"hey-unread","type":"command","exec":"hey omarchy bar-status --notify","interval":180},
  {"id":"37signals.hey","notify":true},{"id":"omarchy.tray"},{"id":"omarchy.power"}]}}}`)
			return "", nil
		case strings.HasPrefix(command, "omarchy bar set"):
			return "no shell", errors.New("exit status 1")
		default:
			return "", nil
		}
	}

	step := omarchySetup{env: env}.installBarPlugin()
	if step.Status != "installed" {
		t.Fatalf("step = %q %q", step.Status, step.Detail)
	}
	if strings.Contains(readText(t, env.shellPath()), "hey-unread") {
		t.Error("a sign-in install must take the legacy indicator out")
	}
	// legacyShellJSON's module was toasting and no entry holds the key: the
	// CLI materializes it — and the removal must not clobber what it wrote.
	if !contains(ran, "omarchy bar set "+omarchyBarPluginID+" notify true --json") {
		t.Errorf("the toast choice must survive the migration: %v", ran)
	}
	if entry := pluginEntry(t, env); entry == nil || entry["notify"] != true {
		t.Errorf("the materialized entry must survive the legacy removal, got %v", entry)
	}

	// When the choice cannot land, the toasting module stays for the next
	// explicit setup rather than the preference dying with it.
	env2, _ := testOmarchyEnv(t)
	writeShell(t, env2, legacyShellJSON)
	enabledTwo := false
	env2.run = func(name string, args ...string) (string, error) {
		command := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.HasPrefix(command, "omarchy plugin list"):
			if enabledTwo {
				return pluginListEnabled, nil
			}
			return pluginListAbsent, nil
		case strings.HasPrefix(command, "omarchy plugin add"):
			enabledTwo = true
			return "Installed", nil
		case strings.HasPrefix(command, "omarchy bar set"):
			return "no shell", errors.New("exit status 1")
		default:
			return "", nil
		}
	}
	if step := (omarchySetup{env: env2}).installBarPlugin(); step.Status != "installed" {
		t.Fatalf("second install = %q %q", step.Status, step.Detail)
	}
	if !strings.Contains(readText(t, env2.shellPath()), "hey-unread") {
		t.Error("an uncarried toast choice must keep the legacy module")
	}
}

func TestSetupWizardSkipsOmarchyOnRejectedCredentials(t *testing.T) {
	// Stored-but-rejected credentials are not a login: the desktop step
	// must not prompt or install while the user is effectively signed out.
	isolateAgents(t)
	stubInteractive(t, true)
	confirms := stubConfirmOmarchyPanel(t, true, nil)
	ran := stubOmarchyRun(t, omarchyUnavailable)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	configHome := t.TempDir()
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "stale-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })
	t.Setenv("OMARCHY_PATH", t.TempDir())

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"--base-url", server.URL, "setup", "--styled"})
	if err := root.Execute(); err != nil {
		t.Fatalf("setup: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Stored sign-in rejected") {
		t.Fatalf("expected the rejected-credentials warning:\n%s", stdout.String())
	}
	if *confirms != 0 || len(*ran) != 0 {
		t.Errorf("the Omarchy step must wait for a working login: confirms=%d ran=%v", *confirms, *ran)
	}
}

// --- Force mode ---

func TestOmarchyPluginForceClearsSuppressionsBeforeActing(t *testing.T) {
	env, _ := testOmarchyEnv(t)
	clonePluginCheckout(t, env)
	seedMarker(t, env, omarchyPluginMarker{DeclinedAt: "2026-08-01T00:00:00Z", RemovedAt: "2026-08-02T00:00:00Z", LastCloneAt: "2026-08-03T00:00:00Z", LastCloneError: "boom"})
	sawIntent := false
	enabled := false
	env.run = func(name string, args ...string) (string, error) {
		command := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.HasPrefix(command, "omarchy plugin list"):
			if enabled {
				return pluginListEnabled, nil
			}
			return pluginListDisabled, nil
		case command == "omarchy plugin enable "+omarchyBarPluginID:
			marker, _ := readMarkerFile(t, env)
			sawIntent = marker.PendingEnable && marker.DeclinedAt == "" && marker.RemovedAt == "" && marker.LastCloneAt == ""
			enabled = true
			return "", nil
		default:
			return "", nil
		}
	}

	step := omarchySetup{env: env, forcePlugin: true}.installBarPlugin()
	if step.Status != "installed" {
		t.Fatalf("step = %q %q", step.Status, step.Detail)
	}
	if !sawIntent {
		t.Error("the intent write, suppressions cleared, must precede the enable")
	}
	marker, _ := readMarkerFile(t, env)
	if marker.PendingEnable || marker.InstalledAt == "" || marker.DeclinedAt != "" || marker.RemovedAt != "" {
		t.Errorf("marker = %+v", marker)
	}
}

func TestOmarchyPluginForceWithThePluginAlreadyEnabled(t *testing.T) {
	env, ran, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: pluginListEnabled},
	})
	clonePluginCheckout(t, env)

	step := omarchySetup{env: env, forcePlugin: true}.installBarPlugin()
	if step.Status != "unchanged" || step.failure != nil {
		t.Fatalf("step = %q %q", step.Status, step.Detail)
	}
	if got := mutationCommands(*ran); len(got) != 0 {
		t.Errorf("no mutation for an enabled plugin: %v", got)
	}
	marker, _ := readMarkerFile(t, env)
	if marker.PendingEnable || marker.InstalledAt == "" {
		t.Errorf("marker = %+v", marker)
	}
}

// --- Removal ---

func TestOmarchyPluginRemoveTombstoneFirst(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	env, ran, replies := testOmarchyEnvScripted(t, nil)
	clonePluginCheckout(t, env)
	seedMarker(t, env, omarchyPluginMarker{AcceptedAt: "2026-08-01T00:00:00Z", InstalledAt: "2026-08-01T00:00:00Z"})
	stateDir := filepath.Dir(env.markerPath())
	if err := os.WriteFile(env.lockPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// An unrecordable removal runs nothing.
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })
	step := omarchySetup{env: env, forcePlugin: true}.removeBarPluginLocked(true)
	if step.Status != "failed" || !strings.Contains(step.Detail, "could not record the removal") {
		t.Fatalf("step = %q %q", step.Status, step.Detail)
	}
	if len(*ran) != 0 {
		t.Errorf("tombstone first: %v", *ran)
	}
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// A failed disable keeps the tombstone and fails.
	replies["omarchy plugin disable"] = omarchyReply{out: "no shell", err: errors.New("exit status 1")}
	step = omarchySetup{env: env, forcePlugin: true}.removeBarPluginLocked(true)
	if step.Status != "failed" {
		t.Fatalf("failed disable = %q %q", step.Status, step.Detail)
	}
	marker, _ := readMarkerFile(t, env)
	if marker.RemovedAt == "" {
		t.Errorf("the tombstone stands: %+v", marker)
	}

	// A clean disable reports removed and keeps the checkout.
	replies["omarchy plugin disable"] = omarchyReply{}
	step = omarchySetup{env: env, forcePlugin: true}.removeBarPluginLocked(true)
	if step.Status != "removed" || !strings.Contains(step.Detail, "checkout stays") {
		t.Fatalf("step = %q %q", step.Status, step.Detail)
	}
	if _, err := os.Stat(filepath.Join(env.pluginDir(), "manifest.json")); err != nil {
		t.Error("the checkout must survive removal")
	}

	// A stale layout entry with Omarchy itself uninstalled: removal keeps
	// working — a missing CLI means nothing is running, on-layout or not.
	replies["omarchy plugin disable"] = omarchyReply{err: exec.ErrNotFound}
	step = omarchySetup{env: env, forcePlugin: true}.removeBarPluginLocked(true)
	if step.Status != "absent" || step.failure != nil {
		t.Errorf("stale entry with no CLI = %q %q", step.Status, step.Detail)
	}
	replies["omarchy plugin disable"] = omarchyReply{}

	// Off the bar and not enabled per the shell: nothing to disable,
	// tombstone still written; the probe is the only command that runs.
	countBefore := len(*ran)
	step = omarchySetup{env: env, forcePlugin: true}.removeBarPluginLocked(false)
	if step.Status != "absent" {
		t.Errorf("off-bar remove = %q %q", step.Status, step.Detail)
	}
	if got := mutationCommands((*ran)[countBefore:]); len(got) != 0 {
		t.Errorf("nothing to disable for an absent plugin: %v", got)
	}
}

func TestOmarchyPluginRemoveDisablesAnEnabledPluginWithoutALayoutEntry(t *testing.T) {
	// An enabled plugin needs no spelled-out shell.json entry, and an
	// unreadable shell.json reads the same way: the shell, not the layout,
	// says whether there is something to disable.
	env, ran, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: pluginListEnabled},
	})
	step := omarchySetup{env: env, forcePlugin: true}.removeBarPluginLocked(false)
	if step.Status != "removed" || !strings.Contains(step.Detail, "checkout stays") {
		t.Fatalf("step = %q %q", step.Status, step.Detail)
	}
	if !contains(*ran, "omarchy plugin disable "+omarchyBarPluginID) {
		t.Errorf("the enabled plugin must be disabled: %v", *ran)
	}
	marker, _ := readMarkerFile(t, env)
	if marker.RemovedAt == "" {
		t.Errorf("tombstone first: %+v", marker)
	}
}

func TestOmarchyPluginRemoveFailsWhenTheShellCannotAnswer(t *testing.T) {
	// With the shell down we can neither see nor disable an enabled plugin:
	// the tombstone stands, and the command fails rather than reporting a
	// removal that did not happen.
	env, ran, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: "omarchy-shell is not running", err: errors.New("exit status 1")},
	})
	step := omarchySetup{env: env, forcePlugin: true}.removeBarPluginLocked(false)
	if step.Status != "failed" || !strings.Contains(step.Detail, "shell is not running") {
		t.Fatalf("step = %q %q", step.Status, step.Detail)
	}
	marker, _ := readMarkerFile(t, env)
	if marker.RemovedAt == "" {
		t.Errorf("the tombstone must land before the probe: %+v", marker)
	}
	if got := mutationCommands(*ran); len(got) != 0 {
		t.Errorf("no mutation with no shell: %v", got)
	}
}

func TestOmarchyPluginLockFailureIsNotContention(t *testing.T) {
	env, _, _ := testOmarchyEnvScripted(t, nil)
	// A file where the lock's directory must go: acquisition fails for a
	// reason waiting cannot fix, and the message must say so.
	if err := os.WriteFile(filepath.Join(env.stateDir, "omarchy"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	step := omarchySetup{env: env, forcePlugin: true}.installBarPlugin()
	if step.failure == nil || !strings.Contains(step.Detail, "could not take the bar plugin lock") || strings.Contains(step.Detail, "another hey") {
		t.Fatalf("step = %q %q", step.Status, step.Detail)
	}
}

// --- The sign-in hook ---

func TestEnsureOmarchyBarPluginAfterLogin(t *testing.T) {
	t.Setenv("OMARCHY_PATH", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stubConfirmOmarchyPanel(t, true, nil)
	enabled := false
	ran := stubOmarchyRun(t, func(name string, args ...string) (string, error) {
		command := strings.Join(append([]string{name}, args...), " ")
		switch {
		case strings.HasPrefix(command, "omarchy plugin list"):
			if enabled {
				return pluginListEnabled, nil
			}
			return pluginListAbsent, nil
		case strings.HasPrefix(command, "omarchy plugin add"):
			enabled = true
			shellDir := filepath.Join(home, ".config", "omarchy")
			if err := os.MkdirAll(shellDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(shellDir, "shell.json"), []byte(pluginShellJSON), 0o644); err != nil {
				t.Fatal(err)
			}
			return "Installed", nil
		default:
			return "", nil
		}
	})
	prev := writer
	t.Cleanup(func() { writer = prev })
	var out bytes.Buffer

	// Machine output never runs the hook's body.
	writer = output.New(output.Options{Format: output.FormatJSON, Stdout: &out, Stderr: &out})
	stubInteractive(t, true)
	ensureOmarchyBarPluginAfterLogin(&out)
	if len(*ran) != 0 || out.Len() != 0 {
		t.Fatalf("machine output must be inert: %v %q", *ran, out.String())
	}

	// Non-interactive stdio never runs it either.
	writer = output.New(output.Options{Format: output.FormatStyled, Stdout: &out, Stderr: &out})
	stubInteractive(t, false)
	ensureOmarchyBarPluginAfterLogin(&out)
	if len(*ran) != 0 || out.Len() != 0 {
		t.Fatalf("non-interactive must be inert: %v %q", *ran, out.String())
	}

	// Styled and interactive: installed and announced, one line.
	stubInteractive(t, true)
	ensureOmarchyBarPluginAfterLogin(&out)
	if got := strings.TrimSpace(out.String()); got != "HEY is in your Omarchy bar now." {
		t.Fatalf("hook output = %q", got)
	}

	// The next sign-in is silent — the work is done.
	out.Reset()
	countBefore := len(*ran)
	ensureOmarchyBarPluginAfterLogin(&out)
	if out.Len() != 0 {
		t.Errorf("second login output = %q", out.String())
	}
	if got := mutationCommands((*ran)[countBefore:]); len(got) != 0 {
		t.Errorf("second login mutations = %v", got)
	}
}

func TestLoginTokenAndCookieNeverRunTheOmarchyHook(t *testing.T) {
	hookCalls := stubOmarchyAfterLogin(t)
	server := quietServer(t)
	configHome := t.TempDir()

	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("cookie login: %v", err)
	}
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--token", "stored-token"); err != nil {
		t.Fatalf("token login: %v", err)
	}
	if *hookCalls != 0 {
		t.Errorf("hook ran %d times for token/cookie logins", *hookCalls)
	}
}

func TestRequireAuthRunsTheOmarchyHookAfterSignIn(t *testing.T) {
	isolateAgents(t)
	server := quietServer(t)
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"--base-url", server.URL, "auth", "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("prime globals: %v", err)
	}

	stubInteractive(t, true)
	stubAskToSignIn(t, true)
	logins := stubLoginInteractively(t, nil)
	hookCalls := stubOmarchyAfterLogin(t)
	prev := writer
	writer = output.New(output.Options{Format: output.FormatStyled, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	t.Cleanup(func() { writer = prev })

	if err := requireAuth(); err != nil {
		t.Fatalf("requireAuth: %v", err)
	}
	if *logins != 1 || *hookCalls != 1 {
		t.Errorf("logins=%d hook=%d, want 1/1", *logins, *hookCalls)
	}
}

// --- The wizard ---

func TestLiteWizardRunsTheOmarchyHookOnceAndFullDoesNot(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	stubStdinTerminal(t, true)
	logins := stubLoginInteractively(t, nil)
	hookCalls := stubOmarchyAfterLogin(t)
	server := quietServer(t)
	configHome := t.TempDir()
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "config", "set", "onboarded", "true"); err != nil {
		t.Fatalf("config set: %v", err)
	}

	// Lite: a logged-out bare `hey`, onboarded, signs in and hooks once.
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", false); err != nil {
		t.Fatalf("bare hey: %v", err)
	}
	if *logins != 1 || *hookCalls != 1 {
		t.Errorf("lite: logins=%d hook=%d, want 1/1", *logins, *hookCalls)
	}

	// Full: the wizard owns Step 3 and never fires the login hook.
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "setup"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if *hookCalls != 1 {
		t.Errorf("the full wizard must not double-fire the hook: %d", *hookCalls)
	}
}

func TestSetupWizardStep3InstallsThePlugin(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	confirms := stubConfirmOmarchyPanel(t, true, nil)
	server := identityServer(t)
	configHome := t.TempDir()
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	enabled := false
	stubOmarchyRun(t, func(name string, args ...string) (string, error) {
		command := strings.Join(append([]string{name}, args...), " ")
		switch {
		case name != "omarchy":
			return "", exec.ErrNotFound
		case strings.HasPrefix(command, "omarchy plugin list"):
			if enabled {
				return pluginListEnabled, nil
			}
			return pluginListAbsent, nil
		case strings.HasPrefix(command, "omarchy plugin add"):
			enabled = true
			return "Installed", nil
		default:
			return "", nil
		}
	})
	t.Setenv("OMARCHY_PATH", t.TempDir())

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"--base-url", server.URL, "setup", "--styled"})
	if err := root.Execute(); err != nil {
		t.Fatalf("setup: %v\n%s", err, stdout.String())
	}
	text := stdout.String()
	for _, want := range []string{"Step 3: Omarchy desktop", "HEY is in your Omarchy bar", "✓ Omarchy desktop", "Setup complete!"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if *confirms != 1 {
		t.Errorf("consent asked %d times, want 1", *confirms)
	}
}

func TestSetupWizardMachineRunSkipsOmarchyAndPointsAtSetup(t *testing.T) {
	isolateAgents(t)
	server := identityServer(t)
	configHome := t.TempDir()
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	ran := stubOmarchyRun(t, omarchyUnavailable)
	confirms := stubConfirmOmarchyPanel(t, true, nil)

	// Roll our own run so OMARCHY_PATH stays set (runAuthCommand isolates it).
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HOME", configHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", configHome)
	t.Setenv("OMARCHY_PATH", t.TempDir())
	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"--base-url", server.URL, "--json", "setup"})
	if err := root.Execute(); err != nil {
		t.Fatalf("setup --json: %v", err)
	}
	var response output.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	data := wizardData(t, response)
	if _, has := data["omarchy"]; has {
		t.Errorf("a machine run must not touch the desktop: %v", data["omarchy"])
	}
	if len(*ran) != 0 || *confirms != 0 {
		t.Errorf("a machine run must be inert: %v %d", *ran, *confirms)
	}
	found := false
	for _, crumb := range response.Breadcrumbs {
		if crumb.Command == "hey setup omarchy" {
			found = true
		}
	}
	if !found {
		t.Errorf("the envelope must point at hey setup omarchy: %+v", response.Breadcrumbs)
	}
}

func TestSetupWizardStep3CloneFailureIsIncomplete(t *testing.T) {
	isolateAgents(t)
	stubInteractive(t, true)
	stubConfirmOmarchyPanel(t, true, nil)
	server := identityServer(t)
	configHome := t.TempDir()
	if _, _, err := runAuthCommand(t, configHome, server.URL, "", true, "auth", "login", "--cookie", "session-cookie"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	origColor := colorDisabled
	colorDisabled = true
	t.Cleanup(func() { colorDisabled = origColor })

	stubOmarchyRun(t, func(name string, args ...string) (string, error) {
		command := strings.Join(append([]string{name}, args...), " ")
		switch {
		case name != "omarchy":
			return "", exec.ErrNotFound
		case strings.HasPrefix(command, "omarchy plugin list"):
			return pluginListAbsent, nil
		case strings.HasPrefix(command, "omarchy plugin add"):
			return "failed to clone " + omarchyBarPluginSource, errors.New("exit status 1")
		default:
			return "", nil
		}
	})
	t.Setenv("OMARCHY_PATH", t.TempDir())

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"--base-url", server.URL, "setup", "--styled"})
	if err := root.Execute(); err != nil {
		t.Fatalf("setup: %v\n%s", err, stdout.String())
	}
	text := stdout.String()
	if !strings.Contains(text, "needs attention") || !strings.Contains(text, "hey setup omarchy") {
		t.Errorf("a clone failure must leave the wizard incomplete with the remediation:\n%s", text)
	}
	if !strings.Contains(text, "✗ Omarchy desktop") {
		t.Errorf("the checklist must not contradict the issue list:\n%s", text)
	}
}

// --- hey setup omarchy, command level ---

func TestSetupOmarchyJSONInstallsThePlugin(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("OMARCHY_PATH", t.TempDir())
	enabled := false
	ran := stubOmarchyRun(t, func(name string, args ...string) (string, error) {
		command := strings.Join(append([]string{name}, args...), " ")
		switch {
		case name != "omarchy":
			return "", exec.ErrNotFound
		case strings.HasPrefix(command, "omarchy plugin list"):
			if enabled {
				return pluginListEnabled, nil
			}
			return pluginListAbsent, nil
		case strings.HasPrefix(command, "omarchy plugin add"):
			enabled = true
			return "Installed", nil
		default:
			return "", nil
		}
	})

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"setup", "omarchy", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("explicit --json setup must install: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "installed and enabled") {
		t.Errorf("steps = %s", stdout.String())
	}
	found := false
	for _, command := range *ran {
		if strings.HasPrefix(command, "omarchy plugin add") {
			found = true
		}
	}
	if !found {
		t.Errorf("the plugin must actually be added: %v", *ran)
	}
}

func TestSetupOmarchyForceFailsLoudWhenTheShellIsDown(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("OMARCHY_PATH", t.TempDir())
	stubOmarchyRun(t, func(name string, args ...string) (string, error) {
		if name == "omarchy" {
			return "omarchy-shell is not running (run from the desktop)", errors.New("exit status 1")
		}
		return "", exec.ErrNotFound
	})

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"setup", "omarchy", "--json"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a forced install with no shell must fail")
	}
	typed := apierr.AsError(err)
	if typed.Code != "setup_failed" || !strings.Contains(typed.Message, "shell is not running") {
		t.Fatalf("err = %v", err)
	}
	marker, _, readErr := readOmarchyPluginMarker(filepath.Join(stateHome, "hey-cli", "omarchy", "bar-plugin.json"))
	if readErr != nil || !marker.PendingEnable {
		t.Errorf("the intent must be durable for the next explicit setup: %+v %v", marker, readErr)
	}
}

func TestSetupOmarchyForceLockContentionFailsWithoutStateClaims(t *testing.T) {
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("OMARCHY_PATH", t.TempDir())
	var omarchyCalls []string
	stubOmarchyRun(t, func(name string, args ...string) (string, error) {
		if name == "omarchy" {
			omarchyCalls = append(omarchyCalls, strings.Join(args, " "))
		}
		return "", exec.ErrNotFound
	})
	lockPath := filepath.Join(stateHome, "hey-cli", "omarchy", "bar-plugin.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	if err != nil || !locked {
		t.Fatalf("take the lock: %v %v", locked, err)
	}
	defer func() { _ = lock.Unlock() }()

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"setup", "omarchy", "--json"})
	execErr := root.Execute()
	if execErr == nil {
		t.Fatal("contention must fail a forced install")
	}
	typed := apierr.AsError(execErr)
	if typed.Code != "setup_failed" || !strings.Contains(typed.Message, "another hey") {
		t.Fatalf("err = %v", execErr)
	}
	if len(omarchyCalls) != 0 {
		t.Errorf("no omarchy subprocess under contention: %v", omarchyCalls)
	}
	if _, statErr := os.Stat(filepath.Join(stateHome, "hey-cli", "omarchy", "bar-plugin.json")); !os.IsNotExist(statErr) {
		t.Error("no pending claim may be written under contention")
	}
}

func TestSetupOmarchyNotifyWithoutALayoutEntryUsesBarSet(t *testing.T) {
	// An enabled plugin needs no spelled-out shell.json entry; --notify must
	// still land, and seeding a partial layout would override the shell's
	// defaults — the omarchy CLI materializes the setting instead.
	env, ran, _ := testOmarchyEnvScripted(t, map[string]omarchyReply{
		"omarchy plugin list": {out: pluginListEnabled},
	})
	clonePluginCheckout(t, env)
	on := true

	bar := stepNamed(omarchySetup{env: env, forcePlugin: true, notify: &on}.apply(), "bar plugin")
	if bar.Status != "installed" || !strings.Contains(bar.Detail, "notifications on") {
		t.Fatalf("bar = %q %q", bar.Status, bar.Detail)
	}
	if !contains(*ran, "omarchy bar set "+omarchyBarPluginID+" notify true --json") {
		t.Errorf("the setting must be materialized by the CLI: %v", *ran)
	}

	// --no-notify with no entry is already off: nothing to write or run.
	off := false
	countBefore := len(*ran)
	bar = stepNamed(omarchySetup{env: env, forcePlugin: true, notify: &off}.apply(), "bar plugin")
	if bar.Status == "failed" {
		t.Fatalf("bar = %q %q", bar.Status, bar.Detail)
	}
	for _, command := range (*ran)[countBefore:] {
		if strings.HasPrefix(command, "omarchy bar set") {
			t.Errorf("no key exists to turn off: %v", command)
		}
	}
}

func TestSetupWizardLoggedOutMachineRunStillPointsAtOmarchySetup(t *testing.T) {
	// Logged out on Omarchy, the automatic hook can never run — the machine
	// envelope must carry the explicit command alongside the login crumb.
	isolateAgents(t)
	server := quietServer(t)
	stubOmarchyRun(t, omarchyUnavailable)
	t.Setenv("HEY_TOKEN", "")
	t.Setenv("HEY_NO_KEYRING", "1")
	t.Setenv("HEY_BASE_URL", "")
	configHome := t.TempDir()
	t.Setenv("HOME", configHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", configHome)
	t.Setenv("OMARCHY_PATH", t.TempDir())

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"--base-url", server.URL, "--json", "setup"})
	if err := root.Execute(); err != nil {
		t.Fatalf("setup --json: %v", err)
	}
	var response output.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	commands := make([]string, 0, len(response.Breadcrumbs))
	for _, crumb := range response.Breadcrumbs {
		commands = append(commands, crumb.Command)
	}
	if !contains(commands, "hey auth login") || !contains(commands, "hey setup omarchy") {
		t.Errorf("breadcrumbs = %v, want both the login and the omarchy remediation", commands)
	}
}

// --- doctor ---

func TestDoctorOmarchyBarPluginStates(t *testing.T) {
	env := omarchyEnv{home: t.TempDir(), omarchyPath: t.TempDir(), stateDir: t.TempDir()}

	check := checkOmarchyBarPlugin(env)
	if check["status"] != "warning" || check["message"] != "Not installed" {
		t.Errorf("fresh: %v", check)
	}

	seedMarker(t, env, omarchyPluginMarker{AcceptedAt: "2026-08-01T00:00:00Z", PendingEnable: true})
	if check := checkOmarchyBarPlugin(env); check["status"] != "warning" || !strings.Contains(check["message"], "pending") {
		t.Errorf("pending: %v", check)
	}

	seedMarker(t, env, omarchyPluginMarker{DeclinedAt: "2026-08-01T00:00:00Z"})
	if check := checkOmarchyBarPlugin(env); check["status"] != "info" || !strings.Contains(check["message"], "Declined") {
		t.Errorf("declined: %v", check)
	}

	seedMarker(t, env, omarchyPluginMarker{RemovedAt: "2026-08-01T00:00:00Z"})
	if check := checkOmarchyBarPlugin(env); check["status"] != "info" || !strings.Contains(check["message"], "Removed") {
		t.Errorf("removed: %v", check)
	}

	seedMarker(t, env, omarchyPluginMarker{AcceptedAt: "2026-08-01T00:00:00Z", InstalledAt: "2026-08-01T00:00:00Z"})
	clonePluginCheckout(t, env)
	if check := checkOmarchyBarPlugin(env); check["status"] != "warning" || !strings.Contains(check["message"], "not in the configured bar layout") {
		t.Errorf("cloned off-layout: %v", check)
	}

	writeShell(t, env, pluginShellJSON)
	if check := checkOmarchyBarPlugin(env); check["status"] != "ok" || check["message"] != "Installed (in the configured bar layout)" {
		t.Errorf("enabled: %v", check)
	}

	// A historical decline or removal does not outrank the present tense.
	seedMarker(t, env, omarchyPluginMarker{DeclinedAt: "2026-08-01T00:00:00Z"})
	if check := checkOmarchyBarPlugin(env); check["status"] != "ok" {
		t.Errorf("an enabled plugin outranks a historical decline: %v", check)
	}

	if err := os.WriteFile(env.markerPath(), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if check := checkOmarchyBarPlugin(env); check["status"] != "error" || !strings.Contains(check["message"], "unreadable") {
		t.Errorf("unreadable: %v", check)
	}
}

func TestDoctorOmarchyCheckOnlyWhenDetected(t *testing.T) {
	stubCompletionEnv(t, testCompletionEnv(t, "bash"))
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OMARCHY_PATH", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	hasCheck := func() bool {
		for _, check := range runDoctorChecks(context.Background(), newRootCmd()) {
			if check["name"] == "Omarchy Bar Plugin" {
				return true
			}
		}
		return false
	}
	if hasCheck() {
		t.Error("the check must not appear off Omarchy")
	}
	t.Setenv("OMARCHY_PATH", t.TempDir())
	if !hasCheck() {
		t.Error("the check must appear on Omarchy")
	}
}
