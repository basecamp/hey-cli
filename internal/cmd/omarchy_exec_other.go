//go:build !unix

package cmd

import "os/exec"

// setOmarchyProcessGroup is a no-op where process groups are not a thing:
// CommandContext's default kill takes the direct child, which is all these
// platforms offer.
func setOmarchyProcessGroup(*exec.Cmd) {}
