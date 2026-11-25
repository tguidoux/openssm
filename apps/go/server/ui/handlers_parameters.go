package ui

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/parameters"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

func (h *handler) parameterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /parameters", h.handleParameters)
	mux.HandleFunc("GET /parameters/new", h.handleParameterNew)
	mux.HandleFunc("POST /parameters", h.handleParameterSave)
	mux.HandleFunc("GET /parameter", h.handleParameterDetail)
	mux.HandleFunc("GET /parameter/edit", h.handleParameterEdit)
	mux.HandleFunc("POST /parameter/delete", h.handleParameterDelete)
	mux.HandleFunc("POST /parameter/label", h.handleParameterLabel)
	mux.HandleFunc("POST /parameter/tags", h.handleParameterTags)
}

type parametersData struct {
	Filter     string
	Parameters []*parameters.ParameterMetadata
	Count      int
}

// handleParameters lists parameters, optionally filtered by a name substring.
func (h *handler) handleParameters(w http.ResponseWriter, r *http.Request) {
	filter := strings.TrimSpace(r.URL.Query().Get("q"))
	in := &parameters.DescribeParametersInput{MaxResults: 50}
	if filter != "" {
		in.ParameterFilters = []parameters.StringFilter{{Key: "Name", Option: "Contains", Values: []string{filter}}}
	}
	var all []*parameters.ParameterMetadata
	for {
		out, err := h.Parameters.Describe(r.Context(), in)
		if err != nil {
			p := h.newPage(r, "Parameters", "parameters", &parametersData{Filter: filter})
			p.Error = errorMessage(err)
			h.render(w, "parameters.html", p)
			return
		}
		all = append(all, out.Parameters...)
		if out.NextToken == "" {
			break
		}
		in.NextToken = out.NextToken
	}
	parameters.SortByName(all)
	h.render(w, "parameters.html", h.newPage(r, "Parameters", "parameters", &parametersData{
		Filter: filter, Parameters: all, Count: len(all),
	}))
}

type parameterDetailData struct {
	Meta    *parameters.ParameterMetadata
	Value   string
	Reveal  bool
	History []*parameters.ParameterHistory
	Tags    []types.Tag
	CLI     string
}

func (h *handler) handleParameterDetail(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	reveal := r.URL.Query().Get("reveal") == "1"
	meta, err := h.describeOne(r, name)
	if err != nil {
		redirect(w, r, "/parameters", "error", errorMessage(err))
		return
	}
	got, err := h.Parameters.Get(r.Context(), &parameters.GetParameterInput{Name: name, WithDecryption: reveal})
	if err != nil {
		redirect(w, r, "/parameters", "error", errorMessage(err))
		return
	}
	var history []*parameters.ParameterHistory
	in := &parameters.GetParameterHistoryInput{Name: name, WithDecryption: reveal, MaxResults: 50}
	for {
		out, err := h.Parameters.History(r.Context(), in)
		if err != nil {
			break
		}
		history = append(history, out.Parameters...)
		if out.NextToken == "" {
			break
		}
		in.NextToken = out.NextToken
	}
	sort.Slice(history, func(i, j int) bool { return history[i].Version > history[j].Version })
	tagList, _ := h.Tags.List(tags.ResourceParameter, name)

	cli := "aws ssm get-parameter --name " + shellQuote(name)
	if meta.Type == parameters.TypeSecureString {
		cli += " --with-decryption"
	}
	h.render(w, "parameter.html", h.newPage(r, name, "parameters", &parameterDetailData{
		Meta: meta, Value: got.Parameter.Value, Reveal: reveal, History: history, Tags: tagList, CLI: cli,
	}))
}

type parameterFormData struct {
	Edit        bool
	Name        string
	Description string
	Type        string
	Tier        string
	DataType    string
	KeyID       string
	Value       string
	Pattern     string
	Keys        []string
}

func (h *handler) handleParameterNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, "parameter_form.html", h.newPage(r, "Create parameter", "parameters", &parameterFormData{
		Type: parameters.TypeString, Tier: parameters.TierStandard, DataType: parameters.DataTypeText,
		Name: r.URL.Query().Get("prefix"), Keys: h.keyRefs(),
	}))
}

func (h *handler) handleParameterEdit(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	meta, err := h.describeOne(r, name)
	if err != nil {
		redirect(w, r, "/parameters", "error", errorMessage(err))
		return
	}
	got, err := h.Parameters.Get(r.Context(), &parameters.GetParameterInput{Name: name, WithDecryption: true})
	if err != nil {
		redirect(w, r, "/parameters", "error", errorMessage(err))
		return
	}
	h.render(w, "parameter_form.html", h.newPage(r, "Edit "+name, "parameters", &parameterFormData{
		Edit: true, Name: name, Description: meta.Description, Type: meta.Type, Tier: meta.Tier,
		DataType: meta.DataType, KeyID: meta.KeyID, Value: got.Parameter.Value, Pattern: meta.AllowedPattern,
		Keys: h.keyRefs(),
	}))
}

