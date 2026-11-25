package documents

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/kv"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

const bucket = "documents"

// Service implements SSM document operations.
type Service struct {
	store   kv.Store
	account types.Account
}

// New creates the document service, seeds built-in documents, and registers
// the Document tag type.
func New(store kv.Store, tagSvc *tags.Service, account types.Account) (*Service, error) {
	s := &Service{store: store, account: account}
	if tagSvc != nil {
		tagSvc.Register(tags.ResourceDocument, func(r kv.Reader, id string) (bool, error) {
			rec, err := s.load(r, id)
			return rec != nil, err
		})
	}
	if err := s.seedBuiltins(); err != nil {
		return nil, err
	}
	return s, nil
}

// seedBuiltins (re)writes Amazon-owned documents so upgrades refresh them.
func (s *Service) seedBuiltins() error {
	return s.store.Update(func(tx kv.Tx) error {
		for _, b := range builtins {
			c, err := parseContent(b.Content, FormatJSON, b.Type)
			if err != nil {
				return err
			}
			created := types.Now()
			if existing, err := s.load(tx, b.Name); err == nil && existing != nil {
				created = existing.CreatedDate
			}
			rec := &record{
				Name: b.Name, Owner: OwnerAmazon, DocumentType: b.Type, DefaultVersion: "1",
				CreatedDate: created, NextVersion: 2,
				Versions: []version{{
					Version: "1", Content: b.Content, Format: FormatJSON, Hash: hashContent(b.Content),
					SchemaVersion: c.SchemaVersion, Description: c.Description,
					PlatformTypes: platformTypes(c, b.Type), CreatedDate: created,
				}},
			}
			if err := kv.PutJSON(tx, bucket, b.Name, rec); err != nil {
				return err
			}
		}
		return nil
	})
}

// Create creates a new document (CreateDocument).
func (s *Service) Create(ctx context.Context, in *CreateDocumentInput) (*DocumentDescriptionOutput, error) {
	if err := validateName(in.Name); err != nil {
		return nil, err
	}
	format := defaultFormat(in.DocumentFormat)
	docType := in.DocumentType
	if docType == "" {
		docType = TypeCommand
	}
	switch docType {
	case TypeCommand, TypeSession, TypeAutomation:
	default:
		return nil, types.NewError(types.ErrInvalidDocumentType, "The document type %s is not supported.", docType)
	}
	if len(in.VersionName) > MaxVersionNameLen {
		return nil, types.NewValidationError("VersionName must be at most %d characters", MaxVersionNameLen)
	}
	if err := tags.ValidateTags(in.Tags); err != nil {
		return nil, err
	}
	c, err := parseContent(in.Content, format, docType)
	if err != nil {
		return nil, err
	}
	var out *DocumentDescription
	err = s.store.Update(func(tx kv.Tx) error {
		existing, err := s.load(tx, in.Name)
		if err != nil {
			return err
		}
		if existing != nil {
			return types.NewError(types.ErrDocumentAlreadyExists, "Document with same name %s already exists", in.Name)
		}
		now := types.Now()
		rec := &record{
			Name: in.Name, DisplayName: in.DisplayName, Owner: OwnerSelf, DocumentType: docType,
			TargetType: in.TargetType, DefaultVersion: "1", CreatedDate: now, NextVersion: 2,
			Versions: []version{newVersion("1", in.VersionName, in.Content, format, c, docType, now)},
		}
		if err := kv.PutJSON(tx, bucket, in.Name, rec); err != nil {
			return err
		}
		if len(in.Tags) > 0 {
			if err := tags.SetTx(tx, tags.ResourceDocument, in.Name, in.Tags); err != nil {
				return err
			}
		}
		out, err = s.describe(tx, rec, rec.latest())
		return err
	})
	if err != nil {
		return nil, err
	}
	return &DocumentDescriptionOutput{DocumentDescription: out}, nil
}

