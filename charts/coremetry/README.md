# Coremetry Helm chart

Enterprise OpenTelemetry APM — traces, metrics, logs, profiles on
ClickHouse. One image, one tag, one release. The same single binary
runs as a monolithic POC install or scales out into role-split
Deployments for billion-span-a-day production.

- Chart `version` / Coremetry `appVersion`: see [`Chart.yaml`](Chart.yaml).
- Source: <https://github.com/cosretr/coremetry>

## Quick start (monolithic)

```bash
helm upgrade --install coremetry \
  oci://ghcr.io/cosretr/charts/coremetry \
  -n coremetry --create-namespace
```

This brings up one Coremetry Deployment plus the bundled ClickHouse,
Redis, and OTel Collector — suitable for SME / POC / single-node
installs. See [`templates/NOTES.txt`](templates/NOTES.txt) (printed
after install) for sign-in, OTLP endpoint, and HA notes.

## Deployment modes

`deployment.mode` selects the topology:

- **`monolithic`** (default) — one Deployment, `COREMETRY_MODE=all`.
  `replicaCount` applies. One Service fronts UI/API + OTLP/gRPC.
- **`distributed`** — three Deployments (ingest / api / worker) running
  the same image in different roles via `COREMETRY_MODE`, plus four
  Services. Worker is locked at 1 replica (leader-elected via Redis);
  the HPA targets the api role; the stable `<release>` Service aliases
  the api role so Route/Ingress don't change.

## Deploying on OpenShift (distributed)

Production-grade, real-bank guidance — restricted-v2 SCC, air-gapped
registries, external ClickHouse + Redis, OpenShift Route, MCP/SSE
session affinity, destructive reset-schema, upgrade safety, render
smoke tests, and a complete `values-openshift.yaml`:

→ **[docs/openshift-distributed.md](docs/openshift-distributed.md)**

For flat (non-Helm) OpenShift manifests, see
[`examples/openshift/`](../../examples/openshift/README.md).

## Configuration

All knobs and their defaults are documented inline in
[`values.yaml`](values.yaml). Highlights:

