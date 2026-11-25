// Package tags stores resource tags for every taggable SSM resource type
// (Parameter, Document, ManagedInstance, ...). Domain packages call the Tx
// helpers inside their own kv transactions so tags are created and removed
// atomically with the resource.
package tags

import (
	"errors"
	"sort"
	"strings"

	"github.com/tguidoux/openssm/pkgs/v1/kv"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

const bucket = "tags"

// Resource types accepted by AddTagsToResource and friends.
const (
	ResourceParameter       = "Parameter"
	ResourceDocument        = "Document"
	ResourceManagedInstance = "ManagedInstance"
	ResourceMaintenanceWin  = "MaintenanceWindow"
	ResourcePatchBaseline   = "PatchBaseline"
	ResourceOpsItem         = "OpsItem"
	ResourceOpsMetadata     = "OpsMetadata"
	ResourceAutomation      = "Automation"
	ResourceAssociation     = "Association"
)

// MaxTagsPerResource is the AWS limit on tags per resource.
const MaxTagsPerResource = 50

// ExistsFunc reports whether a resource exists inside the given transaction.
type ExistsFunc func(r kv.Reader, resourceID string) (bool, error)

// Service implements tag CRUD with per-resource-type existence checks.
type Service struct {
	store    kv.Store
	resolver map[string]ExistsFunc
}

// New creates a tag service. Register resource types with Register.
func New(store kv.Store) *Service {
	return &Service{store: store, resolver: make(map[string]ExistsFunc)}
}

// Register declares a taggable resource type and how to verify existence.
func (s *Service) Register(resourceType string, exists ExistsFunc) {
	s.resolver[resourceType] = exists
}

// Add merges tags into the resource's tag set.
func (s *Service) Add(resourceType, resourceID string, add []types.Tag) error {
	if len(add) == 0 {
		return types.NewValidationError("1 validation error detected: Value null at 'tags' failed to satisfy constraint: Member must not be null")
	}
	if err := ValidateTags(add); err != nil {
		return err
	}
	return s.store.Update(func(tx kv.Tx) error {
		if err := s.checkExists(tx, resourceType, resourceID); err != nil {
			return err
		}
		current, err := getMap(tx, resourceType, resourceID)
		if err != nil {
			return err
		}
		for _, t := range add {
			current[t.Key] = t.Value
		}
		if len(current) > MaxTagsPerResource {
			return types.NewError(types.ErrTooManyTagsError, "The Targets parameter includes too many tags. Remove one or more tags and try the command again.")
		}
		return putMap(tx, resourceType, resourceID, current)
	})
}

// Remove deletes the given tag keys from the resource.
func (s *Service) Remove(resourceType, resourceID string, keys []string) error {
	return s.store.Update(func(tx kv.Tx) error {
		if err := s.checkExists(tx, resourceType, resourceID); err != nil {
			return err
		}
		current, err := getMap(tx, resourceType, resourceID)
		if err != nil {
			return err
		}
		for _, k := range keys {
			delete(current, k)
		}
		return putMap(tx, resourceType, resourceID, current)
	})
}

// List returns the resource's tags sorted by key.
func (s *Service) List(resourceType, resourceID string) ([]types.Tag, error) {
	var out []types.Tag
	err := s.store.View(func(r kv.Reader) error {
		if err := s.checkExists(r, resourceType, resourceID); err != nil {
			return err
		}
		var err error
		out, err = GetTx(r, resourceType, resourceID)
		return err
	})
	return out, err
}

func (s *Service) checkExists(r kv.Reader, resourceType, resourceID string) error {
	exists, ok := s.resolver[resourceType]
	if !ok {
		return types.NewError(types.ErrInvalidResourceType, "The resource type is not valid. ResourceType: %s", resourceType)
	}
	found, err := exists(r, resourceID)
	if err != nil {
		return err
	}
	if !found {
		return types.NewInvalidResourceID()
	}
	return nil
}

// ValidateTags checks AWS tag key/value constraints.
func ValidateTags(tags []types.Tag) error {
	if len(tags) > MaxTagsPerResource {
		return types.NewError(types.ErrTooManyTagsError, "The Targets parameter includes too many tags. Remove one or more tags and try the command again.")
	}
	for _, t := range tags {
		if t.Key == "" || len(t.Key) > 128 {
			return types.NewValidationError("1 validation error detected: Value '%s' at 'tags.key' failed to satisfy constraint: Member must have length between 1 and 128", t.Key)
		}
		if len(t.Value) > 256 {
			return types.NewValidationError("1 validation error detected: Value at 'tags.value' failed to satisfy constraint: Member must have length less than or equal to 256")
		}
		if strings.HasPrefix(strings.ToLower(t.Key), "aws:") {
			return types.NewValidationError("Tag keys beginning with 'aws:' are reserved")
		}
	}
	return nil
}

// SetTx replaces the resource's tags inside an existing transaction.
func SetTx(tx kv.Tx, resourceType, resourceID string, tags []types.Tag) error {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[t.Key] = t.Value
	}
	return putMap(tx, resourceType, resourceID, m)
}

// DeleteTx removes all tags of a resource inside an existing transaction.
func DeleteTx(tx kv.Tx, resourceType, resourceID string) error {
	return tx.Delete(bucket, key(resourceType, resourceID))
}

// GetTx returns tags sorted by key inside an existing transaction.
func GetTx(r kv.Reader, resourceType, resourceID string) ([]types.Tag, error) {
	m, err := getMap(r, resourceType, resourceID)
	if err != nil {
		return nil, err
	}
	out := make([]types.Tag, 0, len(m))
	for k, v := range m {
		out = append(out, types.Tag{Key: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// GetMapTx returns tags as a map inside an existing transaction.
func GetMapTx(r kv.Reader, resourceType, resourceID string) (map[string]string, error) {
	return getMap(r, resourceType, resourceID)
}

func key(resourceType, resourceID string) string {
	return resourceType + "\x00" + resourceID
}

func getMap(r kv.Reader, resourceType, resourceID string) (map[string]string, error) {
	m := make(map[string]string)
	err := kv.GetJSON(r, bucket, key(resourceType, resourceID), &m)
	if errors.Is(err, kv.ErrNotFound) {
		return make(map[string]string), nil
	}
	return m, err
}

func putMap(tx kv.Tx, resourceType, resourceID string, m map[string]string) error {
	if len(m) == 0 {
		return tx.Delete(bucket, key(resourceType, resourceID))
	}
	return kv.PutJSON(tx, bucket, key(resourceType, resourceID), m)
}
