package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/version"
)

const (
	homebrewCask           = "basecamp/tap/hey"
	homebrewCaskroomPath   = "/caskroom/hey/"
	miseTool               = "github:basecamp/hey-cli"
	scoopApp               = "hey"
	scoopAppPath           = "/scoop/apps/hey/"
	scoopShimPath          = "/scoop/shims/"
	defaultGlobalScoopRoot = "/programdata/scoop"
	scoopCommandBaseName   = "hey"
)

// Package-manager seams, swappable for tests. The self-update seams live in
// upgrade_selfupdate.go and the release lookup in release.go.
var (
	executablePathResolver    = resolvedExecutablePath
	rawExecutablePathResolver = rawExecutablePath
	scoopPrefixResolver       = resolveScoopPrefix
	homebrewChecker           = isHomebrew
	homebrewUpgrader          = upgradeHomebrew
	miseInstallResolver       = resolveMiseInstall
	miseBinaryResolver        = resolveMiseBinary
	miseActiveBinaryResolver  = resolveMiseActiveBinary
	miseConfigPathResolver    = resolveMiseConfigPath
	miseUpgrader              = upgradeMise
	scoopChecker              = isScoop
	scoopGlobalScopeChecker   = isGlobalScoopInstall
	scoopUpgrader             = upgradeScoop
)

type upgradeCommand struct {
	cmd *cobra.Command
}

func newUpgradeCommand() *upgradeCommand {
	upgradeCommand := &upgradeCommand{}
	upgradeCommand.cmd = &cobra.Command{
		Use:   "upgrade [version]",
		Short: "Upgrade hey to the latest release",
		Long: `Check for a newer release and upgrade hey in place.

Installer-script and tarball installs under your home directory are replaced
with the verified release binary (Sigstore signature and SHA-256 checksum).
Mise, Homebrew and Scoop installs are upgraded through their package manager.
System packages, Nix and go install builds are never touched; the command
exits nonzero with the right next step for that install method.

A version argument targets that release instead of the latest — the way to
reach a release not yet marked latest, such as a prerelease. Upgrading only
ever moves forward: a version at or below the installed one is a no-op, and
Homebrew and Scoop installs always follow their package manager's version.`,
		Example: `  hey upgrade
  hey upgrade --json
  hey upgrade 0.2.0-rc.1`,
		Annotations: map[string]string{
			"agent_notes": "Exits 0 only when already current or when the upgrade was applied and confirmed. An optional version argument pins the target release for native and mise installs; upgrades never downgrade. Error codes: upgrade_required (an update exists but this install method must be upgraded another way — follow the hint), upgrade_incomplete, upgrade_unverified, upgrade_failed.",
		},
		Args: cobra.MaximumNArgs(1),
		RunE: upgradeCommand.run,
	}

	return upgradeCommand
}

