// Package parameters implements AWS Systems Manager Parameter Store:
// String, StringList, and SecureString parameters with versions, labels,
// hierarchies, tiers, policies, and tags. Request/response structs carry
// AWS JSON 1.1 field names so API handlers can (de)serialize them directly.
package parameters

import (
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

// Parameter types.
const (
	TypeString       = "String"
	TypeStringList   = "StringList"
	TypeSecureString = "SecureString"
)

// Parameter tiers.
const (
	TierStandard           = "Standard"
	TierAdvanced           = "Advanced"
	TierIntelligentTiering = "Intelligent-Tiering"
)

// Data types.
const (
	DataTypeText           = "text"
	DataTypeEC2Image       = "aws:ec2:image"
	DataTypeSSMIntegration = "aws:ssm:integration"
)

// AWS Parameter Store limits.
const (
	MaxStandardValueBytes = 4096
	MaxAdvancedValueBytes = 8192
	MaxNameLength         = 1011
	MaxHierarchyDepth     = 15
	MaxVersions           = 100
	MaxLabelsPerVersion   = 10
	MaxLabelLength        = 100
	MaxDescriptionLength  = 1024
	MaxAllowedPatternLen  = 1024
	MaxGetParametersNames = 10
)

// version is a persisted parameter version.
type version struct {
	Version          int64     `json:"version"`
	Type             string    `json:"type"`
	Value            string    `json:"value"` // ciphertext for SecureString
	KeyID            string    `json:"keyId,omitempty"`
	Description      string    `json:"description,omitempty"`
	AllowedPattern   string    `json:"allowedPattern,omitempty"`
	Tier             string    `json:"tier"`
	DataType         string    `json:"dataType"`
	Policies         []policy  `json:"policies,omitempty"`
	Labels           []string  `json:"labels,omitempty"`
	LastModifiedDate time.Time `json:"lastModifiedDate"`
	LastModifiedUser string    `json:"lastModifiedUser"`
}

// record is the persisted state of a parameter: all retained versions in
// ascending order. The last element is the current version.
type record struct {
	Name     string    `json:"name"`
	Versions []version `json:"versions"`
}

func (r *record) latest() *version { return &r.Versions[len(r.Versions)-1] }

func (r *record) findVersion(v int64) *version {
	for i := range r.Versions {
		if r.Versions[i].Version == v {
			return &r.Versions[i]
		}
	}
	return nil
}

func (r *record) findLabel(label string) *version {
	for i := range r.Versions {
		for _, l := range r.Versions[i].Labels {
			if l == label {
				return &r.Versions[i]
			}
		}
	}
	return nil
}

// policy is a parsed parameter policy.
type policy struct {
	Type       string            `json:"Type"`
	Version    string            `json:"Version"`
	Attributes map[string]string `json:"Attributes"`
	Text       string            `json:"text"`
}

// --- API shapes ---

// Parameter is the AWS Parameter shape.
type Parameter struct {
	Name             string           `json:"Name"`
	Type             string           `json:"Type"`
	Value            string           `json:"Value"`
	Version          int64            `json:"Version"`
	Selector         string           `json:"Selector,omitempty"`
	SourceResult     string           `json:"SourceResult,omitempty"`
	LastModifiedDate *types.Timestamp `json:"LastModifiedDate,omitempty"`
	ARN              string           `json:"ARN"`
	DataType         string           `json:"DataType"`
}

// ParameterInlinePolicy is the AWS ParameterInlinePolicy shape.
type ParameterInlinePolicy struct {
	PolicyText   string `json:"PolicyText"`
	PolicyType   string `json:"PolicyType"`
	PolicyStatus string `json:"PolicyStatus"`
}

// ParameterMetadata is the AWS ParameterMetadata shape (DescribeParameters).
type ParameterMetadata struct {
	Name             string                  `json:"Name"`
	ARN              string                  `json:"ARN"`
	Type             string                  `json:"Type"`
	KeyID            string                  `json:"KeyId,omitempty"`
	LastModifiedDate *types.Timestamp        `json:"LastModifiedDate,omitempty"`
	LastModifiedUser string                  `json:"LastModifiedUser,omitempty"`
	Description      string                  `json:"Description,omitempty"`
	AllowedPattern   string                  `json:"AllowedPattern,omitempty"`
	Version          int64                   `json:"Version"`
	Tier             string                  `json:"Tier"`
	Policies         []ParameterInlinePolicy `json:"Policies"`
	DataType         string                  `json:"DataType"`
}

// ParameterHistory is the AWS ParameterHistory shape.
type ParameterHistory struct {
	Name             string                  `json:"Name"`
	Type             string                  `json:"Type"`
	KeyID            string                  `json:"KeyId,omitempty"`
	LastModifiedDate *types.Timestamp        `json:"LastModifiedDate,omitempty"`
	LastModifiedUser string                  `json:"LastModifiedUser,omitempty"`
	Description      string                  `json:"Description,omitempty"`
	Value            string                  `json:"Value"`
	AllowedPattern   string                  `json:"AllowedPattern,omitempty"`
	Version          int64                   `json:"Version"`
	Labels           []string                `json:"Labels"`
	Tier             string                  `json:"Tier"`
	Policies         []ParameterInlinePolicy `json:"Policies"`
	DataType         string                  `json:"DataType"`
}

// StringFilter is the AWS ParameterStringFilter shape.
type StringFilter struct {
	Key    string   `json:"Key"`
	Option string   `json:"Option,omitempty"`
	Values []string `json:"Values,omitempty"`
}

// LegacyFilter is the deprecated ParametersFilter shape (DescribeParameters Filters).
type LegacyFilter struct {
	Key    string   `json:"Key"`
	Values []string `json:"Values"`
}

// PutParameterInput is the PutParameter request.
type PutParameterInput struct {
	Name           string      `json:"Name"`
	Description    *string     `json:"Description,omitempty"`
	Value          *string     `json:"Value"`
	Type           string      `json:"Type,omitempty"`
	KeyID          string      `json:"KeyId,omitempty"`
	Overwrite      bool        `json:"Overwrite,omitempty"`
	AllowedPattern *string     `json:"AllowedPattern,omitempty"`
	Tags           []types.Tag `json:"Tags,omitempty"`
	Tier           string      `json:"Tier,omitempty"`
	Policies       string      `json:"Policies,omitempty"`
	DataType       string      `json:"DataType,omitempty"`
}

// PutParameterOutput is the PutParameter response.
type PutParameterOutput struct {
	Version int64  `json:"Version"`
	Tier    string `json:"Tier"`
}

// GetParameterInput is the GetParameter request.
type GetParameterInput struct {
	Name           string `json:"Name"`
	WithDecryption bool   `json:"WithDecryption,omitempty"`
}

// GetParameterOutput is the GetParameter response.
type GetParameterOutput struct {
	Parameter *Parameter `json:"Parameter"`
}

// GetParametersInput is the GetParameters request.
type GetParametersInput struct {
	Names          []string `json:"Names"`
	WithDecryption bool     `json:"WithDecryption,omitempty"`
}

// GetParametersOutput is the GetParameters response.
type GetParametersOutput struct {
	Parameters        []*Parameter `json:"Parameters"`
	InvalidParameters []string     `json:"InvalidParameters"`
}

// GetParametersByPathInput is the GetParametersByPath request.
type GetParametersByPathInput struct {
	Path             string         `json:"Path"`
	Recursive        bool           `json:"Recursive,omitempty"`
	ParameterFilters []StringFilter `json:"ParameterFilters,omitempty"`
	WithDecryption   bool           `json:"WithDecryption,omitempty"`
	MaxResults       int            `json:"MaxResults,omitempty"`
	NextToken        string         `json:"NextToken,omitempty"`
}

// GetParametersByPathOutput is the GetParametersByPath response.
type GetParametersByPathOutput struct {
	Parameters []*Parameter `json:"Parameters"`
	NextToken  string       `json:"NextToken,omitempty"`
}

// DescribeParametersInput is the DescribeParameters request.
type DescribeParametersInput struct {
	Filters          []LegacyFilter `json:"Filters,omitempty"`
	ParameterFilters []StringFilter `json:"ParameterFilters,omitempty"`
	MaxResults       int            `json:"MaxResults,omitempty"`
	NextToken        string         `json:"NextToken,omitempty"`
	Shared           bool           `json:"Shared,omitempty"`
}

// DescribeParametersOutput is the DescribeParameters response.
type DescribeParametersOutput struct {
	Parameters []*ParameterMetadata `json:"Parameters"`
	NextToken  string               `json:"NextToken,omitempty"`
}

// GetParameterHistoryInput is the GetParameterHistory request.
type GetParameterHistoryInput struct {
	Name           string `json:"Name"`
	WithDecryption bool   `json:"WithDecryption,omitempty"`
	MaxResults     int    `json:"MaxResults,omitempty"`
	NextToken      string `json:"NextToken,omitempty"`
}

// GetParameterHistoryOutput is the GetParameterHistory response.
type GetParameterHistoryOutput struct {
	Parameters []*ParameterHistory `json:"Parameters"`
	NextToken  string              `json:"NextToken,omitempty"`
}

// DeleteParameterInput is the DeleteParameter request.
type DeleteParameterInput struct {
	Name string `json:"Name"`
}

// DeleteParametersInput is the DeleteParameters request.
type DeleteParametersInput struct {
	Names []string `json:"Names"`
}

// DeleteParametersOutput is the DeleteParameters response.
type DeleteParametersOutput struct {
	DeletedParameters []string `json:"DeletedParameters"`
	InvalidParameters []string `json:"InvalidParameters"`
}

// LabelParameterVersionInput is the LabelParameterVersion request.
type LabelParameterVersionInput struct {
	Name             string   `json:"Name"`
	ParameterVersion *int64   `json:"ParameterVersion,omitempty"`
	Labels           []string `json:"Labels"`
}

// LabelParameterVersionOutput is the LabelParameterVersion response.
type LabelParameterVersionOutput struct {
	InvalidLabels    []string `json:"InvalidLabels"`
	ParameterVersion int64    `json:"ParameterVersion"`
}

// UnlabelParameterVersionInput is the UnlabelParameterVersion request.
type UnlabelParameterVersionInput struct {
	Name             string   `json:"Name"`
	ParameterVersion *int64   `json:"ParameterVersion"`
	Labels           []string `json:"Labels"`
}

// UnlabelParameterVersionOutput is the UnlabelParameterVersion response.
type UnlabelParameterVersionOutput struct {
	RemovedLabels []string `json:"RemovedLabels"`
	InvalidLabels []string `json:"InvalidLabels"`
}
