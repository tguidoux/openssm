package parameters_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	kvbadger "github.com/tguidoux/openssm/pkgs/v1/kv/badger"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	kvsqlite "github.com/tguidoux/openssm/pkgs/v1/kv/sqlite"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/kms"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/parameters"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
	_ "modernc.org/sqlite"
)

var account = types.Account{AccountID: "123456789012", Region: "us-east-1"}

type env struct {
	svc  *parameters.Service
	tags *tags.Service
	kms  *kms.Service
}

var backends = map[string]func(t *testing.T) kv.Store{
	"memory": func(t *testing.T) kv.Store { return memory.New() },
	"sqlite": func(t *testing.T) kv.Store {
		s, db, err := kvsqlite.Open(filepath.Join(t.TempDir(), "p.db"))
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		return s
	},
	"badger": func(t *testing.T) kv.Store {
		s, err := kvbadger.Open(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { s.Close() })
		return s
	},
}

func newEnv(t *testing.T, store kv.Store, opts parameters.Options) *env {
	t.Helper()
	k, err := kms.New(store, account, kms.Options{MasterKey: "test-master"})
	require.NoError(t, err)
	tg := tags.New(store)
	return &env{svc: parameters.New(store, k, tg, account, opts), tags: tg, kms: k}
}

// forEachBackend runs fn against memory, SQLite, and Badger stores.
func forEachBackend(t *testing.T, fn func(t *testing.T, e *env)) {
	for name, mk := range backends {
		t.Run(name, func(t *testing.T) { fn(t, newEnv(t, mk(t), parameters.Options{})) })
	}
}

func ptr[T any](v T) *T { return &v }

var ctx = context.Background()

func put(t *testing.T, e *env, name, value, typ string) *parameters.PutParameterOutput {
	t.Helper()
	out, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: name, Value: &value, Type: typ})
	require.NoError(t, err)
	return out
}

func overwrite(t *testing.T, e *env, name, value string) int64 {
	t.Helper()
	out, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: name, Value: &value, Overwrite: true})
	require.NoError(t, err)
	return out.Version
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, code, types.AsSSMError(err).Code(), err.Error())
}

func TestPutGetString(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		out := put(t, e, "/app/db/host", "localhost", parameters.TypeString)
		assert.Equal(t, int64(1), out.Version)
		assert.Equal(t, parameters.TierStandard, out.Tier)

		got, err := e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/app/db/host"})
		require.NoError(t, err)
		p := got.Parameter
		assert.Equal(t, "localhost", p.Value)
		assert.Equal(t, "String", p.Type)
		assert.Equal(t, "text", p.DataType)
		assert.Equal(t, "arn:aws:ssm:us-east-1:123456789012:parameter/app/db/host", p.ARN)
		assert.NotNil(t, p.LastModifiedDate)
	})
}

func TestPutRequiresTypeAndValue(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/x", Value: ptr("v")})
		assertCode(t, err, types.ErrValidation)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/x", Type: "String"})
		assertCode(t, err, types.ErrValidation)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/x", Value: ptr("v"), Type: "Binary"})
		assertCode(t, err, types.ErrUnsupportedParameterType)
	})
}

func TestPutAlreadyExistsAndOverwrite(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		put(t, e, "/k", "1", "String")
		_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/k", Value: ptr("2"), Type: "String"})
		assertCode(t, err, types.ErrParameterAlreadyExists)

		assert.Equal(t, int64(2), overwrite(t, e, "/k", "2"))
		got, err := e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/k"})
		require.NoError(t, err)
		assert.Equal(t, "2", got.Parameter.Value)
		assert.Equal(t, "String", got.Parameter.Type, "type is inherited on overwrite")
	})
}

func TestNameValidation(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		for _, name := range []string{"", "no spaces", "a/b", "/trailing/", "/a//b", "/aws/x", "ssm-thing", "/ssm/x", "/" + strings.Repeat("a/", 15) + "b"} {
			_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: name, Value: ptr("v"), Type: "String"})
			assert.Error(t, err, name)
		}
		_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/" + strings.Repeat("a/", 15) + "b", Value: ptr("v"), Type: "String"})
		assertCode(t, err, types.ErrHierarchyLevelLimitExceeded)
		put(t, e, "simple.name-1_x", "v", "String")
	})
}