// Update adds a new version (UpdateDocument).
func (s *Service) Update(ctx context.Context, in *UpdateDocumentInput) (*DocumentDescriptionOutput, error) {
	format := defaultFormat(in.DocumentFormat)
	var out *DocumentDescription
	err := s.store.Update(func(tx kv.Tx) error {
		rec, err := s.loadRequired(tx, in.Name)
		if err != nil {
			return err
		}
		if rec.Owner != OwnerSelf {
			return types.NewError(types.ErrInvalidDocumentOperation, "You can't update documents owned by Amazon.")
		}
		if in.DocumentVersion != "" && in.DocumentVersion != "$LATEST" && in.DocumentVersion != rec.latest().Version {
			return types.NewError(types.ErrInvalidDocumentVersion, "The document version is not valid or does not exist. Only $LATEST or the latest version number can be updated.")
		}
		c, err := parseContent(in.Content, format, rec.DocumentType)
		if err != nil {
			return err
		}
		hash := hashContent(in.Content)
		for _, v := range rec.Versions {
			if v.Hash == hash {
				return types.NewError(types.ErrDuplicateDocumentContent, "The content of the association document matches another document. Change the content of the document and try again.")
			}
			if in.VersionName != "" && v.VersionName == in.VersionName {
				return types.NewError(types.ErrDuplicateDocumentVersionName, "The version name has already been used in this document. Specify a different version name, and then try again.")
			}
		}
		if len(rec.Versions) >= MaxVersions {
			return types.NewError(types.ErrDocumentVersionLimitExceeded, "The document has too many versions. Delete one or more document versions and try again.")
		}
		ver := strconv.Itoa(rec.NextVersion)
		rec.NextVersion++
		rec.Versions = append(rec.Versions, newVersion(ver, in.VersionName, in.Content, format, c, rec.DocumentType, types.Now()))
		if in.DisplayName != "" {
			rec.DisplayName = in.DisplayName
		}
		if in.TargetType != "" {
			rec.TargetType = in.TargetType
		}
		if err := kv.PutJSON(tx, bucket, rec.Name, rec); err != nil {
			return err
		}
		out, err = s.describe(tx, rec, rec.latest())
		return err
	})
	if err != nil {
		return nil, err
	}
	return &DocumentDescriptionOutput{DocumentDescription: out}, nil
}

// Get returns document content (GetDocument).
func (s *Service) Get(ctx context.Context, in *GetDocumentInput) (*GetDocumentOutput, error) {
	var out *GetDocumentOutput
	err := s.store.View(func(r kv.Reader) error {
		rec, v, err := s.resolveVersion(r, in.Name, in.DocumentVersion, in.VersionName)
		if err != nil {
			return err
		}
		format := in.DocumentFormat
		if format == "" {
			format = v.Format
		}
		content, err := convertFormat(v.Content, v.Format, format)
		if err != nil {
			return err
		}
		out = &GetDocumentOutput{
			Name: rec.Name, DisplayName: rec.DisplayName, CreatedDate: types.NewTimestamp(v.CreatedDate),
			VersionName: v.VersionName, DocumentVersion: v.Version, Status: statusActive,
			Content: content, DocumentType: rec.DocumentType, DocumentFormat: format,
		}
		return nil
	})
	return out, err
}

