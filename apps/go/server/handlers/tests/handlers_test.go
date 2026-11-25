package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/apps/go/server/handlers"
	"github.com/tguidoux/openssm/apps/go/server/metrics"
	"github.com/tguidoux/openssm/pkgs/v1/kv/memory"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/kms"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/parameters"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

func newHandler(t *testing.T) *handlers.Handler {
	t.Helper()
	store := memory.New()
	account := types.Account{AccountID: "123456789012", Region: "us-east-1"}
	k, err := kms.New(store, account, kms.Options{MasterKey: "m"})
	require.NoError(t, err)
	tg := tags.New(store)
	p := parameters.New(store, k, tg, account, parameters.Options{})
	d, err := documents.New(store, tg, account)
	require.NoError(t, err)
	m := metrics.NewCollectorWithRegistry(prometheus.NewRegistry())
	return handlers.NewHandler(handlers.Services{Parameters: p, Documents: d, Tags: tg}, m, logger.New("test", logger.UncontextualLoggerType))
}

func call(t *testing.T, h http.Handler, action, body string) (int, map[string]any, http.Header) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", types.JSONContentType)
	if action != "" {
		req.Header.Set("X-Amz-Target", "AmazonSSM."+action)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	out := map[string]any{}
	if w.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), w.Body.String())
	}
	return w.Code, out, w.Header()
}

func TestPutAndGetParameter(t *testing.T) {
	h := newHandler(t)
	code, out, hdr := call(t, h, "PutParameter", `{"Name":"/app/x","Value":"v","Type":"String"}`)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, float64(1), out["Version"])
	assert.Equal(t, "Standard", out["Tier"])
	assert.Equal(t, types.JSONContentType, hdr.Get("Content-Type"))
	assert.NotEmpty(t, hdr.Get("X-Amzn-RequestId"))

	code, out, _ = call(t, h, "GetParameter", `{"Name":"/app/x"}`)
	require.Equal(t, http.StatusOK, code)
	p := out["Parameter"].(map[string]any)
	assert.Equal(t, "v", p["Value"])
	_, isNumber := p["LastModifiedDate"].(float64)
	assert.True(t, isNumber, "timestamps are epoch seconds")
}

func TestErrorShape(t *testing.T) {
	h := newHandler(t)
	code, out, hdr := call(t, h, "GetParameter", `{"Name":"/missing"}`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "ParameterNotFound", out["__type"])
	assert.NotEmpty(t, out["message"])
	assert.Equal(t, "ParameterNotFound", hdr.Get("X-Amzn-ErrorType"))
}

func TestUnknownActionAndMissingTarget(t *testing.T) {
	h := newHandler(t)
	_, out, _ := call(t, h, "SendCommand", `{}`)
	assert.Equal(t, "InvalidAction", out["__type"])

	_, out, _ = call(t, h, "", `{}`)
	assert.Equal(t, "UnknownOperationException", out["__type"])
}

func TestMalformedJSON(t *testing.T) {
	h := newHandler(t)
	code, out, _ := call(t, h, "GetParameter", `{not json`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "SerializationException", out["__type"])
}

func TestEmptyBodyIsAccepted(t *testing.T) {
	h := newHandler(t)
	code, out, _ := call(t, h, "DescribeParameters", ``)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []any{}, out["Parameters"])
}

func TestDeleteParameterReturnsEmptyObject(t *testing.T) {
	h := newHandler(t)
	call(t, h, "PutParameter", `{"Name":"/d","Value":"v","Type":"String"}`)
	code, out, _ := call(t, h, "DeleteParameter", `{"Name":"/d"}`)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, out)
}

func TestTagActions(t *testing.T) {
	h := newHandler(t)
	call(t, h, "PutParameter", `{"Name":"/t","Value":"v","Type":"String"}`)
	code, _, _ := call(t, h, "AddTagsToResource", `{"ResourceType":"Parameter","ResourceId":"/t","Tags":[{"Key":"env","Value":"dev"}]}`)
	require.Equal(t, http.StatusOK, code)

	_, out, _ := call(t, h, "ListTagsForResource", `{"ResourceType":"Parameter","ResourceId":"/t"}`)
	assert.Equal(t, []any{map[string]any{"Key": "env", "Value": "dev"}}, out["TagList"])

	call(t, h, "RemoveTagsFromResource", `{"ResourceType":"Parameter","ResourceId":"/t","TagKeys":["env"]}`)
	_, out, _ = call(t, h, "ListTagsForResource", `{"ResourceType":"Parameter","ResourceId":"/t"}`)
	assert.Equal(t, []any{}, out["TagList"])
}

func TestGetRequiresPost(t *testing.T) {
	h := newHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestActionsRegistered(t *testing.T) {
	h := newHandler(t)
	for _, a := range []string{"PutParameter", "GetParameter", "GetParameters", "GetParametersByPath", "DescribeParameters",
		"GetParameterHistory", "DeleteParameter", "DeleteParameters", "LabelParameterVersion", "UnlabelParameterVersion",
		"AddTagsToResource", "RemoveTagsFromResource", "ListTagsForResource",
		"CreateDocument", "UpdateDocument", "GetDocument", "DescribeDocument", "ListDocuments",
		"ListDocumentVersions", "UpdateDocumentDefaultVersion", "DeleteDocument"} {
		assert.Contains(t, h.Actions(), a)
	}
}

func TestDocumentActions(t *testing.T) {
	h := newHandler(t)
	content, _ := json.Marshal(`{"schemaVersion":"2.2","mainSteps":[{"action":"aws:runShellScript","name":"a","inputs":{"runCommand":["true"]}}]}`)
	code, out, _ := call(t, h, "CreateDocument", `{"Name":"Doc1","DocumentType":"Command","Content":`+string(content)+`}`)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "Doc1", out["DocumentDescription"].(map[string]any)["Name"])

	_, out, _ = call(t, h, "GetDocument", `{"Name":"Doc1"}`)
	assert.Equal(t, "1", out["DocumentVersion"])

	_, out, _ = call(t, h, "ListDocuments", `{"Filters":[{"Key":"Owner","Values":["Self"]}]}`)
	assert.Len(t, out["DocumentIdentifiers"], 1)
}