func TestSecureStringEncryption(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		put(t, e, "/app/pw", "hunter2", parameters.TypeSecureString)

		enc, err := e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/app/pw"})
		require.NoError(t, err)
		assert.NotEqual(t, "hunter2", enc.Parameter.Value)
		assert.NotEmpty(t, enc.Parameter.Value)

		dec, err := e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/app/pw", WithDecryption: true})
		require.NoError(t, err)
		assert.Equal(t, "hunter2", dec.Parameter.Value)

		desc, err := e.svc.Describe(ctx, &parameters.DescribeParametersInput{})
		require.NoError(t, err)
		require.Len(t, desc.Parameters, 1)
		assert.Equal(t, kms.DefaultSSMAlias, desc.Parameters[0].KeyID)
	})
}

func TestSecureStringCustomKey(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		k, err := e.kms.CreateKey("app", "alias/app")
		require.NoError(t, err)

		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/s", Value: ptr("v"), Type: "SecureString", KeyID: "alias/app"})
		require.NoError(t, err)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/s2", Value: ptr("v"), Type: "SecureString", KeyID: k.ARN})
		require.NoError(t, err)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/s3", Value: ptr("v"), Type: "SecureString", KeyID: "alias/missing"})
		assertCode(t, err, types.ErrInvalidKeyID)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/s4", Value: ptr("v"), Type: "String", KeyID: "alias/app"})
		assertCode(t, err, types.ErrValidation)

		// Overwrite keeps the previous key.
		overwrite(t, e, "/s", "v2")
		desc, err := e.svc.Describe(ctx, &parameters.DescribeParametersInput{ParameterFilters: []parameters.StringFilter{{Key: "KeyId", Values: []string{"alias/app"}}}})
		require.NoError(t, err)
		require.Len(t, desc.Parameters, 1)
		assert.Equal(t, "/s", desc.Parameters[0].Name)

		dec, err := e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/s", WithDecryption: true})
		require.NoError(t, err)
		assert.Equal(t, "v2", dec.Parameter.Value)
	})
}

func TestVersionAndLabelSelectors(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		put(t, e, "/sel", "v1", "String")
		overwrite(t, e, "/sel", "v2")
		overwrite(t, e, "/sel", "v3")

		got, err := e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/sel:1"})
		require.NoError(t, err)
		assert.Equal(t, "v1", got.Parameter.Value)
		assert.Equal(t, ":1", got.Parameter.Selector)

		_, err = e.svc.Label(ctx, &parameters.LabelParameterVersionInput{Name: "/sel", ParameterVersion: ptr(int64(2)), Labels: []string{"prod"}})
		require.NoError(t, err)
		got, err = e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/sel:prod"})
		require.NoError(t, err)
		assert.Equal(t, "v2", got.Parameter.Value)

		_, err = e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/sel:9"})
		assertCode(t, err, types.ErrParameterVersionNotFound)
		_, err = e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/sel:nolabel"})
		assertCode(t, err, types.ErrParameterVersionNotFound)

		got, err = e.svc.Get(ctx, &parameters.GetParameterInput{Name: account.ParameterARN("/sel") + ":1"})
		require.NoError(t, err)
		assert.Equal(t, "v1", got.Parameter.Value)
	})
}

func TestLabelMovesBetweenVersionsAndValidates(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		put(t, e, "/lbl", "v1", "String")
		overwrite(t, e, "/lbl", "v2")

		out, err := e.svc.Label(ctx, &parameters.LabelParameterVersionInput{Name: "/lbl", ParameterVersion: ptr(int64(1)), Labels: []string{"live", "1bad", "aws-x", "ok.label"}})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"1bad", "aws-x"}, out.InvalidLabels)
		assert.Equal(t, int64(1), out.ParameterVersion)

		// Labeling without version targets latest and moves "live".
		out, err = e.svc.Label(ctx, &parameters.LabelParameterVersionInput{Name: "/lbl", Labels: []string{"live"}})
		require.NoError(t, err)
		assert.Equal(t, int64(2), out.ParameterVersion)

		hist, err := e.svc.History(ctx, &parameters.GetParameterHistoryInput{Name: "/lbl"})
		require.NoError(t, err)
		assert.Equal(t, []string{"ok.label"}, hist.Parameters[0].Labels)
		assert.Equal(t, []string{"live"}, hist.Parameters[1].Labels)

		un, err := e.svc.Unlabel(ctx, &parameters.UnlabelParameterVersionInput{Name: "/lbl", ParameterVersion: ptr(int64(2)), Labels: []string{"live", "nope"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"live"}, un.RemovedLabels)
		assert.Equal(t, []string{"nope"}, un.InvalidLabels)

		_, err = e.svc.Label(ctx, &parameters.LabelParameterVersionInput{Name: "/lbl", ParameterVersion: ptr(int64(7)), Labels: []string{"x"}})
		assertCode(t, err, types.ErrParameterVersionNotFound)
	})
}

