package instances

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
	"github.com/tguidoux/openssm/pkgs/v1/id"
	"github.com/tguidoux/openssm/pkgs/v1/kv"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/tags"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

const (
	bucketInstances   = "instances"
	bucketActivations = "activations"
)

var instanceIDRe = regexp.MustCompile(`^(mi|i)-[0-9a-f]{8,17}$`)

// ErrUnauthorized is returned when an agent token or activation is invalid.
var ErrUnauthorized = errors.New("instances: unauthorized")

// Options configures the instance service.
type Options struct {
	// PingTimeout is how long after the last heartbeat an instance is
	// reported as ConnectionLost (default 2 minutes).
	PingTimeout time.Duration
	// LatestAgentVersion is used for IsLatestVersion.
	LatestAgentVersion string
}

// Service implements managed instance operations.
type Service struct {
	store kv.Store
	opts  Options
}

// New creates the instance service and registers the ManagedInstance tag type.
func New(store kv.Store, tagSvc *tags.Service, opts Options) *Service {
	if opts.PingTimeout <= 0 {
		opts.PingTimeout = 2 * time.Minute
	}
	s := &Service{store: store, opts: opts}
	if tagSvc != nil {
		tagSvc.Register(tags.ResourceManagedInstance, func(r kv.Reader, id string) (bool, error) {
			inst, err := s.GetTx(r, id)
			return inst != nil, err
		})
	}
	return s
}

