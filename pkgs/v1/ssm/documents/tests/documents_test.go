package documents_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	kvbadger "github.com/tguidoux/openssm/pkgs/v1/kv/badger"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	kvsqlite "github.com/tguidoux/openssm/pkgs/v1/kv/sqlite"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
	_ "modernc.org/sqlite"
)

var (
	ctx     = context.Background()
	account = types.Account{AccountID: "123456789012", Region: "us-east-1"}
)

const cmdDoc = `{
  "schemaVersion": "2.2",
  "description": "Say hello",
  "parameters": {
    "who": {"type": "String", "default": "world", "allowedValues": ["world", "you"]},
    "count": {"type": "Integer", "default": 1}
  },
  "mainSteps": [{"action": "aws:runShellScript", "name": "hello", "inputs": {"runCommand": ["echo hello {{ who }}"]}}]
}`

const cmdDocYAML = `schemaVersion: "2.2"
description: YAML doc
mainSteps:
  - action: aws:runShellScript
    name: run
    inputs:
      runCommand:
        - echo yaml
`

type env struct {
	svc  *documents.Service
	tags *tags.Service
}

func forEachBackend(t *testing.T, fn func(t *testing.T, e *env)) {
	backends := map[string]func(t *testing.T) kv.Store{
		"memory": func(t *testing.T) kv.Store { return memory.New() },
		"sqlite": func(t *testing.T) kv.Store {
			s, db, err := kvsqlite.Open(filepath.Join(t.TempDir(), "d.db"))
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
	for name, mk := range backends {
		t.Run(name, func(t *testing.T) {
			store := mk(t)
			tg := tags.New(store)
			svc, err := documents.New(store, tg, account)
			require.NoError(t, err)
			fn(t, &env{svc: svc, tags: tg})
		})
	}
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, code, types.AsSSMError(err).Code(), err.Error())
}

func TestBuiltinsAreSeeded(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		out, err := e.svc.Describe(ctx, &documents.DescribeDocumentInput{Name: documents.DocRunShellScript})
		require.NoError(t, err)
		d := out.Document
		assert.Equal(t, "Amazon", d.Owner)
		assert.Equal(t, "Command", d.DocumentType)
		assert.Equal(t, []string{"Linux", "MacOS"}, d.PlatformTypes)
		names := []string{}
		for _, p := range d.Parameters {
			names = append(names, p.Name)
		}
		assert.Equal(t, []string{"commands", "executionTimeout", "workingDirectory"}, names)

		_, err = e.svc.Describe(ctx, &documents.DescribeDocumentInput{Name: documents.DocSessionShell})
		require.NoError(t, err)

		assertCode(t, e.svc.Delete(ctx, &documents.DeleteDocumentInput{Name: documents.DocRunShellScript}), types.ErrInvalidDocumentOperation)
	})
}

func TestCreateGetDescribe(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		out, err := e.svc.Create(ctx, &documents.CreateDocumentInput{
			Name: "Hello", Content: cmdDoc, DocumentType: "Command", VersionName: "v1",
			Tags: []types.Tag{{Key: "team", Value: "core"}},
		})
		require.NoError(t, err)
		d := out.DocumentDescription
		assert.Equal(t, "1", d.DocumentVersion)
		assert.Equal(t, "1", d.DefaultVersion)
		assert.Equal(t, "1", d.LatestVersion)
		assert.Equal(t, "Self", d.Owner)
		assert.Equal(t, "Active", d.Status)
		assert.Equal(t, "Sha256", d.HashType)
		assert.Len(t, d.Hash, 64)
		assert.Equal(t, "Say hello", d.Description)
		assert.Equal(t, []types.Tag{{Key: "team", Value: "core"}}, d.Tags)
		assert.Equal(t, "world", d.Parameters[1].DefaultValue)

		got, err := e.svc.Get(ctx, &documents.GetDocumentInput{Name: "Hello"})
		require.NoError(t, err)
		assert.Equal(t, cmdDoc, got.Content)
		assert.Equal(t, "JSON", got.DocumentFormat)

		asYAML, err := e.svc.Get(ctx, &documents.GetDocumentInput{Name: "Hello", DocumentFormat: "YAML"})
		require.NoError(t, err)
		assert.Contains(t, asYAML.Content, "schemaVersion:")

		got, err = e.svc.Get(ctx, &documents.GetDocumentInput{Name: account.DocumentARN("Hello"), VersionName: "v1"})
		require.NoError(t, err)
		assert.Equal(t, "1", got.DocumentVersion)

		_, err = e.svc.Create(ctx, &documents.CreateDocumentInput{Name: "Hello", Content: cmdDoc})
		assertCode(t, err, types.ErrDocumentAlreadyExists)
		_, err = e.svc.Get(ctx, &documents.GetDocumentInput{Name: "Missing"})
		assertCode(t, err, types.ErrInvalidDocument)
	})
}

