package parameters

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

var (
	nameCharsRe = regexp.MustCompile(`^[a-zA-Z0-9_.\-/]+$`)
	labelRe     = regexp.MustCompile(`^[a-zA-Z0-9_.\-]+$`)
	amiRe       = regexp.MustCompile(`^ami-[0-9a-f]{8,17}$`)
)

// selector is a parsed "name[:version|:label]" reference.
type selector struct {
	Name    string
	Version int64  // > 0 when a version selector was given
	Label   string // non-empty when a label selector was given
	Raw     string // ":3" / ":prod" or ""
	FromARN bool   // name came from an ARN (single-level names are ambiguous)
}

// normalizeName converts a parameter ARN to its name and leaves names as-is.
// "arn:aws:ssm:r:a:parameter/a/b" -> "/a/b", ".../parameter/foo" -> "foo".
func normalizeName(ref string) string {
	if !strings.HasPrefix(ref, "arn:") {
		return ref
	}
	parts := strings.SplitN(ref, ":", 6)
	if len(parts) != 6 || !strings.HasPrefix(parts[5], "parameter/") {
		return ref
	}
	rest := strings.TrimPrefix(parts[5], "parameter/")
	if strings.Contains(rest, "/") {
		return "/" + rest
	}
	return rest
}

// parseSelector splits "name:selector" after normalizing ARNs.
func parseSelector(ref string) (selector, error) {
	fromARN := strings.HasPrefix(ref, "arn:")
	ref = normalizeName(ref)
	i := strings.LastIndex(ref, ":")
	if i < 0 {
		return selector{Name: ref, FromARN: fromARN}, nil
	}
	sel := selector{Name: ref[:i], Raw: ref[i:], FromARN: fromARN}
	suffix := ref[i+1:]
	if suffix == "" {
		return sel, types.NewValidationError("Invalid parameter selector %q", ref)
	}
	if n, err := strconv.ParseInt(suffix, 10, 64); err == nil {
		if n <= 0 {
			return sel, types.NewValidationError("Invalid parameter version %q", suffix)
		}
		sel.Version = n
	} else {
		sel.Label = suffix
	}
	return sel, nil
}

// validateName enforces AWS parameter naming rules.
func validateName(name string) error {
	if name == "" {
		return types.NewValidationError("1 validation error detected: Value '' at 'name' failed to satisfy constraint: Member must have length greater than or equal to 1")
	}
	if len(name) > MaxNameLength {
		return types.NewValidationError("Parameter name must be at most %d characters", MaxNameLength)
	}
	if !nameCharsRe.MatchString(name) {
		return types.NewValidationError("Parameter name: can't be prefixed with \"aws\" or \"ssm\" (case-insensitive). It must use only letters, numbers, or the following symbols: . (period), - (hyphen), _ (underscore). Special characters are not allowed. All sub-paths, if specified, must use the forward slash symbol \"/\". Valid example: /get/parameters2-/by1./path0_.")
	}
	trimmed := strings.ToLower(strings.TrimPrefix(name, "/"))
	if strings.HasPrefix(trimmed, "aws") || strings.HasPrefix(trimmed, "ssm") {
		return types.NewValidationError("No access to reserved parameter name: %s.", name)
	}
	if strings.Contains(name, "/") {
		if !strings.HasPrefix(name, "/") {
			return types.NewValidationError("Parameter name: if a hierarchy is specified, the name must be fully qualified with a leading forward slash.")
		}
		if strings.HasSuffix(name, "/") || strings.Contains(name, "//") {
			return types.NewValidationError("Parameter name %s is not a valid hierarchical name", name)
		}
	}
	if depth(name) > MaxHierarchyDepth {
		return types.NewError(types.ErrHierarchyLevelLimitExceeded, "A hierarchy can have a maximum of %d levels.", MaxHierarchyDepth)
	}
	return nil
}

// validatePath enforces rules for GetParametersByPath/Path filters.
func validatePath(path string) error {
	if path == "" || !strings.HasPrefix(path, "/") {
		return types.NewValidationError("The parameter doesn't meet the parameter name requirements. The parameter name must begin with a forward slash \"/\".")
	}
	if path != "/" && !nameCharsRe.MatchString(path) {
		return types.NewValidationError("Invalid path %s", path)
	}
	if depth(strings.TrimSuffix(path, "/")) > MaxHierarchyDepth {
		return types.NewError(types.ErrHierarchyLevelLimitExceeded, "A hierarchy can have a maximum of %d levels.", MaxHierarchyDepth)
	}
	return nil
}

