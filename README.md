# OpenSSM

[![CI](https://github.com/tguidoux/openssm/actions/workflows/ci.yml/badge.svg)](https://github.com/tguidoux/openssm/actions/workflows/ci.yml)
[![Code Quality](https://github.com/tguidoux/openssm/actions/workflows/code-quality.yml/badge.svg)](https://github.com/tguidoux/openssm/actions/workflows/code-quality.yml)
[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**An open-source, self-hosted alternative to AWS Systems Manager (SSM).** OpenSSM speaks the real SSM API, so
the AWS CLI, the AWS SDKs, and the official `session-manager-plugin` work against it unchanged.

| Capability | What you get |
|------------|--------------|
| **Parameter Store** | `String`, `StringList` and `SecureString` parameters, hierarchies, versions, labels, tiers, policies, tags |
| **Local KMS** | AES-256-GCM envelope encryption for SecureString values, `alias/aws/ssm`, customer keys and aliases |
| **Documents** | Command and Session documents (JSON/YAML), versions, default version, built-in `AWS-RunShellScript` & friends |
| **Managed instances** | Hybrid activations and a lightweight `openssm-agent` (Linux/macOS) that registers hosts as `mi-…` instances |
| **Run Command** | `SendCommand` by instance ID or tag, invocations, output, timeouts, cancellation |
| **Session Manager** | `aws ssm start-session` interactive shells through the official plugin, terminate/resume, history |
| **Operations** | Web UI, Prometheus metrics, SigV4 auth, rate limiting, memory/SQLite/Badger storage, Helm chart |

## Why OpenSSM?

- **Local development & CI** — test code that reads Parameter Store or runs SSM commands without an AWS account.
- **On-prem & air-gapped** — a configuration store, remote execution and shell access for machines outside AWS.
- **Drop-in** — same API, same error codes, same CLI. Point `AWS_ENDPOINT_URL` at OpenSSM and go.

## Quickstart

### Run the server

```bash
git clone https://github.com/tguidoux/openssm.git && cd openssm
bazel run //apps/go/server:openssm-server
```

| Port | Purpose |
|------|---------|
| `9330` | SSM API (AWS JSON 1.1), agent API, Session Manager data channels |
| `9331` | Web UI |
| `9332` | Prometheus metrics |
| `8001` | Health checks (non-local environments) |

The local [config.yaml](apps/go/server/config.yaml) enables SigV4 auth. Create an access key in the Web UI
(<http://localhost:9331/credentials>), or start from [examples/docker/config.with-creds.yaml](examples/docker/config.with-creds.yaml),
which pre-seeds a well-known key pair.

### Parameter Store

```bash
export AWS_ENDPOINT_URL=http://localhost:9330 AWS_DEFAULT_REGION=us-east-1
export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...

aws ssm put-parameter --name /app/prod/db-password --value 's3cr3t' --type SecureString
aws ssm get-parameter --name /app/prod/db-password --with-decryption
aws ssm get-parameters-by-path --path /app --recursive
aws ssm label-parameter-version --name /app/prod/db-password --parameter-version 1 --labels stable
aws ssm get-parameter --name /app/prod/db-password:stable --with-decryption
```

### Run Command and Session Manager

```bash
# 1. Create an activation and register a host
aws ssm create-activation --iam-role demo
bazel run //apps/go/agent:openssm-agent -- \
  -server http://localhost:9330 -activation-id <ActivationId> -activation-code <ActivationCode>

# 2. Run commands
aws ssm describe-instance-information
aws ssm send-command --document-name AWS-RunShellScript --instance-ids mi-... \
  --parameters 'commands=["uname -a"]'
aws ssm get-command-invocation --command-id <id> --instance-id mi-...

# 3. Open a shell (requires session-manager-plugin)
aws ssm start-session --target mi-...
```

`session-manager-plugin` only connects to stream hosts shaped like `ssmmessages.<region>.<domain>`.
The default config returns `ws://ssmmessages.us-east-1.localhost:9330/...` (`*.localhost` resolves to
127.0.0.1). See [docs/session-manager.md](docs/session-manager.md) for production setups.

### Docker, Compose, Kubernetes

```bash
docker compose -f examples/docker-compose/docker-compose.with-creds.yml up
docker compose -f examples/docker-compose/docker-compose.agent.yml up     # server + managed instance
helm install openssm deploy/helm --set openssm.ssm.serverSecret=$(openssl rand -hex 32)
```

See [examples/README.md](examples/README.md) and [deploy/helm/values.yaml](deploy/helm/values.yaml).

## Supported SSM API actions

| Area | Actions |
|------|---------|
| Parameter Store | `PutParameter`, `GetParameter`, `GetParameters`, `GetParametersByPath`, `DescribeParameters`, `GetParameterHistory`, `DeleteParameter`, `DeleteParameters`, `LabelParameterVersion`, `UnlabelParameterVersion` |
| Tags | `AddTagsToResource`, `RemoveTagsFromResource`, `ListTagsForResource` (`Parameter`, `Document`, `ManagedInstance`) |
| Documents | `CreateDocument`, `UpdateDocument`, `GetDocument`, `DescribeDocument`, `ListDocuments`, `ListDocumentVersions`, `UpdateDocumentDefaultVersion`, `DeleteDocument` |
| Managed instances | `CreateActivation`, `DescribeActivations`, `DeleteActivation`, `DescribeInstanceInformation`, `DeregisterManagedInstance` |
| Run Command | `SendCommand`, `ListCommands`, `ListCommandInvocations`, `GetCommandInvocation`, `CancelCommand` |
| Session Manager | `StartSession`, `ResumeSession`, `TerminateSession`, `DescribeSessions` |

Details and limitations: [docs/api-reference.md](docs/api-reference.md).

## Documentation

| Document | Contents |
|----------|----------|
| [docs/architecture.md](docs/architecture.md) | Components, storage, request flow, agent and session data paths |
| [docs/api-reference.md](docs/api-reference.md) | Every supported action, behaviours, limits, and differences from AWS |
| [docs/configuration.md](docs/configuration.md) | All `config.yaml` fields and environment overrides |
| [docs/protocol.md](docs/protocol.md) | AWS JSON 1.1, SigV4, agent API, and the `ssmmessages` data channel |
| [docs/agent.md](docs/agent.md) | Installing and operating `openssm-agent` |
| [docs/session-manager.md](docs/session-manager.md) | Session Manager setup, StreamUrl, TLS and ingress |
| [docs/shared-packages.md](docs/shared-packages.md) | Reusable Go packages in `pkgs/v1` |

## Development

```bash
bazel test //...                 # all tests (nogo static analysis runs automatically)
bazel run //:gazelle             # regenerate BUILD files after adding Go files
bazel run //:go.clean            # after changing go.mod
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md).

## Out of scope (for now)

Automation runbooks, State Manager associations, Inventory, Patch Manager, Maintenance Windows, OpsCenter,
port-forwarding sessions, Windows interactive sessions, and compatibility with the real `amazon-ssm-agent`.

## License

[MIT](LICENSE)
