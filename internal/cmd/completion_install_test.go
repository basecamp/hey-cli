package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/output"
)

// testCompletionEnv is a machine hey has never seen: everything under one
// temporary home, no shell probed, `hey` on PATH being this binary.
func testCompletionEnv(t *testing.T, shell string) completionEnv {
	t.Helper()
	home := t.TempDir()
	return completionEnv{
		home:      home,
		shell:     "/usr/bin/" + shell,
		dataDir:   filepath.Join(home, ".local", "share"),
		configDir: filepath.Join(home, ".config"),
		binary:    "/usr/local/bin/hey",
		onPath:    "/usr/local/bin/hey",
		zshFpath:  func() []string { return nil },
	}
}

func stubCompletionEnv(t *testing.T, env completionEnv) {
	t.Helper()
	previous := completionEnvResolver
	completionEnvResolver = func() completionEnv { return env }
	t.Cleanup(func() { completionEnvResolver = previous })
	stubPackagedCompletion(t, "")
}

// stubPackagedCompletion answers what a package manager installed, so no test
// reads the /usr paths of the machine it runs on.
func stubPackagedCompletion(t *testing.T, path string) {
	t.Helper()
	previous := packagedCompletionFinder
	packagedCompletionFinder = func(string) string { return path }
	t.Cleanup(func() { packagedCompletionFinder = previous })
}

func installedScript(t *testing.T, env completionEnv, shell string) (completionTarget, []byte) {
	t.Helper()
	target, err := env.target(shell)
	if err != nil {
		t.Fatalf("target(%q) = %v", shell, err)
	}
	script, err := env.script(newRootCmd(), shell)
	if err != nil {
		t.Fatalf("script(%q) = %v", shell, err)
	}
	if _, err := installCompletion(target, script, false); err != nil {
		t.Fatalf("installCompletion(%q) = %v", shell, err)
	}
	return target, script
}

func TestCompletionTargetsPerShell(t *testing.T) {
	env := testCompletionEnv(t, "bash")

	for _, tc := range []struct {
		shell string
		want  string
	}{
		{"bash", filepath.Join(env.dataDir, "bash-completion", "completions", "hey")},
		{"zsh", filepath.Join(env.dataDir, "zsh", "site-functions", "_hey")},
		{"fish", filepath.Join(env.configDir, "fish", "completions", "hey.fish")},
	} {
		target, err := env.target(tc.shell)
		if err != nil {
			t.Fatalf("target(%q) = %v", tc.shell, err)
		}
		if target.Path != tc.want {
			t.Errorf("target(%q) = %s, want %s", tc.shell, target.Path, tc.want)
		}
	}

	// BASH_COMPLETION_USER_DIR is where bash-completion looks when the user
	// moved it, so hey follows rather than writing where nothing reads.
	env.bashDir = filepath.Join(env.home, "bash-completion")
	target, err := env.target("bash")
	if err != nil {
		t.Fatalf("target(bash) = %v", err)
	}
	if want := filepath.Join(env.bashDir, "completions", "hey"); target.Path != want {
		t.Errorf("target(bash) = %s, want %s", target.Path, want)
	}
}

