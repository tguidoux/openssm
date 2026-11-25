# AGENTS.md

Guide for AI agents and human contributors working on the OpenSSM codebase.
Build-system details also live in `.github/copilot-instructions.md`.

## Golden Rules

1. **Search before writing.** Read the existing implementation of a similar action/service and copy its pattern.
2. **Never create summary documents** of conversations, completed work, or code changes.
3. **Use Bazel** for builds, tests and dependencies (`bazel test //...`). No `go build`/`go test` in docs or CI.
4. **Return `*types.SSMError`** from domain code using factories in `pkgs/v1/ssm/types/errors.go`; error codes
   must match the AWS SSM API reference.
5. **One transaction, no nesting.** Never call `store.View`/`store.Update` inside another transaction — use the
   `...Tx` helpers (`kms.EncryptTx`, `tags.SetTx`, `instances.GetTx`, …). The memory backend deadlocks otherwise.
6. **Keep docs and agent instructions in sync** in the same change (see "Documentation Maintenance").

---

## Project Layout

```
apps/go/
├── server/                      # openssm-server
│   ├── main.go                  # Wiring, servers, maintenance loop, graceful shutdown
│   ├── config.go / config.yaml  # ServerConfig and local defaults
│   ├── storage.go               # kv + credential store per storageType
│   ├── startup_*.go             # Seed credentials, KMS keys, parameters
│   ├── handlers/                # SSM action registry (actions_*.go per domain)
│   ├── protocol/                # AWS JSON 1.1 decode + error responses
│   ├── agentapi/                # /agent/v1/* for openssm-agent
│   ├── sessionws/               # Session Manager relay (plugin <-> agent)
│   ├── middleware/              # Request logging, SigV4 auth, rate limiting
│   ├── ui/                      # Server-rendered Web UI (handlers_*.go, templates/)
│   ├── health/ metrics/ tls/ serverbase/
└── agent/                       # openssm-agent main
pkgs/v1/
├── config/ environment/ logger/ id/   # Shared infrastructure
├── credentials/                 # Access keys for SigV4 (memory/sqlite/badger)
├── kv/                          # Transactional kv store + memory/sqlite/badger + kvtest conformance suite
├── agentproto/                  # Agent API wire types (dependency-free)
├── agent/                       # Agent library: client, executor, PTY sessions
├── ssmmessages/                 # Session Manager framing + reliable Stream
└── ssm/
    ├── types/                   # SSMError, codes, Timestamp, Paginate, ARNs, Caller
    ├── kms/ tags/ parameters/ documents/ instances/ commands/ sessions/ agenthub/
tools/        # openssm_go_* rules, release tool, CI helpers
deploy/helm/  # Helm chart (server + optional agent DaemonSet)
examples/     # Docker, Compose, Kubernetes, AWS CLI scripts
docs/         # architecture, api-reference, configuration, protocol, agent, session-manager
```

---

## Architectural Patterns

### Services and the kv store

Every domain service takes a `kv.Store` and works in transactions:

```go
func (s *Service) Put(ctx context.Context, in *PutParameterInput) (*PutParameterOutput, error) {
    // validate input first, outside the transaction
    err := s.store.Update(func(tx kv.Tx) error {
        rec, err := s.load(tx, name)            // read
        ...
        v.Value, err = s.kms.EncryptTx(tx, keyID, plaintext, aad)  // join the transaction
        ...
        return kv.PutJSON(tx, bucket, name, rec) // write
    })
}
```

Records are JSON in a bucket. Composite keys use `\x00` separators (`<instance>\x00<command>`) so prefix scans work
on every backend.

### Request/response structs carry AWS JSON names

Inputs/outputs live next to the service (`parameters/model.go`, `commands/model.go`, …) with exact AWS member names
in JSON tags. Timestamps use `*types.Timestamp` (epoch seconds). Handlers are one line per action:

```go
// apps/go/server/handlers/actions_parameters.go
register(h, "PutParameter", s.Put)          // func(ctx, *In) (*Out, error)
registerNoOutput(h, "DeleteParameter", s.Delete) // func(ctx, *In) error -> "{}"
```

### Errors

```go
return nil, types.NewParameterNotFound(name)
return nil, types.NewError(types.ErrInvalidDocumentVersion, "The document version is not valid or does not exist.")
```

`protocol.WriteError` serializes `{"__type": code, "message": msg}`; non-SSM errors become `InternalServerError`.

