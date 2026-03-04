package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/version"
)

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check system health and configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := runDoctorChecks()

			if writer.IsStyled() {
				allOK := true
				for _, c := range checks {
					icon := "ok"
					if c["status"] == "warning" {
						icon = "!!"
						allOK = false
					} else if c["status"] == "error" {
						icon = "!!"
						allOK = false
					}
					fmt.Printf("[%s] %s: %s\n", icon, c["name"], c["message"])
				}
				if allOK {
					fmt.Println("\nAll checks passed.")
				}
				return nil
			}

			return writer.OK(checks, output.WithSummary("Doctor checks complete"))
		},
	}
}

func runDoctorChecks() []map[string]string {
	var checks []map[string]string

	// CLI Version
	checks = append(checks, map[string]string{
		"name":    "CLI Version",
		"status":  "ok",
		"message": fmt.Sprintf("%s (%s, %s)", version.Version, version.Commit, version.Date),
	})

	// Go Version
	checks = append(checks, map[string]string{
		"name":    "Go Version",
		"status":  "ok",
		"message": runtime.Version(),
	})

	// Config File
	configDir := os.ExpandEnv("${XDG_CONFIG_HOME}")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = home + "/.config"
	}
	configFile := configDir + "/hey-cli/config.json"
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
			"message": "Not authenticated — run `hey auth login`",
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

	// Claude Plugin
	if _, err := os.Stat(".claude-plugin/plugin.json"); err == nil {
		checks = append(checks, map[string]string{
			"name":    "Claude Plugin",
			"status":  "ok",
			"message": "Found .claude-plugin/plugin.json",
		})
	} else {
		// Check if installed globally
		home, _ := os.UserHomeDir()
		if _, err := os.Stat(home + "/.agents/skills/hey/SKILL.md"); err == nil {
			checks = append(checks, map[string]string{
				"name":    "Claude Skill",
				"status":  "ok",
				"message": "Installed at ~/.agents/skills/hey/",
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
