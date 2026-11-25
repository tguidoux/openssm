package ui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ui "github.com/tguidoux/openssm/apps/go/server/ui"
	"github.com/tguidoux/openssm/pkgs/v1/credentials"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/kms"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/parameters"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

type fixture struct {
	srv    *ui.Server
	params *parameters.Service
	docs   *documents.Service
}

func newFixture(t *testing.T, metrics bool) *fixture {
	t.Helper()
	store := memory.New()
	account := types.Account{AccountID: "123456789012", Region: "us-east-1"}
	k, err := kms.New(store, account, kms.Options{MasterKey: "test"})
	require.NoError(t, err)
	tg := tags.New(store)
	p := parameters.New(store, k, tg, account, parameters.Options{})
	d, err := documents.New(store, tg, account)
	require.NoError(t, err)
	srv := ui.NewServer(0, ui.Deps{
		Parameters: p, Documents: d, KMS: k, Tags: tg, Credentials: credentials.NewMemoryCredentialStore(),
		EndpointURL: "http://localhost:9330", Region: "us-east-1", AccountID: "123456789012",
		MetricsEnabled: metrics,
	}, logger.New("ui-test", logger.UncontextualLoggerType), nil)
	return &fixture{srv: srv, params: p, docs: d}
}

func (f *fixture) get(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	return w
}

func (f *fixture) post(path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	return w
}

func (f *fixture) put(t *testing.T, name, value, typ string) {
	t.Helper()
	_, err := f.params.Put(context.Background(), &parameters.PutParameterInput{Name: name, Value: &value, Type: typ})
	require.NoError(t, err)
}

func TestRootRedirectsToParameters(t *testing.T) {
	f := newFixture(t, false)
	w := f.get("/")
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/parameters", w.Header().Get("Location"))
}

func TestParametersPageEmpty(t *testing.T) {
	f := newFixture(t, false)
	w := f.get("/parameters")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "No parameters found")
	assert.Contains(t, w.Body.String(), "AWS_ENDPOINT_URL")
}

func TestParametersPageListsAndFilters(t *testing.T) {
	f := newFixture(t, false)
	f.put(t, "/app/db/host", "localhost", "String")
	f.put(t, "/other/key", "v", "String")

	w := f.get("/parameters")
	assert.Contains(t, w.Body.String(), "/app/db/host")
	assert.Contains(t, w.Body.String(), "/other/key")

	w = f.get("/parameters?q=db")
	assert.Contains(t, w.Body.String(), "/app/db/host")
	assert.NotContains(t, w.Body.String(), "/other/key")
}

func TestParameterDetailHidesSecureStringUntilRevealed(t *testing.T) {
	f := newFixture(t, false)
	f.put(t, "/app/secret", "hunter2", "SecureString")

	w := f.get("/parameter?name=" + url.QueryEscape("/app/secret"))
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "hunter2")
	assert.Contains(t, w.Body.String(), "Show decrypted value")
	assert.Contains(t, w.Body.String(), "--with-decryption")

	w = f.get("/parameter?name=" + url.QueryEscape("/app/secret") + "&reveal=1")
	assert.Contains(t, w.Body.String(), "hunter2")
}

func TestParameterDetailMissingRedirects(t *testing.T) {
	f := newFixture(t, false)
	w := f.get("/parameter?name=/nope")
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "ParameterNotFound")
}

func TestCreateAndEditParameterViaForm(t *testing.T) {
	f := newFixture(t, false)
	w := f.post("/parameters", url.Values{"name": {"/app/x"}, "value": {"one"}, "type": {"String"}, "tier": {"Standard"}, "dataType": {"text"}})
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "/parameter?name=")

	w = f.post("/parameters", url.Values{"edit": {"1"}, "name": {"/app/x"}, "value": {"two"}, "type": {"String"}, "tier": {"Standard"}, "dataType": {"text"}})
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "version+2")

	got, err := f.params.Get(context.Background(), &parameters.GetParameterInput{Name: "/app/x"})
	require.NoError(t, err)
	assert.Equal(t, "two", got.Parameter.Value)
}

func TestCreateDuplicateParameterShowsError(t *testing.T) {
	f := newFixture(t, false)
	f.put(t, "/dup", "a", "String")
	w := f.post("/parameters", url.Values{"name": {"/dup"}, "value": {"b"}, "type": {"String"}, "tier": {"Standard"}, "dataType": {"text"}})
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "ParameterAlreadyExists")
}

func TestParameterLinksAreSingleEscaped(t *testing.T) {
	f := newFixture(t, false)
	f.put(t, "/a/b", "v", "String")
	body := f.get("/parameters").Body.String()
	assert.Contains(t, body, `href="/parameter?name=%2fa%2fb"`)
	assert.NotContains(t, body, "%252F")
}