func (h *handler) handleParameterSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirect(w, r, "/parameters", "error", "Invalid form data")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	value := r.FormValue("value")
	desc := r.FormValue("description")
	pattern := r.FormValue("allowedPattern")
	in := &parameters.PutParameterInput{
		Name:           name,
		Value:          &value,
		Type:           r.FormValue("type"),
		Tier:           r.FormValue("tier"),
		DataType:       r.FormValue("dataType"),
		Description:    &desc,
		AllowedPattern: &pattern,
		Overwrite:      r.FormValue("edit") == "1",
	}
	if in.Type == parameters.TypeSecureString {
		in.KeyID = r.FormValue("keyId")
	}
	out, err := h.Parameters.Put(r.Context(), in)
	if err != nil {
		back := "/parameters/new"
		if in.Overwrite {
			back = "/parameter/edit?name=" + url.QueryEscape(name)
		}
		redirect(w, r, back, "error", errorMessage(err))
		return
	}
	h.log.Infof("UI: put parameter %q version %d", name, out.Version)
	redirect(w, r, "/parameter?name="+url.QueryEscape(name), "success", "Saved version "+itoa(out.Version))
}

func (h *handler) handleParameterDelete(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := h.Parameters.Delete(r.Context(), &parameters.DeleteParameterInput{Name: name}); err != nil {
		redirect(w, r, "/parameter?name="+url.QueryEscape(name), "error", errorMessage(err))
		return
	}
	redirect(w, r, "/parameters", "success", "Deleted "+name)
}

func (h *handler) handleParameterLabel(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	back := "/parameter?name=" + url.QueryEscape(name)
	version, err := parseInt64(r.FormValue("version"))
	if err != nil {
		redirect(w, r, back, "error", "Invalid version")
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if r.FormValue("action") == "remove" {
		_, err = h.Parameters.Unlabel(r.Context(), &parameters.UnlabelParameterVersionInput{Name: name, ParameterVersion: &version, Labels: []string{label}})
	} else {
		var out *parameters.LabelParameterVersionOutput
		out, err = h.Parameters.Label(r.Context(), &parameters.LabelParameterVersionInput{Name: name, ParameterVersion: &version, Labels: []string{label}})
		if err == nil && len(out.InvalidLabels) > 0 {
			redirect(w, r, back, "error", "Invalid label: "+strings.Join(out.InvalidLabels, ", "))
			return
		}
	}
	if err != nil {
		redirect(w, r, back, "error", errorMessage(err))
		return
	}
	redirect(w, r, back, "success", "Labels updated")
}

func (h *handler) handleParameterTags(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	back := "/parameter?name=" + url.QueryEscape(name)
	key := strings.TrimSpace(r.FormValue("key"))
	var err error
	if r.FormValue("action") == "remove" {
		err = h.Tags.Remove(tags.ResourceParameter, name, []string{key})
	} else {
		err = h.Tags.Add(tags.ResourceParameter, name, []types.Tag{{Key: key, Value: r.FormValue("value")}})
	}
	if err != nil {
		redirect(w, r, back, "error", errorMessage(err))
		return
	}
	redirect(w, r, back, "success", "Tags updated")
}

func (h *handler) describeOne(r *http.Request, name string) (*parameters.ParameterMetadata, error) {
	out, err := h.Parameters.Describe(r.Context(), &parameters.DescribeParametersInput{
		ParameterFilters: []parameters.StringFilter{{Key: "Name", Option: "Equals", Values: []string{name}}},
	})
	if err != nil {
		return nil, err
	}
	if len(out.Parameters) == 0 {
		return nil, types.NewParameterNotFound(name)
	}
	return out.Parameters[0], nil
}

// keyRefs returns KMS aliases (or key IDs without alias) for the key picker.
func (h *handler) keyRefs() []string {
	keys, err := h.KMS.ListKeys()
	if err != nil {
		return nil
	}
	var out []string
	for _, k := range keys {
		if len(k.Aliases) == 0 {
			out = append(out, k.KeyID)
		}
		out = append(out, k.Aliases...)
	}
	sort.Strings(out)
	return out
}

func shellQuote(s string) string {
	if strings.ContainsAny(s, " '\"$`\\") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}