func (c *upgradeCommand) run(cmd *cobra.Command, args []string) error {
	// Upgrade reports a single result map. --count and --ids-only can only
	// render lists, and output.Writer would reject them at write time — after
	// the binary had already been replaced, turning a completed upgrade into
	// a structured failure. Refuse them before anything runs.
	switch writer.EffectiveFormat() {
	case output.FormatCount:
		return apierr.ErrUsage("--count requires list data — hey upgrade reports a single result")
	case output.FormatIDs:
		return apierr.ErrUsage("--ids-only requires list data — hey upgrade reports a single result")
	default: // every other format renders upgrade's single result map
	}

	// Progress narration is human-facing and exists only in styled mode. The
	// machine formats discard it — success reserves stdout for the envelope,
	// and failure reserves stderr for the JSON error envelope Execute writes,
	// which narration mixed into either stream would corrupt. Package-manager
	// subprocess output follows the same rule.
	w, procErr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if !writer.IsStyled() {
		w, procErr = io.Discard, io.Discard
	}

	requested := ""
	if len(args) == 1 {
		requested = strings.TrimPrefix(strings.TrimSpace(args[0]), "v")
		if !isReleaseVersion(requested) {
			return apierr.ErrUsage(fmt.Sprintf("%q is not a release version — pass a semantic version like 1.2.3", args[0]))
		}
	}

	current := version.Version
	if !isReleaseVersion(current) {
		return &apierr.Error{
			Code:    "upgrade_required",
			Message: fmt.Sprintf("hey %s is not a release build — upgrade is only available for released versions", current),
			Hint:    "Rebuild from source, or install a release: " + installerHint(),
		}
	}

	fmt.Fprintf(w, "Current version: %s\n", current)
	fmt.Fprint(w, "Checking for updates… ")

	ctx := cmd.Context()
	var release releaseInfo
	var err error
	if requested != "" {
		release, err = releaseByTagFetcher(ctx, requested)
	} else {
		release, err = releaseFetcher(ctx)
	}
	if err != nil {
		fmt.Fprintln(w, "failed")
		detail := "could not check for updates"
		if requested != "" {
			detail = fmt.Sprintf("could not fetch release v%s", requested)
		}
		return &apierr.Error{
			Code:    "upgrade_failed",
			Message: fmt.Sprintf("%s: %v", detail, err),
			Hint:    "Check network access to api.github.com and retry. In CI, set GITHUB_TOKEN to avoid anonymous rate limits.",
		}
	}
	latest := release.Version

	// Validate the metadata before the availability check: a manually
	// published release with a non-semver tag (or a response missing
	// tag_name) would otherwise fall into isUpdateAvailable's inequality
	// fallback and start an upgrade toward a version nothing downstream can
	// compare or build asset names from — mutating a package-manager install
	// before its confirmation step could refuse the bogus version.
	if !isReleaseVersion(latest) {
		fmt.Fprintln(w, "failed")
		return errUpgradeFailedHint(
			fmt.Sprintf("release metadata is unusable: tag %q is not a semantic version", latest),
			"Retry once the release carries a semantic-version tag (vX.Y.Z): https://github.com/basecamp/hey-cli/releases",
		)
	}

	if !isUpdateAvailable(current, latest) {
		fmt.Fprintln(w, "already up to date")
		return writeUpgradeOK(
			map[string]string{"status": "up_to_date", "version": current},
			fmt.Sprintf("Already up to date (%s)", current),
		)
	}

	fmt.Fprintf(w, "update available: %s\n", latest)

	if homebrewChecker(ctx) {
		if requested != "" {
			return errPinnedManagedInstall(current, latest, "Homebrew",
				fmt.Sprintf("brew upgrade --cask %s", homebrewCask))
		}
		prefix, ok := homebrewPrefixFromExecutable()
		if !ok {
			return errUpgradeFailedHint(
				"the running executable could not be resolved to the Homebrew prefix that owns it",
				fmt.Sprintf("Run manually: brew upgrade --cask %s", homebrewCask),
			)
		}
		fmt.Fprintln(w, "Upgrading via Homebrew…")
		if brewErr := homebrewUpgrader(ctx, filepath.Join(prefix, "bin", "brew"), w, procErr); brewErr != nil {
			return &apierr.Error{
				Code:    "upgrade_failed",
				Message: fmt.Sprintf("brew upgrade failed for cask %s: %v", homebrewCask, brewErr),
				Hint:    fmt.Sprintf("Run manually for detail: brew upgrade --cask %s", homebrewCask),
			}
		}
		return confirmManagedUpgrade(ctx, w, "homebrew", filepath.Join(prefix, "bin", "hey"), current, latest, true,
			fmt.Sprintf("brew reinstall --cask %s", homebrewCask))
	}

	if scoopChecker(ctx) {
		global := scoopGlobalScopeChecker(ctx)
		if requested != "" {
			return errPinnedManagedInstall(current, latest, "Scoop",
				fmt.Sprintf("scoop update%s %s", scoopGlobalFlag(global), scoopApp))
		}
		fmt.Fprintln(w, "Upgrading via Scoop…")
		if scoopErr := scoopUpgrader(ctx, global, w, procErr); scoopErr != nil {
			return &apierr.Error{
				Code:    "upgrade_failed",
				Message: fmt.Sprintf("scoop update failed for app %s: %v", scoopApp, scoopErr),
				Hint:    fmt.Sprintf("Run manually for detail: scoop update%s %s", scoopGlobalFlag(global), scoopApp),
			}
		}
		return confirmManagedUpgrade(ctx, w, "scoop", scoopBinaryPath(ctx, global), current, latest, true,
			fmt.Sprintf("scoop uninstall%s %s && scoop install%s %s", scoopGlobalFlag(global), scoopApp, scoopGlobalFlag(global), scoopApp))
	}

	if install, owned, resolveErr := miseInstallResolver(ctx, current); owned {
		useCmd := miseUseCommand(install.configPath, latest)
		if resolveErr != nil {
			return &apierr.Error{
				Code:    "upgrade_required",
				Message: fmt.Sprintf("update available (%s → %s) for a mise-managed install, but mise ownership could not be resolved: %v", current, latest, resolveErr),
				Hint:    "Run manually: " + useCmd,
			}
		}

		fmt.Fprintln(w, "Upgrading via mise…")
		if miseErr := miseUpgrader(ctx, install.command, install.configPath, latest, w, procErr); miseErr != nil {
			return &apierr.Error{
				Code:    "upgrade_failed",
				Message: fmt.Sprintf("mise upgrade failed for %s: %v", miseTool, miseErr),
				Hint:    "Run manually for detail: " + useCmd,
			}
		}

		probePath, _ := miseActiveBinaryResolver(ctx, install.command)
		return confirmManagedUpgrade(ctx, w, "mise", probePath, current, latest, requested == "", useCmd)
	}

	// A `go install` build (stable or pseudo version alike) has no release
	// asset lineage to swap in — the module toolchain owns it.
	if goInstallChecker() {
		ref := "latest"
		if requested != "" {
			ref = "v" + requested
		}
		return &apierr.Error{
			Code:    "upgrade_required",
			Message: fmt.Sprintf("update available (%s → %s) but this binary was built with go install — upgrade it the same way", current, latest),
			Hint:    "Run: go install github.com/basecamp/hey-cli/cmd/hey@" + ref,
		}
	}

	target, err := selfUpdateTargetResolver()
	if err != nil {
		downloadURL := releaseTagURL(latest)
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Download the latest release from:\n  %s\n", downloadURL)
		return &apierr.Error{
			Code:    "upgrade_required",
			Message: fmt.Sprintf("update available (%s → %s) but the running executable could not be resolved: %v", current, latest, err),
			Hint:    "Download manually: " + downloadURL,
		}
	}

	if reason, hint := selfUpdateIneligibility(target); reason != "" {
		return &apierr.Error{
			Code:    "upgrade_required",
			Message: fmt.Sprintf("update available (%s → %s) but this install can't be self-updated (%s)", current, latest, reason),
			Hint:    hint,
		}
	}

	// Serialize the mutating phase. The release-metadata check above is
	// read-only and runs unlocked; the directory-wide lock is taken before
	// any asset download or filesystem mutation so a concurrent upgrade
	// cannot touch the same install directory and a concurrent invocation's
	// sidecar cleanup cannot reap this upgrade's live files.
	lock, err := upgradeLocker(target)
	if err != nil {
		return errUpgradeFailedHint(
			fmt.Sprintf("could not begin the upgrade: %v", err),
			"If another hey upgrade is running, wait for it to finish and retry.",
		)
	}
	defer func() { _ = lock.Unlock() }()

	return runNativeSelfUpdate(ctx, w, target, current, release)
}

