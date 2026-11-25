package handlers

import (
	"github.com/tguidoux/openssm/pkgs/v1/ssm/sessions"
)

func registerSessions(h *Handler, s *sessions.Service) {
	register(h, "StartSession", s.Start)
	register(h, "ResumeSession", s.Resume)
	register(h, "TerminateSession", s.Terminate)
	register(h, "DescribeSessions", s.Describe)
}
