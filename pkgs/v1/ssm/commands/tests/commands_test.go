package commands_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/agenthub"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/commands"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

var ctx = context.Background()

type observer struct {
	mu          sync.Mutex
	commands    int
	invocations map[string]int
}

func (o *observer) IncCommand(string) { o.mu.Lock(); o.commands++; o.mu.Unlock() }
func (o *observer) IncInvocation(s string) {
	o.mu.Lock()
	o.invocations[s]++
	o.mu.Unlock()
}

type env struct {
	svc  *commands.Service
	inst *instances.Service
	docs *documents.Service
	tags *tags.Service
	hub  *agenthub.Hub
	obs  *observer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := memory.New()
	tg := tags.New(store)
	docs, err := documents.New(store, tg, types.Account{AccountID: "1", Region: "r"})
	require.NoError(t, err)
	inst := instances.New(store, tg, instances.Options{})
	hub := agenthub.New()
	obs := &observer{invocations: map[string]int{}}
	return &env{svc: commands.New(store, docs, inst, hub, obs), inst: inst, docs: docs, tags: tg, hub: hub, obs: obs}
}

func (e *env) newInstance(t *testing.T) string {
	t.Helper()
	act, err := e.inst.CreateActivation(ctx, &instances.CreateActivationInput{IamRole: "r"})
	require.NoError(t, err)
	reg, err := e.inst.Register(ctx, &agentproto.RegisterRequest{ActivationID: act.ActivationID, ActivationCode: act.ActivationCode, AgentInfo: agentproto.AgentInfo{ComputerName: "h"}})
	require.NoError(t, err)
	return reg.InstanceID
}

func (e *env) send(t *testing.T, ids []string, cmds ...string) *commands.Command {
	t.Helper()
	out, err := e.svc.Send(ctx, &commands.SendCommandInput{
		DocumentName: documents.DocRunShellScript, InstanceIDs: ids, Comment: "test",
		Parameters: map[string][]string{"commands": cmds},
	})
	require.NoError(t, err)
	return out.Command
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, code, types.AsSSMError(err).Code(), err.Error())
}

func success(stdout string) *agentproto.CommandResult {
	now := time.Now()
	return &agentproto.CommandResult{Status: "Success", Plugins: []agentproto.PluginResult{{Name: "runShellScript", Status: "Success", ResponseCode: 0, Stdout: stdout, StartTime: now, EndTime: now.Add(1500 * time.Millisecond)}}}
}

