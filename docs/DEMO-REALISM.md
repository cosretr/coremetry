# Demo traffic generators — shared realism model

Coremetry ships two self-contained workloads that feed a fresh
install believable telemetry. They are what an operator sees on first
boot, so they must look like a real bank — not flat synthetic noise.
(A third, the Spring Boot `java-demo/`, was removed by operator
request in v0.9.1106.)

| Workload | Path | What it is |
|---|---|---|
| **Go demo** | `cmd/demo/` | Pure synthetic OTLP generator. Hand-builds traces/logs/metrics for a ~45-service retail-banking mesh (Oracle core, Kafka, polyglot runtimes) and POSTs OTLP/protobuf straight to the collector. No real services run. |
| **JBoss demo** | `jboss-demo/` | JAX-RS/JBoss app instrumented zero-code by the OTel **javaagent**, driven by `LoadDriver`. |

## Shared realism model (v0.8.x) — keep all workloads honest

Real production traffic is **not** a flat line with fixed error odds and
uniform latency. The Go demo (`cmd/demo/realism.go`) carries the load
model so metrics, traces and logs tell one coherent story:

1. **Diurnal curve + spikes.** A 0.28→1.0 business-day multiplier
   (overnight trough, ~10:00 peak, ~19:00 bump) plus organic
   micro-spikes scales the emission rate. The demo genuinely slows
   overnight and surges at the morning peak. Drives the Poisson
   inter-arrival gap in the driver loop.
2. **Incidents.** Every few minutes a 1–4 min degradation window starts
   (`oracle-row-lock-contention`, `jvm-gc-pause-storm`,
   `downstream-dependency-degraded`, `noisy-neighbor-cpu-steal`) that
   raises latency AND error rate together, then recovers on its own.
3. **Log-normal latency.** `dur()`
   samples a right-skewed distribution — dense body near the floor, long
   p99 tail — scaled by the live latency factor, so saturation shows up
   as a coordinated latency rise across every hop.
4. **Correlated errors.** `rollFail(pct)`
   folds the incident error-bump into each per-hop failure roll,
   so failures CLUSTER during an incident instead of being uniform.
   Error logs also spike in density and carry an `incident` attribute.
5. **Real histogram buckets.** Duration histograms emit explicit
   `ExplicitBounds` + `BucketCounts` (`latencyBounds` in Go;
   `OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION=explicit_bucket_histogram`
   for the javaagent-driven jboss-demo) so the backend computes real p50/p90/p95/p99 —
   not just min/max/avg.
6. **Richer saturation metrics.** Per-service connection-pool
   usage/max/utilization, cache hit-ratio, in-flight + queued requests,
   GC pause, host CPU/mem, and Kafka consumer lag — all load-correlated
   (Go: `flush()` gauge loop).

7. **Load-bound N+1 fan-out (v0.9.1284).** `MeshPortfolioValuation`
   (`portfolio-service`, `cmd/demo/mesh.go`) emits a Hibernate/JPA
   1 + N: one `SELECT positions`, then N identical
   `SELECT instrument_prices` client spans. N is **not** a constant —
   `nPlusOneRepeats(L.latencyFactor(), jitter)` gives ~8 at rest and
   climbs to the 40 ceiling while an incident saturates the mesh, with a
   floor of 6 so the repeat chip (threshold 5) is always live. It is the
   local fixture for the v0.9.1277 N+1 family: the waterfall repeat
   chip, the ×N sibling grouping, and Explore's
   `?result=repeats&groupBy=db.statement`.

**Rule:** any new demo scenario or metric must read from the load model
(`L`) rather than rolling its own fixed probability or
uniform latency, or it will visibly desync from the rest of the data.

## Kubernetes / OpenShift metric simulator (v0.10.2486, local only)