func TestLabelLimit(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		put(t, e, "/many", "v", "String")
		labels := make([]string, 10)
		for i := range labels {
			labels[i] = fmt.Sprintf("l%d", i)
		}
		_, err := e.svc.Label(ctx, &parameters.LabelParameterVersionInput{Name: "/many", Labels: labels})
		require.NoError(t, err)
		_, err = e.svc.Label(ctx, &parameters.LabelParameterVersionInput{Name: "/many", Labels: []string{"extra"}})
		assertCode(t, err, types.ErrParameterVersionLabelLimitExceeded)
	})
}

func TestGetParameters(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		put(t, e, "/a", "1", "String")
		put(t, e, "/b", "2", "StringList")
		out, err := e.svc.GetMany(ctx, &parameters.GetParametersInput{Names: []string{"/a", "/b", "/missing", "/a:5", "/a"}})
		require.NoError(t, err)
		assert.Len(t, out.Parameters, 2)
		assert.ElementsMatch(t, []string{"/missing", "/a:5"}, out.InvalidParameters)

		_, err = e.svc.GetMany(ctx, &parameters.GetParametersInput{Names: make([]string, 11)})
		assertCode(t, err, types.ErrValidation)
	})
}

func TestGetParametersByPath(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		put(t, e, "/app/a", "1", "String")
		put(t, e, "/app/b", "2", "SecureString")
		put(t, e, "/app/sub/c", "3", "String")
		put(t, e, "/application", "x", "String")
		put(t, e, "root", "r", "String")

		out, err := e.svc.GetByPath(ctx, &parameters.GetParametersByPathInput{Path: "/app"})
		require.NoError(t, err)
		assert.Equal(t, []string{"/app/a", "/app/b"}, names(out.Parameters))

		out, err = e.svc.GetByPath(ctx, &parameters.GetParametersByPathInput{Path: "/app/", Recursive: true, WithDecryption: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"/app/a", "/app/b", "/app/sub/c"}, names(out.Parameters))
		assert.Equal(t, "2", out.Parameters[1].Value)

		out, err = e.svc.GetByPath(ctx, &parameters.GetParametersByPathInput{Path: "/app", Recursive: true, ParameterFilters: []parameters.StringFilter{{Key: "Type", Values: []string{"SecureString"}}}})
		require.NoError(t, err)
		assert.Equal(t, []string{"/app/b"}, names(out.Parameters))

		out, err = e.svc.GetByPath(ctx, &parameters.GetParametersByPathInput{Path: "/", Recursive: true})
		require.NoError(t, err)
		assert.Len(t, out.Parameters, 4, "non-hierarchical names are excluded from path queries")

		_, err = e.svc.GetByPath(ctx, &parameters.GetParametersByPathInput{Path: "app"})
		assertCode(t, err, types.ErrValidation)
		_, err = e.svc.GetByPath(ctx, &parameters.GetParametersByPathInput{Path: "/app", ParameterFilters: []parameters.StringFilter{{Key: "Name", Values: []string{"x"}}}})
		assertCode(t, err, types.ErrInvalidFilterKey)
	})
}

func TestGetParametersByPathLabelFilter(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		put(t, e, "/l/a", "a1", "String")
		overwrite(t, e, "/l/a", "a2")
		put(t, e, "/l/b", "b1", "String")
		_, err := e.svc.Label(ctx, &parameters.LabelParameterVersionInput{Name: "/l/a", ParameterVersion: ptr(int64(1)), Labels: []string{"stable"}})
		require.NoError(t, err)

		out, err := e.svc.GetByPath(ctx, &parameters.GetParametersByPathInput{Path: "/l", ParameterFilters: []parameters.StringFilter{{Key: "Label", Option: "Equals", Values: []string{"stable"}}}})
		require.NoError(t, err)
		require.Len(t, out.Parameters, 1)
		assert.Equal(t, "a1", out.Parameters[0].Value)
	})
}