func TestSendClaimReportLifecycle(t *testing.T) {
	e := newEnv(t)
	iid := e.newInstance(t)
	wake := e.hub.Subscribe(iid)

	cmd := e.send(t, []string{iid}, "echo hi", "uptime")
	assert.Equal(t, "Pending", cmd.Status)
	assert.Equal(t, 1, cmd.TargetCount)
	assert.Equal(t, []string{"3600"}, cmd.Parameters["executionTimeout"])
	select {
	case <-wake:
	default:
		t.Fatal("agent was not notified")
	}

	has, err := e.svc.HasWork(iid)
	require.NoError(t, err)
	assert.True(t, has)

	payloads, cancels, err := e.svc.Claim(iid)
	require.NoError(t, err)
	assert.Empty(t, cancels)
	require.Len(t, payloads, 1)
	p := payloads[0]
	assert.Equal(t, cmd.CommandID, p.CommandID)
	require.Len(t, p.Steps, 1)
	assert.Equal(t, "aws:runShellScript", p.Steps[0].Action)
	assert.Equal(t, []any{"echo hi", "uptime"}, p.Steps[0].Inputs["runCommand"])
	assert.Equal(t, 3600, p.Steps[0].TimeoutSeconds)

	again, _, err := e.svc.Claim(iid)
	require.NoError(t, err)
	assert.Empty(t, again, "work is delivered once")

	inv, err := e.svc.GetInvocation(ctx, &commands.GetCommandInvocationInput{CommandID: cmd.CommandID, InstanceID: iid})
	require.NoError(t, err)
	assert.Equal(t, "InProgress", inv.Status)
	assert.Equal(t, -1, inv.ResponseCode)

	require.NoError(t, e.svc.Report(iid, cmd.CommandID, success("hi\n")))
	inv, err = e.svc.GetInvocation(ctx, &commands.GetCommandInvocationInput{CommandID: cmd.CommandID, InstanceID: iid})
	require.NoError(t, err)
	assert.Equal(t, "Success", inv.Status)
	assert.Equal(t, 0, inv.ResponseCode)
	assert.Equal(t, "hi\n", inv.StandardOutputContent)
	assert.Equal(t, "runShellScript", inv.PluginName)
	assert.Equal(t, "PT1.500S", inv.ExecutionElapsedTime)
	assert.NotEmpty(t, inv.ExecutionStartDateTime)

	list, err := e.svc.List(ctx, &commands.ListCommandsInput{CommandID: cmd.CommandID})
	require.NoError(t, err)
	assert.Equal(t, "Success", list.Commands[0].Status)
	assert.Equal(t, 1, list.Commands[0].CompletedCount)

	invs, err := e.svc.ListInvocations(ctx, &commands.ListCommandInvocationsInput{CommandID: cmd.CommandID, Details: true})
	require.NoError(t, err)
	require.Len(t, invs.CommandInvocations, 1)
	require.Len(t, invs.CommandInvocations[0].CommandPlugins, 1)
	assert.Equal(t, "hi\n", invs.CommandInvocations[0].CommandPlugins[0].Output)

	// Late reports after completion are ignored.
	require.NoError(t, e.svc.Report(iid, cmd.CommandID, &agentproto.CommandResult{Status: "Failed"}))
	assert.Equal(t, 1, e.obs.commands)
	assert.Equal(t, 1, e.obs.invocations["Success"])
}

func TestAggregateStatusAcrossInstances(t *testing.T) {
	e := newEnv(t)
	a, b := e.newInstance(t), e.newInstance(t)
	cmd := e.send(t, []string{a, b}, "x")
	_, _, err := e.svc.Claim(a)
	require.NoError(t, err)
	_, _, err = e.svc.Claim(b)
	require.NoError(t, err)

	require.NoError(t, e.svc.Report(a, cmd.CommandID, success("")))
	list, _ := e.svc.List(ctx, &commands.ListCommandsInput{CommandID: cmd.CommandID})
	assert.Equal(t, "InProgress", list.Commands[0].Status)

	require.NoError(t, e.svc.Report(b, cmd.CommandID, &agentproto.CommandResult{Status: "Failed", Plugins: []agentproto.PluginResult{{Name: "runShellScript", Status: "Failed", ResponseCode: 2, Stderr: "boom"}}}))
	list, _ = e.svc.List(ctx, &commands.ListCommandsInput{CommandID: cmd.CommandID})
	c := list.Commands[0]
	assert.Equal(t, "Failed", c.Status)
	assert.Equal(t, 2, c.CompletedCount)
	assert.Equal(t, 1, c.ErrorCount)

	inv, err := e.svc.GetInvocation(ctx, &commands.GetCommandInvocationInput{CommandID: cmd.CommandID, InstanceID: b})
	require.NoError(t, err)
	assert.Equal(t, 2, inv.ResponseCode)
	assert.Equal(t, "boom", inv.StandardErrorContent)
}

