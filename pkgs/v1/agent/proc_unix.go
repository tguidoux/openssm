//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// configureProcess runs the step in its own process group so cancellation
// and timeouts kill the whole tree, not just the shell.
func configureProcess(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