// errPinnedManagedInstall refuses a version-pinned upgrade of a
// package-manager install: brew and scoop install whatever their manifests
// currently say, so a pin can't be honored — silently upgrading to something
// else would betray the request.
func errPinnedManagedInstall(current, requested, manager, managerCmd string) *apierr.Error {
	return &apierr.Error{
		Code:    "upgrade_required",
		Message: fmt.Sprintf("hey %s can't be upgraded to a specific version (%s) — this install is managed by %s, which installs its own current version", current, requested, manager),
		Hint:    "Upgrade to the manager's version instead: " + managerCmd,
	}
}

// confirmManagedUpgrade verifies a package-manager upgrade actually landed by
// probing the manager-derived entrypoint. No success is reported without a
// confirmed version. Managers that follow a moving manifest may select a newer
// release published during the upgrade; an exact mise request confirms the
// requested version itself.
func confirmManagedUpgrade(ctx context.Context, w io.Writer, method, probePath, current, latest string, allowNewer bool, reinstallCmd string) error {
	unverified := func(detail string) error {
		return &apierr.Error{
			Code:    "upgrade_unverified",
			Message: fmt.Sprintf("%s reported success but %s", method, detail),
			Hint:    fmt.Sprintf("Run `hey version` to confirm; if it still reports %s, run: %s", current, reinstallCmd),
		}
	}

	if probePath == "" {
		return unverified("the installed binary could not be located to confirm the version")
	}

	reported, err := binaryVersionProber(ctx, probePath)
	if err != nil {
		return unverified(fmt.Sprintf("probing %s failed: %v", probePath, err))
	}

	// Semantic comparison accepts reported >= latest only for managers that
	// follow a moving manifest: a release published while they run can
	// legitimately install something newer than the snapshot fetched at the
	// start. An exact mise request must resolve to that version. An unparseable
	// probe result fails safely as unconfirmed rather than guessing.
	reportedSemver, latestSemver := normalizeSemver(reported), normalizeSemver(latest)
	if !semver.IsValid(reportedSemver) || !semver.IsValid(latestSemver) {
		return unverified(fmt.Sprintf("the installed version %q could not be interpreted (expected %s)", reported, latest))
	}
	comparison := semver.Compare(reportedSemver, latestSemver)
	if comparison < 0 || (!allowNewer && comparison != 0) {
		return &apierr.Error{
			Code:    "upgrade_incomplete",
			Message: fmt.Sprintf("%s exited successfully but hey still reports %s (expected %s, upgrading from %s)", method, reported, latest, current),
			Hint:    "Try: " + reinstallCmd,
		}
	}

	fmt.Fprintf(w, "Upgraded %s → %s\n", current, reported)
	return writeUpgradeOK(
		map[string]string{"status": "upgraded", "from": current, "to": reported, "method": method},
		fmt.Sprintf("Upgraded %s → %s", current, reported),
	)
}

