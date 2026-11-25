package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
)

// maxCapturedOutput bounds stdout/stderr kept per step (AWS returns 24000
// characters through the API).
const maxCapturedOutput = 48000

// limitedBuffer keeps at most limit bytes.
type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Executor runs command document steps on the local host.
type Executor struct {
	// Shell is the interpreter for aws:runShellScript (default /bin/sh).
	Shell string
	// PowerShell is the interpreter for aws:runPowerShellScript (default pwsh).
	PowerShell string
	// WorkDir is the default working directory for steps.
	WorkDir string
}

// Run executes every step of the command. ctx cancellation (CancelCommand)
// stops the current step and marks the rest Cancelled.
func (e *Executor) Run(ctx context.Context, cmd agentproto.CommandPayload) agentproto.CommandResult {
	res := agentproto.CommandResult{Status: agentproto.StatusSuccess}
	for _, step := range cmd.Steps {
		if ctx.Err() != nil {
			now := time.Now().UTC()
			res.Plugins = append(res.Plugins, agentproto.PluginResult{Name: step.Name, Status: agentproto.StatusCancelled, ResponseCode: -1, StartTime: now, EndTime: now})
			res.Status = agentproto.StatusCancelled
			continue
		}
		pr := e.runStep(ctx, step)
		res.Plugins = append(res.Plugins, pr)
		switch pr.Status {
		case agentproto.StatusFailed:
			if res.Status == agentproto.StatusSuccess {
				res.Status = agentproto.StatusFailed
			}
		case agentproto.StatusTimedOut:
			res.Status = agentproto.StatusTimedOut
		case agentproto.StatusCancelled:
			res.Status = agentproto.StatusCancelled
		}
	}
	return res
}

func (e *Executor) runStep(ctx context.Context, step agentproto.StepPayload) agentproto.PluginResult {
	pr := agentproto.PluginResult{Name: step.Name, StartTime: time.Now().UTC(), ResponseCode: -1}
	defer func() { pr.EndTime = time.Now().UTC() }()

	if skip, reason := e.skipByPrecondition(step.Precondition); skip {
		pr.Status, pr.ResponseCode, pr.Stdout = agentproto.StatusSuccess, 0, reason
		return pr
	}

	interpreter, args, ext := e.interpreter(step.Action)
	if interpreter == "" {
		pr.Status = agentproto.StatusFailed
		pr.Stderr = fmt.Sprintf("openssm-agent does not support plugin %q", step.Action)
		return pr
	}
	script := strings.Join(toStrings(step.Inputs["runCommand"]), "\n")
	if strings.TrimSpace(script) == "" {
		pr.Status = agentproto.StatusFailed
		pr.Stderr = "runCommand is empty"
		return pr
	}
	f, err := os.CreateTemp("", "openssm-*"+ext)
	if err != nil {
		pr.Status, pr.Stderr = agentproto.StatusFailed, err.Error()
		return pr
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script + "\n"); err != nil {
		f.Close()
		pr.Status, pr.Stderr = agentproto.StatusFailed, err.Error()
		return pr
	}
	f.Close()

	timeout := time.Duration(step.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Hour
	}
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(stepCtx, interpreter, append(args, f.Name())...)
	c.Dir = fmt.Sprint(step.Inputs["workingDirectory"])
	if c.Dir == "" || c.Dir == "<nil>" {
		c.Dir = e.WorkDir
	}
	configureProcess(c)
	stdout := &limitedBuffer{limit: maxCapturedOutput}
	stderr := &limitedBuffer{limit: maxCapturedOutput}
	c.Stdout, c.Stderr = stdout, stderr
	c.WaitDelay = 5 * time.Second
	err = c.Run()
	pr.Stdout, pr.Stderr = stdout.String(), stderr.String()

	switch {
	case errors.Is(stepCtx.Err(), context.DeadlineExceeded):
		pr.Status = agentproto.StatusTimedOut
		pr.Stderr += "\nfailed to run commands: exit status timed out"
	case ctx.Err() != nil:
		pr.Status = agentproto.StatusCancelled
	case err == nil:
		pr.Status, pr.ResponseCode = agentproto.StatusSuccess, 0
	default:
		pr.Status = agentproto.StatusFailed
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			pr.ResponseCode = exitErr.ExitCode()
			pr.Stderr += fmt.Sprintf("\nfailed to run commands: exit status %d", pr.ResponseCode)
		} else {
			pr.Stderr += "\n" + err.Error()
		}
	}
	return pr
}

func (e *Executor) interpreter(action string) (string, []string, string) {
	switch action {
	case "aws:runShellScript":
		shell := e.Shell
		if shell == "" {
			shell = "/bin/sh"
		}
		return shell, nil, ".sh"
	case "aws:runPowerShellScript":
		ps := e.PowerShell
		if ps == "" {
			ps = "pwsh"
			if runtime.GOOS == "windows" {
				ps = "powershell"
			}
		}
		return ps, []string{"-NoProfile", "-NonInteractive", "-File"}, ".ps1"
	}
	return "", nil, ""
}

// skipByPrecondition evaluates {"StringEquals": ["platformType", "Linux"]}.
func (e *Executor) skipByPrecondition(pre map[string]any) (bool, string) {
	eq, ok := pre["StringEquals"].([]any)
	if !ok || len(eq) != 2 || fmt.Sprint(eq[0]) != "platformType" {
		return false, ""
	}
	want := fmt.Sprint(eq[1])
	if !strings.EqualFold(want, PlatformType()) {
		return true, fmt.Sprintf("Step execution skipped due to unsatisfied preconditions: '\"StringEquals\": [platformType, %s]'.", want)
	}
	return false, ""
}

func toStrings(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			out = append(out, fmt.Sprint(item))
		}
		return out
	case []string:
		return t
	case string:
		return []string{t}
	case nil:
		return nil
	default:
		return []string{fmt.Sprint(t)}
	}
}
