// Package instances implements hybrid activations and managed instances:
// CreateActivation/DescribeActivations/DeleteActivation, agent registration
// with an activation code, heartbeats, DescribeInstanceInformation, and
// DeregisterManagedInstance.
package instances

import (
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

// Ping statuses.
const (
	PingOnline         = "Online"
	PingConnectionLost = "ConnectionLost"
	PingInactive       = "Inactive"
)

// Platform types.
const (
	PlatformLinux   = "Linux"
	PlatformMacOS   = "MacOS"
	PlatformWindows = "Windows"
)

const (
	resourceTypeManagedInstance = "ManagedInstance"
	sourceTypeManagedInstance   = "AWS::SSM::ManagedInstance"
	defaultActivationTTL        = 24 * time.Hour
	maxActivationTTL            = 30 * 24 * time.Hour
	maxRegistrationLimit        = 1000
)

type activation struct {
	ActivationID        string    `json:"activationId"`
	CodeHash            string    `json:"codeHash"`
	Description         string    `json:"description,omitempty"`
	DefaultInstanceName string    `json:"defaultInstanceName,omitempty"`
	IamRole             string    `json:"iamRole"`
	RegistrationLimit   int       `json:"registrationLimit"`
	RegistrationsCount  int       `json:"registrationsCount"`
	ExpirationDate      time.Time `json:"expirationDate"`
	CreatedDate         time.Time `json:"createdDate"`
}

func (a *activation) expired(now time.Time) bool { return !now.Before(a.ExpirationDate) }

// Instance is a registered managed instance.
type Instance struct {
	InstanceID       string    `json:"instanceId"`
	TokenHash        string    `json:"tokenHash"`
	ActivationID     string    `json:"activationId"`
	IamRole          string    `json:"iamRole"`
	Name             string    `json:"name,omitempty"`
	ComputerName     string    `json:"computerName,omitempty"`
	IPAddress        string    `json:"ipAddress,omitempty"`
	PlatformType     string    `json:"platformType"`
	PlatformName     string    `json:"platformName,omitempty"`
	PlatformVersion  string    `json:"platformVersion,omitempty"`
	AgentVersion     string    `json:"agentVersion,omitempty"`
	RegistrationDate time.Time `json:"registrationDate"`
	LastPingDateTime time.Time `json:"lastPingDateTime"`
}

// --- API shapes ---

// CreateActivationInput is the CreateActivation request.
type CreateActivationInput struct {
	Description         string           `json:"Description,omitempty"`
	DefaultInstanceName string           `json:"DefaultInstanceName,omitempty"`
	IamRole             string           `json:"IamRole"`
	RegistrationLimit   int              `json:"RegistrationLimit,omitempty"`
	ExpirationDate      *types.Timestamp `json:"ExpirationDate,omitempty"`
	Tags                []types.Tag      `json:"Tags,omitempty"`
}

// CreateActivationOutput is the CreateActivation response.
type CreateActivationOutput struct {
	ActivationID   string `json:"ActivationId"`
	ActivationCode string `json:"ActivationCode"`
}

// DescribeActivationsFilter is the AWS DescribeActivationsFilter shape.
type DescribeActivationsFilter struct {
	FilterKey    string   `json:"FilterKey"`
	FilterValues []string `json:"FilterValues"`
}

// DescribeActivationsInput is the DescribeActivations request.
type DescribeActivationsInput struct {
	Filters    []DescribeActivationsFilter `json:"Filters,omitempty"`
	MaxResults int                         `json:"MaxResults,omitempty"`
	NextToken  string                      `json:"NextToken,omitempty"`
}

// Activation is the AWS Activation shape.
type Activation struct {
	ActivationID        string           `json:"ActivationId"`
	Description         string           `json:"Description,omitempty"`
	DefaultInstanceName string           `json:"DefaultInstanceName,omitempty"`
	IamRole             string           `json:"IamRole"`
	RegistrationLimit   int              `json:"RegistrationLimit"`
	RegistrationsCount  int              `json:"RegistrationsCount"`
	ExpirationDate      *types.Timestamp `json:"ExpirationDate,omitempty"`
	Expired             bool             `json:"Expired"`
	CreatedDate         *types.Timestamp `json:"CreatedDate,omitempty"`
	Tags                []types.Tag      `json:"Tags"`
}

// DescribeActivationsOutput is the DescribeActivations response.
type DescribeActivationsOutput struct {
	ActivationList []*Activation `json:"ActivationList"`
	NextToken      string        `json:"NextToken,omitempty"`
}

// DeleteActivationInput is the DeleteActivation request.
type DeleteActivationInput struct {
	ActivationID string `json:"ActivationId"`
}

// InstanceInformationFilter is the legacy InstanceInformationFilterList entry.
type InstanceInformationFilter struct {
	Key      string   `json:"key"`
	ValueSet []string `json:"valueSet"`
}

// InstanceInformationStringFilter is the Filters entry.
type InstanceInformationStringFilter struct {
	Key    string   `json:"Key"`
	Values []string `json:"Values"`
}

// DescribeInstanceInformationInput is the DescribeInstanceInformation request.
type DescribeInstanceInformationInput struct {
	InstanceInformationFilterList []InstanceInformationFilter       `json:"InstanceInformationFilterList,omitempty"`
	Filters                       []InstanceInformationStringFilter `json:"Filters,omitempty"`
	MaxResults                    int                               `json:"MaxResults,omitempty"`
	NextToken                     string                            `json:"NextToken,omitempty"`
}

// InstanceInformation is the AWS InstanceInformation shape.
type InstanceInformation struct {
	InstanceID       string           `json:"InstanceId"`
	PingStatus       string           `json:"PingStatus"`
	LastPingDateTime *types.Timestamp `json:"LastPingDateTime,omitempty"`
	AgentVersion     string           `json:"AgentVersion,omitempty"`
	IsLatestVersion  bool             `json:"IsLatestVersion"`
	PlatformType     string           `json:"PlatformType"`
	PlatformName     string           `json:"PlatformName,omitempty"`
	PlatformVersion  string           `json:"PlatformVersion,omitempty"`
	ActivationID     string           `json:"ActivationId,omitempty"`
	IamRole          string           `json:"IamRole,omitempty"`
	RegistrationDate *types.Timestamp `json:"RegistrationDate,omitempty"`
	ResourceType     string           `json:"ResourceType"`
	Name             string           `json:"Name,omitempty"`
	IPAddress        string           `json:"IPAddress,omitempty"`
	ComputerName     string           `json:"ComputerName,omitempty"`
	SourceID         string           `json:"SourceId"`
	SourceType       string           `json:"SourceType"`
}

// DescribeInstanceInformationOutput is the DescribeInstanceInformation response.
type DescribeInstanceInformationOutput struct {
	InstanceInformationList []*InstanceInformation `json:"InstanceInformationList"`
	NextToken               string                 `json:"NextToken,omitempty"`
}

// DeregisterManagedInstanceInput is the DeregisterManagedInstance request.
type DeregisterManagedInstanceInput struct {
	InstanceID string `json:"InstanceId"`
}
