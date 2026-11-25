// Package agentapi serves the OpenSSM-specific endpoints used by
// openssm-agent: registration with a hybrid activation, heartbeats,
// long-polling for commands/sessions, and command result reporting.
// Requests (except register) authenticate with the instance bearer token.
package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/agenthub"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/commands"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

const maxBodySize = 4 << 20 // results can carry large stdout/stderr

// SessionSource provides pending Session Manager work for an instance.
type SessionSource interface {
	PendingFor(instanceID string) ([]agentproto.SessionPayload, error)
}

type instanceKey struct{}

// InstanceID returns the authenticated instance ID from the request context.
func InstanceID(ctx context.Context) string {
	id, _ := ctx.Value(instanceKey{}).(string)
	return id
}

// Handler serves the agent API.
type Handler struct {
	instances *instances.Service
	commands  *commands.Service
	sessions  SessionSource
	hub       *agenthub.Hub
	log       logger.LoggerInterface
	mux       *http.ServeMux
}

// New creates the agent API handler. sessions may be nil.
func New(inst *instances.Service, cmds *commands.Service, sessions SessionSource, hub *agenthub.Hub, log logger.LoggerInterface) *Handler {
	h := &Handler{instances: inst, commands: cmds, sessions: sessions, hub: hub, log: log, mux: http.NewServeMux()}
	h.mux.HandleFunc("POST "+agentproto.PathRegister, h.handleRegister)
	h.mux.Handle("POST "+agentproto.PathHeartbeat, h.authenticated(h.handleHeartbeat))
	h.mux.Handle("POST "+agentproto.PathPoll, h.authenticated(h.handlePoll))
	h.mux.Handle("POST "+agentproto.PathResult+"{commandId}", h.authenticated(h.handleResult))
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// InstanceFromRequest authenticates the bearer token of r and returns the
// instance ID (used by the session data channel endpoint).
func (h *Handler) InstanceFromRequest(r *http.Request) (string, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return "", errors.New("missing bearer token")
	}
	return h.instances.Authenticate(token)
}

func (h *Handler) authenticated(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		instanceID, err := h.InstanceFromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Unauthorized", "invalid or revoked agent token")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), instanceKey{}, instanceID)))
	})
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req agentproto.RegisterRequest
	if !decode(w, r, &req) {
		return
	}
	out, err := h.instances.Register(r.Context(), &req)
	if err != nil {
		e := types.AsSSMError(err)
		status := http.StatusBadRequest
		if e.HTTPStatusCode() >= 500 {
			status = http.StatusInternalServerError
		}
		writeError(w, status, e.Code(), e.Message())
		return
	}
	h.log.Info("agent registered", map[string]any{"instanceId": out.InstanceID, "activationId": req.ActivationID, "computerName": req.ComputerName})
	writeJSON(w, out)
}

func (h *Handler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req agentproto.HeartbeatRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.instances.Heartbeat(InstanceID(r.Context()), &req.AgentInfo); err != nil {
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePoll long-polls until work is available or the wait expires.
func (h *Handler) handlePoll(w http.ResponseWriter, r *http.Request) {
	var req agentproto.PollRequest
	if !decode(w, r, &req) {
		return
	}
	instanceID := InstanceID(r.Context())
	wait := time.Duration(req.WaitSeconds) * time.Second
	if wait <= 0 || wait > agentproto.MaxPollWait {
		wait = agentproto.MaxPollWait
	}
	// Polling counts as a heartbeat so instances stay Online while idle.
	if err := h.instances.Heartbeat(instanceID, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		wake := h.hub.Subscribe(instanceID)
		// Claiming marks work as delivered, so never claim for a client
		// that has already gone away.
		if r.Context().Err() != nil {
			return
		}
		resp, err := h.collect(instanceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if len(resp.Commands) > 0 || len(resp.Cancellations) > 0 || len(resp.Sessions) > 0 {
			writeJSON(w, resp)
			return
		}
		select {
		case <-wake:
		case <-deadline.C:
			writeJSON(w, resp)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Handler) collect(instanceID string) (*agentproto.PollResponse, error) {
	cmds, cancels, err := h.commands.Claim(instanceID)
	if err != nil {
		return nil, err
	}
	resp := &agentproto.PollResponse{Commands: cmds, Cancellations: cancels, Sessions: []agentproto.SessionPayload{}}
	if resp.Commands == nil {
		resp.Commands = []agentproto.CommandPayload{}
	}
	if resp.Cancellations == nil {
		resp.Cancellations = []string{}
	}
	if h.sessions != nil {
		sessions, err := h.sessions.PendingFor(instanceID)
		if err != nil {
			return nil, err
		}
		resp.Sessions = append(resp.Sessions, sessions...)
	}
	return resp, nil
}

func (h *Handler) handleResult(w http.ResponseWriter, r *http.Request) {
	var res agentproto.CommandResult
	if !decode(w, r, &res) {
		return
	}
	commandID := r.PathValue("commandId")
	if err := h.commands.Report(InstanceID(r.Context()), commandID, &res); err != nil {
		e := types.AsSSMError(err)
		writeError(w, http.StatusBadRequest, e.Code(), e.Message())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "BadRequest", "request body too large")
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, out); err != nil {
		var syntax *json.SyntaxError
		msg := err.Error()
		if errors.As(err, &syntax) {
			msg = "malformed JSON"
		}
		writeError(w, http.StatusBadRequest, "BadRequest", msg)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(agentproto.Error{Code: code, Message: msg})
}
