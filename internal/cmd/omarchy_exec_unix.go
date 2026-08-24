//go:build unix

package cmd

import (
	"os/exec"
	"syscall"
)

// setOmarchyProcessGroup puts the child in its own process group and kills
// the whole group on timeout, so a plugin add's git subprocess dies with it
// instead of holding the pipes open.
func setOmarchyProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