// writeUpgradeOK finishes a successful upgrade in the active output mode.
// Styled runs have already narrated the outcome on stdout — output.Writer.OK
// has no styled case and would append a JSON envelope after it — so only the
// machine formats write the envelope.
func writeUpgradeOK(data map[string]string, summary string) error {
	if writer.IsStyled() {
		return nil
	}
	return writeOK(data, output.WithSummary(summary))
}

// homebrewPrefixFromExecutable derives the Homebrew prefix that owns the
// running cask payload, <prefix>/Caskroom/hey/<version>/hey. The upgrade and
// its confirmation probe both bind to this prefix rather than to `brew` from
// PATH: on a machine with two Homebrew installations (Intel and Apple
// Silicon), the PATH lookup can win for the other prefix, and upgrading
// through it would leave the running installation untouched while the probe
// happily confirms the other one. The walk preserves the path's real case —
// the lowercased executablePathResolver is for substring matching only.
func homebrewPrefixFromExecutable() (string, bool) {
	exe, ok := rawExecutablePathResolver()
	if !ok {
		return "", false
	}

	appDir := filepath.Dir(filepath.Dir(exe)) // <prefix>/Caskroom/hey
	caskroomDir := filepath.Dir(appDir)       // <prefix>/Caskroom
	if !strings.EqualFold(filepath.Base(exe), "hey") ||
		!strings.EqualFold(filepath.Base(appDir), "hey") ||
		!strings.EqualFold(filepath.Base(caskroomDir), "caskroom") {
		return "", false
	}

	return filepath.Dir(caskroomDir), true
}

// scoopBinaryPath locates the entrypoint of the installation the upgrade just
// mutated. `scoop prefix` resolves the local install first and has no scope
// flag, so when the same app exists in both scopes a global upgrade probed
// through it would inspect the user-scope binary instead — the global path is
// derived from the running executable, which is known to live under the
// global root.
func scoopBinaryPath(ctx context.Context, global bool) string {
	if global {
		return globalScoopBinaryPath()
	}

	prefix, ok := scoopPrefixResolver(ctx, scoopApp)
	if !ok || prefix == "" {
		return ""
	}
	return filepath.Join(filepath.FromSlash(prefix), "hey.exe")
}

// globalScoopBinaryPath rebuilds <root>/scoop/apps/hey/current/hey.exe from
// the running executable — the app payload (whose own versioned directory is
// stale after the upgrade; "current" is re-resolved by the OS at probe time)
// or the shim. The lowercased path is probeable because Scoop only runs on
// Windows, where paths are case-insensitive.
func globalScoopBinaryPath() string {
	exe, ok := executablePathResolver()
	if !ok {
		return ""
	}

	for _, marker := range []string{scoopAppPath, scoopShimPath} {
		if root, _, found := strings.Cut(exe, marker); found {
			return filepath.Join(filepath.FromSlash(root), "scoop", "apps", scoopApp, "current", "hey.exe")
		}
	}
	return ""
}

func upgradeHomebrew(ctx context.Context, brew string, stdout io.Writer, stderr io.Writer) error {
	upgrade := exec.CommandContext(ctx, brew, "upgrade", "--cask", homebrewCask) //nolint:gosec // G204: brew is derived from the running executable's own Homebrew prefix
	upgrade.Stdout = stdout
	upgrade.Stderr = stderr
	return upgrade.Run()
}

