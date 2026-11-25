// Package documents implements SSM documents: versioned JSON/YAML content
// (Command, Session, Automation, ...) with default/latest versions, parameter
// declarations, built-in AWS-* documents, and tags.
package documents

import (
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

// Document types.
const (
	TypeCommand    = "Command"
	TypeSession    = "Session"
	TypeAutomation = "Automation"
)

// Document formats.
const (
	FormatJSON = "JSON"
	FormatYAML = "YAML"
	FormatText = "TEXT"
)

// Owner values.
const (
	OwnerAmazon = "Amazon"
	OwnerSelf   = "Self"
)

// Document limits.
const (
	MaxContentBytes   = 64 * 1024
	MaxVersions       = 1000
	MaxVersionNameLen = 128
	statusActive      = "Active"
	hashTypeSha256    = "Sha256"
)

// DocParameter is a declared document parameter (from the content).
type DocParameter struct {
	Type           string   `json:"type"`
	Description    string   `json:"description,omitempty"`
	Default        any      `json:"default,omitempty"`
	AllowedValues  []string `json:"allowedValues,omitempty"`
	AllowedPattern string   `json:"allowedPattern,omitempty"`
}

// Step is one entry of mainSteps (schema 2.2) or runtimeConfig (1.2).
type Step struct {
	Action         string         `json:"action"`
	Name           string         `json:"name"`
	Inputs         map[string]any `json:"inputs"`
	Precondition   map[string]any `json:"precondition,omitempty"`
	TimeoutSeconds int            `json:"timeoutSeconds,omitempty"`
}

// Content is the parsed, normalized document body used by Run Command and
// Session Manager. The original text is kept in version.Content.
type Content struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Description   string                  `json:"description,omitempty"`
	Parameters    map[string]DocParameter `json:"parameters,omitempty"`
	MainSteps     []Step                  `json:"mainSteps,omitempty"`
	// SessionType, Inputs and Properties are used by Session documents (1.0).
	SessionType string         `json:"sessionType,omitempty"`
	Inputs      map[string]any `json:"inputs,omitempty"`
	Properties  map[string]any `json:"properties,omitempty"`
}

// version is a persisted document version.
type version struct {
	Version       string    `json:"version"`
	VersionName   string    `json:"versionName,omitempty"`
	Content       string    `json:"content"`
	Format        string    `json:"format"`
	Hash          string    `json:"hash"`
	SchemaVersion string    `json:"schemaVersion"`
	Description   string    `json:"description,omitempty"`
	PlatformTypes []string  `json:"platformTypes"`
	CreatedDate   time.Time `json:"createdDate"`
}

// record is the persisted document state.
type record struct {
	Name           string    `json:"name"`
	DisplayName    string    `json:"displayName,omitempty"`
	Owner          string    `json:"owner"`
	DocumentType   string    `json:"documentType"`
	TargetType     string    `json:"targetType,omitempty"`
	DefaultVersion string    `json:"defaultVersion"`
	CreatedDate    time.Time `json:"createdDate"`
	NextVersion    int       `json:"nextVersion"`
	Versions       []version `json:"versions"`
}

func (r *record) latest() *version { return &r.Versions[len(r.Versions)-1] }

func (r *record) find(ver, versionName string) *version {
	for i := range r.Versions {
		v := &r.Versions[i]
		if ver != "" && v.Version != ver {
			continue
		}
		if versionName != "" && v.VersionName != versionName {
			continue
		}
		return v
	}
	return nil
}

func (r *record) defaultVersion() *version { return r.find(r.DefaultVersion, "") }

// --- API shapes ---

// DocumentParameter is the AWS DocumentParameter shape.
type DocumentParameter struct {
	Name         string `json:"Name"`
	Type         string `json:"Type"`
	Description  string `json:"Description,omitempty"`
	DefaultValue string `json:"DefaultValue,omitempty"`
}

