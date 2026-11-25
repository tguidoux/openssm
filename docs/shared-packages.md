# Shared Packages

This document describes the shared packages in `pkgs/v1/` that provide cross-cutting functionality used by the OpenSSM server and other applications.

## Package Overview

```
pkgs/v1/
├── config/        # Configuration loading from YAML, env overrides, validation
├── environment/   # Environment enum (PROD, STAGING, LOCAL)
├── logger/        # Structured JSON logging
├── id/            # UUID / hex ID generation
├── credentials/   # Access key store (memory, SQLite, Badger) used by SigV4 auth
├── kv/            # Transactional key-value store + memory/sqlite/badger backends
├── agentproto/    # JSON wire types shared by the server and openssm-agent
├── agent/         # openssm-agent library (client, executor, PTY sessions)
├── ssmmessages/   # Session Manager binary framing and reliable stream
└── ssm/           # SSM domain services (see docs/architecture.md)
    ├── types/       # SSMError, error codes, Timestamp, pagination, ARNs, caller
    ├── kms/         # Local KMS (SecureString encryption)
    ├── tags/        # Resource tags
    ├── parameters/  # Parameter Store
    ├── documents/   # SSM documents
    ├── instances/   # Activations and managed instances
    ├── commands/    # Run Command
    ├── sessions/    # Session Manager lifecycle
    └── agenthub/    # Wake-ups for long-polling agents
```

---

## config

**Import:** `github.com/tguidoux/openssm/pkgs/v1/config`

**Files:** `config.go`, `default.go`

### Overview

Generic configuration loading from YAML files with environment-based path resolution and validation support.

### ConfigI Interface

```go
type ConfigI[ConfigType any] interface {
    Validate() error
    WithValidation() ConfigType
}
```

Any config struct that implements `Validate()` and `WithValidation()` can be loaded with the generic config loader.

### Config Struct

```go
type Config[ConfigType any] struct {
    ConfigPath string
    Config     ConfigType
    data       []byte  // unexported raw YAML data
}
```

### Functions

#### `NewConfig[ConfigType any](configPath string) Config[ConfigType]`

Reads and parses a YAML config file, then applies environment overrides
(see `ApplyEnvOverrides`). Panics on read or parse errors.

```go
cfg := config.NewConfig[MyConfig]("/path/to/config.yaml")
```

#### `NewConfigFromEnv[ConfigType ConfigI[ConfigType]](envVar ...string) *Config[ConfigType]`

Loads config from a path specified by environment variable. Falls back to `DefaultConfigPath` (`config.yaml`).

```go
// Uses CONFIG_PATH env var, falls back to "config.yaml"
cfg := config.NewConfigFromEnv[MyConfig]()

// Use custom env var
cfg := config.NewConfigFromEnv[MyConfig]("MY_CONFIG_PATH")
```

#### `ApplyEnvOverrides(cfg any, environ []string) error`

Overlays `SECTION__KEY[__SUBKEY]=value` environment variables onto a config struct, matching yaml tags
case-insensitively. Only variables whose first segment matches a top-level key are considered; scalar fields
(string, bool, ints, floats, and pointers to them) can be overridden. Example: `SSM__SERVERSECRET=...`
overrides `ssm.serverSecret`.

### Constants

| Constant | Value |
|----------|-------|
| `DefaultConfigPath` | `config.yaml` |
| `DefaultConfigEnvVar` | `CONFIG_PATH` |

### Default Endpoints (`default.go`)

AWS endpoint URLs for local environment:

| Constant | Value |
|----------|-------|
| `LocalAWSSSMEndpointURL` | `http://localhost:9330` |

### Endpoint Functions

| Function | LOCAL | STAGING/PROD |
|----------|-------|--------------|
| `GetRegion(env)` | `""` | `us-east-1` |
| `GetSSMRegion(env)` | `""` | `us-east-1` |
| `GetAWSSSMEndpointURL(env)` | Local URL | `""` |

---

## environment

**Import:** `github.com/tguidoux/openssm/pkgs/v1/environment`

**File:** `env.go`

### Overview

Simple environment enum used throughout the codebase to switch behavior between deployment targets.

### Type

```go
type Environment string
```

### Constants

| Constant | Value | Description |
|----------|-------|-------------|
| `PROD` | `"prod"` | Production environment |
| `STAGING` | `"staging"` | Staging environment |
| `LOCAL` | `"local"` | Local development |

### Usage

```go
import "github.com/tguidoux/openssm/pkgs/v1/environment"

if env == environment.LOCAL {
    // Local-only behavior
}
```

---

## logger

**Import:** `github.com/tguidoux/openssm/pkgs/v1/logger`

**Files:** `factory.go`, `contextual_logger.go`, `uncontextual_logger.go`

### Overview

Structured JSON logging built on Go's `log/slog` package. Provides two logger types:

- **Contextual** — Requires `context.Context` for each log call (for request-scoped tracing)
- **Uncontextual** — Uses `context.Background()` internally (simpler API)

Both implement `LoggerInterface`.

### LoggerInterface

