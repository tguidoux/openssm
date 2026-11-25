package ui

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/credentials"
	"github.com/tguidoux/openssm/pkgs/v1/logger"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/commands"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/kms"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/parameters"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// pages lists every page template; each is parsed together with layout.html.
var pages = []string{
	"parameters.html",
	"parameter.html",
	"parameter_form.html",
	"documents.html",
	"document.html",
	"document_form.html",
	"instances.html",
	"commands.html",
	"command_form.html",
	"command.html",
	"keys.html",
	"credentials.html",
	"metrics.html",
}

var templates = mustParseTemplates()

var funcs = template.FuncMap{
	"pathEscape":  url.PathEscape,
	"queryEscape": url.QueryEscape,
	"fmtTime": func(t *types.Timestamp) string {
		if t == nil {
			return ""
		}
		return t.Local().Format("2006-01-02 15:04:05")
	},
	"fmtGoTime": func(t time.Time) string { return t.Local().Format("2006-01-02 15:04:05") },
	"join":      strings.Join,
	"list":      func(items ...string) []string { return items },
	"statusClass": func(status string) string {
		switch status {
		case "Success", "Online":
			return "badge-success"
		case "Failed", "TimedOut", "ConnectionLost", "Cancelled":
			return "badge-danger"
		case "InProgress", "Pending", "Cancelling", "Delayed":
			return "badge-warning"
		}
		return "badge-standard"
	},
}

func mustParseTemplates() map[string]*template.Template {
	layout, err := templatesFS.ReadFile("templates/layout.html")
	if err != nil {
		panic(fmt.Sprintf("failed to read layout template: %v", err))
	}
	out := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		body, err := templatesFS.ReadFile("templates/" + page)
		if err != nil {
			panic(fmt.Sprintf("failed to read template %s: %v", page, err))
		}
		t := template.Must(template.New("layout").Funcs(funcs).Parse(string(layout)))
		out[page] = template.Must(t.New(page).Parse(string(body)))
	}
	return out
}

// Deps are the services and settings the UI needs.
type Deps struct {
	Parameters     *parameters.Service
	Documents      *documents.Service
	Instances      *instances.Service
	Commands       *commands.Service
	KMS            *kms.Service
	Tags           *tags.Service
	Credentials    credentials.CredentialStore
	EndpointURL    string
	Region         string
	AccountID      string
	MetricsEnabled bool
}

type handler struct {
	Deps
	log logger.LoggerInterface
}

func newHandler(d Deps, log logger.LoggerInterface) *handler {
	return &handler{Deps: d, log: log}
}

func (h *handler) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/parameters", http.StatusSeeOther)
	})
	h.parameterRoutes(mux)
	if h.Documents != nil {
		h.documentRoutes(mux)
	}
	if h.Instances != nil && h.Commands != nil {
		h.fleetRoutes(mux)
	}
	h.keyRoutes(mux)
	h.credentialRoutes(mux)
	mux.HandleFunc("GET /metrics", h.handleMetrics)
	mux.HandleFunc("GET /api/metrics", h.handleAPIMetrics)
}

// page is the data passed to every template.
type page struct {
	Title          string
	Nav            string
	Error          string
	Success        string
	MetricsEnabled bool
	CLIEnvVars     string
	// Refresh, when > 0, reloads the page every N seconds (live status pages).
	Refresh int
	Data    any
}

func (h *handler) newPage(r *http.Request, title, nav string, data any) *page {
	return &page{
		Title:          title,
		Nav:            nav,
		Error:          r.URL.Query().Get("error"),
		Success:        r.URL.Query().Get("success"),
		MetricsEnabled: h.MetricsEnabled,
		CLIEnvVars: fmt.Sprintf("export AWS_ENDPOINT_URL=%q\nexport AWS_DEFAULT_REGION=%q",
			h.EndpointURL, h.Region),
		Data: data,
	}
}

func (h *handler) render(w http.ResponseWriter, name string, p *page) {
	t, ok := templates[name]
	if !ok {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", p); err != nil {
		h.log.Errorf("template execution failed for %s: %v", name, err)
	}
}

// redirect sends the browser to target with a flash message.
func redirect(w http.ResponseWriter, r *http.Request, target, kind, msg string) {
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	if msg != "" {
		target += sep + kind + "=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// errorMessage renders an SSM error as "Code: message".
func errorMessage(err error) string {
	e := types.AsSSMError(err)
	return e.Code() + ": " + e.Message()
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
