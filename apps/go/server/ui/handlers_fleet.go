package ui

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/commands"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

func (h *handler) fleetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /instances", h.handleInstances)
	mux.HandleFunc("POST /instances/deregister", h.handleDeregister)
	mux.HandleFunc("POST /instances/tags", h.handleInstanceTags)
	mux.HandleFunc("POST /activations", h.handleCreateActivation)
	mux.HandleFunc("POST /activations/delete", h.handleDeleteActivation)
	mux.HandleFunc("GET /commands", h.handleCommands)
	mux.HandleFunc("GET /commands/new", h.handleCommandNew)
	mux.HandleFunc("POST /commands", h.handleSendCommand)
	mux.HandleFunc("GET /command", h.handleCommandDetail)
	mux.HandleFunc("POST /command/cancel", h.handleCancelCommand)
}

type instanceRow struct {
	*instances.InstanceInformation
	Tags []types.Tag
}

type instancesData struct {
	Instances   []instanceRow
	Activations []*instances.Activation
	Created     *instances.CreateActivationOutput
	ServerURL   string
}

func (h *handler) instancesData(r *http.Request) (*instancesData, error) {
	d := &instancesData{ServerURL: h.EndpointURL}
	in := &instances.DescribeInstanceInformationInput{MaxResults: 50}
	for {
		out, err := h.Instances.DescribeInstanceInformation(r.Context(), in)
		if err != nil {
			return d, err
		}
		for _, info := range out.InstanceInformationList {
			tagList, _ := h.Tags.List(tags.ResourceManagedInstance, info.InstanceID)
			d.Instances = append(d.Instances, instanceRow{InstanceInformation: info, Tags: tagList})
		}
		if out.NextToken == "" {
			break
		}
		in.NextToken = out.NextToken
	}
	acts, err := h.Instances.DescribeActivations(r.Context(), &instances.DescribeActivationsInput{})
	if err != nil {
		return d, err
	}
	d.Activations = acts.ActivationList
	return d, nil
}

func (h *handler) handleInstances(w http.ResponseWriter, r *http.Request) {
	d, err := h.instancesData(r)
	p := h.newPage(r, "Fleet", "instances", d)
	if err != nil {
		p.Error = errorMessage(err)
	}
	h.render(w, "instances.html", p)
}

func (h *handler) handleCreateActivation(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.FormValue("limit"))
	role := strings.TrimSpace(r.FormValue("iamRole"))
	if role == "" {
		role = "OpenSSMManagedInstanceRole"
	}
	in := &instances.CreateActivationInput{
		Description: r.FormValue("description"), DefaultInstanceName: r.FormValue("defaultInstanceName"),
		IamRole: role, RegistrationLimit: limit,
	}
	if hours, err := strconv.Atoi(r.FormValue("expiresHours")); err == nil && hours > 0 {
		in.ExpirationDate = types.NewTimestamp(types.Now().Add(time.Duration(hours) * time.Hour))
	}
	out, err := h.Instances.CreateActivation(r.Context(), in)
	if err != nil {
		redirect(w, r, "/instances", "error", errorMessage(err))
		return
	}
	d, err := h.instancesData(r)
	d.Created = out
	p := h.newPage(r, "Fleet", "instances", d)
	p.Success = "Activation created. Copy the activation code now — it won't be shown again."
	if err != nil {
		p.Error = errorMessage(err)
	}
	h.render(w, "instances.html", p)
}

func (h *handler) handleDeleteActivation(w http.ResponseWriter, r *http.Request) {
	if err := h.Instances.DeleteActivation(r.Context(), &instances.DeleteActivationInput{ActivationID: r.FormValue("id")}); err != nil {
		redirect(w, r, "/instances", "error", errorMessage(err))
		return
	}
	redirect(w, r, "/instances", "success", "Activation deleted")
}

func (h *handler) handleDeregister(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if err := h.Instances.Deregister(r.Context(), &instances.DeregisterManagedInstanceInput{InstanceID: id}); err != nil {
		redirect(w, r, "/instances", "error", errorMessage(err))
		return
	}
	redirect(w, r, "/instances", "success", "Deregistered "+id)
}

func (h *handler) handleInstanceTags(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	key := strings.TrimSpace(r.FormValue("key"))
	var err error
	if r.FormValue("action") == "remove" {
		err = h.Tags.Remove(tags.ResourceManagedInstance, id, []string{key})
	} else {
		err = h.Tags.Add(tags.ResourceManagedInstance, id, []types.Tag{{Key: key, Value: r.FormValue("value")}})
	}
	if err != nil {
		redirect(w, r, "/instances", "error", errorMessage(err))
		return
	}
	redirect(w, r, "/instances", "success", "Tags updated on "+id)
}

