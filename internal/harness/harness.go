// Package harness detects and checks AI coding agent integration health.
package harness

import (
	"os"
	"path/filepath"
)

// SkillOwnershipMarker marks a skill directory as written by hey-cli.
// A skill file at a canonical path without it is somebody's hand-authored
// skill: present, but not a working hey integration.
const SkillOwnershipMarker = ".managed-by-hey-cli"

// SkillDirOwned reports whether hey-cli wrote the skill directory at dir.
// The marker itself must be a regular file — the same shape rule as every
// other skill file, so a planted symlink or directory in the marker's name
// cannot confer ownership. Intermediate directory links still resolve, so
// ownership stays visible through hey-cli's own canonical Claude symlink.
func SkillDirOwned(dir string) bool {
	return dir != "" && RegularSkillFile(filepath.Join(dir, SkillOwnershipMarker))
}

// RegularSkillFile reports whether path is a regular file (Lstat, so a
// symlinked SKILL.md does not count — its target was never inspected).
// Intermediate directories may still be symlinks: hey-cli's own canonical
// Claude link is a directory link whose SKILL.md is the baseline's regular
// file. This is the read-side twin of the install paths' write rule, so a
// state installation refuses is never reported healthy.
func RegularSkillFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// StatusCheck represents a single agent integration health check result.
type StatusCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "pass", "warn", "fail"
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}