func TestCompletionZshPrefersADirectoryOnFpath(t *testing.T) {
	env := testCompletionEnv(t, "zsh")
	onFpath := filepath.Join(env.home, ".zfunc")
	if err := os.MkdirAll(onFpath, 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(env.home, ".cache", "some-plugin-manager", "zsh-defer")
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	env.zshFpath = func() []string { return []string{plugin, "/usr/share/zsh/site-functions", onFpath} }

	target, err := env.target("zsh")
	if err != nil {
		t.Fatalf("target(zsh) = %v", err)
	}
	// A plugin manager's cache is writable and on fpath, and is nobody's
	// completion directory: it is rewritten on every plugin update.
	if target.Path != filepath.Join(onFpath, "_hey") {
		t.Errorf("target(zsh) = %s, want %s", target.Path, filepath.Join(onFpath, "_hey"))
	}
	if target.Hint != "" {
		t.Errorf("a directory zsh already searches needs no hint, got %q", target.Hint)
	}

	// Nothing writable on fpath: the file still lands somewhere predictable,
	// and the user is told what zsh needs to find it.
	env.zshFpath = func() []string { return []string{"/usr/share/zsh/site-functions"} }
	target, err = env.target("zsh")
	if err != nil {
		t.Fatalf("target(zsh) = %v", err)
	}
	if want := filepath.Join(env.dataDir, "zsh", "site-functions", "_hey"); target.Path != want {
		t.Errorf("target(zsh) = %s, want %s", target.Path, want)
	}
	if !strings.Contains(target.Hint, "fpath=(") {
		t.Errorf("hint does not say how to put the directory on fpath: %q", target.Hint)
	}
}

func TestCompletionScriptIsMarkedAndLoadable(t *testing.T) {
	env := testCompletionEnv(t, "zsh")

	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, err := env.script(newRootCmd(), shell)
		if err != nil {
			t.Fatalf("script(%q) = %v", shell, err)
		}
		lines := strings.Split(string(script), "\n")
		if len(lines) < 2 || lines[1] != completionMarker {
			t.Errorf("%s script does not carry the marker on its second line: %q", shell, lines[:min(2, len(lines))])
		}
		if shell == "zsh" && lines[0] != "#compdef hey" {
			// compinit autoloads on the first line and nothing else.
			t.Errorf("zsh script starts with %q, want #compdef hey", lines[0])
		}
	}
}

func TestCompletionScriptDoesNotDependOnWhichCommandRan(t *testing.T) {
	env := testCompletionEnv(t, "bash")

	fresh, err := env.script(newRootCmd(), "bash")
	if err != nil {
		t.Fatalf("script(bash) = %v", err)
	}

	// cobra hands --help to the command it is executing, and the bash script
	// lists the flags it can see. Doctor generating the script to compare it
	// must arrive at the same bytes the installer wrote.
	used := newRootCmd()
	for _, sub := range used.Commands() {
		if sub.Name() == "version" {
			sub.InitDefaultHelpFlag()
		}
	}
	after, err := env.script(used, "bash")
	if err != nil {
		t.Fatalf("script(bash) = %v", err)
	}
	if !bytes.Equal(fresh, after) {
		t.Error("the generated script depends on which command cobra ran, so an up-to-date completion reads as stale")
	}
}

func TestCompletionScriptPinsTheBinaryBehindALauncher(t *testing.T) {
	env := testCompletionEnv(t, "bash")
	env.binary = filepath.Join(env.home, ".local/share/mise/installs/github-basecamp-hey-cli/latest/hey")
	env.onPath = filepath.Join(env.home, ".local/bin/hey")

	for shell, anchor := range completionRequestAnchors {
		script, err := env.script(newRootCmd(), shell)
		if err != nil {
			t.Fatalf("script(%q) = %v", shell, err)
		}
		if bytes.Contains(script, []byte(anchor)) {
			t.Errorf("%s script still asks %s for completions, paying for the launcher on every Tab", shell, anchor)
		}
		if !bytes.Contains(script, []byte(shellQuoted(env.binary)+" __complete")) {
			t.Errorf("%s script does not call %s for completions", shell, env.binary)
		}
	}

	if got := env.completingCommand(); got != env.binary {
		t.Errorf("completingCommand() = %q, want the binary the launcher runs", got)
	}
}

func TestCompletionScriptKeepsTheTypedCommandWithoutALauncher(t *testing.T) {
	env := testCompletionEnv(t, "bash")

	script, err := env.script(newRootCmd(), "bash")
	if err != nil {
		t.Fatalf("script(bash) = %v", err)
	}
	// cobra asks whatever the user typed, which is what keeps an alias
	// completing; a directly reachable binary has no reason to give that up.
	if !bytes.Contains(script, []byte(completionRequestAnchors["bash"])) {
		t.Error("bash script no longer asks the typed command for completions")
	}
	if got := env.completingCommand(); got != "hey" {
		t.Errorf("completingCommand() = %q, want hey", got)
	}

	// Running a copy from a build directory says nothing about the `hey` the
	// user types, and pinning that copy would outlive it.
	env.binary = filepath.Join(env.home, "src", "hey-cli", "bin", "hey")
	script, err = env.script(newRootCmd(), "bash")
	if err != nil {
		t.Fatalf("script(bash) = %v", err)
	}
	if !bytes.Contains(script, []byte(completionRequestAnchors["bash"])) {
		t.Errorf("a binary run from %s was pinned; only a shim should be", env.binary)
	}
}

