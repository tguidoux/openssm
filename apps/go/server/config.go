package main

import (
	"fmt"

	tlsconfig "github.com/tguidoux/openssm/apps/go/server/tls"
	environment "github.com/tguidoux/openssm/pkgs/v1/environment"
)

// ServerConfig holds all configuration for the OpenSSM server.
type ServerConfig struct {
	// Server is the HTTP server configuration.
	Server ServerHTTPConfig `yaml:"server"`
	// SSM is the SSM-specific configuration.
	SSM SSMConfig `yaml:"ssm"`
	// Log is the logging configuration.
	Log LogConfig `yaml:"log"`
	// Health is the health check server configuration.
	Health HealthConfig `yaml:"health"`
	// UI is the web dashboard configuration.
	UI UIConfig `yaml:"ui"`
	// Metrics is the Prometheus metrics server configuration.
	Metrics MetricsConfig `yaml:"metrics"`
	// Environment is the deployment environment.
	Environment environment.Environment `yaml:"environment"`
	// Parameters holds Parameter Store settings and startup parameters.
	Parameters ParametersConfig `yaml:"parameters"`
	// KMS holds the local key service configuration.
	KMS KMSConfig `yaml:"kms"`
	// Agents holds managed instance / agent settings.
	Agents AgentsConfig `yaml:"agents"`
	// Sessions holds Session Manager settings.
	Sessions SessionsConfig `yaml:"sessions"`
	// RequestLogging controls structured HTTP request logging.
	RequestLogging RequestLoggingConfig `yaml:"requestLogging"`
	// RateLimit controls per-action and global rate limiting.
	RateLimit RateLimitConfig `yaml:"rateLimit"`
	// Auth controls credential-based request authentication.
	Auth AuthConfig `yaml:"auth"`
}

// ParametersConfig holds Parameter Store settings.
type ParametersConfig struct {
	// DefaultTier is the tier used when PutParameter omits Tier
	// ("Standard", "Advanced", or "Intelligent-Tiering"; default "Standard").
	DefaultTier string `yaml:"defaultTier"`
	// MaxStandardParameters caps Standard-tier parameters (AWS: 10000, 0 = unlimited).
	MaxStandardParameters int `yaml:"maxStandardParameters"`
	// MaxAdvancedParameters caps Advanced-tier parameters (AWS: 100000, 0 = unlimited).
	MaxAdvancedParameters int `yaml:"maxAdvancedParameters"`
	// Startup lists parameters to create at boot (skipped if they exist).
	Startup []StartupParameter `yaml:"startup"`
}

// StartupParameter defines a parameter created when the server starts.
type StartupParameter struct {
	Name        string            `yaml:"name"`
	Value       string            `yaml:"value"`
	Type        string            `yaml:"type"`
	Description string            `yaml:"description"`
	KeyID       string            `yaml:"keyId"`
	Tier        string            `yaml:"tier"`
	Tags        map[string]string `yaml:"tags"`
}

// AgentsConfig configures managed instances and openssm-agent behaviour.
type AgentsConfig struct {
	// PingTimeoutSeconds is how long without a heartbeat before an instance
	// is reported as ConnectionLost (default 120).
	PingTimeoutSeconds int `yaml:"pingTimeoutSeconds"`
	// LatestAgentVersion drives IsLatestVersion in DescribeInstanceInformation.
	LatestAgentVersion string `yaml:"latestAgentVersion"`
}

// SessionsConfig configures Session Manager.
type SessionsConfig struct {
	// StreamURLBase overrides the scheme/host of StreamUrl returned by
	// StartSession (e.g. "wss://ssmmessages.us-east-1.openssm.example.com").
	// session-manager-plugin only connects to hosts shaped like
	// "ssmmessages.<region>.<domain>" (or hosts listed in
	// AWS_SSM_PLUGIN_ADDITIONAL_STREAM_HOSTS). Defaults to the API endpoint.
	StreamURLBase string `yaml:"streamUrlBase"`
}

// KMSConfig configures the local key service used for SecureString values.
type KMSConfig struct {
	// MasterKey wraps all key material. Defaults to ssm.serverSecret.
	// Changing it makes existing SecureString values unreadable.
	MasterKey string `yaml:"masterKey"`
	// AutoCreateKeys creates unknown KMS key IDs/aliases on first use
	// instead of returning InvalidKeyId.
	AutoCreateKeys bool `yaml:"autoCreateKeys"`
	// Keys lists customer keys to create at boot, addressed by alias.
	Keys []StartupKey `yaml:"keys"`
}

