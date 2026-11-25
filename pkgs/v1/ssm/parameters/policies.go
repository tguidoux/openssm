package parameters

import (
	"encoding/json"
	"time"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

// Policy types supported by Parameter Store.
const (
	PolicyExpiration             = "Expiration"
	PolicyExpirationNotification = "ExpirationNotification"
	PolicyNoChangeNotification   = "NoChangeNotification"
)

// parsePolicies validates the Policies JSON array from PutParameter.
func parsePolicies(raw string) ([]policy, error) {
	if raw == "" {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, types.NewError("InvalidPolicyTypeException", "Policies must be a JSON array of policy objects: %v", err)
	}
	if len(items) > 10 {
		return nil, types.NewError("PoliciesLimitExceededException", "You specified more than the maximum number of allowed policies for the parameter.")
	}
	seen := map[string]bool{}
	out := make([]policy, 0, len(items))
	for _, item := range items {
		var p policy
		if err := json.Unmarshal(item, &p); err != nil {
			return nil, types.NewError("InvalidPolicyTypeException", "Invalid policy: %v", err)
		}
		switch p.Type {
		case PolicyExpiration:
			ts, ok := p.Attributes["Timestamp"]
			if !ok {
				return nil, types.NewError("InvalidPolicyAttributeException", "Expiration policy requires a Timestamp attribute.")
			}
			if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
				return nil, types.NewError("InvalidPolicyAttributeException", "Invalid Expiration Timestamp %q", ts)
			}
		case PolicyExpirationNotification, PolicyNoChangeNotification:
			if _, ok := p.Attributes["Before"]; !ok && p.Type == PolicyExpirationNotification {
				return nil, types.NewError("InvalidPolicyAttributeException", "ExpirationNotification policy requires a Before attribute.")
			}
			if _, ok := p.Attributes["After"]; !ok && p.Type == PolicyNoChangeNotification {
				return nil, types.NewError("InvalidPolicyAttributeException", "NoChangeNotification policy requires an After attribute.")
			}
		default:
			return nil, types.NewError("InvalidPolicyTypeException", "The policy type %q is not supported.", p.Type)
		}
		if seen[p.Type] {
			return nil, types.NewError("InvalidPolicyTypeException", "Duplicate policy type %s", p.Type)
		}
		seen[p.Type] = true
		p.Text = string(item)
		out = append(out, p)
	}
	return out, nil
}

// expiresAt returns the expiration time defined by the policies, if any.
func expiresAt(policies []policy) (time.Time, bool) {
	for _, p := range policies {
		if p.Type == PolicyExpiration {
			if t, err := time.Parse(time.RFC3339Nano, p.Attributes["Timestamp"]); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func toInlinePolicies(policies []policy) []ParameterInlinePolicy {
	out := make([]ParameterInlinePolicy, 0, len(policies))
	for _, p := range policies {
		out = append(out, ParameterInlinePolicy{PolicyText: p.Text, PolicyType: p.Type, PolicyStatus: "Pending"})
	}
	return out
}
