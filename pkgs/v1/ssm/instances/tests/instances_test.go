package instances_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

var ctx = context.Background()

type env struct {
	store kv.Store
	svc   *instances.Service
	tags  *tags.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := memory.New()
	tg := tags.New(store)
	return &env{store: store, svc: instances.New(store, tg, instances.Options{PingTimeout: time.Minute, LatestAgentVersion: "1.0"}), tags: tg}
}

func (e *env) activate(t *testing.T, limit int) *instances.CreateActivationOutput {
	t.Helper()
	out, err := e.svc.CreateActivation(ctx, &instances.CreateActivationInput{IamRole: "role", RegistrationLimit: limit, DefaultInstanceName: "box"})
	require.NoError(t, err)
	return out
}

func (e *env) register(t *testing.T, act *instances.CreateActivationOutput) *agentproto.RegisterResponse {
	t.Helper()
	out, err := e.svc.Register(ctx, &agentproto.RegisterRequest{
		ActivationID: act.ActivationID, ActivationCode: act.ActivationCode,
		AgentInfo: agentproto.AgentInfo{ComputerName: "host1", PlatformType: "Linux", AgentVersion: "1.0", IPAddress: "10.0.0.1"},
	})
	require.NoError(t, err)
	return out
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, code, types.AsSSMError(err).Code(), err.Error())
}

func TestActivationLifecycle(t *testing.T) {
	e := newEnv(t)
	act := e.activate(t, 2)
	assert.NotEmpty(t, act.ActivationCode)

	list, err := e.svc.DescribeActivations(ctx, &instances.DescribeActivationsInput{})
	require.NoError(t, err)
	require.Len(t, list.ActivationList, 1)
	a := list.ActivationList[0]
	assert.Equal(t, 2, a.RegistrationLimit)
	assert.False(t, a.Expired)

	filtered, err := e.svc.DescribeActivations(ctx, &instances.DescribeActivationsInput{Filters: []instances.DescribeActivationsFilter{{FilterKey: "ActivationIds", FilterValues: []string{"nope"}}}})
	require.NoError(t, err)
	assert.Empty(t, filtered.ActivationList)

	require.NoError(t, e.svc.DeleteActivation(ctx, &instances.DeleteActivationInput{ActivationID: act.ActivationID}))
	assertCode(t, e.svc.DeleteActivation(ctx, &instances.DeleteActivationInput{ActivationID: act.ActivationID}), types.ErrInvalidActivationID)
}

func TestCreateActivationValidation(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.CreateActivation(ctx, &instances.CreateActivationInput{})
	assertCode(t, err, types.ErrValidation)
	_, err = e.svc.CreateActivation(ctx, &instances.CreateActivationInput{IamRole: "r", RegistrationLimit: 5000})
	assertCode(t, err, types.ErrValidation)
	_, err = e.svc.CreateActivation(ctx, &instances.CreateActivationInput{IamRole: "r", ExpirationDate: types.NewTimestamp(time.Now().Add(-time.Hour))})
	assertCode(t, err, types.ErrValidation)
}

func TestRegisterAndAuthenticate(t *testing.T) {
	e := newEnv(t)
	act := e.activate(t, 1)
	reg := e.register(t, act)
	assert.True(t, strings.HasPrefix(reg.InstanceID, "mi-"))
	assert.Len(t, reg.InstanceID, 20)

	id, err := e.svc.Authenticate(reg.Token)
	require.NoError(t, err)
	assert.Equal(t, reg.InstanceID, id)

	for _, bad := range []string{"", "garbage", reg.InstanceID + ".wrong", "mi-00000000000000000.x"} {
		_, err := e.svc.Authenticate(bad)
		assert.ErrorIs(t, err, instances.ErrUnauthorized, bad)
	}

	// Limit reached.
	_, err = e.svc.Register(ctx, &agentproto.RegisterRequest{ActivationID: act.ActivationID, ActivationCode: act.ActivationCode})
	assertCode(t, err, types.ErrInvalidActivation)

	act2 := e.activate(t, 1)
	_, err = e.svc.Register(ctx, &agentproto.RegisterRequest{ActivationID: act2.ActivationID, ActivationCode: "wrong"})
	assertCode(t, err, types.ErrInvalidActivation)
	_, err = e.svc.Register(ctx, &agentproto.RegisterRequest{ActivationID: "missing", ActivationCode: "x"})
	assertCode(t, err, types.ErrInvalidActivation)
}

func TestExpiredActivationRejected(t *testing.T) {
	e := newEnv(t)
	act := e.activate(t, 1)
	types.SetNowFunc(func() time.Time { return time.Now().Add(48 * time.Hour) })
	t.Cleanup(func() { types.SetNowFunc(nil) })
	_, err := e.svc.Register(ctx, &agentproto.RegisterRequest{ActivationID: act.ActivationID, ActivationCode: act.ActivationCode})
	assertCode(t, err, types.ErrInvalidActivation)
}