func TestPagination(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		for i := range 25 {
			put(t, e, fmt.Sprintf("/p/k%02d", i), "v", "String")
		}
		var all []string
		token := ""
		pages := 0
		for {
			out, err := e.svc.GetByPath(ctx, &parameters.GetParametersByPathInput{Path: "/p", NextToken: token})
			require.NoError(t, err)
			all = append(all, names(out.Parameters)...)
			pages++
			if out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
		assert.Len(t, all, 25)
		assert.Equal(t, 3, pages)

		_, err := e.svc.GetByPath(ctx, &parameters.GetParametersByPathInput{Path: "/p", MaxResults: 11})
		assertCode(t, err, types.ErrValidation)
		_, err = e.svc.GetByPath(ctx, &parameters.GetParametersByPathInput{Path: "/p", NextToken: "garbage!"})
		assertCode(t, err, types.ErrInvalidNextToken)

		desc, err := e.svc.Describe(ctx, &parameters.DescribeParametersInput{MaxResults: 50})
		require.NoError(t, err)
		assert.Len(t, desc.Parameters, 25)
		assert.Empty(t, desc.NextToken)
	})
}

func TestDescribeFilters(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		put(t, e, "/svc/a/x", "1", "String")
		put(t, e, "/svc/b", "2", "SecureString")
		put(t, e, "plain", "3", "StringList")
		require.NoError(t, e.tags.Add(tags.ResourceParameter, "/svc/b", []types.Tag{{Key: "env", Value: "prod"}}))

		cases := []struct {
			filters []parameters.StringFilter
			want    []string
		}{
			{[]parameters.StringFilter{{Key: "Name", Option: "BeginsWith", Values: []string{"/svc"}}}, []string{"/svc/a/x", "/svc/b"}},
			{[]parameters.StringFilter{{Key: "Name", Option: "Contains", Values: []string{"PLA"}}}, []string{"plain"}},
			{[]parameters.StringFilter{{Key: "Name", Values: []string{"/svc/b"}}}, []string{"/svc/b"}},
			{[]parameters.StringFilter{{Key: "Type", Values: []string{"StringList"}}}, []string{"plain"}},
			{[]parameters.StringFilter{{Key: "Path", Option: "OneLevel", Values: []string{"/svc"}}}, []string{"/svc/b"}},
			{[]parameters.StringFilter{{Key: "Path", Option: "Recursive", Values: []string{"/svc"}}}, []string{"/svc/a/x", "/svc/b"}},
			{[]parameters.StringFilter{{Key: "tag:env", Values: []string{"prod"}}}, []string{"/svc/b"}},
			{[]parameters.StringFilter{{Key: "tag:env"}}, []string{"/svc/b"}},
			{[]parameters.StringFilter{{Key: "Tier", Values: []string{"Standard"}}, {Key: "Type", Values: []string{"String"}}}, []string{"/svc/a/x"}},
		}
		for _, c := range cases {
			out, err := e.svc.Describe(ctx, &parameters.DescribeParametersInput{ParameterFilters: c.filters})
			require.NoError(t, err, c.filters)
			got := make([]string, 0, len(out.Parameters))
			for _, p := range out.Parameters {
				got = append(got, p.Name)
			}
			assert.Equal(t, c.want, got, c.filters)
		}

		legacy, err := e.svc.Describe(ctx, &parameters.DescribeParametersInput{Filters: []parameters.LegacyFilter{{Key: "Name", Values: []string{"/svc/a"}}}})
		require.NoError(t, err)
		assert.Len(t, legacy.Parameters, 1)

		_, err = e.svc.Describe(ctx, &parameters.DescribeParametersInput{ParameterFilters: []parameters.StringFilter{{Key: "Bogus", Values: []string{"x"}}}})
		assertCode(t, err, types.ErrInvalidFilterKey)
		_, err = e.svc.Describe(ctx, &parameters.DescribeParametersInput{ParameterFilters: []parameters.StringFilter{{Key: "Name", Option: "Recursive", Values: []string{"x"}}}})
		assertCode(t, err, types.ErrInvalidFilterOption)
		_, err = e.svc.Describe(ctx, &parameters.DescribeParametersInput{
			Filters:          []parameters.LegacyFilter{{Key: "Name", Values: []string{"x"}}},
			ParameterFilters: []parameters.StringFilter{{Key: "Name", Values: []string{"x"}}},
		})
		assertCode(t, err, types.ErrValidation)
	})
}