func upgradeScoop(ctx context.Context, global bool, stdout io.Writer, stderr io.Writer) error {
	args := []string{"update"}
	if global {
		args = append(args, "-g")
	}
	args = append(args, scoopApp)

	upgrade := exec.CommandContext(ctx, "scoop", args...)
	upgrade.Stdout = stdout
	upgrade.Stderr = stderr
	return upgrade.Run()
}

// A miseInstall identifies the mise executable and configuration that own the
// running HEY binary. The same pair performs the upgrade, and normal mise
// resolution confirms the selected version afterwards.
type miseInstall struct {
	command    string
	configPath string
}

// resolveMiseInstall recognizes mise from its release-backend install layout
// before consulting any command on PATH. A recognized layout remains
// mise-owned when its manager cannot be resolved, protecting its files from
// native self-update.
func resolveMiseInstall(ctx context.Context, current string) (miseInstall, bool, error) {
	running, ok := rawExecutablePathResolver()
	if !ok || !isMiseInstallPath(running, current) {
		return miseInstall{}, false, nil
	}

	mise, err := exec.LookPath("mise")
	if err != nil {
		return miseInstall{}, true, err
	}

	managed, err := miseBinaryResolver(ctx, mise, current)
	if err != nil {
		return miseInstall{}, true, err
	}
	if !sameFile(running, managed) {
		return miseInstall{}, true, errors.New("mise resolves this HEY version to a different binary")
	}

	configPath, err := miseConfigPathResolver(ctx, mise, current)
	if err != nil {
		return miseInstall{}, true, err
	}
	return miseInstall{command: mise, configPath: configPath}, true, nil
}

func isMiseInstallPath(path, version string) bool {
	binary := filepath.Base(path)
	versionDir := filepath.Dir(path)
	toolDir := filepath.Dir(versionDir)
	installsDir := filepath.Dir(toolDir)

	return (strings.EqualFold(binary, "hey") || strings.EqualFold(binary, "hey.exe")) &&
		filepath.Base(versionDir) == version &&
		strings.EqualFold(filepath.Base(toolDir), "github-basecamp-hey-cli") &&
		strings.EqualFold(filepath.Base(installsDir), "installs")
}

func sameFile(a, b string) bool {
	aInfo, err := os.Stat(a)
	if err != nil {
		return false
	}
	bInfo, err := os.Stat(b)
	return err == nil && os.SameFile(aInfo, bInfo)
}

func resolveMiseConfigPath(ctx context.Context, mise, version string) (string, error) {
	list := exec.CommandContext(ctx, mise, "ls", "--json", miseTool) //nolint:gosec // mise is independently matched to the running install layout
	out, err := list.Output()
	if err != nil {
		return "", err
	}

	return miseConfigPath(out, version)
}

func miseConfigPath(data []byte, version string) (string, error) {
	var tools []struct {
		Version string `json:"version"`
		Active  bool   `json:"active"`
		Source  struct {
			Path string `json:"path"`
		} `json:"source"`
	}
	if err := json.Unmarshal(data, &tools); err != nil {
		return "", fmt.Errorf("could not read mise's tool configuration: %w", err)
	}
	for _, tool := range tools {
		if tool.Active && tool.Version == version && filepath.IsAbs(tool.Source.Path) {
			return tool.Source.Path, nil
		}
	}
	return "", errors.New("mise did not report the active HEY configuration")
}

func resolveMiseBinary(ctx context.Context, mise, version string) (string, error) {
	tool := miseTool + "@" + version
	resolve := exec.CommandContext(ctx, mise, "which", "hey", "--tool="+tool) // #nosec G204 -- mise is independently matched to the running install layout
	return miseBinaryPath(resolve)
}

func resolveMiseActiveBinary(ctx context.Context, mise string) (string, error) {
	resolve := exec.CommandContext(ctx, mise, "which", "hey") //nolint:gosec // mise is independently matched to the running install layout
	return miseBinaryPath(resolve)
}

func miseBinaryPath(resolve *exec.Cmd) (string, error) {
	out, err := resolve.Output()
	if err != nil {
		return "", err
	}

	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", errors.New("mise returned an empty HEY binary path")
	}
	return path, nil
}

