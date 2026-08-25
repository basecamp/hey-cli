package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// Shell completions are packaged for the distro installs (.goreleaser.yaml
// ships them to /usr/share), so this is for everybody else: the curl
// installer, a downloaded tarball, and mise — which is how Omarchy installs
// hey, and mise neither runs a post-install hook nor scans a directory of its
// own for a tool's completions.

const (
	// completionCommandName is the name every shell completes, and the name
	// the completion files are keyed by.
	completionCommandName = "hey"

	// completionOwnerToken is what makes a completion file ours. It is the
	// file-sized version of the skill directories' ownership marker: a file at
	// one of these paths without it belongs to somebody else and is never
	// overwritten.
	//
	// It names no command, and that is the point — ownership has to survive the
	// prose around it being reworded or a command being renamed. Matching the
	// whole line would orphan every file already written the moment either
	// changed, and the next install would refuse a file hey wrote itself.
	completionOwnerToken = "# managed by hey-cli"

	// completionMarker is the line written into the file: the token, plus the
	// command that put it there for whoever opens it later.
	completionMarker = completionOwnerToken + " — written by `hey shell-completion install`"

	// completionMarkerWindow is how far into a file the marker is looked for.
	// It sits on the second line, so this is generous.
	completionMarkerWindow = 512

	// zshProbeTimeout bounds the one subprocess here. doctor runs it too.
	zshProbeTimeout = 2 * time.Second
)

// completionRequestAnchors is the expression cobra's generated script uses to
// call hey back for completions, per shell. Pinning replaces it, and a
// generator that stops emitting it is a failure rather than a silent no-op.
var completionRequestAnchors = map[string]string{
	"bash": "${words[0]} __complete",
	"zsh":  "${words[1]} __complete",
	"fish": "$args[1] __complete",
}

// completionSystemPaths are the locations a package manager installs hey's
// completions to. Nothing writes them — doctor reads them so a packaged
// install is not reported as missing.
var completionSystemPaths = map[string][]string{
	"bash": {
		"/usr/share/bash-completion/completions/hey",
		"/usr/local/share/bash-completion/completions/hey",
		"/etc/bash_completion.d/hey",
	},
	"zsh": {
		"/usr/share/zsh/site-functions/_hey",
		"/usr/share/zsh/vendor-completions/_hey",
		"/usr/local/share/zsh/site-functions/_hey",
	},
	"fish": {
		"/usr/share/fish/vendor_completions.d/hey.fish",
		"/usr/local/share/fish/vendor_completions.d/hey.fish",
	},
}

// unmanagedCompletionFileError reports a completion file hey did not write.
type unmanagedCompletionFileError struct {
	path   string
	remedy string
}

func (e *unmanagedCompletionFileError) Error() string {
	return fmt.Sprintf("%s exists but was not written by hey-cli; %s", e.path, e.remedy)
}

// completionEnv is everything the installer reads about this machine,
// injectable for tests.
type completionEnv struct {
	home      string
	shell     string // $SHELL, the shell hey was launched from
	dataDir   string
	configDir string
	bashDir   string // $BASH_COMPLETION_USER_DIR
	binary    string // the running binary, symlinks resolved
	onPath    string // what typing `hey` reaches, symlinks resolved
	zshFpath  func() []string
}

// completionEnvResolver is the one place a completion env comes from, so a
// test never reads the machine it runs on.
var completionEnvResolver = liveCompletionEnv

func liveCompletionEnv() completionEnv {
	home, _ := os.UserHomeDir()
	return completionEnv{
		home:      home,
		shell:     os.Getenv("SHELL"),
		dataDir:   xdgHomeDir("XDG_DATA_HOME", home, ".local", "share"),
		configDir: xdgHomeDir("XDG_CONFIG_HOME", home, ".config"),
		bashDir:   os.Getenv("BASH_COMPLETION_USER_DIR"),
		binary:    canonicalExecutable(),
		onPath:    canonicalCommandPath(completionCommandName),
		zshFpath:  zshFunctionPath,
	}
}

func xdgHomeDir(variable, home string, fallback ...string) string {
	if dir := os.Getenv(variable); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(append([]string{home}, fallback...)...)
}

func canonicalExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if canonical, canonErr := canonicalPath(exe); canonErr == nil {
		return canonical
	}
	return exe
}

func canonicalCommandPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	if canonical, canonErr := canonicalPath(path); canonErr == nil {
		return canonical
	}
	return path
}

// zshFunctionPath asks zsh itself where it looks for completion functions.
// fpath is nearly always extended in ~/.zshrc, which only an interactive shell
// reads, and a shell somebody else configured can print anything at all — so
// only lines naming an existing directory are believed. Memoized because both
// the installer and doctor ask.
var zshFunctionPath = sync.OnceValue(func() []string {
	if _, err := exec.LookPath("zsh"); err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), zshProbeTimeout)
	defer cancel()
	var out bytes.Buffer
	probe := exec.CommandContext(ctx, "zsh", "-ic", "print -rl -- $fpath")
	probe.Stdout = &out
	if err := probe.Run(); err != nil {
		return nil
	}

	var dirs []string
	for _, line := range strings.Split(out.String(), "\n") {
		dir := strings.TrimSpace(line)
		if filepath.IsAbs(dir) {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				dirs = append(dirs, dir)
			}
		}
	}
	return dirs
})

// completionTarget is where one shell picks hey's completions up.
type completionTarget struct {
	Shell string `json:"shell"`
	Path  string `json:"path"`
	// Hint carries the shell configuration the file still needs, empty when
	// the shell finds it on its own.
	Hint string `json:"hint,omitempty"`
}

// resolveShell takes the shell from the argument, and otherwise from $SHELL —
// the shell that launched hey.
func (e completionEnv) resolveShell(args []string) (string, error) {
	if len(args) > 0 {
		return installableShell(args[0])
	}
	if e.shell == "" {
		return "", apierr.ErrUsageHint("cannot tell which shell you use", "name it: hey shell-completion install bash|zsh|fish")
	}
	return installableShell(filepath.Base(e.shell))
}

func installableShell(shell string) (string, error) {
	switch shell {
	case "bash", "zsh", "fish":
		return shell, nil
	case "powershell", "pwsh":
		return "", apierr.ErrUsageHint("hey shell-completion install does not write PowerShell profiles",
			"hey shell-completion generate powershell | Out-String | Invoke-Expression")
	default:
		return "", apierr.ErrUsageHint("unsupported shell: "+shell, "hey shell-completion install bash|zsh|fish")
	}
}

func (e completionEnv) target(shell string) (completionTarget, error) {
	switch shell {
	case "bash":
		return completionTarget{Shell: shell, Path: filepath.Join(e.bashCompletionDir(), completionCommandName)}, nil
	case "fish":
		return completionTarget{Shell: shell, Path: filepath.Join(e.configDir, "fish", "completions", completionCommandName+".fish")}, nil
	case "zsh":
		dir, hint := e.zshCompletionDir()
		return completionTarget{Shell: shell, Path: filepath.Join(dir, "_"+completionCommandName), Hint: hint}, nil
	default:
		return completionTarget{}, apierr.ErrUsage("unsupported shell: " + shell)
	}
}

// bashCompletionDir is where bash-completion's loader looks for a per-user
// completion, which it loads lazily by the name of the command being
// completed. Nothing has to be sourced for it.
func (e completionEnv) bashCompletionDir() string {
	if filepath.IsAbs(e.bashDir) {
		return filepath.Join(e.bashDir, "completions")
	}
	return filepath.Join(e.dataDir, "bash-completion", "completions")
}

// zshCompletionDir picks the user's own completion directory when zsh already
// searches it, and otherwise the XDG one plus the line that puts it on fpath.
// zsh autoloads a completion function by filename, so a file outside fpath is
// read by nothing at all.
//
// Only the conventional directories are considered. Most of a real fpath is
// plugins — a plugin manager's cache is writable, on fpath, and the wrong
// place entirely: it is rewritten on every update, and the file would go with
// the plugin.
func (e completionEnv) zshCompletionDir() (dir, hint string) {
	preferred := filepath.Join(e.dataDir, "zsh", "site-functions")
	searched := e.zshFpath()
	for _, candidate := range e.zshUserCompletionDirs(preferred) {
		if slices.Contains(searched, candidate) && probeDirWritable(candidate) == nil {
			return candidate, ""
		}
	}
	return preferred, fmt.Sprintf("Add this to ~/.zshrc, above compinit:  fpath=(%s $fpath)", preferred)
}