| Area | Key(s) |
|---|---|
| Topology | `deployment.mode`, `deployment.roles.*.replicas`, `deployment.roles.*.resources` |
| Image / air-gap | `image.*`, `global.imageRegistry`, `global.imagePullSecrets` |
| External ClickHouse | `clickhouse.enabled`, `clickhouse.external.addr`, `clickhouse.secure` |
| External Redis | `redis.enabled`, `redis.external.url` |
| Secrets | `secrets.existingSecret` (preferred) or inline `secrets.*` |
| Integration secrets (`tokenRef`) | `extraEnv`, `envFrom`, `extraVolumes`, `extraVolumeMounts`: the monolithic pod plus the api, worker and ingest roles, never agent (v0.10.958): since v0.10.966 runbook bash steps get a minimal env allowlist, but mounted files and the network stay reachable from a step. Ingest needs them because VictoriaMetrics metric writes run there. The reference forms are `env:NAME` and `file:/path`. See [docs/openshift-distributed.md §5](docs/openshift-distributed.md#integration-tokens-tokenref-via-extraenv--envfrom--extravolumes) |
| Exposure | `route.enabled` (OpenShift) or `ingress.enabled` (vanilla k8s) |
| MCP / SSE stickiness | `service.sessionAffinity`, `service.sessionAffinityTimeoutSeconds` |
| Autoscaling | `autoscaling.*` (targets the api role in distributed mode) |
| Destructive reset | `clickhouse.resetSchema` — **pre-install only** since chart 0.9.346; harmless if left `true` on upgrades |
| Grafana MCP server | `grafanaMcp.*` — see [Grafana MCP](#grafana-mcp) |

## Grafana MCP

Deploys Grafana's official MCP server ([grafana/mcp-grafana](https://github.com/grafana/mcp-grafana))
next to Coremetry so CoSRE can query dashboards, Prometheus, Loki, alert rules and
incidents through Coremetry's external MCP client (Settings → MCP sunucuları, transport
`http` = streamable HTTP). Off by default.

### Steps

1. In Grafana, create a **service account with the Viewer role** and a token for it.
2. Store the token in a Secret:
   ```sh
   kubectl create secret generic grafana-mcp-token -n <ns> --from-literal=token=<glsa_...>
   ```
3. Enable it:
   ```sh
   helm upgrade --install coremetry charts/coremetry -n <ns> \
     --set grafanaMcp.enabled=true \
     --set grafanaMcp.grafanaUrl=https://grafana.example.test \
     --set grafanaMcp.existingSecret=grafana-mcp-token
   ```
4. Register it in Coremetry. Either add it by hand in Settings → MCP sunucuları: name `grafana`,
   transport `http`, URL `http://<fullname>-grafana-mcp.<ns>.svc:8000/mcp` (NOTES.txt prints the
   exact URL), plus the allow-list below. Or set `grafanaMcp.autoRegister=true`. Coremetry then
   adds that entry at startup with `grafanaMcp.allowTools` as its allow-list.

### What `autoRegister` does

The chart passes `COREMETRY_MCP_SEED_JSON` to the monolithic pod and to the api and worker
roles. At startup Coremetry adds the `grafana` entry **only if no entry with that name exists
and the name was never seeded before**. Seeded names are remembered in `system_settings`
(`mcp_client_seeded`), so your later edits are never overwritten and a deleted entry does not
come back. The write is audited as `settings.mcp_servers.seed` with actor `system`. Only the
`http` transport can be seeded. A token is never placed in the JSON: `tokenEnv` names an env var,
and that name must start with `COREMETRY_MCP_SEED_`.

### Values

| Key | Default | Notes |
|---|---|---|
| `grafanaMcp.enabled` | `false` | Renders Deployment (1 replica) + ClusterIP Service (+ Secret, NetworkPolicy) |
| `grafanaMcp.image.repository` / `.tag` / `.pullPolicy` / `.registry` | `grafana/mcp-grafana` / `2.0.1` / `IfNotPresent` / `""` | Docker Hub tags have no `v` (git tag `v2.0.1`). `global.imageRegistry` rewrites it for air-gapped mirrors |
| `grafanaMcp.grafanaUrl` | `""` | **Required** when enabled |
| `grafanaMcp.existingSecret` / `.existingSecretKey` | `""` / `token` | Preferred token source (`GRAFANA_SERVICE_ACCOUNT_TOKEN`) |
| `grafanaMcp.serviceAccountToken` | `""` | Alternative: the chart creates `<fullname>-grafana-mcp`. Never printed in NOTES. Mutually exclusive with `existingSecret` |
| `grafanaMcp.disableWrite` | `true` | `--disable-write`: write tools are not registered |
| `grafanaMcp.tlsSkipVerify` | `false` | `--tls-skip-verify` for a self-signed Grafana. Prefer `--tls-ca-file` via `extraArgs` + `extraVolumes` |
| `grafanaMcp.transport` / `.port` | `streamable-http` / `8000` | Coremetry's client speaks streamable HTTP only; any other transport fails the render |
| `grafanaMcp.usageStats` | `disabled` | `--usage-stats` (upstream reports anonymous usage by default) |
| `grafanaMcp.extraAllowedHosts` | `[]` | Extra `Host` values for upstream's `--allowed-hosts` check. The chart already adds every in-cluster Service name, with and without the port |
| `grafanaMcp.callerAuth.existingSecret` / `.existingSecretKey` | `""` / `token` | Optional caller auth (`MCP_GRAFANA_SERVER_TOKEN`): requests without `Authorization: Bearer <token>` get a 401. Put the same value in the Coremetry entry's token field. `autoRegister` carries it for you |
| `grafanaMcp.autoRegister` | `false` | Seed the `grafana` entry once (see above) |
| `grafanaMcp.allowTools` | read-only list | Seeded allow-list, also printed in NOTES. A trailing `*` matches a prefix |
| `grafanaMcp.extraArgs` / `.extraEnv` / `.extraVolumes` / `.extraVolumeMounts` | `[]` | e.g. `--disable-oncall`, `--log-level=debug` |
| `grafanaMcp.resources` | 50m/64Mi → 500m/256Mi | |
| `grafanaMcp.nodeSelector` / `.tolerations` / `.affinity` | empty | |
| `grafanaMcp.openshift` | `true` | `true`: no fixed UID, so the restricted-v2 SCC assigns one. **Vanilla k8s: set `false`.** The image's `USER` is non-numeric, so `runAsNonRoot` can't be verified unless the chart adds `runAsUser/runAsGroup: 1000` |
| `grafanaMcp.podSecurityContext` / `.securityContext` | `{}` / restricted | runAsNonRoot, read-only root FS (+ `/tmp` emptyDir), drop ALL, RuntimeDefault seccomp |
| `grafanaMcp.networkPolicy.enabled` / `.extraFrom` | `true` / `[]` | Ingress only from this release's Coremetry pods (`coremetry`, `coremetry-api`, `coremetry-worker`) |

### Security notes

- **Viewer service-account token.** The MCP server acts with the token's rights. Viewer plus
  `--disable-write` gives you two independent locks against writes.
- **`--disable-write` stays on** unless you have a reviewed reason to turn it off.
- **NetworkPolicy.** By default the endpoint authenticates to Grafana but does not authenticate
  its callers. The policy limits ingress to Coremetry pods. Turn on `callerAuth` for defence in
  depth. The Service is ClusterIP only, so don't expose it through an Ingress or Route.
- **Allow-list read-only tools** on the Coremetry entry (`grafanaMcp.allowTools`). Every external
  call is audited (`mcp.call`).
- **Wiki tools.** While any external MCP server is configured, CoSRE hides `search_wiki` and
  `read_wiki_page`, so wiki text can't flow into external tool arguments. The wiki still answers
  through its own retrieval tier.
- **Host check.** Upstream validates the `Host` header (DNS-rebinding guard), and the chart
  allow-lists the Service names. Probes are TCP, because an `httpGet` to `/healthz` would be
  rejected by that check.
