# Rollouts v2 §11 sorgu paketi — v0.10.979

- run: `a1b2c3d4e5f6` · status: **done**
- started 2026-09-27T10:00:00Z · finished 2026-09-27T10:02:51Z · duration 171204 ms · budget 300 s
- calls 318 / planned 331 · packs T,K,D,R,H,N · tokens 35
- unit `clickhouse` (clickhouse): ok, 4 calls, 2400 ms
- unit `cluster-a` (target): ok, 63 calls, 41211 ms, namespace filter
- unit `cluster-b` (target): unauthorized, 2 calls, 900 ms, early stop: unauthorized streak after K0.2 (thanos rejected the cluster credentials for cluster-b, HTTP 403)
- unit `hub-1` (hub): ok, 104 calls, 92300 ms, without-matcher pass
- unit `hub-2` (hub): partial, 88 calls, 70000 ms

Eşleme özel: değerler jetonlu (§11.0); etiket adları, sayımlar, enum, sürüm, port ve uyarı metni harfi harfine. Rapor şablon ifadeleri basar, etkin ifadeyi değil.

## §11.9 paste-back

| ID | cluster-a | cluster-b | hub-1 | hub-2 |
|---|---|---|---|---|
| K0.2 KSM jobs / K0.3 version / K0.4 dup ratio / K0.5 scrape | K0.2: kube-state-metrics=1, openshift-state-metrics=1 · K0.3: v2.13.0=1 · K0.4: 1 (dedup off 2) · K0.5: 10 | K0.2: unauthorized · K0.3: skipped · K0.4: skipped (dedup off skipped) · K0.5: skipped | — | — |
| K1 presence table (name → count) | K1.D: kube_deployment_labels=3, kube_deployment_metadata_generation=3, kube_deployment_spec_paused=3, … +8 · K1.R: kube_replicaset_labels=3, kube_replicaset_owner=3, kube_replicaset_spec_replicas=3, … +2 · K1.P: kube_pod_container_info=3, kube_pod_info=3, kube_pod_labels=3, … +2 · K1.S: kube_statefulset_metadata_generation=3, kube_statefulset_replicas=3, kube_statefulset_status_current_revision=3, … +7 · K1.DS: kube_daemonset_metadata_generation=3, kube_daemonset_status_current_number_scheduled=3, kube_daemonset_status_desired_number_scheduled=3, … +5 · K1.H: Deployment=8, StatefulSet=1 · K1.X: 4 names | K1.D: skipped · K1.R: skipped · K1.P: skipped · K1.S: skipped · K1.DS: skipped · K1.H: skipped · K1.X: skipped | — | — |
| K2.2 image label forms; K2.3 `reason` present?; K2.4 pod labels | K2.2a: 900 · K2.2b: 900 · K2.2c: 900 · K2.2d: 0 · K2.2e: 850 · K2.3c: {Available/MinimumReplicasAvailable/true}=158, {Progressing/NewReplicaSetAvailable/true}=156, {Progressing/ProgressDeadlineExceeded/false}=2 · K2.4a: 870 · K2.4b: 30 · K2.4c: 0 · K2.4d: 12 · K2.4e: 500 · K2.4f: 480 · K2.4g: 0 | K2.2a: skipped · K2.2b: skipped · K2.2c: skipped · K2.2d: skipped · K2.2e: skipped · K2.3c: skipped · K2.4a: skipped · K2.4b: skipped · K2.4c: skipped · K2.4d: skipped · K2.4e: skipped · K2.4f: skipped · K2.4g: skipped | — | — |
| K3 counts (any > 1000?) | K3.1: 1200 (ns 1200) · K3.2: 900 (ns 900) · K3.3: 300 (ns 300) · K3.4: 160 (ns 160) · K3.5: 20 (ns 20) · K3.6: 30 (ns 30) · K3.7: 3000 (ns 3000) | K3.1: skipped · K3.2: skipped · K3.3: skipped · K3.4: skipped · K3.5: skipped · K3.6: skipped · K3.7: skipped | — | — |
| K4 stuck / lag / paused / STS mid-rollout | K4.1: 2 · K4.2: 1 · K4.3: 0 · K4.4: 1 | K4.1: skipped · K4.2: skipped · K4.3: skipped · K4.4: skipped | — | — |
| K5 gen bumps : new RS : reactivated RS : scaled deployments | K5.1: 40 · K5.3: 10 · K5.4: 1 · K5.2: 6 · K5.5: 2 · K5.6: 0 | K5.1: skipped · K5.3: skipped · K5.4: skipped · K5.2: skipped · K5.5: skipped · K5.6: skipped | — | — |
| K6 v1 leg counts (`_created` present?) | K6.1: 1200 · K6.2: 900 · K6.3: 900 · K6.4: 0 (empty) | K6.1: skipped · K6.2: skipped · K6.3: skipped · K6.4: skipped | — | — |
| D DC counts and share | D1: 12 · D2: 10 · D3: 4 · D4: 30 · D5: 0 (empty) · D6: kube_replicationcontroller_owner=3, openshift_deploymentconfig_metadata_generation=3, openshift_deploymentconfig_spec_replicas=3 · D7: 1 · D8: 150 · share 6.2% | D1: skipped · D2: skipped · D3: skipped · D4: skipped · D5: skipped · D6: skipped · D7: skipped · D8: skipped | — | — |
| R Argo Rollouts names/counts | R0: 0 (empty) · R1: 0 (empty) · R2: 0 (empty) · R3: 0 (empty) | R0: skipped · R1: skipped · R2: skipped · R3: skipped | R0: 0 (empty) · R1: 0 (empty) · R2: 0 (empty) · R3: 0 (empty) | R0: 0 (empty) · R1: 0 (empty) · R2: 0 (empty) · R3: 0 (empty) |
| H0.1 / H0.4 / dedup / label names (H0.3 vs H0.5) | — | — | H0.1: 40000 · H0.4: 40000 · H0.2: {<prometheus-1>/}=40000 (dedup off {<prometheus-1>/<replica-1>}=40000, {<prometheus-1>/<replica-2>}=40000) · H0.3: {hub-1/hub-1/cluster-a/<prometheus-1>}=40000 · H0.5: {hub-1/hub-2/cluster-a/<prometheus-1>/hub-1/cluster-b}=12 · labels [cluster,cluster_name,k8s_cluster,prometheus] vs [cluster,k8s_cluster,openshift_cluster,prometheus,tenant,tenant_id] | H0.1: 40000 · H0.4: 40000 · H0.2: {<prometheus-1>/}=40000 (dedup off {<prometheus-1>/<replica-1>}=40000, {<prometheus-1>/<replica-2>}=40000) · H0.3: {hub-1/hub-1/cluster-a/<prometheus-1>}=40000 · H0.5: {hub-1/hub-2/cluster-a/<prometheus-1>/hub-1/cluster-b}=12 · labels [cluster,cluster_name,k8s_cluster,prometheus] vs [cluster,k8s_cluster,openshift_cluster,prometheus,tenant,tenant_id] |
| H1.2 case (A/B/C); instances; max apps per instance and shard | — | — | H1.2: {<team-1>-prod/<team-1>-prod-metrics/<team-1>-prod}=12000, {/<team-2>-dev-metrics/<team-2>-dev}=9000, {<ns-2>/<job-1>/<ns-1>}=400 · H1.1: {<team-1>-prod-metrics/<team-1>-prod}=12000, {<team-2>-dev-metrics/<team-2>-dev}=9000, {<job-1>/<ns-1>}=400 · H1.5: {<team-1>-prod-metrics/<team-1>-prod}=12000, {<team-2>-dev-metrics/<team-2>-dev}=9000, {<job-1>/<ns-1>}=400 · H1.6a: 9000 · H1.6b: 3000 · H1.6c: 0 · case A=1 B=1 C=1 | H1.2: {<team-1>-prod/<team-1>-prod-metrics/<team-1>-prod}=12000, {/<team-2>-dev-metrics/<team-2>-dev}=9000, {<ns-2>/<job-1>/<ns-1>}=400 · H1.1: {<team-1>-prod-metrics/<team-1>-prod}=12000, {<team-2>-dev-metrics/<team-2>-dev}=9000, {<job-1>/<ns-1>}=400 · H1.5: {<team-1>-prod-metrics/<team-1>-prod}=12000, {<team-2>-dev-metrics/<team-2>-dev}=9000, {<job-1>/<ns-1>}=400 · H1.6a: 9000 · H1.6b: 3000 · H1.6c: 0 · case A=1 B=1 C=1 |
| H2 `dest_server` count, `""`, in-cluster, ports, apps per target ns | — | — | H2.2: 5 · H2.3: 5 · H2.4: 100 · H2.5: 6443=4 · H2.6: 1200 · H2.7: 1=3000, 2=500 · H2.8: 3 | H2.2: 5 · H2.3: 5 · H2.4: 100 · H2.5: 6443=4 · H2.6: 1200 · H2.7: 1=3000, 2=500 · H2.8: 3 |
| H3 duplicates / pairs / pair mismatches | — | — | H3.1: 4 · H3.2: 1=38000, 2=4 · H3.3: 2 · H3.4: 2=17000, 1=2000 · H3.5[pair:pair-1]: 1 | H3.1: 4 · H3.2: 1=38000, 2=4 · H3.3: 2 · H3.4: 2=17000, 1=2000 · H3.5[pair:pair-1]: 1 |
| H4 status table; autosync share | — | — | H4.1: {Healthy/Synced}=37000, {Healthy/OutOfSync}=2000, {Degraded/Synced}=140 · H4.2: =39900, sync=100 · H4.3: true=30000, false=10000 | H4.1: {Healthy/Synced}=37000, {Healthy/OutOfSync}=2000, {Degraded/Synced}=140 · H4.2: =39900, sync=100 · H4.3: true=30000, false=10000 |
| H5 label names; versions per instance | — | — | H5.1a: 12 names · H5.1b: 7 names · H5.1c: 5 names · H5.1d: 3 names · H5.3a: 40000 · H5.3b: 40000 · H5.3c: 0 · H5.3d: 40000 · H5.3e: {<team-2>-dev/v2.11.0}=1, {<team-1>-prod/v2.13.3}=1 | H5.1a: error · H5.1b: 7 names · H5.1c: 5 names · H5.1d: 3 names · H5.3a: 40000 · H5.3b: 40000 · H5.3c: 0 · H5.3d: 40000 · H5.3e: {<team-2>-dev/v2.11.0}=1, {<team-1>-prod/v2.13.3}=1 |
| H6 syncs/h and /24h by phase and autosync; transitions/h and /2m; non-steady size | — | — | H6.1: 39000 · H6.2a: Succeeded=120, Failed=3 · H6.2b[inst:<team-2>-dev]: Succeeded=2900, Failed=40 · H6.2b[inst:<team-1>-prod]: Succeeded=2900, Failed=40 · H6.3: true=2500, false=440 · H6.4: 3 · H6.5: 12 · H6.6: 1 · H6.7: 140 | H6.1: 39000 · H6.2a: Succeeded=120, Failed=3 · H6.2b: timeout · H6.3: true=2500, false=440 · H6.4: 3 · H6.5: 12 · H6.6: 1 · H6.7: 140 |
| N1–N7 ratios, near misses, suffix share and uniqueness, hyphenated teams? | — | — | N1: 0.93 · N3a: 120 · N3b: 40 · N3c: 300 · N4: <sfx-1>=300, <suffix-a>=120, <sfx-2>=40 · N5b: https://api.cluster-b.<domain>:6443=0.97, https://api.cluster-a.<domain>:6443=0.95 · N6: 0 (empty) · N7: 0 (empty) · L1: 2800 | N1: 0.93 · N3a: 120 · N3b: 40 · N3c: 300 · N4: <sfx-1>=300, <suffix-a>=120, <sfx-2>=40 · N5b: https://api.cluster-b.<domain>:6443=0.97, https://api.cluster-a.<domain>:6443=0.95 · N6: 0 (empty) · N7: 0 (empty) · L1: 2800 |
| A (optional) version; field presence; username kind | skipped (metrics-only) | skipped (metrics-only) | skipped (metrics-only) | skipped (metrics-only) |
| V HTTP codes, counts, repo kind, flavour, proxy | skipped (metrics-only) | skipped (metrics-only) | skipped (metrics-only) | skipped (metrics-only) |
| T1/T2 coverage and cluster form (per span cluster value) | T1: sampled=120000 depl=119500 rs=119500 env_name=0 k8s_cluster=120000 ocp_cluster=0 · T2: deploy_env=prod-cluster-a n=120000 | T1: sampled=80000 depl=79800 rs=79800 env_name=0 k8s_cluster=80000 ocp_cluster=0 · T2: deploy_env=prod-cluster-b n=80000 | — | — |
| Querier limits (timeout, max samples); warnings seen | errors [] · warnings: store cluster-a responded partially | errors [unauthorized:thanos×2] | errors [] | errors [error:bad_data×1 timeout:query×1] |

