package documents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_\-.]{3,128}$`)

// actionPlatforms maps plugin actions to the platforms that support them.
var actionPlatforms = map[string][]string{
	"aws:runShellScript":      {"Linux", "MacOS"},
	"aws:runPowerShellScript": {"Windows", "Linux"},
}

// parseContent parses and validates document content for the given type and
// format, returning the normalized content.
func parseContent(raw, format, docType string) (*Content, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, types.NewError(types.ErrInvalidDocumentContent, "Document content is empty.")
	}
	if len(raw) > MaxContentBytes {
		return nil, types.NewError(types.ErrMaxDocumentSizeExceeded, "The content for the document exceeds the maximum size of %d bytes.", MaxContentBytes)
	}
	var generic map[string]any
	switch format {
	case FormatJSON:
		if err := json.Unmarshal([]byte(raw), &generic); err != nil {
			return nil, types.NewError(types.ErrInvalidDocumentContent, "Document content is not valid JSON: %v", err)
		}
	case FormatYAML:
		if err := yaml.Unmarshal([]byte(raw), &generic); err != nil {
			return nil, types.NewError(types.ErrInvalidDocumentContent, "Document content is not valid YAML: %v", err)
		}
	case FormatText:
		return &Content{}, nil
	default:
		return nil, types.NewValidationError("1 validation error detected: Value '%s' at 'documentFormat' failed to satisfy constraint: Member must satisfy enum value set: [YAML, JSON, TEXT]", format)
	}
	if generic == nil {
		return nil, types.NewError(types.ErrInvalidDocumentContent, "Document content must be an object.")
	}
	// Round-trip through JSON to normalize YAML maps into our structs.
	normalized, err := json.Marshal(generic)
	if err != nil {
		return nil, types.NewError(types.ErrInvalidDocumentContent, "Document content cannot be normalized: %v", err)
	}
	var c Content
	if err := json.Unmarshal(normalized, &c); err != nil {
		return nil, types.NewError(types.ErrInvalidDocumentContent, "Document content has an invalid structure: %v", err)
	}
	if err := validateContent(&c, generic, docType); err != nil {
		return nil, err
	}
	return &c, nil
}

func validateContent(c *Content, raw map[string]any, docType string) error {
	if c.SchemaVersion == "" {
		return types.NewError(types.ErrInvalidDocumentContent, "Document schemaVersion is required.")
	}
	for name, p := range c.Parameters {
		switch p.Type {
		case "String", "StringList", "Integer", "Boolean", "MapList", "StringMap":
		default:
			return types.NewError(types.ErrInvalidDocumentContent, "Parameter %q has an unsupported type %q.", name, p.Type)
		}
	}
	switch docType {
	case TypeCommand:
		switch c.SchemaVersion {
		case "2.2", "2.0":
			if len(c.MainSteps) == 0 {
				return types.NewError(types.ErrInvalidDocumentContent, "Document with schema version %s requires mainSteps.", c.SchemaVersion)
			}
			seen := map[string]bool{}
			for _, s := range c.MainSteps {
				if s.Action == "" || s.Name == "" {
					return types.NewError(types.ErrInvalidDocumentContent, "Each step requires an action and a name.")
				}
				if seen[s.Name] {
					return types.NewError(types.ErrInvalidDocumentContent, "Duplicate step name %q.", s.Name)
				}
				seen[s.Name] = true
			}
		case "1.2":
			rc, ok := raw["runtimeConfig"].(map[string]any)
			if !ok || len(rc) == 0 {
				return types.NewError(types.ErrInvalidDocumentContent, "Document with schema version 1.2 requires runtimeConfig.")
			}
			// Convert runtimeConfig into mainSteps for uniform execution.
			actions := make([]string, 0, len(rc))
			for k := range rc {
				actions = append(actions, k)
			}
			sort.Strings(actions)
			for _, action := range actions {
				props, _ := rc[action].(map[string]any)
				inputs := map[string]any{}
				if list, ok := props["properties"].([]any); ok && len(list) > 0 {
					if m, ok := list[0].(map[string]any); ok {
						inputs = m
					}
				}
				c.MainSteps = append(c.MainSteps, Step{Action: action, Name: strings.TrimPrefix(action, "aws:"), Inputs: inputs})
			}
		default:
			return types.NewError(types.ErrInvalidDocumentSchemaVersion, "Schema version %s is not supported for Command documents. Supported versions: 1.2, 2.0, 2.2.", c.SchemaVersion)
		}
	case TypeSession:
		if c.SchemaVersion != "1.0" {
			return types.NewError(types.ErrInvalidDocumentSchemaVersion, "Schema version %s is not supported for Session documents. Supported versions: 1.0.", c.SchemaVersion)
		}
		switch c.SessionType {
		case "Standard_Stream", "InteractiveCommands", "NonInteractiveCommands", "Port":
		default:
			return types.NewError(types.ErrInvalidDocumentContent, "sessionType must be one of Standard_Stream, InteractiveCommands, NonInteractiveCommands, Port.")
		}
	}
	return nil
}

// platformTypes derives supported platforms from the step actions.
func platformTypes(c *Content, docType string) []string {
	all := []string{"Linux", "MacOS", "Windows"}
	if docType != TypeCommand {
		return all
	}
	set := map[string]bool{}
	for _, s := range c.MainSteps {
		for _, p := range actionPlatforms[s.Action] {
			set[p] = true
		}
	}
	if len(set) == 0 {
		return all
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func hashContent(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func validateName(name string) error {
	if !nameRe.MatchString(name) {
		return types.NewValidationError("1 validation error detected: Value '%s' at 'name' failed to satisfy constraint: Member must satisfy regular expression pattern: ^[a-zA-Z0-9_\\-.]{3,128}$", name)
	}
	lower := strings.ToLower(name)
	for _, reserved := range []string{"aws-", "amazon", "amzn"} {
		if strings.HasPrefix(lower, reserved) {
			return types.NewValidationError("Document name cannot start with %q (reserved prefix).", reserved)
		}
	}
	return nil
}

// convertFormat re-serializes content into the requested format.
func convertFormat(raw, from, to string) (string, error) {
	if to == "" || from == to || from == FormatText || to == FormatText {
		return raw, nil
	}
	var generic any
	var err error
	if from == FormatJSON {
		err = json.Unmarshal([]byte(raw), &generic)
	} else {
		err = yaml.Unmarshal([]byte(raw), &generic)
	}
	if err != nil {
		return "", types.NewInternalError("cannot convert document: %v", err)
	}
	var out []byte
	if to == FormatJSON {
		out, err = json.MarshalIndent(generic, "", "  ")
	} else {
		out, err = yaml.Marshal(generic)
	}
	if err != nil {
		return "", types.NewInternalError("cannot convert document: %v", err)
	}
	return string(out), nil
}

// ResolveParameters merges user-supplied parameter values with document
// defaults and validates them against the declarations.
func ResolveParameters(c *Content, supplied map[string][]string) (map[string][]string, error) {
	out := make(map[string][]string, len(c.Parameters))
	for name := range supplied {
		if _, ok := c.Parameters[name]; !ok {
			return nil, types.NewError(types.ErrInvalidParameters, "Parameter %q is not defined in the document.", name)
		}
	}
	for name, p := range c.Parameters {
		vals, ok := supplied[name]
		if !ok {
			if p.Default == nil {
				return nil, types.NewError(types.ErrInvalidParameters, "Parameters required: %s", name)
			}
			vals = defaultValues(p.Default)
		}
		if p.Type != "StringList" && p.Type != "MapList" && len(vals) > 1 {
			return nil, types.NewError(types.ErrInvalidParameters, "Parameter %q accepts a single value.", name)
		}
		for _, v := range vals {
			if len(p.AllowedValues) > 0 && !contains(p.AllowedValues, v) {
				return nil, types.NewError(types.ErrInvalidParameters, "Parameter %q value %q is not one of the allowed values.", name, v)
			}
			if p.AllowedPattern != "" && v != "" {
				if re, err := regexp.Compile(`^(?:` + p.AllowedPattern + `)$`); err == nil && !re.MatchString(v) {
					return nil, types.NewError(types.ErrInvalidParameters, "Parameter %q value %q does not match the allowed pattern.", name, v)
				}
			}
			switch p.Type {
			case "Integer":
				if _, err := strconv.Atoi(v); err != nil {
					return nil, types.NewError(types.ErrInvalidParameters, "Parameter %q must be an integer.", name)
				}
			case "Boolean":
				if _, err := strconv.ParseBool(v); err != nil {
					return nil, types.NewError(types.ErrInvalidParameters, "Parameter %q must be a boolean.", name)
				}
			}
		}
		out[name] = vals
	}
	return out, nil
}

func defaultValues(d any) []string {
	switch v := d.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out
	case string:
		return []string{v}
	case float64:
		return []string{strconv.FormatFloat(v, 'f', -1, 64)}
	case bool:
		return []string{strconv.FormatBool(v)}
	default:
		data, _ := json.Marshal(v)
		return []string{string(data)}
	}
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

var paramRefRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)

// Interpolate replaces {{ name }} references in every string within v.
// A string that is exactly one reference to a multi-value parameter expands
// into a list, matching how SSM renders runCommand arrays.
func Interpolate(v any, params map[string][]string) any {
	switch t := v.(type) {
	case string:
		if m := paramRefRe.FindStringSubmatch(t); m != nil && m[0] == strings.TrimSpace(t) {
			if vals, ok := params[m[1]]; ok && len(vals) != 1 {
				out := make([]any, len(vals))
				for i, s := range vals {
					out[i] = s
				}
				return out
			}
		}
		return paramRefRe.ReplaceAllStringFunc(t, func(ref string) string {
			name := paramRefRe.FindStringSubmatch(ref)[1]
			if vals, ok := params[name]; ok {
				return strings.Join(vals, "\n")
			}
			return ref
		})
	case []any:
		out := make([]any, 0, len(t))
		for _, item := range t {
			r := Interpolate(item, params)
			if list, ok := r.([]any); ok {
				if _, wasString := item.(string); wasString {
					out = append(out, list...)
					continue
				}
			}
			out = append(out, r)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = Interpolate(item, params)
		}
		return out
	default:
		return v
	}
}