// StartupKey defines a KMS key created at boot if its alias is missing.
type StartupKey struct {
	Alias       string `yaml:"alias"`
	Description string `yaml:"description"`
}

// HealthConfig holds health check server settings.
type HealthConfig struct {
	// Port is the port for the health check server (default: 8001).
	Port int `yaml:"port"`
	// TLS holds TLS configuration for the health check server.
	TLS TLSConfig `yaml:"tls"`
}

// UIConfig holds web dashboard settings.
type UIConfig struct {
	// Enabled controls whether the UI server starts.
	Enabled bool `yaml:"enabled"`
	// Port is the port for the UI server (default: 9331).
	Port int `yaml:"port"`
	// TLS holds TLS configuration for the UI server.
	TLS TLSConfig `yaml:"tls"`
}

// MetricsConfig holds Prometheus metrics server settings.
type MetricsConfig struct {
	// Enabled controls whether the metrics server starts.
	Enabled bool `yaml:"enabled"`
	// Port is the port for the metrics server (default: 9332).
	Port int `yaml:"port"`
	// TLS holds TLS configuration for the metrics server.
	TLS TLSConfig `yaml:"tls"`
}

// ServerHTTPConfig holds HTTP server settings.
type ServerHTTPConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// TLS holds TLS configuration for the main SSM API server.
	TLS TLSConfig `yaml:"tls"`
}

// SSMConfig holds SSM-specific settings.
type SSMConfig struct {
	// NodeAddress is the external address clients and agents use to reach the
	// server (host:port or URL). Used to build Session Manager stream URLs.
	NodeAddress string `yaml:"nodeAddress"`
	// AccountID is the AWS account ID used in ARNs.
	AccountID string `yaml:"accountId"`
	// Region is the AWS region used in ARNs.
	Region string `yaml:"region"`
	// StorageType is the storage backend ("memory", "sqlite", "badger", or "postgres").
	StorageType string `yaml:"storageType"`
	// SQLitePath is the path to the SQLite database file (used when StorageType is "sqlite").
	SQLitePath string `yaml:"sqlitePath"`
	// BadgerPath is the directory path for BadgerDB (used when StorageType is "badger").
	BadgerPath string `yaml:"badgerPath"`
	// PostgresDSN is the connection string for PostgreSQL (used when StorageType is "postgres").
	PostgresDSN string `yaml:"postgresDsn"`
	// ServerSecret signs agent/session tokens and is the default KMS master key.
	ServerSecret string `yaml:"serverSecret"`
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level string `yaml:"level"`
}

// TLSConfig holds TLS/HTTPS configuration for an HTTP server.
// It is a thin wrapper around the tls package's Config struct
// so it can be embedded in config structs via YAML.
type TLSConfig struct {
	// Enabled controls whether TLS is used for this server.
	Enabled bool `yaml:"enabled"`
	// CertFile is the path to the TLS certificate file (PEM format).
	CertFile string `yaml:"certFile"`
	// KeyFile is the path to the TLS private key file (PEM format).
	KeyFile string `yaml:"keyFile"`
}

// ToTLSConfig converts the YAML config struct to the tls package's Config.
func (c TLSConfig) ToTLSConfig() tlsconfig.Config {
	return tlsconfig.Config{
		Enabled:  c.Enabled,
		CertFile: c.CertFile,
		KeyFile:  c.KeyFile,
	}
}

// RequestLoggingConfig controls structured HTTP request logging.
type RequestLoggingConfig struct {
	// Enabled controls whether request logging middleware is active.
	Enabled bool `yaml:"enabled"`
}

// RateLimitConfig controls per-action and global rate limiting.
type RateLimitConfig struct {
	// Enabled controls whether rate limiting middleware is active.
	Enabled bool `yaml:"enabled"`
	// RequestsPerSecond is the maximum number of requests per second.
	RequestsPerSecond float64 `yaml:"requestsPerSecond"`
	// Burst is the maximum number of requests allowed in a burst.
	Burst int `yaml:"burst"`
	// PerAction controls whether rate limiting is per SSM action (true) or global (false).
	PerAction bool `yaml:"perAction"`
}