func TestDescribeInstanceInformationAndPing(t *testing.T) {
	e := newEnv(t)
	reg := e.register(t, e.activate(t, 1))

	out, err := e.svc.DescribeInstanceInformation(ctx, &instances.DescribeInstanceInformationInput{})
	require.NoError(t, err)
	require.Len(t, out.InstanceInformationList, 1)
	info := out.InstanceInformationList[0]
	assert.Equal(t, reg.InstanceID, info.InstanceID)
	assert.Equal(t, "Online", info.PingStatus)
	assert.Equal(t, "ManagedInstance", info.ResourceType)
	assert.Equal(t, "box", info.Name)
	assert.Equal(t, "host1", info.ComputerName)
	assert.Equal(t, "10.0.0.1", info.IPAddress)
	assert.True(t, info.IsLatestVersion)

	types.SetNowFunc(func() time.Time { return time.Now().Add(5 * time.Minute) })
	out, err = e.svc.DescribeInstanceInformation(ctx, &instances.DescribeInstanceInformationInput{Filters: []instances.InstanceInformationStringFilter{{Key: "PingStatus", Values: []string{"ConnectionLost"}}}})
	require.NoError(t, err)
	assert.Len(t, out.InstanceInformationList, 1)

	require.NoError(t, e.svc.Heartbeat(reg.InstanceID, &agentproto.AgentInfo{AgentVersion: "2.0"}))
	types.SetNowFunc(nil)
	out, err = e.svc.DescribeInstanceInformation(ctx, &instances.DescribeInstanceInformationInput{InstanceInformationFilterList: []instances.InstanceInformationFilter{{Key: "PingStatus", ValueSet: []string{"Online"}}}})
	require.NoError(t, err)
	require.Len(t, out.InstanceInformationList, 1)
	assert.False(t, out.InstanceInformationList[0].IsLatestVersion)

	_, err = e.svc.DescribeInstanceInformation(ctx, &instances.DescribeInstanceInformationInput{Filters: []instances.InstanceInformationStringFilter{{Key: "Bogus", Values: []string{"x"}}}})
	assertCode(t, err, types.ErrInvalidInstanceInformationFilter)
	_, err = e.svc.DescribeInstanceInformation(ctx, &instances.DescribeInstanceInformationInput{MaxResults: 2})
	assertCode(t, err, types.ErrValidation)
}

func TestTagsAndTargets(t *testing.T) {
	e := newEnv(t)
	act := e.activate(t, 3)
	a, b, c := e.register(t, act), e.register(t, act), e.register(t, act)
	require.NoError(t, e.tags.Add(tags.ResourceManagedInstance, a.InstanceID, []types.Tag{{Key: "env", Value: "prod"}, {Key: "role", Value: "web"}}))
	require.NoError(t, e.tags.Add(tags.ResourceManagedInstance, b.InstanceID, []types.Tag{{Key: "env", Value: "prod"}}))
	require.NoError(t, e.tags.Add(tags.ResourceManagedInstance, c.InstanceID, []types.Tag{{Key: "env", Value: "dev"}}))

	resolve := func(targets ...types.Target) []string {
		var out []string
		require.NoError(t, e.store.View(func(r kv.Reader) error {
			var err error
			out, err = e.svc.ResolveTargetsTx(r, targets)
			return err
		}))
		return out
	}
	assert.ElementsMatch(t, []string{a.InstanceID, b.InstanceID}, resolve(types.Target{Key: "tag:env", Values: []string{"prod"}}))
	assert.Equal(t, []string{a.InstanceID}, resolve(types.Target{Key: "tag:env", Values: []string{"prod"}}, types.Target{Key: "tag-key", Values: []string{"role"}}))
	assert.Equal(t, []string{c.InstanceID}, resolve(types.Target{Key: "InstanceIds", Values: []string{c.InstanceID, "mi-00000000000000000"}}))

	out, err := e.svc.DescribeInstanceInformation(ctx, &instances.DescribeInstanceInformationInput{Filters: []instances.InstanceInformationStringFilter{{Key: "tag:env", Values: []string{"dev"}}}})
	require.NoError(t, err)
	require.Len(t, out.InstanceInformationList, 1)
	assert.Equal(t, c.InstanceID, out.InstanceInformationList[0].InstanceID)
}

func TestDeregister(t *testing.T) {
	e := newEnv(t)
	reg := e.register(t, e.activate(t, 1))
	require.NoError(t, e.svc.Deregister(ctx, &instances.DeregisterManagedInstanceInput{InstanceID: reg.InstanceID}))
	_, err := e.svc.Authenticate(reg.Token)
	assert.ErrorIs(t, err, instances.ErrUnauthorized)
	assertCode(t, e.svc.Deregister(ctx, &instances.DeregisterManagedInstanceInput{InstanceID: reg.InstanceID}), types.ErrInvalidInstanceID)
}
