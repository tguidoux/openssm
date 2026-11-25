# Configuration

`openssm-server` reads a YAML file from `CONFIG_PATH` (default `./config.yaml`, then
`/apps/go/server/config.yaml` in the container). The reference file is
[apps/go/server/config.yaml](../apps/go/server/config.yaml).

## Environment overrides

Any scalar field can be overridden with `SECTION__KEY[__SUBKEY]=value` (double underscore, case-insensitive):

```bash
SSM__SERVERSECRET=$(openssl rand -hex 32)
SSM__STORAGETYPE=badger SSM__BADGERPATH=/data/badger
SERVER__PORT=9443
AUTH__ENABLED=false
SESSIONS__STREAMURLBASE=wss://ssmmessages.us-east-1.openssm.example.com
```

Variables whose first segment is not a top-level config key are ignored. Invalid values (e.g. a non-numeric port)
stop the server at startup. The configuration is validated before anything starts.

## Reference

### `server`

| Field | Default | Description |
|-------|---------|-------------|
| `host` | `0.0.0.0` | Bind address for the SSM API |
| `port` | `9330` | SSM API, agent API and Session Manager data channels |
| `tls.enabled` / `certFile` / `keyFile` | `false` | Serve HTTPS/WSS directly |

### `ssm`

| Field | Default | Description |
|-------|---------|-------------|
| `nodeAddress` | — (required) | External `host:port` or URL used to build endpoint URLs |
| `accountId` | — (required) | Account ID used in ARNs |
| `region` | — (required) | Region used in ARNs and checked in SigV4 scopes |
| `storageType` | `memory` | `memory`, `sqlite`, `badger`, or `postgres` |
| `sqlitePath` | — | Required when `storageType: sqlite` |
| `badgerPath` | — | Required when `storageType: badger` |
| `postgresDsn` | — | Required when `storageType: postgres`. PostgreSQL connection string (pgx DSN) |
| `serverSecret` | — (required) | Default KMS master key. Use `SSM__SERVERSECRET` in production |

### `kms`

| Field | Default | Description |
|-------|---------|-------------|
| `masterKey` | `ssm.serverSecret` | Wraps all key material. **Changing it makes SecureString values unreadable** |
| `autoCreateKeys` | `false` | Create unknown key IDs / aliases on first use instead of returning `InvalidKeyId` |
| `keys[]` | `[]` | Customer keys created at startup: `alias` (required), `description` |

### `parameters`

| Field | Default | Description |
|-------|---------|-------------|
| `defaultTier` | `Standard` | Tier when `PutParameter` omits `Tier` (`Standard`, `Advanced`, `Intelligent-Tiering`) |
| `maxStandardParameters` | `10000` | Limit on Standard parameters (`0` = unlimited) |
| `maxAdvancedParameters` | `100000` | Limit on Advanced parameters (`0` = unlimited) |
| `startup[]` | `[]` | Parameters created at boot if missing: `name`, `value`, `type`, `description`, `keyId`, `tier`, `tags` |

### `agents`

| Field | Default | Description |
|-------|---------|-------------|
| `pingTimeoutSeconds` | `120` | Seconds without heartbeat/poll before an instance is `ConnectionLost` |
| `latestAgentVersion` | — | Drives `IsLatestVersion` in `DescribeInstanceInformation` |

### `sessions`

| Field | Default | Description |
|-------|---------|-------------|
| `streamUrlBase` | API endpoint (`ws(s)://nodeAddress`) | Scheme+host of the `StreamUrl` returned by `StartSession`. The official plugin only accepts `ssmmessages.<region>.<domain>` hosts — see [session-manager.md](session-manager.md) |

### `auth`

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `true` | Require SigV4-signed SSM API requests |
| `initialCredentials[]` | `[]` | Credentials imported at startup: `accessKeyId`, `secretAccessKey`, `label`, `region`, `accountId`. Existing keys are skipped |

### `rateLimit`

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable token-bucket rate limiting (HTTP 429 when exceeded) |
| `requestsPerSecond` | — | Refill rate |
| `burst` | — | Bucket size |
| `perAction` | `false` | One bucket per SSM action (`X-Amz-Target`) instead of a global bucket |

### Other sections

| Section | Fields | Notes |
|---------|--------|-------|
| `log` | `level` | `debug`, `info`, `warn`, `error` |
| `health` | `port` (`8001`), `tls` | Started when `environment` is not `local`; serves `/health` |
| `ui` | `enabled`, `port` (`9331`), `tls` | Web dashboard |
| `metrics` | `enabled`, `port` (`9332`), `tls` | Prometheus `/metrics` |
| `requestLogging` | `enabled` | Structured access log with request IDs |
| `environment` | `local` | `local`, `staging`, `prod` |

## Agent configuration

`openssm-agent` is configured with flags or environment variables — see [agent.md](agent.md).