### Caller identity

The SigV4 middleware stores `types.Caller` in the request context. Services use it for `LastModifiedUser`, session
owners, etc. (`types.CallerFrom(ctx)`).

### Agent integration

Work for agents is written to a per-instance queue bucket inside the same transaction as the resource, then
`agenthub.Notify(instanceID)` wakes the long poll. Agents claim work via `Claim`/`PendingFor`, which delete queue
entries atomically.

### Clock

Use `types.Now()` (never `time.Now()` in domain code) so tests can call `types.SetNowFunc`.

### Configuration

`ServerConfig` in `apps/go/server/config.go`. Add a field with a `yaml` tag, validate it in `Validate()`, add a
default to `config.yaml`, map it in the Helm configmap, and document it in `docs/configuration.md`.
Any scalar can be overridden via `SECTION__KEY` environment variables.

---

## Code Quality Standards

- Small files, single responsibility (`actions_<domain>.go`, `handlers_<domain>.go`).
- Early returns, guard clauses, explicit error handling with context (`fmt.Errorf("doing X: %w", err)`).
- Import prefix `github.com/tguidoux/openssm/`; use `openssm_go_*` Bazel rules.
- Log with `logger.LoggerInterface` and structured fields; never log secrets, SecureString values, tokens.
- Store secrets (agent tokens, activation codes, session tokens) only as SHA-256 hashes; compare in constant time.

---

## Testing

- Tests live in `tests/` subfolders; use `testify`.
- Domain services: run against all three backends where storage matters (see `parameters/tests` `forEachBackend`).
- New kv backends must pass `kvtest.Run`.
- End-to-end: `pkgs/v1/agent/tests` (server + real agent + shell) and `apps/go/server/sessionws/tests`
  (fake plugin client + real agent + PTY).
- Inject time with `types.SetNowFunc` and restore it with `t.Cleanup`.

```bash
bazel test //...
bazel test //pkgs/v1/ssm/parameters/tests:go_default_test
bazel run //:gazelle   # after adding files/packages
```

---

## Common Tasks

### Add an SSM action
1. Add input/output structs (AWS member names) to the domain package `model.go`.
2. Implement the service method `func (s *Service) X(ctx, *In) (*Out, error)` with validation and transactions.
3. Register it in `apps/go/server/handlers/actions_<domain>.go`.
4. Add domain tests (all backends if it touches storage) and a handler test if the wire shape is special.
5. Update `docs/api-reference.md`, `README.md` action table, and `CHANGELOG.md`.

### Add a taggable resource type
Call `tagSvc.Register(tags.ResourceX, existsFunc)` in the service constructor and delete tags with `tags.DeleteTx`
when the resource is deleted.

### Add a Run Command plugin
Implement it in `pkgs/v1/agent/executor.go` (`interpreter` / `runStep`), add the platform mapping in
`documents/content.go` `actionPlatforms`, test it in `pkgs/v1/agent/tests`, and document it in
`docs/api-reference.md` and `docs/agent.md`.

### Add a built-in document
Append to `builtins` in `pkgs/v1/ssm/documents/builtin.go` (re-seeded at every start).

### Add a storage backend
Implement `kv.Store`, pass `kvtest.Run`, wire it in `apps/go/server/storage.go` (plus a credential store), add the
`storageType` value to `Validate()`, and document it.

### Add middleware
Create `apps/go/server/middleware/<name>.go` returning `Middleware`, wire it in `main.go`. Remember `/agent/` and
`/v1/data-channel/` must stay reachable without SigV4.

### Seed initial credentials at startup
Add entries under `auth.initialCredentials`; existing access keys are skipped (`startup_credentials.go`).

---

## Documentation Maintenance

| File | Update when |
|------|-------------|
| `AGENTS.md` | Layout, patterns, common tasks change |
| `.github/copilot-instructions.md` | Build system, structure, conventions change |
| `docs/api-reference.md` | Actions, behaviours, limits, errors change |
| `docs/configuration.md` | Config fields change (also `config.yaml`, Helm configmap/values) |
| `docs/architecture.md` | Components, storage buckets, data flows change |
| `docs/protocol.md` | Wire protocols change (AWS JSON, agent API, data channel) |
| `docs/agent.md` / `docs/session-manager.md` | Agent flags/features, session setup change |
| `README.md` | Features, quickstart, action table change |
| `CHANGELOG.md` | Any user-facing change |