## Pack T

### T @ `clickhouse`

#### T1 — span cluster değeri başına öznitelik kapsaması (15 dk örneklem, ≤50 satır)

```promql
rolloutProbeCoverageSQL(900, cluster)
```
- state: **ok** · 114 ms

  | cluster | container_id | depl | ds | env_name | img_tag | k8s_cluster | ocp_cluster | rs | sampled | sts | svc_version | value |
  |---|---|---|---|---|---|---|---|---|---|---|---|---|
  | cluster-a | 119000 | 119500 | 200 | 0 | 118000 | 120000 | 0 | 119500 | 120000 | 300 | 5000 | 120000 |
  | cluster-b | 79500 | 79800 | 100 | 0 | 79000 | 80000 | 0 | 79800 | 80000 | 100 | 4000 | 80000 |
  |  | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 300 | 0 | 0 | 300 |


- `fallback` state: **ok** — T0 fallback: `cluster` column missing — 0011 not applied · 800 ms

  | cluster | container_id | depl | ds | env_name | img_tag | k8s_cluster | ocp_cluster | rs | sampled | sts | svc_version | value |
  |---|---|---|---|---|---|---|---|---|---|---|---|---|
  | cluster-a | 119000 | 119500 | 200 | 0 | 118000 | 120000 | 0 | 119500 | 120000 | 300 | 5000 | 120000 |


#### T2 — deploy_env × cluster biçimi (türetilen cluster formu görünür)

```promql
rolloutProbeEnvSQL(900, cluster)
```
- state: **ok** · 114 ms

  | cluster | deploy_env | value |
  |---|---|---|
  | cluster-a | prod-cluster-a | 120000 |
  | cluster-b | prod-cluster-b | 80000 |
  |  | <deploy-env-1> | 300 |


#### T3 — eşleşmemiş span cluster değeri sayısı (+ ≤50 jetonlu değer)

```promql
EntitySeenClusterValues(now-7d) + SpanClusterOwner
```
- state: **ok** — unmapped=1 total=3 · 114 ms

  | owner | span_cluster | value |
  |---|---|---|
  |  | cluster-c | 300 |


## Pack K

### K @ `cluster-a`

#### K0.1 — skaler > 0 — matcher çalışıyor

```promql
count(kube_node_info)
```
- state: **ok** · 128 ms
  - scalar: `180`

#### K0.2 — kube-state-metrics (+ openshift-state-metrics); ek job = ikinci KSM

```promql
count by (job) (up{job=~".*state-metrics.*"} == 1)
```
- state: **ok** · 128 ms

  | job | value |
  |---|---|
  | kube-state-metrics | 1 |
  | openshift-state-metrics | 1 |


#### K0.3 — tek sürüm satırı (boş = telemetri portu scrape edilmiyor)

```promql
count by (version) (kube_state_metrics_build_info)
```
- state: **ok** · 128 ms

  | version | value |
  |---|---|
  | v2.13.0 | 1 |


#### K0.4 — tam 1; > 1 = kopya (dedup)

```promql
count(kube_deployment_spec_replicas) / count(count by (namespace, deployment) (kube_deployment_spec_replicas))
```
- state: **ok** · 128 ms
  - scalar: `1`

- `dedup_off` state: **ok** · 128 ms
  - scalar: `2`

#### K0.5 — 10 ⇒ 60 s scrape; 20 ⇒ 30 s

```promql
quantile(0.5, count_over_time(kube_deployment_metadata_generation[10m]))
```
- state: **ok** · 128 ms
  - scalar: `10`

#### K0.6 — < 90 s (tazelik)

```promql
time() - max(timestamp(kube_deployment_metadata_generation))
```
- state: **ok** · 128 ms
  - scalar: `42`

#### K0.6b — iki örnek ≥30 s arayla: dedup=true querier aynı scrape zamanını döndürür mü

```promql
max(timestamp(kube_deployment_metadata_generation))
```
- sample 1 (t+6 s): 1784271060 · sample 2 (t+37 s): 1784271060 · equal: yes

- state: **ok** · 135 ms
  - scalar: `1784271060`

- `second_sample` state: **ok** · 135 ms
  - scalar: `1784271060`

#### K1.D — CMO'da created, annotations yok

```promql
count by (__name__) ({__name__=~"kube_deployment_(metadata_generation|status_observed_generation|spec_replicas|status_replicas|status_replicas_updated|status_replicas_available|status_replicas_ready|status_replicas_unavailable|status_condition|spec_paused|created|labels|annotations)"})
```
- state: **ok** · 128 ms

  | __name__ | value |
  |---|---|
  | kube_deployment_labels | 3 |
  | kube_deployment_metadata_generation | 3 |
  | kube_deployment_spec_paused | 3 |
  | kube_deployment_spec_replicas | 3 |
  | kube_deployment_status_condition | 3 |
  | kube_deployment_status_observed_generation | 3 |
  | kube_deployment_status_replicas | 3 |
  | kube_deployment_status_replicas_available | 3 |
  | kube_deployment_status_replicas_ready | 3 |
  | kube_deployment_status_replicas_unavailable | 3 |
  | kube_deployment_status_replicas_updated | 3 |

  - warning: store cluster-a responded partially

#### K1.R — CMO'da created, metadata_generation, status_observed_generation yok

```promql
count by (__name__) ({__name__=~"kube_replicaset_(owner|created|spec_replicas|status_replicas|status_ready_replicas|metadata_generation|status_observed_generation|labels)"})
```
- state: **ok** · 128 ms

  | __name__ | value |
  |---|---|
  | kube_replicaset_labels | 3 |
  | kube_replicaset_owner | 3 |
  | kube_replicaset_spec_replicas | 3 |
  | kube_replicaset_status_ready_replicas | 3 |
  | kube_replicaset_status_replicas | 3 |


#### K1.P — container_info + owner şart

```promql
count by (__name__) ({__name__=~"kube_pod_(container_info|owner|labels|info|created|start_time)"})
```
- state: **ok** · 128 ms

  | __name__ | value |
  |---|---|
  | kube_pod_container_info | 3 |
  | kube_pod_info | 3 |
  | kube_pod_labels | 3 |
  | kube_pod_owner | 3 |
  | kube_pod_start_time | 3 |


#### K1.S — current/update_revision varsa STS rollback görünür

```promql
count by (__name__) ({__name__=~"kube_statefulset_(metadata_generation|status_observed_generation|replicas|status_replicas|status_replicas_updated|status_replicas_ready|status_replicas_available|status_replicas_current|status_current_revision|status_update_revision|created)"})
```
- state: **ok** · 128 ms

  | __name__ | value |
  |---|---|
  | kube_statefulset_metadata_generation | 3 |
  | kube_statefulset_replicas | 3 |
  | kube_statefulset_status_current_revision | 3 |
  | kube_statefulset_status_observed_generation | 3 |
  | kube_statefulset_status_replicas | 3 |
  | kube_statefulset_status_replicas_available | 3 |
  | kube_statefulset_status_replicas_current | 3 |
  | kube_statefulset_status_replicas_ready | 3 |
  | kube_statefulset_status_replicas_updated | 3 |
  | kube_statefulset_status_update_revision | 3 |


#### K1.DS — generation + observed + desired/updated

```promql
count by (__name__) ({__name__=~"kube_daemonset_(metadata_generation|status_observed_generation|status_desired_number_scheduled|status_updated_number_scheduled|status_number_available|status_number_unavailable|status_number_ready|status_current_number_scheduled|created)"})
```
- state: **ok** · 135 ms

  | __name__ | value |
  |---|---|
  | kube_daemonset_metadata_generation | 3 |
  | kube_daemonset_status_current_number_scheduled | 3 |
  | kube_daemonset_status_desired_number_scheduled | 3 |
  | kube_daemonset_status_number_available | 3 |
  | kube_daemonset_status_number_ready | 3 |
  | kube_daemonset_status_number_unavailable | 3 |
  | kube_daemonset_status_observed_generation | 3 |
  | kube_daemonset_status_updated_number_scheduled | 3 |


#### K1.H — HPA payı (generation gürültüsü)

```promql
count by (scaletargetref_kind) (kube_horizontalpodautoscaler_info)
```
- state: **ok** · 128 ms

  | scaletargetref_kind | value |
  |---|---|
  | Deployment | 8 |
  | StatefulSet | 1 |


#### K1.X — PromQL'siz çapraz kontrol: metrik adları

```promql
{__name__=~"kube_(deployment|replicaset|statefulset|daemonset|replicationcontroller)_.*"}
```
- state: **ok** · 128 ms
  - names (4): kube_daemonset_metadata_generation, kube_deployment_metadata_generation, kube_replicaset_owner, kube_statefulset_metadata_generation

#### K2.1a — etiket adları: image, image_spec, image_id …

```promql
kube_pod_container_info
```
- state: **ok** · 135 ms
  - names (7): container, container_id, image, image_id, image_spec, namespace, pod

#### K2.1b — namespace, replicaset, owner_kind, owner_name, owner_is_controller

```promql
kube_replicaset_owner
```
- state: **ok** · 135 ms
  - names (5): namespace, owner_is_controller, owner_kind, owner_name, replicaset

#### K2.1c — condition, status (+ reason ≥ v2.17)

```promql
kube_deployment_status_condition
```
- state: **ok** · 135 ms
  - names (5): condition, deployment, namespace, reason, status

#### K2.1d — namespace, statefulset, revision

```promql
kube_statefulset_status_update_revision
```
- state: **ok** · 135 ms
  - names (3): namespace, revision, statefulset

#### K2.1e — label_* adları

```promql
kube_deployment_labels
```
- state: **ok** · 135 ms
  - names (3): deployment, label_app, namespace

#### K2.2a — container sayısı

```promql
count(kube_pod_container_info)
```
- state: **ok** · 135 ms
  - scalar: `900`

