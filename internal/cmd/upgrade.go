package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/version"
)

const (
	homebrewCask         = "basecamp/tap/hey"
	homebrewCaskroomPath = "/caskroom/hey/"
	scoopApp             = "hey"
	scoopAppPath         = "/scoop/apps/hey/"
	scoopShimPath        = "/scoop/shims/"
	globalScoopRootPath  = "/programdata/scoop/"
	scoopCommandBaseName = "hey"
)

// Package-manager seams, swappable for tests. The self-update seams live in
// upgrade_selfupdate.go and the release lookup in release.go.
var (
	executablePathResolver  = resolvedExecutablePath
	brewPrefixResolver      = resolveBrewPrefix
	scoopPrefixResolver     = resolveScoopPrefix
	homebrewChecker         = isHomebrew
	homebrewUpgrader        = upgradeHomebrew
	scoopChecker            = isScoop
	scoopGlobalScopeChecker = isGlobalScoopInstall
	scoopUpgrader           = upgradeScoop
)

type upgradeCommand struct {
	cmd *cobra.Command
}

func newUpgradeCommand() *upgradeCommand {
	upgradeCommand := &upgradeCommand{}
	upgradeCommand.cmd = &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade hey to the latest release",
		Long: `Check for a newer release and upgrade hey in place.

Installer-script and tarball installs under your home directory are replaced
with the verified release binary (Sigstore signature and SHA-256 checksum).
Homebrew and Scoop installs are upgraded through their package manager.
System packages, Nix and go install builds are never touched; the command
exits nonzero with the right next step for that install method.`,
		Example: `  hey upgrade
  hey upgrade --json`,
		Annotations: map[string]string{
			"agent_notes": "Exits 0 only when already current or when the upgrade was applied and confirmed. Error codes: upgrade_required (an update exists but this install method must be upgraded another way — follow the hint), upgrade_incomplete, upgrade_unverified, upgrade_failed.",
		},
		Args: cobra.NoArgs,
		RunE: upgradeCommand.run,
	}

	return upgradeCommand
}