`cmd/demo/k8s_metrics.go` + `k8s_incident.go`. Local VictoriaMetrics had
no `kube_*` / `container_*` series, so every Kubernetes-metric feature
(pod / namespace / cluster CPU-memory, restarts, phases, the `pod_oom`
detector, the pod page) was empty locally even though spans already
carry `k8s.pod.name` etc. The simulator emits, for the **same**
cluster / namespace / workload / pod / container the spans use (same
helpers: `clusterFor`, `namespaceFor`, `rsHashFor`, `podIP`), the
series the app queries (`internal/thanos/`, `resource_bundle.go`,
`internal/entity/samples.go`, `internal/ml/pod_oom.go`), in the label
shape OpenShift monitoring returns:

- **kube-state-metrics** (`job="kube-state-metrics"`): `kube_pod_info`,
  `kube_pod_owner` (ReplicaSet / StatefulSet), `kube_replicaset_owner`,
  `kube_pod_status_phase`, `kube_pod_container_status_{ready,
  restarts_total,last_terminated_reason,last_terminated_timestamp,
  waiting_reason}`, `kube_pod_container_resource_{requests,limits}`,
  `kube_deployment_{spec_replicas,status_replicas_ready,status_condition}`,
  `kube_statefulset_{replicas,status_replicas_ready}`,
  `kube_node_{info,role,status_condition,status_capacity,
  status_allocatable,created}`.
- **kubelet cAdvisor** (`job="kubelet"`, `metrics_path="/metrics/cadvisor"`):
  `container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`,
  `container_cpu_cfs_{periods,throttled_periods}_total` — container row
  plus the pod row (`container=""`) and pause row (`container="POD"`)
  the queries filter out; `container_network_{receive,transmit}_bytes_total`
  (pod level).
- **node-exporter** (`instance` = node name, as on OpenShift):
  `node_cpu_seconds_total`, `node_memory_Mem{Total,Available}_bytes`,
  `node_network_*_bytes_total`, `node_filesystem_{size,avail}_bytes`.

Every series carries `cluster="<demo cluster>"`. Nodes are
`worker-N.<cluster>.demo.local` plus one `infra-1`. Counters really
accumulate; gauges follow `L` (diurnal + incidents) with per-pod offsets.
About 7.7k series per push (233 pods, 30 nodes).

**Transport:** VictoriaMetrics `/api/v1/import/prometheus` with explicit
timestamps, every 15 s. Sending them as OTLP through the collector would
rename and relabel the series, and would also copy them into Coremetry's
`metric_points` table.

**Scripted incident** (repeats every `DEMO_K8S_INCIDENT_EVERY`, default
75m, minimum 25m, measured from demo start): `api-gateway`
(namespace `channels`, cluster `prod-eu-west`) pod `gw-prod-2-<gen>`.
From +4m its memory climbs toward the 512 Mi limit over 8 min. CPU and
CFS throttling rise with it, and that pod's
`GET /api/v1/accounts/{id}/balance` spans slow to about 3× its siblings'.
At +12m it is OOMKilled (restart +1, `last_terminated_reason=OOMKilled`)
and is not ready for 45 s; while it is not ready, `pickPod` sends its
traffic to the siblings. It recovers, ramps again from +15m, and is
OOMKilled again at +21m, this time with 30 s of `CrashLoopBackOff`.
`gw-prod-1/3` stay normal and form the sibling band. Empty states:
`notif-prod-3-<gen>` (notification-service) is healthy but idle, and
`stmt-prod-3-<gen>` (statement-service) is deleted 10 min after start
(its series stop).

**Turn on / off:** the binary is off unless `DEMO_K8S_METRICS_URL` is set
(VM base URL). The local override `~/.coremetry-local/docker-compose.minimal.yml`
sets it to `http://victoriametrics:8428`. `docker-compose.yml` passes it
through, empty by default. Delete the line and recreate `go-demo` to
turn it off. Then register the demo clusters as Remote Clusters (an
audited admin API call; idempotent, local URLs only):
`scripts/demo-k8s-clusters.sh` (`--remove` undoes it). It registers one
querier in front of N clusters: URL = VM, `thanosLabelName=cluster`,
auth `none`. `pod_oom` additionally needs ML enabled
(Settings › ML, `enabled`).
