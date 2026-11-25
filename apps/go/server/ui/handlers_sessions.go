package ui

import (
	"net/http"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/sessions"
)

func (h *handler) sessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /sessions", h.handleSessions)
	mux.HandleFunc("POST /sessions/terminate", h.handleTerminateSession)
}

type sessionsData struct {
	State    string
	Sessions []*sessions.Session
	Region   string
	Endpoint string
}

func (h *handler) handleSessions(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state != "History" {
		state = "Active"
	}
	out, err := h.Sessions.Describe(r.Context(), &sessions.DescribeSessionsInput{State: state, MaxResults: 200})
	d := &sessionsData{State: state, Region: h.Region, Endpoint: h.EndpointURL}
	if out != nil {
		d.Sessions = out.Sessions
	}
	p := h.newPage(r, "Session Manager", "sessions", d)
	if err != nil {
		p.Error = errorMessage(err)
	}
	if state == "Active" {
		p.Refresh = 5
	}
	h.render(w, "sessions.html", p)
}

func (h *handler) handleTerminateSession(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if _, err := h.Sessions.Terminate(r.Context(), &sessions.TerminateSessionInput{SessionID: id}); err != nil {
		redirect(w, r, "/sessions", "error", errorMessage(err))
		return
	}
	redirect(w, r, "/sessions", "success", "Terminated "+id)
}
