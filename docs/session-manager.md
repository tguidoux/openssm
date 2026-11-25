# Session Manager

OpenSSM supports `aws ssm start-session` with the **official, unmodified** `session-manager-plugin`.

```bash
export AWS_ENDPOINT_URL=http://localhost:9330 AWS_DEFAULT_REGION=us-east-1
aws ssm start-session --target mi-0123456789abcdef0
aws ssm start-session --target mi-0123456789abcdef0 \
  --document-name AWS-StartInteractiveCommand --parameters command="top"
aws ssm describe-sessions --state Active
aws ssm terminate-session --session-id <id>
```

Requirements: an Online managed instance running `openssm-agent` with sessions enabled, and the plugin
installed on the client ([AWS docs](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)).

## The StreamUrl host rule

Since version 1.2.889.0, `session-manager-plugin` refuses to connect unless the `StreamUrl` host:

- is `ssmmessages.<region>.<suffix>` (or contains `ssmmessages`, `mgs`, … in its first label) where `<suffix>` is an
  AWS DNS suffix **or the DNS suffix of your `--endpoint-url` / `AWS_ENDPOINT_URL` host** after stripping a leading
  `ssm.` and `<region>.`, or
- is listed in `AWS_SSM_PLUGIN_ADDITIONAL_STREAM_HOSTS` (comma-separated, exact match).

Configure `sessions.streamUrlBase` accordingly:

| Setup | API endpoint | `sessions.streamUrlBase` |
|-------|--------------|--------------------------|
| Local | `http://localhost:9330` | `ws://ssmmessages.us-east-1.localhost:9330` (`*.localhost` → 127.0.0.1) |
| Production | `https://ssm.us-east-1.openssm.example.com` | `wss://ssmmessages.us-east-1.openssm.example.com` |
| Anything else | any | leave empty and export `AWS_SSM_PLUGIN_ADDITIONAL_STREAM_HOSTS=<host>` on clients |

In production, point both DNS names at OpenSSM (same port) and route `/v1/data-channel/` on the `ssmmessages`
host to the API port. With the Helm chart, add the host to `ingress.hosts` (see `values.prod.yaml`).

## Ingress and proxies

- WebSockets must be allowed on `/v1/data-channel/*` and `/agent/v1/sessions/*`.
- Increase idle timeouts (e.g. nginx `proxy-read-timeout: 3600`) — sessions can be quiet for a long time, and agent
  long polls last up to 30 s.
- Terminate TLS at the ingress or enable `server.tls` and use `wss://`.

## Session lifecycle

| Status | Meaning |
|--------|---------|
| `Connecting` | Created by `StartSession`, waiting for the agent and client (failed after 5 minutes) |
| `Connected` | Both sides attached |
| `Disconnected` | Client dropped; the plugin has 60 s to reconnect with `ResumeSession` |
| `Terminated` | Shell exited, `TerminateSession`, `~.` in the plugin, or idle/max duration reached |
| `Failed` | Never connected |

Sessions are listed under `Active` until terminated, then under `History`.

## Session documents

`SSM-SessionManagerRunShell` holds the defaults (`idleSessionTimeout` 20 minutes). Create your own Session
document to change them:

```yaml
schemaVersion: "1.0"
description: Bash with a 60-minute idle timeout
sessionType: Standard_Stream
inputs:
  idleSessionTimeout: "60"
  maxSessionDuration: "480"
  shellProfile:
    linux: /bin/bash
```

```bash
aws ssm create-document --name My-Shell --document-type Session --document-format YAML --content file://shell.yaml
aws ssm start-session --target mi-... --document-name My-Shell
```

## Not supported

Port forwarding (`AWS-StartPortForwardingSession*`), KMS-encrypted sessions, session logging to S3/CloudWatch, and
interactive sessions on Windows agents.
