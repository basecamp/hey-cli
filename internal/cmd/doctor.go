package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/harness"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/version"
)

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Find login and configuration problems",
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := runDoctorChecks(cmd.Context())

			if writer.IsStyled() {
				w := cmd.OutOrStdout()
				allOK := true
				for _, c := range checks {
					icon := "ok"
					switch c["status"] {
					case "warning", "error":
						icon = "!!"
						allOK = false
					}
					fmt.Fprintf(w, "[%s] %s: %s\n", icon, c["name"], c["message"])
					if hint := c["hint"]; hint != "" {
						fmt.Fprintf(w, "     hint: %s\n", hint)
					}
				}
				if allOK {
					fmt.Fprintln(w, "\nAll checks passed.")
				}
				return nil
			}

			return writeOK(checks, output.WithSummary("Doctor checks complete"))
		},
	}
}

func runDoctorChecks(ctx context.Context) []map[string]string {
	var checks []map[string]string

	// CLI Version
	checks = append(checks, checkVersion(ctx))

	// Go Version
	checks = append(checks, map[string]string{
		"name":    "Go Version",
		"status":  "ok",
		"message": runtime.Version(),
	})

	// Config File
	configFile := filepath.Join(config.ConfigDir(), "config.json")
	if _, err := os.Stat(configFile); err == nil {
		checks = append(checks, map[string]string{
			"name":    "Config File",
			"status":  "ok",
			"message": configFile,
		})
	} else {
		checks = append(checks, map[string]string{
			"name":    "Config File",
			"status":  "ok",
			"message": "Not found (using defaults)",
		})
	}

	// Credentials
	if authMgr != nil {
		store := authMgr.GetStore()
		if store.UsingKeyring() {
			checks = append(checks, map[string]string{
				"name":    "Credentials",
				"status":  "ok",
				"message": "Stored in system keyring",
			})
		} else {
			checks = append(checks, map[string]string{
				"name":    "Credentials",
				"status":  "warning",
				"message": "Stored in plaintext file (keyring unavailable)",
			})
		}
	}

	// Authentication
	if os.Getenv("HEY_TOKEN") != "" {
		checks = append(checks, map[string]string{
			"name":    "Authentication",
			"status":  "ok",
			"message": "Authenticated via HEY_TOKEN env var",
		})
	} else if authMgr != nil && authMgr.IsAuthenticated() {
		store := authMgr.GetStore()
		creds, err := store.Load(authMgr.CredentialKey())
		if err == nil && creds.ExpiresAt > 0 {
			expiry := time.Unix(creds.ExpiresAt, 0)
			if time.Now().After(expiry) {
				checks = append(checks, map[string]string{
					"name":    "Authentication",
					"status":  "warning",
					"message": fmt.Sprintf("Token expired at %s — run `hey auth refresh`", expiry.Format(time.RFC3339)),
				})
			} else {
				checks = append(checks, map[string]string{
					"name":    "Authentication",
					"status":  "ok",
					"message": fmt.Sprintf("Authenticated (expires %s)", expiry.Format(time.RFC3339)),
				})
			}
		} else {
			checks = append(checks, map[string]string{
				"name":    "Authentication",
				"status":  "ok",
				"message": "Authenticated",
			})
		}
	} else {
		checks = append(checks, map[string]string{
			"name":    "Authentication",
			"status":  "error",
			"message": "Not authenticated",
			"hint":    "hey auth login",
		})
	}

	// Shell Completion
	shell := os.Getenv("SHELL")
	if shell != "" {
		checks = append(checks, map[string]string{
			"name":    "Shell",
			"status":  "ok",
			"message": shell,
		})
	}

	// Agent skill + detected coding agents
	checks = append(checks, checkBaselineSkill())
	for _, agent := range harness.DetectedAgents() {
		var agentChecks []*harness.StatusCheck
		if agent.Diagnostics != nil {
			agentChecks = agent.Diagnostics(ctx)
		} else if agent.Checks != nil {
			agentChecks = agent.Checks()
		}
		for _, c := range agentChecks {
			checks = append(checks, map[string]string{
				"name":    c.Name,
				"status":  doctorStatus(c.Status),
				"message": c.Message,
				"hint":    c.Hint,
			})
		}
	}

	// Git
	if _, err := exec.LookPath("git"); err == nil {
		checks = append(checks, map[string]string{
			"name":    "Git",
			"status":  "ok",
			"message": "Available",
		})
	}

	return checks
}

// doctorStatus maps a harness check status onto doctor's status vocabulary.
func doctorStatus(status string) string {
	switch status {
	case "pass":
		return "ok"
	case "warn":
		return "warning"
	case "fail":
		return "error"
	default:
		return status
	}
}

// checkBaselineSkill reports whether the shared agent skill is installed and
// current. Not installed is a warning, not an error: the CLI works without it,
// coding agents just get no HEY skill.
func checkBaselineSkill() map[string]string {
	if !baselineSkillInstalled() {
		return map[string]string{
			"name":    "Agent Skill",
			"status":  "warning",
			"message": "Not installed",
			"hint":    "hey skill install",
		}
	}
	check := map[string]string{
		"name":   "Agent Skill",
		"status": "ok",
	}
	installed := installedSkillVersion()
	switch {
	case installed == "":
		check["message"] = "Installed (version not tracked)"
	case version.IsDev():
		check["message"] = fmt.Sprintf("Installed (%s, dev build)", installed)
	case installed == version.Version:
		check["message"] = fmt.Sprintf("Up to date (%s)", installed)
	default:
		check["status"] = "warning"
		check["message"] = fmt.Sprintf("Stale (installed: %s, current: %s)", installed, version.Version)
		check["hint"] = "hey skill install"
	}
	return check
}

// checkVersion reports the build and, for release builds, whether a newer
// release exists. The lookup is best-effort and bounded: a slow or offline
// api.github.com must not stall doctor. Dev and other non-semver builds have
// nothing to compare against and skip it.
func checkVersion(ctx context.Context) map[string]string {
	goInstall := goInstallChecker()
	message := fmt.Sprintf("%s (%s, %s)", version.Version, version.Commit, version.Date)
	if goInstall {
		message += " [go install]"
	}
	check := map[string]string{
		"name":    "CLI Version",
		"status":  "ok",
		"message": message,
	}

	if !isReleaseVersion(version.Version) {
		return check
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	release, err := releaseFetcher(ctx)
	// A non-semver latest tag (say `nightly`) is nothing hey upgrade can
	// install, so it is not an update worth recommending.
	if err == nil && isReleaseVersion(release.Version) && isUpdateAvailable(version.Version, release.Version) {
		check["status"] = "warning"
		check["message"] = fmt.Sprintf("%s (update available: %s)", message, release.Version)
		// hey upgrade refuses go-install builds by design, so hint the module
		// toolchain command those users actually need.
		if goInstall {
			check["hint"] = "go install github.com/basecamp/hey-cli/cmd/hey@latest"
		} else {
			check["hint"] = "hey upgrade"
		}
	}

	return check
}
