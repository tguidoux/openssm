//go:build !windows

package agent

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"syscall"

	"github.com/creack/pty"
	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/ssmmessages"
)

// ptyProcess runs a command attached to a pseudo-terminal.
type ptyProcess struct {
	cmd *exec.Cmd
	tty *os.File
}

func (p *ptyProcess) Read(b []byte) (int, error)  { return p.tty.Read(b) }
func (p *ptyProcess) Write(b []byte) (int, error) { return p.tty.Write(b) }
func (p *ptyProcess) Close() error                { return p.tty.Close() }

func (p *ptyProcess) Resize(cols, rows uint16) error {
	return pty.Setsize(p.tty, &pty.Winsize{Cols: cols, Rows: rows})
}

func (p *ptyProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	// The shell is a session leader; signal its whole process group.
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGHUP)
	return p.cmd.Process.Kill()
}

func (p *ptyProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// startProcess spawns the session process inside a PTY.
func startProcess(s agentproto.SessionPayload, shell string) (Process, error) {
	var cmd *exec.Cmd
	switch s.SessionType {
	case ssmmessages.SessionTypeInteractive, ssmmessages.SessionTypeNonInteractive:
		if s.Command == "" {
			return nil, errors.New("no command specified for this session document")
		}
		cmd = exec.Command(shell, "-c", s.Command)
	default:
		cmd = exec.Command(shell, "-l")
	}
	if s.RunAsUser != "" && os.Geteuid() == 0 {
		u, err := user.Lookup(s.RunAsUser)
		if err != nil {
			return nil, err
		}
		cmd = exec.Command("su", "-", u.Username)
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "OPENSSM_SESSION_ID="+s.SessionID)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 200, Rows: 50})
	if err != nil {
		return nil, err
	}
	return &ptyProcess{cmd: cmd, tty: tty}, nil
}