func TestSendValidation(t *testing.T) {
	e := newEnv(t)
	iid := e.newInstance(t)
	cases := []struct {
		in   commands.SendCommandInput
		code string
	}{
		{commands.SendCommandInput{InstanceIDs: []string{iid}}, types.ErrValidation},
		{commands.SendCommandInput{DocumentName: documents.DocRunShellScript}, types.ErrValidation},
		{commands.SendCommandInput{DocumentName: "Missing", InstanceIDs: []string{iid}}, types.ErrInvalidDocument},
		{commands.SendCommandInput{DocumentName: documents.DocSessionShell, InstanceIDs: []string{iid}}, types.ErrInvalidDocument},
		{commands.SendCommandInput{DocumentName: documents.DocRunShellScript, InstanceIDs: []string{iid}}, types.ErrInvalidParameters},
		{commands.SendCommandInput{DocumentName: documents.DocRunShellScript, InstanceIDs: []string{"mi-00000000000000000"}, Parameters: map[string][]string{"commands": {"x"}}}, types.ErrInvalidInstanceID},
		{commands.SendCommandInput{DocumentName: documents.DocRunShellScript, InstanceIDs: []string{iid}, TimeoutSeconds: 5, Parameters: map[string][]string{"commands": {"x"}}}, types.ErrValidation},
		{commands.SendCommandInput{DocumentName: documents.DocRunShellScript, InstanceIDs: []string{iid}, MaxConcurrency: "abc", Parameters: map[string][]string{"commands": {"x"}}}, types.ErrValidation},
		{commands.SendCommandInput{DocumentName: documents.DocRunShellScript, InstanceIDs: []string{iid}, DocumentHash: "nope", Parameters: map[string][]string{"commands": {"x"}}}, "InvalidDocumentHash"},
	}
	for _, c := range cases {
		_, err := e.svc.Send(ctx, &c.in)
		assertCode(t, err, c.code)
	}
}

