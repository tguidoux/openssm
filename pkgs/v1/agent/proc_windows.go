//go:build windows

package agent

import "os/exec"

// configureProcess is a no-op on Windows; exec.CommandContext kills the
// direct child on cancellation.
func configureProcess(c *exec.Cmd) {}