func upgradeMise(ctx context.Context, mise, configPath, version string, stdout io.Writer, stderr io.Writer) error {
	upgrade := exec.CommandContext(ctx, mise, "use", "--path", configPath, miseTool+"@"+version) // #nosec G204 -- mise is independently matched to the running install layout
	upgrade.Stdout = stdout
	upgrade.Stderr = stderr
	return upgrade.Run()
}

func miseUseCommand(configPath, version string) string {
	if configPath == "" {
		return "mise use -g " + miseTool + "@" + version
	}
	return fmt.Sprintf("mise use --path %s %s@%s", shellQuoted(configPath), miseTool, version)
}

// isHomebrew reports whether the running binary is the cask's own payload,
// `<prefix>/caskroom/hey/<version>/hey` — not merely something that happens to
// live under a directory of that name.
func isHomebrew(_ context.Context) bool {
	exe, ok := executablePathResolver()
	if !ok {
		return false
	}

	_, rest, found := strings.Cut(exe, homebrewCaskroomPath)
	if !found {
		return false
	}
	segments := strings.Split(rest, "/")
	return len(segments) == 2 && segments[0] != "" && segments[1] == "hey"
}

// isScoop reports whether the running binary is the Scoop app's payload or its
// shim.
func isScoop(ctx context.Context) bool {
	exe, ok := executablePathResolver()
	if !ok {
		return false
	}

	switch {
	case strings.Contains(exe, scoopAppPath):
		return true
	case isScoopShimExecutable(exe):
		prefix, ok := scoopPrefixResolver(ctx, scoopApp)
		return ok && scoopPrefixMatchesShimScope(prefix, hasGlobalScoopPathPrefix(exe))
	default:
		return false
	}
}

func isScoopShimExecutable(exe string) bool {
	if !strings.Contains(exe, scoopShimPath) {
		return false
	}

	name := strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
	return name == scoopCommandBaseName
}

// resolveScoopPrefix returns the installed app root reported by `scoop
// prefix`, which checks local installs first and only then global ones — so
// it can only speak for the local scope; global probes derive their path from
// the running executable instead (globalScoopBinaryPath).
func resolveScoopPrefix(ctx context.Context, app string) (string, bool) {
	if app != scoopApp {
		return "", false
	}

	out, err := exec.CommandContext(ctx, "scoop", "prefix", app).Output() // #nosec G204 -- app is validated against the known constant above
	if err != nil {
		return "", false
	}

	prefix := strings.ToLower(filepath.ToSlash(strings.TrimSpace(string(out))))
	if prefix == "" {
		return "", false
	}

	return prefix, true
}

func scoopPrefixMatchesShimScope(prefix string, global bool) bool {
	return hasGlobalScoopPathPrefix(prefix) == global
}

func isGlobalScoopInstall(_ context.Context) bool {
	exe, ok := executablePathResolver()
	if !ok {
		return false
	}

	return hasGlobalScoopPathPrefix(exe)
}

func hasGlobalScoopPathPrefix(path string) bool {
	prefix := globalScoopRoot()
	path = stripWindowsVolume(path)
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// globalScoopRoot is Scoop's global install root in the same lowercased,
// forward-slash, volume-less form as resolvedExecutablePath: SCOOP_GLOBAL
// when set (Scoop's documented override), otherwise %ProgramData%\scoop.
func globalScoopRoot() string {
	root := strings.TrimSpace(os.Getenv("SCOOP_GLOBAL"))
	if root == "" {
		return defaultGlobalScoopRoot
	}
	root = stripWindowsVolume(strings.ToLower(strings.ReplaceAll(root, `\`, "/")))
	return strings.TrimRight(root, "/")
}

func stripWindowsVolume(path string) string {
	if len(path) >= 2 && path[1] == ':' {
		return path[2:]
	}

	return path
}

func scoopGlobalFlag(global bool) string {
	if global {
		return " -g"
	}

	return ""
}

// resolvedExecutablePath returns the running executable, symlinks resolved,
// lowercased and slash-normalized for substring matching only.
func resolvedExecutablePath() (string, bool) {
	exe, ok := rawExecutablePathResolver()
	if !ok {
		return "", false
	}

	return strings.ToLower(filepath.ToSlash(exe)), true
}

// rawExecutablePath returns the running executable with symlinks resolved and
// its real case preserved, for callers that build filesystem paths from it.
func rawExecutablePath() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}

	if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil {
		exe = resolved
	}

	return exe, true
}
