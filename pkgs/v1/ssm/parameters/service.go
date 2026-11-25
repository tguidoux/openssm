package parameters

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/tguidoux/openssm/pkgs/v1/kv"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/kms"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

const bucket = "parameters"

// Options configures the parameter service.
type Options struct {
	// DefaultTier is used when PutParameter omits Tier (default Standard).
	DefaultTier string
	// MaxStandardParameters caps the number of Standard-tier parameters
	// (AWS: 10,000). Zero disables the limit.
	MaxStandardParameters int
	// MaxAdvancedParameters caps the number of Advanced-tier parameters
	// (AWS: 100,000). Zero disables the limit.
	MaxAdvancedParameters int
}

// Service implements Parameter Store operations.
type Service struct {
	store   kv.Store
	kms     *kms.Service
	account types.Account
	opts    Options
}

// New creates the parameter service and registers the Parameter tag type.
func New(store kv.Store, kmsSvc *kms.Service, tagSvc *tags.Service, account types.Account, opts Options) *Service {
	if opts.DefaultTier == "" {
		opts.DefaultTier = TierStandard
	}
	s := &Service{store: store, kms: kmsSvc, account: account, opts: opts}
	if tagSvc != nil {
		tagSvc.Register(tags.ResourceParameter, func(r kv.Reader, resourceID string) (bool, error) {
			rec, err := s.load(r, normalizeName(resourceID))
			return rec != nil, err
		})
	}
	return s
}

// Put creates or updates a parameter (PutParameter).
func (s *Service) Put(ctx context.Context, in *PutParameterInput) (*PutParameterOutput, error) {
	name := normalizeName(in.Name)
	if err := validateName(name); err != nil {
		return nil, err
	}
	if in.Value == nil || *in.Value == "" {
		return nil, types.NewValidationError("1 validation error detected: Value '' at 'value' failed to satisfy constraint: Member must have length greater than or equal to 1")
	}
	value := *in.Value
	if in.Description != nil && len(*in.Description) > MaxDescriptionLength {
		return nil, types.NewValidationError("Parameter description must be at most %d characters", MaxDescriptionLength)
	}
	if in.Overwrite && len(in.Tags) > 0 {
		return nil, types.NewValidationError("Invalid request: tags and overwrite can't be used together. To create a parameter with tags, please remove overwrite flag. To update tags for an existing parameter, please use AddTagsToResource or RemoveTagsFromResource.")
	}
	if err := tags.ValidateTags(in.Tags); err != nil {
		return nil, err
	}
	policies, err := parsePolicies(in.Policies)
	if err != nil {
		return nil, err
	}
	user := s.account.UserARN(callerName(ctx))

	var out *PutParameterOutput
	err = s.store.Update(func(tx kv.Tx) error {
		rec, err := s.load(tx, name)
		if err != nil {
			return err
		}
		if rec != nil && !in.Overwrite {
			return types.NewParameterAlreadyExists()
		}

		var prev *version
		if rec != nil {
			prev = rec.latest()
		}
		v := version{LastModifiedDate: types.Now(), LastModifiedUser: user}

		v.Type = in.Type
		if v.Type == "" {
			if prev == nil {
				return types.NewValidationError("A parameter type is required when you create a parameter.")
			}
			v.Type = prev.Type
		}
		if err := validateType(v.Type); err != nil {
			return err
		}
		if in.KeyID != "" && v.Type != TypeSecureString {
			return types.NewValidationError("KeyId is required for SecureString type parameter only.")
		}

		v.DataType = in.DataType
		if v.DataType == "" {
			v.DataType = DataTypeText
			if prev != nil {
				v.DataType = prev.DataType
			}
		}
		if v.Type == TypeSecureString && v.DataType != DataTypeText {
			return types.NewValidationError("The SecureString type doesn't support the %s data type.", v.DataType)
		}
		if err := validateDataType(v.DataType, value); err != nil {
			return err
		}

		v.Description = pick(in.Description, prev, func(p *version) string { return p.Description })
		v.AllowedPattern = pick(in.AllowedPattern, prev, func(p *version) string { return p.AllowedPattern })
		if v.AllowedPattern != "" {
			if _, err := compilePattern(v.AllowedPattern); err != nil {
				return err
			}
		}
		if err := checkPattern(v.AllowedPattern, value); err != nil {
			return err
		}

		v.Policies = policies
		if in.Policies == "" && prev != nil {
			v.Policies = prev.Policies
		}

		currentTier := ""
		if prev != nil {
			currentTier = prev.Tier
		}
		requestedTier := in.Tier
		if requestedTier == "" {
			requestedTier = s.opts.DefaultTier
			if currentTier == TierAdvanced {
				requestedTier = TierAdvanced
			}
		}
		v.Tier, err = resolveTier(requestedTier, currentTier, len(value), len(v.Policies) > 0)
		if err != nil {
			return err
		}

		if v.Type == TypeSecureString {
			keyRef := in.KeyID
			if keyRef == "" && prev != nil && prev.Type == TypeSecureString {
				keyRef = prev.KeyID
			}
			keyID, err := s.kms.ResolveKeyIDTx(tx, keyRef)
			if err != nil {
				return err
			}
			v.KeyID = keyRef
			if v.KeyID == "" {
				v.KeyID = kms.DefaultSSMAlias
			}
			v.Value, err = s.kms.EncryptTx(tx, keyID, []byte(value), []byte(name))
			if err != nil {
				return err
			}
		} else {
			v.Value = value
		}

		if rec == nil {
			if err := s.checkCountLimit(tx, v.Tier); err != nil {
				return err
			}
			rec = &record{Name: name}
			v.Version = 1
		} else {
			if prev.Tier != v.Tier && v.Tier == TierAdvanced {
				if err := s.checkCountLimit(tx, v.Tier); err != nil {
					return err
				}
			}
			v.Version = prev.Version + 1
		}
		rec.Versions = append(rec.Versions, v)
		if err := trimVersions(rec); err != nil {
			return err
		}
		if err := kv.PutJSON(tx, bucket, name, rec); err != nil {
			return err
		}
		if len(in.Tags) > 0 {
			if err := tags.SetTx(tx, tags.ResourceParameter, name, in.Tags); err != nil {
				return err
			}
		}
		out = &PutParameterOutput{Version: v.Version, Tier: v.Tier}
		return nil
	})
	return out, err
}

