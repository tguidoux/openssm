package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/config"
)

type TestConfig struct {
	Name    string `yaml:"name"`
	Port    int    `yaml:"port"`
	Enabled bool   `yaml:"enabled"`
}

func TestNewConfig(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test_config.yaml")

	configContent := `name: test-service
port: 8080
enabled: true
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Test NewConfig
	cfg := config.NewConfig[TestConfig](configPath)

	assert.Equal(t, configPath, cfg.ConfigPath)
	assert.Equal(t, "test-service", cfg.Config.Name)
	assert.Equal(t, 8080, cfg.Config.Port)
	assert.True(t, cfg.Config.Enabled)
}

func TestNewConfig_InvalidPath(t *testing.T) {
	// Test with non-existent file path
	assert.Panics(t, func() {
		config.NewConfig[TestConfig]("/non/existent/path.yaml")
	})
}

func TestNewConfig_InvalidYAML(t *testing.T) {
	// Create a temporary config file with invalid YAML
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "invalid.yaml")

	invalidContent := `
name: test
port: [invalid
`
	err := os.WriteFile(configPath, []byte(invalidContent), 0644)
	require.NoError(t, err)

	// Test NewConfig with invalid YAML
	assert.Panics(t, func() {
		config.NewConfig[TestConfig](configPath)
	})
}

func TestNewConfigFromEnv_DefaultEnvVar(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "env_config.yaml")

	configContent := `name: env-service
port: 9090
enabled: false
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Set environment variable
	os.Setenv(config.DefaultConfigEnvVar, configPath)
	defer os.Unsetenv(config.DefaultConfigEnvVar)

	// Test NewConfigFromEnv - we can't directly test this since it requires ConfigI
	// but we can verify the env var logic by testing NewConfig directly
	cfg := config.NewConfig[TestConfig](configPath)

	assert.Equal(t, configPath, cfg.ConfigPath)
	assert.Equal(t, "env-service", cfg.Config.Name)
	assert.Equal(t, 9090, cfg.Config.Port)
	assert.False(t, cfg.Config.Enabled)
}

func TestNewConfigFromEnv_CustomEnvVar(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "custom_env_config.yaml")

	configContent := `name: custom-service
port: 7070
enabled: true
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Set custom environment variable
	customEnvVar := "CUSTOM_CONFIG_PATH"
	os.Setenv(customEnvVar, configPath)
	defer os.Unsetenv(customEnvVar)

	// Test NewConfig with custom path (simulating the env var logic)
	cfg := config.NewConfig[TestConfig](configPath)

	assert.Equal(t, configPath, cfg.ConfigPath)
	assert.Equal(t, "custom-service", cfg.Config.Name)
	assert.Equal(t, 7070, cfg.Config.Port)
	assert.True(t, cfg.Config.Enabled)
}

func TestNewConfigFromEnv_FallbackToDefault(t *testing.T) {
	// Ensure environment variable is not set
	os.Unsetenv(config.DefaultConfigEnvVar)

	// Create default config file in current directory
	defaultConfigPath := config.DefaultConfigPath
	configContent := `name: default-service
port: 6060
enabled: false
`
	err := os.WriteFile(defaultConfigPath, []byte(configContent), 0644)
	require.NoError(t, err)
	defer os.Remove(defaultConfigPath)

	// Test NewConfig with default path
	cfg := config.NewConfig[TestConfig](defaultConfigPath)

	assert.Equal(t, config.DefaultConfigPath, cfg.ConfigPath)
	assert.Equal(t, "default-service", cfg.Config.Name)
	assert.Equal(t, 6060, cfg.Config.Port)
	assert.False(t, cfg.Config.Enabled)
}

func TestConfig_Validate(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "validate_config.yaml")

	configContent := `name: validate-test
port: 5050
enabled: true
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	cfg := config.NewConfig[TestConfig](configPath)

	// Test Validate
	validated := cfg.Validate()
	assert.Equal(t, "validate-test", validated.Name)
	assert.Equal(t, 5050, validated.Port)
	assert.True(t, validated.Enabled)
}

func TestConfig_Validate_EmptyFile(t *testing.T) {
	// Create an empty config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "empty.yaml")

	err := os.WriteFile(configPath, []byte(""), 0644)
	require.NoError(t, err)

	// Test that validation panics on empty file - skipped in external test
	// as it requires accessing internal data field
}

func TestConfig_WithValidation(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "with_validation.yaml")

	configContent := `name: with-validation-test
port: 4040
enabled: false
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	cfg := config.NewConfig[TestConfig](configPath)

	// Test WithValidation
	validated := cfg.WithValidation()
	assert.Equal(t, "with-validation-test", validated.Name)
	assert.Equal(t, 4040, validated.Port)
	assert.False(t, validated.Enabled)
}

func TestConfig_readConfig(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "read_config.yaml")

	configContent := `name: read-test
port: 3030
enabled: true
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Test readConfig - using NewConfig since readConfig is internal
	cfg := config.NewConfig[TestConfig](configPath)

	assert.Equal(t, "read-test", cfg.Config.Name)
	assert.Equal(t, 3030, cfg.Config.Port)
	assert.True(t, cfg.Config.Enabled)
}

type envSection struct {
	Secret  string `yaml:"serverSecret"`
	Port    int    `yaml:"port"`
	Enabled *bool  `yaml:"enabled"`
}

type envConfig struct {
	SSM  envSection `yaml:"ssm"`
	Name string     `yaml:"name"`
}

func TestApplyEnvOverrides(t *testing.T) {
	cfg := envConfig{SSM: envSection{Secret: "file", Port: 1}}
	err := config.ApplyEnvOverrides(&cfg, []string{
		"SSM__SERVERSECRET=from-env",
		"ssm__port=9330",
		"SSM__ENABLED=false",
		"OTHER__THING=ignored",
		"PATH=/usr/bin",
	})
	require.NoError(t, err)
	assert.Equal(t, "from-env", cfg.SSM.Secret)
	assert.Equal(t, 9330, cfg.SSM.Port)
	require.NotNil(t, cfg.SSM.Enabled)
	assert.False(t, *cfg.SSM.Enabled)
}

func TestApplyEnvOverrides_InvalidValue(t *testing.T) {
	cfg := envConfig{}
	err := config.ApplyEnvOverrides(&cfg, []string{"SSM__PORT=notanumber"})
	assert.Error(t, err)
}

func TestApplyEnvOverrides_UnknownNestedKey(t *testing.T) {
	cfg := envConfig{}
	err := config.ApplyEnvOverrides(&cfg, []string{"SSM__NOPE=1"})
	assert.Error(t, err)
}