// depth counts hierarchy levels: "/a/b/c" -> 3 (the leaf counts as a level).
func depth(name string) int {
	return strings.Count(name, "/")
}

// validateLabel returns false for labels AWS reports as InvalidLabels.
func validateLabel(label string) bool {
	if label == "" || len(label) > MaxLabelLength || !labelRe.MatchString(label) {
		return false
	}
	if unicode.IsDigit(rune(label[0])) {
		return false
	}
	lower := strings.ToLower(label)
	return !strings.HasPrefix(lower, "aws") && !strings.HasPrefix(lower, "ssm")
}

func validateType(t string) error {
	switch t {
	case TypeString, TypeStringList, TypeSecureString:
		return nil
	}
	return types.NewError(types.ErrUnsupportedParameterType, "1 validation error detected: Value '%s' at 'type' failed to satisfy constraint: Member must satisfy enum value set: [SecureString, StringList, String]", t)
}

func validateDataType(dataType, value string) error {
	switch dataType {
	case DataTypeText, DataTypeSSMIntegration:
		return nil
	case DataTypeEC2Image:
		if !amiRe.MatchString(value) {
			return types.NewValidationError("Parameter value is not a valid AMI ID. AMI IDs must use the format ami-xxxxxxxxxxxxxxxxx.")
		}
		return nil
	}
	return types.NewValidationError("The following data type is not supported: %s (Data type names are all lowercase.)", dataType)
}

// compilePattern compiles an AllowedPattern with full-match semantics,
// matching the Java Pattern.matches behaviour used by AWS.
func compilePattern(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > MaxAllowedPatternLen {
		return nil, types.NewError(types.ErrInvalidAllowedPattern, "The allowed pattern is too long.")
	}
	re, err := regexp.Compile(`^(?:` + pattern + `)$`)
	if err != nil {
		return nil, types.NewError(types.ErrInvalidAllowedPattern, "The allowed pattern is not a valid regular expression: %v", err)
	}
	return re, nil
}

func checkPattern(pattern, value string) error {
	if pattern == "" {
		return nil
	}
	re, err := compilePattern(pattern)
	if err != nil {
		return err
	}
	if !re.MatchString(value) {
		return types.NewError(types.ErrParameterPatternMismatch, "Parameter value, cannot be validated against allowedPattern: %s", pattern)
	}
	return nil
}

// resolveTier picks the effective tier for a value, honoring Intelligent-Tiering
// and refusing downgrades from Advanced to Standard.
func resolveTier(requested, current string, valueLen int, hasPolicies bool) (string, error) {
	tier := requested
	if tier == TierIntelligentTiering {
		tier = TierStandard
		if valueLen > MaxStandardValueBytes || hasPolicies || current == TierAdvanced {
			tier = TierAdvanced
		}
	}
	switch tier {
	case TierStandard, TierAdvanced:
	default:
		return "", types.NewValidationError("1 validation error detected: Value '%s' at 'tier' failed to satisfy constraint: Member must satisfy enum value set: [Advanced, Intelligent-Tiering, Standard]", requested)
	}
	if current == TierAdvanced && tier == TierStandard {
		return "", types.NewValidationError("This parameter uses the advanced-parameter tier. You can't downgrade a parameter from the advanced-parameter tier to the standard-parameter tier. If necessary, you can delete the advanced parameter and recreate it as a standard parameter.")
	}
	limit := MaxStandardValueBytes
	if tier == TierAdvanced {
		limit = MaxAdvancedValueBytes
	}
	if valueLen > limit {
		if tier == TierStandard {
			return "", types.NewValidationError("Standard tier parameters support a maximum parameter value of 4096 characters. To create a larger parameter value, upgrade the parameter to use the advanced-parameter tier.")
		}
		return "", types.NewValidationError("Advanced tier parameters support a maximum parameter value of 8192 characters.")
	}
	if hasPolicies && tier == TierStandard {
		return "", types.NewError(types.ErrIncompatiblePolicy, "Parameter policies are supported only for advanced parameters.")
	}
	return tier, nil
}
