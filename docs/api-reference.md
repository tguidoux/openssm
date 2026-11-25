# API reference

OpenSSM implements the AWS SSM API (`AmazonSSM`, AWS JSON 1.1). Request and response members, error codes and
HTTP statuses follow the [AWS SSM API reference](https://docs.aws.amazon.com/systems-manager/latest/APIReference/);
this page documents what is supported and where OpenSSM differs.

All timestamps are epoch seconds (with milliseconds). Paginated operations return an opaque `NextToken`.

## Parameter Store

| Action | Notes |
|--------|-------|
| `PutParameter` | `String`, `StringList`, `SecureString`; `Overwrite`, `Description`, `AllowedPattern`, `KeyId`, `Tier`, `Policies`, `DataType` (`text`, `aws:ec2:image`, `aws:ssm:integration`), `Tags` (create only) |
| `GetParameter` | Name, ARN, `name:version`, `name:label`; `WithDecryption` |
| `GetParameters` | Up to 10 names; unknown names/selectors are returned in `InvalidParameters` |
| `GetParametersByPath` | `Recursive`, `ParameterFilters` (`Type`, `KeyId`, `Label`, `tag:*`), `WithDecryption`, max 10 per page |
| `DescribeParameters` | `ParameterFilters` (`Name` Equals/BeginsWith/Contains, `Path` OneLevel/Recursive, `Type`, `KeyId`, `Tier`, `DataType`, `Label`, `tag:<key>`) or legacy `Filters`; max 50 per page |
| `GetParameterHistory` | All retained versions with labels and policies; max 50 per page |
| `DeleteParameter` / `DeleteParameters` | Deletes all versions and tags |
| `LabelParameterVersion` | Up to 10 labels per version; a label moves from any other version; invalid labels returned in `InvalidLabels` |
| `UnlabelParameterVersion` | Returns `RemovedLabels` and `InvalidLabels` |

**Rules enforced:** name syntax and reserved `aws`/`ssm` prefixes, hierarchy depth (15), value size (4 KB Standard,
8 KB Advanced), no Advanced → Standard downgrade, Intelligent-Tiering, 100 versions (oldest unlabeled version dropped;
`ParameterMaxVersionLimitExceeded` if it is labeled), `AllowedPattern` full-match, parameter count limits.

**Policies:** `Expiration` is enforced (expired parameters disappear and are swept). `ExpirationNotification` and
`NoChangeNotification` are validated and stored but no events are emitted (there is no EventBridge).

**SecureString:** encrypted with the local KMS. `KeyId` accepts a key ID, key ARN, alias name or alias ARN.
`alias/aws/ssm` is the default; other keys must exist (`kms.keys`, Web UI) unless `kms.autoCreateKeys` is on.

## Tags

`AddTagsToResource`, `RemoveTagsFromResource`, `ListTagsForResource` for resource types `Parameter`, `Document` and
`ManagedInstance` (max 50 tags; `aws:` prefix reserved). Other resource types return `InvalidResourceType`.

## Documents

| Action | Notes |
|--------|-------|
| `CreateDocument` | `Command` (schema 1.2, 2.0, 2.2), `Session` (1.0), `Automation` (stored, not executed); `JSON`/`YAML`; `VersionName`, `DisplayName`, `TargetType`, `Tags` |
| `UpdateDocument` | New version from `$LATEST`; duplicate content and version names rejected |
| `GetDocument` | By default/`$LATEST`/number/`VersionName`; `DocumentFormat` converts between JSON and YAML |
| `DescribeDocument` | Parameters, platform types, hash (`Sha256`), default/latest version, tags |
| `ListDocuments` | `Filters` (`Owner` Self/Amazon/All, `Name`, `SearchKeyword`, `DocumentType`, `PlatformTypes`, `TargetType`, `tag:*`) or legacy `DocumentFilterList` |
| `ListDocumentVersions` | Newest first |
| `UpdateDocumentDefaultVersion` | Version number or `$LATEST` |
| `DeleteDocument` | Whole document or one non-default version |

Built-in, read-only (`Owner: Amazon`) documents: `AWS-RunShellScript`, `AWS-RunPowerShellScript`,
`SSM-SessionManagerRunShell`, `AWS-StartInteractiveCommand`, `AWS-StartNonInteractiveCommand`.

Names starting with `AWS-`, `Amazon` or `Amzn` are reserved. Content is limited to 64 KB.

## Managed instances

| Action | Notes |
|--------|-------|
| `CreateActivation` | `IamRole` (informational, required), `RegistrationLimit` (1–1000), `ExpirationDate` (≤ 30 days, default 24 h), `DefaultInstanceName`, `Tags` |
| `DescribeActivations` | Filters `ActivationIds`, `DefaultInstanceName`, `IamRole` |
| `DeleteActivation` | Registered instances are kept |
| `DescribeInstanceInformation` | `Filters`/`InstanceInformationFilterList`: `InstanceIds`, `PingStatus`, `PlatformTypes`, `AgentVersion`, `ActivationIds`, `IamRole`, `ResourceType`, `tag:*`, `tag-key`; `MaxResults` 5–50 |
| `DeregisterManagedInstance` | Revokes the agent token and deletes the instance's tags |

Instances are registered by `openssm-agent` (not the real `amazon-ssm-agent`). `PingStatus` is `Online` until no
heartbeat or poll has been received for `agents.pingTimeoutSeconds`, then `ConnectionLost`.

## Run Command

| Action | Notes |
|--------|-------|
| `SendCommand` | `InstanceIds` (≤ 50) or `Targets` (`InstanceIds`, `tag:<key>`, `tag-key`; multiple targets are ANDed), `DocumentName`/`DocumentVersion`/`DocumentHash`, `Parameters`, `TimeoutSeconds` (delivery, 30–2592000), `Comment`, `MaxConcurrency`/`MaxErrors` (validated) |
| `ListCommands` | By `CommandId`/`InstanceId`, filters `Status`, `DocumentName`, `InvokedAfter`, `InvokedBefore`, `ExecutionStage` |
| `ListCommandInvocations` | `Details=true` includes per-plugin status and output (first 2500 chars) |
| `GetCommandInvocation` | Standard output/error (first 24000 chars), response code, execution times; `PluginName` required for multi-step documents |
| `CancelCommand` | Whole command or specific instances; undelivered invocations are cancelled immediately |

Supported plugins: `aws:runShellScript` (`/bin/sh` by default) and `aws:runPowerShellScript` (`pwsh`/`powershell`),
with `runCommand`, `workingDirectory`, and `timeoutSeconds`. `precondition` `StringEquals [platformType, …]`
skips non-matching steps. Other plugins fail with a clear error.

Statuses follow AWS: `Pending` → `InProgress` → `Success` | `Failed` | `TimedOut` | `Cancelled` (`Cancelling` while a
cancellation is in flight). `DeliveryTimedOut` and `ExecutionTimedOut` appear in `StatusDetails`.

**Differences:** `MaxConcurrency`/`MaxErrors` are not used for rate control (all targets run immediately);
S3/CloudWatch output and SNS notifications are not supported.

## Session Manager

| Action | Notes |
|--------|-------|
| `StartSession` | `Target` must be an Online managed instance. Documents: `SSM-SessionManagerRunShell` (default, `Standard_Stream`), `AWS-StartInteractiveCommand`, `AWS-StartNonInteractiveCommand`, or your own Session documents. Returns `SessionId`, `TokenValue`, `StreamUrl` |
| `ResumeSession` | Rotates `TokenValue` for an active session (used by the plugin to reconnect) |
| `TerminateSession` | Closes both sides of the data channel; idempotent |
| `DescribeSessions` | `State` `Active` or `History`; filters `Target`, `Owner`, `Status`, `SessionId`, `InvokedAfter`, `InvokedBefore` |

Session document `inputs` honoured by the agent: `idleSessionTimeout`, `maxSessionDuration`, `shellProfile.linux`,
`runAsEnabled`/`runAsDefaultUser` (agent running as root). Port-forwarding (`Port`) sessions, KMS session encryption
and session logging to S3/CloudWatch are not supported.

## Errors

Errors use the AWS JSON 1.1 shape and the `X-Amzn-ErrorType` header:

```json
{"__type": "ParameterNotFound", "message": "Parameter /missing not found."}
```

| Situation | `__type` | HTTP |
|-----------|----------|------|
| Unknown action | `InvalidAction` | 400 |
| Missing/invalid `X-Amz-Target` | `UnknownOperationException` | 400 |
| Malformed JSON | `SerializationException` | 400 |
| Missing signature | `MissingAuthenticationTokenException` | 400 |
| Unknown access key | `InvalidClientTokenId` | 400 |
| Bad signature / wrong service or region | `SignatureDoesNotMatch` / `InvalidSignatureException` | 400 |
| Rate limited | (empty body) | 429 |
| Unexpected failure | `InternalServerError` | 500 |

Domain errors (`ParameterNotFound`, `ParameterAlreadyExists`, `InvalidDocument`, `InvalidInstanceId`,
`InvocationDoesNotExist`, `DoesNotExistException`, …) match the AWS names.
