# openssm-agent

`openssm-agent` turns a host into an OpenSSM managed instance (`mi-…`). It registers with a hybrid activation,
keeps the instance `Online`, runs Run Command invocations, and serves Session Manager shells.

> OpenSSM does not support the real `amazon-ssm-agent`; it uses this lightweight agent and a simple HTTPS/JSON
> protocol ([protocol.md](protocol.md#agent-api)).

## Build and run

```bash
bazel build //apps/go/agent:openssm-agent
# binary: bazel-bin/apps/go/agent/openssm-agent_/openssm-agent

aws ssm create-activation --iam-role openssm --registration-limit 10   # returns ActivationId + ActivationCode
openssm-agent -server https://openssm.example.com \
  -activation-id <ActivationId> -activation-code <ActivationCode>
```

A container image is built with `bazel run //apps/go/agent:openssm_agent_image_platform_transition_load_docker`.

On first start the agent redeems the activation and stores its identity in the state file. Later starts reuse it,
so the instance ID survives restarts and the activation code is no longer needed. If the server rejects the
stored token (instance deregistered), the agent registers again when activation details are provided.

## Flags and environment variables

| Flag | Environment | Default | Description |
|------|-------------|---------|-------------|
| `-server` | `OPENSSM_SERVER` | `http://localhost:9330` | OpenSSM API endpoint |
| `-activation-id` | `OPENSSM_ACTIVATION_ID` | — | Hybrid activation ID (first start) |
| `-activation-code` | `OPENSSM_ACTIVATION_CODE` | — | Hybrid activation code (first start) |
| `-state-file` | `OPENSSM_STATE_FILE` | `~/.openssm/agent.json` | Persisted instance ID and token (mode 0600) |
| `-shell` | `OPENSSM_SHELL` | `/bin/sh` (commands), `$SHELL` (sessions) | Interpreter |
| `-work-dir` | `OPENSSM_WORK_DIR` | process cwd | Default working directory for commands |
| `-heartbeat` | — | `30s` | Heartbeat interval (polls also count as heartbeats) |
| `-disable-sessions` | `OPENSSM_DISABLE_SESSIONS=true` | `false` | Refuse Session Manager sessions |
| `-debug` | `OPENSSM_DEBUG=true` | `false` | Debug logging |
| `-version` | — | — | Print version |

## What it runs

| Feature | Details |
|---------|---------|
| `aws:runShellScript` | Writes `runCommand` to a temp script, runs it with the shell in its own process group, captures stdout/stderr (48 KB each), honours `workingDirectory` and `timeoutSeconds` |
| `aws:runPowerShellScript` | Same with `pwsh -NoProfile -NonInteractive -File` (`powershell` on Windows) |
| Preconditions | `{"StringEquals": ["platformType", "Linux"]}` skips steps on other platforms |
| Cancellation | `CancelCommand` kills the step's process group and reports `Cancelled` |
| Sessions | `Standard_Stream` login shell in a PTY; `InteractiveCommands` / `NonInteractiveCommands` run the document command; terminal resize; idle and max duration from the session document; `runAsDefaultUser` when running as root |

Windows supports Run Command but not interactive sessions yet.

## Running as a service

systemd example:

```ini
[Unit]
Description=OpenSSM agent
After=network-online.target

[Service]
Environment=OPENSSM_SERVER=https://openssm.example.com
Environment=OPENSSM_STATE_FILE=/var/lib/openssm/agent.json
EnvironmentFile=-/etc/openssm/activation.env
ExecStart=/usr/local/bin/openssm-agent
Restart=always

[Install]
WantedBy=multi-user.target
```

Kubernetes: enable the agent DaemonSet in the Helm chart (`agent.enabled=true`, `agent.activationSecret=<secret>`).

## Security notes

- Commands and sessions run with the agent's privileges. Run it as a dedicated user unless you need root.
- The state file contains the instance token; protect it like a password. Deregistering the instance in OpenSSM
  revokes it.
- Use HTTPS (`-server https://…`) whenever the agent crosses an untrusted network.