func (h *handler) handleCommands(w http.ResponseWriter, r *http.Request) {
	out, err := h.Commands.List(r.Context(), &commands.ListCommandsInput{MaxResults: 50})
	p := h.newPage(r, "Run Command", "commands", out)
	if err != nil {
		p.Error = errorMessage(err)
	}
	h.render(w, "commands.html", p)
}

type commandFormData struct {
	Documents []string
	Document  string
	Instances []*instances.InstanceInformation
	Selected  map[string]bool
	Commands  string
}

func (h *handler) handleCommandNew(w http.ResponseWriter, r *http.Request) {
	d := &commandFormData{Document: documents.DocRunShellScript, Selected: map[string]bool{}}
	for _, id := range r.URL.Query()["instance"] {
		d.Selected[id] = true
	}
	docs, err := h.Documents.List(r.Context(), &documents.ListDocumentsInput{
		Filters: []documents.DocumentKeyValuesFilter{{Key: "DocumentType", Values: []string{documents.TypeCommand}}},
	})
	if err == nil {
		for _, doc := range docs.DocumentIdentifiers {
			d.Documents = append(d.Documents, doc.Name)
		}
	}
	insts, err := h.Instances.DescribeInstanceInformation(r.Context(), &instances.DescribeInstanceInformationInput{})
	if err == nil {
		d.Instances = insts.InstanceInformationList
	}
	h.render(w, "command_form.html", h.newPage(r, "Run a command", "commands", d))
}

func (h *handler) handleSendCommand(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirect(w, r, "/commands/new", "error", "Invalid form data")
		return
	}
	in := &commands.SendCommandInput{
		DocumentName: r.FormValue("document"),
		Comment:      r.FormValue("comment"),
		InstanceIDs:  r.Form["instance"],
		Parameters:   map[string][]string{},
	}
	if tagKey := strings.TrimSpace(r.FormValue("tagKey")); tagKey != "" {
		in.InstanceIDs = nil
		in.Targets = []types.Target{{Key: "tag:" + tagKey, Values: []string{r.FormValue("tagValue")}}}
	}
	if cmds := strings.TrimSpace(r.FormValue("commands")); cmds != "" {
		in.Parameters["commands"] = strings.Split(strings.ReplaceAll(cmds, "\r\n", "\n"), "\n")
	}
	for _, line := range strings.Split(r.FormValue("parameters"), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && k != "" {
			in.Parameters[strings.TrimSpace(k)] = append(in.Parameters[strings.TrimSpace(k)], strings.TrimSpace(v))
		}
	}
	if t, err := strconv.Atoi(r.FormValue("executionTimeout")); err == nil && t > 0 {
		in.Parameters["executionTimeout"] = []string{strconv.Itoa(t)}
	}
	out, err := h.Commands.Send(r.Context(), in)
	if err != nil {
		redirect(w, r, "/commands/new", "error", errorMessage(err))
		return
	}
	redirect(w, r, "/command?id="+url.QueryEscape(out.Command.CommandID), "success", "Command sent")
}

type commandDetailData struct {
	Command     *commands.Command
	Invocations []*commands.CommandInvocation
	Active      bool
}

func (h *handler) handleCommandDetail(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	list, err := h.Commands.List(r.Context(), &commands.ListCommandsInput{CommandID: id})
	if err != nil || len(list.Commands) == 0 {
		redirect(w, r, "/commands", "error", "Command not found")
		return
	}
	invs, err := h.Commands.ListInvocations(r.Context(), &commands.ListCommandInvocationsInput{CommandID: id, Details: true})
	d := &commandDetailData{Command: list.Commands[0]}
	if err == nil {
		d.Invocations = invs.CommandInvocations
	}
	switch d.Command.Status {
	case commands.StatusPending, commands.StatusInProgress, commands.StatusCancelling:
		d.Active = true
	}
	p := h.newPage(r, "Command "+id, "commands", d)
	if d.Active {
		p.Refresh = 3
	}
	h.render(w, "command.html", p)
}

func (h *handler) handleCancelCommand(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	back := "/command?id=" + url.QueryEscape(id)
	if err := h.Commands.Cancel(r.Context(), &commands.CancelCommandInput{CommandID: id}); err != nil {
		redirect(w, r, back, "error", errorMessage(err))
		return
	}
	redirect(w, r, back, "success", "Cancellation requested")
}