func TestCreateYAML(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		out, err := e.svc.Create(ctx, &documents.CreateDocumentInput{Name: "Yaml", Content: cmdDocYAML, DocumentFormat: "YAML"})
		require.NoError(t, err)
		assert.Equal(t, "YAML", out.DocumentDescription.DocumentFormat)

		asJSON, err := e.svc.Get(ctx, &documents.GetDocumentInput{Name: "Yaml", DocumentFormat: "JSON"})
		require.NoError(t, err)
		assert.Contains(t, asJSON.Content, `"schemaVersion": "2.2"`)
	})
}

func TestCreateValidation(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		cases := []struct {
			in   documents.CreateDocumentInput
			code string
		}{
			{documents.CreateDocumentInput{Name: "ab", Content: cmdDoc}, types.ErrValidation},
			{documents.CreateDocumentInput{Name: "AWS-Mine", Content: cmdDoc}, types.ErrValidation},
			{documents.CreateDocumentInput{Name: "bad name", Content: cmdDoc}, types.ErrValidation},
			{documents.CreateDocumentInput{Name: "Empty", Content: ""}, types.ErrInvalidDocumentContent},
			{documents.CreateDocumentInput{Name: "BadJSON", Content: "{"}, types.ErrInvalidDocumentContent},
			{documents.CreateDocumentInput{Name: "NoSteps", Content: `{"schemaVersion":"2.2"}`}, types.ErrInvalidDocumentContent},
			{documents.CreateDocumentInput{Name: "BadSchema", Content: `{"schemaVersion":"9.9","mainSteps":[]}`}, types.ErrInvalidDocumentSchemaVersion},
			{documents.CreateDocumentInput{Name: "BadType", Content: cmdDoc, DocumentType: "Nope"}, types.ErrInvalidDocumentType},
			{documents.CreateDocumentInput{Name: "BadSession", Content: `{"schemaVersion":"1.0","sessionType":"Bogus"}`, DocumentType: "Session"}, types.ErrInvalidDocumentContent},
			{documents.CreateDocumentInput{Name: "Huge", Content: `{"schemaVersion":"2.2","x":"` + strings.Repeat("a", 70000) + `"}`}, types.ErrMaxDocumentSizeExceeded},
			{documents.CreateDocumentInput{Name: "BadParam", Content: `{"schemaVersion":"2.2","parameters":{"p":{"type":"Float"}},"mainSteps":[{"action":"a","name":"n"}]}`}, types.ErrInvalidDocumentContent},
		}
		for _, c := range cases {
			_, err := e.svc.Create(ctx, &c.in)
			assertCode(t, err, c.code)
		}
	})
}

func TestSchema12IsConvertedToSteps(t *testing.T) {
	e := &env{}
	store := memory.New()
	var err error
	e.svc, err = documents.New(store, tags.New(store), account)
	require.NoError(t, err)
	_, err = e.svc.Create(ctx, &documents.CreateDocumentInput{Name: "Legacy", Content: `{
		"schemaVersion": "1.2",
		"runtimeConfig": {"aws:runShellScript": {"properties": [{"runCommand": ["uptime"]}]}}
	}`})
	require.NoError(t, err)
	r, err := e.svc.Resolve("Legacy", "")
	require.NoError(t, err)
	require.Len(t, r.Content.MainSteps, 1)
	assert.Equal(t, "aws:runShellScript", r.Content.MainSteps[0].Action)
}