func TestCompletionInstallIsIdempotent(t *testing.T) {
	env := testCompletionEnv(t, "fish")
	target, script := installedScript(t, env, "fish")

	written, err := os.ReadFile(target.Path)
	if err != nil {
		t.Fatalf("reading the installed completion: %v", err)
	}
	if !bytes.Equal(written, script) {
		t.Error("the installed file is not the generated script")
	}

	changed, err := installCompletion(target, script, false)
	if err != nil {
		t.Fatalf("second install = %v", err)
	}
	if changed {
		t.Error("re-installing an unchanged completion rewrote the file")
	}
}

func TestCompletionInstallRefusesAFileHeyDidNotWrite(t *testing.T) {
	env := testCompletionEnv(t, "bash")
	target, err := env.target("bash")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := []byte("# hand-written completion\ncomplete -F _mine hey\n")
	if err := os.WriteFile(target.Path, mine, 0o644); err != nil {
		t.Fatal(err)
	}

	script, err := env.script(newRootCmd(), "bash")
	if err != nil {
		t.Fatal(err)
	}

	var unmanaged *unmanagedCompletionFileError
	if _, err := installCompletion(target, script, false); !errors.As(err, &unmanaged) {
		t.Fatalf("installCompletion = %v, want a refusal", err)
	}
	if current, _ := os.ReadFile(target.Path); !bytes.Equal(current, mine) {
		t.Error("a completion hey did not write was modified")
	}

	if _, err := installCompletion(target, script, true); err != nil {
		t.Fatalf("--force install = %v", err)
	}
	if !ownedCompletionFile(target.Path) {
		t.Error("--force did not leave hey's own completion behind")
	}
}

func TestCompletionInstallRefusesToWriteThroughALink(t *testing.T) {
	env := testCompletionEnv(t, "fish")
	target, err := env.target("fish")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(env.home, "somebody-elses-file")
	if err := os.WriteFile(elsewhere, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, target.Path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	script, err := env.script(newRootCmd(), "fish")
	if err != nil {
		t.Fatal(err)
	}

	var unmanaged *unmanagedCompletionFileError
	// --force overrides a plain file somebody else wrote, never a link whose
	// target was never inspected.
	for _, force := range []bool{false, true} {
		if _, err := installCompletion(target, script, force); !errors.As(err, &unmanaged) {
			t.Fatalf("installCompletion(force=%v) = %v, want a refusal", force, err)
		}
	}
	if content, _ := os.ReadFile(elsewhere); string(content) != "keep me\n" {
		t.Error("the link's target was written through")
	}
}

func TestCompletionInstallResolvesTheShell(t *testing.T) {
	env := testCompletionEnv(t, "fish")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "fish"},
		{[]string{"zsh"}, "zsh"},
	} {
		got, err := env.resolveShell(tc.args)
		if err != nil || got != tc.want {
			t.Errorf("resolveShell(%v) = %q, %v; want %q", tc.args, got, err, tc.want)
		}
	}

	if _, err := env.resolveShell([]string{"powershell"}); err == nil {
		t.Error("powershell has no file to install; want a usage error pointing at the manual route")
	}
	if _, err := env.resolveShell([]string{"nushell"}); err == nil {
		t.Error("an unsupported shell must be refused")
	}

	env.shell = ""
	if _, err := env.resolveShell(nil); err == nil {
		t.Error("without $SHELL hey cannot guess; want a usage error")
	}
}