#### K2.2b — image_spec dolu

```promql
count(kube_pod_container_info{image_spec!=""})
```
- state: **ok** · 135 ms
  - scalar: `900`

#### K2.2c — image_id dolu

```promql
count(kube_pod_container_info{image_id!=""})
```
- state: **ok** · 135 ms
  - scalar: `900`

#### K2.2d — image digest biçimli

```promql
count(kube_pod_container_info{image=~".+@sha256:.+"})
```
- state: **ok** · 135 ms
  - scalar: `0`

#### K2.2e — image_spec digest biçimli

```promql
count(kube_pod_container_info{image_spec=~".+@sha256:.+"})
```
- state: **ok** · 135 ms
  - scalar: `850`

#### K2.3a — enum tablosu

```promql
count by (owner_kind, owner_is_controller) (kube_replicaset_owner)
```
- state: **ok** · 135 ms

  | owner_is_controller | owner_kind | value |
  |---|---|---|
  | true | Deployment | 1150 |
  |  | <none> | 40 |


#### K2.3b — ReplicaSet, DaemonSet, StatefulSet, ReplicationController …

```promql
count by (owner_kind) (kube_pod_owner)
```
- state: **ok** · 135 ms

  | owner_kind | value |
  |---|---|
  | ReplicaSet | 800 |
  | DaemonSet | 30 |
  | ReplicationController | 30 |
  | StatefulSet | 20 |


#### K2.3c — boş reason ⇒ KSM < v2.17

```promql
count by (condition, status, reason) (kube_deployment_status_condition == 1)
```
- state: **ok** · 135 ms

  | condition | reason | status | value |
  |---|---|---|---|
  | Available | MinimumReplicasAvailable | true | 158 |
  | Progressing | NewReplicaSetAvailable | true | 156 |
  | Progressing | ProgressDeadlineExceeded | false | 2 |


#### K2.4a — pod-template-hash

```promql
count(kube_pod_labels{label_pod_template_hash!=""})
```
- state: **ok** · 135 ms
  - scalar: `870`

#### K2.4b — DS/STS revizyon kaynağı

```promql
count(kube_pod_labels{label_controller_revision_hash!=""})
```
- state: **ok** · 135 ms
  - scalar: `30`

#### K2.4c — Argo Rollouts pod'ları

```promql
count(kube_pod_labels{label_rollouts_pod_template_hash!=""})
```
- state: **ok** · 135 ms
  - scalar: `0`

#### K2.4d — DC pod'ları

```promql
count(kube_pod_labels{label_deploymentconfig!=""})
```
- state: **ok** · 135 ms
  - scalar: `12`

#### K2.4e — eşleme ipucu

```promql
count(kube_pod_labels{label_app_kubernetes_io_instance!=""})
```
- state: **ok** · 135 ms
  - scalar: `500`

#### K2.4f — eşleme ipucu

```promql
count(kube_pod_labels{label_argocd_argoproj_io_instance!=""})
```
- state: **ok** · 135 ms
  - scalar: `480`

#### K2.4g — CMO'da 0 beklenir

```promql
count(kube_deployment_labels{label_app_kubernetes_io_instance!=""})
```
- state: **ok** · 135 ms
  - scalar: `0`

#### K3.1 — > 1000 ⇒ v1 yolu bugün kesik

```promql
count(kube_replicaset_owner{owner_kind="Deployment"{{ns}}})
```
- state: **ok** · 128 ms
  - scalar: `1200`

- `ns` state: **ok** · 128 ms
  - scalar: `1200`

#### K3.2 — RS spec serisi

```promql
count(kube_replicaset_spec_replicas{{nsSel}})
```
- state: **ok** · 128 ms
  - scalar: `900`

- `ns` state: **ok** · 128 ms
  - scalar: `900`

#### K3.3 — etkin RS

```promql
count(kube_replicaset_spec_replicas{{nsSel}} > 0)
```
- state: **ok** · 128 ms
  - scalar: `300`

- `ns` state: **ok** · 128 ms
  - scalar: `300`

#### K3.4 — Deployment sayısı

```promql
count(kube_deployment_metadata_generation{{nsSel}})
```
- state: **ok** · 128 ms
  - scalar: `160`

- `ns` state: **ok** · 128 ms
  - scalar: `160`

#### K3.5 — STS sayısı

```promql
count(kube_statefulset_replicas{{nsSel}})
```
- state: **ok** · 128 ms
  - scalar: `20`

- `ns` state: **ok** · 128 ms
  - scalar: `20`

#### K3.6 — DS sayısı

```promql
count(kube_daemonset_status_desired_number_scheduled{{nsSel}})
```
- state: **ok** · 128 ms
  - scalar: `30`

- `ns` state: **ok** · 128 ms
  - scalar: `30`

#### K3.7 — > 5000 ⇒ K5 penceresi 1h

```promql
count(kube_pod_container_info{{nsSel}})
```
- state: **ok** · 128 ms
  - scalar: `3000`

- `ns` state: **ok** · 128 ms
  - scalar: `3000`

#### K4.1 — şu an stuck

```promql
count(kube_deployment_status_condition{condition="Progressing",status="false"} == 1)
```
- state: **ok** · 128 ms
  - scalar: `2`

#### K4.2 — controller gecikmesi

```promql
count(max by (namespace, deployment) (kube_deployment_status_observed_generation) < on (namespace, deployment) max by (namespace, deployment) (kube_deployment_metadata_generation))
```
- state: **ok** · 128 ms
  - scalar: `1`

#### K4.3 — paused

```promql
count(kube_deployment_spec_paused == 1)
```
- state: **ok** · 128 ms
  - scalar: `0`

#### K4.4 — STS rollout ortasında

```promql
count(kube_statefulset_status_update_revision unless on (namespace, statefulset, revision) kube_statefulset_status_current_revision)
```
- state: **ok** · 128 ms
  - scalar: `1`

#### K5.1 — generation artışları

```promql
sum(changes(kube_deployment_metadata_generation[{{k5w}}]))
```
- state: **ok** · 128 ms
  - scalar: `40`

#### K5.2 — ölçeklenen Deployment

```promql
count(changes(kube_deployment_spec_replicas[{{k5w}}]) > 0)
```
- state: **ok** · 128 ms
  - scalar: `6`

#### K5.3 — yeni RS

```promql
count(count by (namespace, replicaset) (kube_replicaset_owner{owner_kind="Deployment"}) unless count by (namespace, replicaset) (kube_replicaset_owner{owner_kind="Deployment"} offset {{k5w}}))
```
- state: **ok** · 128 ms
  - scalar: `10`

#### K5.4 — yeniden etkin RS (rollback adayı)

```promql
count((kube_replicaset_spec_replicas > 0) and on (namespace, replicaset) (kube_replicaset_spec_replicas offset {{k5w}} == 0))
```
- state: **ok** · 128 ms
  - scalar: `1`

#### K5.5 — STS generation artışları

```promql
sum(changes(kube_statefulset_metadata_generation[{{k5w}}]))
```
- state: **ok** · 128 ms
  - scalar: `2`

#### K5.6 — DS generation artışları

```promql
sum(changes(kube_daemonset_metadata_generation[{{k5w}}]))
```
- state: **ok** · 128 ms
  - scalar: `0`

#### K6.1 — > 1000 ⇒ v1 yolu kesik

```promql
count(kube_replicaset_owner{replicaset!="",owner_kind="Deployment"})
```
- state: **ok** · 128 ms
  - scalar: `1200`

#### K6.2 — v1 yolu

```promql
count(kube_replicaset_spec_replicas{replicaset!=""})
```
- state: **ok** · 128 ms
  - scalar: `900`

#### K6.3 — v1 yolu

```promql
count(kube_replicaset_status_ready_replicas{replicaset!=""})
```
- state: **ok** · 128 ms
  - scalar: `900`

#### K6.4 — _created var mı

```promql
count(kube_replicaset_created{replicaset!=""})
```
- state: **empty** · 128 ms

### K @ `cluster-b`

#### K0.1 — skaler > 0 — matcher çalışıyor

```promql
count(kube_node_info)
```
- state: **unauthorized** — thanos rejected the cluster credentials for cluster-b (HTTP 403) · 40 ms

#### K0.2 — kube-state-metrics (+ openshift-state-metrics); ek job = ikinci KSM

```promql
count by (job) (up{job=~".*state-metrics.*"} == 1)
```
- state: **unauthorized** — thanos rejected the cluster credentials for cluster-b (HTTP 403) · 40 ms

#### K0.3 — tek sürüm satırı (boş = telemetri portu scrape edilmiyor)

```promql
count by (version) (kube_state_metrics_build_info)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K0.4 — tam 1; > 1 = kopya (dedup)

```promql
count(kube_deployment_spec_replicas) / count(count by (namespace, deployment) (kube_deployment_spec_replicas))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

- `dedup_off` state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K0.5 — 10 ⇒ 60 s scrape; 20 ⇒ 30 s

```promql
quantile(0.5, count_over_time(kube_deployment_metadata_generation[10m]))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K0.6 — < 90 s (tazelik)

```promql
time() - max(timestamp(kube_deployment_metadata_generation))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K0.6b — iki örnek ≥30 s arayla: dedup=true querier aynı scrape zamanını döndürür mü