// DocumentDescription is the AWS DocumentDescription shape.
type DocumentDescription struct {
	Hash            string              `json:"Hash"`
	HashType        string              `json:"HashType"`
	Name            string              `json:"Name"`
	DisplayName     string              `json:"DisplayName,omitempty"`
	VersionName     string              `json:"VersionName,omitempty"`
	Owner           string              `json:"Owner"`
	CreatedDate     *types.Timestamp    `json:"CreatedDate,omitempty"`
	Status          string              `json:"Status"`
	DocumentVersion string              `json:"DocumentVersion"`
	Description     string              `json:"Description,omitempty"`
	Parameters      []DocumentParameter `json:"Parameters,omitempty"`
	PlatformTypes   []string            `json:"PlatformTypes"`
	DocumentType    string              `json:"DocumentType"`
	SchemaVersion   string              `json:"SchemaVersion"`
	LatestVersion   string              `json:"LatestVersion"`
	DefaultVersion  string              `json:"DefaultVersion"`
	DocumentFormat  string              `json:"DocumentFormat"`
	TargetType      string              `json:"TargetType,omitempty"`
	Tags            []types.Tag         `json:"Tags"`
}

// DocumentIdentifier is the AWS DocumentIdentifier shape (ListDocuments).
type DocumentIdentifier struct {
	Name            string           `json:"Name"`
	CreatedDate     *types.Timestamp `json:"CreatedDate,omitempty"`
	DisplayName     string           `json:"DisplayName,omitempty"`
	Owner           string           `json:"Owner"`
	VersionName     string           `json:"VersionName,omitempty"`
	PlatformTypes   []string         `json:"PlatformTypes"`
	DocumentVersion string           `json:"DocumentVersion"`
	DocumentType    string           `json:"DocumentType"`
	SchemaVersion   string           `json:"SchemaVersion"`
	DocumentFormat  string           `json:"DocumentFormat"`
	TargetType      string           `json:"TargetType,omitempty"`
	Tags            []types.Tag      `json:"Tags"`
}

// DocumentVersionInfo is the AWS DocumentVersionInfo shape.
type DocumentVersionInfo struct {
	Name             string           `json:"Name"`
	DisplayName      string           `json:"DisplayName,omitempty"`
	DocumentVersion  string           `json:"DocumentVersion"`
	VersionName      string           `json:"VersionName,omitempty"`
	CreatedDate      *types.Timestamp `json:"CreatedDate,omitempty"`
	IsDefaultVersion bool             `json:"IsDefaultVersion"`
	DocumentFormat   string           `json:"DocumentFormat"`
	Status           string           `json:"Status"`
}

// DocumentFilter is the deprecated DocumentFilterList entry.
type DocumentFilter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DocumentKeyValuesFilter is the AWS DocumentKeyValuesFilter shape.
type DocumentKeyValuesFilter struct {
	Key    string   `json:"Key"`
	Values []string `json:"Values"`
}

// CreateDocumentInput is the CreateDocument request.
type CreateDocumentInput struct {
	Content        string      `json:"Content"`
	Name           string      `json:"Name"`
	DisplayName    string      `json:"DisplayName,omitempty"`
	VersionName    string      `json:"VersionName,omitempty"`
	DocumentType   string      `json:"DocumentType,omitempty"`
	DocumentFormat string      `json:"DocumentFormat,omitempty"`
	TargetType     string      `json:"TargetType,omitempty"`
	Tags           []types.Tag `json:"Tags,omitempty"`
}

// DocumentDescriptionOutput wraps DocumentDescription (Create/Update).
type DocumentDescriptionOutput struct {
	DocumentDescription *DocumentDescription `json:"DocumentDescription"`
}

// DescribeDocumentOutput is the DescribeDocument response.
type DescribeDocumentOutput struct {
	Document *DocumentDescription `json:"Document"`
}

// GetDocumentInput is the GetDocument request.
type GetDocumentInput struct {
	Name            string `json:"Name"`
	VersionName     string `json:"VersionName,omitempty"`
	DocumentVersion string `json:"DocumentVersion,omitempty"`
	DocumentFormat  string `json:"DocumentFormat,omitempty"`
}

