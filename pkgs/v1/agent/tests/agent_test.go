package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/apps/go/server/agentapi"
	"github.com/tguidoux/openssm/pkgs/v1/agent"
	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/agenthub"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/commands"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

var log = logger.New("agent-test", logger.UncontextualLoggerType)

func TestExecutorSuccessFailureAndTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell executor test requires /bin/sh")
	}
	e := &agent.Executor{}
	res := e.Run(context.Background(), agentproto.CommandPayload{Steps: []agentproto.StepPayload{
		{Name: "ok", Action: "aws:runShellScript", Inputs: map[string]any{"runCommand": []any{"echo hello", "echo err >&2"}}, TimeoutSeconds: 10},
	}})
	require.Len(t, res.Plugins, 1)
	assert.Equal(t, "Success", res.Status)
	assert.Equal(t, "hello\n", res.Plugins[0].Stdout)
	assert.Equal(t, "err\n", res.Plugins[0].Stderr)
	assert.Equal(t, 0, res.Plugins[0].ResponseCode)

	res = e.Run(context.Background(), agentproto.CommandPayload{Steps: []agentproto.StepPayload{
		{Name: "fail", Action: "aws:runShellScript", Inputs: map[string]any{"runCommand": []any{"exit 3"}}, TimeoutSeconds: 10},
	}})
	assert.Equal(t, "Failed", res.Status)
	assert.Equal(t, 3, res.Plugins[0].ResponseCode)

	start := time.Now()
	res = e.Run(context.Background(), agentproto.CommandPayload{Steps: []agentproto.StepPayload{
		{Name: "slow", Action: "aws:runShellScript", Inputs: map[string]any{"runCommand": []any{"sleep 30"}}, TimeoutSeconds: 1},
	}})
	assert.Equal(t, "TimedOut", res.Status)
	assert.Less(t, time.Since(start), 10*time.Second)

	res = e.Run(context.Background(), agentproto.CommandPayload{Steps: []agentproto.StepPayload{
		{Name: "bad", Action: "aws:unknownPlugin", Inputs: map[string]any{}},
	}})
	assert.Equal(t, "Failed", res.Status)

	dir := t.TempDir()
	res = e.Run(context.Background(), agentproto.CommandPayload{Steps: []agentproto.StepPayload{
		{Name: "cwd", Action: "aws:runShellScript", Inputs: map[string]any{"runCommand": []any{"pwd"}, "workingDirectory": dir}, TimeoutSeconds: 5},
	}})
	resolved, _ := filepath.EvalSymlinks(dir)
	assert.Contains(t, []string{dir, resolved}, strings.TrimSpace(res.Plugins[0].Stdout))
}

func TestExecutorPreconditionSkips(t *testing.T) {
	e := &agent.Executor{}
	other := "Windows"
	if agent.PlatformType() == "Windows" {
		other = "Linux"
	}
	res := e.Run(context.Background(), agentproto.CommandPayload{Steps: []agentproto.StepPayload{
		{Name: "skip", Action: "aws:runShellScript", Precondition: map[string]any{"StringEquals": []any{"platformType", other}}, Inputs: map[string]any{"runCommand": []any{"exit 1"}}},
	}})
	assert.Equal(t, "Success", res.Status)
	assert.Contains(t, res.Plugins[0].Stdout, "skipped")
}

type stack struct {
	server *httptest.Server
	inst   *instances.Service
	cmds   *commands.Service
}

func newStack(t *testing.T) *stack {
	t.Helper()
	store := memory.New()
	tg := tags.New(store)
	docs, err := documents.New(store, tg, types.Account{AccountID: "1", Region: "r"})
	require.NoError(t, err)
	inst := instances.New(store, tg, instances.Options{})
	hub := agenthub.New()
	cmds := commands.New(store, docs, inst, hub, nil)
	mux := http.NewServeMux()
	mux.Handle(agentproto.PathPrefix, agentapi.New(inst, cmds, nil, hub, log))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &stack{server: srv, inst: inst, cmds: cmds}
}