// Get returns a single parameter (GetParameter).
func (s *Service) Get(ctx context.Context, in *GetParameterInput) (*GetParameterOutput, error) {
	var p *Parameter
	err := s.store.View(func(r kv.Reader) error {
		var err error
		p, err = s.getOne(r, in.Name, in.WithDecryption)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &GetParameterOutput{Parameter: p}, nil
}

// GetMany returns up to 10 parameters (GetParameters).
func (s *Service) GetMany(ctx context.Context, in *GetParametersInput) (*GetParametersOutput, error) {
	if len(in.Names) == 0 || len(in.Names) > MaxGetParametersNames {
		return nil, types.NewValidationError("1 validation error detected: Value at 'names' failed to satisfy constraint: Member must have length less than or equal to %d", MaxGetParametersNames)
	}
	out := &GetParametersOutput{Parameters: []*Parameter{}, InvalidParameters: []string{}}
	seen := make(map[string]bool)
	err := s.store.View(func(r kv.Reader) error {
		for _, ref := range in.Names {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			p, err := s.getOne(r, ref, in.WithDecryption)
			if err != nil {
				if types.IsCode(err, types.ErrParameterNotFound) || types.IsCode(err, types.ErrParameterVersionNotFound) || types.IsCode(err, types.ErrValidation) {
					out.InvalidParameters = append(out.InvalidParameters, ref)
					continue
				}
				return err
			}
			out.Parameters = append(out.Parameters, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetByPath returns parameters under a hierarchy (GetParametersByPath).
func (s *Service) GetByPath(ctx context.Context, in *GetParametersByPathInput) (*GetParametersByPathOutput, error) {
	if err := validatePath(in.Path); err != nil {
		return nil, err
	}
	for _, f := range in.ParameterFilters {
		if f.Key != "Type" && f.Key != "KeyId" && f.Key != "Label" && !strings.HasPrefix(f.Key, "tag:") {
			return nil, types.NewError(types.ErrInvalidFilterKey, "The following filter key is not valid: %s. Valid filter keys include: [Type, KeyId, Label].", f.Key)
		}
	}
	prefix := in.Path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	var all []*Parameter
	err := s.store.View(func(r kv.Reader) error {
		recs, err := s.list(r, prefix)
		if err != nil {
			return err
		}
		for _, rec := range recs {
			if !in.Recursive && strings.Contains(rec.Name[len(prefix):], "/") {
				continue
			}
			v := rec.latest()
			if label := filterLabel(in.ParameterFilters); label != nil {
				v = nil
				for _, l := range label {
					if lv := rec.findLabel(l); lv != nil {
						v = lv
						break
					}
				}
				if v == nil {
					continue
				}
			}
			ok, err := s.matches(r, rec, v, in.ParameterFilters)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			p, err := s.toParameter(r, rec.Name, v, "", in.WithDecryption)
			if err != nil {
				return err
			}
			all = append(all, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	page, next, err := types.Paginate(all, in.NextToken, in.MaxResults, 10, 10)
	if err != nil {
		return nil, err
	}
	if page == nil {
		page = []*Parameter{}
	}
	return &GetParametersByPathOutput{Parameters: page, NextToken: next}, nil
}

// Describe lists parameter metadata (DescribeParameters).
func (s *Service) Describe(ctx context.Context, in *DescribeParametersInput) (*DescribeParametersOutput, error) {
	if len(in.Filters) > 0 && len(in.ParameterFilters) > 0 {
		return nil, types.NewValidationError("You can use either Filters or ParameterFilters in a single request, but not both.")
	}
	filters := append([]StringFilter{}, in.ParameterFilters...)
	for _, f := range in.Filters {
		switch f.Key {
		case "Name", "Type", "KeyId":
		default:
			return nil, types.NewError(types.ErrInvalidFilterKey, "The following filter key is not valid: %s", f.Key)
		}
		opt := "Equals"
		if f.Key == "Name" {
			opt = "BeginsWith"
		}
		filters = append(filters, StringFilter{Key: f.Key, Option: opt, Values: f.Values})
	}
	if err := validateDescribeFilters(filters); err != nil {
		return nil, err
	}

	all := []*ParameterMetadata{}
	if !in.Shared {
		err := s.store.View(func(r kv.Reader) error {
			recs, err := s.list(r, "")
			if err != nil {
				return err
			}
			for _, rec := range recs {
				ok, err := s.matches(r, rec, rec.latest(), filters)
				if err != nil {
					return err
				}
				if ok {
					all = append(all, s.toMetadata(rec.Name, rec.latest()))
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	page, next, err := types.Paginate(all, in.NextToken, in.MaxResults, 10, 50)
	if err != nil {
		return nil, err
	}
	if page == nil {
		page = []*ParameterMetadata{}
	}
	return &DescribeParametersOutput{Parameters: page, NextToken: next}, nil
}

// History returns every retained version (GetParameterHistory).
func (s *Service) History(ctx context.Context, in *GetParameterHistoryInput) (*GetParameterHistoryOutput, error) {
	name := normalizeName(in.Name)
	var all []*ParameterHistory
	err := s.store.View(func(r kv.Reader) error {
		rec, err := s.load(r, name)
		if err != nil {
			return err
		}
		if rec == nil {
			return types.NewParameterNotFound(in.Name)
		}
		for i := range rec.Versions {
			v := &rec.Versions[i]
			value, err := s.reveal(r, name, v, in.WithDecryption)
			if err != nil {
				return err
			}
			labels := v.Labels
			if labels == nil {
				labels = []string{}
			}
			all = append(all, &ParameterHistory{
				Name:             name,
				Type:             v.Type,
				KeyID:            v.KeyID,
				LastModifiedDate: types.NewTimestamp(v.LastModifiedDate),
				LastModifiedUser: v.LastModifiedUser,
				Description:      v.Description,
				Value:            value,
				AllowedPattern:   v.AllowedPattern,
				Version:          v.Version,
				Labels:           labels,
				Tier:             v.Tier,
				Policies:         toInlinePolicies(v.Policies),
				DataType:         v.DataType,
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
	return &GetParameterHistoryOutput{Parameters: page, NextToken: next}, nil
}

// Delete removes a parameter and all its versions (DeleteParameter).
func (s *Service) Delete(ctx context.Context, in *DeleteParameterInput) error {
	name := normalizeName(in.Name)
	return s.store.Update(func(tx kv.Tx) error {
		rec, err := s.load(tx, name)
		if err != nil {
			return err
		}
		if rec == nil {
			return types.NewParameterNotFound(in.Name)
		}
		return s.deleteTx(tx, name)
	})
}

// DeleteMany removes up to 10 parameters (DeleteParameters).
func (s *Service) DeleteMany(ctx context.Context, in *DeleteParametersInput) (*DeleteParametersOutput, error) {
	if len(in.Names) == 0 || len(in.Names) > MaxGetParametersNames {
		return nil, types.NewValidationError("1 validation error detected: Value at 'names' failed to satisfy constraint: Member must have length less than or equal to %d", MaxGetParametersNames)
	}
	out := &DeleteParametersOutput{DeletedParameters: []string{}, InvalidParameters: []string{}}
	err := s.store.Update(func(tx kv.Tx) error {
		for _, ref := range in.Names {
			name := normalizeName(ref)
			rec, err := s.load(tx, name)
			if err != nil {
				return err
			}
			if rec == nil {
				out.InvalidParameters = append(out.InvalidParameters, ref)
				continue
			}
			if err := s.deleteTx(tx, name); err != nil {
				return err
			}
			out.DeletedParameters = append(out.DeletedParameters, ref)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Label attaches labels to a version (LabelParameterVersion). A label can
// only be attached to one version, so it is moved from any other version.
func (s *Service) Label(ctx context.Context, in *LabelParameterVersionInput) (*LabelParameterVersionOutput, error) {
	if len(in.Labels) == 0 || len(in.Labels) > MaxLabelsPerVersion {
		return nil, types.NewValidationError("1 validation error detected: Value at 'labels' failed to satisfy constraint: Member must have length between 1 and %d", MaxLabelsPerVersion)
	}
	name := normalizeName(in.Name)
	out := &LabelParameterVersionOutput{InvalidLabels: []string{}}
	err := s.store.Update(func(tx kv.Tx) error {
		rec, err := s.load(tx, name)
		if err != nil {
			return err
		}
		if rec == nil {
			return types.NewParameterNotFound(in.Name)
		}
		target := rec.latest()
		if in.ParameterVersion != nil {
			if target = rec.findVersion(*in.ParameterVersion); target == nil {
				return types.NewError(types.ErrParameterVersionNotFound, "Systems Manager could not find version %d of %s. Verify the version and try again.", *in.ParameterVersion, name)
			}
		}
		var valid []string
		for _, l := range in.Labels {
			if !validateLabel(l) {
				out.InvalidLabels = append(out.InvalidLabels, l)
				continue
			}
			valid = append(valid, l)
		}
		merged := slices.Clone(target.Labels)
		for _, l := range valid {
			if !slices.Contains(merged, l) {
				merged = append(merged, l)
			}
		}
		if len(merged) > MaxLabelsPerVersion {
			return types.NewError(types.ErrParameterVersionLabelLimitExceeded, "A parameter version can have maximum %d labels.", MaxLabelsPerVersion)
		}
		for i := range rec.Versions {
			if rec.Versions[i].Version == target.Version {
				continue
			}
			rec.Versions[i].Labels = slices.DeleteFunc(rec.Versions[i].Labels, func(l string) bool { return slices.Contains(valid, l) })
		}
		target.Labels = merged
		out.ParameterVersion = target.Version
		return kv.PutJSON(tx, bucket, name, rec)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Unlabel detaches labels from a version (UnlabelParameterVersion).
func (s *Service) Unlabel(ctx context.Context, in *UnlabelParameterVersionInput) (*UnlabelParameterVersionOutput, error) {
	if in.ParameterVersion == nil {
		return nil, types.NewValidationError("1 validation error detected: Value null at 'parameterVersion' failed to satisfy constraint: Member must not be null")
	}
	if len(in.Labels) == 0 || len(in.Labels) > MaxLabelsPerVersion {
		return nil, types.NewValidationError("1 validation error detected: Value at 'labels' failed to satisfy constraint: Member must have length between 1 and %d", MaxLabelsPerVersion)
	}
	name := normalizeName(in.Name)
	out := &UnlabelParameterVersionOutput{RemovedLabels: []string{}, InvalidLabels: []string{}}
	err := s.store.Update(func(tx kv.Tx) error {
		rec, err := s.load(tx, name)
		if err != nil {
			return err
		}
		if rec == nil {
			return types.NewParameterNotFound(in.Name)
		}
		target := rec.findVersion(*in.ParameterVersion)
		if target == nil {
			return types.NewError(types.ErrParameterVersionNotFound, "Systems Manager could not find version %d of %s. Verify the version and try again.", *in.ParameterVersion, name)
		}
		for _, l := range in.Labels {
			if i := slices.Index(target.Labels, l); i >= 0 {
				target.Labels = slices.Delete(target.Labels, i, i+1)
				out.RemovedLabels = append(out.RemovedLabels, l)
			} else {
				out.InvalidLabels = append(out.InvalidLabels, l)
			}
		}
		return kv.PutJSON(tx, bucket, name, rec)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Sweep deletes parameters whose Expiration policy has passed and returns
// the deleted names. The server runs it periodically.
func (s *Service) Sweep(ctx context.Context) ([]string, error) {
	var deleted []string
	err := s.store.Update(func(tx kv.Tx) error {
		entries, err := tx.List(bucket, "")
		if err != nil {
			return err
		}
		now := types.Now()
		for _, e := range entries {
			var rec record
			if err := kv.GetJSON(tx, bucket, e.Key, &rec); err != nil {
				return err
			}
			if t, ok := expiresAt(rec.latest().Policies); ok && !now.Before(t) {
				if err := s.deleteTx(tx, rec.Name); err != nil {
					return err
				}
				deleted = append(deleted, rec.Name)
			}
		}
		return nil
	})
	return deleted, err
}

// Counts returns the number of live parameters keyed by [type, tier].
func (s *Service) Counts() (map[[2]string]int, error) {
	counts := make(map[[2]string]int)
	err := s.store.View(func(r kv.Reader) error {
		recs, err := s.list(r, "")
		for _, rec := range recs {
			v := rec.latest()
			counts[[2]string{v.Type, v.Tier}]++
		}
		return err
	})
	return counts, err
}

// --- internals ---

func (s *Service) deleteTx(tx kv.Tx, name string) error {
	if err := tx.Delete(bucket, name); err != nil {
		return err
	}
	return tags.DeleteTx(tx, tags.ResourceParameter, name)
}

// load returns the parameter record or nil when missing/expired.
func (s *Service) load(r kv.Reader, name string) (*record, error) {
	var rec record
	err := kv.GetJSON(r, bucket, name, &rec)
	if errors.Is(err, kv.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(rec.Versions) == 0 {
		return nil, nil
	}
	if t, ok := expiresAt(rec.latest().Policies); ok && !types.Now().Before(t) {
		return nil, nil
	}
	return &rec, nil
}

func (s *Service) list(r kv.Reader, prefix string) ([]*record, error) {
	entries, err := r.List(bucket, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*record, 0, len(entries))
	for _, e := range entries {
		rec, err := s.load(r, e.Key)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (s *Service) getOne(r kv.Reader, ref string, decrypt bool) (*Parameter, error) {
	sel, err := parseSelector(ref)
	if err != nil {
		return nil, err
	}
	rec, err := s.load(r, sel.Name)
	if err == nil && rec == nil && sel.FromARN && !strings.HasPrefix(sel.Name, "/") {
		rec, err = s.load(r, "/"+sel.Name)
	}
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, types.NewParameterNotFound(ref)
	}
	v := rec.latest()
	switch {
	case sel.Version > 0:
		if v = rec.findVersion(sel.Version); v == nil {
			return nil, types.NewParameterVersionNotFound(sel.Raw[1:])
		}
	case sel.Label != "":
		if v = rec.findLabel(sel.Label); v == nil {
			return nil, types.NewParameterVersionNotFound(sel.Raw[1:])
		}
	}
	return s.toParameter(r, rec.Name, v, sel.Raw, decrypt)
}

func (s *Service) reveal(r kv.Reader, name string, v *version, decrypt bool) (string, error) {
	if v.Type != TypeSecureString || !decrypt {
		return v.Value, nil
	}
	plain, err := s.kms.DecryptTx(r, v.Value, []byte(name))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Service) toParameter(r kv.Reader, name string, v *version, selector string, decrypt bool) (*Parameter, error) {
	value, err := s.reveal(r, name, v, decrypt)
	if err != nil {
		return nil, err
	}
	return &Parameter{
		Name:             name,
		Type:             v.Type,
		Value:            value,
		Version:          v.Version,
		Selector:         selector,
		LastModifiedDate: types.NewTimestamp(v.LastModifiedDate),
		ARN:              s.account.ParameterARN(name),
		DataType:         v.DataType,
	}, nil
}

func (s *Service) toMetadata(name string, v *version) *ParameterMetadata {
	return &ParameterMetadata{
		Name:             name,
		ARN:              s.account.ParameterARN(name),
		Type:             v.Type,
		KeyID:            v.KeyID,
		LastModifiedDate: types.NewTimestamp(v.LastModifiedDate),
		LastModifiedUser: v.LastModifiedUser,
		Description:      v.Description,
		AllowedPattern:   v.AllowedPattern,
		Version:          v.Version,
		Tier:             v.Tier,
		Policies:         toInlinePolicies(v.Policies),
		DataType:         v.DataType,
	}
}

func (s *Service) checkCountLimit(r kv.Reader, tier string) error {
	limit := s.opts.MaxStandardParameters
	if tier == TierAdvanced {
		limit = s.opts.MaxAdvancedParameters
	}
	if limit <= 0 {
		return nil
	}
	recs, err := s.list(r, "")
	if err != nil {
		return err
	}
	count := 0
	for _, rec := range recs {
		if rec.latest().Tier == tier {
			count++
		}
	}
	if count >= limit {
		return types.NewError(types.ErrParameterLimitExceeded, "You have exceeded the number of %s parameters allowed (%d).", strings.ToLower(tier), limit)
	}
	return nil
}

// matches applies ParameterFilters to a record/version.
func (s *Service) matches(r kv.Reader, rec *record, v *version, filters []StringFilter) (bool, error) {
	for _, f := range filters {
		var ok bool
		switch {
		case f.Key == "Name":
			ok = matchString(rec.Name, f.Option, f.Values)
		case f.Key == "Type":
			ok = slices.Contains(f.Values, v.Type)
		case f.Key == "KeyId":
			ok = v.Type == TypeSecureString && slices.Contains(f.Values, v.KeyID)
		case f.Key == "Tier":
			ok = slices.Contains(f.Values, v.Tier)
		case f.Key == "DataType":
			ok = slices.Contains(f.Values, v.DataType)
		case f.Key == "Label":
			ok = slices.ContainsFunc(f.Values, func(l string) bool { return rec.findLabel(l) != nil })
		case f.Key == "Path":
			ok = matchPath(rec.Name, f.Option, f.Values)
		case strings.HasPrefix(f.Key, "tag:"):
			m, err := tags.GetMapTx(r, tags.ResourceParameter, rec.Name)
			if err != nil {
				return false, err
			}
			val, has := m[strings.TrimPrefix(f.Key, "tag:")]
			ok = has && (len(f.Values) == 0 || slices.Contains(f.Values, val))
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func validateDescribeFilters(filters []StringFilter) error {
	for _, f := range filters {
		opt := f.Option
		switch {
		case f.Key == "Name":
			if opt != "" && opt != "Equals" && opt != "BeginsWith" && opt != "Contains" {
				return types.NewError(types.ErrInvalidFilterOption, "The filter option %s is not valid for key Name. Valid options include: [Equals, BeginsWith, Contains].", opt)
			}
		case f.Key == "Path":
			if opt != "" && opt != "Recursive" && opt != "OneLevel" {
				return types.NewError(types.ErrInvalidFilterOption, "The filter option %s is not valid for key Path. Valid options include: [Recursive, OneLevel].", opt)
			}
			for _, p := range f.Values {
				if err := validatePath(p); err != nil {
					return types.NewError(types.ErrInvalidFilterValue, "The filter value %s is not valid for key Path.", p)
				}
			}
		case f.Key == "Type" || f.Key == "KeyId" || f.Key == "Tier" || f.Key == "DataType" || f.Key == "Label" || strings.HasPrefix(f.Key, "tag:"):
			if opt != "" && opt != "Equals" {
				return types.NewError(types.ErrInvalidFilterOption, "The filter option %s is not valid for key %s. Valid options include: [Equals].", opt, f.Key)
			}
		default:
			return types.NewError(types.ErrInvalidFilterKey, "The following filter key is not valid: %s.", f.Key)
		}
		if f.Key != "Path" && !strings.HasPrefix(f.Key, "tag:") && len(f.Values) == 0 {
			return types.NewError(types.ErrInvalidFilterValue, "The filter values for key %s must not be empty.", f.Key)
		}
	}
	return nil
}

func matchString(s, option string, values []string) bool {
	for _, v := range values {
		switch option {
		case "BeginsWith":
			if strings.HasPrefix(s, v) {
				return true
			}
		case "Contains":
			if strings.Contains(strings.ToLower(s), strings.ToLower(v)) {
				return true
			}
		default:
			if s == v || s == normalizeName(v) {
				return true
			}
		}
	}
	return false
}

func matchPath(name, option string, paths []string) bool {
	if !strings.HasPrefix(name, "/") {
		return false
	}
	for _, p := range paths {
		prefix := p
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if option == "Recursive" || !strings.Contains(name[len(prefix):], "/") {
			return true
		}
	}
	return false
}

func filterLabel(filters []StringFilter) []string {
	for _, f := range filters {
		if f.Key == "Label" {
			return f.Values
		}
	}
	return nil
}

// trimVersions drops the oldest unlabeled version once MaxVersions is
// exceeded, like AWS. If the oldest version is labeled the put fails.
func trimVersions(rec *record) error {
	if len(rec.Versions) <= MaxVersions {
		return nil
	}
	if len(rec.Versions[0].Labels) > 0 {
		return types.NewError(types.ErrParameterMaxVersionLimitExceeded,
			"You attempted to create a new version of %s by calling the PutParameter API with the overwrite flag. Version %d, the oldest version, can't be deleted because it has a label associated with it. Move the label to another version of the parameter, and try again.",
			rec.Name, rec.Versions[0].Version)
	}
	rec.Versions = rec.Versions[1:]
	return nil
}

func pick(in *string, prev *version, get func(*version) string) string {
	if in != nil {
		return *in
	}
	if prev != nil {
		return get(prev)
	}
	return ""
}

func callerName(ctx context.Context) string {
	if c, ok := types.CallerFrom(ctx); ok {
		return c.Name
	}
	return ""
}

// SortByName is a helper for UI listings.
func SortByName(ps []*ParameterMetadata) {
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
}