// GetDocumentOutput is the GetDocument response.
type GetDocumentOutput struct {
	Name            string           `json:"Name"`
	DisplayName     string           `json:"DisplayName,omitempty"`
	CreatedDate     *types.Timestamp `json:"CreatedDate,omitempty"`
	VersionName     string           `json:"VersionName,omitempty"`
	DocumentVersion string           `json:"DocumentVersion"`
	Status          string           `json:"Status"`
	Content         string           `json:"Content"`
	DocumentType    string           `json:"DocumentType"`
	DocumentFormat  string           `json:"DocumentFormat"`
}

// DescribeDocumentInput is the DescribeDocument request.
type DescribeDocumentInput struct {
	Name            string `json:"Name"`
	DocumentVersion string `json:"DocumentVersion,omitempty"`
	VersionName     string `json:"VersionName,omitempty"`
}

// ListDocumentsInput is the ListDocuments request.
type ListDocumentsInput struct {
	DocumentFilterList []DocumentFilter          `json:"DocumentFilterList,omitempty"`
	Filters            []DocumentKeyValuesFilter `json:"Filters,omitempty"`
	MaxResults         int                       `json:"MaxResults,omitempty"`
	NextToken          string                    `json:"NextToken,omitempty"`
}

// ListDocumentsOutput is the ListDocuments response.
type ListDocumentsOutput struct {
	DocumentIdentifiers []*DocumentIdentifier `json:"DocumentIdentifiers"`
	NextToken           string                `json:"NextToken,omitempty"`
}

// UpdateDocumentInput is the UpdateDocument request.
type UpdateDocumentInput struct {
	Content         string `json:"Content"`
	Name            string `json:"Name"`
	DisplayName     string `json:"DisplayName,omitempty"`
	VersionName     string `json:"VersionName,omitempty"`
	DocumentVersion string `json:"DocumentVersion,omitempty"`
	DocumentFormat  string `json:"DocumentFormat,omitempty"`
	TargetType      string `json:"TargetType,omitempty"`
}

// DeleteDocumentInput is the DeleteDocument request.
type DeleteDocumentInput struct {
	Name            string `json:"Name"`
	DocumentVersion string `json:"DocumentVersion,omitempty"`
	VersionName     string `json:"VersionName,omitempty"`
	Force           bool   `json:"Force,omitempty"`
}

// UpdateDocumentDefaultVersionInput is the UpdateDocumentDefaultVersion request.
type UpdateDocumentDefaultVersionInput struct {
	Name            string `json:"Name"`
	DocumentVersion string `json:"DocumentVersion"`
}

// DocumentDefaultVersionDescription is the AWS shape of the same name.
type DocumentDefaultVersionDescription struct {
	Name               string `json:"Name"`
	DefaultVersion     string `json:"DefaultVersion"`
	DefaultVersionName string `json:"DefaultVersionName,omitempty"`
}

// UpdateDocumentDefaultVersionOutput is the UpdateDocumentDefaultVersion response.
type UpdateDocumentDefaultVersionOutput struct {
	Description *DocumentDefaultVersionDescription `json:"Description"`
}

// ListDocumentVersionsInput is the ListDocumentVersions request.
type ListDocumentVersionsInput struct {
	Name       string `json:"Name"`
	MaxResults int    `json:"MaxResults,omitempty"`
	NextToken  string `json:"NextToken,omitempty"`
}

// ListDocumentVersionsOutput is the ListDocumentVersions response.
type ListDocumentVersionsOutput struct {
	DocumentVersions []*DocumentVersionInfo `json:"DocumentVersions"`
	NextToken        string                 `json:"NextToken,omitempty"`
}

// Resolved is a document version resolved for execution.
type Resolved struct {
	Name         string
	Version      string
	DocumentType string
	Hash         string
	Content      *Content
}