// Describe returns document metadata (DescribeDocument).
func (s *Service) Describe(ctx context.Context, in *DescribeDocumentInput) (*DescribeDocumentOutput, error) {
	var out *DocumentDescription
	err := s.store.View(func(r kv.Reader) error {
		rec, v, err := s.resolveVersion(r, in.Name, in.DocumentVersion, in.VersionName)
		if err != nil {
			return err
		}
		out, err = s.describe(r, rec, v)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &DescribeDocumentOutput{Document: out}, nil
}

// List lists documents (ListDocuments).
func (s *Service) List(ctx context.Context, in *ListDocumentsInput) (*ListDocumentsOutput, error) {
	filters := append([]DocumentKeyValuesFilter{}, in.Filters...)
	for _, f := range in.DocumentFilterList {
		key := map[string]string{"Name": "Name", "Owner": "Owner", "PlatformTypes": "PlatformTypes", "DocumentType": "DocumentType"}[f.Key]
		if key == "" {
			return nil, types.NewError(types.ErrInvalidFilterKey, "The specified key is not valid: %s", f.Key)
		}
		filters = append(filters, DocumentKeyValuesFilter{Key: key, Values: []string{f.Value}})
	}
	var all []*DocumentIdentifier
	err := s.store.View(func(r kv.Reader) error {
		recs, err := kv.ListJSON[record](r, bucket, "")
		if err != nil {
			return err
		}
		for i := range recs {
			rec := &recs[i]
			v := rec.defaultVersion()
			tagMap, err := tags.GetMapTx(r, tags.ResourceDocument, rec.Name)
			if err != nil {
				return err
			}
			ok, err := matchDocument(rec, v, tagMap, filters)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			tagList, err := tags.GetTx(r, tags.ResourceDocument, rec.Name)
			if err != nil {
				return err
			}
			all = append(all, &DocumentIdentifier{
				Name: rec.Name, CreatedDate: types.NewTimestamp(rec.CreatedDate), DisplayName: rec.DisplayName,
				Owner: rec.Owner, VersionName: v.VersionName, PlatformTypes: v.PlatformTypes,
				DocumentVersion: v.Version, DocumentType: rec.DocumentType, SchemaVersion: v.SchemaVersion,
				DocumentFormat: v.Format, TargetType: rec.TargetType, Tags: tagList,
			})
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
		page = []*DocumentIdentifier{}
	}
	return &ListDocumentsOutput{DocumentIdentifiers: page, NextToken: next}, nil
}

// ListVersions lists document versions (ListDocumentVersions).
func (s *Service) ListVersions(ctx context.Context, in *ListDocumentVersionsInput) (*ListDocumentVersionsOutput, error) {
	var all []*DocumentVersionInfo
	err := s.store.View(func(r kv.Reader) error {
		rec, err := s.loadRequired(r, in.Name)
		if err != nil {
			return err
		}
		for i := len(rec.Versions) - 1; i >= 0; i-- {
			v := rec.Versions[i]
			all = append(all, &DocumentVersionInfo{
				Name: rec.Name, DisplayName: rec.DisplayName, DocumentVersion: v.Version, VersionName: v.VersionName,
				CreatedDate: types.NewTimestamp(v.CreatedDate), IsDefaultVersion: v.Version == rec.DefaultVersion,
				DocumentFormat: v.Format, Status: statusActive,
			})
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
	return &ListDocumentVersionsOutput{DocumentVersions: page, NextToken: next}, nil
}

// UpdateDefaultVersion sets the default version (UpdateDocumentDefaultVersion).
func (s *Service) UpdateDefaultVersion(ctx context.Context, in *UpdateDocumentDefaultVersionInput) (*UpdateDocumentDefaultVersionOutput, error) {
	var out *DocumentDefaultVersionDescription
	err := s.store.Update(func(tx kv.Tx) error {
		rec, err := s.loadRequired(tx, in.Name)
		if err != nil {
			return err
		}
		if rec.Owner != OwnerSelf {
			return types.NewError(types.ErrInvalidDocumentOperation, "You can't update documents owned by Amazon.")
		}
		ver := in.DocumentVersion
		if ver == "$LATEST" {
			ver = rec.latest().Version
		}
		v := rec.find(ver, "")
		if v == nil {
			return types.NewError(types.ErrInvalidDocumentVersion, "The document version is not valid or does not exist.")
		}
		rec.DefaultVersion = v.Version
		out = &DocumentDefaultVersionDescription{Name: rec.Name, DefaultVersion: v.Version, DefaultVersionName: v.VersionName}
		return kv.PutJSON(tx, bucket, rec.Name, rec)
	})
	if err != nil {
		return nil, err
	}
	return &UpdateDocumentDefaultVersionOutput{Description: out}, nil
}

// Delete deletes a document or one of its versions (DeleteDocument).
func (s *Service) Delete(ctx context.Context, in *DeleteDocumentInput) error {
	return s.store.Update(func(tx kv.Tx) error {
		rec, err := s.loadRequired(tx, in.Name)
		if err != nil {
			return err
		}
		if rec.Owner != OwnerSelf {
			return types.NewError(types.ErrInvalidDocumentOperation, "You can't delete documents owned by Amazon.")
		}
		if in.DocumentVersion == "" && in.VersionName == "" {
			if err := tx.Delete(bucket, rec.Name); err != nil {
				return err
			}
			return tags.DeleteTx(tx, tags.ResourceDocument, rec.Name)
		}
		v := rec.find(in.DocumentVersion, in.VersionName)
		if v == nil {
			return types.NewError(types.ErrInvalidDocumentVersion, "The document version is not valid or does not exist.")
		}
		if v.Version == rec.DefaultVersion {
			return types.NewError(types.ErrInvalidDocumentOperation, "You can't delete the default version of a document. Change the default version and try again.")
		}
		target := v.Version
		rec.Versions = slices.DeleteFunc(rec.Versions, func(x version) bool { return x.Version == target })
		return kv.PutJSON(tx, bucket, rec.Name, rec)
	})
}

// Resolve returns parsed content for execution (used by Run Command and
// Session Manager). version may be "", "$DEFAULT", "$LATEST", or a number.
func (s *Service) Resolve(name, ver string) (*Resolved, error) {
	var out *Resolved
	err := s.store.View(func(r kv.Reader) error {
		rec, v, err := s.resolveVersion(r, name, ver, "")
		if err != nil {
			return err
		}
		c, err := parseContent(v.Content, v.Format, rec.DocumentType)
		if err != nil {
			return err
		}
		out = &Resolved{Name: rec.Name, Version: v.Version, DocumentType: rec.DocumentType, Hash: v.Hash, Content: c}
		return nil
	})
	return out, err
}

// CountOwned returns the number of user-owned documents.
func (s *Service) CountOwned() (int, error) {
	n := 0
	err := s.store.View(func(r kv.Reader) error {
		recs, err := kv.ListJSON[record](r, bucket, "")
		for _, rec := range recs {
			if rec.Owner == OwnerSelf {
				n++
			}
		}
		return err
	})
	return n, err
}

// --- internals ---

func newVersion(ver, versionName, content, format string, c *Content, docType string, now time.Time) version {
	return version{
		Version: ver, VersionName: versionName, Content: content, Format: format, Hash: hashContent(content),
		SchemaVersion: c.SchemaVersion, Description: c.Description, PlatformTypes: platformTypes(c, docType),
		CreatedDate: now,
	}
}

func defaultFormat(f string) string {
	if f == "" {
		return FormatJSON
	}
	return f
}

// normalizeDocName accepts document ARNs as well as names.
func normalizeDocName(name string) string {
	if i := strings.Index(name, ":document/"); strings.HasPrefix(name, "arn:") && i >= 0 {
		return name[i+len(":document/"):]
	}
	return name
}

func (s *Service) load(r kv.Reader, name string) (*record, error) {
	var rec record
	err := kv.GetJSON(r, bucket, normalizeDocName(name), &rec)
	if errors.Is(err, kv.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *Service) loadRequired(r kv.Reader, name string) (*record, error) {
	rec, err := s.load(r, name)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, types.NewInvalidDocument(name)
	}
	return rec, nil
}

func (s *Service) resolveVersion(r kv.Reader, name, ver, versionName string) (*record, *version, error) {
	rec, err := s.loadRequired(r, name)
	if err != nil {
		return nil, nil, err
	}
	switch ver {
	case "", "$DEFAULT":
		if versionName == "" {
			return rec, rec.defaultVersion(), nil
		}
		ver = ""
	case "$LATEST":
		ver = rec.latest().Version
	}
	v := rec.find(ver, versionName)
	if v == nil {
		return nil, nil, types.NewError(types.ErrInvalidDocumentVersion, "The document version is not valid or does not exist.")
	}
	return rec, v, nil
}

func (s *Service) describe(r kv.Reader, rec *record, v *version) (*DocumentDescription, error) {
	c, err := parseContent(v.Content, v.Format, rec.DocumentType)
	if err != nil {
		return nil, err
	}
	tagList, err := tags.GetTx(r, tags.ResourceDocument, rec.Name)
	if err != nil {
		return nil, err
	}
	return &DocumentDescription{
		Hash: v.Hash, HashType: hashTypeSha256, Name: rec.Name, DisplayName: rec.DisplayName,
		VersionName: v.VersionName, Owner: rec.Owner, CreatedDate: types.NewTimestamp(v.CreatedDate),
		Status: statusActive, DocumentVersion: v.Version, Description: v.Description,
		Parameters: describeParameters(c), PlatformTypes: v.PlatformTypes, DocumentType: rec.DocumentType,
		SchemaVersion: v.SchemaVersion, LatestVersion: rec.latest().Version, DefaultVersion: rec.DefaultVersion,
		DocumentFormat: v.Format, TargetType: rec.TargetType, Tags: tagList,
	}, nil
}

func describeParameters(c *Content) []DocumentParameter {
	names := make([]string, 0, len(c.Parameters))
	for n := range c.Parameters {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]DocumentParameter, 0, len(names))
	for _, n := range names {
		p := c.Parameters[n]
		dp := DocumentParameter{Name: n, Type: p.Type, Description: p.Description}
		if p.Default != nil {
			dp.DefaultValue = strings.Join(defaultValues(p.Default), ",")
		}
		out = append(out, dp)
	}
	return out
}

func matchDocument(rec *record, v *version, tagMap map[string]string, filters []DocumentKeyValuesFilter) (bool, error) {
	for _, f := range filters {
		var ok bool
		switch {
		case f.Key == "Name":
			ok = slices.ContainsFunc(f.Values, func(x string) bool { return strings.HasPrefix(rec.Name, x) })
		case f.Key == "SearchKeyword":
			ok = slices.ContainsFunc(f.Values, func(x string) bool {
				return strings.Contains(strings.ToLower(rec.Name), strings.ToLower(x))
			})
		case f.Key == "Owner":
			ok = slices.ContainsFunc(f.Values, func(x string) bool {
				switch x {
				case "Self":
					return rec.Owner == OwnerSelf
				case "Amazon":
					return rec.Owner == OwnerAmazon
				case "Private":
					return rec.Owner == OwnerSelf
				case "Public", "ThirdParty", "All":
					return x == "All"
				}
				return rec.Owner == x
			})
		case f.Key == "PlatformTypes":
			ok = slices.ContainsFunc(f.Values, func(x string) bool { return slices.Contains(v.PlatformTypes, x) })
		case f.Key == "DocumentType":
			ok = slices.Contains(f.Values, rec.DocumentType)
		case f.Key == "TargetType":
			ok = slices.Contains(f.Values, rec.TargetType)
		case strings.HasPrefix(f.Key, "tag:"):
			val, has := tagMap[strings.TrimPrefix(f.Key, "tag:")]
			ok = has && (len(f.Values) == 0 || slices.Contains(f.Values, val))
		default:
			return false, types.NewError(types.ErrInvalidFilterKey, "The specified key is not valid: %s", f.Key)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
