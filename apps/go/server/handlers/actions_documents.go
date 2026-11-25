package handlers

import (
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
)

func registerDocuments(h *Handler, s *documents.Service) {
	register(h, "CreateDocument", s.Create)
	register(h, "UpdateDocument", s.Update)
	register(h, "GetDocument", s.Get)
	register(h, "DescribeDocument", s.Describe)
	register(h, "ListDocuments", s.List)
	register(h, "ListDocumentVersions", s.ListVersions)
	register(h, "UpdateDocumentDefaultVersion", s.UpdateDefaultVersion)
	registerNoOutput(h, "DeleteDocument", s.Delete)
}
