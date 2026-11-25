// Package agentproto defines the JSON wire protocol between the OpenSSM
// server and openssm-agent. It has no dependencies so the agent binary stays
// small. All endpoints live under PathPrefix on the SSM API port; every
// endpoint except Register requires "Authorization: Bearer <token>".
package agentproto

import "time"

// Endpoint paths.
const (
	PathPrefix    = "/agent/v1/"
	PathRegister  = PathPrefix + "register"
	PathHeartbeat = PathPrefix + "heartbeat"
	PathPoll      = PathPrefix + "poll"
	// PathResult is followed by the command ID.
	PathResult = PathPrefix + "commands/"
	// PathSession is followed by the session ID (WebSocket upgrade).
	PathSession = PathPrefix + "sessions/"
)

// MaxPollWait bounds long-poll duration.
const MaxPollWait = 30 * time.Second

// AgentInfo is the host information an agent reports.
type AgentInfo struct {
	ComputerName    string `json:"computerName"`
	IPAddress       string `json:"ipAddress"`
	PlatformType    string `json:"platformType"`
	PlatformName    string `json:"platformName"`
	PlatformVersion string `json:"platformVersion"`
	AgentVersion    string `json:"agentVersion"`
}

// RegisterRequest registers an agent with a hybrid activation.
type RegisterRequest struct {
	ActivationID   string `json:"activationId"`
	ActivationCode string `json:"activationCode"`
	AgentInfo
}

// RegisterResponse returns the instance identity and its bearer token.
type RegisterResponse struct {
	InstanceID string `json:"instanceId"`
	Token      string `json:"token"`
}

// HeartbeatRequest refreshes the instance's ping status and host info.
type HeartbeatRequest struct {
	AgentInfo
}

// PollRequest long-polls for work.
type PollRequest struct {
	WaitSeconds int `json:"waitSeconds"`
}

// PollResponse carries work for the agent.
type PollResponse struct {
	Commands      []CommandPayload `json:"commands"`
	Cancellations []string         `json:"cancellations"`
	Sessions      []SessionPayload `json:"sessions"`
}

// CommandPayload is a command invocation to execute.
type CommandPayload struct {
	CommandID       string        `json:"commandId"`
	DocumentName    string        `json:"documentName"`
	DocumentVersion string        `json:"documentVersion"`
	Steps           []StepPayload `json:"steps"`
}

// StepPayload is one document step with parameters already interpolated.
type StepPayload struct {
	Name           string         `json:"name"`
	Action         string         `json:"action"`
	Inputs         map[string]any `json:"inputs"`
	Precondition   map[string]any `json:"precondition,omitempty"`
	TimeoutSeconds int            `json:"timeoutSeconds"`
}

// Plugin/invocation statuses reported by the agent.
const (
	StatusInProgress = "InProgress"
	StatusSuccess    = "Success"
	StatusFailed     = "Failed"
	StatusTimedOut   = "TimedOut"
	StatusCancelled  = "Cancelled"
)

// PluginResult is the outcome of one step.
type PluginResult struct {
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	ResponseCode int       `json:"responseCode"`
	Stdout       string    `json:"stdout"`
	Stderr       string    `json:"stderr"`
	StartTime    time.Time `json:"startTime"`
	EndTime      time.Time `json:"endTime"`
}

// CommandResult is reported when an invocation finishes (or to stream
// progress with Status InProgress).
type CommandResult struct {
	Status  string         `json:"status"`
	Plugins []PluginResult `json:"plugins"`
}

// SessionPayload asks the agent to open a Session Manager data channel.
type SessionPayload struct {
	SessionID    string            `json:"sessionId"`
	DocumentName string            `json:"documentName"`
	SessionType  string            `json:"sessionType"`
	Shell        string            `json:"shell,omitempty"`
	Command      string            `json:"command,omitempty"`
	RunAsUser    string            `json:"runAsUser,omitempty"`
	Parameters   map[string]string `json:"parameters,omitempty"`
}

// Error is the JSON body of non-2xx agent API responses.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
