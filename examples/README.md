# OpenSSM examples

| Example | Auth | Storage | Use case |
|---------|------|---------|----------|
| [docker/Dockerfile.no-auth](docker/Dockerfile.no-auth) | Disabled | Memory | Local dev / CI |
| [docker/Dockerfile.with-creds](docker/Dockerfile.with-creds) | Pre-seeded | Memory | Local dev |
| [docker-compose.no-auth.yml](docker-compose/docker-compose.no-auth.yml) | Disabled | Memory | Local dev |
| [docker-compose.with-creds.yml](docker-compose/docker-compose.with-creds.yml) | Pre-seeded | Memory | Local dev |
| [docker-compose.sqlite.yml](docker-compose/docker-compose.sqlite.yml) | Pre-seeded | SQLite volume | Persistent dev |
| [docker-compose.agent.yml](docker-compose/docker-compose.agent.yml) | Pre-seeded | Memory | Server + managed instance (Run Command, sessions) |
| [k8s.no-auth.yaml](kubernetes/k8s.no-auth.yaml) | Disabled | Memory | Staging |
| [k8s.with-creds.yaml](kubernetes/k8s.with-creds.yaml) | Pre-seeded | Memory | Staging |
| [k8s.persistent.yaml](kubernetes/k8s.persistent.yaml) | Pre-seeded | SQLite + PVC | Production-like |

For production Kubernetes deployments prefer the [Helm chart](../deploy/helm).

## Pre-seeded credentials

The `with-creds` configs seed the AWS documentation example key pair:

```bash
export AWS_ENDPOINT_URL=http://localhost:9330
export AWS_DEFAULT_REGION=us-east-1
export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
export AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

These are public example values — replace them and `serverSecret` anywhere that matters.

## AWS CLI scripts

- [commands/quick-start.sh](commands/quick-start.sh) — Parameter Store (String/StringList/SecureString,
  versions, labels, tags, filters) and Command documents.
- [commands/run-command-and-sessions.sh](commands/run-command-and-sessions.sh) — register this machine with
  `openssm-agent`, run a command by tag, and open an interactive `aws ssm start-session` shell.

## Session Manager and StreamUrl

`session-manager-plugin` only connects to stream hosts shaped like `ssmmessages.<region>.<domain>`.
The example configs set `sessions.streamUrlBase: ws://ssmmessages.us-east-1.localhost:9330`, which resolves
to `127.0.0.1` on most systems. If your resolver does not handle `*.localhost`, either add a hosts entry or
leave `streamUrlBase` empty and export `AWS_SSM_PLUGIN_ADDITIONAL_STREAM_HOSTS=localhost` on the client.