```go
type LoggerInterface interface {
    Debug(msg string, extra ...map[string]any) LoggerInterface
    Info(msg string, extra ...map[string]any) LoggerInterface
    Error(msg string, extra ...map[string]any) LoggerInterface
    Warning(msg string, extra ...map[string]any) LoggerInterface
    Fatal(msg string, extra ...map[string]any) LoggerInterface
    Debugf(format string, args ...any) LoggerInterface
    Infof(format string, args ...any) LoggerInterface
    Errorf(format string, args ...any) LoggerInterface
    Warningf(format string, args ...any) LoggerInterface
    Fatalf(format string, args ...any) LoggerInterface
    WithExtra(extra ...map[string]any) LoggerInterface
    GetWriter() *os.File
    Printf(msg string, args ...interface{})
}
```

### LoggerType

```go
type LoggerType int

const (
    UncontextualLoggerType LoggerType = 0
    ContextLoggerType      LoggerType = 1
)
```

### Factory Functions

#### `New(name string, loggerType LoggerType, level ...slog.Level) LoggerInterface`

Creates a logger by type. Both types currently return `UncontextualLogger`.

```go
log := logger.New("my-service", logger.UncontextualLoggerType, slog.LevelInfo)
```

#### `NewUncontextual(name string, level ...slog.Level) LoggerInterface`

Creates an uncontextual logger.

```go
log := logger.NewUncontextual("my-service")
```

#### `NewContextual(name string, level ...slog.Level) *Logger`

Creates a contextual logger (returns concrete `*Logger` type).

```go
log := logger.NewContextual("my-service", slog.LevelDebug)
```

### Log Level Resolution

Log level is determined by:
1. Variadic `level` argument (if provided)
2. `DEBUG` environment variable (if set)
3. Default: `slog.LevelInfo`

### Output Format

JSON on `stdout`. Example:

```json
{
  "asctime": "2025-01-15T10:30:00.000Z",
  "level": "INFO",
  "msg": "Server started",
  "logger.name": "openssm-server",
  "extra": {"port": "9330"}
}
```

Note: The `time` field is renamed to `asctime` in the output.

### Contextual Logger (`*Logger`)

All methods take `context.Context` as first argument:

```go
log := logger.NewContextual("my-service")
log.Info(ctx, "Request received", map[string]any{"method": "POST", "path": "/"})
log.Infof(ctx, "Parameter %s created", name)
log.Error(ctx, "Failed to process", map[string]any{"error": err.Error()})
log.Fatal(ctx, "Critical failure")  // Calls os.Exit(1)
```

#### `WithExtra(extra ...map[string]any) *Logger`

Adds extra fields to the logger. Returns the same instance (fields accumulate):

```go
log := log.WithExtra(map[string]any{"requestId": "abc-123"})
log.Info(ctx, "Processing")  // Includes requestId in output
```

#### `Printf(msg string, args ...interface{})`

Implements the `rs/cors` Logger interface for compatibility with HTTP middleware.

### Uncontextual Logger (`*UncontextualLogger`)

Same API as contextual logger but without `context.Context`:

```go
log := logger.NewUncontextual("my-service")
log.Info("Server started", map[string]any{"port": "9330"})
log.Infof("Parameter %s created", name)
log.Error("Failed", map[string]any{"error": err.Error()})
```

#### `GetLogger() *Logger`

Returns the underlying contextual `*Logger` for cases where context is needed:

```go
unctxLog := logger.NewUncontextual("my-service")
ctxLog := unctxLog.GetLogger()
ctxLog.Info(ctx, "With context")
```

### Method Chaining

All log methods return `LoggerInterface`, enabling chaining:

```go
log.Info("Step 1").
    WithExtra(map[string]any{"step": 1}).
    Info("Step 2")
```

---

## kv

**Import:** `github.com/tguidoux/openssm/pkgs/v1/kv`

A minimal transactional key-value abstraction used by every SSM domain service. Keys are grouped into buckets;
values are bytes (domain packages store JSON via `kv.GetJSON`, `kv.PutJSON`, `kv.ListJSON`).

```go
type Store interface {
    View(fn func(r Reader) error) error   // read-only transaction
    Update(fn func(tx Tx) error) error    // atomic read-write transaction (rolled back on error)
    Close() error
}
```

| Backend | Package | Notes |
|---------|---------|-------|
| Memory | `kv/memory` | Single RWMutex; buffered writes give rollback semantics |
| SQLite | `kv/sqlite` | One `kv(bucket, key, value)` table, WAL mode, byte-range prefix scans |
| Badger | `kv/badger` | `kv:<bucket>\x00<key>` keys, retries on `ErrConflict` |

`kv/kvtest.Run` is a conformance suite every backend must pass. **Never call `View`/`Update` from inside
another transaction** (the memory backend would deadlock) — domain packages expose `...Tx` helpers
(`kms.EncryptTx`, `tags.SetTx`, `instances.GetTx`, …) that join the caller's transaction.

---

## ssmmessages

**Import:** `github.com/tguidoux/openssm/pkgs/v1/ssmmessages`

Implements the Session Manager data channel framing (`ClientMessage`, 120-byte header + payload, SHA-256 digest),
acknowledge / channel_closed / handshake payloads, and `Stream` — a reliable ordered stream with acknowledgements,
retransmission and de-duplication. It is wire compatible with `aws/session-manager-plugin`; see
[protocol.md](protocol.md#session-manager-data-channel).

---

## agent

**Import:** `github.com/tguidoux/openssm/pkgs/v1/agent`

The `openssm-agent` implementation: `Client` (agent API), `Agent` (registration, heartbeats, long poll),
`Executor` (`aws:runShellScript`, `aws:runPowerShellScript`) and `ShellSessionHandler` (PTY sessions). See
[agent.md](agent.md).

