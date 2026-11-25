// Package config provides YAML-based configuration loading with schema validation
// and environment-specific overrides for OpenSSM services.
package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

const (
	DefaultConfigPath   = "config.yaml"
	DefaultConfigEnvVar = "CONFIG_PATH"
)

type ConfigI[ConfigType any] interface {
	Validate() error
	WithValidation() ConfigType
}

type Config[ConfigType any] struct {
	ConfigPath string
	Config     ConfigType
	data       []byte
}

func NewConfig[ConfigType any](configPath string) Config[ConfigType] {
	config := Config[ConfigType]{
		ConfigPath: configPath,
	}

	config.readConfig()

	return config
}

func NewConfigFromEnv[ConfigType ConfigI[ConfigType]](envVar ...string) *Config[ConfigType] {
	var env string
	if len(envVar) == 0 || envVar[0] == "" {
		env = DefaultConfigEnvVar
	} else {
		env = envVar[0]
	}

	configPath := os.Getenv(env)

	if configPath == "" {
		configPath = DefaultConfigPath
	}

	config := NewConfig[ConfigType](configPath)

	return &config
}

func (c *Config[ConfigType]) readConfig() {

	// Read file from ConfigPath
	data, err := os.ReadFile(c.ConfigPath)
	if err != nil {
		panic(fmt.Errorf("failed to read config file %s: %w", c.ConfigPath, err))
	}
	c.data = data

	var config ConfigType
	if err := yaml.Unmarshal(c.data, &config); err != nil {
		panic(fmt.Errorf("failed to convert raw config to ConfigType: %w", err))
	}
	if err := ApplyEnvOverrides(&config, os.Environ()); err != nil {
		panic(fmt.Errorf("failed to apply environment overrides to %s: %w", c.ConfigPath, err))
	}

	c.Config = config
}

// EnvSeparator separates nested keys in environment overrides,
// e.g. SSM__SERVERSECRET overrides ssm.serverSecret.
const EnvSeparator = "__"

// ApplyEnvOverrides overlays environment variables of the form
// SECTION__KEY[__SUBKEY]=value onto cfg (a pointer to a struct). Segments are
// matched case-insensitively against yaml struct tags. Variables whose first
// segment doesn't match a top-level field are ignored, so unrelated
// environment variables are harmless. Only scalar fields (string, bool,
// integers, floats, and pointers to them) can be overridden.
func ApplyEnvOverrides(cfg any, environ []string) error {
	root := reflect.ValueOf(cfg)
	if root.Kind() != reflect.Pointer || root.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config must be a pointer to a struct, got %T", cfg)
	}
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.Contains(name, EnvSeparator) {
			continue
		}
		path := strings.Split(name, EnvSeparator)
		if fieldByTag(root.Elem(), path[0]) == nil {
			continue
		}
		if err := setByPath(root.Elem(), path, value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func fieldByTag(v reflect.Value, segment string) *reflect.Value {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" {
			tag = f.Name
		}
		if strings.EqualFold(tag, segment) {
			fv := v.Field(i)
			return &fv
		}
	}
	return nil
}

func setByPath(v reflect.Value, path []string, value string) error {
	f := fieldByTag(v, path[0])
	if f == nil {
		return fmt.Errorf("unknown config key %q", path[0])
	}
	if len(path) == 1 {
		return setScalar(*f, value)
	}
	target := *f
	if target.Kind() == reflect.Pointer {
		if target.IsNil() {
			target.Set(reflect.New(target.Type().Elem()))
		}
		target = target.Elem()
	}
	if target.Kind() != reflect.Struct {
		return fmt.Errorf("config key %q is not a section", path[0])
	}
	return setByPath(target, path[1:], value)
}

func setScalar(f reflect.Value, value string) error {
	if f.Kind() == reflect.Pointer {
		ptr := reflect.New(f.Type().Elem())
		if err := setScalar(ptr.Elem(), value); err != nil {
			return err
		}
		f.Set(ptr)
		return nil
	}
	switch f.Kind() {
	case reflect.String:
		f.SetString(value)
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		f.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		f.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
		f.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		f.SetFloat(n)
	default:
		return fmt.Errorf("cannot override %s field from environment", f.Kind())
	}
	return nil
}

func (c *Config[ConfigType]) Validate() ConfigType {
	if len(c.data) == 0 {
		panic(fmt.Errorf("config file %s is empty", c.ConfigPath))
	}
	return c.Config
}

func (c *Config[ConfigType]) WithValidation() ConfigType {
	return c.Validate()
}