```promql
max(timestamp(kube_deployment_metadata_generation))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K1.D — CMO'da created, annotations yok

```promql
count by (__name__) ({__name__=~"kube_deployment_(metadata_generation|status_observed_generation|spec_replicas|status_replicas|status_replicas_updated|status_replicas_available|status_replicas_ready|status_replicas_unavailable|status_condition|spec_paused|created|labels|annotations)"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K1.R — CMO'da created, metadata_generation, status_observed_generation yok

```promql
count by (__name__) ({__name__=~"kube_replicaset_(owner|created|spec_replicas|status_replicas|status_ready_replicas|metadata_generation|status_observed_generation|labels)"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K1.P — container_info + owner şart

```promql
count by (__name__) ({__name__=~"kube_pod_(container_info|owner|labels|info|created|start_time)"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K1.S — current/update_revision varsa STS rollback görünür

```promql
count by (__name__) ({__name__=~"kube_statefulset_(metadata_generation|status_observed_generation|replicas|status_replicas|status_replicas_updated|status_replicas_ready|status_replicas_available|status_replicas_current|status_current_revision|status_update_revision|created)"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K1.DS — generation + observed + desired/updated

```promql
count by (__name__) ({__name__=~"kube_daemonset_(metadata_generation|status_observed_generation|status_desired_number_scheduled|status_updated_number_scheduled|status_number_available|status_number_unavailable|status_number_ready|status_current_number_scheduled|created)"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K1.H — HPA payı (generation gürültüsü)

```promql
count by (scaletargetref_kind) (kube_horizontalpodautoscaler_info)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K1.X — PromQL'siz çapraz kontrol: metrik adları

```promql
{__name__=~"kube_(deployment|replicaset|statefulset|daemonset|replicationcontroller)_.*"}
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.1a — etiket adları: image, image_spec, image_id …

```promql
kube_pod_container_info
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.1b — namespace, replicaset, owner_kind, owner_name, owner_is_controller

```promql
kube_replicaset_owner
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.1c — condition, status (+ reason ≥ v2.17)

```promql
kube_deployment_status_condition
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.1d — namespace, statefulset, revision

```promql
kube_statefulset_status_update_revision
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.1e — label_* adları

```promql
kube_deployment_labels
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.2a — container sayısı

```promql
count(kube_pod_container_info)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.2b — image_spec dolu

```promql
count(kube_pod_container_info{image_spec!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.2c — image_id dolu

```promql
count(kube_pod_container_info{image_id!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.2d — image digest biçimli

```promql
count(kube_pod_container_info{image=~".+@sha256:.+"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.2e — image_spec digest biçimli

```promql
count(kube_pod_container_info{image_spec=~".+@sha256:.+"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.3a — enum tablosu

```promql
count by (owner_kind, owner_is_controller) (kube_replicaset_owner)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.3b — ReplicaSet, DaemonSet, StatefulSet, ReplicationController …

```promql
count by (owner_kind) (kube_pod_owner)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.3c — boş reason ⇒ KSM < v2.17

```promql
count by (condition, status, reason) (kube_deployment_status_condition == 1)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.4a — pod-template-hash

```promql
count(kube_pod_labels{label_pod_template_hash!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.4b — DS/STS revizyon kaynağı

```promql
count(kube_pod_labels{label_controller_revision_hash!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.4c — Argo Rollouts pod'ları

```promql
count(kube_pod_labels{label_rollouts_pod_template_hash!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.4d — DC pod'ları

```promql
count(kube_pod_labels{label_deploymentconfig!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.4e — eşleme ipucu

```promql
count(kube_pod_labels{label_app_kubernetes_io_instance!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.4f — eşleme ipucu

```promql
count(kube_pod_labels{label_argocd_argoproj_io_instance!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K2.4g — CMO'da 0 beklenir

```promql
count(kube_deployment_labels{label_app_kubernetes_io_instance!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K3.1 — > 1000 ⇒ v1 yolu bugün kesik

```promql
count(kube_replicaset_owner{owner_kind="Deployment"{{ns}}})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K3.2 — RS spec serisi

```promql
count(kube_replicaset_spec_replicas{{nsSel}})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K3.3 — etkin RS

```promql
count(kube_replicaset_spec_replicas{{nsSel}} > 0)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K3.4 — Deployment sayısı

```promql
count(kube_deployment_metadata_generation{{nsSel}})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K3.5 — STS sayısı

```promql
count(kube_statefulset_replicas{{nsSel}})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K3.6 — DS sayısı

```promql
count(kube_daemonset_status_desired_number_scheduled{{nsSel}})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K3.7 — > 5000 ⇒ K5 penceresi 1h

```promql
count(kube_pod_container_info{{nsSel}})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K4.1 — şu an stuck

```promql
count(kube_deployment_status_condition{condition="Progressing",status="false"} == 1)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K4.2 — controller gecikmesi

```promql
count(max by (namespace, deployment) (kube_deployment_status_observed_generation) < on (namespace, deployment) max by (namespace, deployment) (kube_deployment_metadata_generation))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K4.3 — paused

```promql
count(kube_deployment_spec_paused == 1)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K4.4 — STS rollout ortasında

```promql
count(kube_statefulset_status_update_revision unless on (namespace, statefulset, revision) kube_statefulset_status_current_revision)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K5.1 — generation artışları

```promql
sum(changes(kube_deployment_metadata_generation[{{k5w}}]))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K5.2 — ölçeklenen Deployment

```promql
count(changes(kube_deployment_spec_replicas[{{k5w}}]) > 0)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K5.3 — yeni RS

```promql
count(count by (namespace, replicaset) (kube_replicaset_owner{owner_kind="Deployment"}) unless count by (namespace, replicaset) (kube_replicaset_owner{owner_kind="Deployment"} offset {{k5w}}))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K5.4 — yeniden etkin RS (rollback adayı)

```promql
count((kube_replicaset_spec_replicas > 0) and on (namespace, replicaset) (kube_replicaset_spec_replicas offset {{k5w}} == 0))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K5.5 — STS generation artışları

```promql
sum(changes(kube_statefulset_metadata_generation[{{k5w}}]))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K5.6 — DS generation artışları

```promql
sum(changes(kube_daemonset_metadata_generation[{{k5w}}]))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K6.1 — > 1000 ⇒ v1 yolu kesik

```promql
count(kube_replicaset_owner{replicaset!="",owner_kind="Deployment"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K6.2 — v1 yolu

```promql
count(kube_replicaset_spec_replicas{replicaset!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K6.3 — v1 yolu

```promql
count(kube_replicaset_status_ready_replicas{replicaset!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### K6.4 — _created var mı

```promql
count(kube_replicaset_created{replicaset!=""})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

## Pack D

### D @ `cluster-a`

#### D1 — DC sayısı

```promql
count(openshift_deploymentconfig_spec_replicas)
```
- state: **ok** · 114 ms
  - scalar: `12`

#### D2 — etkin DC

```promql
count(openshift_deploymentconfig_spec_replicas > 0)
```
- state: **ok** · 114 ms
  - scalar: `10`

#### D3 — DC'li namespace

```promql
count(count by (namespace) (openshift_deploymentconfig_spec_replicas))
```
- state: **ok** · 114 ms
  - scalar: `4`

#### D4 — RC pod'ları

```promql
count(kube_pod_owner{owner_kind="ReplicationController"})
```
- state: **ok** · 114 ms
  - scalar: `30`

#### D5 — DENEYSEL; minimal profilde boş

```promql
count by (owner_kind) (kube_replicationcontroller_owner)
```
- state: **empty** · 114 ms

#### D6 — RC/DC metrik adları

```promql
count by (__name__) ({__name__=~"kube_replicationcontroller_.*|openshift_deploymentconfig_.*"})
```
- state: **ok** · 114 ms

  | __name__ | value |
  |---|---|
  | kube_replicationcontroller_owner | 3 |
  | openshift_deploymentconfig_metadata_generation | 3 |
  | openshift_deploymentconfig_spec_replicas | 3 |


#### D7 — DC generation artışları

```promql
sum(changes(openshift_deploymentconfig_metadata_generation[6h]))
```
- state: **ok** · 114 ms
  - scalar: `1`

#### D8 — pay = D2 ÷ (D2 + D8)

```promql
count(kube_deployment_spec_replicas > 0)
```
- state: **ok** · 114 ms
  - scalar: `150`

### D @ `cluster-b`

#### D1 — DC sayısı

```promql
count(openshift_deploymentconfig_spec_replicas)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### D2 — etkin DC

```promql
count(openshift_deploymentconfig_spec_replicas > 0)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### D3 — DC'li namespace

```promql
count(count by (namespace) (openshift_deploymentconfig_spec_replicas))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### D4 — RC pod'ları

```promql
count(kube_pod_owner{owner_kind="ReplicationController"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### D5 — DENEYSEL; minimal profilde boş

```promql
count by (owner_kind) (kube_replicationcontroller_owner)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### D6 — RC/DC metrik adları

```promql
count by (__name__) ({__name__=~"kube_replicationcontroller_.*|openshift_deploymentconfig_.*"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### D7 — DC generation artışları

```promql
sum(changes(openshift_deploymentconfig_metadata_generation[6h]))
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### D8 — pay = D2 ÷ (D2 + D8)

```promql
count(kube_deployment_spec_replicas > 0)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

## Pack R

### R @ `cluster-a`

#### R0 — boş = Argo Rollouts yok

```promql
{__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"}
```
- state: **empty** · 114 ms
  - names: (none)

#### R1 — metrik adı → sayı

```promql
count by (__name__) ({__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"})
```
- state: **empty** · 114 ms

#### R2 — Rollout sahipli RS

```promql
count(kube_replicaset_owner{owner_kind="Rollout"})
```
- state: **empty** · 114 ms

#### R3 — strateji dağılımı

```promql
count by (strategy, traffic_router, phase) (rollout_info)
```
- state: **empty** · 114 ms

### R @ `cluster-b`

#### R0 — boş = Argo Rollouts yok

```promql
{__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"}
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### R1 — metrik adı → sayı

```promql
count by (__name__) ({__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### R2 — Rollout sahipli RS

```promql
count(kube_replicaset_owner{owner_kind="Rollout"})
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

#### R3 — strateji dağılımı

```promql
count by (strategy, traffic_router, phase) (rollout_info)
```
- state: **error** (skipped) — skipped: unit unauthorized (streak) · 0 ms

### R @ `hub-1`

#### R0 — boş = Argo Rollouts yok

```promql
{__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"}
```
- state: **empty** · 114 ms
  - names: (none)

#### R1 — metrik adı → sayı

```promql
count by (__name__) ({__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"})
```
- state: **empty** · 114 ms

#### R2 — Rollout sahipli RS

```promql
count(kube_replicaset_owner{owner_kind="Rollout"})
```
- state: **empty** · 114 ms

#### R3 — strateji dağılımı

```promql
count by (strategy, traffic_router, phase) (rollout_info)
```
- state: **empty** · 114 ms

### R @ `hub-2`

#### R0 — boş = Argo Rollouts yok

```promql
{__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"}
```
- state: **empty** · 114 ms
  - names: (none)

#### R1 — metrik adı → sayı

```promql
count by (__name__) ({__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"})
```
- state: **empty** · 114 ms

#### R2 — Rollout sahipli RS

```promql
count(kube_replicaset_owner{owner_kind="Rollout"})
```
- state: **empty** · 114 ms

#### R3 — strateji dağılımı

```promql
count by (strategy, traffic_router, phase) (rollout_info)
```
- state: **empty** · 114 ms

## Pack H

### H @ `hub-1`

#### H0.1 — toplam seri (≈ 40000?)

```promql
count(argocd_app_info)
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | 40000 | |
| without matcher | 41000 | 1000 |

- `nomatch` state: **ok** · 128 ms
  - scalar: `41000`

- state: **ok** · 128 ms
  - scalar: `40000`

#### H0.2 — dedup: prometheus_replica'sız 1 satır

```promql
count by (prometheus, prometheus_replica) (argocd_app_info)
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | {<prometheus-1>/}=40000 | |
| without matcher | {<prometheus-1>/}=40000 | 0 |

- `nomatch` state: **ok** · 128 ms

  | prometheus | prometheus_replica | value |
  |---|---|---|
  | <prometheus-1> |  | 40000 |


- state: **ok** · 128 ms

  | prometheus | prometheus_replica | value |
  |---|---|---|
  | <prometheus-1> |  | 40000 |


- `dedup_off` state: **ok** · 128 ms

  | prometheus | prometheus_replica | value |
  |---|---|---|
  | <prometheus-1> | <replica-1> | 40000 |
  | <prometheus-1> | <replica-2> | 40000 |


#### H0.3 — Argo serisindeki küme etiketleri (H0.5 ile kıyasla)

```promql
count by (cluster, cluster_id, cluster_name, k8s_cluster, openshift_cluster, prometheus, tenant, tenant_id) (argocd_app_info)
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | {hub-1/hub-1/cluster-a/<prometheus-1>}=40000 | |
| without matcher | {hub-1/hub-1/cluster-a/<prometheus-1>}=40000, {/<prometheus-1>/<secret_host-1>}=1000 | n/a |

- `nomatch` state: **ok** · 128 ms

  | cluster | cluster_name | k8s_cluster | prometheus | secret_host | value |
  |---|---|---|---|---|---|
  | hub-1 | hub-1 | cluster-a | <prometheus-1> |  | 40000 |
  |  |  |  | <prometheus-1> | <secret_host-1> | 1000 |


- state: **ok** · 128 ms

  | cluster | cluster_name | k8s_cluster | prometheus | value |
  |---|---|---|---|---|
  | hub-1 | hub-1 | cluster-a | <prometheus-1> | 40000 |


#### H0.4 — tekil uygulama (= H0.1 değilse shard/replika kopyası)

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info))
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | 40000 | |
| without matcher | 41000 | 1000 |

- `nomatch` state: **ok** · 128 ms
  - scalar: `41000`

- state: **ok** · 128 ms
  - scalar: `40000`

#### H0.5 — kube_node_info'daki küme etiketleri

```promql
count by (cluster, cluster_id, cluster_name, k8s_cluster, openshift_cluster, prometheus, tenant, tenant_id) (kube_node_info)
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | {hub-1/hub-2/cluster-a/<prometheus-1>/hub-1/cluster-b}=12 | |
| without matcher | {hub-1/hub-2/cluster-a/<prometheus-1>/hub-1/cluster-b}=12 | 0 |

- `nomatch` state: **ok** · 128 ms

  | cluster | k8s_cluster | openshift_cluster | prometheus | tenant | tenant_id | value |
  |---|---|---|---|---|---|---|
  | hub-1 | hub-2 | cluster-a | <prometheus-1> | hub-1 | cluster-b | 12 |


- state: **ok** · 128 ms

  | cluster | k8s_cluster | openshift_cluster | prometheus | tenant | tenant_id | value |
  |---|---|---|---|---|---|---|
  | hub-1 | hub-2 | cluster-a | <prometheus-1> | hub-1 | cluster-b | 12 |


#### H1.1 — instance başına bir satır

```promql
count by (namespace, job) (argocd_app_info)
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | {<team-1>-prod-metrics/<team-1>-prod}=12000, {<team-2>-dev-metrics/<team-2>-dev}=9000, {<job-1>/<ns-1>}=400 | |
| without matcher | {<team-1>-prod-metrics/<team-1>-prod}=12000, {<team-2>-dev-metrics/<team-2>-dev}=9000, {<job-1>/<ns-1>}=400 | 0 rows |

- `nomatch` state: **ok** · 128 ms

  | job | namespace | value |
  |---|---|---|
  | <team-1>-prod-metrics | <team-1>-prod | 12000 |
  | <team-2>-dev-metrics | <team-2>-dev | 9000 |
  | <job-1> | <ns-1> | 400 |


- state: **ok** · 128 ms

  | job | namespace | value |
  |---|---|---|
  | <team-1>-prod-metrics | <team-1>-prod | 12000 |
  | <team-2>-dev-metrics | <team-2>-dev | 9000 |
  | <job-1> | <ns-1> | 400 |


#### H1.2 — (A) exported_namespace == namespace; (B) yok; (C) farklı

```promql
count by (namespace, exported_namespace, job) (argocd_app_info)
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | {<team-1>-prod/<team-1>-prod-metrics/<team-1>-prod}=12000, {/<team-2>-dev-metrics/<team-2>-dev}=9000, {<ns-2>/<job-1>/<ns-1>}=400 | |
| without matcher | {<team-1>-prod/<team-1>-prod-metrics/<team-1>-prod}=12000, {/<team-2>-dev-metrics/<team-2>-dev}=9000, {<ns-2>/<job-1>/<ns-1>}=400 | 0 rows |

- `nomatch` state: **ok** · 128 ms

  | exported_namespace | job | namespace | value |
  |---|---|---|---|
  | <team-1>-prod | <team-1>-prod-metrics | <team-1>-prod | 12000 |
  |  | <team-2>-dev-metrics | <team-2>-dev | 9000 |
  | <ns-2> | <job-1> | <ns-1> | 400 |


- state: **ok** · 128 ms

  | exported_namespace | job | namespace | value |
  |---|---|---|---|
  | <team-1>-prod | <team-1>-prod-metrics | <team-1>-prod | 12000 |
  |  | <team-2>-dev-metrics | <team-2>-dev | 9000 |
  | <ns-2> | <job-1> | <ns-1> | 400 |


#### H1.3a — çakışma (boş = yok)

```promql
count by (exported_namespace) (group by (namespace, exported_namespace) (argocd_app_info)) > 1
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | 0 (empty) | |
| without matcher | 0 (empty) | 0 |

- `nomatch` state: **empty** · 135 ms

- state: **empty** · 135 ms

#### H1.3b — job çakışması

```promql
count by (job) (group by (namespace, job) (argocd_app_info)) > 1
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | 0 (empty) | |
| without matcher | 0 (empty) | 0 |

- `nomatch` state: **empty** · 135 ms

- state: **empty** · 135 ms

#### H1.4 — instance başına controller shard

```promql
count by (namespace) (group by (namespace, pod) (argocd_app_info))
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | <team-1>-prod=2, <ns-1>=1, <team-2>-dev=1 | |
| without matcher | <team-1>-prod=2, <ns-1>=1, <team-2>-dev=1 | 0 rows |

- `nomatch` state: **ok** · 128 ms

  | namespace | value |
  |---|---|
  | <team-1>-prod | 2 |
  | <ns-1> | 1 |
  | <team-2>-dev | 1 |


- state: **ok** · 128 ms

  | namespace | value |
  |---|---|
  | <team-1>-prod | 2 |
  | <ns-1> | 1 |
  | <team-2>-dev | 1 |


#### H1.5 — sıfır uygulamalı instance'lar dahil

```promql
count by (namespace, job) (argocd_cluster_info)
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | {<team-1>-prod-metrics/<team-1>-prod}=12000, {<team-2>-dev-metrics/<team-2>-dev}=9000, {<job-1>/<ns-1>}=400 | |
| without matcher | {<team-1>-prod-metrics/<team-1>-prod}=12000, {<team-2>-dev-metrics/<team-2>-dev}=9000, {<job-1>/<ns-1>}=400 | 0 rows |

- `nomatch` state: **ok** · 128 ms

  | job | namespace | value |
  |---|---|---|
  | <team-1>-prod-metrics | <team-1>-prod | 12000 |
  | <team-2>-dev-metrics | <team-2>-dev | 9000 |
  | <job-1> | <ns-1> | 400 |


- state: **ok** · 128 ms

  | job | namespace | value |
  |---|---|---|
  | <team-1>-prod-metrics | <team-1>-prod | 12000 |
  | <team-2>-dev-metrics | <team-2>-dev | 9000 |
  | <job-1> | <ns-1> | 400 |


#### H1.6a — en büyük instance

```promql
max(count by (namespace) (argocd_app_info))
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | 9000 | |
| without matcher | 9000 | 0 |

- `nomatch` state: **ok** · 135 ms
  - scalar: `9000`

- state: **ok** · 135 ms
  - scalar: `9000`

#### H1.6b — en büyük shard

```promql
max(count by (namespace, dest_server) (argocd_app_info))
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | 3000 | |
| without matcher | 3000 | 0 |

- `nomatch` state: **ok** · 135 ms
  - scalar: `3000`

- state: **ok** · 135 ms
  - scalar: `3000`

#### H1.6c — 1000'i aşan shard

```promql
count(count by (namespace, dest_server) (argocd_app_info) > 1000)
```
| variant | result | Δ (without − with) |
|---|---|---|
| with matcher | 0 | |
| without matcher | 0 | 0 |

- `nomatch` state: **ok** · 135 ms
  - scalar: `0`

- state: **ok** · 135 ms
  - scalar: `0`

#### H2.1 — onlarca satır; jetonlu

```promql
count by (dest_server) (argocd_app_info)
```
- state: **ok** · 128 ms

  | dest_server | value |
  |---|---|
  | https://api.cluster-a.<domain>:6443 | 20000 |
  | https://api.cluster-b.<domain>:6443 | 18000 |
  | https://kubernetes.default.svc | 100 |
  | https://api.server-1.<domain>:6443 | 50 |
  |  | 5 |


#### H2.2 — hedef sayısı

```promql
count(group by (dest_server) (argocd_app_info))
```
- state: **ok** · 128 ms
  - scalar: `5`

#### H2.3 — destination.name ya da başarısız lookup

```promql
count(argocd_app_info{dest_server=""})
```
- state: **ok** · 128 ms
  - scalar: `5`

#### H2.4 — hub'ın kendisindeki uygulamalar

```promql
count(argocd_app_info{dest_server="https://kubernetes.default.svc"})
```
- state: **ok** · 128 ms
  - scalar: `100`

#### H2.5 — port dağılımı (port literal)

```promql
count by (port) (label_replace(group by (dest_server) (argocd_app_info), "port", "$1", "dest_server", `https?://[^/]+:([0-9]+)/?`))
```
- state: **ok** · 128 ms

  | port | value |
  |---|---|
  | 6443 | 4 |


#### H2.6 — hedef ns sayısı

```promql
count(group by (dest_server, dest_namespace) (argocd_app_info))
```
- state: **ok** · 128 ms
  - scalar: `1200`

#### H2.7 — hedef ns başına uygulama histogramı

```promql
count_values("apps_per_target_ns", count by (dest_server, dest_namespace) (group by (namespace, exported_namespace, name, dest_server, dest_namespace) (argocd_app_info)))
```
- state: **ok** · 128 ms

  | apps_per_target_ns | value |
  |---|---|
  | 1 | 3000 |
  | 2 | 500 |


#### H2.8 — dest_namespace boş

```promql
count(argocd_app_info{dest_namespace=""})
```
- state: **ok** · 128 ms
  - scalar: `3`

#### H3.1 — gerçek kopyalar

```promql
count(count by (name, dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info)) > 1)
```
- state: **ok** · 128 ms
  - scalar: `4`

#### H3.2 — histogram

```promql
count_values("same_name_same_server", count by (name, dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info)))
```
- state: **ok** · 128 ms

  | same_name_same_server | value |
  |---|---|
  | 1 | 38000 |
  | 2 | 4 |


#### H3.3 — aynı ad iki instance'ta

```promql
count(count by (name) (group by (namespace, name) (argocd_app_info)) > 1)
```
- state: **ok** · 128 ms
  - scalar: `2`

#### H3.4 — taban başına küme sayısı histogramı

```promql
count_values("clusters_per_base", count by (base) (group by (base, dest_server) (label_replace(argocd_app_info{name=~`.+-({{env}})-({{sfx}})`}, "base", "$1", "name", `(.+)-({{sfx}})`))))
```
- state: **ok** · 128 ms

  | clusters_per_base | value |
  |---|---|
  | 2 | 17000 |
  | 1 | 2000 |


#### H3.5 — çift durum uyuşmazlığı (şimdi)

```promql
count(count by (base) (group by (base, sync_status, health_status) (label_replace(argocd_app_info{name=~`.+-({{pair}})`}, "base", "$1", "name", `(.+)-({{pair}})`))) > 1)
```
- `pair:pair-1` state: **ok** · 128 ms
  - scalar: `1`

#### H4.1 — ≤ 18 satır

```promql
count by (sync_status, health_status) (group by (namespace, exported_namespace, name, sync_status, health_status) (argocd_app_info))
```
- state: **ok** · 128 ms

  | health_status | sync_status | value |
  |---|---|---|
  | Healthy | Synced | 37000 |
  | Healthy | OutOfSync | 2000 |
  | Degraded | Synced | 140 |


#### H4.2 — operasyon dağılımı

```promql
count by (operation) (argocd_app_info)
```
- state: **ok** · 128 ms

  | operation | value |
  |---|---|
  |  | 39900 |
  | sync | 100 |


#### H4.3 — etiketsiz tek satır ⇒ Argo ≤ 2.8

```promql
count by (autosync_enabled) (group by (namespace, exported_namespace, name, autosync_enabled) (argocd_app_info))
```
- state: **ok** · 128 ms

  | autosync_enabled | value |
  |---|---|
  | true | 30000 |
  | false | 10000 |


#### H4.4 — instance başına autosync

```promql
count by (namespace, autosync_enabled) (argocd_app_info)
```
- state: **ok** · 128 ms

  | autosync_enabled | namespace | value |
  |---|---|---|
  | true | <team-1>-prod | 12000 |
  | false | <team-2>-dev | 9000 |


#### H5.1a — etiket adları harfi harfine

```promql
argocd_app_info
```
- state: **ok** · 135 ms
  - names (12): autosync_enabled, dest_namespace, dest_server, exported_namespace, health_status, job, name, namespace, operation, project, repo, sync_status

#### H5.1b — etiket adları

```promql
argocd_app_sync_total
```
- state: **ok** · 135 ms
  - names (7): dest_server, exported_namespace, job, name, namespace, phase, project

#### H5.1c — etiket adları

```promql
argocd_cluster_info
```
- state: **ok** · 135 ms
  - names (5): job, k8s_version, name, namespace, server

#### H5.1d — etiket adları

```promql
argocd_app_labels
```
- state: **ok** · 135 ms
  - names (3): job, name, namespace

#### H5.3a — autosync_enabled var mı

```promql
count(argocd_app_info{autosync_enabled!=""})
```
- state: **ok** · 135 ms
  - scalar: `40000`

#### H5.3b — exported_namespace var mı

```promql
count(argocd_app_info{exported_namespace!=""})
```
- state: **ok** · 135 ms
  - scalar: `40000`

#### H5.3c — dry_run (v3.1+)

```promql
count(argocd_app_sync_total{dry_run!=""})
```
- state: **ok** · 135 ms
  - scalar: `0`

#### H5.3d — sync serisinde exported_namespace

```promql
count(argocd_app_sync_total{exported_namespace!=""})
```
- state: **ok** · 135 ms
  - scalar: `40000`

#### H5.3e — instance başına sürüm (v2.13+)

```promql
count by (namespace, version) (argocd_info)
```
- state: **ok** · 135 ms

  | namespace | version | value |
  |---|---|---|
  | <team-2>-dev | v2.11.0 | 1 |
  | <team-1>-prod | v2.13.3 | 1 |


#### H5.4a — distinct name

```promql
count(group by (name) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `38000`

#### H5.4b — distinct project

```promql
count(group by (project) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `40`

#### H5.4c — distinct repo

```promql
count(group by (repo) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `120`

#### H5.4d — distinct dest_server

```promql
count(group by (dest_server) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `5`

#### H5.4e — distinct dest_namespace

```promql
count(group by (dest_namespace) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `1100`

#### H5.4f — distinct namespace

```promql
count(group by (namespace) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `3`

#### H5.4g — distinct job

```promql
count(group by (job) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `3`

#### H6.1 — sync serisi sayısı

```promql
count(argocd_app_sync_total)
```
- state: **ok** · 128 ms
  - scalar: `39000`

#### H6.2a — saatlik sync/faz

```promql
sum by (phase) (increase(argocd_app_sync_total[1h]))
```
- state: **ok** · 135 ms

  | phase | value |
  |---|---|
  | Succeeded | 120 |
  | Failed | 3 |


#### H6.2b — 24 saatlik sync/faz (instance başına)

```promql
sum by (phase) (increase(argocd_app_sync_total{{inst}}[24h]))
```
- `inst:<team-2>-dev` state: **ok** · 135 ms

  | phase | value |
  |---|---|
  | Succeeded | 2900 |
  | Failed | 40 |


- `inst:<team-1>-prod` state: **ok** · 135 ms

  | phase | value |
  |---|---|
  | Succeeded | 2900 |
  | Failed | 40 |


#### H6.3 — autosync payı

```promql
sum by (autosync_enabled) (sum by (namespace, exported_namespace, name) (increase(argocd_app_sync_total[24h])) * on (namespace, exported_namespace, name) group_left (autosync_enabled) group by (namespace, exported_namespace, name, autosync_enabled) (argocd_app_info))
```
- state: **ok** · 128 ms

  | autosync_enabled | value |
  |---|---|
  | true | 2500 |
  | false | 440 |


#### H6.4 — sayaç sıfırlamaları

```promql
count(resets(argocd_app_sync_total[24h]) > 0)
```
- state: **ok** · 128 ms
  - scalar: `3`

#### H6.5 — saatlik geçiş

```promql
count(group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info) unless on (namespace, exported_namespace, name, sync_status, health_status, operation) group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info offset 1h))
```
- state: **ok** · 128 ms
  - scalar: `12`

#### H6.6 — 2 dakikalık geçiş

```promql
count(group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info) unless on (namespace, exported_namespace, name, sync_status, health_status, operation) group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info offset 2m))
```
- state: **ok** · 128 ms
  - scalar: `1`

#### H6.7 — kararsız küme boyu (tik başına çekim)

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info unless argocd_app_info{sync_status="Synced",health_status="Healthy",operation=""}))
```
- state: **ok** · 128 ms
  - scalar: `140`

### H @ `hub-2`

#### H0.1 — toplam seri (≈ 40000?)

```promql
count(argocd_app_info)
```
- state: **ok** · 128 ms
  - scalar: `40000`

#### H0.2 — dedup: prometheus_replica'sız 1 satır

```promql
count by (prometheus, prometheus_replica) (argocd_app_info)
```
- state: **ok** · 128 ms

  | prometheus | prometheus_replica | value |
  |---|---|---|
  | <prometheus-1> |  | 40000 |


- `dedup_off` state: **ok** · 128 ms

  | prometheus | prometheus_replica | value |
  |---|---|---|
  | <prometheus-1> | <replica-1> | 40000 |
  | <prometheus-1> | <replica-2> | 40000 |


#### H0.3 — Argo serisindeki küme etiketleri (H0.5 ile kıyasla)

```promql
count by (cluster, cluster_id, cluster_name, k8s_cluster, openshift_cluster, prometheus, tenant, tenant_id) (argocd_app_info)
```
- state: **ok** · 128 ms

  | cluster | cluster_name | k8s_cluster | prometheus | value |
  |---|---|---|---|---|
  | hub-1 | hub-1 | cluster-a | <prometheus-1> | 40000 |


#### H0.4 — tekil uygulama (= H0.1 değilse shard/replika kopyası)

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info))
```
- state: **ok** · 128 ms
  - scalar: `40000`

#### H0.5 — kube_node_info'daki küme etiketleri

```promql
count by (cluster, cluster_id, cluster_name, k8s_cluster, openshift_cluster, prometheus, tenant, tenant_id) (kube_node_info)
```
- state: **ok** · 128 ms

  | cluster | k8s_cluster | openshift_cluster | prometheus | tenant | tenant_id | value |
  |---|---|---|---|---|---|---|
  | hub-1 | hub-2 | cluster-a | <prometheus-1> | hub-1 | cluster-b | 12 |


#### H1.1 — instance başına bir satır

```promql
count by (namespace, job) (argocd_app_info)
```
- state: **ok** · 128 ms

  | job | namespace | value |
  |---|---|---|
  | <team-1>-prod-metrics | <team-1>-prod | 12000 |
  | <team-2>-dev-metrics | <team-2>-dev | 9000 |
  | <job-1> | <ns-1> | 400 |


#### H1.2 — (A) exported_namespace == namespace; (B) yok; (C) farklı

```promql
count by (namespace, exported_namespace, job) (argocd_app_info)
```
- state: **ok** · 128 ms

  | exported_namespace | job | namespace | value |
  |---|---|---|---|
  | <team-1>-prod | <team-1>-prod-metrics | <team-1>-prod | 12000 |
  |  | <team-2>-dev-metrics | <team-2>-dev | 9000 |
  | <ns-2> | <job-1> | <ns-1> | 400 |


#### H1.3a — çakışma (boş = yok)

```promql
count by (exported_namespace) (group by (namespace, exported_namespace) (argocd_app_info)) > 1
```
- state: **empty** · 135 ms

#### H1.3b — job çakışması

```promql
count by (job) (group by (namespace, job) (argocd_app_info)) > 1
```
- state: **empty** · 135 ms

#### H1.4 — instance başına controller shard

```promql
count by (namespace) (group by (namespace, pod) (argocd_app_info))
```
- state: **ok** · 128 ms

  | namespace | value |
  |---|---|
  | <team-1>-prod | 2 |
  | <ns-1> | 1 |
  | <team-2>-dev | 1 |


#### H1.5 — sıfır uygulamalı instance'lar dahil

```promql
count by (namespace, job) (argocd_cluster_info)
```
- state: **ok** · 128 ms

  | job | namespace | value |
  |---|---|---|
  | <team-1>-prod-metrics | <team-1>-prod | 12000 |
  | <team-2>-dev-metrics | <team-2>-dev | 9000 |
  | <job-1> | <ns-1> | 400 |


#### H1.6a — en büyük instance

```promql
max(count by (namespace) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `9000`

#### H1.6b — en büyük shard

```promql
max(count by (namespace, dest_server) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `3000`

#### H1.6c — 1000'i aşan shard

```promql
count(count by (namespace, dest_server) (argocd_app_info) > 1000)
```
- state: **ok** · 135 ms
  - scalar: `0`

#### H2.1 — onlarca satır; jetonlu

```promql
count by (dest_server) (argocd_app_info)
```
- state: **ok** · truncated (total 2300) · 128 ms

  | dest_server | value |
  |---|---|
  | https://api.cluster-a.<domain>:6443 | 20000 |
  | https://api.cluster-b.<domain>:6443 | 18000 |
  | https://kubernetes.default.svc | 100 |
  | https://api.server-1.<domain>:6443 | 50 |
  |  | 5 |
  | … | +2295 rows |


#### H2.2 — hedef sayısı

```promql
count(group by (dest_server) (argocd_app_info))
```
- state: **ok** · 128 ms
  - scalar: `5`

#### H2.3 — destination.name ya da başarısız lookup

```promql
count(argocd_app_info{dest_server=""})
```
- state: **ok** · 128 ms
  - scalar: `5`

#### H2.4 — hub'ın kendisindeki uygulamalar

```promql
count(argocd_app_info{dest_server="https://kubernetes.default.svc"})
```
- state: **ok** · 128 ms
  - scalar: `100`

#### H2.5 — port dağılımı (port literal)

```promql
count by (port) (label_replace(group by (dest_server) (argocd_app_info), "port", "$1", "dest_server", `https?://[^/]+:([0-9]+)/?`))
```
- state: **ok** · 128 ms

  | port | value |
  |---|---|
  | 6443 | 4 |


#### H2.6 — hedef ns sayısı

```promql
count(group by (dest_server, dest_namespace) (argocd_app_info))
```
- state: **ok** · 128 ms
  - scalar: `1200`

#### H2.7 — hedef ns başına uygulama histogramı

```promql
count_values("apps_per_target_ns", count by (dest_server, dest_namespace) (group by (namespace, exported_namespace, name, dest_server, dest_namespace) (argocd_app_info)))
```
- state: **ok** · 128 ms

  | apps_per_target_ns | value |
  |---|---|
  | 1 | 3000 |
  | 2 | 500 |


#### H2.8 — dest_namespace boş

```promql
count(argocd_app_info{dest_namespace=""})
```
- state: **ok** · 128 ms
  - scalar: `3`

#### H3.1 — gerçek kopyalar

```promql
count(count by (name, dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info)) > 1)
```
- state: **ok** · 128 ms
  - scalar: `4`

#### H3.2 — histogram

```promql
count_values("same_name_same_server", count by (name, dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info)))
```
- state: **ok** · 128 ms

  | same_name_same_server | value |
  |---|---|
  | 1 | 38000 |
  | 2 | 4 |


#### H3.3 — aynı ad iki instance'ta

```promql
count(count by (name) (group by (namespace, name) (argocd_app_info)) > 1)
```
- state: **ok** · 128 ms
  - scalar: `2`

#### H3.4 — taban başına küme sayısı histogramı

```promql
count_values("clusters_per_base", count by (base) (group by (base, dest_server) (label_replace(argocd_app_info{name=~`.+-({{env}})-({{sfx}})`}, "base", "$1", "name", `(.+)-({{sfx}})`))))
```
- state: **ok** · 128 ms

  | clusters_per_base | value |
  |---|---|
  | 2 | 17000 |
  | 1 | 2000 |


#### H3.5 — çift durum uyuşmazlığı (şimdi)

```promql
count(count by (base) (group by (base, sync_status, health_status) (label_replace(argocd_app_info{name=~`.+-({{pair}})`}, "base", "$1", "name", `(.+)-({{pair}})`))) > 1)
```
- `pair:pair-1` state: **ok** · 128 ms
  - scalar: `1`

#### H4.1 — ≤ 18 satır

```promql
count by (sync_status, health_status) (group by (namespace, exported_namespace, name, sync_status, health_status) (argocd_app_info))
```
- state: **ok** · 128 ms

  | health_status | sync_status | value |
  |---|---|---|
  | Healthy | Synced | 37000 |
  | Healthy | OutOfSync | 2000 |
  | Degraded | Synced | 140 |


#### H4.2 — operasyon dağılımı

```promql
count by (operation) (argocd_app_info)
```
- state: **ok** · 128 ms

  | operation | value |
  |---|---|
  |  | 39900 |
  | sync | 100 |


#### H4.3 — etiketsiz tek satır ⇒ Argo ≤ 2.8

```promql
count by (autosync_enabled) (group by (namespace, exported_namespace, name, autosync_enabled) (argocd_app_info))
```
- state: **ok** · 128 ms

  | autosync_enabled | value |
  |---|---|
  | true | 30000 |
  | false | 10000 |


#### H4.4 — instance başına autosync

```promql
count by (namespace, autosync_enabled) (argocd_app_info)
```
- state: **ok** · 128 ms

  | autosync_enabled | namespace | value |
  |---|---|---|
  | true | <team-1>-prod | 12000 |
  | false | <team-2>-dev | 9000 |


#### H5.1a — etiket adları harfi harfine

```promql
argocd_app_info
```
- state: **error** — bad_data: match[] is not supported by this querier · 30 ms

#### H5.1b — etiket adları

```promql
argocd_app_sync_total
```
- state: **ok** · 135 ms
  - names (7): dest_server, exported_namespace, job, name, namespace, phase, project

#### H5.1c — etiket adları

```promql
argocd_cluster_info
```
- state: **ok** · 135 ms
  - names (5): job, k8s_version, name, namespace, server

#### H5.1d — etiket adları

```promql
argocd_app_labels
```
- state: **ok** · 135 ms
  - names (3): job, name, namespace

#### H5.2a — H5.1a bad_data ise: yalnız etiket ADLARI

```promql
topk(1, argocd_app_info)
```
- state: **ok** · 135 ms
  - names (12): autosync_enabled, dest_namespace, dest_server, exported_namespace, health_status, job, name, namespace, operation, project, repo, sync_status

#### H5.3a — autosync_enabled var mı

```promql
count(argocd_app_info{autosync_enabled!=""})
```
- state: **ok** · 135 ms
  - scalar: `40000`

#### H5.3b — exported_namespace var mı

```promql
count(argocd_app_info{exported_namespace!=""})
```
- state: **ok** · 135 ms
  - scalar: `40000`

#### H5.3c — dry_run (v3.1+)

```promql
count(argocd_app_sync_total{dry_run!=""})
```
- state: **ok** · 135 ms
  - scalar: `0`

#### H5.3d — sync serisinde exported_namespace

```promql
count(argocd_app_sync_total{exported_namespace!=""})
```
- state: **ok** · 135 ms
  - scalar: `40000`

#### H5.3e — instance başına sürüm (v2.13+)

```promql
count by (namespace, version) (argocd_info)
```
- state: **ok** · 135 ms

  | namespace | version | value |
  |---|---|---|
  | <team-2>-dev | v2.11.0 | 1 |
  | <team-1>-prod | v2.13.3 | 1 |


#### H5.4a — distinct name

```promql
count(group by (name) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `38000`

#### H5.4b — distinct project

```promql
count(group by (project) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `40`

#### H5.4c — distinct repo

```promql
count(group by (repo) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `120`

#### H5.4d — distinct dest_server

```promql
count(group by (dest_server) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `5`

#### H5.4e — distinct dest_namespace

```promql
count(group by (dest_namespace) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `1100`

#### H5.4f — distinct namespace

```promql
count(group by (namespace) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `3`

#### H5.4g — distinct job

```promql
count(group by (job) (argocd_app_info))
```
- state: **ok** · 135 ms
  - scalar: `3`

#### H6.1 — sync serisi sayısı

```promql
count(argocd_app_sync_total)
```
- state: **ok** · 128 ms
  - scalar: `39000`

#### H6.2a — saatlik sync/faz

```promql
sum by (phase) (increase(argocd_app_sync_total[1h]))
```
- state: **ok** · 135 ms

  | phase | value |
  |---|---|
  | Succeeded | 120 |
  | Failed | 3 |


#### H6.2b — 24 saatlik sync/faz (instance başına)

```promql
sum by (phase) (increase(argocd_app_sync_total{{inst}}[24h]))
```
- state: **timeout** — query exceeded the 30s worker timeout · 30000 ms

#### H6.3 — autosync payı

```promql
sum by (autosync_enabled) (sum by (namespace, exported_namespace, name) (increase(argocd_app_sync_total[24h])) * on (namespace, exported_namespace, name) group_left (autosync_enabled) group by (namespace, exported_namespace, name, autosync_enabled) (argocd_app_info))
```
- state: **ok** · 128 ms

  | autosync_enabled | value |
  |---|---|
  | true | 2500 |
  | false | 440 |


#### H6.4 — sayaç sıfırlamaları

```promql
count(resets(argocd_app_sync_total[24h]) > 0)
```
- state: **ok** · 128 ms
  - scalar: `3`

#### H6.5 — saatlik geçiş

```promql
count(group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info) unless on (namespace, exported_namespace, name, sync_status, health_status, operation) group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info offset 1h))
```
- state: **ok** · 128 ms
  - scalar: `12`

#### H6.6 — 2 dakikalık geçiş

```promql
count(group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info) unless on (namespace, exported_namespace, name, sync_status, health_status, operation) group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info offset 2m))
```
- state: **ok** · 128 ms
  - scalar: `1`

#### H6.7 — kararsız küme boyu (tik başına çekim)

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info unless argocd_app_info{sync_status="Synced",health_status="Healthy",operation=""}))
```
- state: **ok** · 128 ms
  - scalar: `140`

## Pack N

### N @ `hub-1`

#### N1 — genel eşleşme oranı

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info{name=~{{RE}}})) / count(group by (namespace, exported_namespace, name) (argocd_app_info))
```
- state: **ok** · 114 ms
  - scalar: `0.93`

#### N2 — instance başına oran

```promql
(count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info{name=~{{RE}}})) or count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info)) * 0) / count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info))
```
- state: **ok** · 114 ms

  | namespace | value |
  |---|---|
  | <team-1>-prod | 0.98 |
  | <team-2>-dev | 0.9 |
  | <ns-1> | 0 |


#### N3a — bilinen env, bilinmeyen suffix

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}, name=~`.+-({{env}})-[^-]+`}))
```
- state: **ok** · 121 ms
  - scalar: `120`

#### N3b — bilinmeyen env, bilinen suffix

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}, name=~`.+-({{sfx}})`}))
```
- state: **ok** · 121 ms
  - scalar: `40`

#### N3c — dört parça ya da az

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}, name=~`[^-]+(-[^-]+){0,3}`}))
```
- state: **ok** · 121 ms
  - scalar: `300`

#### N4 — bilinmeyen son parçalar (jetonlu)

```promql
topk(20, count by (sfx) (label_replace(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}}), "sfx", "$1", "name", `.*-([^-]+)`)))
```
- state: **ok** · 114 ms

  | sfx | value |
  |---|---|
  | <sfx-1> | 300 |
  | <suffix-a> | 120 |
  | <sfx-2> | 40 |


#### N5a — dest_server başına baskın son parça

```promql
topk by (dest_server) (1, count by (dest_server, sfx) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "sfx", "$1", "name", `.*-([^-]+)`)))
```
- state: **ok** · 121 ms

  | dest_server | sfx | value |
  |---|---|---|
  | https://api.cluster-a.<domain>:6443 | <suffix-a> | 19000 |
  | https://api.cluster-b.<domain>:6443 | <suffix-b> | 17500 |


#### N5b — baskın parçanın payı

```promql
max by (dest_server) (count by (dest_server, sfx) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "sfx", "$1", "name", `.*-([^-]+)`))) / count by (dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info))
```
- state: **ok** · 121 ms

  | dest_server | value |
  |---|---|
  | https://api.cluster-b.<domain>:6443 | 0.97 |
  | https://api.cluster-a.<domain>:6443 | 0.95 |


#### N6 — bir suffix birden çok sunucuda (suffix→cluster kırılır)

```promql
count by (sfx) (group by (sfx, dest_server) (label_replace(argocd_app_info{name=~`.+-({{sfx}})`}, "sfx", "$1", "name", `.+-({{sfx}})`))) > 1
```
- state: **empty** · 114 ms

#### N7 — instance birden çok env servis ediyor

```promql
count by (namespace) (group by (namespace, env) (label_replace(argocd_app_info{name=~{{RE}}}, "env", "$1", "name", `.+-({{env}})-[^-]+`))) > 1
```
- state: **empty** · 114 ms

#### L1 — eşleşmeyen ad sayısı

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}}))
```
- state: **ok** · 114 ms
  - scalar: `2800`

#### L2 — eşleşmeyenler instance × proje

```promql
count by (namespace, project) (group by (namespace, exported_namespace, name, project) (argocd_app_info{name!~{{RE}}}))
```
- state: **ok** · 114 ms

  | namespace | project | value |
  |---|---|---|
  | <team-1>-prod | <project-1> | 2000 |
  | <ns-1> | <project-2> | 800 |


### N @ `hub-2`

#### N1 — genel eşleşme oranı

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info{name=~{{RE}}})) / count(group by (namespace, exported_namespace, name) (argocd_app_info))
```
- state: **ok** · 114 ms
  - scalar: `0.93`

#### N2 — instance başına oran

```promql
(count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info{name=~{{RE}}})) or count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info)) * 0) / count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info))
```
- state: **ok** · 114 ms

  | namespace | value |
  |---|---|
  | <team-1>-prod | 0.98 |
  | <team-2>-dev | 0.9 |
  | <ns-1> | 0 |


#### N3a — bilinen env, bilinmeyen suffix

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}, name=~`.+-({{env}})-[^-]+`}))
```
- state: **ok** · 121 ms
  - scalar: `120`

#### N3b — bilinmeyen env, bilinen suffix

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}, name=~`.+-({{sfx}})`}))
```
- state: **ok** · 121 ms
  - scalar: `40`

#### N3c — dört parça ya da az

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}, name=~`[^-]+(-[^-]+){0,3}`}))
```
- state: **ok** · 121 ms
  - scalar: `300`

#### N4 — bilinmeyen son parçalar (jetonlu)

```promql
topk(20, count by (sfx) (label_replace(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}}), "sfx", "$1", "name", `.*-([^-]+)`)))
```
- state: **ok** · 114 ms

  | sfx | value |
  |---|---|
  | <sfx-1> | 300 |
  | <suffix-a> | 120 |
  | <sfx-2> | 40 |


#### N5a — dest_server başına baskın son parça

```promql
topk by (dest_server) (1, count by (dest_server, sfx) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "sfx", "$1", "name", `.*-([^-]+)`)))
```
- state: **ok** · 121 ms

  | dest_server | sfx | value |
  |---|---|---|
  | https://api.cluster-a.<domain>:6443 | <suffix-a> | 19000 |
  | https://api.cluster-b.<domain>:6443 | <suffix-b> | 17500 |


#### N5b — baskın parçanın payı

```promql
max by (dest_server) (count by (dest_server, sfx) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "sfx", "$1", "name", `.*-([^-]+)`))) / count by (dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info))
```
- state: **ok** · 121 ms

  | dest_server | value |
  |---|---|
  | https://api.cluster-b.<domain>:6443 | 0.97 |
  | https://api.cluster-a.<domain>:6443 | 0.95 |


#### N6 — bir suffix birden çok sunucuda (suffix→cluster kırılır)

```promql
count by (sfx) (group by (sfx, dest_server) (label_replace(argocd_app_info{name=~`.+-({{sfx}})`}, "sfx", "$1", "name", `.+-({{sfx}})`))) > 1
```
- state: **empty** · 114 ms

#### N7 — instance birden çok env servis ediyor

```promql
count by (namespace) (group by (namespace, env) (label_replace(argocd_app_info{name=~{{RE}}}, "env", "$1", "name", `.+-({{env}})-[^-]+`))) > 1
```
- state: **empty** · 114 ms

#### L1 — eşleşmeyen ad sayısı

```promql
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~{{RE}}}))
```
- state: **ok** · 114 ms
  - scalar: `2800`

#### L2 — eşleşmeyenler instance × proje

```promql
count by (namespace, project) (group by (namespace, exported_namespace, name, project) (argocd_app_info{name!~{{RE}}}))
```
- state: **ok** · 114 ms

  | namespace | project | value |
  |---|---|---|
  | <team-1>-prod | <project-1> | 2000 |
  | <ns-1> | <project-2> | 800 |


## Varsayımlar V1–V14

| V | verdict | evidence | note |
|---|---|---|---|
| V1 | unknown | K1.D@cluster-a: 11 names, generation+observed present<br>K1.S@cluster-a: 10 names, generation+observed present<br>K1.DS@cluster-a: 8 names, generation+observed present<br>K1.D@cluster-b: skipped<br>K1.S@cluster-b: skipped<br>K1.DS@cluster-b: skipped | kube_<kind>_metadata_generation ve _status_observed_generation her hedefte her üç tür için |
| V2 | unknown | K2.1b@cluster-a: all label names present<br>K2.1d@cluster-a: all label names present<br>K2.3a@cluster-a: owner_is_controller rows<br>K2.1b@cluster-b: skipped<br>K2.1d@cluster-b: skipped<br>K2.3a@cluster-b: skipped | RS owner etiketleri (owner_kind, owner_name, owner_is_controller); STS revizyonu `revision` etiketinde |
| V2b | unknown | K1.S@cluster-a: current+update revision present<br>K1.S@cluster-b: skipped | kube_statefulset_status_{current,update}_revision var (CMO denylist dışı) |
| V3 | unknown | K1.R@cluster-a: owner+spec_replicas present<br>K3.1@cluster-a: 1200<br>K3.2@cluster-a: 900<br>K1.R@cluster-b: skipped<br>K3.1@cluster-b: skipped<br>K3.2@cluster-b: skipped | cluster-a: K3.1 > 1000 — v1 leg truncated today (client.go maxSeriesParsed) → dec 3 |
| V4 | unknown | K1.*@cluster-a: no _created, K6.4=0<br>K1.*/K6.4@cluster-b: skipped |  |
| V5 | unknown | K0.5@cluster-a: 10 (60 s), K0.6b equal<br>K0.5@cluster-b: skipped, K0.6b: skipped/missing | CMO scrape 60 s; iki K0.6b örneği aynı scrape zamanını döndürür |
| V6 | unknown | K0.4@cluster-a: 1<br>K0.4b@cluster-a: raw HA ratio 2 (informational)<br>K0.4@cluster-b: skipped<br>H0.2@hub-1: 1 rows, no prometheus_replica<br>H0.2@hub-2: 1 rows, no prometheus_replica | dedup=true sonrası iş yükü başına tek seri |
| V7 | unknown | K2.3c@cluster-a: 3 rows, reason=true, K0.3=v2.13.0<br>K2.3c@cluster-b: skipped |  |
| V8 | unknown | K2.4b@cluster-a: 30, DaemonSet owner rows<br>K2.4b@cluster-b: skipped | DS pod'larında label_controller_revision_hash |
| V9 | unknown | K0.2@cluster-a: single KSM, K0.6=42 s<br>K0.2@cluster-b: unauthorized | KSM taze (< 90 s) ve tek KSM |
| V10 | unknown | K5@cluster-a: gen bumps 40 : new RS 10 : scaled 6<br>cluster-b: not usable K1.H, K5.1, K5.2, K5.3, K5.4, K5.5, K5.6 | cluster-a: K5.1/K5.3 = 4 (write-volume sizing) |
| V11 | unknown | K1.D@cluster-a: status_replicas present=true<br>K5.2@cluster-a: 6 | doğrudan probe yok: status_replicas semantiği KSM sözleşmesi |
| V12 | unknown | D@cluster-a: DC share 6.2% (D2=10, D8=150), D7=1<br>cluster-b: not usable D1, D2, D3, D4, D5, D6, D7, D8 | cluster-a: DC usage material → dec 12 |
| V13 | confirmed | cluster-a: K3(ns) usable<br>cluster-b: no namespace filter (n/a) | namespace filtresi sabit |
| V14 | unknown | K2.2a@cluster-a: 900<br>K1.P/K2.1a/K2.2a@cluster-b: skipped/skipped/skipped | cluster-a: digest/tag carriers K2.2b=900, K2.2c=900, K2.2e=850 |

## Bilgilendirilen kararlar

| decision | verdict | queries |
|---|---|---|
| dec 2 | hub-1: max 40000 (fits 50k), shards > 1000: 0; hub-2: max 40000 (fits 50k), shards > 1000: 0 | H0.1, H1.6a, H1.6b, H1.6c |
| dec 3 | cluster-a: K3.1=1200 K6.1=1200 → v1 leg truncated today; cluster-b: K3.1=0 K6.1=0 → not truncated | K3.1, K6.1 |
| dec 5 / hubs[].injectClusterLabel | hub-1: H0.1 with 40000 ≠ without 41000 — injection hides Argo series; H0.3 labels [cluster,cluster_name,k8s_cluster,prometheus] vs H0.5 [cluster,k8s_cluster,openshift_cluster,prometheus,tenant,tenant_id]; hub-2: no without-matcher pass; H0.3 labels [cluster,cluster_name,k8s_cluster,prometheus] vs H0.5 [cluster,k8s_cluster,openshift_cluster,prometheus,tenant,tenant_id] | H0.1, H0.3, H0.5 |
| dec 7 | hub-1: ports [6443×4]; H3.4 2 rows; pairs [pair:pair-1=1]; N4 3 rows; N6 0 rows; hub-2: ports [6443×4]; H3.4 2 rows; pairs [pair:pair-1=1]; N4 3 rows; N6 0 rows | H2.5, H3.4, H3.5, N4, N5a, N5b, N6 |
| dec 8 | cluster-a: K0.5=10; cluster-b: K0.5 skipped | K0.5 |
| dec 9 | see V4 (unknown) | K1.*, K6.4 |
| dec 11 | R0–R3 empty everywhere → do not build the Argo Rollouts slice (cluster-a: 0, cluster-b: 0, hub-1: 0, hub-2: 0) | R0, R1, R2, R3 |
| dec 12 | DC share cluster-a: 6.2%; cluster-b: n/a | D2, D8, D7 |
| dec 15 | hub-1: autosync_enabled=false:10000 autosync_enabled=true:30000; H6.3 2 rows; hub-2: autosync_enabled=false:10000 autosync_enabled=true:30000; H6.3 2 rows | H4.3, H4.4, H6.3 |
| dec 16 | hub-1: N3b (unknown env, known suffix) 40; N7 impure instances 0 rows; hub-2: N3b (unknown env, known suffix) 40; N7 impure instances 0 rows | N3b, N7 |
| dec 26 | cluster-a: jobs [kube-state-metrics=1 openshift-state-metrics=1] version [v2.13.0]; cluster-b: jobs [] version [] | K0.2, K0.3 |
| dec 27 | clickhouse: errors [] warnings 0; cluster-a: errors [] warnings 1; cluster-b: errors [unauthorized=2] warnings 0; hub-1: errors [] warnings 0; hub-2: errors [error=1 timeout=1] warnings 0 | all |
| dec 28 | hub-1: H1.2 case A=1 B=1 C=1; H2.3 empty dest_server 5; H5.3c dry_run 0; versions [v2.11.0 v2.13.3]; hub-2: H1.2 case A=1 B=1 C=1; H2.3 empty dest_server 5; H5.3c dry_run 0; versions [v2.11.0 v2.13.3] | H1.2, H2.3, H5.3c, H5.3e |
| dec 29 | cluster-a: sampled=120000 k8s_cluster=120000 ocp_cluster=0 env_name=0; cluster-b: sampled=80000 k8s_cluster=80000 ocp_cluster=0 env_name=0; (empty): sampled=300 k8s_cluster=0 ocp_cluster=0 env_name=0; T0 fallback used: `cluster` column missing — 0011 not applied | T1, T2, T3 |
| dec 30 | skipped (metrics-only) | A, V |

## Atlananlar

- A (§11.6 Argo CD API): skipped (metrics-only)
- V (§11.7 Azure DevOps): skipped (metrics-only)
- L3 (§11.5 name listing): operator-only, never run by the probe
- cluster-b (cluster-b): K0.3–R3 skipped: unit unauthorized (streak)
- budget 300 s: not exhausted
- unit `cluster-b`: early stop — unauthorized streak after K0.2 (thanos rejected the cluster credentials for cluster-b, HTTP 403)

## Footer

- default-denied label names (values never shown): secret_host
- results per pack: T=4 K=120 D=16 R=16 H=128 N=24
- run warnings (scrubbed): PromQL warning: store cluster-a partial at https://<host>
