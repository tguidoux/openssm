# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.1] - 2026-11-25

### Fixed
- Server container image crashed on start because it looked for `/apps/go/server/config.yaml`; it now
  falls back to the config bundled next to the executable.

## [0.1.0] - 2026-11-25

### Added
- **Parameter Store**: `PutParameter`, `GetParameter`, `GetParameters`, `GetParametersByPath`,
  `DescribeParameters`, `GetParameterHistory`, `DeleteParameter`, `DeleteParameters`,
  `LabelParameterVersion`, `UnlabelParameterVersion`; `String`/`StringList`/`SecureString`, hierarchies,
  versions, labels, tiers (incl. Intelligent-Tiering), `AllowedPattern`, data types, policies, tags.
- **Local KMS** for SecureString values (AES-256-GCM envelope encryption, `alias/aws/ssm`, customer keys).
- **Documents**: `CreateDocument`, `UpdateDocument`, `GetDocument`, `DescribeDocument`, `ListDocuments`,
  `ListDocumentVersions`, `UpdateDocumentDefaultVersion`, `DeleteDocument`; built-in `AWS-*` documents.
- **Managed instances**: hybrid activations, `DescribeInstanceInformation`, `DeregisterManagedInstance`.
- **Run Command**: `SendCommand` (instance IDs or tags), `ListCommands`, `ListCommandInvocations`,
  `GetCommandInvocation`, `CancelCommand`, delivery/execution timeouts.
- **Session Manager**: `StartSession`, `ResumeSession`, `TerminateSession`, `DescribeSessions`, compatible with
  the official `session-manager-plugin`.
- **openssm-agent**: registration, heartbeats, shell/PowerShell commands, PTY sessions.
- Tags for `Parameter`, `Document` and `ManagedInstance`.
- Memory, SQLite and BadgerDB storage behind a shared transactional kv layer.
- SigV4 authentication (`ssm` service), per-action or global rate limiting, Prometheus metrics, Web UI.
- `SECTION__KEY` environment overrides in the config loader.
- Helm chart with optional agent DaemonSet; Docker, Compose and Kubernetes examples.

[Unreleased]: https://github.com/tguidoux/openssm/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/tguidoux/openssm/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/tguidoux/openssm/releases/tag/v0.1.0