// AuthConfig controls credential-based request authentication.
// When enabled, incoming SSM API requests must include a valid
// SigV4 signature from a credential in a credential in the store.
type AuthConfig struct {
	// Enabled controls whether credential authentication is active.
	// Defaults to true when omitted (enabled by default).
	Enabled *bool `yaml:"enabled"`
	// InitialCredentials is an optional list of pre-existing credentials
	// to seed into the credential store at startup. This is useful when you
	// already have AWS-style credentials (e.g. from an external identity
	// provider) and want to use them with OpenSSM from the first boot,
	// without having to create credentials via the UI first.
	// If a credential with the same accessKeyId already exists in the
	// store, startup will fail with an error.
	InitialCredentials []InitialCredential `yaml:"initialCredentials"`
}

// InitialCredential defines a credential to import at startup.
// Unlike credentials created via the UI (which auto-generate the access
// key ID and secret), these fields are provided explicitly.
type InitialCredential struct {
	// Label is a human-readable name for the credential.
	// Defaults to "imported" if empty.
	Label string `yaml:"label"`
	// AccessKeyID is the AWS-style access key ID (e.g. "AKIA...").
	AccessKeyID string `yaml:"accessKeyId"`
	// SecretAccessKey is the AWS-style secret access key.
	SecretAccessKey string `yaml:"secretAccessKey"`
	// Region is the AWS region for this credential.
	// If empty, defaults to ssm.region from the config.
	Region string `yaml:"region"`
	// AccountID is the AWS account ID for this credential.
	// If empty, defaults to ssm.accountId from the config.
	AccountID string `yaml:"accountId"`
}

// IsEnabled returns true unless explicitly set to false.
func (c AuthConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// Validate implements config.ConfigI[ServerConfig].
func (c ServerConfig) Validate() error {
	if c.SSM.AccountID == "" {
		return fmt.Errorf("ssm.accountId is required")
	}
	if c.SSM.Region == "" {
		return fmt.Errorf("ssm.region is required")
	}
	if c.SSM.NodeAddress == "" {
		return fmt.Errorf("ssm.nodeAddress is required")
	}
	switch c.SSM.StorageType {
	case "", "memory", "sqlite", "badger", "postgres":
	default:
		return fmt.Errorf("ssm.storageType must be one of: memory, sqlite, badger, postgres (got %q)", c.SSM.StorageType)
	}
	if c.SSM.StorageType == "sqlite" && c.SSM.SQLitePath == "" {
		return fmt.Errorf("ssm.sqlitePath is required when storageType is sqlite")
	}
	if c.SSM.StorageType == "badger" && c.SSM.BadgerPath == "" {
		return fmt.Errorf("ssm.badgerPath is required when storageType is badger")
	}
	if c.SSM.StorageType == "postgres" && c.SSM.PostgresDSN == "" {
		return fmt.Errorf("ssm.postgresDsn is required when storageType is postgres")
	}
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 0 and 65535")
	}
	if c.SSM.ServerSecret == "" {
		return fmt.Errorf("ssm.serverSecret is required")
	}
	switch c.Parameters.DefaultTier {
	case "", "Standard", "Advanced", "Intelligent-Tiering":
	default:
		return fmt.Errorf("parameters.defaultTier must be one of: Standard, Advanced, Intelligent-Tiering (got %q)", c.Parameters.DefaultTier)
	}
	for i, p := range c.Parameters.Startup {
		if p.Name == "" || p.Value == "" {
			return fmt.Errorf("parameters.startup[%d] requires name and value", i)
		}
	}
	for i, k := range c.KMS.Keys {
		if k.Alias == "" {
			return fmt.Errorf("kms.keys[%d].alias is required", i)
		}
	}
	for i, ic := range c.Auth.InitialCredentials {
		if ic.AccessKeyID == "" {
			return fmt.Errorf("auth.initialCredentials[%d].accessKeyId is required", i)
		}
		if ic.SecretAccessKey == "" {
			return fmt.Errorf("auth.initialCredentials[%d].secretAccessKey is required", i)
		}
	}
	return nil
}

// WithValidation implements config.ConfigI[ServerConfig].
func (c ServerConfig) WithValidation() ServerConfig {
	return c
}