func TestHistory(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/h", Value: ptr("a"), Type: "SecureString", Description: ptr("first")})
		require.NoError(t, err)
		overwrite(t, e, "/h", "b")

		out, err := e.svc.History(ctx, &parameters.GetParameterHistoryInput{Name: "/h", WithDecryption: true})
		require.NoError(t, err)
		require.Len(t, out.Parameters, 2)
		assert.Equal(t, "a", out.Parameters[0].Value)
		assert.Equal(t, "b", out.Parameters[1].Value)
		assert.Equal(t, "first", out.Parameters[1].Description, "description is inherited")
		assert.Equal(t, []string{}, out.Parameters[0].Labels)

		_, err = e.svc.History(ctx, &parameters.GetParameterHistoryInput{Name: "/missing"})
		assertCode(t, err, types.ErrParameterNotFound)
	})
}

func TestMaxVersionsDropsOldestUnlabeled(t *testing.T) {
	e := newEnv(t, memory.New(), parameters.Options{})
	put(t, e, "/v", "0", "String")
	for i := 1; i < parameters.MaxVersions; i++ {
		overwrite(t, e, "/v", fmt.Sprint(i))
	}
	assert.Equal(t, int64(101), overwrite(t, e, "/v", "100"))
	_, err := e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/v:1"})
	assertCode(t, err, types.ErrParameterVersionNotFound)

	_, err = e.svc.Label(ctx, &parameters.LabelParameterVersionInput{Name: "/v", ParameterVersion: ptr(int64(2)), Labels: []string{"keep"}})
	require.NoError(t, err)
	_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/v", Value: ptr("x"), Overwrite: true})
	assertCode(t, err, types.ErrParameterMaxVersionLimitExceeded)
}

func TestDelete(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		put(t, e, "/d", "1", "String")
		require.NoError(t, e.tags.Add(tags.ResourceParameter, "/d", []types.Tag{{Key: "k", Value: "v"}}))
		require.NoError(t, e.svc.Delete(ctx, &parameters.DeleteParameterInput{Name: "/d"}))
		assertCode(t, e.svc.Delete(ctx, &parameters.DeleteParameterInput{Name: "/d"}), types.ErrParameterNotFound)

		// Recreate: version restarts and tags are gone.
		out := put(t, e, "/d", "1", "String")
		assert.Equal(t, int64(1), out.Version)
		list, err := e.tags.List(tags.ResourceParameter, "/d")
		require.NoError(t, err)
		assert.Empty(t, list)

		put(t, e, "/e", "1", "String")
		res, err := e.svc.DeleteMany(ctx, &parameters.DeleteParametersInput{Names: []string{"/d", "/e", "/nope"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"/d", "/e"}, res.DeletedParameters)
		assert.Equal(t, []string{"/nope"}, res.InvalidParameters)
	})
}

func TestAllowedPattern(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/num", Value: ptr("12"), Type: "String", AllowedPattern: ptr(`\d+`)})
		require.NoError(t, err)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/num", Value: ptr("12a"), Overwrite: true})
		assertCode(t, err, types.ErrParameterPatternMismatch)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/bad", Value: ptr("x"), Type: "String", AllowedPattern: ptr(`(`)})
		assertCode(t, err, types.ErrInvalidAllowedPattern)
	})
}

func TestTiersAndSizes(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		big := strings.Repeat("x", 5000)
		_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/big", Value: &big, Type: "String"})
		assertCode(t, err, types.ErrValidation)

		out, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/big", Value: &big, Type: "String", Tier: "Intelligent-Tiering"})
		require.NoError(t, err)
		assert.Equal(t, parameters.TierAdvanced, out.Tier)

		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/big", Value: ptr("small"), Overwrite: true, Tier: "Standard"})
		assertCode(t, err, types.ErrValidation)

		out, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/big", Value: ptr("small"), Overwrite: true})
		require.NoError(t, err)
		assert.Equal(t, parameters.TierAdvanced, out.Tier, "advanced is sticky")

		huge := strings.Repeat("x", 9000)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/huge", Value: &huge, Type: "String", Tier: "Advanced"})
		assertCode(t, err, types.ErrValidation)
	})
}

func TestParameterCountLimit(t *testing.T) {
	e := newEnv(t, memory.New(), parameters.Options{MaxStandardParameters: 2})
	put(t, e, "/1", "v", "String")
	put(t, e, "/2", "v", "String")
	_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/3", Value: ptr("v"), Type: "String"})
	assertCode(t, err, types.ErrParameterLimitExceeded)
	overwrite(t, e, "/2", "again")
}

