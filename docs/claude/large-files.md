# ≥1000 satırlık dosyalar — okuma kuralı

Üretim: 2026-11-05, `find internal cmd main*.go frontend/src docs migrations charts` + `wc -l`
(node_modules, dist, `components/shadcn/`, `*.min.*` ve ikili dosyalar hariç). Yenilemek için aynı
komutu koş; liste değişirse bu dosyayı güncelle. Kaynak: `docs/claude/token-audit.md` §4.

## Grup kuralları

| Grup | Kural |
|---|---|
| **G** — `testdata/` golden dosyaları | Asla okunmaz. Golden farkı `go test` çıktısından okunur. |
| **A** — `internal/api/api.go` | Okunmaz. Rota `route_registry.go` + `internal/api/<domain>.go`'da (`/api-route`); sembol gerekiyorsa grep. |
| **D** — `docs/*.md` (DECISIONS, INCIDENTS, …) | Başlık grep'i (`grep -n '^#' …`), sonra yalnız ilgili bölüm. |
| **T** — `*_test.go`, `*.test.ts(x)` | Test adını grep'le (`grep -n 'func Test\|it(\|describe('`), ilgili testi ±80 satır oku. |
| **S** — kaynak (`.go`, `.ts`, `.tsx`, `.css`) | Sembolü grep'le, ±80 satır oku. Bütün dosya okunmaz. |

## Liste (104)

| Yol | Satır | Kural |
|---|---:|---|
| `docs/DECISIONS.md` | 12145 | D |
| `frontend/src/lib/types.ts` | 11721 | S |
| `internal/api/api.go` | 10681 | A |
| `frontend/src/styles/globals.css` | 6263 | S |
| `internal/chstore/repo.go` | 6157 | S |
| `internal/chstore/store.go` | 5677 | S |
| `internal/api/copilot_guided.go` | 4721 | S |
| `frontend/src/lib/api.ts` | 4489 | S |
| `frontend/src/pages/AdminClickhouse.tsx` | 4321 | S |
| `internal/logstore/elasticsearch.go` | 3520 | S |
| `internal/copilot/prompts.go` | 3032 | S |
| `internal/rollout/v2probe/testdata/golden_report.md` | 2859 | G |
| `internal/devops/code.go` | 2835 | S |
| `internal/api/inbox.go` | 2597 | S |
| `main.go` | 2588 | S |
| `cmd/demo/main.go` | 2475 | S |
| `internal/notify/notify.go` | 2395 | S |
| `internal/devops/code_test.go` | 2261 | T |
| `frontend/src/pages/Traces.tsx` | 2237 | S |
| `internal/chstore/problem.go` | 2161 | S |
| `internal/thanos/client.go` | 2132 | S |
| `internal/evaluator/evaluator.go` | 2077 | S |
| `internal/mcptools/service_sweep.go` | 2061 | S |
| `internal/mcptools/pivot_trace.go` | 1887 | S |
| `internal/chstore/state_path_rebuild_test.go` | 1847 | T |
| `internal/api/copilot_chat.go` | 1847 | S |
| `internal/anomaly/anomaly.go` | 1820 | S |
| `internal/chstore/replica_repair.go` | 1742 | S |
| `internal/mcptools/tools.go` | 1716 | S |
| `internal/api/argocd_settings_routes_test.go` | 1716 | T |
| `internal/chstore/state_path_rebuild.go` | 1687 | S |
| `internal/chstore/dependencies.go` | 1675 | S |
| `frontend/src/pages/Trace.tsx` | 1655 | S |
| `frontend/src/pages/Inbox.tsx` | 1616 | S |
| `internal/rollout/v2detect_test.go` | 1611 | T |
| `internal/api/promql_console_handlers_test.go` | 1605 | T |
| `internal/chstore/spanmetric.go` | 1604 | S |
| `internal/chstore/topology.go` | 1596 | S |
| `internal/rollout/v2detect.go` | 1535 | S |
| `internal/chstore/exception_inbox.go` | 1526 | S |
| `docs/rollouts/v2-audit.md` | 1494 | D |
| `internal/api/topology.go` | 1483 | S |
| `frontend/src/pages/settings/AnomalyTab.tsx` | 1471 | S |
| `internal/mcp/mcp.go` | 1466 | S |
| `internal/mcptools/metrics_tools.go` | 1458 | S |
| `frontend/src/pages/Explore.tsx` | 1441 | S |
| `internal/chstore/cluster.go` | 1407 | S |
| `internal/copilot/copilot.go` | 1406 | S |
| `frontend/src/components/chart/CorePanel.tsx` | 1393 | S |
| `frontend/src/pages/Logs.tsx` | 1387 | S |
| `internal/api/ai_evalset_core.go` | 1375 | S |
| `internal/vmetrics/promql.go` | 1368 | S |
| `frontend/src/pages/Clusters.tsx` | 1367 | S |
| `docs/ux-parity/00-inventory.md` | 1351 | D |
| `frontend/src/features/anomalies/ProblemDetail.tsx` | 1347 | S |
| `internal/api/copilot_service_topology.go` | 1337 | S |
| `frontend/src/pages/Endpoints.tsx` | 1336 | S |
| `frontend/src/components/ai/ChatBubble.tsx` | 1335 | S |
| `internal/vmetrics/promql_test.go` | 1333 | T |
| `frontend/src/pages/service/Overview.tsx` | 1325 | S |
| `internal/mcptools/metrics_tools_test.go` | 1316 | T |
| `internal/mcptools/log_spike.go` | 1281 | S |
| `frontend/src/components/dashboard/PanelRenderer.tsx` | 1272 | S |
| `frontend/src/lib/i18n.ts` | 1270 | S |
| `internal/chstore/deploys.go` | 1259 | S |
| `internal/mcptools/guided_parity.go` | 1248 | S |
| `internal/rollout/v2worker_test.go` | 1226 | T |
| `internal/api/copilot_service_report.go` | 1213 | S |
| `cmd/demo/mesh.go` | 1211 | S |
| `internal/vmetrics/histogram_test.go` | 1192 | T |
| `frontend/src/components/ui/DataTable/DataTable.tsx` | 1179 | S |
| `internal/thanos/console.go` | 1168 | S |
| `internal/api/api_logs.go` | 1158 | S |
| `internal/api/rollouts_v2_probe.go` | 1150 | S |
| `frontend/src/pages/Services.tsx` | 1138 | S |
| `internal/argocd/metrics_worker_test.go` | 1129 | T |
| `frontend/src/components/chart/corePanelContracts.test.ts` | 1129 | T |
| `internal/rollout/reconcile_test.go` | 1123 | T |
| `docs/superpowers/plans/2026-07-17-deployment-analysis-report.md` | 1114 | D |
| `internal/sre/eval.go` | 1108 | S |
| `docs/rollouts/argo-annex.md` | 1106 | D |
| `internal/sre/hypotheses.go` | 1105 | S |
| `internal/devops/operation_card_parse.go` | 1105 | S |
| `internal/api/trace_link_identity_test.go` | 1100 | T |
| `internal/watcher/watcher.go` | 1095 | S |
| `internal/ldap/ldap.go` | 1095 | S |
| `internal/chstore/compare_periods.go` | 1091 | S |
| `frontend/src/components/viz/TimeSeriesPanel.tsx` | 1075 | S |
| `internal/api/config_iox.go` | 1070 | S |
| `frontend/src/pages/settings/ArgoCDTab.contract.test.tsx` | 1069 | T |
| `frontend/src/features/anomalies/ProblemsSection.tsx` | 1062 | S |
| `internal/config/config.go` | 1059 | S |
| `internal/chstore/problem_standard.go` | 1052 | S |
| `internal/api/rollouts_v2_probe_test.go` | 1048 | T |
| `internal/api/chat_trace_followup_loop_test.go` | 1047 | T |
| `internal/argocd/metrics_worker.go` | 1035 | S |
| `frontend/src/components/DependenciesTable.tsx` | 1033 | S |
| `internal/thanos/client_test.go` | 1028 | T |
| `internal/chstore/rollout_layer_admin_test.go` | 1024 | T |
| `internal/rollout/reconcile.go` | 1019 | S |
| `docs/audit/frontend-background-mobile-audit.md` | 1013 | D |
| `frontend/src/components/TraceWaterfall.tsx` | 1010 | S |
| `internal/api/copilot_guided_test.go` | 1009 | T |