func TestSendByTagTargets(t *testing.T) {
	e := newEnv(t)
	a, b := e.newInstance(t), e.newInstance(t)
	require.NoError(t, e.tags.Add(tags.ResourceManagedInstance, a, []types.Tag{{Key: "role", Value: "web"}}))
	out, err := e.svc.Send(ctx, &commands.SendCommandInput{
		DocumentName: documents.DocRunShellScript, Targets: []types.Target{{Key: "tag:role", Values: []string{"web"}}},
		Parameters: map[string][]string{"commands": {"x"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{a}, out.Command.InstanceIDs)
	assert.Equal(t, 1, out.Command.TargetCount)
	payloads, _, _ := e.svc.Claim(b)
	assert.Empty(t, payloads)
}

func TestCancelUndeliveredAndDelivered(t *testing.T) {
	e := newEnv(t)
	a, b := e.newInstance(t), e.newInstance(t)
	cmd := e.send(t, []string{a, b}, "sleep 100")
	_, _, err := e.svc.Claim(a) // a delivered, b still queued
	require.NoError(t, err)

	require.NoError(t, e.svc.Cancel(ctx, &commands.CancelCommandInput{CommandID: cmd.CommandID}))
	list, _ := e.svc.List(ctx, &commands.ListCommandsInput{CommandID: cmd.CommandID})
	assert.Equal(t, "Cancelling", list.Commands[0].Status)

	payloads, _, _ := e.svc.Claim(b)
	assert.Empty(t, payloads, "undelivered invocation was cancelled immediately")

	_, cancels, err := e.svc.Claim(a)
	require.NoError(t, err)
	assert.Equal(t, []string{cmd.CommandID}, cancels)
	_, cancels, _ = e.svc.Claim(a)
	assert.Empty(t, cancels, "cancellation is delivered once")

	require.NoError(t, e.svc.Report(a, cmd.CommandID, &agentproto.CommandResult{Status: "Cancelled"}))
	list, _ = e.svc.List(ctx, &commands.ListCommandsInput{CommandID: cmd.CommandID})
	assert.Equal(t, "Cancelled", list.Commands[0].Status)

	assertCode(t, e.svc.Cancel(ctx, &commands.CancelCommandInput{CommandID: "nope"}), types.ErrInvalidCommandID)
}

func TestSweepDeliveryAndExecutionTimeouts(t *testing.T) {
	e := newEnv(t)
	a, b := e.newInstance(t), e.newInstance(t)
	out, err := e.svc.Send(ctx, &commands.SendCommandInput{
		DocumentName: documents.DocRunShellScript, InstanceIDs: []string{a, b}, TimeoutSeconds: 60,
		Parameters: map[string][]string{"commands": {"x"}, "executionTimeout": {"30"}},
	})
	require.NoError(t, err)
	_, _, err = e.svc.Claim(a)
	require.NoError(t, err)

	types.SetNowFunc(func() time.Time { return time.Now().Add(2 * time.Minute) })
	t.Cleanup(func() { types.SetNowFunc(nil) })
	n, err := e.svc.Sweep()
	require.NoError(t, err)
	assert.Equal(t, 1, n, "undelivered invocation times out")

	inv, err := e.svc.GetInvocation(ctx, &commands.GetCommandInvocationInput{CommandID: out.Command.CommandID, InstanceID: b})
	require.NoError(t, err)
	assert.Equal(t, "TimedOut", inv.Status)
	assert.Equal(t, "DeliveryTimedOut", inv.StatusDetails)

	types.SetNowFunc(func() time.Time { return time.Now().Add(10 * time.Minute) })
	n, err = e.svc.Sweep()
	require.NoError(t, err)
	assert.Equal(t, 1, n, "lost agent execution times out")

	list, _ := e.svc.List(ctx, &commands.ListCommandsInput{CommandID: out.Command.CommandID})
	assert.Equal(t, "TimedOut", list.Commands[0].Status)
	assert.Equal(t, 1, list.Commands[0].DeliveryTimedOutCount)
}

func TestGetInvocationErrors(t *testing.T) {
	e := newEnv(t)
	iid := e.newInstance(t)
	cmd := e.send(t, []string{iid}, "x")
	_, err := e.svc.GetInvocation(ctx, &commands.GetCommandInvocationInput{CommandID: "nope", InstanceID: iid})
	assertCode(t, err, types.ErrInvalidCommandID)
	_, err = e.svc.GetInvocation(ctx, &commands.GetCommandInvocationInput{CommandID: cmd.CommandID, InstanceID: "mi-00000000000000000"})
	assertCode(t, err, types.ErrInvocationDoesNotExist)
	_, err = e.svc.GetInvocation(ctx, &commands.GetCommandInvocationInput{CommandID: cmd.CommandID, InstanceID: iid, PluginName: "nope"})
	assertCode(t, err, "InvalidPluginName")
}

func TestListFiltersAndOrder(t *testing.T) {
	e := newEnv(t)
	iid := e.newInstance(t)
	first := e.send(t, []string{iid}, "a")
	time.Sleep(5 * time.Millisecond)
	second := e.send(t, []string{iid}, "b")

	list, err := e.svc.List(ctx, &commands.ListCommandsInput{InstanceID: iid})
	require.NoError(t, err)
	require.Len(t, list.Commands, 2)
	assert.Equal(t, second.CommandID, list.Commands[0].CommandID, "newest first")
	assert.Equal(t, first.CommandID, list.Commands[1].CommandID)

	list, err = e.svc.List(ctx, &commands.ListCommandsInput{Filters: []commands.CommandFilter{{Key: "Status", Value: "Pending"}}})
	require.NoError(t, err)
	assert.Len(t, list.Commands, 2)
	list, err = e.svc.List(ctx, &commands.ListCommandsInput{Filters: []commands.CommandFilter{{Key: "DocumentName", Value: "Other"}}})
	require.NoError(t, err)
	assert.Empty(t, list.Commands)
	_, err = e.svc.List(ctx, &commands.ListCommandsInput{Filters: []commands.CommandFilter{{Key: "Bad", Value: "x"}}})
	assertCode(t, err, types.ErrInvalidFilterKey)
	_, err = e.svc.List(ctx, &commands.ListCommandsInput{CommandID: "nope"})
	assertCode(t, err, types.ErrInvalidCommandID)
}