func (s *stack) startAgent(t *testing.T, stateFile string) (*agent.Agent, context.CancelFunc) {
	t.Helper()
	act, err := s.inst.CreateActivation(context.Background(), &instances.CreateActivationInput{IamRole: "r", RegistrationLimit: 2})
	require.NoError(t, err)
	a := agent.New(agent.Config{
		ServerURL: s.server.URL, ActivationID: act.ActivationID, ActivationCode: act.ActivationCode,
		StateFile: stateFile, PollWait: 2 * time.Second, HeartbeatInterval: time.Second,
	}, log)
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, a.Register(ctx))
	done := make(chan struct{})
	go func() { _ = a.Run(ctx); close(done) }()
	return a, func() { cancel(); <-done }
}

func waitStatus(t *testing.T, cmds *commands.Service, commandID, instanceID string, want ...string) *commands.GetCommandInvocationOutput {
	t.Helper()
	var last *commands.GetCommandInvocationOutput
	require.Eventually(t, func() bool {
		out, err := cmds.GetInvocation(context.Background(), &commands.GetCommandInvocationInput{CommandID: commandID, InstanceID: instanceID})
		if err != nil {
			return false
		}
		last = out
		for _, w := range want {
			if out.Status == w {
				return true
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond)
	return last
}

func TestAgentEndToEndRunCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires /bin/sh")
	}
	s := newStack(t)
	stateFile := filepath.Join(t.TempDir(), "agent.json")
	a, stop := s.startAgent(t, stateFile)
	defer stop()

	info, err := s.inst.DescribeInstanceInformation(context.Background(), &instances.DescribeInstanceInformationInput{})
	require.NoError(t, err)
	require.Len(t, info.InstanceInformationList, 1)
	assert.Equal(t, a.InstanceID(), info.InstanceInformationList[0].InstanceID)
	assert.Equal(t, "Online", info.InstanceInformationList[0].PingStatus)
	assert.Equal(t, agent.Version, info.InstanceInformationList[0].AgentVersion)

	out, err := s.cmds.Send(context.Background(), &commands.SendCommandInput{
		DocumentName: documents.DocRunShellScript, InstanceIDs: []string{a.InstanceID()},
		Parameters: map[string][]string{"commands": {"echo from-agent", "exit 0"}},
	})
	require.NoError(t, err)
	inv := waitStatus(t, s.cmds, out.Command.CommandID, a.InstanceID(), "Success", "Failed")
	assert.Equal(t, "Success", inv.Status)
	assert.Equal(t, "from-agent\n", inv.StandardOutputContent)

	// Cancellation of a running command.
	out, err = s.cmds.Send(context.Background(), &commands.SendCommandInput{
		DocumentName: documents.DocRunShellScript, InstanceIDs: []string{a.InstanceID()},
		Parameters: map[string][]string{"commands": {"sleep 60"}},
	})
	require.NoError(t, err)
	waitStatus(t, s.cmds, out.Command.CommandID, a.InstanceID(), "InProgress")
	require.NoError(t, s.cmds.Cancel(context.Background(), &commands.CancelCommandInput{CommandID: out.Command.CommandID}))
	inv = waitStatus(t, s.cmds, out.Command.CommandID, a.InstanceID(), "Cancelled")
	assert.Equal(t, "Cancelled", inv.Status)
}

func TestAgentResumesFromStateFile(t *testing.T) {
	s := newStack(t)
	stateFile := filepath.Join(t.TempDir(), "agent.json")
	a, stop := s.startAgent(t, stateFile)
	first := a.InstanceID()
	stop()

	// A new agent without activation reuses the stored identity.
	b := agent.New(agent.Config{ServerURL: s.server.URL, StateFile: stateFile}, log)
	require.NoError(t, b.Register(context.Background()))
	assert.Equal(t, first, b.InstanceID())

	// After deregistration the stored token is rejected.
	require.NoError(t, s.inst.Deregister(context.Background(), &instances.DeregisterManagedInstanceInput{InstanceID: first}))
	c := agent.New(agent.Config{ServerURL: s.server.URL, StateFile: stateFile}, log)
	assert.Error(t, c.Register(context.Background()))
}

func TestAgentAPIRejectsBadTokens(t *testing.T) {
	s := newStack(t)
	client := agent.NewClient(s.server.URL, "mi-00000000000000000.bad", nil)
	_, err := client.Poll(context.Background(), time.Second)
	var apiErr *agent.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.Status)

	_, err = client.Register(context.Background(), &agentproto.RegisterRequest{ActivationID: "x", ActivationCode: "y"})
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "InvalidActivation", apiErr.Code)
}
