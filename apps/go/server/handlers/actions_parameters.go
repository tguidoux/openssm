package handlers

import (
	"github.com/tguidoux/openssm/pkgs/v1/ssm/parameters"
)

func registerParameters(h *Handler, s *parameters.Service) {
	register(h, "PutParameter", s.Put)
	register(h, "GetParameter", s.Get)
	register(h, "GetParameters", s.GetMany)
	register(h, "GetParametersByPath", s.GetByPath)
	register(h, "DescribeParameters", s.Describe)
	register(h, "GetParameterHistory", s.History)
	registerNoOutput(h, "DeleteParameter", s.Delete)
	register(h, "DeleteParameters", s.DeleteMany)
	register(h, "LabelParameterVersion", s.Label)
	register(h, "UnlabelParameterVersion", s.Unlabel)
}
