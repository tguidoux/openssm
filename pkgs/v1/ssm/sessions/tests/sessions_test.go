package sessions_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/agenthub"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/sessions"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

var ctx = context.Background()

type env struct {
	svc  *sessions.Service
	inst *instances.Service
	docs *documents.Service
	hub  *agenthub.Hub
	iid  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := memory.New()
	tg := tags.New(store)
	account := types.Account{AccountID: "123456789012", Region: "us-east-1"}
	docs, err := documents.New(store, tg, account)
	require.NoError(t, err)
	inst := instances.New(store, tg, instances.Options{PingTimeout: time.Minute})
	hub := agenthub.New()
	svc := sessions.New(store, docs, inst, hub, account, sessions.Options{
		StreamURL: func(id string) string { return "ws://ssmmessages.us-east-1.localhost/v1/data-channel/" + id },
	}, nil)
	act, err := inst.CreateActivation(ctx, &instances.CreateActivationInput{IamRole: "r"})
	require.NoError(t, err)
	reg, err := inst.Register(ctx, &agentproto.RegisterRequest{ActivationID: act.ActivationID, ActivationCode: act.ActivationCode})
	require.NoError(t, err)
	return &env{svc: svc, inst: inst, docs: docs, hub: hub, iid: reg.InstanceID}
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, code, types.AsSSMError(err).Code(), err.Error())
}

func TestStartSessionQueuesForAgent(t *testing.T) {
	e := newEnv(t)
	wake := e.hub.Subscribe(e.iid)
	c := types.WithCaller(ctx, types.Caller{Name: "alice"})
	out, err := e.svc.Start(c, &sessions.StartSessionInput{Target: e.iid, Reason: "debug"})
	require.NoError(t, err)
	assert.Regexp(t, `^alice-[0-9a-f]{17}$`, out.SessionID)
	assert.Equal(t, "ws://ssmmessages.us-east-1.localhost/v1/data-channel/"+out.SessionID, out.StreamURL)
	assert.NotEmpty(t, out.TokenValue)
	select {
	case <-wake:
	default:
		t.Fatal("agent not notified")
	}

	pending, err := e.svc.PendingFor(e.iid)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, out.SessionID, pending[0].SessionID)
	assert.Equal(t, "Standard_Stream", pending[0].SessionType)
	assert.Equal(t, "20", pending[0].Parameters["idleSessionTimeout"])
	again, _ := e.svc.PendingFor(e.iid)
	assert.Empty(t, again)

	require.NoError(t, e.svc.AuthorizeClient(out.SessionID, out.TokenValue))
	assert.ErrorIs(t, e.svc.AuthorizeClient(out.SessionID, "bad"), sessions.ErrInvalidToken)
	require.NoError(t, e.svc.AuthorizeAgent(out.SessionID, e.iid))
	assert.ErrorIs(t, e.svc.AuthorizeAgent(out.SessionID, "mi-other"), sessions.ErrInvalidToken)

	active, err := e.svc.Describe(ctx, &sessions.DescribeSessionsInput{State: "Active"})
	require.NoError(t, err)
	require.Len(t, active.Sessions, 1)
	s := active.Sessions[0]
	assert.Equal(t, "Connecting", s.Status)
	assert.Equal(t, "arn:aws:iam::123456789012:user/alice", s.Owner)
	assert.Equal(t, "debug", s.Reason)
	assert.Equal(t, documents.DocSessionShell, s.DocumentName)
}

func TestStartSessionValidation(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Start(ctx, &sessions.StartSessionInput{})
	assertCode(t, err, types.ErrValidation)
	_, err = e.svc.Start(ctx, &sessions.StartSessionInput{Target: "mi-00000000000000000"})
	assertCode(t, err, types.ErrInvalidInstanceID)
	_, err = e.svc.Start(ctx, &sessions.StartSessionInput{Target: e.iid, DocumentName: documents.DocRunShellScript})
	assertCode(t, err, types.ErrInvalidDocument)
	_, err = e.svc.Start(ctx, &sessions.StartSessionInput{Target: e.iid, DocumentName: documents.DocStartInteractive})
	assertCode(t, err, types.ErrInvalidParameters)

	types.SetNowFunc(func() time.Time { return time.Now().Add(10 * time.Minute) })
	t.Cleanup(func() { types.SetNowFunc(nil) })
	_, err = e.svc.Start(ctx, &sessions.StartSessionInput{Target: e.iid})
	assertCode(t, err, types.ErrTargetNotConnected)
}

