package commands

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/id"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/agenthub"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/documents"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/instances"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

const (
	bucketCommands    = "commands"
	bucketInvocations = "invocations"
	// bucketQueue indexes undelivered work per instance: "<instance>\x00<command>".
	bucketQueue = "command_queue"
	// bucketCancelQueue indexes cancellations awaiting delivery, keyed like
	// bucketQueue, so agent polls never scan the whole invocation history.
	bucketCancelQueue = "command_cancel_queue"
)

// Observer receives command lifecycle events (used for metrics).
type Observer interface {
	IncCommand(document string)
	IncInvocation(status string)
}

// Service implements Run Command.
type Service struct {
	store     kv.Store
	docs      *documents.Service
	instances *instances.Service
	hub       *agenthub.Hub
	observer  Observer
}

// New creates the Run Command service.
func New(store kv.Store, docs *documents.Service, inst *instances.Service, hub *agenthub.Hub, observer Observer) *Service {
	return &Service{store: store, docs: docs, instances: inst, hub: hub, observer: observer}
}

// Send creates a command and queues one invocation per target (SendCommand).
func (s *Service) Send(ctx context.Context, in *SendCommandInput) (*SendCommandOutput, error) {
	if in.DocumentName == "" {
		return nil, types.NewValidationError("1 validation error detected: Value null at 'documentName' failed to satisfy constraint: Member must not be null")
	}
	if len(in.InstanceIDs) == 0 && len(in.Targets) == 0 {
		return nil, types.NewValidationError("You must specify either InstanceIds or Targets.")
	}
	if len(in.InstanceIDs) > 0 && len(in.Targets) > 0 {
		return nil, types.NewValidationError("You can't specify both InstanceIds and Targets.")
	}
	if len(in.InstanceIDs) > MaxTargetInstances {
		return nil, types.NewValidationError("InstanceIds can contain at most %d instances.", MaxTargetInstances)
	}
	deliveryTimeout := in.TimeoutSeconds
	if deliveryTimeout == 0 {
		deliveryTimeout = DefaultDeliveryTimeout
	}
	if deliveryTimeout < MinDeliveryTimeout || deliveryTimeout > MaxDeliveryTimeout {
		return nil, types.NewValidationError("TimeoutSeconds must be between %d and %d.", MinDeliveryTimeout, MaxDeliveryTimeout)
	}
	maxConcurrency, maxErrors := defaultString(in.MaxConcurrency, "50"), defaultString(in.MaxErrors, "0")
	if err := validateRate(maxConcurrency, "MaxConcurrency"); err != nil {
		return nil, err
	}
	if err := validateRate(maxErrors, "MaxErrors"); err != nil {
		return nil, err
	}

	doc, err := s.docs.Resolve(in.DocumentName, in.DocumentVersion)
	if err != nil {
		return nil, err
	}
	if doc.DocumentType != documents.TypeCommand {
		return nil, types.NewError(types.ErrInvalidDocument, "Document %s is not a Command document.", in.DocumentName)
	}
	if in.DocumentHash != "" && in.DocumentHash != doc.Hash {
		return nil, types.NewError("InvalidDocumentHash", "The document hash doesn't match the document.")
	}
	params, err := documents.ResolveParameters(doc.Content, in.Parameters)
	if err != nil {
		return nil, err
	}
	steps, err := buildSteps(doc.Content, params)
	if err != nil {
		return nil, err
	}

	now := types.Now()
	cmd := &command{
		CommandID: id.NewUUID(), DocumentName: doc.Name, DocumentVersion: doc.Version, Comment: in.Comment,
		Parameters: params, Targets: in.Targets, RequestedDateTime: now,
		ExpiresAfter: now.Add(time.Duration(deliveryTimeout) * time.Second), TimeoutSeconds: deliveryTimeout,
		MaxConcurrency: maxConcurrency, MaxErrors: maxErrors, ServiceRole: in.ServiceRoleArn,
		OutputS3BucketName: in.OutputS3BucketName, OutputS3KeyPrefix: in.OutputS3KeyPrefix, Steps: steps,
	}
	var out *Command
	err = s.store.Update(func(tx kv.Tx) error {
		if len(in.InstanceIDs) > 0 {
			for _, iid := range in.InstanceIDs {
				inst, err := s.instances.GetTx(tx, iid)
				if err != nil {
					return err
				}
				if inst == nil {
					return types.NewInvalidInstanceID(iid)
				}
			}
			cmd.InstanceIDs = slices.Compact(slices.Sorted(slices.Values(in.InstanceIDs)))
		} else {
			cmd.InstanceIDs, err = s.instances.ResolveTargetsTx(tx, in.Targets)
			if err != nil {
				return err
			}
		}
		if err := kv.PutJSON(tx, bucketCommands, cmd.CommandID, cmd); err != nil {
			return err
		}
		invs := make([]*invocation, 0, len(cmd.InstanceIDs))
		for _, iid := range cmd.InstanceIDs {
			inst, err := s.instances.GetTx(tx, iid)
			if err != nil {
				return err
			}
			inv := &invocation{
				CommandID: cmd.CommandID, InstanceID: iid, Status: StatusPending, StatusDetails: StatusPending,
				RequestedDateTime: now,
			}
			if inst != nil {
				inv.InstanceName = inst.Name
			}
			if err := s.putInvocation(tx, inv); err != nil {
				return err
			}
			if err := tx.Put(bucketQueue, queueKey(iid, cmd.CommandID), nil); err != nil {
				return err
			}
			invs = append(invs, inv)
		}
		out = toCommand(cmd, invs)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, iid := range cmd.InstanceIDs {
		s.hub.Notify(iid)
	}
	if s.observer != nil {
		s.observer.IncCommand(cmd.DocumentName)
	}
	return &SendCommandOutput{Command: out}, nil
}

// List lists commands (ListCommands).
func (s *Service) List(ctx context.Context, in *ListCommandsInput) (*ListCommandsOutput, error) {
	var all []*Command
	err := s.store.View(func(r kv.Reader) error {
		cmds, err := s.loadCommands(r, in.CommandID)
		if err != nil {
			return err
		}
		for _, cmd := range cmds {
			if in.InstanceID != "" && !slices.Contains(cmd.InstanceIDs, in.InstanceID) {
				continue
			}
			invs, err := s.invocationsFor(r, cmd.CommandID)
			if err != nil {
				return err
			}
			c := toCommand(cmd, invs)
			ok, err := matchCommandFilters(cmd.RequestedDateTime, c.Status, cmd.DocumentName, in.Filters)
			if err != nil {
				return err
			}
			if ok {
				all = append(all, c)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	page, next, err := types.Paginate(all, in.NextToken, in.MaxResults, 50, 50)
	if err != nil {
		return nil, err
	}
	if page == nil {
		page = []*Command{}
	}
	return &ListCommandsOutput{Commands: page, NextToken: next}, nil
}

// ListInvocations lists invocations (ListCommandInvocations).
func (s *Service) ListInvocations(ctx context.Context, in *ListCommandInvocationsInput) (*ListCommandInvocationsOutput, error) {
	var all []*CommandInvocation
	err := s.store.View(func(r kv.Reader) error {
		cmds, err := s.loadCommands(r, in.CommandID)
		if err != nil {
			return err
		}
		for _, cmd := range cmds {
			invs, err := s.invocationsFor(r, cmd.CommandID)
			if err != nil {
				return err
			}
			for _, inv := range invs {
				if in.InstanceID != "" && inv.InstanceID != in.InstanceID {
					continue
				}
				ok, err := matchCommandFilters(inv.RequestedDateTime, inv.Status, cmd.DocumentName, in.Filters)
				if err != nil {
					return err
				}
				if ok {
					all = append(all, toInvocation(cmd, inv, in.Details))
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	page, next, err := types.Paginate(all, in.NextToken, in.MaxResults, 50, 50)
	if err != nil {
		return nil, err
	}
	if page == nil {
		page = []*CommandInvocation{}
	}
	return &ListCommandInvocationsOutput{CommandInvocations: page, NextToken: next}, nil
}

// GetInvocation returns one invocation's output (GetCommandInvocation).
func (s *Service) GetInvocation(ctx context.Context, in *GetCommandInvocationInput) (*GetCommandInvocationOutput, error) {
	var out *GetCommandInvocationOutput
	err := s.store.View(func(r kv.Reader) error {
		cmd, err := s.loadCommand(r, in.CommandID)
		if err != nil {
			return err
		}
		inv, err := s.loadInvocation(r, in.CommandID, in.InstanceID)
		if err != nil {
			return err
		}
		if inv == nil {
			return types.NewError(types.ErrInvocationDoesNotExist, "An error occurred (InvocationDoesNotExist) when calling the GetCommandInvocation operation")
		}
		var plugin *agentproto.PluginResult
		pluginName := in.PluginName
		if pluginName != "" {
			for i := range inv.Plugins {
				if inv.Plugins[i].Name == pluginName {
					plugin = &inv.Plugins[i]
				}
			}
			if plugin == nil && !slices.ContainsFunc(cmd.Steps, func(st agentproto.StepPayload) bool { return st.Name == pluginName }) {
				return types.NewError("InvalidPluginName", "The plugin name is not valid.")
			}
		} else if len(cmd.Steps) > 0 {
			pluginName = cmd.Steps[0].Name
			if len(cmd.Steps) > 1 {
				return types.NewValidationError("PluginName is required for documents with more than one step.")
			}
			if len(inv.Plugins) > 0 {
				plugin = &inv.Plugins[0]
			}
		}
		out = &GetCommandInvocationOutput{
			CommandID: cmd.CommandID, InstanceID: inv.InstanceID, Comment: cmd.Comment, DocumentName: cmd.DocumentName,
			DocumentVersion: cmd.DocumentVersion, PluginName: pluginName, ResponseCode: -1,
			Status: inv.Status, StatusDetails: inv.StatusDetails,
		}
		if plugin != nil {
			out.ResponseCode = plugin.ResponseCode
			out.StandardOutputContent = truncate(plugin.Stdout)
			out.StandardErrorContent = truncate(plugin.Stderr)
			if !plugin.StartTime.IsZero() {
				out.ExecutionStartDateTime = plugin.StartTime.UTC().Format(time.RFC3339Nano)
			}
			if !plugin.EndTime.IsZero() {
				out.ExecutionEndDateTime = plugin.EndTime.UTC().Format(time.RFC3339Nano)
				out.ExecutionElapsedTime = formatElapsed(plugin.EndTime.Sub(plugin.StartTime))
			}
			if !isTerminal(inv.Status) {
				out.Status, out.StatusDetails = StatusInProgress, StatusInProgress
			} else if plugin.Status != "" && inv.Status != StatusCancelled {
				out.Status, out.StatusDetails = plugin.Status, pluginDetails(plugin.Status)
			}
		}
		return nil
	})
	return out, err
}

// Cancel requests cancellation (CancelCommand).
func (s *Service) Cancel(ctx context.Context, in *CancelCommandInput) error {
	var notify []string
	err := s.store.Update(func(tx kv.Tx) error {
		cmd, err := s.loadCommand(tx, in.CommandID)
		if err != nil {
			return err
		}
		targets := in.InstanceIDs
		if len(targets) == 0 {
			targets = cmd.InstanceIDs
			cmd.Cancelled = true
			if err := kv.PutJSON(tx, bucketCommands, cmd.CommandID, cmd); err != nil {
				return err
			}
		}
		for _, iid := range targets {
			inv, err := s.loadInvocation(tx, cmd.CommandID, iid)
			if err != nil {
				return err
			}
			if inv == nil {
				return types.NewInvalidInstanceID(iid)
			}
			if isTerminal(inv.Status) {
				continue
			}
			if !inv.Delivered {
				// Never reached the agent: cancel immediately.
				if err := tx.Delete(bucketQueue, queueKey(iid, cmd.CommandID)); err != nil {
					return err
				}
				s.finish(inv, StatusCancelled, StatusCancelled)
			} else {
				inv.CancelRequested = true
				inv.Status, inv.StatusDetails = StatusCancelling, StatusCancelling
				if err := tx.Put(bucketCancelQueue, queueKey(iid, cmd.CommandID), nil); err != nil {
					return err
				}
				notify = append(notify, iid)
			}
			if err := s.putInvocation(tx, inv); err != nil {
				return err
			}
		}
		return nil
	})
	for _, iid := range notify {
		s.hub.Notify(iid)
	}
	return err
}

// --- agent-facing operations ---

// Claim marks queued invocations for instanceID as delivered and returns
// their payloads, plus command IDs whose cancellation must be delivered.
func (s *Service) Claim(instanceID string) ([]agentproto.CommandPayload, []string, error) {
	var payloads []agentproto.CommandPayload
	var cancels []string
	err := s.store.Update(func(tx kv.Tx) error {
		entries, err := tx.List(bucketQueue, instanceID+"\x00")
		if err != nil {
			return err
		}
		now := types.Now()
		for _, e := range entries {
			commandID := strings.TrimPrefix(e.Key, instanceID+"\x00")
			if err := tx.Delete(bucketQueue, e.Key); err != nil {
				return err
			}
			cmd, err := s.loadCommand(tx, commandID)
			if err != nil {
				continue
			}
			inv, err := s.loadInvocation(tx, commandID, instanceID)
			if err != nil || inv == nil || isTerminal(inv.Status) {
				continue
			}
			inv.Delivered, inv.DeliveredAt = true, now
			inv.Status, inv.StatusDetails = StatusInProgress, StatusInProgress
			if err := s.putInvocation(tx, inv); err != nil {
				return err
			}
			payloads = append(payloads, agentproto.CommandPayload{
				CommandID: cmd.CommandID, DocumentName: cmd.DocumentName, DocumentVersion: cmd.DocumentVersion, Steps: cmd.Steps,
			})
		}
		// Pending cancellations for delivered invocations.
		cancelEntries, err := tx.List(bucketCancelQueue, instanceID+"\x00")
		if err != nil {
			return err
		}
		for _, e := range cancelEntries {
			if err := tx.Delete(bucketCancelQueue, e.Key); err != nil {
				return err
			}
			inv, err := s.loadInvocation(tx, strings.TrimPrefix(e.Key, instanceID+"\x00"), instanceID)
			if err != nil || inv == nil || !inv.CancelRequested || inv.CancelDelivered || isTerminal(inv.Status) {
				continue
			}
			inv.CancelDelivered = true
			if err := s.putInvocation(tx, inv); err != nil {
				return err
			}
			cancels = append(cancels, inv.CommandID)
		}
		return nil
	})
	return payloads, cancels, err
}

// HasWork reports whether instanceID has queued commands or cancellations.
func (s *Service) HasWork(instanceID string) (bool, error) {
	found := false
	err := s.store.View(func(r kv.Reader) error {
		entries, err := r.List(bucketQueue, instanceID+"\x00")
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			found = true
			return nil
		}
		cancels, err := r.List(bucketCancelQueue, instanceID+"\x00")
		found = len(cancels) > 0
		return err
	})
	return found, err
}

// Report records progress or the final result of an invocation.
func (s *Service) Report(instanceID, commandID string, res *agentproto.CommandResult) error {
	var finalStatus string
	err := s.store.Update(func(tx kv.Tx) error {
		inv, err := s.loadInvocation(tx, commandID, instanceID)
		if err != nil {
			return err
		}
		if inv == nil {
			return types.NewError(types.ErrInvocationDoesNotExist, "invocation %s/%s does not exist", commandID, instanceID)
		}
		if isTerminal(inv.Status) {
			return nil
		}
		inv.Plugins = res.Plugins
		switch res.Status {
		case agentproto.StatusInProgress:
			if !inv.CancelRequested {
				inv.Status, inv.StatusDetails = StatusInProgress, StatusInProgress
			}
		case agentproto.StatusSuccess, agentproto.StatusFailed, agentproto.StatusCancelled:
			s.finish(inv, res.Status, res.Status)
			finalStatus = res.Status
		case agentproto.StatusTimedOut:
			s.finish(inv, StatusTimedOut, statusDetailsExecutionTimedOut)
			finalStatus = StatusTimedOut
		default:
			return types.NewValidationError("unknown status %q", res.Status)
		}
		return s.putInvocation(tx, inv)
	})
	if err == nil && finalStatus != "" && s.observer != nil {
		s.observer.IncInvocation(finalStatus)
	}
	return err
}

// Sweep times out invocations that were never delivered before the command
// expired, and executions that ran far past their step timeouts (agent lost).
func (s *Service) Sweep() (int, error) {
	timedOut := 0
	err := s.store.Update(func(tx kv.Tx) error {
		cmds, err := kv.ListJSON[command](tx, bucketCommands, "")
		if err != nil {
			return err
		}
		now := types.Now()
		for i := range cmds {
			cmd := &cmds[i]
			invs, err := s.invocationsFor(tx, cmd.CommandID)
			if err != nil {
				return err
			}
			for _, inv := range invs {
				if isTerminal(inv.Status) {
					continue
				}
				switch {
				case !inv.Delivered && !now.Before(cmd.ExpiresAfter):
					if err := tx.Delete(bucketQueue, queueKey(inv.InstanceID, cmd.CommandID)); err != nil {
						return err
					}
					s.finish(inv, StatusTimedOut, statusDetailsDeliveryTimedOut)
				case inv.Delivered && now.Sub(inv.DeliveredAt) > executionBudget(cmd):
					s.finish(inv, StatusTimedOut, statusDetailsExecutionTimedOut)
				default:
					continue
				}
				timedOut++
				if s.observer != nil {
					s.observer.IncInvocation(StatusTimedOut)
				}
				if err := s.putInvocation(tx, inv); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return timedOut, err
}

// --- internals ---

func (s *Service) finish(inv *invocation, status, details string) {
	inv.Status, inv.StatusDetails = status, details
	inv.FinishedAt = types.Now()
}

func invocationKey(commandID, instanceID string) string { return commandID + "\x00" + instanceID }

func queueKey(instanceID, commandID string) string { return instanceID + "\x00" + commandID }

func (s *Service) putInvocation(tx kv.Tx, inv *invocation) error {
	return kv.PutJSON(tx, bucketInvocations, invocationKey(inv.CommandID, inv.InstanceID), inv)
}

func (s *Service) loadCommand(r kv.Reader, commandID string) (*command, error) {
	var cmd command
	err := kv.GetJSON(r, bucketCommands, commandID, &cmd)
	if errors.Is(err, kv.ErrNotFound) {
		return nil, types.NewInvalidCommandID()
	}
	if err != nil {
		return nil, err
	}
	return &cmd, nil
}

// loadCommands returns one command (when commandID is set) or all commands
// newest first.
func (s *Service) loadCommands(r kv.Reader, commandID string) ([]*command, error) {
	if commandID != "" {
		cmd, err := s.loadCommand(r, commandID)
		if err != nil {
			return nil, err
		}
		return []*command{cmd}, nil
	}
	list, err := kv.ListJSON[command](r, bucketCommands, "")
	if err != nil {
		return nil, err
	}
	out := make([]*command, len(list))
	for i := range list {
		out[i] = &list[i]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].RequestedDateTime.After(out[j].RequestedDateTime) })
	return out, nil
}

func (s *Service) loadInvocation(r kv.Reader, commandID, instanceID string) (*invocation, error) {
	var inv invocation
	err := kv.GetJSON(r, bucketInvocations, invocationKey(commandID, instanceID), &inv)
	if errors.Is(err, kv.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (s *Service) invocationsFor(r kv.Reader, commandID string) ([]*invocation, error) {
	list, err := kv.ListJSON[invocation](r, bucketInvocations, commandID+"\x00")
	if err != nil {
		return nil, err
	}
	out := make([]*invocation, len(list))
	for i := range list {
		out[i] = &list[i]
	}
	return out, nil
}

// buildSteps interpolates parameters into each step's inputs.
func buildSteps(c *documents.Content, params map[string][]string) ([]agentproto.StepPayload, error) {
	steps := make([]agentproto.StepPayload, 0, len(c.MainSteps))
	for _, st := range c.MainSteps {
		inputs, _ := documents.Interpolate(st.Inputs, params).(map[string]any)
		timeout := st.TimeoutSeconds
		if v, ok := inputs["timeoutSeconds"]; ok {
			if n, err := strconv.Atoi(fmt.Sprint(v)); err == nil {
				timeout = n
			}
		}
		if timeout <= 0 {
			timeout = defaultExecTimeout
		}
		steps = append(steps, agentproto.StepPayload{
			Name: st.Name, Action: st.Action, Inputs: inputs, Precondition: st.Precondition, TimeoutSeconds: timeout,
		})
	}
	return steps, nil
}

// executionBudget is how long a delivered invocation may run before the
// server assumes the agent died (sum of step timeouts plus a grace period).
func executionBudget(cmd *command) time.Duration {
	total := 0
	for _, st := range cmd.Steps {
		total += st.TimeoutSeconds
	}
	return time.Duration(total)*time.Second + 5*time.Minute
}

func toCommand(cmd *command, invs []*invocation) *Command {
	c := &Command{
		CommandID: cmd.CommandID, DocumentName: cmd.DocumentName, DocumentVersion: cmd.DocumentVersion,
		Comment: cmd.Comment, ExpiresAfter: types.NewTimestamp(cmd.ExpiresAfter), Parameters: cmd.Parameters,
		InstanceIDs: cmd.InstanceIDs, Targets: cmd.Targets, RequestedDateTime: types.NewTimestamp(cmd.RequestedDateTime),
		OutputS3BucketName: cmd.OutputS3BucketName, OutputS3KeyPrefix: cmd.OutputS3KeyPrefix,
		MaxConcurrency: cmd.MaxConcurrency, MaxErrors: cmd.MaxErrors, TargetCount: len(invs),
		ServiceRole: cmd.ServiceRole, TimeoutSeconds: cmd.TimeoutSeconds,
	}
	if c.InstanceIDs == nil {
		c.InstanceIDs = []string{}
	}
	if c.Targets == nil {
		c.Targets = []types.Target{}
	}
	counts := map[string]int{}
	for _, inv := range invs {
		counts[inv.Status]++
		if isTerminal(inv.Status) {
			c.CompletedCount++
		}
		switch inv.Status {
		case StatusFailed:
			c.ErrorCount++
		case StatusTimedOut:
			c.ErrorCount++
			if inv.StatusDetails == statusDetailsDeliveryTimedOut {
				c.DeliveryTimedOutCount++
			}
		}
	}
	c.Status = aggregateStatus(cmd, invs, counts)
	c.StatusDetails = c.Status
	if c.Status == StatusCancelling {
		c.StatusDetails = StatusCancelling
	}
	return c
}

// aggregateStatus derives the command status from its invocations.
func aggregateStatus(cmd *command, invs []*invocation, counts map[string]int) string {
	n := len(invs)
	switch {
	case n == 0:
		return StatusSuccess
	case counts[StatusPending] == n:
		return StatusPending
	case cmd.Cancelled && counts[StatusCancelling] > 0:
		return StatusCancelling
	}
	terminal := counts[StatusSuccess] + counts[StatusFailed] + counts[StatusTimedOut] + counts[StatusCancelled]
	if terminal < n {
		return StatusInProgress
	}
	switch {
	case counts[StatusSuccess] == n:
		return StatusSuccess
	case cmd.Cancelled:
		return StatusCancelled
	case counts[StatusTimedOut] > 0 && counts[StatusFailed] == 0:
		return StatusTimedOut
	case counts[StatusFailed] > 0 || counts[StatusTimedOut] > 0:
		return StatusFailed
	default:
		return StatusCancelled
	}
}

func toInvocation(cmd *command, inv *invocation, details bool) *CommandInvocation {
	ci := &CommandInvocation{
		CommandID: cmd.CommandID, InstanceID: inv.InstanceID, InstanceName: inv.InstanceName, Comment: cmd.Comment,
		DocumentName: cmd.DocumentName, DocumentVersion: cmd.DocumentVersion,
		RequestedDateTime: types.NewTimestamp(inv.RequestedDateTime), Status: inv.Status, StatusDetails: inv.StatusDetails,
		CommandPlugins: []*CommandPlugin{}, ServiceRole: cmd.ServiceRole,
	}
	if !details {
		return ci
	}
	results := map[string]agentproto.PluginResult{}
	for _, p := range inv.Plugins {
		results[p.Name] = p
	}
	for _, st := range cmd.Steps {
		cp := &CommandPlugin{Name: st.Name, Status: StatusPending, StatusDetails: StatusPending, ResponseCode: -1,
			OutputS3BucketName: cmd.OutputS3BucketName, OutputS3KeyPrefix: cmd.OutputS3KeyPrefix}
		if inv.Status == StatusInProgress || inv.Status == StatusCancelling {
			cp.Status, cp.StatusDetails = StatusInProgress, StatusInProgress
		}
		if p, ok := results[st.Name]; ok {
			cp.Status, cp.StatusDetails = p.Status, pluginDetails(p.Status)
			cp.ResponseCode = p.ResponseCode
			cp.ResponseStartDateTime = types.NewTimestamp(p.StartTime)
			cp.ResponseFinishDateTime = types.NewTimestamp(p.EndTime)
			cp.Output = truncatePluginOutput(p.Stdout, p.Stderr)
		} else if isTerminal(inv.Status) {
			cp.Status, cp.StatusDetails = inv.Status, inv.StatusDetails
		}
		ci.CommandPlugins = append(ci.CommandPlugins, cp)
	}
	return ci
}

func matchCommandFilters(requested time.Time, status, docName string, filters []CommandFilter) (bool, error) {
	for _, f := range filters {
		switch f.Key {
		case "InvokedAfter", "InvokedBefore":
			t, err := time.Parse(time.RFC3339, f.Value)
			if err != nil {
				return false, types.NewError("InvalidFilterValue", "Invalid timestamp %q", f.Value)
			}
			if f.Key == "InvokedAfter" && requested.Before(t) || f.Key == "InvokedBefore" && requested.After(t) {
				return false, nil
			}
		case "Status":
			if !strings.EqualFold(status, f.Value) {
				return false, nil
			}
		case "DocumentName":
			if docName != f.Value {
				return false, nil
			}
		case "ExecutionStage":
			stage := "Executing"
			if isTerminal(status) {
				stage = "Complete"
			}
			if stage != f.Value {
				return false, nil
			}
		default:
			return false, types.NewError(types.ErrInvalidFilterKey, "The specified key is not valid: %s", f.Key)
		}
	}
	return true, nil
}

func pluginDetails(status string) string {
	if status == StatusTimedOut {
		return statusDetailsExecutionTimedOut
	}
	return status
}

func truncate(s string) string {
	if len(s) <= MaxOutputChars {
		return s
	}
	return s[:MaxOutputChars]
}

// truncatePluginOutput mimics the CommandPlugin.Output field (first 2500 chars).
func truncatePluginOutput(stdout, stderr string) string {
	out := stdout
	if stderr != "" {
		out += "\n----------ERROR-------\n" + stderr
	}
	if len(out) > 2500 {
		out = out[:2500] + "\n---Output truncated---"
	}
	return out
}

func formatElapsed(d time.Duration) string {
	d = d.Round(time.Millisecond)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := d.Seconds() - float64(h*3600+m*60)
	return fmt.Sprintf("PT%s", strings.TrimPrefix(fmt.Sprintf("%dH%dM%.3fS", h, m, sec), "0H0M"))
}

func defaultString(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func validateRate(v, field string) error {
	s := strings.TrimSuffix(v, "%")
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || (strings.HasSuffix(v, "%") && n > 100) {
		return types.NewValidationError("%s must be a number or percentage (got %q)", field, v)
	}
	return nil
}

// Stats returns the number of commands and how many are still active.
func (s *Service) Stats() (total, active int, err error) {
	err = s.store.View(func(r kv.Reader) error {
		invs, err := kv.ListJSON[invocation](r, bucketInvocations, "")
		for i := range invs {
			if !isTerminal(invs[i].Status) {
				active++
			}
		}
		cmds, err2 := r.List(bucketCommands, "")
		total = len(cmds)
		return errors.Join(err, err2)
	})
	return total, active, err
}