func TestCompletionInstallCommandReportsWhatItWrote(t *testing.T) {
	env := testCompletionEnv(t, "bash")
	stubCompletionEnv(t, env)

	out, err := runCompletionInstall(t, "bash")
	if err != nil {
		t.Fatalf("hey shell-completion install bash = %v", err)
	}
	target, _ := env.target("bash")
	if !strings.Contains(out, target.Path) {
		t.Errorf("output does not name the file it wrote:\n%s", out)
	}
	if !ownedCompletionFile(target.Path) {
		t.Errorf("no completion at %s", target.Path)
	}

	out, err = runCompletionInstall(t, "bash")
	if err != nil {
		t.Fatalf("re-running = %v", err)
	}
	if !strings.Contains(out, "already installed") {
		t.Errorf("a second run does not report the file as already installed:\n%s", out)
	}
}

func TestCompletionInstallLeavesAPackagedCompletionAlone(t *testing.T) {
	env := testCompletionEnv(t, "bash")
	stubCompletionEnv(t, env)
	stubPackagedCompletion(t, "/usr/share/bash-completion/completions/hey")

	out, err := runCompletionInstall(t, "bash")
	if err != nil {
		t.Fatalf("hey shell-completion install bash = %v", err)
	}
	if !strings.Contains(out, "/usr/share/bash-completion/completions/hey") {
		t.Errorf("output does not name the packaged file:\n%s", out)
	}

	target, _ := env.target("bash")
	if _, err := os.Lstat(target.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a user-level copy was written to %s, shadowing the packaged one", target.Path)
	}

	install := newCompletionInstallCommand()
	install.force = true
	if _, err := runCompletionInstallWith(t, install, "bash"); err != nil {
		t.Fatalf("--force = %v", err)
	}
	if !ownedCompletionFile(target.Path) {
		t.Errorf("--force did not write a copy to %s", target.Path)
	}
}

func runCompletionInstall(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runCompletionInstallWith(t, newCompletionInstallCommand(), args...)
}

func runCompletionInstallWith(t *testing.T, install *completionInstallCommand, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	previousWriter := writer
	t.Cleanup(func() { writer = previousWriter })
	writer = output.New(output.Options{Format: output.FormatStyled, Stdout: &out, Stderr: &out})

	root := newRootCmd()
	install.cmd.SetOut(&out)
	install.cmd.SetErr(&out)
	root.AddCommand(install.cmd)

	err := install.run(install.cmd, args)
	return out.String(), err
}

func TestDoctorShellCompletionCheck(t *testing.T) {
	env := testCompletionEnv(t, "bash")
	stubCompletionEnv(t, env)
	root := newRootCmd()

	check := checkShellCompletion(root)
	if check["status"] != "warning" || !strings.Contains(check["hint"], "hey shell-completion install") {
		t.Errorf("uninstalled completions = %v, want a warning with an install hint", check)
	}

	target, script := installedScript(t, env, "bash")
	check = checkShellCompletion(root)
	if check["status"] != "ok" || !strings.Contains(check["message"], target.Path) {
		t.Errorf("installed completions = %v, want ok naming %s", check, target.Path)
	}

	// A release that changed the command tree leaves the installed script
	// behind, which is the one thing doctor can tell the user to fix.
	stale := append(bytes.TrimSuffix(script, []byte("\n")), []byte("\n# from an older hey\n")...)
	if err := os.WriteFile(target.Path, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	check = checkShellCompletion(root)
	if check["status"] != "warning" || !strings.Contains(check["message"], "Out of date") {
		t.Errorf("stale completions = %v, want an out-of-date warning", check)
	}
}

func TestDoctorShellCompletionLeavesOtherPeoplesFilesAlone(t *testing.T) {
	env := testCompletionEnv(t, "fish")
	stubCompletionEnv(t, env)
	target, err := env.target("fish")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.Path, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	check := checkShellCompletion(newRootCmd())
	if check["status"] != "ok" || !strings.Contains(check["message"], "not written by hey-cli") {
		t.Errorf("check = %v, want it reported as somebody else's rather than as missing", check)
	}
}