func TestDataTypeEC2Image(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/ami", Value: ptr("ami-0123456789abcdef0"), Type: "String", DataType: "aws:ec2:image"})
		require.NoError(t, err)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/ami2", Value: ptr("not-an-ami"), Type: "String", DataType: "aws:ec2:image"})
		assertCode(t, err, types.ErrValidation)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/ami3", Value: ptr("ami-0123456789abcdef0"), Type: "SecureString", DataType: "aws:ec2:image"})
		assertCode(t, err, types.ErrValidation)
		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/dt", Value: ptr("x"), Type: "String", DataType: "TEXT"})
		assertCode(t, err, types.ErrValidation)
	})
}

func TestTagsOnCreateAndOverwriteConflict(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/t", Value: ptr("v"), Type: "String", Tags: []types.Tag{{Key: "a", Value: "1"}}})
		require.NoError(t, err)
		list, err := e.tags.List(tags.ResourceParameter, "/t")
		require.NoError(t, err)
		assert.Equal(t, []types.Tag{{Key: "a", Value: "1"}}, list)

		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/t", Value: ptr("v"), Overwrite: true, Tags: []types.Tag{{Key: "b", Value: "2"}}})
		assertCode(t, err, types.ErrValidation)

		assertCode(t, e.tags.Add(tags.ResourceParameter, "/missing", []types.Tag{{Key: "a"}}), types.ErrInvalidResourceID)
		assertCode(t, e.tags.Add("Bogus", "/t", []types.Tag{{Key: "a"}}), types.ErrInvalidResourceType)
	})
}

func TestExpirationPolicy(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		types.SetNowFunc(func() time.Time { return now })
		t.Cleanup(func() { types.SetNowFunc(nil) })

		policy := `[{"Type":"Expiration","Version":"1.0","Attributes":{"Timestamp":"2030-01-01T01:00:00Z"}}]`
		_, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/exp", Value: ptr("v"), Type: "String", Policies: policy})
		assertCode(t, err, types.ErrIncompatiblePolicy)

		out, err := e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/exp", Value: ptr("v"), Type: "String", Tier: "Intelligent-Tiering", Policies: policy})
		require.NoError(t, err)
		assert.Equal(t, parameters.TierAdvanced, out.Tier)

		desc, err := e.svc.Describe(ctx, &parameters.DescribeParametersInput{})
		require.NoError(t, err)
		require.Len(t, desc.Parameters[0].Policies, 1)
		assert.Equal(t, "Expiration", desc.Parameters[0].Policies[0].PolicyType)

		now = now.Add(2 * time.Hour)
		_, err = e.svc.Get(ctx, &parameters.GetParameterInput{Name: "/exp"})
		assertCode(t, err, types.ErrParameterNotFound)
		deleted, err := e.svc.Sweep(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"/exp"}, deleted)

		_, err = e.svc.Put(ctx, &parameters.PutParameterInput{Name: "/bad", Value: ptr("v"), Type: "String", Tier: "Advanced", Policies: `[{"Type":"Nope"}]`})
		assertCode(t, err, "InvalidPolicyTypeException")
	})
}

func TestLastModifiedUserFromCaller(t *testing.T) {
	e := newEnv(t, memory.New(), parameters.Options{})
	c := types.WithCaller(ctx, types.Caller{Name: "alice"})
	_, err := e.svc.Put(c, &parameters.PutParameterInput{Name: "/u", Value: ptr("v"), Type: "String"})
	require.NoError(t, err)
	desc, err := e.svc.Describe(ctx, &parameters.DescribeParametersInput{})
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:iam::123456789012:user/alice", desc.Parameters[0].LastModifiedUser)
}

func TestCounts(t *testing.T) {
	e := newEnv(t, memory.New(), parameters.Options{})
	put(t, e, "/a", "v", "String")
	put(t, e, "/b", "v", "String")
	put(t, e, "/c", "v", "SecureString")
	counts, err := e.svc.Counts()
	require.NoError(t, err)
	assert.Equal(t, 2, counts[[2]string{"String", "Standard"}])
	assert.Equal(t, 1, counts[[2]string{"SecureString", "Standard"}])
}

func names(ps []*parameters.Parameter) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}