func TestInteractiveCommandDocument(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Start(ctx, &sessions.StartSessionInput{Target: e.iid, DocumentName: documents.DocStartInteractive, Parameters: map[string][]string{"command": {"top"}}})
	require.NoError(t, err)
	pending, err := e.svc.PendingFor(e.iid)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "InteractiveCommands", pending[0].SessionType)
	assert.Equal(t, "top", pending[0].Command)
}

func TestTerminateResumeAndHistory(t *testing.T) {
	e := newEnv(t)
	out, err := e.svc.Start(ctx, &sessions.StartSessionInput{Target: e.iid})
	require.NoError(t, err)
	var hooked string
	e.svc.OnTerminate(func(id string) { hooked = id })

	res, err := e.svc.Resume(ctx, &sessions.ResumeSessionInput{SessionID: out.SessionID})
	require.NoError(t, err)
	assert.NotEqual(t, out.TokenValue, res.TokenValue)
	assert.ErrorIs(t, e.svc.AuthorizeClient(out.SessionID, out.TokenValue), sessions.ErrInvalidToken, "old token is revoked")
	require.NoError(t, e.svc.AuthorizeClient(out.SessionID, res.TokenValue))

	term, err := e.svc.Terminate(ctx, &sessions.TerminateSessionInput{SessionID: out.SessionID})
	require.NoError(t, err)
	assert.Equal(t, out.SessionID, term.SessionID)
	assert.Equal(t, out.SessionID, hooked)
	_, err = e.svc.Terminate(ctx, &sessions.TerminateSessionInput{SessionID: out.SessionID})
	require.NoError(t, err, "terminate is idempotent")

	_, err = e.svc.Resume(ctx, &sessions.ResumeSessionInput{SessionID: out.SessionID})
	assertCode(t, err, types.ErrDoesNotExist)
	_, err = e.svc.Terminate(ctx, &sessions.TerminateSessionInput{SessionID: "nope"})
	assertCode(t, err, types.ErrDoesNotExist)

	hist, err := e.svc.Describe(ctx, &sessions.DescribeSessionsInput{State: "History"})
	require.NoError(t, err)
	require.Len(t, hist.Sessions, 1)
	assert.Equal(t, "Terminated", hist.Sessions[0].Status)
	assert.NotNil(t, hist.Sessions[0].EndDate)

	pending, _ := e.svc.PendingFor(e.iid)
	assert.Empty(t, pending, "terminated sessions are not delivered")

	_, err = e.svc.Describe(ctx, &sessions.DescribeSessionsInput{State: "Bogus"})
	assertCode(t, err, types.ErrValidation)
	filtered, err := e.svc.Describe(ctx, &sessions.DescribeSessionsInput{State: "History", Filters: []sessions.SessionFilter{{Key: "Target", Value: "mi-x"}}})
	require.NoError(t, err)
	assert.Empty(t, filtered.Sessions)
	_, err = e.svc.Describe(ctx, &sessions.DescribeSessionsInput{State: "History", Filters: []sessions.SessionFilter{{Key: "Bad", Value: "x"}}})
	assertCode(t, err, types.ErrInvalidFilterKey)
}

func TestSweepFailsStaleConnectingSessions(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Start(ctx, &sessions.StartSessionInput{Target: e.iid})
	require.NoError(t, err)
	types.SetNowFunc(func() time.Time { return time.Now().Add(10 * time.Minute) })
	t.Cleanup(func() { types.SetNowFunc(nil) })
	n, err := e.svc.Sweep()
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	hist, _ := e.svc.Describe(ctx, &sessions.DescribeSessionsInput{State: "History"})
	require.Len(t, hist.Sessions, 1)
	assert.Equal(t, "Failed", hist.Sessions[0].Status)
}