// zshUserCompletionDirs is where people keep their own completion functions,
// most conventional first.
func (e completionEnv) zshUserCompletionDirs(preferred string) []string {
	return []string{
		preferred,
		filepath.Join(e.home, ".zfunc"),
		filepath.Join(e.home, ".zsh", "completions"),
		filepath.Join(e.configDir, "zsh", "completions"),
		filepath.Join(e.dataDir, "zsh", "completions"),
	}
}

// script generates the completion script for shell, marked as hey's and
// pointed at the binary the shell should ask for completions.
func (e completionEnv) script(root *cobra.Command, shell string) ([]byte, error) {
	initHelpFlags(root)

	var buf bytes.Buffer
	var err error
	switch shell {
	case "bash":
		err = root.GenBashCompletion(&buf)
	case "zsh":
		err = root.GenZshCompletion(&buf)
	case "fish":
		err = root.GenFishCompletion(&buf, true)
	default:
		err = apierr.ErrUsage("unsupported shell: " + shell)
	}
	if err != nil {
		return nil, err
	}
	return e.pinCompletionCommand(markCompletionScript(buf.Bytes()), shell)
}

// initHelpFlags gives every command its --help before a script is generated.
// cobra adds that flag lazily, to the commands along the path it is executing,
// and the bash script lists the flags it can see — so without this the script
// depends on which command generated it, and reinstalling it after `hey
// doctor` would produce a different file than after `hey setup`.
func initHelpFlags(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	cmd.InitDefaultVersionFlag()
	for _, sub := range cmd.Commands() {
		initHelpFlags(sub)
	}
}

// markCompletionScript writes the ownership marker into a generated script. It
// goes on the second line because zsh only autoloads a file whose first line
// is `#compdef`.
func markCompletionScript(script []byte) []byte {
	first, rest, found := bytes.Cut(script, []byte("\n"))
	var marked bytes.Buffer
	if found {
		marked.Write(first)
		marked.WriteString("\n" + completionMarker + "\n")
		marked.Write(rest)
	} else {
		marked.WriteString(completionMarker + "\n")
		marked.Write(script)
	}
	return marked.Bytes()
}

// pinCompletionCommand points the script at this binary when typing `hey`
// reaches it through a shim. cobra asks the command the user typed, which is
// what lets an alias complete, but a mise install is run through a shim — and
// Omarchy's wrapper re-resolves the tool on every call — so every press of Tab
// would pay for that resolution.
func (e completionEnv) pinCompletionCommand(script []byte, shell string) ([]byte, error) {
	anchor, known := completionRequestAnchors[shell]
	if !e.shimmed() || !known {
		return script, nil
	}
	if !bytes.Contains(script, []byte(anchor)) {
		return nil, fmt.Errorf("cannot point %s completions at %s: the generated script no longer calls %s", shell, e.binary, anchor)
	}
	return bytes.ReplaceAll(script, []byte(anchor), []byte(shellQuoted(e.binary)+" __complete")), nil
}

// shimmed reports whether hey is reached through something standing in front
// of it. Only a mise install counts: `hey` resolving to a different file is
// ordinarily just this binary being run from somewhere else — a build
// directory, a downloaded tarball — and those completions belong to the `hey`
// on PATH, not to the copy that happened to install them.
func (e completionEnv) shimmed() bool {
	return e.binary != "" && e.binary != e.onPath && e.miseInstalled()
}

