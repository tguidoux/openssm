package ui

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/tguidoux/openssm/pkgs/v1/credentials"
)

// --- KMS keys ---

func (h *handler) keyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /keys", h.handleKeys)
	mux.HandleFunc("POST /keys", h.handleCreateKey)
}

func (h *handler) handleKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.KMS.ListKeys()
	p := h.newPage(r, "KMS keys", "keys", keys)
	if err != nil {
		p.Error = errorMessage(err)
	}
	h.render(w, "keys.html", p)
}

func (h *handler) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	alias := strings.TrimSpace(r.FormValue("alias"))
	if alias != "" && !strings.HasPrefix(alias, "alias/") {
		alias = "alias/" + alias
	}
	k, err := h.KMS.CreateKey(r.FormValue("description"), alias)
	if err != nil {
		redirect(w, r, "/keys", "error", errorMessage(err))
		return
	}
	redirect(w, r, "/keys", "success", "Created key "+k.KeyID)
}

// --- Credentials ---

func (h *handler) credentialRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /credentials", h.handleCredentials)
	mux.HandleFunc("POST /credentials", h.handleCreateCredential)
	mux.HandleFunc("POST /credentials/{id}/delete", h.handleDeleteCredential)
}

type credentialView struct {
	ID              string
	Label           string
	AccessKeyID     string
	SecretAccessKey string
	Region          string
	AccountID       string
	CreatedAt       string
	ExportCommands  string
}

type credentialsData struct {
	Credentials []credentialView
	Created     *credentialView
	Region      string
	AccountID   string
}

func newCredentialView(c *credentials.Credential, endpoint string, withSecret bool) credentialView {
	v := credentialView{
		ID: c.ID, Label: c.Label, AccessKeyID: c.AccessKeyID, Region: c.Region,
		AccountID: c.AccountID, CreatedAt: c.CreatedAt.Local().Format("2006-01-02 15:04:05"),
	}
	if withSecret {
		v.SecretAccessKey = c.SecretAccessKey
		v.ExportCommands = fmt.Sprintf("export AWS_ACCESS_KEY_ID=%q\nexport AWS_SECRET_ACCESS_KEY=%q\nexport AWS_DEFAULT_REGION=%q\nexport AWS_ENDPOINT_URL=%q",
			c.AccessKeyID, c.SecretAccessKey, c.Region, endpoint)
	}
	return v
}

func (h *handler) credentialsData(created *credentials.Credential) (*credentialsData, error) {
	creds, err := h.Credentials.List()
	d := &credentialsData{Region: h.Region, AccountID: h.AccountID}
	for _, c := range creds {
		d.Credentials = append(d.Credentials, newCredentialView(c, h.EndpointURL, false))
	}
	if created != nil {
		v := newCredentialView(created, h.EndpointURL, true)
		d.Created = &v
	}
	return d, err
}

func (h *handler) handleCredentials(w http.ResponseWriter, r *http.Request) {
	d, err := h.credentialsData(nil)
	p := h.newPage(r, "Credentials", "credentials", d)
	if err != nil {
		p.Error = err.Error()
	}
	h.render(w, "credentials.html", p)
}

func (h *handler) handleCreateCredential(w http.ResponseWriter, r *http.Request) {
	region := strings.TrimSpace(r.FormValue("region"))
	if region == "" {
		region = h.Region
	}
	account := strings.TrimSpace(r.FormValue("accountId"))
	if account == "" {
		account = h.AccountID
	}
	cred, err := h.Credentials.Create(r.FormValue("label"), region, account)
	if err != nil {
		redirect(w, r, "/credentials", "error", "Failed to create credential: "+err.Error())
		return
	}
	h.log.Infof("UI: created credential %q (label=%s)", cred.ID, cred.Label)
	d, _ := h.credentialsData(cred)
	p := h.newPage(r, "Credentials", "credentials", d)
	p.Success = "Credential created. Copy the secret access key now — it won't be shown again."
	h.render(w, "credentials.html", p)
}

func (h *handler) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	if err := h.Credentials.Delete(r.PathValue("id")); err != nil {
		redirect(w, r, "/credentials", "error", "Failed to delete credential: "+err.Error())
		return
	}
	redirect(w, r, "/credentials", "success", "Credential deleted")
}

// --- Metrics ---

type metricRow struct {
	Labels string
	Value  float64
}

type metricFamily struct {
	Name string
	Help string
	Rows []metricRow
}

func (h *handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !h.MetricsEnabled {
		http.NotFound(w, r)
		return
	}
	h.render(w, "metrics.html", h.newPage(r, "Metrics", "metrics", collectMetrics()))
}

func (h *handler) handleAPIMetrics(w http.ResponseWriter, r *http.Request) {
	if !h.MetricsEnabled {
		writeJSONError(w, "metrics not enabled", http.StatusNotFound)
		return
	}
	writeJSON(w, collectMetrics())
}

// collectMetrics summarizes openssm_* metrics from the default registry.
func collectMetrics() []metricFamily {
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return nil
	}
	var out []metricFamily
	for _, mf := range mfs {
		if !strings.HasPrefix(mf.GetName(), "openssm_") {
			continue
		}
		f := metricFamily{Name: mf.GetName(), Help: mf.GetHelp()}
		for _, m := range mf.GetMetric() {
			f.Rows = append(f.Rows, metricRow{Labels: labelString(m), Value: metricValue(m)})
		}
		sort.Slice(f.Rows, func(i, j int) bool { return f.Rows[i].Labels < f.Rows[j].Labels })
		out = append(out, f)
	}
	return out
}

func labelString(m *dto.Metric) string {
	parts := make([]string, 0, len(m.GetLabel()))
	for _, lp := range m.GetLabel() {
		parts = append(parts, lp.GetName()+"="+lp.GetValue())
	}
	return strings.Join(parts, ", ")
}

func metricValue(m *dto.Metric) float64 {
	switch {
	case m.GetCounter() != nil:
		return m.GetCounter().GetValue()
	case m.GetGauge() != nil:
		return m.GetGauge().GetValue()
	case m.GetHistogram() != nil:
		return float64(m.GetHistogram().GetSampleCount())
	}
	return 0
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func parseInt64(s string) (int64, error) { return strconv.ParseInt(strings.TrimSpace(s), 10, 64) }
