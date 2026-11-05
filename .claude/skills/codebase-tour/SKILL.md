---
name: codebase-tour
description: 'Coremetry dizin → amaç haritası + hangi skill. WHEN: repoya ilk bakış, "bu dizin ne yapar". WHEN NOT: belirli bir kavramın file:line yeri (/where-is), iş yapmak (ilgili skill).'
---

# /codebase-tour — dizin indeksi

Ayrıntı yok; dizini bul, ilgili skill'i aç. Kavram araması: `/where-is`.

| Dizin | Amaç | Skill |
|---|---|---|
| `main.go` | tek binary, `COREMETRY_MODE` rolleri, boot sırası | `/release` |
| `internal/otlp/` | OTLP gRPC/HTTP alımı, OTLP→CH dönüşümü | `/otel-conventions` |
| `internal/chstore/`, `migrations/` | CH yazım/okuma, şema, MV'ler, rollup'lar | `/clickhouse-schema` |
| `internal/chmigrate/` | tek düğüm → küme veri kopyası | `/clickhouse-schema` |
| `internal/logstore/` | log okuma (CH ya da ES) | `/aiops` (CountPatterns) |
| `internal/tempo/` | Tempo trace yedeği; ayar şablonu | — |
| `internal/api/` | HTTP API, `serveCached`, rota dosyaları | `/api-route` |
| `internal/evaluator/`, `internal/anomaly/`, `internal/templater/` | alarm, anomali, Drain | `/aiops` |
| `internal/correlator/`, `internal/rca/`, `internal/topology/` | korelasyon, kök neden, topoloji | `/aiops` |
| `internal/notify/` | bildirim kanalları | `/aiops` (§9 sınır) |
| `internal/auth/`, `internal/ldap/` | yerel + LDAP + OIDC kimlik | `/api-route` |
| `internal/copilot/`, `internal/ai/`, `internal/cosre/` | LLM runtime, prompt'lar (`prompts.go`), CoSRE | `/copilot-surface` |
| `internal/mcp/`, `internal/mcptools/` | kendi MCP sunucusu + tool'lar | `/mcp-tools` |
| `internal/mcpclient/` | dış MCP sunucularına istemci | — |
| `internal/sse/` | tek `/api/events` SSE veriyolu | `/frontend-design-system` |
| `internal/vmetrics/`, `internal/thanos/`, `internal/promql/` | VictoriaMetrics / Thanos / PromQL | `/clickhouse-schema` |
| `internal/oracle/`, `internal/rollout/`, `internal/argocd/` | Oracle hata tablosu, rollout'lar, Argo CD | — |
| `frontend/src/pages/`, `components/` | sayfalar, bileşenler | `/frontend-design-system` |
| `frontend/src/components/dashboard/` | dashboard paneli türleri | `/frontend-dashboard-panel` |
| `frontend/src/lib/{types,api}.ts` | tek şekil kaynağı + istemci | `/frontend-design-system` |
| `charts/coremetry/` | Helm chart | `/helm-chart-coremetry` |
| `cmd/demo/`, `jboss-demo/` | demo üreteçleri | `/where-is` (Demo realism) |
| `docs/DECISIONS.md`, `docs/INCIDENTS.md` | karar kaydı, olay hikâyeleri (başlık grep'i) | — |
| `scripts/gate.sh` | release kapı zinciri | `/release` |

Büyük dosyalar (≥1000 satır) bütün okunmaz: `docs/claude/large-files.md`.