func TestUpdateVersionsAndDefault(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		_, err := e.svc.Create(ctx, &documents.CreateDocumentInput{Name: "Doc", Content: cmdDoc})
		require.NoError(t, err)

		_, err = e.svc.Update(ctx, &documents.UpdateDocumentInput{Name: "Doc", Content: cmdDoc})
		assertCode(t, err, types.ErrDuplicateDocumentContent)

		v2 := strings.Replace(cmdDoc, "Say hello", "Say hello v2", 1)
		out, err := e.svc.Update(ctx, &documents.UpdateDocumentInput{Name: "Doc", Content: v2, DocumentVersion: "$LATEST", VersionName: "two"})
		require.NoError(t, err)
		assert.Equal(t, "2", out.DocumentDescription.DocumentVersion)
		assert.Equal(t, "1", out.DocumentDescription.DefaultVersion)
		assert.Equal(t, "2", out.DocumentDescription.LatestVersion)

		_, err = e.svc.Update(ctx, &documents.UpdateDocumentInput{Name: "Doc", Content: v2 + " ", DocumentVersion: "1"})
		assertCode(t, err, types.ErrInvalidDocumentVersion)
		_, err = e.svc.Update(ctx, &documents.UpdateDocumentInput{Name: "Doc", Content: v2 + "  ", VersionName: "two"})
		assertCode(t, err, types.ErrDuplicateDocumentVersionName)

		got, err := e.svc.Get(ctx, &documents.GetDocumentInput{Name: "Doc"})
		require.NoError(t, err)
		assert.Equal(t, "1", got.DocumentVersion, "default version is returned")
		got, err = e.svc.Get(ctx, &documents.GetDocumentInput{Name: "Doc", DocumentVersion: "$LATEST"})
		require.NoError(t, err)
		assert.Equal(t, "2", got.DocumentVersion)

		def, err := e.svc.UpdateDefaultVersion(ctx, &documents.UpdateDocumentDefaultVersionInput{Name: "Doc", DocumentVersion: "2"})
		require.NoError(t, err)
		assert.Equal(t, "two", def.Description.DefaultVersionName)

		versions, err := e.svc.ListVersions(ctx, &documents.ListDocumentVersionsInput{Name: "Doc"})
		require.NoError(t, err)
		require.Len(t, versions.DocumentVersions, 2)
		assert.Equal(t, "2", versions.DocumentVersions[0].DocumentVersion)
		assert.True(t, versions.DocumentVersions[0].IsDefaultVersion)

		assertCode(t, e.svc.Delete(ctx, &documents.DeleteDocumentInput{Name: "Doc", DocumentVersion: "2"}), types.ErrInvalidDocumentOperation)
		require.NoError(t, e.svc.Delete(ctx, &documents.DeleteDocumentInput{Name: "Doc", DocumentVersion: "1"}))
		_, err = e.svc.Get(ctx, &documents.GetDocumentInput{Name: "Doc", DocumentVersion: "1"})
		assertCode(t, err, types.ErrInvalidDocumentVersion)

		// Version numbers are never reused.
		out, err = e.svc.Update(ctx, &documents.UpdateDocumentInput{Name: "Doc", Content: strings.Replace(cmdDoc, "hello", "hi", 1)})
		require.NoError(t, err)
		assert.Equal(t, "3", out.DocumentDescription.DocumentVersion)

		require.NoError(t, e.svc.Delete(ctx, &documents.DeleteDocumentInput{Name: "Doc"}))
		_, err = e.svc.Describe(ctx, &documents.DescribeDocumentInput{Name: "Doc"})
		assertCode(t, err, types.ErrInvalidDocument)
	})
}