// miseInstalled reports whether mise owns this binary. mise has no post-install
// hook and scans no directory of its own for a tool's completions, so a
// mise-installed hey registers them nowhere — which is what `hey
// shell-completion install` is here for, and Omarchy installs hey through mise.
func (e completionEnv) miseInstalled() bool {
	installs := filepath.Join(e.miseDataDir(), "installs")
	// Lexical containment: the binary path is already canonical, and the mise
	// directory need not exist for the answer to be no.
	relative, err := filepath.Rel(installs, e.binary)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (e completionEnv) miseDataDir() string {
	if dir := os.Getenv("MISE_DATA_DIR"); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(e.dataDir, "mise")
}

// shellQuoted quotes a path for the command string the completion script
// evaluates. bash, zsh and fish all read a single-quoted word literally.
func shellQuoted(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// installCompletion writes script to the target and reports whether the file
// changed.
func installCompletion(target completionTarget, script []byte, force bool) (bool, error) {
	if err := claimCompletionFile(target.Path, force); err != nil {
		return false, err
	}
	return writeFileIfChanged(target.Path, script, 0o644)
}

// claimCompletionFile is the gate every completion write goes through. It
// accepts a missing file and one carrying hey's marker, and refuses anything
// else — a completion somebody wrote by hand, or a package manager installed,
// is not ours to replace. --force overrides that for a plain file, never for a
// symlink or a directory: writing through one lands somewhere never inspected.
func claimCompletionFile(path string, force bool) error {
	info, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return fmt.Errorf("inspecting %s: %w", path, err)
	case !info.Mode().IsRegular():
		return &unmanagedCompletionFileError{path: path, remedy: "it is not a regular file, so hey will not write through it; move it aside and try again"}
	case force || ownedCompletionFile(path):
		return nil
	default:
		return &unmanagedCompletionFileError{path: path, remedy: "move it aside, or re-run with --force"}
	}
}

// ownedCompletionFile reports whether hey-cli wrote the completion at path.
func ownedCompletionFile(path string) bool {
	data, err := os.ReadFile(path) // #nosec G304 -- a completion path this package computed from the user's own home
	if err != nil {
		return false
	}
	return bytes.Contains(data[:min(len(data), completionMarkerWindow)], []byte(completionOwnerToken))
}

// packagedCompletionFinder is the seam for the one lookup here that reads paths
// outside the user's home, so a test never depends on the machine it runs on.
var packagedCompletionFinder = packagedCompletionPath

// packagedCompletionPath answers where a package manager installed hey's
// completions for shell, empty when none did.
func packagedCompletionPath(shell string) string {
	for _, path := range completionSystemPaths[shell] {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

// --- hey shell-completion install ---

type completionInstallCommand struct {
	cmd   *cobra.Command
	force bool
}

func newCompletionInstallCommand() *completionInstallCommand {
	completionInstallCommand := &completionInstallCommand{}
	completionInstallCommand.cmd = &cobra.Command{
		Use:   "install [bash|zsh|fish]",
		Short: "Install shell completions where your shell will find them",
		Long: `Write hey's completions to the place your shell reads them from, instead of
printing a script for you to redirect somewhere.

Without an argument the shell is taken from $SHELL. The files land in:

  bash   ~/.local/share/bash-completion/completions/hey
  zsh    a directory on your fpath, else ~/.local/share/zsh/site-functions/_hey
  fish   ~/.config/fish/completions/hey.fish

Re-running is a no-op once the file matches. A completion file hey did not
write is never overwritten — move it aside, or pass --force.

Installs from a package manager already ship completions system-wide, and this
leaves those alone rather than shadowing them with a copy that goes stale on the
next upgrade. Installs through mise, the installer script or a tarball are what
this is for.`,
		Example: `  hey shell-completion install
  hey shell-completion install zsh
  hey shell-completion install bash --force`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []cobra.Completion{"bash", "zsh", "fish"},
		Annotations: map[string]string{
			"agent_notes": "Writes one file under the user's home. Reports the path it wrote and any shell configuration still needed.",
		},
		RunE: completionInstallCommand.run,
	}

	completionInstallCommand.cmd.Flags().BoolVar(&completionInstallCommand.force, "force", false, "Write even over a packaged or foreign completion file")

	return completionInstallCommand
}

func (c *completionInstallCommand) run(cmd *cobra.Command, args []string) error {
	if err := rejectListOnlyFormats("hey shell-completion install"); err != nil {
		return err
	}

	env := completionEnvResolver()
	shell, err := env.resolveShell(args)
	if err != nil {
		return err
	}
	if packaged := packagedCompletionFinder(shell); packaged != "" && !c.force {
		return c.reportPackaged(cmd, shell, packaged)
	}

	target, err := env.target(shell)
	if err != nil {
		return err
	}
	script, err := env.script(cmd.Root(), shell)
	if err != nil {
		return err
	}

	changed, err := installCompletion(target, script, c.force)
	if err != nil {
		return &apierr.Error{
			Code:    "completion_install_failed",
			Message: err.Error(),
			Hint:    "Pick another shell with `hey shell-completion install <shell>`, or write the script yourself: hey shell-completion generate " + shell,
			Meta:    map[string]any{"shell": shell, "path": target.Path},
		}
	}

	return c.report(cmd, env, target, changed)
}

// reportPackaged leaves a package manager's completions alone. A user-level copy would
// shadow them and then go stale on the next package upgrade, which is worse than nothing.
func (c *completionInstallCommand) reportPackaged(cmd *cobra.Command, shell, path string) error {
	notices := []string{"Your package manager keeps them up to date.", "Use --force to install a copy anyway."}
	data := map[string]any{
		"shell":   shell,
		"path":    path,
		"status":  "packaged",
		"notices": notices,
	}
	if err := writeMutationLine(cmd,
		fmt.Sprintf("%s completions are already installed by your package manager at %s", shell, path),
		"Shell completions already installed", data); err != nil {
		return err
	}

	if writer.IsStyled() {
		w := cmd.OutOrStdout()
		for _, notice := range notices {
			fmt.Fprintln(w, notice)
		}
	}
	return nil
}

func (c *completionInstallCommand) report(cmd *cobra.Command, env completionEnv, target completionTarget, changed bool) error {
	status := "unchanged"
	line := fmt.Sprintf("%s completions are already installed at %s", target.Shell, target.Path)
	if changed {
		status = "installed"
		line = fmt.Sprintf("Installed %s completions to %s", target.Shell, target.Path)
	}

	notices := []string{"Open a new shell to pick them up."}
	if target.Hint != "" {
		notices = append(notices, target.Hint)
	}

	data := map[string]any{
		"shell":     target.Shell,
		"path":      target.Path,
		"status":    status,
		"completes": env.completingCommand(),
		"notices":   notices,
	}
	if err := writeMutationLine(cmd, line, "Shell completions installed", data); err != nil {
		return err
	}

	if writer.IsStyled() {
		w := cmd.OutOrStdout()
		for _, notice := range notices {
			fmt.Fprintln(w, notice)
		}
	}
	return nil
}

// completingCommand is what the installed script asks for completions: the
// binary itself where hey is reached through a launcher, and whatever the user
// typed otherwise, which is how an alias keeps completing.
func (e completionEnv) completingCommand() string {
	if e.shimmed() {
		return e.binary
	}
	return completionCommandName
}

// --- doctor ---

// checkShellCompletion reports whether the shell hey runs under can complete
// `hey`. Not installed is a warning, never an error: the CLI works either way.
func checkShellCompletion(root *cobra.Command) map[string]string {
	check := map[string]string{"name": "Shell Completion", "status": "ok"}

	env := completionEnvResolver()
	shell, err := env.resolveShell(nil)
	if err != nil {
		check["message"] = "Not installable for this shell"
		return check
	}
	target, err := env.target(shell)
	if err != nil {
		check["message"] = "Not installable for " + shell
		return check
	}

	switch {
	case !ownedCompletionFile(target.Path):
		return completionElsewhereCheck(check, shell, target)
	case target.Hint != "":
		check["status"] = "warning"
		check["message"] = fmt.Sprintf("Installed at %s, but zsh does not search that directory", target.Path)
		check["hint"] = target.Hint
	case completionScriptStale(root, env, shell, target.Path):
		check["status"] = "warning"
		check["message"] = fmt.Sprintf("Out of date (%s)", target.Path)
		check["hint"] = "hey shell-completion install"
	default:
		check["message"] = fmt.Sprintf("Installed (%s)", target.Path)
	}
	return check
}

// completionElsewhereCheck covers the paths hey did not write: a packaged
// install, somebody's own completion sitting where hey would install, and
// nothing at all.
func completionElsewhereCheck(check map[string]string, shell string, target completionTarget) map[string]string {
	if packaged := packagedCompletionFinder(shell); packaged != "" {
		check["message"] = fmt.Sprintf("Installed by your package manager (%s)", packaged)
		return check
	}
	if _, err := os.Lstat(target.Path); err == nil {
		check["message"] = fmt.Sprintf("Present at %s, but not written by hey-cli", target.Path)
		return check
	}
	check["status"] = "warning"
	check["message"] = "Not installed for " + shell
	check["hint"] = "hey shell-completion install"
	return check
}

// completionScriptStale reports whether the installed script differs from what
// hey generates now — which is what a new release with new commands leaves
// behind.
func completionScriptStale(root *cobra.Command, env completionEnv, shell, path string) bool {
	installed, err := os.ReadFile(path) // #nosec G304 -- a completion path this package computed from the user's own home
	if err != nil {
		return false
	}
	current, err := env.script(root, shell)
	if err != nil {
		return false
	}
	return !bytes.Equal(installed, current)
}
