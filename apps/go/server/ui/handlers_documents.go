package ui

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
)

func (h *handler) documentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /documents", h.handleDocuments)
	mux.HandleFunc("GET /documents/new", h.handleDocumentNew)
	mux.HandleFunc("POST /documents", h.handleDocumentSave)
	mux.HandleFunc("GET /document", h.handleDocumentDetail)
	mux.HandleFunc("GET /document/edit", h.handleDocumentEdit)
	mux.HandleFunc("POST /document/delete", h.handleDocumentDelete)
	mux.HandleFunc("POST /document/default", h.handleDocumentDefault)
}

type documentsData struct {
	Owner     string
	Documents []*documents.DocumentIdentifier
}

func (h *handler) handleDocuments(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("owner")
	if owner == "" {
		owner = "Self"
	}
	in := &documents.ListDocumentsInput{MaxResults: 50}
	if owner != "All" {
		in.Filters = []documents.DocumentKeyValuesFilter{{Key: "Owner", Values: []string{owner}}}
	}
	var all []*documents.DocumentIdentifier
	for {
		out, err := h.Documents.List(r.Context(), in)
		if err != nil {
			p := h.newPage(r, "Documents", "documents", &documentsData{Owner: owner})
			p.Error = errorMessage(err)
			h.render(w, "documents.html", p)
			return
		}
		all = append(all, out.DocumentIdentifiers...)
		if out.NextToken == "" {
			break
		}
		in.NextToken = out.NextToken
	}
	h.render(w, "documents.html", h.newPage(r, "Documents", "documents", &documentsData{Owner: owner, Documents: all}))
}

type documentDetailData struct {
	Doc      *documents.DocumentDescription
	Content  string
	Versions []*documents.DocumentVersionInfo
	Version  string
}

func (h *handler) handleDocumentDetail(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	ver := r.URL.Query().Get("version")
	desc, err := h.Documents.Describe(r.Context(), &documents.DescribeDocumentInput{Name: name, DocumentVersion: ver})
	if err != nil {
		redirect(w, r, "/documents", "error", errorMessage(err))
		return
	}
	got, err := h.Documents.Get(r.Context(), &documents.GetDocumentInput{Name: name, DocumentVersion: desc.Document.DocumentVersion})
	if err != nil {
		redirect(w, r, "/documents", "error", errorMessage(err))
		return
	}
	versions, _ := h.Documents.ListVersions(r.Context(), &documents.ListDocumentVersionsInput{Name: name, MaxResults: 50})
	data := &documentDetailData{Doc: desc.Document, Content: got.Content, Version: desc.Document.DocumentVersion}
	if versions != nil {
		data.Versions = versions.DocumentVersions
	}
	h.render(w, "document.html", h.newPage(r, name, "documents", data))
}

type documentFormData struct {
	Edit        bool
	Name        string
	Type        string
	Format      string
	VersionName string
	Content     string
}

const documentTemplate = `{
  "schemaVersion": "2.2",
  "description": "Describe what this document does",
  "parameters": {
    "message": {"type": "String", "default": "hello"}
  },
  "mainSteps": [
    {
      "action": "aws:runShellScript",
      "name": "run",
      "inputs": {"runCommand": ["echo {{ message }}"]}
    }
  ]
}`

func (h *handler) handleDocumentNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, "document_form.html", h.newPage(r, "Create document", "documents", &documentFormData{
		Type: documents.TypeCommand, Format: documents.FormatJSON, Content: documentTemplate,
	}))
}

func (h *handler) handleDocumentEdit(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	got, err := h.Documents.Get(r.Context(), &documents.GetDocumentInput{Name: name, DocumentVersion: "$LATEST"})
	if err != nil {
		redirect(w, r, "/documents", "error", errorMessage(err))
		return
	}
	h.render(w, "document_form.html", h.newPage(r, "Edit "+name, "documents", &documentFormData{
		Edit: true, Name: name, Type: got.DocumentType, Format: got.DocumentFormat, Content: got.Content,
	}))
}

func (h *handler) handleDocumentSave(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	content := r.FormValue("content")
	format := r.FormValue("format")
	versionName := strings.TrimSpace(r.FormValue("versionName"))
	var err error
	if r.FormValue("edit") == "1" {
		_, err = h.Documents.Update(r.Context(), &documents.UpdateDocumentInput{
			Name: name, Content: content, DocumentFormat: format, VersionName: versionName, DocumentVersion: "$LATEST",
		})
	} else {
		_, err = h.Documents.Create(r.Context(), &documents.CreateDocumentInput{
			Name: name, Content: content, DocumentFormat: format, DocumentType: r.FormValue("type"), VersionName: versionName,
		})
	}
	if err != nil {
		back := "/documents/new"
		if r.FormValue("edit") == "1" {
			back = "/document/edit?name=" + url.QueryEscape(name)
		}
		redirect(w, r, back, "error", errorMessage(err))
		return
	}
	redirect(w, r, "/document?name="+url.QueryEscape(name)+"&version=$LATEST", "success", "Document saved")
}

func (h *handler) handleDocumentDelete(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	in := &documents.DeleteDocumentInput{Name: name, DocumentVersion: r.FormValue("version")}
	if err := h.Documents.Delete(r.Context(), in); err != nil {
		redirect(w, r, "/document?name="+url.QueryEscape(name), "error", errorMessage(err))
		return
	}
	if in.DocumentVersion != "" {
		redirect(w, r, "/document?name="+url.QueryEscape(name), "success", "Deleted version "+in.DocumentVersion)
		return
	}
	redirect(w, r, "/documents", "success", "Deleted "+name)
}

func (h *handler) handleDocumentDefault(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	ver := r.FormValue("version")
	back := "/document?name=" + url.QueryEscape(name)
	if _, err := h.Documents.UpdateDefaultVersion(r.Context(), &documents.UpdateDocumentDefaultVersionInput{Name: name, DocumentVersion: ver}); err != nil {
		redirect(w, r, back, "error", errorMessage(err))
		return
	}
	redirect(w, r, back, "success", "Default version set to "+ver)
}