// CreateActivation creates a hybrid activation (CreateActivation).
func (s *Service) CreateActivation(ctx context.Context, in *CreateActivationInput) (*CreateActivationOutput, error) {
	if in.IamRole == "" {
		return nil, types.NewValidationError("1 validation error detected: Value null at 'iamRole' failed to satisfy constraint: Member must not be null")
	}
	limit := in.RegistrationLimit
	if limit == 0 {
		limit = 1
	}
	if limit < 1 || limit > maxRegistrationLimit {
		return nil, types.NewValidationError("RegistrationLimit must be between 1 and %d", maxRegistrationLimit)
	}
	now := types.Now()
	expires := now.Add(defaultActivationTTL)
	if in.ExpirationDate != nil {
		expires = in.ExpirationDate.UTC()
		if !expires.After(now) || expires.Sub(now) > maxActivationTTL {
			return nil, types.NewValidationError("ExpirationDate must be in the future and at most 30 days from now")
		}
	}
	if err := tags.ValidateTags(in.Tags); err != nil {
		return nil, err
	}
	code := randomToken(15)
	act := &activation{
		ActivationID: id.NewUUID(), CodeHash: hashSecret(code), Description: in.Description,
		DefaultInstanceName: in.DefaultInstanceName, IamRole: in.IamRole, RegistrationLimit: limit,
		ExpirationDate: expires, CreatedDate: now,
	}
	err := s.store.Update(func(tx kv.Tx) error {
		if err := kv.PutJSON(tx, bucketActivations, act.ActivationID, act); err != nil {
			return err
		}
		if len(in.Tags) > 0 {
			return tags.SetTx(tx, "Activation", act.ActivationID, in.Tags)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &CreateActivationOutput{ActivationID: act.ActivationID, ActivationCode: code}, nil
}

// DescribeActivations lists activations (DescribeActivations).
func (s *Service) DescribeActivations(ctx context.Context, in *DescribeActivationsInput) (*DescribeActivationsOutput, error) {
	var all []*Activation
	now := types.Now()
	err := s.store.View(func(r kv.Reader) error {
		acts, err := kv.ListJSON[activation](r, bucketActivations, "")
		if err != nil {
			return err
		}
		sort.Slice(acts, func(i, j int) bool { return acts[i].CreatedDate.After(acts[j].CreatedDate) })
		for i := range acts {
			a := &acts[i]
			ok := true
			for _, f := range in.Filters {
				switch f.FilterKey {
				case "ActivationIds":
					ok = ok && slices.Contains(f.FilterValues, a.ActivationID)
				case "DefaultInstanceName":
					ok = ok && slices.Contains(f.FilterValues, a.DefaultInstanceName)
				case "IamRole":
					ok = ok && slices.Contains(f.FilterValues, a.IamRole)
				default:
					return types.NewError(types.ErrInvalidFilterKey, "The specified filter key is not valid: %s", f.FilterKey)
				}
			}
			if !ok {
				continue
			}
			tagList, err := tags.GetTx(r, "Activation", a.ActivationID)
			if err != nil {
				return err
			}
			all = append(all, &Activation{
				ActivationID: a.ActivationID, Description: a.Description, DefaultInstanceName: a.DefaultInstanceName,
				IamRole: a.IamRole, RegistrationLimit: a.RegistrationLimit, RegistrationsCount: a.RegistrationsCount,
				ExpirationDate: types.NewTimestamp(a.ExpirationDate), Expired: a.expired(now),
				CreatedDate: types.NewTimestamp(a.CreatedDate), Tags: tagList,
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
		page = []*Activation{}
	}
	return &DescribeActivationsOutput{ActivationList: page, NextToken: next}, nil
}

// DeleteActivation deletes an activation; registered instances are kept.
func (s *Service) DeleteActivation(ctx context.Context, in *DeleteActivationInput) error {
	return s.store.Update(func(tx kv.Tx) error {
		if _, err := tx.Get(bucketActivations, in.ActivationID); err != nil {
			if errors.Is(err, kv.ErrNotFound) {
				return types.NewError(types.ErrInvalidActivationID, "The activation ID is not valid. Verify that you entered the correct ActivationId and try again.")
			}
			return err
		}
		if err := tx.Delete(bucketActivations, in.ActivationID); err != nil {
			return err
		}
		return tags.DeleteTx(tx, "Activation", in.ActivationID)
	})
}

// Register registers an agent with an activation code and returns the new
// instance ID and its token. Called by the agent API (not the SSM API).
func (s *Service) Register(ctx context.Context, in *agentproto.RegisterRequest) (*agentproto.RegisterResponse, error) {
	token := randomToken(32)
	var out *agentproto.RegisterResponse
	err := s.store.Update(func(tx kv.Tx) error {
		var act activation
		if err := kv.GetJSON(tx, bucketActivations, in.ActivationID, &act); err != nil {
			if errors.Is(err, kv.ErrNotFound) {
				return types.NewError(types.ErrInvalidActivation, "Activation %s is not valid.", in.ActivationID)
			}
			return err
		}
		now := types.Now()
		if subtle.ConstantTimeCompare([]byte(hashSecret(in.ActivationCode)), []byte(act.CodeHash)) != 1 {
			return types.NewError(types.ErrInvalidActivation, "Activation code is not valid.")
		}
		if act.expired(now) {
			return types.NewError(types.ErrInvalidActivation, "Activation %s has expired.", in.ActivationID)
		}
		if act.RegistrationsCount >= act.RegistrationLimit {
			return types.NewError(types.ErrInvalidActivation, "Activation %s has reached its registration limit.", in.ActivationID)
		}
		act.RegistrationsCount++
		if err := kv.PutJSON(tx, bucketActivations, act.ActivationID, &act); err != nil {
			return err
		}
		inst := &Instance{
			InstanceID: newInstanceID(), TokenHash: hashSecret(token), ActivationID: act.ActivationID,
			IamRole: act.IamRole, Name: act.DefaultInstanceName, RegistrationDate: now, LastPingDateTime: now,
		}
		applyInfo(inst, in.AgentInfo)
		if err := kv.PutJSON(tx, bucketInstances, inst.InstanceID, inst); err != nil {
			return err
		}
		out = &agentproto.RegisterResponse{InstanceID: inst.InstanceID, Token: inst.InstanceID + "." + token}
		return nil
	})
	return out, err
}

// Authenticate validates an agent bearer token and returns the instance ID.
func (s *Service) Authenticate(token string) (string, error) {
	instanceID, secret, ok := strings.Cut(token, ".")
	if !ok || !instanceIDRe.MatchString(instanceID) {
		return "", ErrUnauthorized
	}
	var inst Instance
	err := s.store.View(func(r kv.Reader) error { return kv.GetJSON(r, bucketInstances, instanceID, &inst) })
	if err != nil {
		return "", ErrUnauthorized
	}
	if subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(inst.TokenHash)) != 1 {
		return "", ErrUnauthorized
	}
	return instanceID, nil
}

// Heartbeat records a ping and refreshes reported host information.
func (s *Service) Heartbeat(instanceID string, info *agentproto.AgentInfo) error {
	return s.store.Update(func(tx kv.Tx) error {
		var inst Instance
		if err := kv.GetJSON(tx, bucketInstances, instanceID, &inst); err != nil {
			return err
		}
		inst.LastPingDateTime = types.Now()
		if info != nil {
			applyInfo(&inst, *info)
		}
		return kv.PutJSON(tx, bucketInstances, instanceID, &inst)
	})
}

// DescribeInstanceInformation lists managed instances.
func (s *Service) DescribeInstanceInformation(ctx context.Context, in *DescribeInstanceInformationInput) (*DescribeInstanceInformationOutput, error) {
	if len(in.InstanceInformationFilterList) > 0 && len(in.Filters) > 0 {
		return nil, types.NewValidationError("You can't specify both InstanceInformationFilterList and Filters.")
	}
	if in.MaxResults != 0 && (in.MaxResults < 5 || in.MaxResults > 50) {
		return nil, types.NewValidationError("1 validation error detected: Value '%d' at 'maxResults' failed to satisfy constraint: Member must have value between 5 and 50", in.MaxResults)
	}
	filters := append([]InstanceInformationStringFilter{}, in.Filters...)
	for _, f := range in.InstanceInformationFilterList {
		filters = append(filters, InstanceInformationStringFilter{Key: f.Key, Values: f.ValueSet})
	}
	var all []*InstanceInformation
	err := s.store.View(func(r kv.Reader) error {
		list, err := kv.ListJSON[Instance](r, bucketInstances, "")
		if err != nil {
			return err
		}
		for i := range list {
			info := s.toInfo(&list[i])
			ok, err := matchInstance(r, info, filters)
			if err != nil {
				return err
			}
			if ok {
				all = append(all, info)
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
		page = []*InstanceInformation{}
	}
	return &DescribeInstanceInformationOutput{InstanceInformationList: page, NextToken: next}, nil
}

// Deregister removes a managed instance (DeregisterManagedInstance).
func (s *Service) Deregister(ctx context.Context, in *DeregisterManagedInstanceInput) error {
	return s.store.Update(func(tx kv.Tx) error {
		inst, err := s.GetTx(tx, in.InstanceID)
		if err != nil {
			return err
		}
		if inst == nil {
			return types.NewInvalidInstanceID(in.InstanceID)
		}
		if err := tx.Delete(bucketInstances, in.InstanceID); err != nil {
			return err
		}
		return tags.DeleteTx(tx, tags.ResourceManagedInstance, in.InstanceID)
	})
}

// GetTx returns an instance or nil inside an existing transaction.
func (s *Service) GetTx(r kv.Reader, instanceID string) (*Instance, error) {
	var inst Instance
	err := kv.GetJSON(r, bucketInstances, instanceID, &inst)
	if errors.Is(err, kv.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

// PingStatus computes the ping status of an instance.
func (s *Service) PingStatus(inst *Instance) string {
	if types.Now().Sub(inst.LastPingDateTime) > s.opts.PingTimeout {
		return PingConnectionLost
	}
	return PingOnline
}

// ResolveTargetsTx expands SendCommand/StartSession targets to instance IDs.
// Supported keys: InstanceIds, tag:<key>, tag-key.
func (s *Service) ResolveTargetsTx(r kv.Reader, targets []types.Target) ([]string, error) {
	list, err := kv.ListJSON[Instance](r, bucketInstances, "")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for i, t := range targets {
		var matched []string
		switch {
		case strings.EqualFold(t.Key, "InstanceIds"):
			for _, id := range t.Values {
				if inst, err := s.GetTx(r, id); err != nil {
					return nil, err
				} else if inst != nil {
					matched = append(matched, id)
				}
			}
		case strings.HasPrefix(t.Key, "tag:") || t.Key == "tag-key":
			for _, inst := range list {
				m, err := tags.GetMapTx(r, tags.ResourceManagedInstance, inst.InstanceID)
				if err != nil {
					return nil, err
				}
				if t.Key == "tag-key" {
					if slices.ContainsFunc(t.Values, func(k string) bool { _, ok := m[k]; return ok }) {
						matched = append(matched, inst.InstanceID)
					}
					continue
				}
				if v, ok := m[strings.TrimPrefix(t.Key, "tag:")]; ok && slices.Contains(t.Values, v) {
					matched = append(matched, inst.InstanceID)
				}
			}
		default:
			return nil, types.NewValidationError("Target key %s is not supported. Supported keys: InstanceIds, tag:<key>, tag-key.", t.Key)
		}
		// Multiple targets are ANDed, like AWS.
		next := map[string]bool{}
		for _, id := range matched {
			if i == 0 || set[id] {
				next[id] = true
			}
		}
		set = next
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// PingCounts returns instance counts keyed by ping status.
func (s *Service) PingCounts() (map[string]int, error) {
	counts := map[string]int{PingOnline: 0, PingConnectionLost: 0}
	err := s.store.View(func(r kv.Reader) error {
		list, err := kv.ListJSON[Instance](r, bucketInstances, "")
		for i := range list {
			counts[s.PingStatus(&list[i])]++
		}
		return err
	})
	return counts, err
}

func (s *Service) toInfo(inst *Instance) *InstanceInformation {
	return &InstanceInformation{
		InstanceID: inst.InstanceID, PingStatus: s.PingStatus(inst), LastPingDateTime: types.NewTimestamp(inst.LastPingDateTime),
		AgentVersion: inst.AgentVersion, IsLatestVersion: s.opts.LatestAgentVersion == "" || inst.AgentVersion == s.opts.LatestAgentVersion,
		PlatformType: inst.PlatformType, PlatformName: inst.PlatformName, PlatformVersion: inst.PlatformVersion,
		ActivationID: inst.ActivationID, IamRole: inst.IamRole, RegistrationDate: types.NewTimestamp(inst.RegistrationDate),
		ResourceType: resourceTypeManagedInstance, Name: inst.Name, IPAddress: inst.IPAddress,
		ComputerName: inst.ComputerName, SourceID: inst.InstanceID, SourceType: sourceTypeManagedInstance,
	}
}

func matchInstance(r kv.Reader, info *InstanceInformation, filters []InstanceInformationStringFilter) (bool, error) {
	for _, f := range filters {
		var ok bool
		switch {
		case f.Key == "InstanceIds":
			ok = slices.Contains(f.Values, info.InstanceID)
		case f.Key == "AgentVersion":
			ok = slices.Contains(f.Values, info.AgentVersion)
		case f.Key == "PingStatus":
			ok = slices.Contains(f.Values, info.PingStatus)
		case f.Key == "PlatformTypes":
			ok = slices.Contains(f.Values, info.PlatformType)
		case f.Key == "ActivationIds":
			ok = slices.Contains(f.Values, info.ActivationID)
		case f.Key == "IamRole":
			ok = slices.Contains(f.Values, info.IamRole)
		case f.Key == "ResourceType":
			ok = slices.Contains(f.Values, info.ResourceType)
		case f.Key == "AssociationStatus":
			ok = false
		case strings.HasPrefix(f.Key, "tag:") || f.Key == "tag-key":
			m, err := tags.GetMapTx(r, tags.ResourceManagedInstance, info.InstanceID)
			if err != nil {
				return false, err
			}
			if f.Key == "tag-key" {
				ok = slices.ContainsFunc(f.Values, func(k string) bool { _, has := m[k]; return has })
			} else {
				v, has := m[strings.TrimPrefix(f.Key, "tag:")]
				ok = has && slices.Contains(f.Values, v)
			}
		default:
			return false, types.NewError(types.ErrInvalidInstanceInformationFilter, "The specified filter key is not valid: %s", f.Key)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func applyInfo(inst *Instance, info agentproto.AgentInfo) {
	if info.ComputerName != "" {
		inst.ComputerName = info.ComputerName
		if inst.Name == "" {
			inst.Name = info.ComputerName
		}
	}
	if info.IPAddress != "" {
		inst.IPAddress = info.IPAddress
	}
	if info.PlatformType != "" {
		inst.PlatformType = info.PlatformType
	}
	if info.PlatformName != "" {
		inst.PlatformName = info.PlatformName
	}
	if info.PlatformVersion != "" {
		inst.PlatformVersion = info.PlatformVersion
	}
	if info.AgentVersion != "" {
		inst.AgentVersion = info.AgentVersion
	}
}

func newInstanceID() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return "mi-" + hex.EncodeToString(b)[:17]
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