func TestListFilters(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		_, err := e.svc.Create(ctx, &documents.CreateDocumentInput{Name: "MyCmd", Content: cmdDoc, Tags: []types.Tag{{Key: "env", Value: "dev"}}})
		require.NoError(t, err)
		_, err = e.svc.Create(ctx, &documents.CreateDocumentInput{Name: "MySession", DocumentType: "Session", Content: `{"schemaVersion":"1.0","sessionType":"Standard_Stream","inputs":{}}`})
		require.NoError(t, err)

		list := func(filters ...documents.DocumentKeyValuesFilter) []string {
			out, err := e.svc.List(ctx, &documents.ListDocumentsInput{Filters: filters})
			require.NoError(t, err)
			names := []string{}
			for _, d := range out.DocumentIdentifiers {
				names = append(names, d.Name)
			}
			return names
		}
		assert.Equal(t, []string{"MyCmd", "MySession"}, list(documents.DocumentKeyValuesFilter{Key: "Owner", Values: []string{"Self"}}))
		assert.Equal(t, []string{"MySession"}, list(
			documents.DocumentKeyValuesFilter{Key: "Owner", Values: []string{"Self"}},
			documents.DocumentKeyValuesFilter{Key: "DocumentType", Values: []string{"Session"}},
		))
		assert.Equal(t, []string{"MyCmd"}, list(documents.DocumentKeyValuesFilter{Key: "tag:env", Values: []string{"dev"}}))
		assert.Contains(t, list(documents.DocumentKeyValuesFilter{Key: "Owner", Values: []string{"Amazon"}}), documents.DocRunShellScript)
		assert.Contains(t, list(documents.DocumentKeyValuesFilter{Key: "SearchKeyword", Values: []string{"shell"}}), documents.DocRunShellScript)

		out, err := e.svc.List(ctx, &documents.ListDocumentsInput{DocumentFilterList: []documents.DocumentFilter{{Key: "Owner", Value: "Self"}}})
		require.NoError(t, err)
		assert.Len(t, out.DocumentIdentifiers, 2)

		_, err = e.svc.List(ctx, &documents.ListDocumentsInput{Filters: []documents.DocumentKeyValuesFilter{{Key: "Bogus", Values: []string{"x"}}}})
		assertCode(t, err, types.ErrInvalidFilterKey)

		n, err := e.svc.CountOwned()
		require.NoError(t, err)
		assert.Equal(t, 2, n)
	})
}

func TestDocumentTags(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		_, err := e.svc.Create(ctx, &documents.CreateDocumentInput{Name: "Tagged", Content: cmdDoc})
		require.NoError(t, err)
		require.NoError(t, e.tags.Add(tags.ResourceDocument, "Tagged", []types.Tag{{Key: "a", Value: "b"}}))
		list, err := e.tags.List(tags.ResourceDocument, "Tagged")
		require.NoError(t, err)
		assert.Len(t, list, 1)
		assertCode(t, e.tags.Add(tags.ResourceDocument, "Nope", []types.Tag{{Key: "a"}}), types.ErrInvalidResourceID)
	})
}

func TestResolveParametersAndInterpolate(t *testing.T) {
	store := memory.New()
	svc, err := documents.New(store, tags.New(store), account)
	require.NoError(t, err)
	r, err := svc.Resolve(documents.DocRunShellScript, "$DEFAULT")
	require.NoError(t, err)

	_, err = documents.ResolveParameters(r.Content, map[string][]string{})
	assertCode(t, err, types.ErrInvalidParameters)
	_, err = documents.ResolveParameters(r.Content, map[string][]string{"commands": {"x"}, "bogus": {"y"}})
	assertCode(t, err, types.ErrInvalidParameters)
	_, err = documents.ResolveParameters(r.Content, map[string][]string{"commands": {"x"}, "executionTimeout": {"0"}})
	assertCode(t, err, types.ErrInvalidParameters)

	params, err := documents.ResolveParameters(r.Content, map[string][]string{"commands": {"echo a", "echo b"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"3600"}, params["executionTimeout"])

	inputs := documents.Interpolate(r.Content.MainSteps[0].Inputs, params).(map[string]any)
	assert.Equal(t, []any{"echo a", "echo b"}, inputs["runCommand"])
	assert.Equal(t, "3600", inputs["timeoutSeconds"])
	assert.Equal(t, "", inputs["workingDirectory"])

	single := documents.Interpolate([]any{"echo {{ who }}!", "{{ list }}"}, map[string][]string{"who": {"me"}, "list": {"a", "b"}})
	assert.Equal(t, []any{"echo me!", "a", "b"}, single)
}