func (c *upgradeCommand) run(cmd *cobra.Command, args []string) error {
	// Progress narration goes to stdout when styled and to stderr otherwise,
	// so the JSON envelope on stdout stays parseable.
	w := cmd.OutOrStdout()
	if !writer.IsStyled() {
		w = cmd.ErrOrStderr()
	}

	current := version.Version
	if !isReleaseVersion(current) {
		return &output.Error{
			Code:    "upgrade_required",
			Message: fmt.Sprintf("hey %s is not a release build — upgrade is only available for released versions", current),
			Hint:    "Rebuild from source, or install a release: " + installerHint(),
		}
	}

	fmt.Fprintf(w, "Current version: %s\n", current)
	fmt.Fprint(w, "Checking for updates… ")

	ctx := cmd.Context()
	release, err := releaseFetcher(ctx)
	if err != nil {
		fmt.Fprintln(w, "failed")
		return &output.Error{
			Code:    "upgrade_failed",
			Message: fmt.Sprintf("could not check for updates: %v", err),
			Hint:    "Check network access to api.github.com and retry. In CI, set GITHUB_TOKEN to avoid anonymous rate limits.",
		}
	}
	latest := release.Version

	if !isUpdateAvailable(current, latest) {
		fmt.Fprintln(w, "already up to date")
		return writeUpgradeOK(
			map[string]string{"status": "up_to_date", "version": current},
			fmt.Sprintf("Already up to date (%s)", current),
		)
	}

	fmt.Fprintf(w, "update available: %s\n", latest)

	if homebrewChecker(ctx) {
		fmt.Fprintln(w, "Upgrading via Homebrew…")
		if brewErr := homebrewUpgrader(ctx, w, cmd.ErrOrStderr()); brewErr != nil {
			return &output.Error{
				Code:    "upgrade_failed",
				Message: fmt.Sprintf("brew upgrade failed for cask %s: %v", homebrewCask, brewErr),
				Hint:    fmt.Sprintf("Run manually for detail: brew upgrade --cask %s", homebrewCask),
			}
		}
		return confirmManagedUpgrade(ctx, w, "homebrew", homebrewBinaryPath(ctx), current, latest,
			fmt.Sprintf("brew reinstall --cask %s", homebrewCask))
	}

	if scoopChecker(ctx) {
		global := scoopGlobalScopeChecker(ctx)
		fmt.Fprintln(w, "Upgrading via Scoop…")
		if scoopErr := scoopUpgrader(ctx, global, w, cmd.ErrOrStderr()); scoopErr != nil {
			return &output.Error{
				Code:    "upgrade_failed",
				Message: fmt.Sprintf("scoop update failed for app %s: %v", scoopApp, scoopErr),
				Hint:    fmt.Sprintf("Run manually for detail: scoop update%s %s", scoopGlobalFlag(global), scoopApp),
			}
		}
		return confirmManagedUpgrade(ctx, w, "scoop", scoopBinaryPath(ctx), current, latest,
			fmt.Sprintf("scoop uninstall%s %s && scoop install%s %s", scoopGlobalFlag(global), scoopApp, scoopGlobalFlag(global), scoopApp))
	}

	// A `go install` build (stable or pseudo version alike) has no release
	// asset lineage to swap in — the module toolchain owns it.
	if goInstallChecker() {
		return &output.Error{
			Code:    "upgrade_required",
			Message: fmt.Sprintf("update available (%s → %s) but this binary was built with go install — upgrade it the same way", current, latest),
			Hint:    "Run: go install github.com/basecamp/hey-cli/cmd/hey@latest",
		}
	}

	target, err := selfUpdateTargetResolver()
	if err != nil {
		downloadURL := releaseTagURL(latest)
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Download the latest release from:\n  %s\n", downloadURL)
		return &output.Error{
			Code:    "upgrade_required",
			Message: fmt.Sprintf("update available (%s → %s) but the running executable could not be resolved: %v", current, latest, err),
			Hint:    "Download manually: " + downloadURL,
		}
	}

	if reason, hint := selfUpdateIneligibility(target); reason != "" {
		return &output.Error{
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

// confirmManagedUpgrade verifies a package-manager upgrade actually landed by
// probing the manager-derived entrypoint. No success is reported without a
// confirmed version: a probe that can't run is upgrade_unverified, a probe
// that reports anything but the latest version is upgrade_incomplete.
func confirmManagedUpgrade(ctx context.Context, w io.Writer, method, probePath, current, latest, reinstallCmd string) error {
	unverified := func(detail string) error {
		return &output.Error{
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

	// Semantic comparison, accepting reported >= latest: a release published
	// while the manager ran can legitimately install something newer than the
	// snapshot fetched at the start. An unparseable probe result fails safely
	// as unconfirmed rather than pretending to know either way.
	reportedSemver, latestSemver := normalizeSemver(reported), normalizeSemver(latest)
	if !semver.IsValid(reportedSemver) || !semver.IsValid(latestSemver) {
		return unverified(fmt.Sprintf("the installed version %q could not be interpreted (expected %s)", reported, latest))
	}
	if semver.Compare(reportedSemver, latestSemver) < 0 {
		return &output.Error{
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

// homebrewBinaryPath derives the brew-managed entrypoint from `brew --prefix`.
// os.Executable is deliberately not used here: Go documents it may return the
// symlink or the resolved target — possibly stale after the cask swap.
func homebrewBinaryPath(ctx context.Context) string {
	prefix, err := brewPrefixResolver(ctx)
	if err != nil || prefix == "" {
		return ""
	}
	return filepath.Join(prefix, "bin", "hey")
}

// scoopBinaryPath derives the scoop-managed entrypoint from `scoop prefix`
// (the loaded module on Windows is the exe, not the shim, and may be stale).
func scoopBinaryPath(ctx context.Context) string {
	prefix, ok := scoopPrefixResolver(ctx, scoopApp)
	if !ok || prefix == "" {
		return ""
	}
	return filepath.Join(filepath.FromSlash(prefix), "hey.exe")
}

func resolveBrewPrefix(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "brew", "--prefix").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func upgradeHomebrew(ctx context.Context, stdout io.Writer, stderr io.Writer) error {
	upgrade := exec.CommandContext(ctx, "brew", "upgrade", "--cask", homebrewCask)
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

// resolveScoopPrefix returns the installed app root reported by `scoop prefix`.
// Scoop already checks local installs first, then global installs, so there is
// no separate scope flag to thread through here.
func resolveScoopPrefix(ctx context.Context, app string) (string, bool) {
	if app != scoopApp {
		return "", false
	}

	out, err := exec.CommandContext(ctx, "scoop", "prefix", app).Output() //nolint:gosec // G204: app is validated against the known constant above
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
	prefix := strings.TrimSuffix(globalScoopRootPath, "/")
	path = stripWindowsVolume(path)
	return path == prefix || strings.HasPrefix(path, prefix+"/")
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
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}

	if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil {
		exe = resolved
	}

	return strings.ToLower(filepath.ToSlash(exe)), true
}
