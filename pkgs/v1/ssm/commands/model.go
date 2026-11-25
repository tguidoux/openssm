// Package commands implements Run Command: SendCommand fan-out to managed
// instances, per-instance invocations with their plugin results, the status
// state machine, delivery/execution timeouts, and cancellation.
package commands

import (
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

// Command and invocation statuses.
const (
	StatusPending    = "Pending"
	StatusInProgress = "InProgress"
	StatusDelayed    = "Delayed"
	StatusSuccess    = "Success"
	StatusCancelled  = "Cancelled"
	StatusFailed     = "Failed"
	StatusTimedOut   = "TimedOut"
	StatusCancelling = "Cancelling"
)

// Limits matching AWS Run Command.
const (
	MaxOutputChars                 = 24000
	DefaultDeliveryTimeout         = 3600
	MinDeliveryTimeout             = 30
	MaxDeliveryTimeout             = 2592000
	MaxTargetInstances             = 50
	defaultExecTimeout             = 3600
	statusDetailsDeliveryTimedOut  = "DeliveryTimedOut"
	statusDetailsExecutionTimedOut = "ExecutionTimedOut"
)

func isTerminal(status string) bool {
	switch status {
	case StatusSuccess, StatusCancelled, StatusFailed, StatusTimedOut:
		return true
	}
	return false
}

// command is the persisted command.
type command struct {
	CommandID          string                   `json:"commandId"`
	DocumentName       string                   `json:"documentName"`
	DocumentVersion    string                   `json:"documentVersion"`
	Comment            string                   `json:"comment,omitempty"`
	Parameters         map[string][]string      `json:"parameters"`
	InstanceIDs        []string                 `json:"instanceIds"`
	Targets            []types.Target           `json:"targets,omitempty"`
	RequestedDateTime  time.Time                `json:"requestedDateTime"`
	ExpiresAfter       time.Time                `json:"expiresAfter"`
	TimeoutSeconds     int                      `json:"timeoutSeconds"`
	MaxConcurrency     string                   `json:"maxConcurrency"`
	MaxErrors          string                   `json:"maxErrors"`
	ServiceRole        string                   `json:"serviceRole,omitempty"`
	OutputS3BucketName string                   `json:"outputS3BucketName,omitempty"`
	OutputS3KeyPrefix  string                   `json:"outputS3KeyPrefix,omitempty"`
	Steps              []agentproto.StepPayload `json:"steps"`
	Cancelled          bool                     `json:"cancelled,omitempty"`
}

// invocation is the persisted per-instance execution.
type invocation struct {
	CommandID         string                    `json:"commandId"`
	InstanceID        string                    `json:"instanceId"`
	InstanceName      string                    `json:"instanceName,omitempty"`
	Status            string                    `json:"status"`
	StatusDetails     string                    `json:"statusDetails"`
	RequestedDateTime time.Time                 `json:"requestedDateTime"`
	Delivered         bool                      `json:"delivered"`
	DeliveredAt       time.Time                 `json:"deliveredAt,omitempty"`
	CancelRequested   bool                      `json:"cancelRequested,omitempty"`
	CancelDelivered   bool                      `json:"cancelDelivered,omitempty"`
	Plugins           []agentproto.PluginResult `json:"plugins,omitempty"`
	FinishedAt        time.Time                 `json:"finishedAt,omitempty"`
}

// --- API shapes ---

// SendCommandInput is the SendCommand request.
type SendCommandInput struct {
	InstanceIDs        []string            `json:"InstanceIds,omitempty"`
	Targets            []types.Target      `json:"Targets,omitempty"`
	DocumentName       string              `json:"DocumentName"`
	DocumentVersion    string              `json:"DocumentVersion,omitempty"`
	DocumentHash       string              `json:"DocumentHash,omitempty"`
	DocumentHashType   string              `json:"DocumentHashType,omitempty"`
	TimeoutSeconds     int                 `json:"TimeoutSeconds,omitempty"`
	Comment            string              `json:"Comment,omitempty"`
	Parameters         map[string][]string `json:"Parameters,omitempty"`
	OutputS3Region     string              `json:"OutputS3Region,omitempty"`
	OutputS3BucketName string              `json:"OutputS3BucketName,omitempty"`
	OutputS3KeyPrefix  string              `json:"OutputS3KeyPrefix,omitempty"`
	MaxConcurrency     string              `json:"MaxConcurrency,omitempty"`
	MaxErrors          string              `json:"MaxErrors,omitempty"`
	ServiceRoleArn     string              `json:"ServiceRoleArn,omitempty"`
}

// Command is the AWS Command shape.
type Command struct {
	CommandID             string              `json:"CommandId"`
	DocumentName          string              `json:"DocumentName"`
	DocumentVersion       string              `json:"DocumentVersion"`
	Comment               string              `json:"Comment"`
	ExpiresAfter          *types.Timestamp    `json:"ExpiresAfter,omitempty"`
	Parameters            map[string][]string `json:"Parameters"`
	InstanceIDs           []string            `json:"InstanceIds"`
	Targets               []types.Target      `json:"Targets"`
	RequestedDateTime     *types.Timestamp    `json:"RequestedDateTime,omitempty"`
	Status                string              `json:"Status"`
	StatusDetails         string              `json:"StatusDetails"`
	OutputS3Region        string              `json:"OutputS3Region,omitempty"`
	OutputS3BucketName    string              `json:"OutputS3BucketName"`
	OutputS3KeyPrefix     string              `json:"OutputS3KeyPrefix"`
	MaxConcurrency        string              `json:"MaxConcurrency"`
	MaxErrors             string              `json:"MaxErrors"`
	TargetCount           int                 `json:"TargetCount"`
	CompletedCount        int                 `json:"CompletedCount"`
	ErrorCount            int                 `json:"ErrorCount"`
	DeliveryTimedOutCount int                 `json:"DeliveryTimedOutCount"`
	ServiceRole           string              `json:"ServiceRole,omitempty"`
	TimeoutSeconds        int                 `json:"TimeoutSeconds"`
	TriggeredAlarms       []any               `json:"TriggeredAlarms,omitempty"`
}

// SendCommandOutput is the SendCommand response.
type SendCommandOutput struct {
	Command *Command `json:"Command"`
}

// CommandFilter is the AWS CommandFilter shape.
type CommandFilter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ListCommandsInput is the ListCommands request.
type ListCommandsInput struct {
	CommandID  string          `json:"CommandId,omitempty"`
	InstanceID string          `json:"InstanceId,omitempty"`
	MaxResults int             `json:"MaxResults,omitempty"`
	NextToken  string          `json:"NextToken,omitempty"`
	Filters    []CommandFilter `json:"Filters,omitempty"`
}

// ListCommandsOutput is the ListCommands response.
type ListCommandsOutput struct {
	Commands  []*Command `json:"Commands"`
	NextToken string     `json:"NextToken,omitempty"`
}

// CommandPlugin is the AWS CommandPlugin shape.
type CommandPlugin struct {
	Name                   string           `json:"Name"`
	Status                 string           `json:"Status"`
	StatusDetails          string           `json:"StatusDetails"`
	ResponseCode           int              `json:"ResponseCode"`
	ResponseStartDateTime  *types.Timestamp `json:"ResponseStartDateTime,omitempty"`
	ResponseFinishDateTime *types.Timestamp `json:"ResponseFinishDateTime,omitempty"`
	Output                 string           `json:"Output"`
	StandardOutputURL      string           `json:"StandardOutputUrl"`
	StandardErrorURL       string           `json:"StandardErrorUrl"`
	OutputS3Region         string           `json:"OutputS3Region,omitempty"`
	OutputS3BucketName     string           `json:"OutputS3BucketName"`
	OutputS3KeyPrefix      string           `json:"OutputS3KeyPrefix"`
}

// CommandInvocation is the AWS CommandInvocation shape.
type CommandInvocation struct {
	CommandID         string           `json:"CommandId"`
	InstanceID        string           `json:"InstanceId"`
	InstanceName      string           `json:"InstanceName"`
	Comment           string           `json:"Comment"`
	DocumentName      string           `json:"DocumentName"`
	DocumentVersion   string           `json:"DocumentVersion"`
	RequestedDateTime *types.Timestamp `json:"RequestedDateTime,omitempty"`
	Status            string           `json:"Status"`
	StatusDetails     string           `json:"StatusDetails"`
	TraceOutput       string           `json:"TraceOutput"`
	StandardOutputURL string           `json:"StandardOutputUrl"`
	StandardErrorURL  string           `json:"StandardErrorUrl"`
	CommandPlugins    []*CommandPlugin `json:"CommandPlugins"`
	ServiceRole       string           `json:"ServiceRole,omitempty"`
}

// ListCommandInvocationsInput is the ListCommandInvocations request.
type ListCommandInvocationsInput struct {
	CommandID  string          `json:"CommandId,omitempty"`
	InstanceID string          `json:"InstanceId,omitempty"`
	MaxResults int             `json:"MaxResults,omitempty"`
	NextToken  string          `json:"NextToken,omitempty"`
	Filters    []CommandFilter `json:"Filters,omitempty"`
	Details    bool            `json:"Details,omitempty"`
}

// ListCommandInvocationsOutput is the ListCommandInvocations response.
type ListCommandInvocationsOutput struct {
	CommandInvocations []*CommandInvocation `json:"CommandInvocations"`
	NextToken          string               `json:"NextToken,omitempty"`
}

// GetCommandInvocationInput is the GetCommandInvocation request.
type GetCommandInvocationInput struct {
	CommandID  string `json:"CommandId"`
	InstanceID string `json:"InstanceId"`
	PluginName string `json:"PluginName,omitempty"`
}

// GetCommandInvocationOutput is the GetCommandInvocation response.
type GetCommandInvocationOutput struct {
	CommandID              string `json:"CommandId"`
	InstanceID             string `json:"InstanceId"`
	Comment                string `json:"Comment"`
	DocumentName           string `json:"DocumentName"`
	DocumentVersion        string `json:"DocumentVersion"`
	PluginName             string `json:"PluginName"`
	ResponseCode           int    `json:"ResponseCode"`
	ExecutionStartDateTime string `json:"ExecutionStartDateTime"`
	ExecutionElapsedTime   string `json:"ExecutionElapsedTime"`
	ExecutionEndDateTime   string `json:"ExecutionEndDateTime"`
	Status                 string `json:"Status"`
	StatusDetails          string `json:"StatusDetails"`
	StandardOutputContent  string `json:"StandardOutputContent"`
	StandardOutputURL      string `json:"StandardOutputUrl"`
	StandardErrorContent   string `json:"StandardErrorContent"`
	StandardErrorURL       string `json:"StandardErrorUrl"`
}

// CancelCommandInput is the CancelCommand request.
type CancelCommandInput struct {
	CommandID   string   `json:"CommandId"`
	InstanceIDs []string `json:"InstanceIds,omitempty"`
}
