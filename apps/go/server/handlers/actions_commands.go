package handlers

import (
	"github.com/tguidoux/openssm/pkgs/v1/ssm/commands"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
)

func registerInstances(h *Handler, s *instances.Service) {
	register(h, "CreateActivation", s.CreateActivation)
	register(h, "DescribeActivations", s.DescribeActivations)
	registerNoOutput(h, "DeleteActivation", s.DeleteActivation)
	register(h, "DescribeInstanceInformation", s.DescribeInstanceInformation)
	registerNoOutput(h, "DeregisterManagedInstance", s.Deregister)
}

func registerCommands(h *Handler, s *commands.Service) {
	register(h, "SendCommand", s.Send)
	register(h, "ListCommands", s.List)
	register(h, "ListCommandInvocations", s.ListInvocations)
	register(h, "GetCommandInvocation", s.GetInvocation)
	registerNoOutput(h, "CancelCommand", s.Cancel)
}
