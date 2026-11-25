// Package handlers dispatches SSM API actions (identified by X-Amz-Target)
// to the domain services. Each action is registered with a typed function
// that receives the decoded request struct and returns the response struct.
package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/tguidoux/openssm/apps/go/server/metrics"
	"github.com/tguidoux/openssm/apps/go/server/protocol"
	"github.com/tguidoux/openssm/pkgs/v1/id"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/parameters"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

// maxRequestBodySize bounds request bodies (largest SSM input is a 64 KB
// document plus envelope).
const maxRequestBodySize = 1 << 20

// actionFunc handles one decoded action and returns the response value.
type actionFunc func(ctx context.Context, body []byte) (any, error)

// Services groups the domain services the API exposes. Nil services leave
// their actions unregistered (InvalidAction).
type Services struct {
	Parameters *parameters.Service
	Documents  *documents.Service
	Tags       *tags.Service
}

// Handler is the SSM API http.Handler.
type Handler struct {
	actions map[string]actionFunc
	metrics *metrics.Collector
	log     logger.LoggerInterface
}

// NewHandler builds the action registry from the given services.
func NewHandler(svc Services, m *metrics.Collector, log logger.LoggerInterface) *Handler {
	h := &Handler{actions: make(map[string]actionFunc), metrics: m, log: log}
	if svc.Parameters != nil {
		registerParameters(h, svc.Parameters)
	}
	if svc.Tags != nil {
		registerTags(h, svc.Tags)
	}
	if svc.Documents != nil {
		registerDocuments(h, svc.Documents)
	}
	return h
}

// Actions returns the sorted list of supported action names.
func (h *Handler) Actions() []string {
	out := make([]string, 0, len(h.actions))
	for a := range h.actions {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := id.NewUUID()
	start := time.Now()

	if r.Method != http.MethodPost {
		protocol.WriteError(w, requestID, types.NewError("UnknownOperationException", "SSM API requests must use POST"))
		return
	}
	action, err := protocol.ParseAction(r.Header.Get(protocol.TargetHeader))
	if err != nil {
		protocol.WriteError(w, requestID, err)
		return
	}
	defer func() {
		if h.metrics != nil {
			h.metrics.IncAPIRequest(action)
			h.metrics.ObserveAPIRequestDuration(action, time.Since(start).Seconds())
		}
	}()

	fn, ok := h.actions[action]
	if !ok {
		h.fail(w, requestID, action, types.NewInvalidAction(action))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodySize))
	if err != nil {
		h.fail(w, requestID, action, types.NewValidationError("Request body too large or unreadable"))
		return
	}
	resp, err := fn(r.Context(), body)
	if err != nil {
		h.fail(w, requestID, action, err)
		return
	}
	if resp == nil {
		resp = struct{}{}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		h.fail(w, requestID, action, types.NewInternalError("failed to marshal response"))
		return
	}
	protocol.WriteJSON(w, requestID, data)
}

func (h *Handler) fail(w http.ResponseWriter, requestID, action string, err error) {
	e := types.AsSSMError(err)
	if h.metrics != nil {
		h.metrics.IncAPIError(action, e.Code())
	}
	if e.HTTPStatusCode() >= 500 {
		h.log.Error("ssm action failed", map[string]any{"requestId": requestID, "action": action, "error": err.Error()})
	} else {
		h.log.Debug("ssm action rejected", map[string]any{"requestId": requestID, "action": action, "code": e.Code()})
	}
	protocol.WriteError(w, requestID, e)
}

// register adds a typed action. In is decoded from the body; the result is
// marshalled as the response.
func register[In any, Out any](h *Handler, action string, fn func(ctx context.Context, in *In) (Out, error)) {
	h.actions[action] = func(ctx context.Context, body []byte) (any, error) {
		in := new(In)
		if err := protocol.Decode(body, in); err != nil {
			return nil, err
		}
		return fn(ctx, in)
	}
}

// registerNoOutput adds a typed action whose response is "{}".
func registerNoOutput[In any](h *Handler, action string, fn func(ctx context.Context, in *In) error) {
	register(h, action, func(ctx context.Context, in *In) (struct{}, error) {
		return struct{}{}, fn(ctx, in)
	})
}