func TestDeleteParameter(t *testing.T) {
	f := newFixture(t, false)
	f.put(t, "/gone", "a", "String")
	w := f.post("/parameter/delete", url.Values{"name": {"/gone"}})
	assert.Equal(t, http.StatusSeeOther, w.Code)
	_, err := f.params.Get(context.Background(), &parameters.GetParameterInput{Name: "/gone"})
	assert.True(t, types.IsCode(err, types.ErrParameterNotFound))
}

func TestLabelAndTagParameter(t *testing.T) {
	f := newFixture(t, false)
	f.put(t, "/lbl", "a", "String")

	f.post("/parameter/label", url.Values{"name": {"/lbl"}, "version": {"1"}, "label": {"prod"}})
	got, err := f.params.Get(context.Background(), &parameters.GetParameterInput{Name: "/lbl:prod"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.Parameter.Version)

	f.post("/parameter/tags", url.Values{"name": {"/lbl"}, "key": {"team"}, "value": {"core"}})
	w := f.get("/parameter?name=/lbl")
	assert.Contains(t, w.Body.String(), "team")
	assert.Contains(t, w.Body.String(), "core")
}

func TestCSRFRejectsCrossOriginPost(t *testing.T) {
	f := newFixture(t, false)
	req := httptest.NewRequest(http.MethodPost, "/parameters", strings.NewReader("name=/x"))
	req.Header.Set("Origin", "http://evil.com")
	req.Host = "example.com"
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestKeysPageAndCreate(t *testing.T) {
	f := newFixture(t, false)
	w := f.get("/keys")
	assert.Contains(t, w.Body.String(), "alias/aws/ssm")

	w = f.post("/keys", url.Values{"alias": {"my-app"}, "description": {"test"}})
	assert.Equal(t, http.StatusSeeOther, w.Code)
	w = f.get("/keys")
	assert.Contains(t, w.Body.String(), "alias/my-app")
}

func TestCredentialsCreateShowsSecretOnce(t *testing.T) {
	f := newFixture(t, false)
	w := f.post("/credentials", url.Values{"label": {"ci"}})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "AWS_SECRET_ACCESS_KEY")

	w = f.get("/credentials")
	assert.Contains(t, w.Body.String(), "ci")
	assert.NotContains(t, w.Body.String(), "AWS_SECRET_ACCESS_KEY")
}

func TestMetricsPageDisabled(t *testing.T) {
	f := newFixture(t, false)
	assert.Equal(t, http.StatusNotFound, f.get("/metrics").Code)
	assert.NotContains(t, f.get("/parameters").Body.String(), `href="/metrics"`)
}

func TestMetricsPageEnabled(t *testing.T) {
	f := newFixture(t, true)
	w := f.get("/metrics")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, f.get("/parameters").Body.String(), `href="/metrics"`)
}

func TestSecurityHeaders(t *testing.T) {
	f := newFixture(t, false)
	w := f.get("/parameters")
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
}

func TestStaticAssets(t *testing.T) {
	f := newFixture(t, false)
	assert.Equal(t, http.StatusOK, f.get("/static/app.js").Code)
	assert.Equal(t, http.StatusOK, f.get("/static/style.css").Code)
}

func TestDocumentsPages(t *testing.T) {
	f := newFixture(t, false)
	w := f.get("/documents")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "No documents found")

	w = f.get("/documents?owner=Amazon")
	assert.Contains(t, w.Body.String(), "AWS-RunShellScript")

	w = f.get("/documents/new")
	assert.Contains(t, w.Body.String(), "schemaVersion")

	content := `{"schemaVersion":"2.2","mainSteps":[{"action":"aws:runShellScript","name":"a","inputs":{"runCommand":["true"]}}]}`
	w = f.post("/documents", url.Values{"name": {"MyDoc"}, "content": {content}, "format": {"JSON"}, "type": {"Command"}})
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "success")

	w = f.get("/document?name=MyDoc")
	assert.Contains(t, w.Body.String(), "Create new version")
	assert.Contains(t, w.Body.String(), "runShellScript")

	w = f.post("/documents", url.Values{"edit": {"1"}, "name": {"MyDoc"}, "content": {content + " "}, "format": {"JSON"}})
	require.Equal(t, http.StatusSeeOther, w.Code)
	f.post("/document/default", url.Values{"name": {"MyDoc"}, "version": {"2"}})
	desc, err := f.docs.Describe(context.Background(), &documents.DescribeDocumentInput{Name: "MyDoc"})
	require.NoError(t, err)
	assert.Equal(t, "2", desc.Document.DefaultVersion)

	w = f.post("/document/delete", url.Values{"name": {"MyDoc"}})
	assert.Equal(t, http.StatusSeeOther, w.Code)
	_, err = f.docs.Describe(context.Background(), &documents.DescribeDocumentInput{Name: "MyDoc"})
	assert.Error(t, err)
}

func TestAmazonDocumentIsReadOnlyInUI(t *testing.T) {
	f := newFixture(t, false)
	w := f.get("/document?name=AWS-RunShellScript")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "Create new version")
}
