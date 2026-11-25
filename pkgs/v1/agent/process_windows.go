//go:build windows

package agent

import (
	"errors"

	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
)

// startProcess is not implemented on Windows yet (no ConPTY support).
func startProcess(s agentproto.SessionPayload, shell string) (Process, error) {
	return nil, errors.New("interactive sessions are not supported on Windows by openssm-agent yet")
}
