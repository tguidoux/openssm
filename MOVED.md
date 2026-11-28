# ⚠️ This repository has moved

OpenSSM is now part of the [tguidoux/openaws](https://github.com/tguidoux/openaws) monorepo — a collection of
lightweight, self-hostable AWS-compatible services that share components, build and deployment. This repository
(`tguidoux/openssm`) will no longer receive new issues, pull requests or releases.

## New location

| What | Where |
|------|-------|
| SSM server & agent | <https://github.com/tguidoux/openaws/tree/main/apps/go/ssm> |
| SSM libraries | <https://github.com/tguidoux/openaws/tree/main/pkgs/v1/ssm> |
| Shared libraries | <https://github.com/tguidoux/openaws/tree/main/pkgs/v1/common> |
| Helm chart | <https://github.com/tguidoux/openaws/tree/main/deploy/helm/ssm> |

## Migration

- **Go import paths** change from `github.com/tguidoux/openssm/...` to `github.com/tguidoux/openaws/...`.
  Re-point your `go.mod` `require` entries and rewrite imports accordingly.
- **Container images** move within GHCR:

  | Old | New |
  |-----|-----|
  | `ghcr.io/tguidoux/openssm/openssm-server` | `ghcr.io/tguidoux/openaws/ssm-server` |
  | `ghcr.io/tguidoux/openssm/openssm-agent` | `ghcr.io/tguidoux/openaws/ssm-agent` |

  Update image references in Docker, Compose, Kubernetes and Helm values.
- **Issues and pull requests** — open new ones in [tguidoux/openaws](https://github.com/tguidoux/openaws), not here.
- Git clones should point at `https://github.com/tguidoux/openaws.git`; SSM code lives under `apps/go/ssm`.

Everything else — the API surface, the agent, the Helm chart values, the documentation — stays the same; only the
repository, import paths and image names change.
