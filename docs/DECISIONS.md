# Decision log — architectural calls

Referenced from CLAUDE.md. Newest entries append at the bottom.

- **v0.5.208** — Tempo external trace backend as a *fallback*,
  not a replacement. Coremetry samples at low rate, Tempo holds
  100%, fallback resolves the long-tail trace-by-id.
- **v0.5.210** — P1/P2/P3 priority score blended at READ time
  (no extra column). Persisted is wasteful; fresh recompute is
  cheap and lets fresh deploys/threshold ratios re-rank
  instantly.
- **v0.5.220** — Local monolithic Tempo + 30/100 collector split
  for POC. Operators replicate this layout in prod (sample to
  Coremetry, 100% to Tempo).
- **v0.5.226 / v0.5.235** — Faceted sidebar shipped THEN dropped
  at billion-doc scale because top-10 terms aren't useful when
  the operator's value is in the long tail. Replaced with
  click-from-row filter (still in place) + significant_text +
  Drain templates.
- **v0.5.241** — Log-pattern detector consumes `logstore.Store`,
  not raw `chstore`. Decoupled detector from CH-only path so
  ES-backed installs get coverage too. ES backend batches via
  `_msearch`.
- **v0.5.244** — Drain templater is sample-based on purpose.
  Three-layer log anomaly cover: curated regex (high-priority
  known failures) + significant_text (rare tokens, unsupervised)
  + Drain templates (full shape clustering, sample-based).
- **v0.5.246-247** — Topology op view + service view share the
  same NODE / COL / ROW constants + orphan handling so the
  operator's eye doesn't recalibrate when switching tabs.
- **v0.6.0** — `COREMETRY_MODE` env var lets the single
  binary run in four roles: `all` (default, monolithic POC),
  `ingest` (OTLP receivers + CH writers), `api` (HTTP API +
  SSE + Copilot), `worker` (evaluator + anomaly + topology
  agg + notifier; replicas=1 — leader-elected). Preserves the
  single-binary pitch (one image, one tag) while letting
  banks run 5×ingest + 2×api + 1×worker at billion-spans
  scale.
- **v0.6.2** — Helm chart `deployment.mode: monolithic |
  distributed` toggle. Monolithic = unchanged behaviour from
  v0.5.x (one Deployment, replicaCount applies). Distributed
  = three Deployments + four Services (`<release>` alias →
  api, plus `-ingest`/`-api`/`-worker`). HPA targets api in
  distributed mode; worker locked at 1 replica.
- **v0.6.3** — SSE Redis pub/sub bridge. Worker-pod-fired
  events (problem.open, anomaly.fire) ride a `coremetry-
  events` Redis channel so every api pod's local SSE
  subscribers receive them. PodID-stamped envelopes prevent
  loops; 200ms publish deadline so a wedged Redis doesn't
  stall the evaluator. Single-pod / Noop-cache installs are
  unchanged (no Redis activity).
- **v0.6.4-v0.6.7** — Model Context Protocol server. JSON-RPC
  2.0 over HTTP+SSE per spec 2024-11-05. Exposes tools (7
  telemetry surfaces in `internal/mcptools/`), resources
  (URI-addressed snapshots + templated per-id reads), and
  prompts (curated system+user message pairs that surface the
  in-app ✨ Explain workflows). Auth via existing JWT
  middleware — viewer/editor/admin roles carry into MCP. Runs
  on api+all modes only (worker/ingest pods don't take
  operator traffic).
- **v0.6.8** — AI-driven CH query optimizer on
  `/admin/clickhouse`. Operator pastes SQL, Copilot rewrites
  it against the MV catalogue + six-rule checklist (MV bypass
  / LIMIT / max_execution_time / time-bounded WHERE / GLOBAL
  IN / quantileTDigest defaults). Suggestion only — no auto-
  run; operator copies the optimized SQL to their CH client.
  Routes through `s.copilotExplain` so every call writes an
  ai_calls row for /ai attribution.

## v0.7 → v0.10.629 — karar kaydı (2026-05-29 → 2026-09-10)

Temaya göre gruplu, tema içinde sürüm sırasıyla. Git mesajı olan
kararlar sürüm etiketiyle anılır; yalnız operatör notundan bilinenler
"(kaynak: operatör notu, tarih)" ile işaretli. Müşteri/kurum adı,
alan adı, gerçek şema/tablo/kolon ve iş yükü adları bu dosyaya
girmez — jenerik ad kullanılır.

### Depolama / ClickHouse

- **v0.9.385-387 + `migrations/0001-0003`** — Rollup katmanı iki
  aile: DAR (service · kind · status, tdigest, 10s→1m→5m→1h) hızlı
  overview için; GENİŞ (endpoint · channel_code · function_code,
  20 kovalı eksponansiyel sayaç, 1m→5m→1h) breakdown için; metrik
  zinciri ayrı (0003). Okuma `PickRollup` karar tablosuyla step'i
  tam bölen en kaba kademeye iner; pencere başı tablonun en eski
  verisinden eskiyse ham yola düşer (kapsama dürüstlüğü), tablo
  yoksa sessizce ham yol. Reddedilen: DDL'in boot'ta otomatik
  koşması — ON CLUSTER DDL'i N pod yarıştırınca dağıtık kuyruk
  tıkanır (v0.9.613 vakası), sahibi operatör. `spanmetrics_*`
  ailesinin emekliliği v0.9.428'de RESMEN ertelendi (ölçüt: rollup
  prod'da ≥30 g + resolver okumaları eşit/hızlı + 0004 backfill).
  Kod: `internal/chstore/rollupselect.go`, `rollup_read.go`,
  `rollup_fastpath.go`.
- **v0.9.770 + 2026-08-09 A/B** — Rollup kurulumu UI sihirbazından
  (/admin/clickhouse → "Rollup katmanı": durum → ön kontrol →
  kur → 5 dk write_failed bekçisi → geri al); yine boot'ta ASLA
  koşmaz, tek tetik admin'in düğmesi. Aynı hafta ölçülen A/B
  (9 koşu, query_log medyanı, iki bağımsız tekrar): GENİŞ ailenin
  ORDER BY'ında `ts` sonda olduğu için servis+zaman okuması zaman
  budaması yapamıyor ve aile ≈1.02:1 sıkışıyor → **0002+0005 prod
  kurulumu ÖNERİLMEZ**; geniş aile ancak ORDER BY yeniden
  tasarımıyla (ayrı bilinçli karar) değer üretir. Kod:
  `frontend/src/pages/AdminClickhouse.tsx`,
  `migrations/0002_rollup_wide.sql` (kaynak: operatör notu,
  2026-08-09).
- **v0.9.1308 + `migrations/0009_state_unify.sql`** — State
  tabloları (problems, users, alert_rules, dashboards, …) tek
  replikasyon grubunda: ZK yolunda `{shard}` yok, yol koda gömülü
  değil kümeden okunuyor (`resolveStateReplicaPaths` — komşular
  hangi yolu tutuyorsa o), 37 state tablosu negatif türetimle
  bulunuyor (shard kayıtlarında adı geçmeyen = state). Sebep:
  `Replicated*` ama Distributed sarmalayıcısız tablolar failover'da
  bağlanılan host'a göre ayrışıyordu (prod: problems 633k / 4k iki
  shard'da). `ReplacingMergeTree(version)` + `FINAL` sözleşmesi
  göçü güvenli kıldı: iki shard'ın bağımsız ürettiği aynı
  deterministik id'ler `version` ile birleşti (prod 635.864,
  birleşim aritmetiği oturdu). Reddedilen: uygulamayı tek host'a
  pinlemek (yeni bölünmeyi durdurur, ayrışmayı onarmaz); ayrı
  1×N replica küme tanımı (ZK yolu yerinde değişemediği için
  iki kat iş). Kod: `internal/chstore/state_unify.go`.
- **v0.9.1139 · v0.10.247 · v0.10.561** — Kullanıcı durumu için
  yeni şema yok, `saved_views(page='<kind>')` tek tablo (invariant
  #5): sohbet kalıcılığı `ai-chat` blobu (1139), kişisel tablo
  tercihleri `page='table:<key>', id='pref:<uid>:<key>'` doğal
  upsert + `name=''` tombstone (247), 90 günlük sohbet arşivi
  süpürücüsü (561). Reddedilen: yüzey başına tablo. Kod:
  `internal/api/preferences_routes.go`.
- **v0.10.181** — Operatör kararı deposu `anomaly_verdicts`: düşük
  hacimli operatör state'i için şablon — `ReplacingMergeTree
  (version)`, ORDER BY event_id (olay başına SON karar), partition
  YOK, `FINAL` okuma, TTL 180 g, küme kipinde otomatik Replicated
  (`anomaly_silences` ile aynı sınıf). "Değil" artık yalnız
  susturma değil, kayıtlı karar; "Anomali" kararının deposu ilk
  kez var. Kod: `internal/chstore/anomaly_verdict.go`,
  `internal/api/anomaly_verdicts.go`.
- **v0.9.1150 → v0.10.601 (şablon)** — Her dış kaynak/ayar
  `system_settings` JSON blobu + `LoadPersisted` boot'ta + 30 s
  yenileme + maskeli `Snapshot` (Tempo şablonu,
  `internal/tempo/client.go`): `victoria_metrics` (v0.9.1150),
  `mcp_client_servers` (v0.10.87), `influx_sources` (v0.10.222),
  `oracle_sources` + `oracle_poll:<id>` watermark (v0.10.580/601).
  Sır sözleşmesi tek: blob'da düz token SAKLANIR, GET asla geri
  vermez (`hasToken`), boş girdi saklıyı korur, rotasyon = yenisini
  yapıştır; `env:`/`file:` referansı OPSİYONEL ve doluysa kazanır
  (`internal/secretref`, v0.10.271-273; Tempo/Thanos/VM'e yayıldı,
  ES/SMTP/AI/LDAP bilinçli kapsam dışı). Reddedilen: "yalnız secret
  referansı, düz token reddedilir" (v0.10.222-223 arasında yaşadı;
  operatör "saklanabilir, maskeli yaparsın", 2026-09-01). Kod:
  `internal/chstore/vmetrics.go`, `internal/secretref/`.

### Ingest / OTLP

- **v0.8.73** — Binary içi head+tail sampler ve Settings "Trace
  sampling" sekmesi tamamen söküldü (`internal/sampling/` yok);
  Coremetry ALDIĞI span'lerin %100'ünü saklar, örnekleme istenirse
  collector'da (v0.5.208/220 sample-to-Coremetry / 100%-to-Tempo
  ayrımı aynen). Ingest sıcak yolundan span başına bir karar ve bir
  ayar katmanı gitti; geriye dönük şim yok (eski `sampling:` bloğu
  sessizce yok sayılır). KALAN ve karıştırılmaması gereken:
  pipeline `KindSample` drop kuralı, self-obs SDK sampler'ı, okuma
  zamanı downsampling (heatmap/scatter). Kod:
  `internal/otlp/http.go` (addSpan → pipeline → store).
- **v0.9.797** — Dışlama mimarisi: ingest drop'un TEK sahibi
  `internal/pipeline` motoru (AcceptSpan/Log/Metric üçü OTLP
  hattına bağlı; kurallar `system_settings`, UI Settings →
  Pipeline). Span için OKUMA-zamanı route filtresi bilinçli YOK:
  dar rollup ve `operation_summary_5m` route bilmiyor, filtre
  entry-RED'i ham taramaya düşürür ve KPI fallback'i dışlanmamış
  sayı gösterirdi. Metrik için okuma-zamanı `metric_exclusions`
  var (kural seti cache anahtarına FNV özetiyle girer, kural yokken
  SQL bayt-bayt eski). Müşteri path desenleri repoya ASLA —
  operatör Settings'ten girer. Kod: `internal/pipeline/pipeline.go`,
  `internal/otlp/http.go` (kaynak: operatör notu, 2026-08-09).
- **v0.9.240 + 2026-07-25 direktifi** — Giriş-span ilkesi: bir
  servisin RED metrikleri yalnız `kind IN ('server','consumer')`
  giriş span'lerinden hesaplanır ("aynı Dynatrace gibi");
  client/producer/internal span'ler servisin YAPTIĞI çağrılardır,
  ölçüsü değil. Latency 240'ta, throughput+error operatör onayıyla
  çekildi (ölçülen etki: demo p50 60→807 ms, throughput 8× düşer,
  error rate 5× çıkar; prod latency SLI %99.99→%99.85, alarm
  fırtınası yok). Tuzak: `service_summary_5m`'de `kind` boyutu
  yok ve ORDER BY değişemez → çözüm boyut eklemek değil
  `entry_*_state` kolonları. Giriş span'i olmayan servis için "tüm
  span'lere düş + nedeni yaz" fallback'i şart. Kod:
  `frontend/src/lib/entrySpans.ts`, `internal/chstore/slo.go`
  (kaynak: operatör notu, 2026-07-25).
- **Sürekli (operatör kararı)** — PII/veri redaction özelliği YOK:
  attribute, log gövdesi, DB ifadesi, trace yükü hiçbir yerde
  maskelenmez/hash'lenmez/tokenize edilmez. Gerekçe: olay
  incelemesinde ham adli veri gerekir; erişim denetimi çevrede
  (roller, audit, depolama şifrelemesi) yaşar, okuma anında
  mutasyon değil. Reddedilen: field-level encryption, DLP,
  "configure off" toggle'lı redaction. Kod: `internal/otlp/
  convert.go` attribute'ları verbatim taşır (kaynak: operatör
  notu, "redact etme asla").

### Metrikler / VictoriaMetrics

- **v0.9.1150 (+1151)** — VictoriaMetrics OKUMA backend'i, Settings
  toggle'ıyla (env değil): açıkken katalog/picker,
  `/api/metrics/query` (Explore + dashboard + MCP), etiket
  değerleri VM'den; kapalıyken CH yolu bayt-bayt aynı. İki katı
  kural: **sessiz CH fallback YOK** (VM açık+erişilemez → 502;
  metrikte SAYILAR değişir ve ekran bunu söyleyemez — Tempo
  fallback'inden bu yüzden farklı) ve **400 ≠ 502** (çevrilemeyen
  sorgu 400 + desteklenen küme; transport/VM hatası 502 — sağlıklı
  VM'e "bozuk" dedirtmeyiz). Cache anahtarları `src=` backend
  işareti taşır (v0.5.187 sınıfının ayar-girdili hâli). 1151:
  `?metricsrc=vm|ch` deneme parametresi (Enabled şart değil, UI
  yazmaz). Span-türevi her şey + `/api/metrics/resolve` CH'de.
  Reddedilen: `internal/thanos`'u ortak çekirdeğe göçürmek —
  promapi kopya-desen, Thanos/clusters ayrı kalır. Kod:
  `internal/api/metricsource.go` (seam), `internal/vmetrics/`,
  `internal/promapi/`.
- **v0.9.1154-1160** — Çeviri dili MetricsQL (PromQL süperseti;
  operatör düzeltmesi 2026-08-17): rate/last 300 s pencere tabanı,
  `increase` ASLA taban almaz (v0.6.36 birim-ölçek sınıfı); `last`
  groupBy'da `max` ile çöker (CH argMax sınıfı); ad eşleyici OTel
  noktalı adı Prometheus yazımına PROBE ile değil aday
  alternation'ıyla eşler (varlık ≠ canlılık — bayat aile probe'u
  kandırır); OTLP histogramının taban serisi olmadığı için
  avg/rate/increase `_sum`/`_count` kollarıyla MetricsQL `or`
  kompozisyonu (operatör kararı, ilk kesimi değiştirdi: sol kol
  kazanır, alternation'ın çift sayma bedeli kalktı).
  `ValidatePromQL` asimetrik: CH parse eder, VM nil (üst kümeyi
  motorundan katı reddetmek "bozuk uç" gibi görünür). Kod:
  `internal/vmetrics/promql.go`, `names.go`, `histogram.go`.
- **2026-09-02 kararı → v0.10.292-294 (+365/366/374)** —
  **VictoriaMetrics TEK metrik deposu** (operatör: "VM'i ANA metrik
  deposu olarak konumlandırmak istiyorum", 2026-09-04; audit
  onaylı). OTLP metrik gövdesi HAM hâliyle VM'e forward edilir
  (`/opentelemetry/v1/metrics`; gzip aynen, JSON→protobuf; CH
  modelinden GERİ ÜRETİLMEZ çünkü CH modeli kayıplı), ayrı bayt
  bütçeli kuyruk, OTLP cevabını asla bekletmez, varsayılan KAPALI;
  `/api/metrics/compare` kıyas ucu; sabit-adlı okuyucular
  (hosts/infra, DB kapasite, JVM pod) `metricSource` seam'ine
  taşındı, `dql.go` bilinçli CH-çakılı. **AÇIK DURUM
  (2026-09-10):** ClickHouse `metric_points` yazımı HÂLÂ sürüyor —
  Dilim 5 (CH yazımını kapat, ölü `spanmetrics_*_5m` sil) ön koşulu
  prod çift yazım + compare sıfır fark, operatörde; VM OSS olduğu
  için 13 aylık saatlik rollup ufkunun VM karşılığı yok (ufuk VM
  retention'ına iner ya da CH rollup dondurulur — karar bekliyor).
  Ekip-hazırlık denetimi bunu "kural aspirasyonel, kod ÇİFT
  yazıyor" (B3) diye kaydetti. Thanos: remote_write tercih, olmazsa
  ayrı kaynak. 3b-2/3b-3 (db receiver keşfi + motor panelleri)
  PARK: "db receiver kullanmıyorum" (2026-09-05). Kod:
  `internal/otlp/forward.go`, `internal/vmetrics/write.go`,
  `internal/api/metrics_routes.go`,
  `docs/audit/vm-metrics-migration.md`.
- **v0.10.222-231 → v0.10.606** — InfluxDB 2.x dış hata serisi
  entegrasyonu eklendi (audit onayı 2026-09-01: kaynak yönetimi,
  1 dk kovalı poller → `metric_points` `ext:<sorgu>`, kind=external
  anomali, kanıt zinciri, Problem paneli, mevsimsel baseline) ve
  operatör "influxdb ile işimiz kalmadı" deyince Oracle Aşama 2
  gemiye çıktıktan SONRA tek sürümde söküldü (2026-09-10; şim yok,
  `influx_*` blobları okuyansız kaldı). Kalıcı olan kararlar: dış
  seri için AYRI tablo değil `metric_points` + `exemplars` (K1);
  istemci kütüphanesi değil net/http + annotated CSV (K2); seri
  kova bitiş zamanına yazılır, "operatörün Grafana'sıyla aynı
  sayı" ilkesi (v0.10.224). `ExternalScanner`/kind=external hattı
  KALDI — Oracle onun üstüne kurulu. Kod (kalan):
  `internal/anomaly/external.go`, `internal/secretref/`.

### Loglar / Elasticsearch

- **v0.7.14** — Collector logları Elasticsearch'e ÇİFT gönderir
  (Coremetry OTLP export'una ek); CH yazma deposu kalır, ES yalnız
  OKUMA backend'i (invariant #2), `COREMETRY_LOGS_BACKEND` ile
  seçilir. `logstore.Switchable` iki backend'i BİRLEŞTİRMEZ,
  aralarında SEÇER — prod ES'teyken CH `logs`'a yazılan satır hiç
  görünmez (Oracle satırlarının ayrı tabloya gitmesinin nedeni,
  aşağıda). Kod: `internal/logstore/switchable.go`, chart
  `otelCollector.elasticsearch.*`.
- **v0.8.8 · v0.8.139-140 · v0.8.270 (+2026-06-15 direktifi)** — ES
  maliyet disiplini: Live Patterns + Similar Traces silindi
  (`significant_text` 30 s poll'da, v0.8.3 CPU olayı; −1120
  satır); her ES okuma `serveCached` (singleflight + SWR);
  `track_total_hits` tavanı + soft timeout; canlı kuyruk tiki
  5→10 s; log-anomali `?window=` cache anahtarında sınırsız
  gezmesin diye {1m,5m,15m,30m} basamağına sabitlendi → uç başına
  en fazla 4 `_msearch`/TTL. Operatör: "Elastic üzerinde yoğun api
  istekleri olsun istemiyorum" — sayı VE sıklık; fetch yalnız
  expand/open'da, liste prefetch'i yok, staleTime ≥ sunucu TTL.
  Kod: `internal/api/cache.go`,
  `internal/api/log_patterns_explain.go` (kaynak: operatör notu,
  2026-06-15).
- **v0.10.566** — İstek kimliği (request_id) YALNIZ log gövde
  metninde yaşar; yapılandırılmış alan EKLENMEZ (operatör:
  ingest'i değiştirmek istemiyor; `LogRecord.Attributes` hiçbir
  backend'de dolmuyor). Dış log platformu linki: gövdede
  request_id varsa onunla, yoksa span attribute yolu (function_id
  + channel_code birlikte); kazanan span sırası seçili → ilk
  hatalı → root. ES bedeli (10B doc/gün) yüzünden okuma tavanlı.
  Kod: `internal/reqid/`, `internal/api/trace_link_identity.go`.
- **v0.10.580-603** — Kurumun Oracle hata tablosu → Problem hattı,
  Aşama 1-2. Sürücü **go-ora** (saf Go): Dockerfile `CGO_ENABLED=0`
  + alpine/musl, godror Instant Client ister ve musl için
  yayınlanmaz — "tek binary, tek imaj" kısıtı sürücüyü seçti.
  Şema/tablo/kolon adları ve tip süzgeci KODA GÖMÜLMEZ, ayardan
  (`Columns{alan: KOLON}`, `-` = alan tabloda yok; identifier
  regex'i tırnaksız SQL'e girer, değerler daima bind); zaman dilimi
  IANA (varsayılan Europe/Istanbul) + "kolon dilimli" anahtarı,
  `time/tzdata` gömülü (alpine'de zoneinfo yok). Tüketim modeli
  periyodik ingest (yalnız `COREMETRY_MODE=worker|all` lideri,
  `cache.LeaderHolder` 30 s kira), watermark `system_settings`'te
  kalıcı ve yalnız yazım başarılıysa ilerler; yeniden görülen satır
  aynı `row_id` → RMT'de tek satır. Satırlar CH `logs`'a DEĞİL
  kendi RMT tablosuna yazılır ve Trace Logs sekmesinde üçüncü dizi
  olarak (`/api/oracle/errors`, origin `oracle`) birleşir (audit
  B4). Bağlantı testi 200 + `{ok:false}`; dar pencere boşsa 24 s
  geniş ikinci deneme ("hata yok + satır yok" üç sorunu
  ayırt edemiyordu — Influx bunu prod'da öğretti). Aşama 3 (shadow
  Problem) açık. Kod: `internal/oracle/`,
  `internal/api/oracle_logs_routes.go`,
  `frontend/src/pages/settings/oracleForm.ts`,
  `docs/audit/oracle-error-log-2026-09-09.md`.

### CoSRE / AI / MCP

- **v0.8.374 + v0.8.397-398** — Copilot yerel küçük model
  (air-gapped kurum, 2B sınıfı; gemma4, 2026-07-21) için tasarlanır:
  veri sunucuda deterministik PREFETCH edilir, model yalnız ANLATIR
  (analyze-service deseni; guided chat = deterministik niyet
  router'ı + prefetch→anlatım); çok turlu tool döngüsü ve katı JSON
  şeması şüpheli. Her düzyazı yüzeyi Türkçe (`AnswerInTurkish`
  13 prompt'a; yapılandırılmış çıktı prompt'ları bilinçli
  dışarıda). Harici LLM API'si yok, dış ajan tüketicisi yok — MCP
  ToolList'in değeri in-app function-calling. Kod:
  `internal/copilot/`, `internal/api/copilot_guided.go` (kaynak:
  operatör notu, 2026-07-21).
- **v0.8.468 + Faz 1.6 → v0.9.1232** — AI çağrı disiplini: TÜM
  sistem prompt'ları `internal/copilot/prompts.go`'da (sicil +
  dil kapısı `prompt_language_test.go`; sicilde olmak doğru sınıfta
  olmak değil, satır-içi ekler de sicil dışı); her Explain
  `s.copilotExplain(r, …)` üzerinden ki `ai_calls` satırı düşsün ve
  `/ai` atfı dürüst kalsın — `s.copilot.Explain` doğrudan
  çağrılmaz. Erişilemez ve pahalı filo-geneli tek-atış
  SystemAnalysis (v0.8.75) silindi; tezi odaklı yüzeylerde yaşıyor
  (Problems Explain, RootCausePanel). Kod:
  `internal/api/ai_observability.go` (`copilotExplain`),
  `internal/copilot/prompts.go`.
- **v0.9.477 → v0.10.461/483** — TEK AI çekmecesi: inline Explain
  panelleri v0.9.477'de çekmeceye toplandı; operatör üç kez
  "Explain ile CoSRE iki ayrı drawer" deyince 461 kabuğu
  (genişlik 620, tek cevap kartı sınıfı), 483 bileşeni eşitledi:
  `AIDrawer` gövdesi `AIDrawerBody`'ye, `CopilotChat` `?ai=`
  öznesini kendisi okuyup aynı kabukta açıklama kipine geçer;
  AppShell yalnız CopilotChat mount eder, `AIDrawer` ince
  sarmalayıcı. Reddedilen: iki bileşen, iki başlık anatomisi. Kod:
  `frontend/src/components/CopilotChat.tsx`,
  `frontend/src/components/ai/AIDrawer.tsx`.
- **v0.10.172 + v0.10.194** — Serbest soru için kademe 3.5: router
  → çekmece → RAG eşleşmezse küçük modele TEK katı-JSON niyet
  sınıflandırma çağrısı (slotlar canlı listeye doğrulanır,
  adlandırılmış ama eşleşmeyen → none); eşleşirse mevcut
  prefetch→anlatım yolu. Kip `intentClassify` off | on |
  **on_no_loop (varsayılan)** — operatör "RAG'a gitmesine gerek
  yok"; 194'te `none` artık cevapsız değil: tool'suz TEK genel
  anlatım (`chat-general`, "telemetriyle eşleşmedi — genel
  bilgiyle" notu, KB adayı OLAMAZ). Reddedilen: serbest tool
  döngüsüne düşmek (2B modelde "schema soup"). Kod:
  `internal/api/copilot_intent.go`, `copilot_guided.go`,
  `internal/copilot/copilot.go` (IntentClassifyMode).
- **v0.9.1136** — MCP yetki: `MinRole` kayıt-defteri metadata'sı +
  tek `CallGate` (tools/call + resources/read + prompts/get; route
  tablosu yok); sıra rol → sonra rate (rol reddi bütçe tüketmez);
  tanınmayan MinRole FAIL-CLOSED; `cmk_` token kendi rolünü taşır,
  kişi-bazlı scoping yok. A7: REST↔MCP sapması sıfır olsun diye
  `GET /api/anomalies/active` editor kapısı viewer'a indi (okuma
  kapısı hiçbir şeyi korumuyordu, viewer'ın Cmd-K paletini
  bozuyordu). Yazma tool'u yok; gelirse MinRole ≥ editor + audit.
  Kod: `internal/api/mcp_gate.go`, `internal/mcp/`.
- **v0.10.86-89** — Dış MCP İSTEMCİSİ (Coremetry o güne dek yalnız
  sunucuydu): operatör izin listesi `system_settings`
  `mcp_client_servers` (8 sunucu tavanı, 200 tool katalog tavanı),
  tool başına allow/deny köprüde (deny kazanır, `*` önek; süzülen
  tool modele HİÇ görünmez), her çağrı audit `mcp.call`, `[dış:
  <sunucu>]` etiketi, önek `<sunucu>__<tool>` (çakışma politikayla
  değil biçimle imkânsız), tekrar muhafızı tüm tool'lara, deponun
  ilk giden-istemci span'leri. Bilinçli sınır: dış tool'lar YALNIZ
  serbest sohbet döngüsünde (guided/drawer/RAG'a sızmaz); MinRole
  viewer (rol merdiveni Coremetry verisinin). Kod:
  `internal/mcpclient/`, `internal/api/chat_mcp_bridge.go`.

### Anomali / Problems

- **v0.9.487 · v0.9.611 · v0.9.612** — P1 SEYREK kalmalı:
  paylaşılan patlama eşiği 10 servis (operatörün prod deneyimi;
  8→4 tahminleri yanlıştı); taze deploy tetikleyicisi önceliğe
  KARIŞMAZ (deploy sıklığı yüksek, her dağıtım penceresi P1
  üretiyordu) — deploy bilgisi ProblemDetail'de görünür, sıraya
  sokmaz; Inbox varsayılanı yalnız P1, exception-dışı türler
  inbox'ta HEP P3 (görünüm önceliği; evaluator/bildirim değişmez).
  P1 kapıları problemin KENDİ şiddetinden türer (2× eşik, 4+ saat
  açık); öncelik hâlâ okuma anında türetilir, yazılabilir alan yok
  (v0.5.210). Kod: `internal/chstore/problem.go` computePriority,
  `frontend/src/pages/Inbox.tsx`.
- **v0.9.1205** — P1'i HAK ETMİŞ exception grubu ele alınana dek
  (resolve/ignore) P1 KALIR; aynı sınıfın beşinci bildirimiydi
  (627→699→775→1189) — dört düzeltme "şiddet olgu, aciliyet
  zamanla düşer" ilkesini koruyup pencereyi oynatmıştı, operatör
  dördünde de aynı duvara çarptı. Patlama koşulsuz P1; yalnız
  kronik damlama basamaklara iner; P2/P3 eskisi gibi zamanla
  düşer. Kod: `internal/chstore/problem.go` (6 test direktife
  çevrildi).
- **v0.10.543** — `service_silent` dedektörü VARSAYILAN KAPALI
  (operatör: "Anomali service silent'lara ihtiyacım yok"); prod
  /incidents 100+ açık CRITICAL doluydu, span kesintisi bu filoda
  arıza değil. Kapalıyken açık kalanlar tikte çözülür. "Sinyal
  kaybı = critical" çıkarımı yapan yeni dedektör eklenmez
  (evaluator'ın "source silent" resolve gerekçesi ayrı, kalır).
  Kod: `internal/chstore/anomaly_sensitivity.go` (ServiceSilent).
- **v0.10.228 · 587 · 588 · 597** — Dış hat TEK tarayıcı:
  `ExternalScanner` `metric_points`'teki `ext:*` serilerini MEVCUT
  `evaluateAnomaly` çekirdeğine verir (MAD/dwell/z, aynı hassasiyet
  blobu), Problem `kind=external`, özne `ext:<kaynak>/<grup
  değerleri>`; kaynak başına ikinci tarayıcı YAZILMAZ — sözleşme
  "seriyi şu tabloya yaz, ben okurum" (Oracle audit B1). Tarama
  yalnız BAŞARILI poll sonrası (sıfır-padli sahte iyileşme yok).
  587 tik başına açılış tavanı (iki fazlı: önce değerlendir, sonra
  z'ye göre en güçlü N; refresh/resolve etkilenmez); 588 üç ardışık
  poll hatası = "kaynağa erişilemiyor" Problem'i (veri yokluğu ≠
  hata yokluğu); 597 aynı ilk boyutta ≥3 taze açılış tek küme
  Problem'i. Kod: `internal/anomaly/external.go`.
- **v0.10.592 + v0.10.605** — Poller-sahipli Problem'lerin yaşam
  döngüsü: evaluator'ın bayat süpürmesi (updated_at < 3×interval)
  `anomaly:ext-down:` / `anomaly:ext-cap:` kurallarını atlar — tek
  karar noktası `chstore.PollerOwnedRule` (önek sabitleri
  chstore'da; evaluator ve anomaly birbirini import etmez, ikisi
  chstore'u eder); seri Problem'leri BİLEREK süpürülür ("source
  silent" dürüst sinyal). 605: muafiyet yalnız YAŞAYAN kaynak için
  (`SetPollerSourceLive`) — kaynak silinince/kapatılınca Problem'i
  resolve edecek kimse kalmıyordu, Influx sökümü bunu ortaya
  çıkardı. Karar saf `staleSweepCandidates`, kablolama
  kaynak-pinli. Kod: `internal/chstore/problem.go:1439`,
  `internal/evaluator/evaluator.go`.

### Frontend

- **v0.7.55-65 + v0.8.306** — Her veri tablosu tek primitif:
  `useDataTable` (sort + resize, `storageKey` ile kalıcı genişlik,
  COLS `sortValue`/`numeric`) + `DataTable`; sunucu-sayfalı
  tablolar yalnız resize yarısını alır; >100 satır
  `content-visibility`. Elle sıralama yazan son tablolar 8.306'da
  göçtü. Kod: `frontend/src/components/ui/DataTable/DataTable.tsx`.
- **v0.8.253 (+v0.9.937)** — URL = seçimlerin tek kaynağı
  (filtre/çekmece/odak/sekme/aralık); sayfa `replace:true` ile
  yazar, URL→state içe aktarımı sig-guard'lı (bir range
  değişikliği yerel filtreleri siliyordu; tek-yönlü okuma tekrar
  eden bug sınıfı: 256/265/267). 937 v0.8.409'un "custom aralık
  asla kalıcı olmaz" kuralını TERSİNE çevirdi: kayıt sessionStorage
  (sekme ömrü) ve `?range=` olarak URL'e yazılır — asıl acı
  pencerenin SESSİZ olmasıydı. Reddedilen: localStorage kalıcılığı
  (haftalar sonra her sayfa geçmişte açılıyordu). Kod:
  `frontend/src/lib/logFilters.ts`,
  `frontend/src/lib/useUrlRange.ts`.
- **v0.8.268 / v0.8.285 (+2026-05 kararı)** — Tema yalnız token
  düzeyi CSS değişkeni: `redhat` paleti (PatternFly renkleri,
  OpenShift tarzı koyu nav) `#sidebar`'a kapsamlı token remap'iyle,
  sıfır bileşen değişikliği, yeni bağımlılık yok, ~1.5 KB CSS;
  285'te varsayılan tema. Grafikler `data-theme` değişince
  değişkenleri yeniden çözer. Reddedilen: Tailwind/shadcn göçü —
  30+ sayfayı yeniden stillemek kozmetik kazanç için haftalar,
  v1.0'a ertelendi. Kod: `frontend/src/styles/globals.css`
  (kaynak: operatör notu).
- **v0.8.428 · v0.8.509-510 · v0.9.67 · v0.9.1267** — Günlük triage
  yüzeyleri KLASİK kalır: Problems kart-feed'i (426) → sıralanabilir
  tablo geri (428; tam-sayfa detay kaldı); exception detayı
  drawer'ı (508) ve v426 düzeni → v0.8.425 klasik düzen (510);
  Operations Elastic-parity tablosu mockup onayına RAĞMEN canlıda
  reddedildi (67); Servis Overview "Tek-Bakış" `?newpage=1`
  bayrağıyla gerçek veride denendi, "eski hali daha iyiydi" (1267).
  Desen: operatör yoğun kullandığı listelerde yoğunluk/kas
  hafızasını estetiğe tercih ediyor; mockup onayı nihai değil.
  Kural: mockup-first + tek-commit'lik revert; 2026-07-30 "red
  geçmişimi overwrite edebiliriz" — hard kısıt değil, gerekçe hâlâ
  geçerli. Kod: `frontend/src/features/anomalies/ProblemDetail.tsx`
  (kaynak: operatör notları, 2026-07-10 → 2026-08-23).
- **v0.9.844 (+v0.10.283)** — Zaman serisi için TEK motor:
  `CorePanel` (`@grafana/ui` üstüne tek sarmalayıcı, altında uPlot);
  `chartsV2` bayrağı, MultiLineChart'ın kendi uPlot gövdesi ve
  DashboardViz SVG motoru söküldü (430 ekleme / 1992 silme).
  Chart.js vb. ağır kütüphane yok; @grafana/* + uplot tam pin
  (283). `'-ms'` senkron ad alanı kalıcı (saniye-eksenli motorlar
  yaşıyor). Reddedilen: eski yolu kaçış kapısı olarak tutmak (bir
  yıldır yalnız `?chartsV2=0`'da çalışıyordu). Kod:
  `frontend/src/components/chart/CorePanel.tsx`.
- **v0.9.1078** — Sayfa düzeyi yüzen/yapışkan şerit YOK: sticky
  kontrol çubuğu, sticky tablo başlığı ve `stickyBottom` Pager
  (v0.9.639/644/645'in denetim kararları) operatör isteğiyle geri
  alındı — görüş alanını yiyor, kaydırmada "oynuyor"; is-fit kaçışı
  yatay taşmayı `#content`'e sızdırıyordu. Kap içi `.is-scroll`
  (drawer) meşru; geniş tablo kendi `.table-wrap`'ında kaydırır.
  Kod: `frontend/src/components/Pager.tsx`, `globals.css` (kaynak:
  operatör notu, 2026-08-16).
- **v0.10.220 · 340 · 347 · 351 · 513** — Traces operatör UX
  kararları: zaman damgası EN SOLDA, sıra **Start time · Service ·
  Name · <attr> · Duration · Status · Spans** (Dynatrace'in sağ
  timestamp'i reddedildi; sabit sıra her kullanıcıda, sunucu yalnız
  EK kolonları saklar); Correlate ve alt Compare kalktı, dış link
  düğmeleri en sağda renkli; Tempo fallback ŞERİDİ kalktı ama
  PROVENANCE "source: Tempo fallback" ÇİPİ kalır (2026-09-07);
  şerit çizgisi p95 varsayılan, `?rt=` kompakt seçici,
  expand/shrink kalktı. Kod: `frontend/src/lib/traceColumns.ts`
  (test sırayı pinler),
  `frontend/src/components/traces/TraceHonesty.tsx`.
- **v0.10.255 + v0.10.257** — ContextBar sırası EnvPicker → kapsam
  (cluster/namespace/service/compare) → **TimeRangePicker sağ
  kenarda, son çocuk** (operatör: "eski yeri sağda daha iyiydi");
  sayfanın kendi kontrolüyle sunduğu boyut çubukta İKİNCİ KEZ
  çizilmez (`hidden` prop; devre dışı kutu bile değil) ve çubuğun
  URL→state effect'i yalnız uygulanan boyutlar için yazılır. Kod:
  `frontend/src/components/ContextBar/ContextBar.tsx`
  (`ContextBar.contract.test.tsx` sırayı pinler).

### Ops / Release

- **v0.9.0 (2026-07-17) → v0.10.1 (2026-08-25)** — Sürüm zinciri:
  her mantıksal birim KENDİ `vX.Y.Z` tag'i (operatör bug'ı derhal
  X+1, asla toplu; bisect tek küçük diff'e iner); v0.8 zinciri
  588'de, v0.9 zinciri 1388'de kapandı. 1.0 kesimi HAZIRDI
  (docs/RELEASE-1.0.md; canlı kapılar prod'da koşuldu, altısından
  beşi geçti) ama operatör "0.10.1 diye devam edelim, 1'e
  geçmeyelim" dedi; dosya silinmedi, ön-durum bloğu o gün yeniden
  ölçülür. Zincir değişince sürüm ÜRETEN makine (release skill'inin
  tag deseni) de çevrilir — aksi hâlde sessiz çatallanma
  (`version_chain_test.go` yönlü kapı). Kod:
  `.claude/skills/release/SKILL.md`,
  `internal/api/version_chain_test.go`.
- **2026-07-28 + 2026-08-25 (operatör kararları)** — Kabul edilen
  güvenlik duruşu, yeniden bulgu diye açılmaz: `/tempo/*` kimliksiz
  kalır; custom roller yalnız tarayıcıda uygulanır; admin config
  export'u saklı kimlik bilgilerini düz metin yazar; prod JWT
  imzalama anahtarı yer tutucu değerde ve ROTASYON REDDEDİLDİ
  (bedeli: tüm açık oturumlar düşer; `/admin/stats` şeridi bu
  yüzden kırmızı kalır). Gerekçe: tek-tenant, kurum ağı içi
  kurulum; operatör işletilebilirliği teorik maruziyete tercih
  ediyor. Kapsam TAM OLARAK bunlar — admin/editor/viewer rolleri
  sunucuda zorlanmaya devam eder, Settings GET asla secret geri
  vermez. (kaynak: operatör notları.)
- **2026-05-19 (operatör kararı)** — Multi-tenant YOK: her tabloya
  `tenant_id`, her sorguya WHERE, cache anahtarı patlaması ve ingest
  yönlendirmesi 3-5 sürümlük refactor; kurulum senaryosu
  (self-hosted, tek kurum, SaaS/MSP hedefi yok) karşılığını ödemiyor.
  "İleride lazım olur" diye kolon/WHERE eklenmez (YAGNI);
  tenant varsayan özellikler (per-tenant fiyat, signup, süper-admin
  çapraz görünüm) de kapsam dışı. (kaynak: operatör notu.)

### Repo / Ekip

- **v0.10.247 + v0.10.625** — `api.go` BÜYÜMEZ: yeni yüzey kendi
  `internal/api/<domain>.go` dosyasında `init() {
  registerRoutesExtra(name, fn) }` defteriyle kaydolur (çift ad
  panic; `buildMux` defteri ad sırasıyla deterministik kurar; Go
  1.22 çakışma testi defteri de kapsar; api.go 5 satır küçüldü).
  625: kural CLAUDE.md'de yazılıydı ama GLOBAL kapı yoktu →
  ratchet: taban `.claude/baselines/api_go_lines` (12113, yalnız
  aşağı iner), `api_go_size_test.go` büyümeyi VE indirilmemiş
  tabanı hata sayar, script + post-edit hook + CI adımı; tabanı
  yükseltmek CODEOWNERS onayı + `Api-Go-Baseline:` trailer'ı ister.
  Hedef: registerRoutes aileler hâlinde taşındıkça ~11000. Kod:
  `internal/api/route_registry.go`,
  `internal/api/api_go_size_test.go`,
  `scripts/guard-api-go-size.sh`.
- **v0.10.613 → v0.10.617-621** — Ekip geliştirmesi + GitHub org
  yayını hazırlık denetimi (`docs/audit/team-readiness-audit.md`):
  repo `cosretr` organizasyonuna taşındı; README/Chart/values/ghcr
  yolları org'a çekildi, Chart appVersion aylar sonra uygulama
  tag'iyle yeniden senkron (617). BİLİNÇLİ değişmeyen: go.mod
  modül yolu ve 622 dosyadaki import'lar (GitHub yönlendirir;
  kanonik yol ayrı karar). Maskeleme politikası koda girdi: gerçek
  prod IP/FQDN fixture'ları TEST-NET'e, LDAP fixture'ındaki gerçek
  kişi/birim adları, gerçek OpenShift iş yükü/küme adları (54
  dosya), kurumun Oracle şema/kolon adları ve Influx artıkları
  SENTETİK (618-621); kurum/müşteri adı, alan adı, gerçek
  şema/tablo adı repoya ve commit mesajına ASLA. Kalan: history
  rewrite (`git filter-repo`) ve kök manifestlerin repo dışına
  taşınması operatörde.
- **v0.10.623-624 · 626-628 · 629** — Katkı altyapısı ve kapılar:
  PR/issue şablonları, CODEOWNERS, .editorconfig, .gitattributes,
  CONTRIBUTING + SECURITY.md; `release.yml`'e `gate` job'ı
  (tsc+build, vet, `CGO_ENABLED=0` build, test, `make audit`) —
  tag push'u artık kırmızı main'den imaj basmaz (o gün üç kez
  basmıştı); ESLint gerçek kapı (0 hata); `go mod tidy -diff`;
  gofmt borcu tek mekanik sweep'te kapandı (86 dosya; dört
  toolchain aynı listeyi verdi — sapma değil borç) + CI gofmt
  kapısı; Trivy/npm audit HIGH'ları ve govulncheck pini
  (615/616/622). Reddedilen: go.mod'a `toolchain` satırı (operatör
  kararı; CI zaten 1.25.x). Onboarding: `docs/ENV.md` (89
  `COREMETRY_*`, yokken davranış + rol + secret bayrağı),
  `docs/local-dev.md`. Kod: `.github/workflows/ci.yml`,
  `.github/workflows/release.yml`, `.github/CODEOWNERS`.

## 2026-09-23 — Dış seri Problem'leri kaynak yaşarken süpürülmez (v0.10.900; v0.10.592 kararının tersi)

**Karar:** `anomaly:ext:<kaynak>/…` (seri) ve `anomaly-cluster:ext:<kaynak>/…` (küme)
satırları, kaynak etkin olduğu sürece evaluator'ın bayat süpürmesinden MUAF (ext-down /
ext-cap ile aynı `PollerOwnedSubject`). Yaşam döngüsü tarayıcıda: sayaç her poll'da aktif
anahtarlara sıfır yazar (v0.10.893), tarayıcı resolve eder; kaynak gerçekten susarsa ext-down
(v0.10.588) söyler; kaynak silinince muafiyet düşer.

**Neden 592 tersine döndü:** 592'de "seri bilerek süpürülür — 'source silent' dürüst sinyal"
denmişti. Oracle kaynağı aralığı 3600 s'ye kadar izinli; touch yalnız poll anında atılır;
süpürme eşiği 3×1 dk. >3 dk aralıkta her poll seri Problem'i "source silent" diye kapanıp
yeniden açılıyordu — canlı kipte alarm seli, gerekçe de yalan (kaynak susmamıştı). Dense
sıfır yazımı geldiğinde süpürmenin işi kalmadı.

**Kapsam:** yalnız dış hat. RED anomalileri (`anomaly:<svc>:<metric>`) süpürülmeye devam
eder; onların histerezis touch'u v0.10.889.

## 2026-09-23 — Forecast çekirdeği: "zaten limitte" bir DURUMDUR, kararı çağıran verir; disk "kaç gün" rozeti Problem satırından beslenir (v0.10.901; parite #6 dilim 1+2)

**Karar 1 — `forecast.StatusAtLimit`:** `internal/forecast.Fit` regresyon doğrusu limitin
üstündeyse tahmin yerine `StatusAtLimit` döner; anlamı ÇAĞIRAN verir. `capacityETA` (DB
kapasitesi) → tahmin yok (eşik dalı zaten alarmı açar; ikinci bir "0 saat" cümlesi gürültü),
`diskETADays` (CH diski) → 0 gün, ok=true (dolu diskte susmak alarmı kaçırmaktı; v0.9.1279).
İki test dosyası (capacity_eta_test, selfhealth_test:176) zıt sözleşmeyi pinler ve ikisi de
delegasyon sonrası DEĞİŞMEDİ. Paket bu ikiliği tek davranışa indirmez — birleştirilecek her
yeni çağıran (dilim 3 chip, dilim 4 Hosts/Clusters/heap) kendi çevirisini açıkça yazar.

**Karar 2 — kayan-nokta sırası pinli:** paket iki eski çekirdeğin sx/sy/sxx/sxy birikimini,
den/slope/intercept'i, SSres/SStot döngüsünü ve lastFit'i BİREBİR aynı sırada hesaplar;
`linear_test` eski gövdeyi `==` ile karşılaştırır (ufuk sınırı ±ulp dâhil). Eski testler kaba
(aralık kontrolü / 0.001 tolerans) olduğundan "temizlik" onlardan sessizce geçerdi. Tek
bilinçli sapma: NaN/±Inf örnek "geçersiz" (eskiden ok=true + "~NaNh").

**Karar 3 — "kaç gün kaldı" rozeti Problem satırından, yeni yüzey/polling YOK:** /admin/stats
disk rozeti yalnız `OpenProblemsSnapshot`'taki çözülmemiş `self-disk-eta` satırını okur
(sayı = `Problem.Value`, gün). Gerekçe: seri evaluator belleğinde ve yalnız liderde
(`COREMETRY_MODE=api` pod'unda sıfır); satır her pod'dan okunur, snapshot 5 s memo'lu, zarf
zaten 60 s önbellekli → ek sorgu sıfır. Bedeli dürüstçe beyan: chip yalnız eşiğin (7 gün)
altında görünür, "tahmin var ama problem yok" hâli görünmez (dilim 4: kalıcı seri). Satırda
olmayan sayı (R², band) chip'e YAZILMAZ.

**Karar 4 — rozet tonu SAYIDAN, satır ciddiyetinden değil:** `Severity` yaş eskalasyonuyla
30 dk'da critical'a çıkar (Inbox sözleşmesi, self-disk-eta muaf değil). "Kırmızı = 2 günden
az" cümlesi ancak `days < SelfDiskCriticalDays` ile doğru kalır; `severity` payload'da kalır
ama ton için okunmaz.

**Karar 5 — lider değişiminde açık satır taşınır:** bellek-içi disk serisi yeni liderde
boşken (ısınma: <4 örnek ya da <30 dk) açık satır son değeriyle yeniden sunulur
(`diskCarryOver`; v0.9.1294 volCache "son ölçümü yeniden sun" deseniyle aynı gerekçe).
Aksi hâlde her deploy bir sahte "çözüldü" + 30 dk sonra mükerrer bildirim üretiyordu.
Isınma dışında "eğilim yok" satırı yine kapatır — taşıma bir yaşam döngüsü uzatması değil,
kör pencerenin köprüsüdür.

## 2026-09-25 — CH disk doluluğu kalıcı seri olarak yazılır (v0.10.911; v0.9.1279 kararının tersi)

**Karar:** Evaluator lideri her tikte zaten okuduğu `system.disks` değerini `metric_points`'e
`coremetry.self.disk_used_bytes` gauge'ı olarak da yazar (service_name `coremetry-self/<disk>`
tek düğümde, `coremetry-self/<host>/<disk>` kümede — rollup şeması attr'ları katladığı için
kimlik service_name'de). /admin/stats disk rozeti açık problem YOKKEN son 7 günün saatlik
serisinden (rollup_metrics_1h) `forecast.Fit` ile tahmin eder (ufuk ≤30 gün, R² ≥ 0.6).

**Neden 1279 tersine döndü:** 1279 "kalıcılaştırmak yeni tablo + yeni yazma yolu demek, 6
saatlik bellek penceresi yeter" demişti. Yeter değildi: rozet yalnız 7 günün altındaki açık
problemden besleniyordu ("20 gün sonra dolacak" hiçbir yerde yoktu), deploy'da 30 dk kör pencere
vardı, mevsimsellik imkânsızdı. Yeni TABLO yok (metric_points + otomatik rollup); hacim dakikada
düğüm × disk satırı. Operatör onayı 2026-09-25 (parite #6 dilim 4 Karar 1).

**Kapsam:** self-disk-eta KURALI hâlâ bellek serisinden karar verir (davranış değişmedi; yazım
hatası kuralı etkilemez). Hosts/Clusters çipi ve heap ETA bu karara dahil değil. Haftalık
mevsimsellik ≥14 gün tarihçe biriktikten sonra ayrı adım.

## 2026-09-25 — Buton bütünlüğü: shadcn/Tailwind DEĞİL, mevcut atomlar (Seçenek B, v0.10.919)

**Karar:** Buton/bileşen tutarlılığı mevcut `components/ui` atomları üzerinde kurulur;
Tailwind + shadcn/ui eklenmez. Operatör onayı 2026-09-25 ("Onay", audit:
`docs/frontend/shadcn-audit.md` §6).

**Neden:** Sorun görünüm farkıydı (sayfaya özel ham düğmeler), stil motoru değil. Tailwind
4.3k satırlık global CSS'in yanına ikinci bir stil sistemi getirirdi; `button.sm` gibi
eleman kuralları (0,1,1) tek Tailwind yardımcısını (0,1,0) sessizce yener. İstenen
özelliklerin çoğu zaten vardı (zorunlu `variant`, `loading`, IconButton'da tip düzeyinde
`aria-label`); A seçeneği bunları yeniden yazıp ~700 çağrı yerini değiştirecek, ~20–35 KB gz
ekleyecekti. Üç tema (`data-theme` dark/light/redhat) shadcn'in `.dark` sözleşmesine uymuyor.

**Ne geldi (Seçenek B temeli):** ESLint `ui/no-raw-button` (+ `eslint-suppressions.json`
mevcut 102 ihlali sayar, yalnız azalır; istisna `-- gerekçe` ister), `ButtonGroup`,
`Tooltip` (ağaçta `position: fixed` — `--z-tooltip` çekmece/modal altında kaldığı için
portal değil), yüklenirken genişlik koruyan `Button`, yalnız geliştirmede `/design` kataloğu.

**Geri dönüş:** Atom arayüzleri korunduğu için ileride shadcn'e geçiş atomların İÇİNİ
değiştirmek demek; çağrı yerleri değişmez. Ters yön (A'dan B'ye) pahalı olurdu.

## 2026-09-25 — Sade palet: renk yalnız sapma, seçim ve veri için (v0.10.920 adım 0)

**Karar (operatör, mockup üzerinden):** K1 açık tema beyaz kart / gri sayfa (ters merdiven kapandı);
K2 koyu temada uyarı rengi sakin altın `#f3b94c`; K3 grafik serisi rengi isimden gelmeye devam eder
(açıklama/ipucu zorunlu); K4 redhat PatternFly kimliğini korur (nötrler, `#0066cc`, kare köşeler,
koyu kenar çubuğu) ve yalnız geçmeyen çiftler PF'nin kendi metin tonlarıyla düzeltilir
(uyarı `#795600`, form kenarı `#8a8d90`, hover `#004080`); K5 sağlıklı durum nötr, yeşil yalnız
"düzeldi" (adım 1); K6 Coremetry kırmızısı yalnız logoda (adım 1).

**Neden:** Varsayılan redhat temasında uyarı metni 1,99:1, bilgi 2,99:1, kenar çubuğunda seçili
menü 2,62:1 idi; bugünkü tokenlarda 130 çift WCAG'den kalıyordu. Rengin çoğu "sağlıklı" durumu
işaretliyordu. Değerler OKLCH ile karşıtlık hedeflerine göre çözüldü; 296 denetim, 0 başarısız
(kalan 5 uyarı: renk körlüğünde ok/uyarı/hata ayrımı AA içinde çözülemiyor → durum her zaman
ikon/yazıyla).

**Adım 0 kapsamı:** yalnız token değerleri + yeni tokenlar (`--accent-hover`, `--focus`,
`--*-bg`, `--err-solid`, `--warn-solid`) + kural düzeltmeleri + grafik paleti (durum renklerinden
OKLab ≥0,10). Rol ayrımı: `--accent` dolgu / `--accent2` metin; `--warn` metin / `--warn-solid`
dolgu (açık temalarda metin kahvesi renk körlüğünde kırmızıya yaklaşıyordu). İnceleme turu
(3 mercek + kuşkucu) 14 gerçek bulgu çıkardı; hepsi düzeltildi ve `styles/contrastTokens.test.ts`
tüm WCAG çift tablosunu artık doğrudan globals.css'ten denetliyor.
Bileşenlerden renk çekme (adım 1), yapı sadeleştirme (adım 2), hex/ton ratchet'i (adım 3) ve
yazı ölçeği (adım 4) ayrı onayla. Mockup: claude.ai artifact "Coremetry Sade Palet".

## 2026-09-26 — Sade palet adım 2: yapı sadeleştirme (v0.10.928)

**Karar (operatör: "Önerini yapalım"):** Y1 kartlar sabit — hover'da mavi kenar ve el imleci yok;
yalnız gerçekten bir yere giden kart `CardLink` (gerçek `<a>`, hover/odakta `--border-strong`).
Y2 iç ayırıcılar yeni `--divider` tonunda (bg1'e karşı 1,21–1,27:1; dış çerçeve `--border`
kalır). Y3 tablo başlıkları sade: `--fs-xs` (11px; 11,5 merdivende yok), 600, `--text2`,
büyük harf ve harf aralığı yok; küçük harfli doğal dil etiketleri cümle düzenine çekildi
(tanımlayıcı/birim olduğu gibi). Y4 ikincil buton dolgusuz: şeffaf + `--border-control`
(bg1'e ≥2:1), hover bg2 + `--border-strong`, basılı bg3; içeriğin ÜSTÜNDE yüzen ikinciller
`is-overlay` ile opak.

**Neden:** Rengin ve çizginin çoğu yapıyı değil gürültüyü taşıyordu: 76 statik kart
tıklanabilir gibi davranıyordu, başlıklar büyük harfle bağırıyordu, 325 ikincil buton
birincil kadar dolguluydu. Kapılar: `contrastTokens` iki yeni tokenı her temada ölçer,
`paletteStep2.pin` yapıyı çiviler; `.card-tight` (hiç uygulanmamıştı) kural, prop ve çağrı
yerleriyle birlikte kalktı.

## 2026-09-26 — Tablo standardı: dört tür, sessiz satır, renk yalnız sapmada (v0.10.930→)

**Karar (operatör: "önerini yapalım mockup gördüm uygundur", mockup "Coremetry Tablo
Standardı"):** T1–T12 ve sekiz açık soruda önerilenler. T1 dört tür tablo (DataTable,
gerekçeli statik tablo, KeyValue atomu, muaf lejant/sohbet); T2 satırın dört hâli, hover ve el
imleci yalnız tıklanabilir satırda; T3 sessiz başlık; T4 sayılar arayüz fontunda (günlük dört
tablo dahil); T5 tek yazı boyu, hiyerarşi renkle, monospace yalnız kimlik/kodda; T6 `--row-h`;
T7 bir satır bir eylem, gezinen satır gerçek bağlantı; T8 tek eylem sütunu (⋯ hep görünür,
soluk); T9 renk yalnız sapan değerde, durum sütunu nokta + metin; T10 tek çerçeve; T11 hücre
tek satır; T12 durumlar tablonun içinde. Yoğunluk 4 → 3 basamak; "Kolonları sıfırla" başlık
satırının ⋯ menüsünde; varlık başına tek açılış hedefi.

**Neden:** ~195 tablonun iskeleti ortaktı, gürültü detaydaydı: satırların %74'ü tıklanamadığı
hâlde el imleci alıyordu, 705 sütunda boşta ok, 284 monospace sayı hücresi, 405 satır içi hücre
stili (yoğunluk hücrelerin yarısına ulaşmıyordu), 13 farklı satır yüksekliği tahmini, 5 ayrı
satır-tıklama yolu, 26 dosyada kutu içinde kutu. Envanter iki gerçek hata da buldu (v0.10.930
SLO kırpma, v0.10.931 çizilmeyen hata rengi).

**Göç:** dilim 0 kapılar + hatalar, dilim 1 tek CSS sürümü, dilim 2 primitif, dilim 3 sayfa
süpürmeleri, dilim 4 durumlar. `tableUnityRatchet` tabanları yalnız azalır.


## 2026-09-26 — CoSRE değerlendirme paneli: donmuş vakalar Ayarlar'daki modelle sunucuda koşar (v0.10.940)

**Karar (operatör: "bunu settingsteki modelden alsa direkt", "Ücretli sağlayıcı yol local llm
modeline bağlıyız", mockup "CoSRE Değerlendirme Paneli" + "Ok"):** K1 Ayarlar › CoSRE içinde
"Değerlendirme" alt sekmesi (`?tab=eval`, yalnız admin). Koşu, binary'ye gömülü evalset'i
(`internal/copilot/evalset/*.json`) sunucuda, her yüzeyi üretimin o yüzey için seçtiği
profille (eşleme > grup kardeşi > varsayılan) ve üretimin çağrı yoluyla (`aiCall`) koşar;
onay adımı ve ücretli sağlayıcı uyarısı YOK. K2 çağrılar `ai_calls`'a `evalset-<Yüzey>`
yüzeyiyle yazılır; /ai sayaçları, seri, çağrı listesi ve bütçe bu satırları varsayılan olarak
HARİÇ tutar, `?source=evalset` ("Kaynak: Değerlendirme") yalnız onları gösterir — yeni kolon
yok, ayrım tek önek sabitinde (`chstore.AICallEvalsetSurfacePrefix`). K3 listede son 20 koşu;
fiziksel silme `ai_eval_runs` tablosunun 180 günlük TTL'i (mutasyon yok).

**Neden:** Vakalar yalnız CI dışı bir CLI koşumuyla ölçülebiliyordu; prompt denetiminin yeni
vakaları hiç koşulmamıştı. Puanlama ve vaka yolu CLI ile ORTAK (`runEvalsetCase`), böylece
panel ile `go test -tags evalset` aynı şeyi ölçer; CLI özel Service'iyle sıcaklık 0'da ve
`ai_calls`'a yazmadan kalır. Aynı anda tek koşu (süreç + taze `running` satırı), 15 dk
güncellenmeyen koşu "yarım kaldı" sayılır; son yazım başarısızsa sonuç bellekte tutulur.

## 2026-09-26 — CoSRE araştırma asistanı, Faz A: kaynak durumu, eşleme, hesap doğruluğu (v0.10.944)

**Karar (operatör: CoSRE trace/log/metrik kaynaklarını birlikte inceleyen, kanıt gösteren ve takip
sorularıyla süren bir asistana dönüşsün; "yalnızca plan sunma, uygula"):** Mevcut sağlayıcı ve
mimari korunur; araçlar sohbet ile MCP'nin ORTAK kayıt defterinde kalır. Her araç sonucu veriden
ayrı bir kaynak durumu taşır (`internal/sourcestate`: ok · empty · unreachable · unauthorized ·
timeout · partial · delayed · truncated · not_configured); erişilemeyen kaynak boş sonuç gibi
görünmez, "log yok" "hata yok" diye okunmaz. Yeni araç hata sınıfı `unauthorized` (401/403: tekrar
deneme, kapsam dışı olduğunu söyle). `search_logs` / `query_metric` / `get_trace` yenilendi,
`list_log_fields` / `list_metric_labels` / `compare_periods` eklendi (57 → 60 araç). ES alan ve VM
etiket eşlemesi yapılandırılabilir (servis, ortam, cluster, namespace, pod, sürüm); öncelik
yapılandırma > keşif > yok ve araç hangisini kullandığını söyler. Trace↔log eşleşmesi kimlikle
ise `match=trace_id/span_id`, servis/pod/zamanla ise `contextual`; trace kimliği metrik etiketi
olamaz (exemplar). Karşılaştırma tüm pencere yüzdeliği kullanır (p95 ortalaması YOK), çok ortamlı
serviste env ister, trafik karışımı / örnek sayısı / kapsama / örnekleme notu taşır; kritik yol
toplamı iç içe süreleri toplamaz. Çekmecede görünür bağlam şeridi (trace, span, servis, ortam,
cluster/namespace, pencere) her turda gönderilir ve konuşmayla saklanır.

**Neden:** Haritalama (6 okuyucu) mevcut araçların kaynak hatalarını sessizce boş sonuca, 401/403'ü
`internal`'a çevirdiğini; alanların kodda sabit aday listeleriyle çözüldüğünü; kritik yol
toplamının ve iki AI karşılaştırma yolunun (aggRED, guided window_compare) yanlış hesapladığını
gösterdi. **Sınırlar (mevcut kararlar):** üründe veri maskeleme yok (maskeleme collector'da;
yalnız DataNotInstruction çerçevesi ve FenceSafe); tenant modeli yok (rol tabanlı yetki, yeni
araçlar viewer, REST eşleriyle aynı). Faz B: seçili trace için kanıta dayalı inceleme akışı ve
takip sorularının araç döngüsüne bağlanması.

## 2026-09-26 — Exception varsayılan tabanı 5; çok servisli ve regressed gruplar muaf (v0.10.949)

**Karar (operatör: "aynı anda farklı servislerden gelmiyorsa 5'ten düşük exception'ı göstermeye
gerek yok; tek servisten gelen 5'ten küçükleri göstermeyebiliriz"; regressed sorusuna "görünür
kalsın"):** Inbox ve /problems varsayılan tabanı 2 → 5 (v0.10.740 kararının yerine). İstisnalar:
(1) aynı exception (tür + normalize mesaj; fingerprint servisi içerdiği için değil) aynı anda
(etkinlik aralıkları storm penceresi payıyla — varsayılan 10 dk — çakışan) ≥2 serviste görülüyorsa;
(2) regressed gruplar (öncelik koduyla aynı `state` kaynağı); (3) P1 eşiği 5'in altına çekilirse
taban ona iner. Açık `?minOcc=N` ve `minOcc=0` (hepsi) aynen. Şerit gizlenenleri ve muafiyetle
tutulanları sayar; muaf satırda "N servis" işareti.

**Neden:** Tek servisteki birkaç oluşum gürültüydü; aynı hatanın birden çok serviste aynı anda
görülmesi ve geri dönen hata ise sayı küçük olsa da sinyal. Mesajsız / yalnız kimlikten oluşan
exception'lar muafiyet anahtarı almaz (genel tür filoda her an bir yerde tekrarlanır).

## 2026-09-26 — PromQL konsolu (Thanos): editor+, audit_log, pod başına sınırlar (v0.10.950–953)

**Karar (operatör: Faz 0 denetimi docs/promql-console/audit.md, "4 onay"):** Uzak küme başına
Thanos üzerinde salt okunur PromQL konsolu; `/api/promql/*` kendi rota dosyasında (api.go büyümez).
Önerilerin tamamı onaylı: editor ve üstü (özel roller sunucuda kısıtlayamadığı için viewer değil);
her sorgu `audit_log`'a `promql.query` (başarılı, hata, reddedilen; otomatik tamamlama değil);
kullanıcı başına eşzamanlılık ve dakikalık sınırlar v1'de POD BAŞINA; `partial_response=false`;
7 günü aşan aralık REDDEDİLİR (kırpılmaz); otomatik adım grafik genişliğine göre, elle adım taban
ve 11k nokta tavanına yükseltilir; seri tavanı 500 + toplam sayı, gövde tavanı ayar (32 MiB) açık
hatayla; sorgu geçmişi sunucuda, kullanıcı başına son 50 (saved_views blob'u, localStorage değil);
paylaşılan querier kipinde küme etiketi yorum/dizgi güvenli eklenir, match[]'e de eklenir.
shadcn/Tailwind yok (2026-09-25 buton kararı): mevcut atomlar ve CorePanelMulti.

**İkinci Thanos okuyucusu:** `internal/promapi` başlığındaki "yeni çağıranlar promapi kullanır"
kuralına rağmen konsol okuyucusu `internal/thanos/console.go`'da (belirteç çözümü ve eşleştirici
ekleme o pakette, `effectiveTokenFor` dışa kapalı; onaylı denetimin planı). Eski `doQuery`'e ve
`promapi`'ye dokunulmadı. Rollouts v2 aynı taşıma üzerinden `WorkerQuery` girişi ekleyecek
(docs/rollouts/v2-audit.md §3.4) — üçüncü bir okuyucu yazılmaz.

## 2026-09-26 — Rollouts v2 Faz 1: KSM doğruluk kaynağı, Argo hub, ayrı durum tabloları (v0.10.955–960)

**Karar (operatör: Faz 0 denetimi docs/rollouts/v2-audit.md, "3 onay" = §12.3 karar 1–10,
"24 25 onay"):** Önerilerin tamamı onaylı.
1. İşçi okuyucusu PromQL konsolunun taşıması üzerinde tek dışa açık giriş: `thanos.WorkerQuery`
   + `WorkerLimits` (konsol yeniden platformlanmaz; `doQuery` ve `promapi` dokunulmaz;
   çözülemeyen `TokenRef` → istek gönderilmez).
2. İşçi tavanları: çağrı başına ≤ 50 000 seri, ≤ 64 MiB gövde, 30 sn (tavan 45 sn).
3. v1 KSM bacağı taşınmaz (v2 onun yerini alır).
4. Filtre `?trigger=`; kolon "Kaynak"/"Source" YENİ kolon kimliğiyle; API alanı `trigger`.
5. Argo hub sıradan bir Remote Cluster. Ortamda Argo CD İKİ hub kümesinde kurulu (operatör,
   2026-09-26): blobda `hubs[{clusterId, injectClusterLabel}]` (etiket ekleme hub başına), her
   instance kendi `hubClusterId`'sini taşır;
   `https://kubernetes.default.svc` o instance'ın kendi hub'ına çözülür, keşif hub başına koşar.
6. Sekiz tablo (`rollout_events`, `rollout_workload_state`, `argocd_app_status`,
   `argocd_sync_events`, `argocd_app_mapping`, `rollout_classification`,
   `ado_commit_enrichment`, `rollout_worker_runs`); TTL 180 g / 400 g (durum) / 30 g
   (eşleme, koşu kaydı). `workload_rollouts` genişletilmez (kimliği span türevi, iki yazarlı).
7. `apiServerUrls` liste (normalize: küçük harf şema+host, sondaki `/` yok, port yoksa
   `:6443`); `pairGroup` serbest metin; küme başına tek `argoSuffix`.
8. Dedektör tick'i 30 sn.
9. `rollout_events` anahtarında `incarnation_at` (CMO denylist'i `_created`'ı düşürüyor;
   sil/yeniden yarat geçmişi ezmesin).
10. İlk görülen iş yükü için `change_type='initial'` olayı; hiç koşmamış ilk tur yalnız taban.
24. Purge: sekizi de telemetri sınıfı, `argocd_sync_events` HARİÇ — o config gibi korunur
    (Argo kendi geçmişinde yalnız son 10 kaydı tutar; silinen geri gelmez).
25. `0015`'te ZooKeeper yolu `0012` gibi sabit `/clickhouse/tables/state/<ad>`; boot'un çalışma
    zamanında çözdüğü önekten farkı migration başlığında yazılı.

**Neden:** KSM her iş yükünü görür (span'i olmayan dahil), span türevi v1 görmez; Argo ve Azure
DevOps kanıtı geç gelir ve ayrı yazarlıdır, bu yüzden olay satırına kopyalanmaz, okurken
birleştirilir. **Faz 1 davranış değiştirmez:** ayarlar, okuyucu, tablolar; hiçbir işçi başlamaz,
bayraklar kapalı. Faz 2–5 her biri kendi §11 sorgu paketi yanıtlarını bekler.

## 2026-09-27 — On state tablosu birleşik ZK yolunda yeniden kurulur: sihirbaz, veri kaybı kabul, taşıma yok (v0.10.965)

**Karar (operatör: "sihirbaza ekle … düzeltmesi ve kontrolü"; "2 için önerin a"; "3 için de data
kaybı önemsiz"):** Prod'da (v0.10.960, 4 host / 2 shard) on state tablosu eski shard'lı ZK yolunda
(`/clickhouse/tables/<shard>/<ad>`) ve her biri iki replikasyon grubuna bölünmüş: `ingest_ledger`,
`ai_eval_runs` ve sekiz Rollouts v2 tablosu (`rollout_events`, `rollout_workload_state`,
`argocd_app_status`, `argocd_sync_events`, `argocd_app_mapping`, `rollout_classification`,
`ado_commit_enrichment`, `rollout_worker_runs`). Denetim ve düzeltme Admin → ClickHouse → Replika
tutarlılığı kartına eklendi (Rollouts kartına değil; oradaki 0015 ön kontrolü bağımsız son kontrol
olarak kalır). Düzeltme on tablonun HEPSİ için DROP (`ON CLUSTER … SYNC`) ve birleşik yolda
(`<önek>/state/<ad>`, replika `{shard}-{replica}`) CREATE; önce hepsi düşer ve her host'ta gittiği
yoklanır, sonra hepsi kurulur ve doğrulanır.
- `ingest_ledger` (türev sayaç defteri): en fazla 30 günlük filo defteri geçmişi gider (öneri a).
- `ai_eval_runs` (normalde korunan sınıf): evalset koşu geçmişinin kaybı kabul; çalışan koşu varsa ret.
- Sekiz Rollouts v2 tablosu yalnız BOŞKEN ve yazıcılar kapalıyken (source v1, argocd kapalı).
- Veri TAŞINMAZ (0009 usulü birleştirme yok). Onay planın ölçüm anını ve tablo başına satırı taşır.

**Onaylanmayan:** boot kuralı değişikliği (hiç var olmayan yeni state tablosunu eski yolda başka
tablo olsa bile her zaman birleşik yola kurmak — `useUnifiedStatePath` kural 3). Açık öneri olarak
duruyor; kural değişmedi. Bu yüzden izin listesi dışında eski yolda kalan bir state tablosu kilidi
kapalı tutar ve sihirbaz kısmi seçimi ancak açık onayla (`partialOK`) koşar.

**Neden:** Kural 3 yüzünden tek bir eski yollu state tablosu (ilk `ingest_ledger`, v0.10.767)
sonradan eklenen her state tablosunu bölünmüş doğurdu. Hepsi birleşik yola geçince kural 4 devreye
girer ve kilit her pod'un bir sonraki açılışında kendiliğinden açılır.

## 2026-09-27 — Rollouts v2: açık kararlar 11–23 önerilerle (16 hariç); Argo CD ayar sekmesi onaylı

**Karar (operatör: "Onay rollout için önerin"):** Argo CD ayar sekmesi mockup'ı onaylı (yapımı sürüyor;
boş tokenRef kayıtlıyı korur, keşifte uygulama/shard sayısı, kayıtlı instance kimliği salt okunur,
instance bağlı hub kaldırılamaz). docs/rollouts/v2-audit.md §12.3'ün 11–23 kararları audit'in
önerisiyle: 11 Argo Rollouts dilimi yalnız §11 R bulursa; 12 DeploymentConfig "kapsanmıyor" (pay
anlamlı değilse); 13 JBoss/k8s-dışı için span'den çıkarılan sürüm "çıkarım" etiketli yedek, KSM
olaylarına karışmaz; 14 eski `?rollout=` bağlantıları TTL'e dek eski tablodan, yenisi 6 parçalı
anahtar; 15 metrics-only modda tahmin gösterilir, açıkça etiketli; 17 tetikleyici Problem puanına
girmez, yalnız kanıtta; 18 v2'de Problem/olay üretilmez; 19 RecentDeploy kaynağı P2.5'te
rollout_events; 20 DevOps PAT önce tokenRef, Build (Read) yalnız pipeline commit status
yazmıyorsa; 21 ikinci atlama P4'te §11 V sonrası isteğe bağlı; 22 out_of_band aktör göstermez;
23 etki okumada hesaplanır (P5). **16 (operatör, 2026-09-27: "servis adı eki olsun ama env
bulamazsa env parçacığına da baksın"):** ortamın asıl kaynağı servis adı eki (`-prod/-int/-uat/-prep`);
ek yoksa ya da tanınmazsa Argo uygulama adındaki `<env>` parçası. `envList` bu ek sözlüğüyle hizalı.
26–30 karar değil, ortam bilgisi (§11). Hiçbiri P2.2'den önce koda girmez.

## 2026-09-27 — Argo CD ayar sekmesi: dört arka uç kuralı (v0.10.968)

**Karar (operatör: "Onay rollout için önerin" — mockup onayı bu dört arka uç değişikliğini de
kapsar; kod `internal/argocd/put.go`, `discover.go`, `internal/api/argocd_settings_routes.go`):**
1. **Boş `tokenRef` kayıtlıyı korur.** PUT'ta instance başına (id kırpılmış): dolu ref değiştirir
   (`secretref.Valid`); boş ref + id kayıtlı → kayıtlı ref kopyalanır; boş ref + istek-yalnız
   `clearTokenRef: true` → kaldırılır; dolu ref + `clearTokenRef: true` → 400
   `instances[i].clearTokenRef`; bool olmayan bayrak (null dahil) → 400 aynı yolda; kayıtlı
   olmayan id → ref yok, bayrak etkisiz. `clearTokenRef` `Instance` alanı DEĞİL: kalıcı blobda,
   GET/PUT cevabında ve audit'te görünmez. FE yeni satırın boş ref'i için `clearTokenRef: true`
   gönderir (aynı kimlikli silinmiş satırın ref'ini miras almasın). Config import'la bozuk gelmiş
   kayıtlı ref PUT'ta yeniden yazılmaz (400 `instances[i].tokenRef`, değer yankılanmaz). PUT
   birleştirmeden önce kalıcı blobu best-effort yeniden yükler: B pod'undaki kayıt A pod'unun az
   önceki kaydına karşı birleşir (hata loglanır, bellekteki blobla sürülür).
2. **Kayıtlı instance kimliği salt okunur — RED, örtük sil+ekle değil.** Kayıtlı bir
   `(hubClusterId, hubNamespace)` yuvasını, o yuvanın kayıtlı id'si gövdede yokken yeni bir id
   alırsa 400 `instances[i].id`; mesaj eski ve yeni id'yi, hub/ns'yi ve çareyi söyler (önce eski
   satırı kaldırıp kaydet, sonra yeni kimlikle ekle). Açık bir "sil+ekle" bayrağı yok: iki ayrı
   kayıt yeterince açık. İzinli: kayıtlı id'yi başka hub'a taşımak, silmek, boş yuvaya yeni id,
   taşınan (gövdede duran) id'nin boşalttığı yuvaya yeni id. Namespace'i de değişen yeniden
   adlandırma sil+ekle'den ayırt edilemez ve izinli; pinleri `pins[i].instanceId` korur. Gerekçe:
   id Faz 3'ten itibaren ClickHouse `instance_id`; sessiz değişim geçmişi yetim bırakırdı.
3. **Instance'ları bağlı hub kaldırılamaz.** `Validate`'ten ÖNCE ön denetim: kayıtlı bir hub
   `hubs`'tan çıkarılırken ona bağlı instance gövdede duruyorsa 400 — yol bugünkü
   `instances[i].hubClusterId` (mockup'ın kayıt hatası listesi ve mevcut test pini), mesaj hub adı
   + id, bağlı instance sayısı ve çare (taşı ya da hub'ı bırak). Aynı PUT'ta instance'ları taşımak
   kaldırmayı serbest bırakır. Genel "hubs listesinde değil" mesajı da artık çareyi söyler.
4. **Keşifte uygulama ve shard sayısı.** Aday bulma label-values'ta kalır. Adaylar kurulduktan
   SONRA ikinci tur: uygulama sayısı iş başına TEK anlık `count by (namespace) (group by
   (namespace, exported_namespace, name) (argocd_app_info{job="J"}))` (iş genelinde
   `exported_namespace` yoksa by'sız `count(...)`); shard sayısı sınırlı `pod` label-values (≤100,
   1 sa pencere; kesikse alt sınır, FE "≥100"). Onay metni "label-values çağrıları" diyordu; onaylı
   mockup dipnotu ve audit §5.4 izlendi: kural 1–3 anlık sorgu, listelemeden önce sayım ve
   kopyaları gruplama ister (shard/HA pod'ları çarpmaz); kural 4 `name` üzerinde label browser'ı
   yasaklar; §5.2'ye göre `name` tek başına durum C'yi eksik sayar. Sonuç ≤100 seri (iş başına
   namespace tavanı), ~40k serilik metrikte güvenli. **Bütçe:** sayım çağrıları aynı ≤150 çağrı /
   60 sn bütçesinden sayılır ve yalnız ARTANI kullanır (adaylar önce kurulur; sayım hiçbir adaya
   mal olmaz). Sayım hatası `error`/`incomplete` yazmaz, adayı düşürmez, 200'ü bozmaz; kısa neden
   `countNote`'ta. Bütçe biterse kalan adaylar "sayım atlandı: keşif bütçesi doldu" notu, sonuç
   `countsIncomplete` alır (audit details'te de). Çağrı sayısı iş başına 2'den ~4'e çıkar.

**Açık kalan (ertelendi):** iki admin arasında bütün-blob kayıp güncellemesi (son yazan kazanır);
hub Thanos 401/403 ayrı bir "yetki yok" durumu yerine "erişilemedi" + sabit metin (test pinli).

## 2026-09-27 — Runbook bash adımları Coremetry'nin sırlarını devralmaz (v0.10.966)

**Karar (operatör: "Önerini yapalım" — öneri 1, alt önerilerle):** bash adımı yalnız bir taban env
izin listesini görür (PATH, HOME, LANG/LC_ALL, TZ, TMPDIR, USER, HOSTNAME, sertifika yolları,
KUBECONFIG/KUBERNETES_SERVICE_*, NO_PROXY); proxy değişkenleri yalnız parola taşımıyorsa geçer;
`COREMETRY_AGENT_ENV_PASSTHROUGH` ile açıkça listelenen adlar geçer (tam ad; token benzeri ad
boot log'unda işaretlenir), `COREMETRY_*` ASLA. Linux'ta süreç her rolde dump edilemez
(PR_SET_DUMPABLE=0): aynı uid'deki çocuk `/proc/<pid>/environ`'u okuyamaz. Zaman aşımında ve adım
sonunda tüm süreç grubu öldürülür; boru tutan arka plan süreci 2 sn sonra kesilir, adım başarılı
sayılır ve çıktıya not düşülür. **Neden:** varsayılan all-mode'da editor rolündeki biri JWT secret'ı
(→ admin oturumu) ve CH parolasını adım çıktısına basabiliyordu (Helm incelemesi, v0.10.958).
**Kalan risk (belgeli):** aynı uid'in okuyabildiği dosyalar (config, bağlı secret'lar, SA token),
iç ağdaki CH/Redis; gerçek sınır ayrı agent rolü, ileride ayrı uid/sidecar.

## 2026-09-27 — Boot: hiç var olmayan yeni state tablosu her zaman birleşik ZK yoluna (öneri 2)

**Karar (operatör: "Önerini yapalım" — öneri 2):** `useUnifiedStatePath` kural 3 kaldırılır: kümede
HİÇBİR düğümde olmayan bir state tablosu, eski yolda başka state tabloları olsa bile birleşik yola
kurulur (kural 2 — tablo bir yerde varsa komşusuna katıl — "yeni düğüm" senaryosunu zaten korur).
**Neden:** kural 3 kendini besleyen bir kilitti: ingest_ledger eski yolda doğunca (v0.10.767) sonraki
her state tablosu shard'a bölünmüş doğdu (prod, 10 tablo; v0.10.965 sihirbazı onarır).

## 2026-09-27 — CoSRE trace incelemesi: «Kök neden» güvenle, koşullu «Stacktrace detayı» (v0.10.972)

**Karar (operatör: "CoSRE de neden kök neden çıkmıyor artık" → "Kök neden olsun yine de"; "Stacktrace
detayı bölümü de geri gelsin"):** "CoSRE'ye sor" ilk cevabında (v0.10.948) «Olası neden» → «Kök
neden»; ilk satırı güven: "Güven: kesin" (hata veren span/log'dan nedene kanıt zinciri kesintisiz)
ya da "Güven: olası — <eksik halka>". Kanıt kuralları aynen (kimlik, uydurmama, ilişki ≠ neden;
yalnız zamansal ilişki kesin olamaz). Kart Karar şeridini YALNIZ "kesin"de çizer; "olası" hipotez
olarak kalır (şerit yok). «Stacktrace detayı» Kanıt'la Kök neden arasında TEK koşullu bölüm: yalnız
L satırında stacktrace varken; Oracle satırı / çıplak exception.type sayılmaz, yoksa bölüm hiç
yazılmaz. Sunucu L satırına üst kareleri "stacktrace:" alanıyla koyar (önce exception.stacktrace
prompt'a hiç girmiyordu): kaynak değer kesik değilse ilk 3 kare + her Caused by'ın ilk karesi,
≤600 rune, FenceSafe, en çok 2 farklı stack; L bütçesi basılan stack kadar büyür.
get_logs_for_trace öznitelik değerini 200 runede kestiği için OTel/ECS exception.stacktrace'te
çoğunlukla başlık + ~1 kare gelir; kesik olduğu satırda "(kaynak kesik: …)" diye söylenir ve istem
kesik stack'i tek başına "Güven: kesin" dayanağı saymaz. Problem özetinin düz "Olası neden:"i ve RCA
etiketleri AYRI yüzey, değişmedi. Takip (onay ister): mcptools logAttrs'ta stack anahtarlarına
(exception.stacktrace, error.stack_trace, …) sınırlı büyük tavan (~1500 rune, FenceSafe) — MCP
çıktısını dış istemciler ve sohbet için de değiştirir.

## 2026-10-03 — Kök neden: iyileşen / bağlantısız servis "olası neden" olamaz (v0.10.1063)

**Operatör (prod, alarm-kuralı Problem'i, bff p99 3872 ms > 3000):** panel "LIKELY CAUSE: Co-moving with
svc-b (score 404) — possible upstream / downstream propagation" diyordu; svc-b'nin hatası %76.8 → %0'a,
trafiği −%99.5'e, p99'u −%93.7'ye inmişti. "<svc-B> ile ilgili olduğunu düşünüyor ama alakasız."
**Kök neden:** "What else changed" bileşik skoru yönsüz büyüklük (hata düşüşü skorun ¾'ü) ve topolojiden
habersiz; manşet skoru ≥ 20 olan ilk satırı yayılım diye ilan ediyordu. **Karar:** manşete yalnız
sunucunun `causeEligible` işaretlediği satır çıkar. (KENAR) özneyle analiz penceresinde (taban + cari)
DOĞRUDAN `topology_edges_5m` kenarı (kuyruk üzerinden bağ sayılmaz). (YÖN, konuma göre) aşağı akış /
iki yönlü aday: `worse` (hata ≥ +1 puan, p99 > +%25, trafik > +%25) ya da `lost` (trafik ≤ −%90);
yukarı akış (çağıran): YALNIZ trafik sıçraması — çağıranın yavaşlaması öznenin etki alanıdır, neden
değil. `better` yalnız hata ya da p99 GERÇEKTEN düştüğünde ("iyileşti"); yalnız trafik düşüşü (%25–%90)
`quieter` ("trafik azaldı"); NaN delta `unknown` (asla better). p99 artışı hacim kapısız. Yön
`scoreChangedService`'te, kenar `MarkCorrelationCauses`'ta — yeni sorgu yok, servis haritasının özne
odaklı okuması (`GetServiceGraphTopN`, tavan 500; servissiz problemde okuma yok). Skor DEĞİŞMEZ;
/rootcause 50'lik havuzu işaretler, 20 tavanını SONRA keser (sıra: uygun → diğer → iyileşen /
sakinleşen). Uygun satır yoksa "localized" manşeti + topoloji okunduysa "hiçbiri bağlı ve kötüleşen
bağımlılık değil", okunamadıysa "bağlantı doğrulanamadı" (`topologyKnown`). Ribbon canlı adayları da
uygun-önce, better/quieter hariç. Hipotez işçisi, RCA kalkanları ve hakem kataloğu (`rca/extras.go`)
değişmedi. Trafiği kesilen ama aşağı akışta BAĞLI servis uygun kalır — hata oranının ~0'a inmesi onu
dışlamaz: tamamen sönen servis de 0 span'le "%0 hata" okunur, kesici (circuit breaker) açan çağıranın
arkasındaki bağımlılık da temiz bir damla gösterir; ikisi de gerçek neden olabilir.

## 2026-10-03 — Alarm problemi detayı: tetiklenen metriğin grafiği (v0.10.1064)

**Operatör (prod, "HTTP P99 latency >3s (sustained 10 min)"):** "grafik olmadığı için de anlamak çok zor
artışları". **Karar:** span-metrik alarm kuralının problem detayında sol kolonun ilk bölümü tek grafik:
kuralın metriği · servis, eşik kesik çizgi, başlangıç "başladı" bölgesiyle (CorePanelMulti); açıklama
paragrafı yok. Seri yeni `GET /api/alert-rules/{id}/series`'ten: değerlendiricinin KENDİ kaynağı ve süzgeci
(`measureAllServicesPlan` ikizi, parite testli) ve kayan penceresi (kuralın `WindowSec`'i, SQL'de `-Merge`
pencere işlevi) — çizgi eşiği problemin açıldığı yerde keser. Kural editörünün önizlemesi kullanılmadı: yalnız
dört temel metriği tanıyor, spanmetrics'ten ve kova başına okuyor. http_/db_/rpc_ değerlendirici gibi ham
spans'tan (tek servis, ≤6 sa); temel RED + mq_* MV'den (≤24 sa). Log sorgusu, watcher, hedefli kurallar ve
dedektör problemleri (anomali, SLO, runtime …) grafik almaz — sahte grafik yok.

## 2026-10-03 — CoSRE'ye sor: ulaşılamayan "investigation" hattı silindi (v0.10.1065)

Operatör onaylı: "CoSRE'ye sor eskisi gibi" (prod) — v0.10.1036'nın iki adımlı planının ikinci adımı. Uçtan
erişilemeyen v0.10.948 trace incelemesi bütünüyle silindi; uyumluluk katmanı, bayrak yok. **Arka uç:**
`trace_investigate.go` + testi; `trace_explain_handler.go`'da `explainTraceInvestigation`,
`traceInvestigationPrepared`, `invAnswerWithTail`; `deliverExplainPrepared` çekirdeği (hazırlık adım olayları,
ikinci anahtar, `onStore`, eklerin kendi linkleri) — `deliverExplain` yine v0.10.947'deki tek çıkış;
`systemTraceInvestigation` / `SystemPromptTraceInvestigation` (+ `promptVersionRegistry`, dil sicili, istem
testi). Kayıttan düştüğü için global istem sürümü (`ai_calls.prompt_version`) bir kez değişir. **Ön yüz:**
`ExplainSteps.tsx`, `ExplainEvidence.tsx`, `investigationSteps.ts`; `api.ts` `onStep` / `explainStepFrame`;
tipler `ExplainSourceStatus`, `ExplainStepEvent`, `ExplainTraceAnswer.sources`; `explainAnatomy`'de
`splitSourceFooter` ve Bulgu / güven satırı şekli (Karar kuralı v0.10.947'deki hâline döndü); CSS `cx-step*`,
`cx-sources`; trace açıklamasının `?span=` odağı (`copilotExplainTrace` 4. argümanı, `traceUrlSpan`) ve ona ait
testler. **Kalan:** klasik varsayılan + "Kodu da incele" (`explainTraceClassicPrepared`, `writeExplainPrepareErr`,
`explainPrepared` dörtlüsü), takip sohbeti (`TraceFollowUpAddendum`, kıyas penceresi `traceCompareWindow` olarak
`chat_trace_followup.go`'ya taşındı), sohbet `read_source_code`, `StateBadges`, Durdur. Klasik varsayılanı
pinleyen testler (`trace_explain_default_test.go`, `CopilotExplain.classicDefault.test.tsx`) yerinde.

## 2026-10-03 — Exception oluşum çubukları OTel sarısı, sayım ekseni tam sayı (v0.10.1066)

**Operatör (prod, exception detayı):** "Barlar eskiden sarı renkteydi, OpenTelemetry sarısında yine öyle olsun." +
"Occurrences decimal yazıyor, düz adet yazsa daha iyi."
**Kök:** çubuk rengi `statusColor('warn')` idi — açık temada `--warn` koyu zeytin (#8f5c04); sayım bir durum değil.
Sol eksen bölmeleri `[0, max/2, max]` ve headroom'lu max → "0 · 0.55 · 1.1".
**Karar:** `OTEL_YELLOW` (#f5a800, logo rengi) sabit; `TimeChart.leftInteger` → bölmeler tam sayıya yuvarlı ve tekilleşmiş
(`integerSplits`). Yalnız bu grafik `leftInteger` kullanır; oran/süre eksenleri değişmedi.

## 2026-10-03 — OIDC girişi Settings'ten yönetilir (v0.10.1067)

**Operatör:** "Settings'ten yönetebilsem Helm'e göre daha iyi olur." OIDC yalnız `config.yaml`
`auth.oidc` (Helm `config.auth.oidc` + `oidcClientSecret`) ile kuruluyor, keşif boot'ta bir kez koşuyordu;
değişiklik = Helm upgrade + restart. **Karar:** ayar `system_settings` `auth_oidc` blobunda (invariant #6);
Settings > SSO formu + `GET`/`PUT /api/settings/oidc` + `POST /api/settings/oidc/test`, üçü de admin.
**Öncelik:** kayıtlı blob varsa `config.yaml`'ın `auth.oidc`'sinin önüne geçer; blob yoksa `config.yaml`
kaynak kalır (Helm'li kurulumlar aynen çalışır, form etkin değerlerle dolu açılır). **Secret:** `client_secret`
hiçbir GET'te dönmez (`clientSecretStored`); PUT'ta boş secret kayıtlıyı (yoksa `config.yaml`'dakini) YALNIZ
issuer + client id değişmemişse korur, değiştiyse 400 (yoksa tek girişle kayıtlı secret yeni token ucuna
giderdi); audit'e yalnız `clientSecretChanged` bayrağı. **Canlı uygulama:** PUT keşfi koşar ve istemciyi atomik
takaslar (restart yok, giriş düğmesi aynı anda); keşif başarısızsa kayıt reddedilir — `enabled:false` her zaman
kaydedilir (kapatma anahtarı). Peer pod'lar `config:oidc` sinyali + 30 s değişim tespitli yenilemeyle alır;
issuer aynıysa yeniden kurulum ağsız (alan adı/rol değişikliği peer'da hemen), uygulanamayan yapılandırmada
istemci kaldırılır (`active:false`). `system_settings` okunamazsa SSO kapalı başlar, `config.yaml`'a düşülmez.
**Ağ sınırı (Settings kaynağı):** keşif + JWKS + token değişimi tek sınırlı istemciyle — https, dial anında
loopback / link-local (169.254/16 metadata dahil, fe80::/10) / belirtilmemiş / multicast / eşlemeli adres reddi,
ad çözülüp IP'ye sabitlenir (http/loopback yalnız yaml `allow_insecure_issuer`, dev), ≤10 s, ≤3 yönlendirme,
gövde ≤1 MiB, sorgulu uç reddi; hata metni tek genel cümle; giriş hatasında kullanıcıya genel mesaj, loga yalnız
sınıf (IdP gövdesi hiçbir yere). **Özel ağ (RFC1918, fc00::/7) serbest:** operatörün IdP'si banka ağı içinde;
bir Helm anahtarı istemek "Settings'ten yönet"i boşa çıkarırdı. Bedeli kabul edildi: oturum admin'i keşif
üzerinden özel bir https ana makinesinin erişilebilir olup olmadığını öğrenebilir — admin zaten küme/ağ
erişimine sahip, loopback ve metadata kapalı, gövde/durum kodu hiç dönmez, ve test ucu audit'lenir
(`settings.oidc.test`: issuer + ok, gövde yok). Üç uç yalnız oturum admin'i (API token 403). viewer dışı varsayılan rol izinli alan adı ister; `default_role: editor` artık editor
verir (callback eskiden admin dışını viewer'a indiriyordu). **Güvenlik sıkılaştırması:** id_token'da
`email_verified` VARSA ve false ise giriş reddedilir (her kaynakta). Yedek dışa aktarımı OIDC secret'ını da
taşır (LDAP/Tempo gibi). Yerel kullanıcı/parola girişi her zaman açık.

## 2026-10-03 — Tablolar kaba sığar: bütçeli sığdırma, pinned taşmadan önce küçülür, sütun önceliği (v0.10.1068)

**Operatör (prod, ~1440px laptop, sidebar açık, Exceptions):** "Kolonlar kayıyor, sığmıyor; sayfa responsive
değil ve bu hemen hemen her tabloda böyle. Kötü bir deneyim." **Ölçüm (sentetik veri, 1280–1680 px, sidebar
açık/kapalı):** boş localStorage'da Rollouts her genişlikte, GitOps 1280–1366'da taşıyordu; Exceptions taşmıyordu
ama Exception kolonu 40–72 px'e eziliyordu. Kalıcı (sürüklenmiş) genişlikle Exceptions 1366 ve 1440'ta taşıyordu
(scroll 1412 px, kap 1178 px; diğer kolonlar 48 px'te). **Kök (paylaşılan primitif):** (1) sığdırma tabanı düz 48 px
(okunmaz ama "sığmış"), esneyen kolon da 48 px'le yetiniyordu; (2) v0.10.1057 sürüklenen kolonu hiç küçültmüyordu;
(3) tabanlar sığmayınca tek çare yatay kaydırmaydı. **Karar — `fitColumnWidths` sözleşmesi:** a) beyan sığıyorsa
dokunulmaz; b) sürüklenmemişler oransal küçülür, esneyen kolon (beyan `flex` ya da yoksa en geniş metin kolonu)
tabanını korur; c) **v0.10.1057 REVİZYONU:** sürüklenen genişlik SIĞDIĞI sürece aynen kazanır, sığmazsa
sürüklenmemişler tabana indikten sonra o da küçülür — taşmaz (sürükleme çizilen genişlikten başlar, sonraki tık
yutulur, kalıyor); d) tabanlar sığmazsa yeni `priority` alanına göre kolon gizlenir (1 asla; büyük önce; eşitlikte
sağdaki), başlıkta "+N sütun" belirir, oradan geri açılan kolon aynı storageKey'de kalıcı; e) yalnız öncelik-1
tabanlar sığmazsa taşma. Taban (`fitFloor`): `minWidth`, yoksa beyanın %60'ı, esneyen kolonda 160 px. Gizlenen
kolonun başlığı ve `<col>`u basılmaz, gövde hücresini primitif tam hücre sayılı satırlarda CSS ile düşürür (sayfalar
`<td>` atlamaz); colSpan'lı detay satırı için 0 px yuva; birden çok esneyen kolon kalanı tabanlarıyla orantılı
paylaşır. Kırpılan hücreye `title` otomatik. **Sayfalar:** Exceptions, Problems inbox (+ alert-rules), Anomalies,
Messaging/DB bağımlılık tablosu, Rollouts, GitOps, Slow queries, Cluster pod'ları — taban/öncelik; listelerde
yılsız damga (`tsCompact`, tam damga title'da). Ölçüm sonrası 15 sayfa × 10 genişlikte taşma yok.

## 2026-10-03 — Yerleşik alarm kuralları varsayılan kapalı (v0.10.1069)

**Operatör (prod, Alert rules sayfası):** "Built-in alertleri kaldıralım, çok false pozitif geliyor." Son 24 saatte
"HTTP P99 latency >3s (sustained 10 min)" 340×, "HTTP P99 latency >5s (5 min)" 215× açılmış; ikisinden 26 + 12 açık
problem vardı. Binlerce servisin gecikme profili çok farklı — filo geneli mutlak eşik gürültü.

**Karar — varsayılan değişikliği, özellik kaldırma değil:** dokuz `builtin-*` kuralı `Enabled:false` gemiye biner
(`internal/evaluator/evaluator.go` `builtins`); tanımlar ve satırlar durur, Alert rules sayfasında BUILT-IN · OFF
listelenir, operatör istediğini açar. **Yükseltme:** tek seferlik göç (`builtins_default_off.go`), lider tikinde,
`evaluateAll`'dan önce: açık her yerleşiği kapatır, yerleşiklerin açık/ack problemlerini normal kapatma yolundan
(`MarkResolved` + `UpsertProblem`, Value ezilmez) "rule disabled by default v0.10.1069" gerekçesiyle kapatır — bugün
kapalı kuralın problemi ~3 dk sonra bayat süpürmeyle "source silent" diye (yanlış gerekçe) kapanıyordu; incident'lar
aynı tikin kaskadında kapanır. Tek audit satırı (`alert_rule.builtin_default_off`, aktör `system`, kapatılan id'ler).
İşaret `system_settings` → `builtin_rules_default_off`; varken göç bir daha koşmaz → operatörün sonradan açtığı kural
yeniden kapatılmaz (`deprecatedBuiltinIDs` her boot'ta kapatır — bilinçli fark). Hata yönü: işaret okunamaz ya da bir
yazım düşerse işaret yazılmaz, sonraki tik dener; yeniden deneme çift audit/kapatma üretmez. Yeni kurulumda göç yalnız
işareti yazar. "Noisy rules" raporu ve paneli kapalı kuralları düşer (yoksa zaten kapalı kurala "Disable" önerirdi).

**Bedel:** P1 "critical × 2" yolu (`computePriority` critical + `bigBreach`) kural tarafında çoğunlukla beş yerleşik
critical kuraldan besleniyordu; artık nadiren tetiklenir. Eşik alarmı operatörün kendi (servis bazlı) kurallarından,
SLO burn ve anomali hattından gelir. Varsayılanı yeniden açmayı önerme.

## 2026-10-03 — Davranış değişimi: mutlak taban + sıçramalı geçmiş toleransı (v0.10.1070)

**Operatör (prod, `behavior_change`):** "Bu da mesela false pozitif." Başlık: "svc-gate servisinde p99 gecikme
normalin 28 katına çıktı (4.26ms → 118.51ms)". Grafik: haftalardır p99 ≈ 4 ms, birkaç dakikada bir ~100 ms'e kısa
sıçramalar. İki kusur: mutlak taban yoktu (118 ms p99 kimsenin sorunu değil) ve kovanın kendi üst bandı bu
sıçramaları zaten içerirken medyan+MAD onu görmüyordu.

**Karar (onaylı, `anomaly_sensitivity.behavior` altında üç vida):** `minP99Ms` (vars. 200 — op-latency'nin
`opLatencyMinP99Ms`'iyle aynı sayı), `minErrorRatePct` (vars. %1): son dilim tabanın altındaysa aday yok — rejim ve
mevsimsel dal aynı `evalBehaviorWindow`'dan geçer, yön fark etmez (tabanın altına inen değer de olay değil; tabanın
üstündeki düşüşler aynen raporlanır); `request_rate`'te taban yok. `spikyBandFactor` (vars. 1.5): yukarı yönlü HER
dilim kendi haftanın-saati kovasının p90'ının bu katını aşmalı (z / oran testlerine ek). p90, medyan+MAD ile aynı
örnek kümesinden Go'da hesaplanır — sorgu zaten ham 5 dk satırlarını döndürüyor, SQL değişmedi. Saklanan `baseline`
alanı medyan kalır; UI'ya ek metin yok. Eski blob alanları taşımaz → Normalize varsayılanı yazar (0 meşru değil).

**Bedel:** sıkı bir kovada 1.3× yükseliş artık mevsimsel aday değil (1.5×p90 altında) — mevsimsel sinyal yukarı yönde
fiilen "rejimden önce, 15 dk'da görülen belirgin kayma"ya daraldı; düşüş tarafı değişmedi. Pin:
`behavior_floor_test.go`. Tabanları kaldırmayı önerme; ihtiyaç varsa Settings → Anomaly → Davranış değişimi.

## 2026-10-03 — Log deseni: "Logları aç" dedektörün kendi yüklemiyle açılır; sistem adı token'ı kaldırıldı (v0.10.1071)

**Operatör (prod, ES):** "External system rejected" grafiği ~24 bin sayarken "Logları aç" 80 bin ilgisiz DEBUG/INFO
satırı açtı; ayrıca "'OR <sistem adı>' ibaresi yanlış olmuş, o bir hata değil." (ad depo kuralı gereği yazılmadı).
**Bulunan neden:** 1062 pivotu deseni arama DİLİNE çeviriyordu (`q="t1" OR …`) — o metin dedektörün yüklemi değil:
CH'de token'ların harf-duyarsız alt-dize OR'u (dedektör ayrıca büyük-küçük duyarlı regex ister → üst küme); ES'te
gövde yan tümcesi Lucene anlamında eşdeğer ama başka bağlamda (`expandShorthand`, `default_operator` AND, `must`,
düzenlenebilir kutu) ve başka pencereyle (pivot `anomalyChartWindow`, grafik basamağa hizalı ≥1 sa öncesi). İlgisiz
satırların kaynağı token'ın kendisi: ES dedektörü regex'i hiç uygulamaz, yalnız token'larla sayar; bir sistem ADI olan
token o adın geçtiği her satırı desene katıyordu. **Karar:** (1) çeviri yok — `/logs?pattern=<küratörlü ad>`; mevcut
okumalar (`/api/logs`, `/search`, `/timeseries`, `/fieldstats`, `/patterns`, `/stream`) `pattern` alır, sunucu
`LogPatternSpecByName` → `Filter.Pattern` → ES `patternQueryStringClause` (CountPatterns/grafikle aynı harita) / CH
`chPatternMatchSQL`; serbest metinle AND, bilinmeyen ad 400, önbellek anahtarları desenli. Sayfa çip gösterir ("desen:
…", ×), kutu boş kalır; desen etkinken Kibana bağlantısı çizilmez (KQL'e çevrilemez). `logsQuery` /
`PatternSearchText` silindi; /anomalies "logs ↗" ve AI kartının "Loglar (desen)"i de `pattern=` (`PatternKQL` silindi). (2) Sistem adı alternasyondan ve token'dan çıktı,
testte adı `payments-bpm` sentetiğiyle değişti.

## 2026-10-03 — P1 görünürlüğü: inbox dar istisna listesi + regressed grupta yeniden açılış hacmi (v0.10.1072)

**Operatör (prod):** "Dün akşam CRM database'inde sorun oldu ama problemlerde P1 gelmedi." İki ayrı kusur:
kritik hata oranı anomalisi kaynağında P1'di (`computePriority`: critical + 14.9× ≥ 2×) ama inbox v0.9.487 görünüm
kuralıyla P3 gösterdi; 54.812 oluşumlu bir exception grubu `regressed` olduğu için P2 kaldı (regressed dalı hacim
kapısından ÖNCE dönüyordu — "kazanılmış P1 düşmez" bu grupta geçerli değildi). Operatör onayı: "go".

**A — v0.9.487 KISMİ REVİZYONU:** "exception dışı türler inbox'ta HEP P3" kuralı DAR bir istisna listesiyle
gevşedi: `problem_priority.inboxKeepSourcePriority` (glob; `*` her dizi, tam eşleşme; varsayılan
`anomaly:*:error_rate`, `builtin-*`, `db-health:*`, `incident:critical`). Kimlik: Problem'de RuleID, incident'te
`incident:<severity>`. Uyan satır kaynak P1/P2'sini korur, gerekçe "kaynak önceliği korundu (kritik hata oranı) · …".
Bilinçli olarak DIŞARIDA: trace_op / trace_op_latency / log_* / behavior_change / `anomaly-auto:*` / SLO burn /
yavaş ifade / self-health — v0.9.487'nin susturduğu gürültü onlar. Kural facet sayaçlarından önce (sıra aynı).
nil = varsayılan, `[]` = saf v0.9.487; geniş kalıp 400 (ilk `*`'tan önce literal önek + en az 3 literal karakter
şart — `*:*` reddedilir; 20 tavanı tekilleştirilmiş listede). PUT gövdesi KAYITLI değerin üstüne çözülür: alanı
göndermeyen istemci kayıtlı listeyi (`[]` dahil) ezmez. Okuma hatasında son iyi değer korunur; PUT inbox önbelleğini
düşürür, anahtar `inbox:v9`. Ayar ekranında salt-okunur. `builtin-*` operatörün `builtin-` ile başlayan kendi kural
kimliklerine de uyar — kabul edildi. Kod: `chstore/problem_priority_inbox.go`, `api/inbox_keep_priority.go`.

**C — regressed + yeniden açılış hacmi ⇒ P1:** resolve (manuel ya da bayat süpürme) anında `occurrences` anlık
görüntüsü `exception_groups.occurrences_at_resolve`'a yazılır (ALTER ADD COLUMN, iki-boot probe
`hasExResolveSnapCol`). Regressed grupta `occurrences − occurrences_at_resolve ≥ p1MinOccurrences` (500) ⇒ P1
"yeniden açıldıktan sonra ≥500 oluşum"; değilse P2 "regressed". Kapıyı ömür boyu toplamla regressed'in üstüne
taşımak her kronik grubu P1 yapardı — reddedildi. Bu sürümden önce çözülmüş (anlık görüntüsüz) grup P2'de kalır.
Yükseltme yalnız GÖRÜNÜM/öncelik değişikliği: exception bildiricisinin tekilleştirmesi (`<fp>:regressed`,
`notify/exception_notifier.go` routeGroups) regresyonda bir kez gönderir, yani grup sonradan 500'ü aşıp P1 olunca
yalnız-P1 kanallar bildirim ALMAZ. Bildirici bu sürümde bilinçli olarak değişmedi.

## 2026-10-03 — Veritabanı sağlık kuralı (db-health): hata oranı / p99, ≥2 çağıran (v0.10.1073)

**Operatör:** "Dün akşam CRM database'inde sorun oldu ama problemlerde P1 gelmedi." 2026-10-02 ~21:30–22:30: bir
veritabanının hata oranı ~%0 → ~%10 (~15 dk), p99'u ms → ~5 s; çağıranların p99'u 8 s, ardından 55k HTTP 503.
Veritabanı öznesinde hiçbir şey açılmadı: db-capacity yalnız doyma gauge'u, db-slow-stmt tek ifade, `db_p99_ms`
yerleşikleri çağıran başına ve varsayılan kapalı (v0.10.1069). Operatör onayı: "go".

**Karar:** yeni evaluator pası `evaluateDBHealth` (db-slow-stmt'ten sonra, lider tiki). 2 ardışık 5 dk kovada ihlal:
db hata % ≥ 5 (MUTLAK) YA DA p99 ≥ 2000 ms VE ≥ 3 × aynı veritabanının 24 sa önceki aynı kovası (GÖRELİ). p99 bilerek
göreli: aynı günün v0.10.1069 kararı filo geneli mutlak gecikme eşiğini gürültü saydı; raporlama veritabanları gün boyu
2 s üstünde. Dünkü kova yoksa (yeni DB, dün veri yok) p99 boyutu o kova için KAPALI, hata % çalışır. Kova başına ≥ 100
çağrı VE ≥ 2 etkilenen çağıran servis. Etkilenen çağıran: kovada ≥ 10 çağrı yapmış ve kendi hata %'si ya da p99'u
tabanı aşmış (çağıran p99'u mutlak). Batch kalıbına uyan çağıranlar hem sayımdan hem agregeden düşer. Eşiğin 2 katı
critical → `computePriority` P1; tazelemede warning → critical yükselirse yeniden bildirim. Açılış, çağrı tabanını
geçmiş yarım cari kovayı kullanabilir. Kapanış yalnız son iki TAMAMLANMIŞ kovaya bakar (temiz = iki boyut ihlalsiz ya
da kova çağrı tabanı altında/verisiz) ve iki ardışık okumada (tik) temiz ister. Düşük hacimde sonsuz tutma (4 sa sonra
bayat-critical P1) yok. Ayar hiç okunamadı, ana okuma ya da referans okuması düştü → açık problemler yalnız tazelenir
(süpürme olay ortasında P1'i "source silent" diye kapatmaz). Kural kapatılınca açıklar "rule disabled" gerekçesiyle
kapanır (v0.10.1069 emsali).

**Okuma:** `db_caller_summary_5m` (Databases detayıyla aynı MV, çağıran boyutu olan tek db MV'si). Tik başına tek
sorgu: iç sorgu çağıran başına tDigest durumu (-MergeState + finalizeAggregation, tek birleştirme). `HAVING` yalnız
tabanı aşan satırları ve AÇIK problemlerin tüm satırlarını geçirir (açık kural id'leri bağlı dizi). Böylece kesik
okumada düzelmiş DB'nin temiz satırı düşmez; açık DB okumada hiç yoksa (gerçekten verisiz) temiz sayılır. LIMIT 20000,
kesik okumada hiçbir şey kapanmaz. p99 adayı varken ikinci sorgu `db_summary_5m`'den dünkü 3 kovayı okur (aday kural
id'leriyle süzülü, LIMIT, max_execution_time). Referans tüm çağıranları kapsar, ana okuma batch'siz: küçük tutarsızlık
kabul. Ham spans yok. **Maliyet notu:** iki MV'nin ORDER BY'ı `time_bucket` ile BİTİYOR; zaman budaması yalnız
partition (`toDate`) + minmax. Prod'da güvenmeden önce `system.query_log` `read_bytes` ölçülmeli. Takip (bu sürümde
yok): iki adımlı okuma — önce `db_summary_5m`'den aday DB'ler, sonra yalnız onlar için `db_caller_summary_5m`.

**Kimlik:** kural id `db-health:<system>@<instance>/<db>` (detay sayfasının üçlüsü). Özne gerçek db.name varsa
`db:<sys>@<db>`, yoksa instance biçimi. Vidalar `db_slow_query` blobunun `health` alanında (yeni anahtar yok, son iyi
değer korunur); tik başına ≤ 20 yeni açılış. Problem detayı "Veritabanı sayfası" ve "Trace'ler" pivotlarını kural
id'sinden kurar; alarm metrik grafiği bu türde çizilmez.

**Bedel (kabul):** `db:<sys>@<db>` öznesi aynı db adlı farklı instance'larda (ör. prod/staging) çakışır; incident'lar
birleşebilir. `db:` öznelerinin topoloji komşusu yok, bu yüzden problem çağıranların incident'ına katılmaz. Çağıran
"etkilenen" bayrağında p99 mutlak (kronik yavaş çağıran, hata % ihlalinde çağıran kapısını doldurabilir). db.name'inde
'/' olan veritabanının id çözümü instance'a kayar.

## 2026-10-03 — Takip sohbeti prompt'u: olmayan "K bölümü" atfı kaldırıldı (v0.10.1075)

**Kusur:** v0.10.1065 incelemeyi silince trace takip sohbetinin AKTİF BAĞLAM satırı hâlâ "kıyas penceresi (ilk
cevabın K bölümüyle AYNI …)" diyordu; klasik ilk cevapta (İşlem Akışı ve Veri Özeti / Stacktrace Detayı / Kök Neden
ve Sonraki Adım) böyle bir bölüm ve dönem kıyası yok — model olmayan bir bölümü arayabilir ya da ona atıf yapabilirdi.
**Karar:** satır pencereyi kendi tarifiyle verir: "kıyas penceresi (trace ortalı; ilk cevapta dönem kıyası yok —
compare_periods'ta bunu kullan, reference=previous)". Yalnız metin: pencere hesabı (`traceCompareWindow`), girdiler,
araçlar ve `TraceFollowUpAddendum` aynı. Satır `chat_trace_followup.go`'da çalışma-zamanı önsözü olduğundan
`promptVersionRegistry`'de değil; global istem sürümü değişmez. `TestTraceFollowUpCompareWindow` "K bölüm" yokluğunu pinler.

## 2026-10-03 — Logs: ilk yüklemede süzgeçsiz istek yok (v0.10.1076)

**Kusur:** `/logs?q=…&pattern=…&service=…&range=…` derin linki (anomali "Logları aç", kayıtlı görünüm,
paylaşılan link) önce SÜZGEÇSİZ bir liste + histogram isteği atıyordu, sonra süzgeçli olanları. Kök neden
efekt sırası: `filter`/`filters` boş varsayılanla başlıyor, URL içe aktarma efekti ilk commit'ten SONRA
koşuyordu. O commit'te `useLogs` sorgusu ve `LogsHistogram`'ın fetch efekti varsayılan durumla ES'e
gitmişti. Pencere (`useUrlRange`) ve env zaten senkron okunduğu için istek doğru pencerede ama süzgeçsizdi.
Milyar-belge indekste her derin linkte boşa giden bir tam-pencere sorgusu. **Karar:** süzgeç durumu
(`filter`, `filters`, `cols`) ilk render'da URL'den kurulur. Okuma `lib/logsUrl.ts`
`readLogsUrlState` içinde, efektle ortak. Sig-guard'ın ref'i ilk sig ile başlar, bağlama anındaki içe
aktarma no-op olur; sonraki URL değişimleri (geri/ileri, kayıtlı görünüm) eskisi gibi içe aktarılır. URL
şeması değişmedi. Çıplak `/logs` davranışı aynı (tek varsayılan istek). Test: `Logs.firstLoad.test.tsx`.

## 2026-10-03 — Grafik: "<" eşiklerinde gölge altta, dar "başladı" bandında etiket solda (v0.10.1077)

**Eşik gölgesi:** `drawThresholds` ihlal bandını her zaman çizginin ÜSTÜNE boyuyordu; `request_rate <` gibi
kurallarda ihlal ALTTA, grafik ters tarafı işaretliyordu. `ChartThreshold` / `Threshold`'a `side?: 'above' | 'below'`
(varsayılan `above` — mevcut çağıranlar birebir); yön `comparatorSide`'dan (`lib/chart/thresholdLines.ts`): `<` / `<=`
→ `below`, gerisi `above`. Alarm problemi grafiği (v0.10.1064, `alertThreshold`) ve kural editörü önizlemesi
(`ConditionPreview` → MultiLineChart → CorePanel) aynı kapıdan geçer. CorePanel + TimeChart yönü taşır (CorePanel'in overlay
imzası zaten tüm eşik nesnesini, `chartBuildSig` eşik özeti artık yönü de içerir); TimeSeriesPanel'in kendi `TSThreshold`'u değişmedi (karşılaştırıcısı yok).
**"başladı" etiketi:** ~2 dk açık problemde başlangıç bandı (v0.10.1060 deseni) sağ kenarda ince bir şerit; etiket
sığmadığı için `fitLabel` onu susturuyordu. `drawTimeRegions` artık bant içine sığmayan etiketi bandın SOLUNA, bant
başına sağdan hizalı yazar (`regionLabelPlacement`, saf) — yalnız çizim alanının içinde ve aynı şeritte soldaki bandın
bitişini aşmıyorsa; yoksa eski yol (kısalt / sustur). Renkler, şerit ve isabet satırı değişmedi; anomali bantları da
aynı kuraldan yararlanır.

## 2026-10-03 — Exception bildirici: regressed grup P1'e yükselince bir kez bildirir (v0.10.1078)

**Boşluk (v0.10.1072):** regressed grup yeniden açıldıktan sonra ≥500 oluşumda P1 oluyor, ama bildirici
`<fp>:regressed`'i ilk (P2) değerlendirmede gönderilmiş sayıyordu; yalnız-P1 kanallar yükselmeyi hiç duymuyordu.
Operatör onaylı.

**Karar:** yeni anahtar `exception-group:<fp>:regressed:p1:<epoch>` (epoch = resolve anı, sn; damgasız eski satırda
`…:p1`). Grup regressed iken önceliği P1 olunca regresyon başına BİR kez gönderilir; kanallar P1 ve kendi
minPriority'leriyle değerlendirir, şablon aynı, gerekçe "yeniden açıldıktan sonra ≥N oluşum". Sonraki bir regresyonda
500'ü yeniden aşan grup gerçek bir P1'dir, yine bildirilir. Regresyon zaten P1 başlarsa yalnız `:p1` gider ve taban da
gönderilmiş sayılır (P1 kanala çift yok). Dedup eskisi gibi lider-yerel defter + `notification_log`
(`HasAnyNotification`), restart çift göndermez; P2 tiklerinde `:p1` için CH okuması yok. Yükseltme tabanın Sustur'unu da
dinler. Pin: `notify/exception_regressed_p1_test.go`.

**Taban anahtar DEĞİŞMEDİ (bilinçli):** `<fp>:regressed` damgasız, 90 günde bir; Sustur anlamı aynı. Operatör önceliği
gürültü azlığı: her gün resolve/regress olan kronik bir grup her regresyonda bildirim üretmemeli. Taban için regresyon
başına damga reddedildi. Bilinen kenar: P1 başlayan bir regresyonda taban yalnız bellekte kapanır (log'da yalnız `:p1`
var), bu yüzden 90 gün içindeki SONRAKİ bir P2 regresyonu tabanı bir kez gönderir.

## 2026-10-03 — Exception detayı: stack trace / pods / sample traces kolonları eşit (v0.10.1079)

**Operatör:** "Stack trace'le pods · nodes ve sample traces panelleri orantısız gözüküyor. Eşit olsa sayfaya daha
iyi sığar." **Karar:** `.pd-cols-14` (1.4fr 1fr, v0.8.61) → `.pd-cols-11` (1fr 1fr); telefon katmanı tek kolon
aynen. Anomali detayının `.pd-cols-15`i (grafik 1.5fr / "Ne yapabilirim" 1fr) değişmedi.

## 2026-10-03 — ES log desenleri: token eşleşmesi örneklemle regex'e karşı doğrulanır (v0.10.1080)

**Operatör (prod, ES):** "Oracle TNS error diyor ama loglarda öyle bir şey yok, hatalı desen buluyor." Desen
filtresiyle açılan 13 satırın hiçbiri `TNS-NNNN` değildi. **Neden:** ES dedektörü `message:"tns-"` ile sayar;
standart çözümleyici tireyi atar, ifade çıplak `tns` terimidir ve regex ES'te hiç uygulanmaz (CH uygular).
**Karar:** sayım sonrası, tetiklemek üzere olan adaylar (oran sırasıyla tik başına ≤10) tek `_msearch`'le
örneklenir (`logstore.VerifyPatterns`: size/terminate_after 50, `_source` yalnız gövde, `_doc` sırası, ≤5 s,
request_cache) ve `PatternSpec.Regex` Go'da CH `match()` anlamıyla (`(?s)`, harf duyarlı) uygulanır. r = 0 →
olay yazılmaz (desen başına saatte bir log); 0 < r < 1 → cur ve taban r ile ölçeklenip eşikler yeniden sınanır
(taban da token sayımı: spike oranı korunur, mutlak tabanlar tahmini sayıya uygulanır); r = 1 → aynen. Örnek
satır regex'e uyan gövdeden gelir. Tetiklemeyen desen için istek yok; örnek alınamazsa sayım aynen. Oran
`anomaly_events.verified_ratio`'da (probe'lu ALTER, iki-boot); terfi açıklaması "örneklemde %r regex
doğrulandı", grafik alt başlığı "token eşleşmesi · örneklemde %r doğrulandı" (yalnız r < 1). Grafik ve
`pattern=` pivotu token tabanlı kalır. CH yolu değişmedi (`VerifyPatterns` sorgusuz nil). Oracle token'ları
değişmedi: standart çözümleyicide "tire + rakam" AND'i kurulamaz, örnekleme yeterli.

## 2026-10-03 — SLO burn-rate problem üretmez (varsayılan kapalı); Problems varsayılanı yalnız P1, first seen sıralı; drawer kalktı, incident sayfası okunur (v0.10.1081)

**Operatör (prod, dört istek):** "SLO burn rate problem olmasın, çıkar. SLO ile ilgili beklentim yok." · "Problems
sayfasında sadece P1'ler gözüksün ve first seen'e göre sıralı olsun" · "Drawer çıkmasın, problem sayfasında direkt
içeriğine girebileyim, Exceptions sayfası gibi." · "ekteki hata mesela hiç anlaşılmıyor" (db-health problemiyle açılmış
bir incident: yalnız başlık, "Declared incident, critical" gerekçesi ve dört düğme).

**SLO burn:** varsayılan değişikliği, özellik kaldırma değil (v0.10.1069 emsali). `problem_priority.sloBurnProblems`
(*bool, nil = kapalı; Settings → Anomaly → Alert problemi önceliği, tek satır Türkçe ipucu). Kapalıyken evaluator burn
ölçmez, `slo:*` Problem / incident / bildirim yok; SLO sayfası, burn hesabı ve grafikler aynen. Ayar yayınlanmışken
kapatılırsa açık burn satırları "slo burn problems disabled" ile kapanır; boot okuması düşmüşse açıklara dokunulmaz.
Yükseltme: tek seferlik göç (`evaluator/slo_burn_default_off.go`), lider tikinde `evaluateAll`'dan önce, açık/ack `slo:*`
problemlerini normal kapatma yolundan "slo burn problems disabled by default v0.10.1081" ile kapatır (incident'lar
aynı tikin kaskadında), tek audit `slo.burn_problems_default_off`, işaret `system_settings.slo_burn_problems_default_off`;
bayrak açıksa yalnız işaret. Kapanışlar bildirim göndermez.

**Problems varsayılanı:** v0.10.1014'ün öncelik yarısının tersi — parametresiz `/inbox` yalnız P1, ilk görülmeye göre
en yeni önce (tür varsayılanı her şey kalır). URL durumu (`?prio=`, `?s_inbox=`); kişisel localStorage sıralaması artık
okunmaz, yoksa "parametresiz link = varsayılan" tarayıcıya göre değişirdi. Sunucu öncelik süzgecini tavandan ÖNCE
uygular, sonra ilk görülmeye göre sıralayıp keser (`inboxSortAndCap`, saf, testli) — en yeni P1 kırpılmaz; çip
sayaçları tüm öncelikleri sayar, "tüm öncelikler" tek tık. **Bedel:** parametresiz kaydedilmiş görünümler (1014–NEXT
arası) artık yalnız P1 açar.

**Drawer kalktı:** `InboxTriageDrawer` silindi; her satır tam sayfa açar (incident → `/incident?id=`, geri bağlantı
kuyruğa döner). Eski `?item=<tür>:<kimlik>` linki kimlikten tam sayfaya yönlendirilir (liste beklenmez). Toplu seçim ve
j/k + Enter aynen. **Incident sayfası okunur:** başlığın altında birincil (en erken) bağlı problemin kendi açıklaması +
"ne zaman" satırı, bağlı problemler (her biri kendi grafikli detayına), problem sayfalarıyla aynı üreticilerden "Ne
yapabilirim" (tek problemde ondan — db-health'te Veritabanı sayfası + trace'ler; çoklu problemde incident servisinden),
zaman çizelgesi altta. "Declared incident" / "kaynak önceliği korundu" gerekçesi şeritten ve satırdan çıktı (kapalı
Teknik ayrıntı'da); kuyruk satırı incident özetini (= onu açan problemin açıklaması) basar.

## 2026-10-03 — Traces: Errors + nitelik süzgecinde histogram listeyle aynı kümeyi sayar (v0.10.1082)

**Operatör (prod):** "Error seçildiğinde histogram gelmiyor." `hasError=true` + `function_code = KYC0001` (ya da
`k8s.pod.name = …`) → liste dolu, şerit "0 SPANS · 0 ERROR SPANS"; boş durum "hata aynı trace'in başka bir span'inde"
diyordu. **Neden:** iki yüzey hatayı farklı yerde arıyordu. Liste iki basamaklı (v0.10.1010): çipe uyan hatalı span
varsa o, yoksa trace düzeyi (çip bir span'de, hata başka span'de). Şerit `/api/spans/metric-batch`'te Errors'u span
düzeyinde `status = error` çipi olarak AND'liyordu (`span_metric_batch.go`), giriş kapsamındaki çiplerde (k8s.*)
üstüne `kind IN (server, consumer)`; ayrıca pencere başı listenin 5 dk hizasını almıyordu. Trace kipinde ve hata
istemci/iç span'deyken sayım yapısal olarak sıfırdı.
**Karar:** Errors + span-düzeyi çip (arama / süre / services / trace id yokken) şerit yeni `GET /api/traces/error-histogram`'dan
okur: listenin sorgu dizesi, aynı ayrıştırıcı (`parseTraceFilter`), aynı kip kararı (`traceLevelErrorCandidates`).
Span kipinde listenin probuyla AYNI WHERE (`traceErrBothWhere`, tek yardımcı) kovalanır (birim "spans", tavansız,
eski şeritle aynı maliyet sınıfı); trace kipinde listenin aday kümesi (≤6000 id, `PREWHERE trace_id IN`) trace
başlangıcına göre kovalanır (birim "traces", Root adaylar üstünde, tavanlıysa ipucu söyler). `trace_summary_5m`
nitelik taşımadığı için ham `spans`, zaman sınırı + LIMIT + `max_execution_time` ile. Diğer hâller metric-batch'te
(MV / dar rollup korunur). "Başka span'de" cümlesi yalnız doğruyken: metric-batch yolu + Errors + birim spans + liste
dolu (ör. arama + Errors). Pin: `chstore/trace_error_histogram_test.go`, `api/trace_error_histogram_test.go`,
`pages/traces/scopeParams.test.ts`, `volumeSeries.test.ts`.

## 2026-10-03 — Oracle hataları P1'e ulaşır: tablo yolu taban+süre+istisna listesi, db-health mutlak hata sayısı kolu (v0.10.1083)

**Operatör:** "Oracle hataları da problemse hâlâ düşmüyor" → onay "Yap". **Kök neden (iki yol):** (1) Oracle hata
tablosu serisi seyrek, taban medyanı 0; Problem.Threshold = medyan = 0 iken `computePriority` oran kuramıyor, critical
satır kaynakta P2'de kalıyordu; üstüne kural id'si (`anomaly:ext:…:ext:error_count`) inbox istisna listesinde
olmadığından Problems listesinde P3'e çiviliydi. (2) Span tarafındaki ORA patlaması db-health'in hata yüzdesine
(tüm veritabanının çağrıları üzerinden) yansımıyordu.

**Karar A — tablo yolu:** `Threshold = max(medyan, MinAbsDelta)` (taban 5/dk; ≥10/dk critical → P1; gerekçe gerçek
medyanı yazar; eski Threshold 0 satırı tazelemede tabana çıkar). Dış küme Problem'i üye sayısıyla değil EN GÜÇLÜ
ÜYENİN değeri/eşiği ile yayılımın (N / 3) BÜYÜĞÜYLE ölçülür — tek güçlü üye de 50 serilik 6/dk fırtına da P1;
gerekçe hangi kolun kazandığını yazar. Sürdürme kaynak ayarında `dwellMinutes` (varsayılan 10 ardışık
dakika, 3–30) → tarayıcının Dwell üst-yazımı. Inbox varsayılan istisna listesine `anomaly:ext:*:ext:error_count` +
`anomaly-cluster:ext:*` + `anomaly:ext-cap:*`; kayıtlı liste ESKİ varsayılana küme olarak eşitse tek seferlik göçle yeniye taşınır (audit,
işaret `problem_priority_inbox_keep_v2`), özelleştirilmişse dokunulmaz, bir kez loglanır. `ext-down` listede DEĞİL.
Kabul edilen bedeller: gölge kipteki Oracle kaynaklarının Problem'leri de Problems listesinde P1/P2 görünür (sayfalama
yok; kural id'si gölgeyi canlıdan ayıramaz). Oracle kaynaklarında sürdürme küresel 3 dakikadan 10 dakikaya çıkar;
eski küme satırları tazelenene dek N/3 ölçüsünü taşır.

**Karar B — db-health hata sayısı kolu:** iki ardışık 5 dk kovanın her birinde hata SAYISI ≥ `minErrorCount` (50)
VE ≥ `errorRiseFactor` (3) × dünkü aynı kova (yoksa kol kapalı) VE ≥ 2 çağıranın her biri ≥ `minCallerErrors` (10)
hata; ≥ 2×50 critical → P1 (metrik `db.error_count`, kategori ERROR). Okuma sayısı aynı (ana sorgu + referans
sorgusuna kolon). Kapanış/histerezis diğer kollarla aynı. Problem detayına "Hata kırılımı" pivotu (Databases
detayının hata kartı, `#db-errors`). Bilinen asimetri: dünkü referans batch çağıranları da sayar → kol temkinli yönde susar.

## 2026-10-03 — db-health hariç sistemler (couchbase); Problems listesi incident başına tek satır, Exceptions biçimi (v0.10.1084)

**Operatör:** "%100 hata oranı gerçek değil" (birkaç Couchbase veritabanında P1, %100 / %67 / %11) ve "tekilleştir,
Exceptions'taki format güzel" → onaylı. **Kök neden (A):** Couchbase SDK'sı KV "bulunamadı" cevabını span'de ERROR
işaretliyor (biz değil — dönüştürücü OTLP status'u aynen kopyalar); önbellek deseninde her ıska hata sayılıyor.

**Karar A:** `db_slow_query.health.excludeSystems` (varsayılan `["couchbase"]`, küçük harf + kırpma + tekrarsız, ≤ 20,
keep-last-good; alanı bilmeyen sekmenin PUT'u saklı listeyi korur; `[]` = hiçbiri). Hariç sistem ana okumada ve
referans okumasında SQL'de düşer (`lower(trimBoth(db_system)) NOT IN ?`; boş listede koşul yok). Açık satırları bir
sonraki tikte normal kapanış yoluyla "system excluded (db-health)" gerekçesiyle kapanır (incident kaskadı görür),
bir kez. Settings → Database health "Hariç sistemler". Kalıcı çözüm (kuyrukta): "beklenen hata" türleri
(`DocumentNotFound` vb.) kaynakta ya da ölçü katmanında ayrılsın — sistemi bütünüyle susturmak gerçek Couchbase
kesintisini de gizler (bedel).

**Karar B:** /inbox'ta AÇIK incident'ın bağlı problem satırları gizlenir, incident satırı kalır: başlığı birincil
bağlı problemin cümlesi, Occurrences "N problem", öncelik bağlıların görünüm önceliklerinin en yükseği, ilk görülme
en erken / son görülme en geç. Sunucuda, görünüm önceliğinden sonra, sayaç / sıralama / tavandan önce; incident'ı
kapanınca problem kendisi olarak döner; incident satırı süzgeçle elendiyse problem gizlenmez. Satır biçimi Exceptions
listesiyle ORTAK bileşen (`TriageTitleCell`): kalın başlık + satır içi durum çipi, soluk ayrıntı; ayrı Source kolonu
kalktı. Bilinen sınırlar: rozet (/api/inbox/count) katlamayı uygulamaz; Problems türü seçili değilken tür çipi COUNT'tan.

## 2026-10-03 — Operasyon gecikmesi: iki ardışık kova şartıyla yeniden açık (v0.10.1085)

**Operatör (prod):** dedektör açıkken ~30 çağrılı bir operasyonda TEK yavaş istek (4 sn'lik bir Kafka publish) o
5 dk kovanın p99'u olup "168×" anomali açıyor, sonraki kovada 1×'e iniyordu — 1056'nın kapatma sebebi. Sürdürme
önerisine onay: "Önerini yapalım". **v0.10.1056'yı kısmen revize eder:** varsayılan ve "yeniden açmayı önerme" notu
değişti; dedektörün eşikleri, anahtarı ve v0.10.1046 batch kapısı aynen.

**Kural:** (servis, operasyon) ancak ardışık `anomaly_sensitivity.opLatencyDwellBuckets` (vars. 2, aralık 1–6)
TAMAMLANMIŞ kovanın HER BİRİNDE ihlal ederse olay: her kova ≥ 30 çağrı, p99 ≥ 200 ms, p99 ≥ 3 × taban (24 sa; taban
sürdürme penceresinden önce biter). Olay şekli aynı, alanlar en yeni kovadan; 1 = eski tek-kova davranışı. Batch
kapısı yalnız HER kova yük altındaysa susturur (metrik dedektörünün "her dwell kovası" emsali).

**Okuma:** aynı tek MV pivotu (`operation_summary_5m`): iç sorgunun `is_cur`'u kova numarasına genişler
(`multiIf(time_bucket >= ?, 1, time_bucket >= ?, 2, 0) AS slot`, GROUP BY slot), dışta önceki kova başına
`p99_<i>` / `calls_<i>`, HAVING her kovaya aynı tabanları uygular (LIMIT yalnız sürdürmeyi geçebilecek satırlarda
ısırır). LIMIT 200 + `max_execution_time` aynen, çift başına döngü yok; dwell 1'de metin v0.10.1046 ile birebir.
Sınıflayıcı iki kova üstünde saf (`classifyOpLatency(rows, dwell, gate)`).

**Varsayılan + göç:** `opLatency` nil = AÇIK, açıkça false kapalı; ayar henüz doğrulanmadıysa (boot okuması
düştü) dedektör kapalı okunur. 1056'nın Normalize'ı nil'i false'a somutlaştırdığı için o dönemde Kaydet'e basılmış
her blob `opLatency:false` taşır — o false varsayılan sayılır. Tek seferlik göç (ayar yükleyicisinde okumadan önce):
`opLatency:false` VE `opLatencyDwellBuckets` alanı yok → alan silinir (nil = açık) + system audit; alan varsa (bu
sürümde kaydedilmiş false) dokunulmaz. İşaret `system_settings.anomaly_sensitivity_oplatency_v2`; varken bir daha
koşmaz, sonradan kapatan operatörün false'u kalır.

**Bedeller:** tespit bir kova (~5 dk) geç; 1056 döneminde bilerek kapatılmış false varsayılandan ayırt edilemez ve
açılır (audit'te görünür, Settings → Anomaly'den tek tık kapanır). Olay kova sayısını taşımadığı için açıklama
cümlesine "2 kovada sürdü" eklenmedi.

**Histerezis — sürdürme yalnız AÇILIŞA:** olayı zaten aktif çift (son 15 dk'da yazılmış; v0.10.1046 batch kapısının
AYNI `ListActiveAnomalyKeys` okuması, dwell ≥ 2'de servis daraltmasız, 200 çift / 64 KiB tavan) en yeni kova tek
başına ihlal ettikçe tazelenir — SQL'de önceki kova koşulları `OR (service_name, name) IN (…)` ile muaf; en yeni kova
temizse yazım durur ve olay olağan aktif yaşla düşer (tek dip kovada "anomaly cleared" + yeni bildirim dalgalanması
yok). Okuma hatası ya da tavan ötesi → muafiyet yok, aktif çift de iki kova ister. Bedel: recorder dakikada bir
küçük `anomaly_events` FINAL okuması artık her kurulumda (eskiden yalnız batch listesi doluyken).

## 2026-10-03 — Problems rozeti varsayılan listeyle aynı sayar (katlama dahil) (v0.10.1086)

**Kusur (operatör onaylı):** v0.10.1084'ün bilinen sınırı — liste açık incident'ın bağlı problemlerini tek satıra
katlıyor, kenar çubuğu rozeti (`/api/inbox/count`) ise kaynak başına COUNT topluyordu: problems + anomalies +
incidents, TÜM öncelikler, katlamasız (exception'lar v0.9.442'den beri manşet dışı). Rozet ekrandaki satırdan büyük
okunuyordu. **Karar:** manşet = Problems listesinin VARSAYILAN görünümünün satır sayısı (open, yalnız P1, tüm türler,
servis şeridi, varsayılan taban + istisna; görünüm önceliği + istisna listesi + incident katlaması dahil). Ayrı sayım
yok: liste derlemesi handler'dan `inboxView`'a taşındı (gövde aynen), rozet sayfanın parametresiz isteğini
(`inboxBadgeQuery`) kurar ve AYNI önbellek girdisini (`cachedJSON`, aynı anahtar + 15 s TTL) okur; sayı gövdenin
`total`ı (facet + katlamadan sonra, 300 tavanından önce), tarama tavanı listeyle aynı `scanCapped` bayrağıyla gelir.
Sonuç: P1 exception'lar artık manşette (v0.9.442'nin manşet tanımını revize eder — P1 süzgeci 3.1K grup şişmesini
zaten keser); P2/P3 ve katlanan problem satırları sayılmaz. Exceptions girişinin sönük rozeti (exceptions +
httpErrors COUNT'ları) aynen. Anahtar `inbox:count:v2:` (anlam + gövde değişti; `problems/anomalies/incidents`
kırılımı kalktı), TTL / 30 s poll / gizli sekmede durma aynen. **Bedel:** rozetin soğuk yolu artık üç COUNT değil tam
liste derlemesi (sayfanın varsayılan açılışıyla aynı okuma); ısıtma döngüsü bunu 30 s'de bir öder ve karşılığında
parametresiz /inbox sıcak açılır. Rozet liste girdisinin anlık görüntüsünü taşıdığı için iki SWR katmanı üst üste
biner (en kötü ~1 dk gecikme); mutasyonlar `inbox:` önekini düşürdüğünden ikisi birlikte tazelenir.

## 2026-10-03 — ES log desenleri: sözcük içindeki token'lar prefix sorgusuyla eşleşir (v0.10.1087)

**Operatör (prod, ES):** "Elastic'te eksik." **Neden:** ES dedektörü token'ları `message:"token"` ifadesiyle sayıyordu;
standart çözümleyici (UAX#29) kod benzeri sözcüğü TEK terim tutar — harf/rakam bitişikliği (`MQJCA1011` → `mqjca1011`)
ve iki harf arasındaki nokta (`java.lang.NullPointerException` → `java.lang.nullpointerexception`) bölünmez. Terimin
öneki/içi olan token (`nullpointer`, `mqjca`, `jbas`, `sqlexception`, `timeout`) hiç eşleşmez; CH alt-dize eşler.
Sentetik fikstürde regex'in (= CH'nin) eşlediği 57 satırın 10'unu sayıyordu, şimdi 57'sini.

**Karar:** `query_string` yerine token başına `bool.should` (`minimum_should_match: 1`, filtre bağlamı) —
`logstore.patternMatchClause`, dedektör / grafik (1060) / `pattern=` pivotu (1071) / örneklem (1080) aynı haritayı okur
(parite testi dördünü bayt bayt karşılaştırır).

| Token sınıfı | Mod | Örnek |
|---|---|---|
| tek terim biçimli (`[a-z0-9_]`, nokta yalnız iki harf arası), ≥5 karakter | `prefix` (küçük harf, `case_insensitive`) | `nullpointer`, `mqjca`, `timeout`, `exception`, `ij000655` |
| çok sözcüklü | `match_phrase` | `not allowed for uri`, `service quota` |
| tire / iki nokta taşıyan ya da <5 karakter | `match_phrase` (tam terim) | `ora-`, `tns-`, `401`, `panic:`, `x509:` |
| `esPrefixForms` (desen adı → ES'e özel ek) | `prefix` | kısa kod öneki `wfly`, `jbas`; paket-nitelikli `java.lang.nullpointer`, `java.sql.sqlexception`, `org.springframework.beans.factory.beancreation` … |

Kısa önek `ora*` "oracle"ı, `401*` "4012"yi sayardı — ifade kalır, fazlasını 1080 örneklemi ayıklar. Paket-nitelikli
biçimler yalnız kararlı JDK / Spring / Hibernate / JPA adları (`PatternSpec.ESPrefixes`); kurum içi paket bilinmediği
için "External system rejected" yalnız bare `externalsystemexception` önekiyle kalır. "Java exceptions" artık sınıf
köklerini (`classcast`, `java.lang.illegalstate` …) de arar. CH listesi (`Tokens`) ve CH yüklemi değişmedi.

**Maliyet:** `prefix` terim sözlüğünde tek seek + yalnız öneki taşıyan terimler (konum okumaz; çok sözcüklü ifadeden
ucuz). Baştaki joker (`*token*`) YOK — sözlüğün tamamını gezer. `_msearch` alt sorgu sayısı aynı (desen başına bir);
eklenen ~40 prefix üyesi 12 desene dağılır. **ES varsayımı:** `prefix.case_insensitive` ≥ 7.10 (depo v0.8.377 seviye
bantlarından beri varsayıyor); `query_string` reddi tuzağı burada yok. **Bedel / risk:** `search.allow_expensive_queries:
false` kümede prefix reddedilir (seviye histogramıyla aynı risk); `wfly` öneki WildFly kodlarını (`WFLYCTL0013`) artık
bulur ama regex `(WFLY|JBAS)[0-9]+` onları eşlemez — 1080 örneklemi bastırır, CH ile aynı (regex düzeltmesi ayrı iş).

## 2026-10-03 — Traces: servis seçici şeridin solunda, görünüm anahtarı sağda (v0.10.1088)

**Operatör:** "Traces sayfasındaki service search sol başta olabilir." Traces / Aggregated / Shapes anahtarı ve
Aggregated'ın grup alanları şeridin sonuna (`margin-left:auto`), servis seçici Services sayfasındaki gibi en solda.
Davranış değişmedi.

## 2026-10-03 — Seçici açılır listesi: ortak popover (kart, klavye, son kullanılan, kenarda kırpılmaz) (v0.10.1089)

**Operatör (prod, Services "Filter services…", Traces "Filter by service…"):** "Search daha iyi bir deneyim
sunabilir. Şu an sanki geçici bir menü açılmış gibi hissiyat var, iframe içinde geliyor." **Neden:** Combobox
listeyi girdinin yanına `position: absolute` çiziyordu — kart/tablo `overflow`u kesiyor, uzun ad yatay kaydırma
açıyor, satırlar çıplak, klavye satırı ve sonuç sayısı yok. **Karar:** tek primitif `ui/PickerPopover`, Combobox
onu çizer; ServicePicker / OperationPicker / MetricNamePicker ve Combobox'lı her alan birlikte değişir. Body'ye
portal + `position: fixed`, yerleşim saf `lib/pickerPopover.placePickerPop` (sol kenara hizalı, alta sığmazsa
üst, girdiyi asla örtmez, yükseklik o tarafın boşluğuna iner); çapa `[role="dialog"]` içindeyse `--z-modal-nested`.
Kart yüzeyi (`--bg`/`--border`/`--radius`/`--shadow-pop`), başlıkta "N sonuç" (sunucu toplamı) ya da
"aranıyor…" / "arama başarısız", boşta "eşleşme yok"; ad üç noktayla kısalır, tam ad `title`da, yatay kaydırma
yok; klavye satırı `aria-activedescendant`. "Son kullanılan": listeden SEÇİLEN son 5 değer, tarayıcı başına
(`lib/pickerRecents`, kapsam servis / `operation:<servis>` / `metric:<servis>`), yalnız alan boşken. Services
satır ipucu (runtime · span) sayfanın ELDEKİ verisinden; yeni istek yok. Sunucu araması aynı (180 ms debounce,
aynı uçlar, 200 satır) — üç kopya `usePickerSearch` kancasına indi, geç dönen eski cevap yenisini ezemez.
`ui/Popover` kullanılmadı: odağı içine alır, seçicide odak girdide kalmalı.

## 2026-10-03 — Kök neden adayları: verdict, shift ve şerit de yön/bağlantı süzgecinden geçer (v0.10.1090)

**Bağlam:** v0.10.1063 yalnız panel manşetini (`coMovingCause`) `causeEligible`'a bağladı; aynı yönsüz skor üç
yerde daha aday / "kötüleşen" diye sızıyordu. **Karar:** (1) ✨ RCA hakem kataloğu (`rca/extras.go`) yalnız
`causeEligible` satırı "aynı pencerede kötüleşen (ya da trafiği kesilen) komşu" yapar — beyaz liste ve
`root_cause.entity` enum'u yalnız bunlarla genişler. `gatherRCACatalogExtras` /rootcause ile aynı işaretlemeyi yapar
(50'lik havuz + `rootCauseTopo` → `MarkCorrelationCauses`; yeni sorgu şekli yok). Kalanlar ADSIZ tek satır: "değişen
ama bağlantısız / iyileşen N servis daha var … kök neden adayı DEĞİLDİR" (topoloji okunamadıysa "doğrulanamadı");
ad basılmaz ki gösterilen jeton olup K3'ü geçmesin. `copilot/prompts.go` değişmedi — talimat katalog satırında
(blast satırı emsali). (2) `/shift`: "En çok kötüleşen" yalnız `direction=worse`; `lost` ayrı "Trafiği kesilen",
`better`/`quieter` "İyileşen" tablosunda; `unknown` hiçbirinde. Tavan (10) grup başına, 50'lik havuzdan sonra.
(3) Şerit "Ranked candidates" canlı yolu yalnız `causeEligible`; yoksa panelin hükmü (`localizedNote` →
`ribbonNoCandidateNote`). Anomali kök-neden demeti de artık işaretlenir (problem ucunun ikizi) — yoksa şerit
anomalilerde topoloji okunabilirken "doğrulanamadı" derdi. Hipotez işçisinin kendi adayları değişmedi. Çivi:
`api/correlation_consumers_pin_test.go`.

## 2026-10-03 — Yaygın yavaşlama hızlı yolu: tek kovada aşırı sapma → servis üzerinde P1 (v0.10.1091)

**Operatör (prod, iki gün üst üste):** "Dün söylediğim CRM sorunu yine oldu, bir sürü anomali geldi ama P1 problem
gelmedi." ~10 dk'lık ağır olay: bir servisin birçok operasyonunda p99 ms'lerden 15–20 s'ye çıktı, trafik ~%40 düştü;
20 operasyon anomalisi (×700–×2000) açılıp 5–10 dk'da düştü. P1 yok: bu hafta eklenen her kural 2 ardışık kova / 10
dk istiyor, operasyon anomalileri bilinçli P3. Onay: "Onay".

**Karar:** evaluator'da lider pası `svc-slowdown:<servis>` (kural id = problem id, servis başına tek, SLOWDOWN,
critical). TEK tamamlanmış 5 dk kovada ≥ 3 operasyonun her biri ≥ 30 çağrı, p99 ≥ 5 s, p99 ≥ 20× kendi 24 sa tabanı
VE kova p95'i ≥ 2.5 s (yavaş pay); servis toplamı ≥ 100 çağrı; batch değil. Taban 24 sa'in HAVUZLANMIŞ p95'i (aynı
tDigest): dünkü 10 dk'lık olay taban p99'unu şişirip bugünkü aynı olayı susturuyordu; kova başı p99 medyanı filo
ölçeğinde (işlem × 288 kova tDigest durumu) bellek olarak ağır bulundu. İkinci kol (aynı id): kova trafiği önceki
saatin ortalamasına göre ≥ %40 düşük, cari kovada ≥ 100 çağrı, servis p99'u ≥ 5 s VE ≥ 3× p95 tabanı. Kural
tetiklenince HER ZAMAN P1 (operatör): Value = en yüksek operasyon p99'u, Threshold = O operasyonun kendi tabanı (oran ≥
20× → 5.5 s / 120 ms da P1); çöküş kolunda servis p99'u / servis tabanı (≥ 3×). Bu güvence `problem_priority.
bigBreachRatio` ≤ 3 (vars. 2) iken geçerli; operatör onu 3'ün (ya da riseFactor'ün) üstüne çekerse ilgili kol critical P2
olur (pinli). Detay grafiğinin çizgisi ("op tabanı 5 s") ve yavaş trace süzgeci Problem eşiğinden AYRIK — ayardaki
minP99Ms; yalnız çöküşle açılan satırda çizgi yok. Açıkken ölçü yalnız yükselir; 2 ardışık temiz kovada kapanır;
okumalar 10 s bütçeli, hata kova başına bir kez yeniden denenir, sonra açıklar yalnız tazelenir. Vidalar
`anomaly_sensitivity.serviceSlowdown`; inbox istisnasına `svc-slowdown:*` (v3 göçü). Operasyon anomalileri aynen
sürer — bastırılmaz.

**Neden tek kova burada güvenli:** 1056'nın gürültüsü TEK operasyonda TEK yavaş isteğin p99'u şişirmesiydi. p99
tek başına yetmez: iç içe span'leri olan TEK yavaş trace (sunucu + iç + istemci operasyonu, ~40'ar çağrı) üç
operasyonun p99'unu birden çeker. Bu yüzden mutlak (p99 ≥ 5 s), göreli (≥ 20× kendi tabanı), hacim (≥ 30 çağrı /
operasyon) ve YAVAŞ PAY (p95 ≥ 2.5 s — çağrıların ~%6'sından fazlası yavaş) tabanları ile genişlik (≥ 3 operasyon
aynı kovada) birlikte aranır; tek trace p95'i kıpırdatmaz. Okuma kova başına iki MV sorgusu (op_latency'nin pivotu +
service_summary_5m); trace_op_latency sürdürme kuralı (1085) ve metni değişmedi.

## 2026-10-03 — Oracle hata tablosu satırları Exceptions'ta hata grubu; özel SQL sayfalama; UI testi durumu düzeltildi (v0.10.1092)

**Operatör:** "Oracle hataları Exceptions gibi görünsün, hatta Exceptions altında da olabilir." Oracle hata tablosu
satırları DURGUN bir akış (dakikada binlerce); sıçrama-anomalisi yolu (`ext:error_count` → dış tarayıcı) tasarım
gereği onlarda ateşlemez. **Karar:** (kaynak, hata kodu, operasyon kodu) başına bir `exception_groups` satırı —
parmak izi `ora:` + sha1(kaynak kimliği | kod | operasyon)[:16]; kanal / host / servis anahtar değil kırılım.
`ex_type` = kod, `ex_message` = operasyon, servis = trace → servis oylarının baskını yoksa `oracle:<kaynak>`.
Tazeleyici (`oracle/exgroups.go`) poller kancasında, worker liderinde: yalnız KAPANMIŞ dakikalar (özel SQL'de
`trunc(now) − windowMin − 1 dk`, tablo kipinde overlap + aralık) `oracle_error_log FINAL`'dan dakika tanesiyle
toplanır; imleç + kırılım + 10 dk dilimler `system_settings.oracle_exgroups:<kaynak>`'ta, imleç HER turda oradan
yeniden okunur (liderlik el değiştirince eski bellek imleci ikinci sayım yapmasın). Artımlar TEK batch INSERT
(ya hepsi ya hiçbiri); imleç yalnız ondan sonra ilerler. Toplama tavanında (20 000 satır, dakika sıralı) imleç tam
okunan son dakikaya kadar. Kalan risk (bilinçli): gerçek ayrık beyin (bölünmüş Redis kilidi) ya da imleç kaydı
düşüp liderlik el değiştirmesi bir aralığı iki kez sayabilir. Kip: off = grup yazılmaz; shadow = grup var,
bildirim yok; live = olağan exception bildirim hattı (tazelik kapıları kapanmış dakika gecikmesi kadar geriden).

**Öncelik — "kazanılmış P1 düşmez" ilkesinden BİLİNÇLİ sapma (koordinatör kararı, operatör "az ama gerçek
P1" istiyor):** Oracle grupları span merdiveninin yapışkan hacim P1'inden ve regressed "açıldıktan sonra ≥N"
P1'inden MUAF — durgun bir akışın ömür toplamı birkaç saatte her eşiği geçer, her grup kalıcı P1 olurdu. Oracle
grubunda P1 yalnız son 1 sa ≥ `exception_triage.oracleP1MinOccurrences` (varsayılan 5000) VE ≥ 3× önceki 1 sa
(patlama) ya da grup YENİ (ilk görülme P1 penceresinde) ve son 1 sa ≥ eşik; P2 son 1 sa ≥ 100 ve taze/regressed;
gerisi P3 "sürekli akış (Oracle)". Saatlik toplamlar ve gecikme her rolde enjekte edilen önbellekten
(`oracle.GroupStatsCache`, kaynak başına 30 sn TTL) — /inbox, Exceptions ve bildirimci aynı sayıyı görür, akan grup
"17 dk önce durdu" okunmaz; takım anonsu Oracle grubunda 500'lük span eşiği değil merdivenin P1'i. Resolve /
ignore / assign, AI özeti, bayat oto-çözüm aynen. Fırtına, paylaşılan patlama, ölümcül dedektör, yayılım işareti
ve span tazeleyicisinin boot tohumu `ora:` gruplarını okumaz. Örnek / oluşum ucu önekte Oracle satırlarına dallanır
(seri: dakika başına ağırlık, son 24 sa). Exceptions satırı aynı biçim: "<kod> · <operasyon>" + "Oracle" rozeti,
soluk "Oracle · <kaynak> · N servis"; "Oracle" çipi (tümü / yalnız / hariç, varsayılan dahil).

**Önkoşul A — UI testi:** `TestWith`'in defer'i yerel sonucu yakalıyordu; özel SQL dalı kopya döndürdüğü için her
başarılı test "başarısız (gerekçesiz)" kaydediliyordu. Adlı dönüş; satır "Son UI testi (bu API pod'u, formdaki
değerler)", sağlık satırı "Son okuma (worker)"; test sorgunun `INTERVAL 'N' MINUTE` geriye bakışını pencereyle
karşılaştırıp uyarır. **Önkoşul B — özel SQL tavanı:** sırasız `FETCH FIRST 5000` her poll aynı pencerenin
gelişigüzel ilk 5000 satırını okuyordu. Artık TEK ifade: `ORDER BY "<zaman>", "<eşlenen anahtarlar>" FETCH FIRST
maxPages × 5000 + 1` (tek anlık görüntü, tek SYSDATE — OFFSET sayfaları ayrı ifade/ayrı SYSDATE olurdu), bütçe
3 × sorgu süresi; adlar eşlemedeki yazımla tırnaklı, ORA-00904'te büyük harf, o da düşerse sırasız yedek (Capped).
Satır > tavan → "tavan: pencerenin tamamı okunamadı (N sayfa)", sayaç son-kesimi yalnız o zaman. **Bedeller:** grubun
last_seen'i duvar saatinin ~windowMin gerisinde; saatlik pencereler 10 dk tanesinde; trace → servis oyları operasyon
düzeyinde.

## 2026-10-04 — Statement detayı: ifadenin tüm trace'lerine geçiş (db_stmt_hash süzgeci) (v0.10.1093)

**Operatör (prod, Databases › Top statements › statement detail):** "Bu sayfada traces alanı yok, ilgili
statement'ın trace'lerine gidemiyorum." Exemplar linkleri (slowest / worst error) TEK trace'e, N+1 linki
Explore'a gidiyordu; sınıfın tüm trace'lerini listeleyen bir yol yoktu. **Karar:** başlığın altında tek
satır: "Trace'ler →" ve "Hatalı trace'ler →" — /traces'e sayfanın penceresiyle, `rootOnly=false`. Süzgeç
exemplar okumasının KİMLİĞİ: `db_stmt_hash = <id>` (+ URL'de sistem varsa `db.system = …`), normalize SQL
metni değil (span'deki metin ham; tam eşleşme boş, LIKE öneki başka sınıfları toplar). Servis daraltması
yok — kimlik kesin; çağıranlar yalnız link başlığında. **Arka uç:** `db_stmt_hash` süzgeç anahtarı değildi
(dizi aramasına düşüp boş dönerdi). Artık spans süzgeç derleyicisinde kolona çözülür: yalnız `=`, `!=`,
`IN`, `NOT IN`, değer ondalık uint64 (sınırda 400), UInt64 olarak bağlanır; dizi/metin/LIKE yolu yok. Kolon
yoksa (dış Distributed, `cluster_name` boş) aynı MATERIALIZED ifade satırda hesaplanır — süzgeç sessizce
düşmez. Liste ile v0.10.1082 hata şeridi aynı derleyiciden geçer; parite testli. metric_points yolu
değişmedi. **Çip:** /traces'te "statement #<kısa id>" (detay başlığındaki kimlik); istek ve düzenleme ham
değeri taşır. Yeni indeks yok; ham `spans` okuması listenin mevcut sınırları içinde (pencere + LIMIT +
max_execution_time).

## 2026-10-04 — Endpoint detayı: gecikme karosunda büyük sayı ortalama, alt satır p99 (v0.10.1094)

**Operatör:** "13 ms yerine average yazsın. Avg ile p99 yer değiştirsin." Karo "Avg latency": büyük sayı pencere
ortalaması, alt satır "p99 N ms". Kova serisi ve grafik başlığı p99 kalır (MV'de ortalama sparkline yok;
`MetricTile.chartLabel`). Davranış değişmedi.

## 2026-10-04 — db_summary_1m: kısa pencerede veritabanı grafikleri 1 dakikalık (v0.10.1095)

**Operatör:** "Önerin A yapalım." /database detayının üç grafiği (Calls/s · Error % · P99) `db_summary_5m`'in
5 dk kovalarını çiziyordu; 10 dakikalık bir DB olayı tek nokta oluyordu. **Karar:** yeni MV `db_summary_1m` —
`db_summary_5m`'in birebir ikizi (anahtar `(db_system, instance, db_name)`, aynı instance/db_name zincirleri, aynı
dört state; `quantilesTDigestState`), kova 1 dk, **TTL 7 gün** (yalnız kısa pencere; uzun vadeli depo 5m kalır).
Küme kipinde `_local` + Distributed, shard anahtarı kardeşle aynı `cityHash64(db_system)`. **Geriye dolmaz**:
tarihçe deploy anından başlar. **Seçici:** pencere **≤ 3 sa** ve 1m tablonun ilk kovası pencere başından önceyse
1m, aksi 5m (kapsama probu 60 sn önbellekli; tablo yok / DDL ertelendi / boş → sessizce 5m, iki boot gerekmez).
Yeni uç `GET /api/databases/detail/trend` (önbellek anahtarı seçilen grenliği taşır); panel başlığı "· 1 dk" /
"· 5 dk". **Değişmeyenler:** karolar (db_caller_summary_5m), /databases tablo sparkline + rozeti
(/api/databases/trends, 5 dk), db-health dedektörü (5m), Statement detayı (MV'si ifade başına,
`db_statement_summary_5m`; 1 dk ikizi yok). **Yazma bedeli:** db span'i taşıyan her INSERT bloğu bir MV daha
tetikler (blok başına satır sayısı 5m kardeşiyle aynı mertebe: blok tipik 1-2 kova görür); kalıcı satır DB kimliği
başına günde ≤ 1440 (× span'in düştüğü shard sayısı), 7 günde ~10k — 5m'in 90 günlük ~26k'sının ~%40'ı.

## 2026-10-04 — Trace Metrics pod paneli: grafikler üstte yan yana, metin tek satır + katlı ayrıntı (v0.10.1096)

**Operatör (prod, Trace › Metrics, seçili pod'un satır altı ayrıntısı):** "Çok fazla yazı var; sadece metrik
yatayda inline gözükse olacak. Diğer yazılar aşağı olabilir." v0.10.976'nın gövdesi solda üç yoğun metin
bloğu (Bu trace'te / Trace anında / Şu an), sağda sıkışmış iki grafik ve uzun gri açıklamaydı. **Karar:**
başlık satırı, alt satır ve eylemler (Odak görünümü, Pod sayfasında aç, Span'ları Trace'te göster) aynen;
altında sağa yaslı küçük "Karşılaştır" denetimi (xs çipler + Kardeş çizgileri), sonra TEK yatay ızgara
(auto-fit, dar ekranda sarar): Bellek · CPU, JVM servisinde heap · GC; her grafiğin tek satır başlığı
"Bellek 9,03 GiB · limit 16 GiB" (renk yalnız limite göre sapmada, "şu an" ve kısıtlama notu ipucunda),
trace bandı grafikte. Altında tek satır özet: span · hata ↗ · kritik yol · en büyük öz süre ↗ · faz ·
restart (· son sonlanma rozeti). Kalan metin (üç blok, sayılar ve bağlantılarıyla, grafik açıklaması,
JVM-dışı runtime notu) kapalı "Teknik ayrıntı"da — anomali/sorun sayfalarının deseni, kapalıyken mount
edilmez; hiçbir şey silinmedi. JVM artık kapalı açılır bölüm değil: seçimle açılan panelde sorgulanır (fetch
on open; servis başına bir kez, çip değişimi istek atmaz). "Kaynak: …" dipnotu sekmenin altında kalır
(tabloya da ait). Odak görünümü değişmedi.

## 2026-10-04 — Kafka istemcileri: topic/client_id süzgeci (etiket varsa), pod bazlı görünüm, kısa kaynak notu, bağlantı paneli (v0.10.1097)

**Operatör:** "9'u yap" (dördü birden). /messaging/topic "Kafka istemcileri" sekmesi (`set=clients`). **Etiket
keşfi:** sekme metriklerinin ad birleşimi üzerinde TEK `/api/v1/labels` (pencere ≤ 1 sa, cevap `set=clients`
önbelleğinde); CH'de iki temsilî metrikte attr_keys. Süzgeç ve görünüm ancak etiket serilerde varsa
uygulanır; yoksa denetim çizilmez, hakkında bir şey yazılmaz (ölü denetim / görünmez daraltma yok). Katalog
`topic` demese de keşifte görülen etiket `KafkaScope.ExtraLabels` ile kabul edilir. **Topic / client_id:**
sunucu araması (`GET /api/messaging/kafka-label-values`, beyaz liste topic|client_id, limit ≤ 100, 60 sn
önbellek); topic süzgeci uygulanınca kapsam "topic" olur ve "süzülemez" uyarısı düşer. URL: `ktopic`, `kclient`.
**Pod bazlı:** pod etiketi (`k8s_pod_name` → `pod`) varsa "Toplam / Pod bazlı"; pod görünümünde blok başına
ilk 12 pod + "diğer N" sunucuda katlanır (blok toplamasıyla), varsayılan Toplam; URL `kview=pod`. **Kaynak
notu:** tek satır "Kaynak: VictoriaMetrics · kafka client metrikleri · N sn adım" (adım sorgunun promStep'i;
CH'de yazılmaz), uzun metin ipucunda. **Bağlantılar:** mevcut `connection_count` (iki taraf toplamı) pod başına,
"aktif pod" = son adımda ≥ 1 bağlantı; seri yoksa "metrik yok"; bu iki blok ızgarada ikinci kez çizilmez.
Pod görünümünde ek sorgu yok, Toplam'da iki ek soru. Yeni metrik/kardinalite yok; adım mevcut mdp
kelepçesinden; anahtar süzgeç+görünümü taşır (`msg-clients:v2`); sekme açıkken 30 sn yoklama, gizli sekmede durur.

## 2026-10-04 — Log desenleri: WildFly/JBoss kod regex'i gerçek kod biçimiyle (v0.10.1098)

**Neden:** "JBoss / WildFly errors" regex'i `(WFLY|JBAS)[0-9]+` önekten hemen sonra rakam istiyordu; gerçek WildFly
kodu önek + 2–6 harf alt sistem + rakam (`WFLYCTL0013`, `WFLYEJB0034`, `WFLYMSGAMQ0090`). CH bu satırları hiç
saymıyor, ES'te `wfly` öneki (1087) buluyor ama 1080 örneklemi regex'e uymadığı için bastırıyordu — desen iki arka
uçta da ölüydü. **Karar:**
`\b(WFLY[A-Z]{2,6}[0-9]{4,6}|JBAS[0-9]{6})\b.*(?i:fail|error|exception|unable to|could not|cannot|missing)`. Kod harf duyarlı, `\b` RE2 ASCII sözcük sınırı (Go ve CH re2 aynı; yerel CH 26.3
`match()` ile 22 tablo satırında Go'yla birebir). Kodun kendisi seviye taşımaz ve aynı önek açılıştaki INFO/WARN
satırlarında da var (`WFLYSRV0049 … starting`, `WFLYUT0021 Registered web context`, `WFLYTX0013`); ad "errors"
dediği için kodun ARDINDAN arıza işareti aranır — yoksa her yeniden başlatma spike olurdu. Bu yüzden eski regex'in
saydığı arıza işaretsiz `JBAS` INFO satırları (`JBAS015876: Starting deployment`) artık sayılmaz. Token'lar
(`wfly`/`jbas`, ES'te prefix) aynen; ES'in bulduğu INFO fazlasını 1080 örneklemi ayıklar. Diğer küratörlü kod
regex'leri (`ORA-`/`TNS-` + rakam, `IJ000453`/`IJ000655`, `MQJCA1011`) aynı hatayı taşımıyor: kodları önekten
sonra doğrudan rakam. Test: `log_patterns_wfly_test.go` (CH `match()` tablosu), ES önek fikstürüne 7 satır.

## 2026-10-04 — Oracle hata grubu için AI açıklaması: span yerine Oracle bağlamı (v0.10.1100)

**Operatör onaylı.** `ora:` exception gruplarında (v0.10.1092) "Explain root cause" span girdisini kuruyordu (stack,
örnek trace, loglar, pod) — Oracle satırı bunları taşımaz, açıklama boş/jenerik çıkıyordu. **Karar:** girdi grubun
kaynağına göre tek noktada seçilir (`api.exceptionExplainInput`; explain ucu + insight kartı). Oracle girdisi
(`anomaly.BuildOracleExceptionExplainInput`): kaynak, kod + operasyon, son 1 sa / önceki 1 sa + oran + akış sınıfı
(patlama ≥3× öncelik kuralıyla aynı / yeni akış / sürekli / sönüyor), kanal kırılımı (ilk 3, %), servisler (ilk 3), en
yeni satırlardan host / instance, ≤5 trace (mevcut trace → servis çözümü: servis + exception tipi; kök operasyon
okunmaz — ek sorgu gerekirdi), ilk/son görülme, en yeni 3 satırın eşlenen alanları (mesaj, dış kod, tip, ≤8 eşlenmeyen
kolon; SONUC/mesaj/kod adlıları önce). Okumalar sınırlı: `OracleErrorsByOpCode` tavanı 50 (son görülmeden 1 sa geri),
trace çözümü ≤5 id; yeni tablo yok. Saatlik sayı ve kırılım `oracle.GroupStatsCache`'ten (Exceptions satırıyla aynı
sayı; anomaly oracle'ı import edemediği için `anomaly.SetOracleExplainFacts` ile main.go enjekte eder). Ayrı sistem
prompt'u `SystemPromptOracleException` (sicilde, Türkçe cevap): kodun anlamı (ORA-/PLS-/TNS- biliniyorsa), yoğunlaşma,
patlama mı akış mı, DB tarafı mı çağıran mı. Oracle grubunda "Kodu da incele" koşmaz (FE çipi yerine "Oracle · <kaynak> ·
<kod> · <operasyon>" satırı). **Otomatik özet:** aday kuralı (≤5 dk, ≥500) Oracle grubunda yaşı kapanmış dakika
gecikmesi (`GroupLag`, aynı önbellek) kadar geriden ölçer — eskiden ≥16 dk gecikme yüzünden hiç aday olmuyordu; kaynağı
bulunmayan grup aday değil; tik başına 4 / kota kapıları aynen. AI çekmecesinin takip sohbeti (read_source_code yolu)
değişmedi.

## 2026-10-04 — Traces: süzgeçsiz Errors'ta da histogram listeyle aynı kümeyi sayar (v0.10.1101)

**Sorun (1082'de açık kalan):** Errors açık, çip yok (ya da yalnız servis / ortam / küme) → şerit metric-batch'te
GİRİŞ span'lerinin hatasını sayıyordu (`kind IN (server, consumer)` + `status = error`); liste ise herhangi bir
span'i hatalı trace'leri gösteriyor. İstemci / iç span hataları şeritte ve "x ERROR SPANS / ERR RATE" başlığında
yoktu. **Karar:** bu sınıf da v0.10.1082'nin `GET /api/traces/error-histogram`'ına gider (③ kapsam kipi, birim
"spans", Mode "span"): listenin kendi yüklemi `status_code = 'error'` (hasErrorSpanLocal → WHERE). Kaynak: ortam /
küme yoksa DAR rollup (service_name, span_kind, status_code; kind kısıtı yok, pencere başı listenin 5 dk hizası,
baş dilimi ilk kovaya katlanır) — eski şeridin MV maliyet sınıfı; yoksa ham `spans`, listenin ham WHERE'iyle bayt bayt
aynı (`traceErrScopeWhere` = `buildGetTracesWhere(f)`; servisli MV dalının hata-önce WHERE'i de aynı). Liste bu
sınıfta `trace_summary_5m`'den okur (servissiz dilim `error_count_state > 0`), ama şerit kaynağı olamaz: span süresi
kuantili yok, 5 dk'dan ince kova yok, servis boyutu yok, kova sayımı pencerenin tüm durum satırlarını okur. Çipsiz +
Root metric-batch'te kalır (kök trace düzeyi; span sayımı taşıyamaz). Errors kapalı hâller değişmedi. Başlık
sayıları şeridin kendi cevabından (`stripHeaderStats`). Pin: `chstore/trace_error_histogram_test.go` (parite
a/b/c), `api/trace_error_histogram_test.go`, `pages/traces/scopeParams.test.ts`, `volumeSeries.test.ts`. İnceleme
düzeltmesi: rollup okuma adımı ≤5 dk (`traceErrScopeRollupMaxStep`) — 3g+ pencerede saatlik katman 5 dk hizalı pencere
başının ilk kısmi saatini okumuyordu; şimdi 5 dk satırları Go'da çıktı adımına katlanır. Fırçalanmış pencerede son
kısmi 5 dk satırı pencere sonunu ≤5 dk aşabilir (eski şeritte de öyleydi, kabul).

## 2026-10-04 — Grafik y-ekseni etiketi ölçülür (kırpılma yok); Kafka seçici önerileri sayfa kapsamında (v0.10.1102)

**Operatör:** Trace › Metrics pod panelinde JVM heap "953.7 MiB" "353.7 MiB", Kafka bağlantı grafiğinde "12.5"
"2.5" okunuyordu. **Kök neden:** oluk çizilen etiketten ölçülmüyordu — CorePanel'de (MultiLineChart dahil)
1/2/5 merdiveniyle TAHMİN edilen tick'lerden, uPlot ise 1/2/2.5/5 ve kendi aralık yuvarlamasıyla çiziyor
("12.5" tahminde yoktu); OverviewChart'ta sabit 34 px. **Karar:** y oluğu uPlot'un `size(self, values, …)`
geri çağrısında, çizilecek etiketlerden eksen fontuyla ölçülür (`measuredAxisSize`): çentik + boşluk + 4 px
pay + en geniş etiket, [32, 96] px; panelin %40'ı tavanı kalktı (dar hücrede kırpan oydu). CorePanel'de
Grafana `AxisProps.size` sayı istediği için builder alt sınıfı config'te y eksenine yazar; oluk config
kimliğinden çıktı, "yalnız büyür" mandalı silindi. TimeChart'ın ana ekseni side 0 = üst şerit (v0.8.91'den
beri; sola taşımak ayrı operatör kararı): orada oluk yüksekliktir (38 → 32 px), genişlik yalnız sağ (y2)
eksende ölçülür. **Kafka seçici:** `kafka-label-values` filo geneli değil. İstemci yalnız sayfa
anahtarlarını (system / cluster / destination, pencere) ve karşı süzgeci (client_id aranırken seçili topic,
topic aranırken seçili client_id) yollar; üretici/tüketici kümeleri sunucuda `/api/messaging/clients` ile
AYNI yoldan türetilir (`resolveKafkaServiceScope`: span ∪ keşif, taraf başına ≤ 200) — 200+200 adlık sorgu
dizesi ingress başlık tamponunu aşar, virgüllü ad bölünürdü. Türetilen kapsam boşsa 400. VM taraf başına
bir `match[]` (panellerin `service_name=~` eşleştiricisi), CH aynı FilterExpr'lerle. ≤ 100 değer, 60 sn;
kapsam kendi anahtarıyla önbellekli (`kafka-page-scope:v1`), cevap anahtarı sayfa anahtarlarını, türetilen
kümelerin FNV özetini ve karşı süzgeci taşır (`kafka-label-values:v3`).

## 2026-10-04 — Oracle hata grubu AI açıklaması: Coremetry trace'i ve logları da girdide (v0.10.1103)

**Operatör:** "Oracle hata grubu için varsa coremetry üzerindeki trace ve o trace loglarını da kullanabilsin."
v0.10.1100'ün Oracle girdisi trace id'leri yalnız servise + exception tipine çözüyordu; çağıran operasyon ve
uygulamanın hata anında yazdığı log modele gitmiyordu. **Karar:** en yeni ≤50 satırdan çözülen ≤5 trace id
içinden (en yeni önce) `TraceFactsByIDs`'in Coremetry'de BULDUĞU ilki TEK trace olarak okunur — span yoluyla
aynı yardımcılar (`anomaly/trace_evidence.go`; span yolunun satır içi gövdesi oraya çıkarıldı, prompt'u bayt
bayt aynı, eski kodun kopyasına karşı pinli): `GetTrace` 8 sn, kompakt blokta ≤60 span (≤20 hata span'i
garantili), ≤5 kanıt span'i, hata span'lerinden ≤3 SQL; ve YALNIZ trace yüklendiyse o trace'in logları
(`LogsForTrace` 6 sn, ≤30 çekilir, severity'ye göre ≤12 satır, stack çözücü + tekrar katlaması). Yüklenmeyen
trace için log sorgusu yok (v0.9.414 ES maliyet disiplini); log deposu yoksa (CH-only) log bloğu atlanır.
Prompt'ta bloklar çözülen trace'lerin JSON'undan sonra; trace yoksa açık "Coremetry trace'i: yok" satırı ve
sistem prompt'u yeni "Çağıran taraf (Coremetry trace)" bölümünde çağıran servis/operasyonu ve logu ister,
trace yoksa çağıranı tahmin etmez. `EvSpans` / `LogsBlock` / `DBStatements` kardeşteki gibi dolar; kartın
örnek trace çipi yüklenen trace'e gider. Explain, insight kartı ve arka plan özeti aynı kurucudan geçer.
**Değişmeyen:** kod çekilmez ("Kodu da incele" Oracle'da koşmaz, `Stack` boş); AI çekmecesinin takip sohbeti
aynen; yeni tablo yok. **Neden tek trace, beş değil:** her trace bir span okuması + bir ES sorgusu demek ve
tık başına bedel beş katına çıkar; aynı (kod, operasyon) grubunun trace'leri aynı çağrı yolunu taşır — en yeni
bulunan trace kanıta yeter, kalan dört id servis çözümüyle zaten prompt'ta.

## 2026-10-04 — Exceptions: trace id'ler gerçek bağlantı (orta tık yeni sekme); Coremetry'de olmayan Oracle trace'leri işaretli (v0.10.1104)

**Operatör:** "Exceptions sayfasındaki traceidler mouse orta clickle yeni sekmede açmıyorum. Bu arada bazı
traceidler de aslında coremetry üzerinde olmayabilir"
Exception detayının "Sample traces" satırında trace id bir `<span>`'dı, satır yalnız `navigate()` çağırıyordu —
orta / Ctrl / ⌘ tık hiçbir şey yapmıyordu. **Karar (1):** satır `ExceptionSampleRow.tsx`'e çıkarıldı; trace id
react-router `<Link to={traceHref(id)}>` = gerçek `<a href>`, tarayıcının yeni-sekme davranışı kendiliğinden
gelir. Satırın `rowActivation`'ı (düz tık + Enter/Boşluk) kalır; link tıkı `stopPropagation` ile satıra
çıkmaz (a11y.ts sözleşmesi) → düz tık tek gezinme, değiştiricili tık aynı sekmeyi gezdirmez. Sayfadaki diğer
trace id'ler (Oracle paneli, dış kanıt, kök neden örneği, liste quick-peek) zaten `<Link>`. **Karar (2):**
`ora:` grubunun örnekleri Oracle hata satırlarından gelir; trace o satırın taşıdığı değerdir ve Coremetry'ye
hiç ulaşmamış olabilir (boş /trace sayfası). `oracleGroupSamples` örnekleri kurduktan sonra ayrık trace
id'leri (≤100) TEK `TraceFactsByIDs` sorgusuyla arar — pencere örnek satırlarının aralığı ±5 dk (v0.10.1100
explain payı), 6 sn bütçe; hata/zaman aşımı alanı boş bırakır, örnek çağrısı asla düşmez.
`ExceptionSample.traceInCoremetry` (*bool, omitempty): nil = bilinmiyor (span grupları hiç aranmaz — örnek
span'den doğar). Ön yüz `false`'ta link basmaz: soluk mono id + `badge b-gray` "Coremetry'de yok", title
"Bu trace Coremetry'de yok — yalnız Oracle hata satırı taşıyor", satır tıklanmaz; Oracle panelinde bulunan
trace'ler önce. **Değişmeyen:** span gruplarının örnek sorgusu ve yanıtı; uç `serveCached`'li değil (anahtar
yok). **Bilinen sınır:** yalnız CH spans'a bakılır — Tempo yedeğinden açılabilecek trace de "yok" görünür.

## 2026-10-04 — Trace id bağlantıları her listede gerçek <a> (orta tık yeni sekme) (v0.10.1105)

**Operatör:** v0.10.1104'teki Exceptions düzeltmesi diğer sayfalarda da uygulansın. **Kural:** trace id
taşıyan her kayıt-listesi satırında id react-router `<Link to={traceHref(…)}>` = gerçek `<a href>`; satırın
`rowActivation`'ı (düz tık + Enter/Boşluk) kalır, link tıkı paylaşılan `stopRowClick` ile satıra çıkmaz
(`lib/a11y.ts`'e taşındı; `features/anomalies/sampleTrace.ts` yeniden dışa açar) → düz tık tek gezinme,
Ctrl/⌘/orta tık tarayıcının yeni sekmesi ve aynı sekmeyi gezdirmez. **Düzeltilen:** Traces → Shapes
(`ShapesView.tsx`) exemplar hücresi düz metindi, satır yalnız `navigate()` çağırıyordu — artık `<Link
className="mono">`, renk/hücre aynı. Explore Repeats / Traces (`RepeatsResult.tsx`, `TracesResult.tsx`)
zaten `<Link>` + elle `stopPropagation` taşıyordu; paylaşılan `stopRowClick`'e geçti. /traces listesi
(`Traces.tsx`) v0.10.216'dan beri her hücrede `row-link` `<Link>` — değişmedi. **Sohbet balonu
(`ChatBubble.tsx`):** `data-nav` trace linki işleyicisi HER tıkta `preventDefault()` + `navigate()`
yapıyordu; Ctrl/⌘-tık yeni sekme yerine aynı sekmede gidiyordu. Artık yalnız düz sol tık
(`isPlainLeftClick`: `button === 0` ve Ctrl/⌘/Shift/Alt yok) SPA içi gider; değiştiricili ve orta tık
tarayıcıya kalır. **Kapsam dışı (yalnız raporlandı):** trace olmayan, yalnız `navigate()` ile gezinen
satırlar (Profiling, Databases/SlowQueries ifade satırı, servis Overview operasyon/DB satırları,
TopEndpointsCard, Clusters pod satırı, DependenciesTable, Inbox) — ayrı karar.

## 2026-10-04 — Problem detayında log deseni sayım grafiği (terfi Problem'i kaynak olayını okur) (v0.10.1106)

**Bağlam:** v0.10.1060 "Desen sayısı" bar grafiğini yalnız anomali OLAYI detayına koydu ve terfi Problem'ini
(`anomaly-auto:`) bilerek dışarıda bıraktı ("kaynak olayı ayrıca okumak gerekir"). **Erteleme kalktı — operatör
isteği (kuyruk, onaylı):** artışın ne zaman başladığı Problem detayında, kaynak olayı açmadan görünsün.
**Karar:** yeni uç `GET /api/problems/{id}/source-event` (`problem_source_event.go`, defterden; api.go büyümedi).
`/api/problems/{id}` yükü genişletilmedi: detay Problem'i önce zaten yüklü listeden çözer, by-id ucu yalnız
derin-link yedeği — grafik çoğu açılışta gelmezdi; liste yüküne koymak sıcak `/api/problems`'e okuma eklerdi.
`/api/anomalies/event` kullanılmadı: tam satır + dört zenginleştirme okuması, grafik yedi kolon ister. Olay
kimliği SORGUSUZ (`PromotedAnomalyEventID`, v0.10.1054 — Problem kimliği `anomaly-auto:<fp>:<servis>`'i kabul
eder; problems okunmaz); terfi değilse okuma yok. **Okuma sınırı:** `chstore.GetPromotedSourceEvent` — TEK
okuma, PK eşitliği + `FINAL` + `ORDER BY last_seen DESC LIMIT 1` + `max_execution_time = 2`, yalnız id / tür /
desen / servis / başlangıç / son görülme / türetilmiş durum (+ probe varsa `verified_ratio`); sample / oranlar /
bölüm kolonları yok. Zaman sınırı yok: 30 gün TTL'li küçük state tablosu, `GetAnomalyEvent` emsali.
`serveCached` 30 s, anahtar olay kimliği. Olay yok → alan yok; okuma hatası önbelleğe yazılmaz. **Ön yüz:**
`AlertProblemDetail` sol kolonunun ilk bölümü olay detayıyla AYNI `LogPatternCountSection` (açık; sözleşme aynen:
kova ≤120, ≤7 gün, 60 s önbellek, yalnız aktifte 60 s yoklama) + "Correlated signals"ta desen log pivotu
(`patternLogsPivot`). Grafik yalnız `hasLogPatternSeries` kaynağında (ikinci yüklem yok); kaynak aynı bölümdeyse
olayın alanları (olay detayıyla aynı sorgu anahtarı), olay yeni bölüme geçtiyse Problem'in kendi penceresi
(`promotedPatternChartEvent`). Okunurken / hata / desen dışı türde bölüm çizilmez.

## 2026-10-05 — Menüde "Exceptions" → "Exceptions/Errors" (v0.10.1107)

**Operatör:** "Exceptions sayfa ismi de menüde Exceptions/Errors olarak gözüksün. Çok uzun olacaksa sadece Errors
olsun." Oracle hata tablosu grupları (v0.10.1092) ve HTTP hata grupları da bu sayfada; ad yalnız exception demiyor.
**Karar:** yalnız kenar çubuğu etiketi (`nav.problems`, iki dil) "Exceptions/Errors" — 17 karakter, menüdeki
"Deployment/Rollouts" (19) zaten sığıyor, kısaltmaya gerek yok. Rota (`/problems`), URL'ler, sayfa içi metinler,
komut paleti anahtarları değişmedi.

## 2026-10-05 — Oracle hata grupları kullanıcıya "Teknik hata" olarak görünür (etiket ayarlanabilir) (v0.10.1108)

**Operatör:** "Oracleden gelen problemlerde exceptionsta Oracle yazıyor onun yerine başka bir şey yazsa. Teknik
Hata gibi mesela". **Karar:** `ora:` hata tablosu gruplarının (v0.10.1092) GÖRÜNEN adı ayar oldu: branding
blobuna `oracleGroupLabel` (Settings › Branding › "Oracle hata grubu etiketi"); boş = **"Teknik hata"**. Sunucu
kırpar ve 40 rune'a keser (`chstore.NormalizeBranding`, yazışta ve okuyuşta); api.go büyümedi. **Uygulandığı
yerler:** Exceptions satır rozeti + soluk satır (`<etiket> · <kaynak> · N servis`) + ipucu, çip ("<etiket> N" /
"<etiket> hariç"), ProblemDetail rozeti, detay kartı başlığı ("<etiket> grubu") ve okuma hatası, AI paneli bağlam
satırı; sunucuda öncelik gerekçesi ("<etiket> patlaması", "yeni <etiket> grubu", "sürekli akış (<etiket>)") —
`chstore` atomiği, `GetBranding`/`PutBranding` ve 30 sn triyaj yenilemesi yayınlar (satır başına CH okuması yok;
başka podun kaydı ≤30 sn). FE saf yardımcılar etiketi PARAMETRE alır (`useBranding().oracleGroupLabel`).
**Bilerek Oracle kalan:** title/ipucu açıklamaları ("Oracle hata tablosu satırlarından oluşan grup"), çipin
aria-label'ı, detay kartı alt satırı ve Settings yolu dipnotu, trace-yok ipucu; Inbox `source` alanı "Oracle"
(exception satırında çizilmiyor, arama anahtarı); kod adları, `?oracle=`, `ora:` parmak izi; AI istemleri
(model Oracle olduğunu bilmeli). Bildirim e-postası zaten "Oracle" taşımıyordu.

## 2026-10-05 — Oracle "Teknik hata" grupları Problems sayfasında ve bildirimde (v0.10.1109)

**Operatör:** "oracledan gelen teknik hatalar problems sayfasında da gözüksün isterim" / "bu sayede P1 tipinde
gözüken oracle teknik hataları notifikasyon da gönderilmiş olur". **Bulgu 1 — liste DÜŞÜRMÜYORDU:** gerçek bir
ClickHouse'a karşı tam `inboxView` derlemesi (varsayılan `prio=P1`, ilk görülme sıralı) `ora:` P1 grubunu exception
satırı, P1, "<etiket> patlaması" gerekçesiyle döndürdü; tür kuralı, oluşum tabanı, katlama, tavan ona dokunmuyor
(pin `api/inbox_oracle_default_view_test.go`, düzeltmeden önce de yeşil). Görünmezlik **kimlikti**: v0.10.1108
etiketi yalnız Exceptions'a girmişti; Problems satırı span exception'ından ayırt edilemiyordu (yalnız kod +
operasyon). **Bulgu 2 — kök neden bildirimde:** kanal yolu (`notify/exception_notifier.go` `routeGroups`) her gruba
span grubunun tazelik kapısını (`isChannelCandidate`: `new` yalnız ilk görülmeden sonraki 15 dk, ya da `regressed`)
uyguluyordu. Oracle P1'i yapışkan değil, saatlik toplamlardan doğan bir OLAY (patlama ya da P1 penceresinde yeni
grup); (kaynak, kod, operasyon) grupları kalıcı olduğundan patlama hemen her zaman günler önce doğmuş bir grupta olur
→ Exceptions'ta P1 görünen Oracle grubu HİÇBİR kanala gitmiyordu. İkincil (yapısal): tarama `last_seen DESC LIMIT 300`
idi; Oracle grubunun son görülmesi kapanmış dakika gecikmesi kadar geride, büyük filoda canlı span grupları onu
pencereden itebiliyordu. Elenen şüpheliler: istatistik önbelleği worker'da da kurulu (`main.go`, her rol); triyaj
ayarı her rolde hidrate; `inboxKeepSourcePriority` exception türüne dokunmaz; env süzgeci Exceptions ile Problems'te
simetrik. **Karar:** (1) Oracle grubu merdivende P1 VE (gecikme kaydırılmış) son 10 dk içinde görülmüşse ilk görülme
yaşına bakılmadan kanala girer; kimlik `exception-group:ora:<hex>:p1` (Sustur bağlantısı kalıcı; grubun yeni-grup
Sustur'u ve "problem değil" de susturur); grup başına 4 sa soğuma (`exOracleP1Cooldown`; lider defteri + yeni
`chstore.LastNotificationAt` — yalnız soğuma penceresini okur (`ORDER BY sent_at DESC LIMIT 1`, 10 sn; tablo
related_id indeksi taşımaz, 90 günü taramaz); restart'ta çift yok, soğuma sonrası yeni patlama yeniden); susturulmuş
grup defterde "alındı" (negatif önbellek — patlayan susturulmuş grup her tik CH okumaz); P1 gidince grubun taban
kimliği de defterde, aynı bölümün P2'si çalmaz. Sentetik Problem adı "<etiket> · <kod> · <operasyon>", P1 →
critical, tür exception (kanalın "Exception" türü). **Fırtına özeti:** aynı tikte bir kaynağın 5'ten FAZLA grubu P1
olursa (DB kesintisi, deploy, kaynak shadow → live) tek tek N kritik yerine TEK özet — `exception-group:ora-source:
<kaynak>:p1`, "<etiket> · <kaynak> · N grup P1", gövdede son 1 sa'e göre ilk 10 grup, aynı soğuma, bağlantı
Exceptions'ın Oracle çipi; özete giren grupların `:p1`'i talep edilmiş sayılır (sonraki tiklerde tek tek çalmazlar);
≤ 5 grup ve kaynağı çözülemeyen grup tek tek. Kaynak + son 1 sa aynı `GroupStatsCache`'ten (main.go
`SetOracleGroupRef`). (2) Kanal taraması dört geçiş: span (`oracle=exclude`, şekli aynı) ve Oracle (`only`, son
görülme ≥ şimdi − 40 dk: 10 dk tazelik + 30 dk en çok gecikme) ayrı, new/regressed; `ListExceptionGroups`'a
`max_execution_time = 10`.
(3) Problems satırında Oracle grubu Exceptions'taki etiket rozetini ve "<etiket> · <operasyon>" soluk satırını
taşır; sentetik `oracle:<kaynak>` servisi link almaz. **Değişmeyen:** Oracle öncelik kuralı (P1 yalnız patlama /
yeni grup), Problems varsayılanı (yalnız P1 + ilk görülme), span gruplarının kanal kuralı ve kimlikleri, takım
anonsu (`run`, grup ömrü başına bir), kaynak kipi kapısı — **shadow kaynağın grubu yine bildirim almaz**, bildirim
için kaynak `problemMode=live` olmalı. Bilinen sınırlar: Problems araması "teknik" ile Oracle satırı bulmaz (SQL
araması kod/operasyon/servis; kaynak `source` "Oracle"); env seçiliyken sentetik servisli grup iki sayfada da gizli.

## 2026-10-05 — Merkezi login yetki servisi: POST /api/auth/permissions + token claim'inden rol (v0.10.1110)

**Bağlam (müşterinin merkezi login standardı):** merkezi OIDC girişi, kullanıcı oturumu başına BİR kez (token
yenilemede değil) uygulamanın "yetki servisine" POST atar ve dönen `permissions` dizisini access token'a claim
olarak gömer; 200/204 dışı her durumda token claim'siz basılır; zaman aşımları bağlantı ≤1 s, cevap ≤2 s.
**Sözleşme (uygulanan):** `POST /api/auth/permissions`, gövde `{"userId","username","email","registrationNumber"}`
(JSON ≤4 KB, `registrationNumber` zorunlu, alan ≤256) → `200 {"subject":<registrationNumber AYNEN>,
"permissions":["COREMETRY_ADMIN"|"COREMETRY_EDITOR"|"COREMETRY_VIEWER"|"COREMETRY_ROLE_<AD>"],"ttlSeconds":N}` ·
`204` gövdesiz (YALNIZ devre dışı hesap) · `400` bozuk gövde · `401` gövdesiz (anahtar yok/yanlış) · `404`
gövdesiz (servis kapalı ya da anahtar yok). Sıra 404 → 401 → 400: kimliksiz çağıran gövde doğrulamasından bilgi
almaz. Özel rol (viewer tabanlı) katalogda varsa `COREMETRY_ROLE_<BÜYÜK_AD>`, yoksa taban rol. **Eşleme sırası
(operatör kararı):** (1) `email` → kullanıcı e-postası; (2) yoksa `ldap_username` = `username`, sonra =
`registrationNumber` (küçük harf; yeni `GetUserByLdapUsername`, FINAL + LIMIT 1); (3) yoksa **tanımsız kullanıcı →
viewer** — her adım tek sınırlı okuma, disabled satırları da görür (aktif önce). **Operatör kararı (2026-10-05):
"herkesin viewer rolünde login olabilmesi gerekir — kullanıcı ilk defa login olacaksa da viewer":** tanımsız
kullanıcıya 200 + SSO `defaultRole`'ün yetkisi (varsayılan `["COREMETRY_VIEWER"]`), `subject` yankısı ve aynı TTL;
**204 yalnız devre dışı hesap** (204 = "bu uygulamada yetki yok", merkezi login'de girişi engelleyebilir; devre dışı
hesabı Coremetry girişi de reddeder). **Güvenlik:** uç oturumsuz (sunucudan-sunucuya) — `auth.SkipPath` yalnız `POST
/api/auth/permissions`'ı muaf tutar (login / OIDC callback'le aynı yer); sınır `X-Coremetry-Auth-Key` paylaşılan
anahtarı, SHA-256 özetleri üzerinden `crypto/subtle` ile karşılaştırılır (uzunluk da sızmaz). Anahtar bir secret:
OIDC blobunda saklanır, hiçbir GET/PUT cevabında, audit'te ya da logda yok (`permissionServiceKeySet`), boş PUT
kayıtlıyı korur (clientSecret kalıbı, kimlik koşulu yok); ≥16 karakter, boşluksuz ASCII, yer tutucu reddi
(`weakSecretMarkers`). Depo hatası 500 gövdesiz (iç ayrıntı dış sisteme gitmez). Hız sınırı EKLENMEDİ: login'de
de sınırlayıcı yok; yanlış anahtar depoya ulaşmaz (yalnız bir özet), ve paylaşılan ingress arkasında IP kilidi
meşru IdP'yi kilitleyebilirdi — yerine sayaç `auth_permission_requests_total{result}` (401 fırtınası görünür).
Log çağrı başına tek satır, slog DEBUG, PII yalnız kullanıcı kimliği. **Ayarlar:** `auth_oidc` blobuna
`permissionServiceEnabled`, `permissionServiceKey`, `permissionTTLSeconds` (varsayılan 300, 60–3600 kıskacı),
`permissionsClaim` (varsayılan `permissions`), `roleFromClaim` (varsayılan KAPALI); servis SSO'nun açık olmasından
bağımsız, alanları SSO kapalıyken de doğrulanır; son-iyi ve audit (`settings.oidc.update`, anahtar yerine
`permissionServiceKeyChanged`) mevcut OIDC yolundan. **Coremetry'de kalan:** rol yönetimi — admin rolü Kullanıcılar
sayfasında atar, servis yalnız RAPORLAR. **Operatör kararı (2026-10-05): "oidc ile kullanıcılar login olduğunda yine
default viewer olsun."** `roleFromClaim` varsayılan kapalı; kapalıyken claim hiç okunmaz, giriş bugünkü gibi: ilk
kez gelen OIDC kullanıcısı `defaultRole` (viewer) ile açılır, rol yalnız Coremetry'den değişir. Açıkken claim access
token'dan okunur (id_token ile aynı JWKS'le imza doğrulanır, aud denetimsiz; opak ya da doğrulanamayan access
token'da id_token'daki aynı adlı claim); claim YALNIZ dizgi dizisi kabul edilir (tek dizgi yok sayılır) ve claim adı
standart kimlik/profil claim'i (`sub`, `name`, `preferred_username`, `email`, … `azp`) olamaz — kullanıcının
düzenleyebildiği serbest metinden rol okunmasın. `ADMIN > EDITOR > VIEWER` ilk eşleşen, yalnız KAYITLI kullanıcıya
uygulanır ve **claim'den rol yalnız düşürür; yükseltme Users sayfasından** (müşteri cevabı önbellekliyor, token IdP
oturumu boyunca yaşıyor — bayat bir claim düşürülmüş ya da ele geçirildiği için düşürülmüş bir admin'i yeniden
yükseltemesin). Düşürme audit'lenir (`user.set_role_from_claim`, aktör rolü ÖNCEKİ rol, istek IP'si, ayrıntıda
from→to), son admin düşürülmez; claim yok / boş / tanınmayan → rol değişmez (müşteri servis cevap vermeyince
token'ı claim'siz basıyor; Coremetry'de atanmış rol korunur); ilk giriş bu açıkken de `defaultRole`. Yazım
callback'te: `api.go` `oidcCallback`'in kullanıcı okuması TEK satırda `s.oidcLoginUser(r, email, claims)`
(`auth_permissions.go`) ile değişti, satır sayısı aynı (11576). **Mevcut hata düzeltildi (güvenlik incelemesi):**
`GetUserByEmail` `disabled=0` süzdüğü için callback devre dışı bırakılmış kullanıcıyı "yok" sayıp YENİ bir viewer
satırıyla yeniden açıyordu (`users ORDER BY id`); artık `GetUserByEmailAnyState` ile disabled satır görülür ve giriş
"hesap devre dışı" ile reddedilir. **Dayanıklılık:** depodaki / içe aktarılmış blobda yetki servisi alanları
geçersizse SSO düşmez — yalnız servis kapanır (anahtar düşer, claim'den rol kapanır, `lastError` + log); PUT yolu
aynı alanlara 400 verir. **Bilinen, bırakılan:** (1) müşteri cevabı `ttlSeconds` boyunca önbellekler — düşürme
yönünde bayat claim, Coremetry'de yeni yükseltilmiş bir kullanıcıyı o süre içindeki girişte geri düşürebilir; TTL
kısa tutulur (operatör belgesi: `docs/SSO-PERMISSION-SERVICE.md`). (2) Son-admin kapısı oku-sonra-yaz (CountAdmins →
Upsert); iki admin aynı anda claim'le düşürülürse ikisi de geçebilir — `setUserRole`'daki kapıyla aynı yarış sınıfı.
(3) Yetki servisi anahtarı, `clientSecret` gibi, `auth_oidc` blobuyla birlikte yapılandırma dışa aktarımına/yedeğe
girer.

## 2026-10-05 — Yetki servisi: anahtar isteğe bağlı, IP/CIDR izin listesi (v0.10.1111)

**Operatör (2026-10-05): "Anahtarsız olmaz mı"** — müşterinin merkezi login'i yetki servisine özel bir başlık
(`X-Coremetry-Auth-Key`) gönderemiyor; v0.10.1110'daki zorunlu anahtar entegrasyonu engelliyordu. **Karar:**
`auth_oidc` blobuna `permissionServiceAllowNoKey` (varsayılan KAPALI). Açıkken başlık **hiç denetlenmez** —
kayıtlı bir anahtar olsa bile (anlam belirsizliği yok: "anahtar varsa yine iste" kipi YOK); kapalıyken bugünkü
davranış (anahtar zorunlu, anahtar yoksa `404`). Doğrulama: servis açık + anahtarsız kip kapalı + anahtar yok →
400 (bugünkü gibi); anahtarsız kip + anahtar yok geçerli; girilen anahtar yine ≥16/yer tutucu kurallarından
geçer. **Risk:** anahtarsız uç, ona ağdan erişebilen herkese bir kullanıcının Coremetry rolünü bildirir (e-posta /
kullanıcı adı / sicil tahminiyle rol keşfi); uç yalnız RAPORLAR — rol değiştirmez, oturum açmaz, ve bildirdiği
yetkiyi token'a ancak merkezi login gömer. **Azaltım:** `permissionServiceAllowedCIDRs` (≤32 IPv4/IPv6 CIDR ya da
tek IP, `net/netip`; kanonik biçimde saklanır; boş = kısıt yok); doluysa listede olmayan çağıran `403` gövdesiz,
anahtar ve gövde denetiminden ÖNCE. Sıra: `404` → `403` → `401` (anahtarsız kipte atlanır) → `400` → eşleme;
sayaç etiketi `ip_denied`. Settings > SSO: "Anahtarsız kabul et" + tek satır uyarı ("uç ağdan erişen herkese açık
olur; IP izin listesi kullanın"); anahtarsız ve liste boşken görünür uyarı satırı (engel değil — operatör ağ
katmanında, ör. NetworkPolicy ile, daraltmış olabilir). **Çağıranın IP'si — `clientIP(r)` KULLANILMADI:** o
yardımcı `X-Forwarded-For`'un ilk girişine koşulsuz güvenir; audit/iz için yeterli ama erişim kararı için sahte
başlıkla aşılır (çağıran XFF'i yazar; başlığı sonuna EKLEYEN ingress'lerde — ör. OpenShift router varsayılanı — ilk
giriş çağıranındır; doğrudan pod erişiminde tamamı). Repo'da genel bir güvenilen-vekil mekanizması yok
(`auth.trusted_header.trusted_proxies` yalnız trusted-header giriş kipine bağlı, config.yaml/Helm'de); bu yüzden aynı blobda
üçüncü alan `permissionServiceTrustedProxies` (≤32 CIDR): doğrudan eş bu listede DEĞİLSE çağıran `RemoteAddr`'dır
ve XFF hiç okunmaz; listedeyse XFF sağdan sola yürünür, ilk güvenilmeyen adres çağırandır ("rightmost untrusted");
ayrıştırılamayan giriş → red. Liste boşken XFF okunmaz — ingress arkasında izin listesi ingress adresini görür;
Settings bunu da uyarır, belge (`docs/SSO-PERMISSION-SERVICE.md`) ingress CIDR'ını yazmayı söyler. **Dayanıklılık:**
depodaki blobda bozuk CIDR → SSO düşmez, yalnız servis kapanır (N4 kalıbı; ağ alanları da sıfırlanır); her
ihtimale karşı ayrıştırılamayan liste çalışma anında servisi kapatır (atlanan giriş listeyi boşaltıp ucu açmasın).
Ağ alanları secret değil: GET'te ve audit'te (`settings.oidc.update`) aynen. **Bilinen, bırakılan:** (1) anahtarsız
kipte kayıtlı eski anahtar silinmez (kullanılmaz; boş PUT kayıtlıyı korur kuralı aynen). (2) Anahtarsız + izin
listesi boş kayıt engellenmez — yalnız uyarı.

## 2026-10-06 — OIDC: özel CA sertifikası ve TLS doğrulamasını kapatma seçeneği (v0.10.1112)

**Operatör (2026-10-06): "oidc bağlantısının tls kontrolünü kapatma seçeneği de olsun sertifikaya takılıyor."** —
müşterinin IdP'si kurum içi bir CA ile imzalı; Coremetry'nin keşif / JWKS / token çağrıları sistem kök
sertifikalarıyla doğrulanamıyor, SSO hiç kurulamıyor. **Karar:** `auth_oidc` blobuna iki alan, ikisi de Settings >
SSO'da Issuer'ın hemen altında. (1) **`tlsCACertPEM` (tercih edilen):** bir ya da birden çok PEM `CERTIFICATE`
bloğu, **sistem havuzuna eklenir** (yerine geçmez — kamuya açık bir IdP'ye geçişte de çalışır). Doğrulama: ≤64 KB,
`x509` ile ≥1 sertifika; bloklar arası yorum satırları (CA paketleri) yok sayılır; özel anahtar bloğu (bozuk kodlu
olsa da) açık bir hatayla, başka blok türü ve çözülemeyen sertifika 400 — güvenlik incelemesi F1: SSO **kapalıyken
de** (yoksa `enabled:false` gövdesiyle bir özel anahtar bloba yazılıp GET'te yankılanırdı; yalnız ağ/keşif
denetimi kapalıyken atlanır; yüklemede de aynı). **Kanonik saklama (N2):** blob ve GET YALNIZ çözülen
sertifikaları taşır (`pem.EncodeToMemory` ile yeniden kodlanmış) — bloklar arası her metin düşer: yorumlar ve
`pem.Decode`'un atladığı, "PRIVATE KEY-----" dize denetimini de aşan zırhlı bir PGP özel anahtar bloğu; Settings
kayıttan sonra kanonik biçimi gösterir. Secret değil (açık sertifika): GET'te döner. (2) **`tlsInsecureSkipVerify` (son çare, varsayılan kapalı):** sertifika doğrulaması hiç yapılmaz;
UI kırmızı uyarı satırı gösterir ("Ortadaki-adam saldırısına açık; mümkünse CA sertifikası ekleyin."), sunucu
ayar yüklemesi başına (PUT + blob değişimi; 30 s yenilemenin değişmeyen tiklerinde değil) bir kez WARN loglar.
**Neden CA tercih:** skip-verify'da ağ yolundaki herhangi biri IdP'yi taklit edip keşif belgesini / JWKS'i
değiştirebilir — kendi imzaladığı id_token kabul edilir, yani her kullanıcı (yönetici dahil) taklit edilebilir;
özel CA güveni yalnız o kurumun CA'sına genişletir, doğrulama sürer. **Zorunlu kalan:** https kuralı (skip-verify
düz http'ye izin VERMEZ — http yalnız config.yaml `allow_insecure_issuer`) ve dial koruması (loopback /
link-local / metadata / eşlemeli adres reddi, DNS'e sabitleme) TLS ayarından bağımsız `oidcNetPolicy`'de.
**Tek taşıma kurucusu:** `newOIDCHTTPClient(policy, tls)` (`internal/auth/oidc.go`; TLS `oidc_tls.go`
`oidcTLSConfig`) keşif, JWKS (go-oidc RemoteKeySet sağlayıcı bağlamındaki istemciyi kullanır), token değişimi +
id_token doğrulaması, v0.10.1110 access token doğrulaması (aynı JWKS), userinfo (sağlayıcının istemcisi) ve
"Bağlantıyı test et" için tek istemci; `oidc_tls_test.go` hem kaynak taramasıyla (başka `http.Client` /
`DefaultClient` / `TLSClientConfig` / go-oidc ağ yardımcısı yok; her `ClientContext` ve `discoverOIDC` kurucunun
istemcisini taşır) hem davranışla (kurum içi CA ile imzalı `httptest` IdP: varsayılan düşer, CA ile ve skip-verify
ile keşif + kayıt + giriş geçer, skip-verify'da http ve loopback yine red) çivili. **Test ucu** formun
KAYDEDİLMEMİŞ TLS alanlarıyla koşar (kaydetmeden sınama). **Önbellekli keşif:** issuer aynı ama TLS ayarı
değiştiyse önbellek kullanılmaz — yeni güven kayıttan önce gerçek bir el sıkışmayla sınanır (yoksa bağlanamayan
bir TLS ayarı "kaydedildi" der, giriş token ucunda düşerdi); bedeli: IdP kesintisinde TLS alanı değiştirilemez
(diğer alanlar ağsız kaydedilmeye devam eder). **Dayanıklılık:** depodaki blobda çözülemeyen CA SSO'yu sessizce
DÜŞÜRMEZ — N4 kalıbı: yapılandırma özel CA'sız uygulanır, `lastError` yazılır (CA'sız IdP'ye bağlanılamıyorsa
`lastError` iki nedeni de taşır). **Audit:** `settings.oidc.update` eski→yeni — skip-verify bayrağı; CA için PEM
değil özet (CN · SHA-256 parmak izinin ilk 8 baytı · bitiş tarihi, en çok 10 + "+N sertifika daha") ve `changed`;
"eski" kayıtla AYNI kilit (`applyMu`) altında alınır (`SaveSettingsWithPrev`, inceleme N5 — eşzamanlı PUT'lar
çifti karıştırmaz). `settings.oidc.test` satırı yoklamanın TLS güvenini de taşır: `tlsInsecureSkipVerify` +
`customCA` (var/yok, PEM değil; N3). Gövde tavanı 64 KB → 128 KB (CA alanı). **Bilinen, bırakılan (N4, bu
sürümden önce de vardı):** taşıma `http.DefaultTransport`'tan klonlandığı için `HTTPS_PROXY`/`HTTP_PROXY` ortam
değişkeni tanımlıysa bağlantı vekile gider ve dial koruması yalnız vekil adresini denetler — hedef IdP adresinin
loopback/metadata denetimi o kurulumda vekile kalır; kurum çıkışı vekil gerektirebileceği için davranış
DEĞİŞTİRİLMEDİ. **Kapsam dışı:** config.yaml/Helm kaynaklı OIDC'ye bu alanlar eklenmedi (o yolda sistem havuzu
`SSL_CERT_FILE` / imaj CA paketiyle genişletilir); istemci sertifikası (mTLS) yok.

## 2026-10-06 — Problem detayında başlangıçta doğan log şablonları (kök neden kanıtı) (v0.10.1113)

**Operatör onaylı kuyruk maddesi ("Log şablonu kök neden bağlantısı").** `log_template_new` dedektörü v0.10.1061'den
beri varsayılan KAPALI (tek başına problem olarak gürültülü), ama templater puller'ı `log_templates` defterini
anahtardan bağımsız yazmayı sürdürüyor. **Karar:** defter alarm kaynağı olarak değil, başka bir Problem'in KANITI
olarak okunur — Problem detayında "Başlangıçta doğan log şablonları", her türde (hata oranı anomalisi, svc-slowdown,
db-health, exception, incident üyesi…): "bu başladığı anda yeni bir hata mesajı belirdi". Dedektör KAPALI kalır;
yeniden açmayı önerme.

**Pencere ve küme:** first_seen ∈ [başlangıç − 10 dk, başlangıç + 5 dk) — geri bakış uzun (kök neden belirtiden önce
loglanır), ileri bakış puller'ın 5 dk örnekleme adımı. Servisler ≤ 5: özne (yalnız kind=service) + kalıcı hipotezin
çağrı-grafiği adayları RCA sırasıyla (Kind'ı boş olmayan HER aday hariç — `node`: ad bir node, `rollout`: ad bir
`rollout:<cluster>/<ns>/<workload>@<rev>` öznesi). db / dış özne ve hipotez yoksa okuma yok, boş liste.

**"Yeni" = dedektörün kararı, çatal YOK** (`anomaly/log_template_evidence.go`): `candidateTemplatesFilter` (+ pencere
üst sınırı + `hasAny(services)`), `newTemplateCandidatesMin`, `knownTemplatesFilter` ("now" = pencere sonu, 7 gün
ufku), `usableKnownTemplatesMin`, `filterNewTemplateFamilies` (bilinen şablonun Drain-varyantı düşer, aynı aileden
yalnız en erken doğan). **Tek fark sayı tabanı:** puller Drain'i her tik sıfırlar ve satırın `total_count` /
`services` alanlarını o tikin örneğiyle EZER — dedektör için "şu an ≥ 3" doğru soru, ama geçmiş bir problemin kanıtı
şablon seyrekleşince sonradan kaybolurdu. Kanıtta aday tabanı ≥ 1 (aile süzgeci + ≤ 5 tavan küçük tutar), bilinen
kümede taban YOK (daha geniş bilinen küme yalnız daha çok varyantı gizler — güvenli yön). Paylaşılan işlevlerin
varsayılanı (dedektörün 3'ü) değişmedi; taban parametreyle geçer. **Bilinen sınır:** `services` da son tiki yansıtır —
sonradan yalnız başka bir serviste görülen şablon eski bir problemin bölümünden düşebilir (hasAny artık eşleşmez).
Bilinen okuması düşerse fail-closed (hata döner, önbelleğe yazılmaz) — dedektörün yönü. Sıra:
başlangıca yakınlık, sonra sayı; ≤ 5 satır. Şablon dedektörün 160 bayt kesimi; arama metni KESİLMEMİŞ şablondan
`logstore.PatternSearchQuery` (Şablonlar sekmesinin "Ara" sözleşmesi), satırın "Logları aç" pivotu servis + bu metin.

**Yüzey: ayrı uç** `GET /api/problems/{id}/log-templates` (`api/problem_log_templates.go`, defter kaydı), /rootcause
demetine alan DEĞİL: RootCausePanel dış seri Problem'inde çizilmiyor ve demet soğukta onlarca saniye (v0.9.1082
ölçümü) — iki ucuz okumanın cevabı onu beklemesin. `serveCached` 60 s; anahtar başlangıç ns + özne + SIRALI küme
FNV. Pencere `now`'a değil Problem'in SABİT başlangıcına bağlı, dakikaya yuvarlanmadı: yuvarlamak aynı servisleri
paylaşan iki Problem'e birbirinin "N sn önce"sini ve sırasını (≤ 59 sn) sunardı.

**Sınırlar:** istek başına bir problem + bir hipotez nokta okuması, en çok iki `ListLogTemplates` (FINAL + LIMIT ≤ 500 +
max_execution_time 5; aday yoksa bir). FE yalnız detay açıkken tek istek; yoklama yalnız açık Problem'de
başlangıçtan sonraki 15 dk boyunca 60 s (defter dolarken), sonra yok; hook `retry: 0`. Boş / ilk okuma hatası →
bölüm hiç çizilmez (boş kart yok); düşen bir arka plan tazelemesi eldeki satırları silmez. Sayı `total_count` = son
templater örnekleminin satırı ("örnek" etiketiyle), gerçek log sayısı değil. **Bilinen maliyet:** `log_templates`
`ORDER BY id`, partition ve TTL YOK — iki okuma da tablonun tam FINAL taraması, yalnız LIMIT + 5 sn
max_execution_time ile sınırlı; tablo her yeni şablon kimliğiyle (v0.10.1030 yeniden kümelemesi tik başına yeni kimlik
doğurabilir) sınırsız büyür ve prod boyutu ölçülmedi. Aynı tarama şeklini Şablonlar sekmesi, derin kanıt ve desen
açıklaması zaten ödüyor; TTL şimdi eklenmedi — büyürse ilk çare `last_seen` TTL'i (kuyruk adayı).

**Copilot'a eklenmedi (bilinçli):** tık yolu `copilotExplainProblem` api.go'da (büyüyemez), arka plan yolu
`renderEvidence` güven-kapılı ve golden pinli; yalnız birine eklemek iki Explain yüzeyini ayrıştırırdı — ayrı iş.

## 2026-10-06 — Bağımlılık: source-map-js 1.2.2 (npm audit yüksek uyarı) (v0.10.1114)

1112'nin CI'ında `npm audit` yeni yayımlanan GHSA-68fv-2mgg-jv7q (source-map-js <1.2.2, indeksli source-map
bölümüyle event-loop DoS) yüzünden kırmızı. Paket transitif ve yalnız geliştirme/test zincirinde (jsdom → css-tree;
vite/postcss); üretim paketine girmez. **Karar:** `npm audit fix --package-lock-only` — yalnız lockfile, 1.2.1 → 1.2.2;
`package.json` değişmedi. tsc / vite build / vitest yeşil. Kalan 6 orta önem uyarısı kapının dışında (yüksek+kritik
zorunlu).

## 2026-10-06 — Operations › Normalized satırından Traces'e: op_group süzgeci (boş liste düzeltmesi) (v0.10.1115)

**Kök neden:** Service › Operations, Normalized kipte satırlar operasyon ŞEKİLLERİ — `spans.op_group`, ingest'te
`templater.NormalizeOperation` üretir (`GET /users/:id`). Satır (ve satır altı panelin "View traces" / Explore
bağlantıları) /traces'i `name = <şekil>` çipiyle açıyordu: kesin ad eşitliği, hiçbir gerçek span adı
(`GET /users/8421`) şekle eşit değil → boş liste, boş histogram şeridi. `op_group` bir süzgeç anahtarı da değildi
(çıplak anahtar dizi aramasına düşüp yine boş dönerdi). **Karar:** yeni süzgeç anahtarı `op_group` — spans süzgeç
derleyicisinde (`filterexpr_opgroup.go`, `db_stmt_hash` emsali v0.10.1093) kolona çözülür: yalnız `=`, `!=`, `IN`,
`NOT IN` (şekil bir kimlik; LIKE / regex / aralık / EXISTS sınırda 400 — kolon olsun olmasın), değer `?` ile
bağlanır. **Kolon yoksa** (dış Distributed `spans`, `cluster_name` boş — boot probu `hasOpGroupCol` + self-heal) anahtar
`name` anahtarıyla birebir aynı derlenir (aynı op, aynı değerler: `name = ?` / `name IN (…)`), 400 yok; süreç başına
bir kez INFO loglanır. Gerekçe: o kurulumda op_group değerinin tek üreticisi Normalized tablosudur ve tablo orada ham
span adlarını taşır (v0.8.186 düşüşü; Explore/dashboard `groupBy=op_group` da `name`e düşer) — doğru kolon `name`,
bugün çalışan "satır → `name = <değer>`" tıklaması kırılmaz. Elle yazılmış bir şekil orada boş liste verir: daha
dar, asla daha geniş; kolon anılmaz (code 47 → 500 değil). MATERIALIZED ifade fallback'i yok: şekil Go'da üretilir.
**FE:** `opTraceFilters` Normalized kipte tek çip `op_group = <şekil>` üretir (Raw kip değişmedi: `name =`, bölünmüş
fiilde `http.route` çipleri); /traces çipi "operation shape · = · <şekil>" okunur, title / düzenleme ham `op_group`,
✕ ile kalkar; anahtar yalnız /traces sorgu kutusunun önerisine eklenir (`extraKeys`), ortak `SUGGESTED_KEYS`'e değil
(o liste aggregate "group by attribute" ve sütunları da besler, orada op_group çözülmez). Değer önerisi yok — şekil
Operations'tan pivotlanır ya da yazılır.

**Okuma yolları:** hepsi aynı derleyiciden geçer. Liste ham WHERE'i (`buildGetTracesWhere`, servis + `op_group = ?`);
arama + çipte trace düzeyi HAVING (`countIf(op_group = ?) > 0`); Errors şeridinin iki basamaklı / span kipi
(`traceErrBothWhere`, v0.10.1082/1101) listeyle bayt bayt aynı. `trace_summary_5m`'de op_group yok → çip MV hızlı
yolunu kapatır (`tracesMVEligible`, sayım planı da ham) ve span düzeyi çip sayılır (satır onarımı, kök sorusu
daraltılmamış kaynaktan) — diğer nitelik çipleriyle aynı. Hacim şeridi (metric-batch): op-MV kapısı ve dar rollup
op_group'u boyut olarak tanımaz, spanmetrics kademe çözücüsünde de yok → ham spans (zaman sınırı + LIMIT +
max_execution_time); şerit kapsamı "spans" (op_group adla aynı sınıf, giriş span'ine ait değil — v0.10.730), kind
kısıtı yok. `operation_group_summary_5m` şerit kaynağı yapılmadı (batch yüzeyinde okuyucusu yok; yeni yol açmak
yerine mevcut ham yol). Yan kazanç: Explore `groupBy=op_group` satırından kaynağa iniş ve dashboard TopN
`groupBy op_group` + "traces" bağlantısı da artık eşleşir (ikisi zaten `op_group =` çipi üretiyordu).

**Değişmeyenler:** Raw kip pivotları, metric_points süzgeç yolu (orada kolon yok), Normalized'da gizli kapsamlı grafik
simgesi (`?op=`, v0.8.422). **Bırakılan:** /endpoints "Group by shape" (okuma anında `op_sig` regex'i, `http_route`
üzerinde) satırının ve endpoint detayının Traces / Explore pivotu `http.route = <şekil>` taşır — rotası ham id içeren
kurulumda aynı boş liste. Doğru düzeltme `endpointRoutePred`'in `opSigWrap(http_route) = ?` yüklemini taşıyan ayrı
bir süzgeç anahtarı (desen argümanları bağlı; v0.8.356 clickhouse-go parametre tuzağı ayrıca doğrulanmalı) — kuyruk
adayı. Kolonsuz kurulumda Normalized kipin ham ada düştüğünü FE'ye söyleyen bir sinyal yok (çip yine "operation shape"
der, sunucu `name` olarak eşler).

## 2026-10-07 — log_templates: 30 gün TTL (tablo sınırsız büyüyordu) (v0.10.1116)

**Sorun:** `log_templates` `ReplacingMergeTree(version) ORDER BY id`, partition ve TTL YOK. v0.10.1030'dan beri
biliniyor: puller Drain'i her tik örneklemden yeniden kurduğu için şablon kimlikleri kayar, her tik yeni satır
gelir. Okuyucuların hepsi (`log_template_new` dedektörü — varsayılan kapalı ama defter yazılıyor; v0.10.1113 Problem
kanıtı; /logs Şablonlar sekmesi) tam FINAL taraması yapıyor, sınır yalnız LIMIT + max_execution_time. 1113 bunu
"ilk çare `last_seen` TTL'i" diye kuyruğa almıştı.

**Karar:** `TTL toDateTime(last_seen) + INTERVAL 30 DAY` (`chstore/log_templates_ttl.go`). `last_seen` DateTime64(9) →
`toDateTime` (CH 24.8 code 450); `toDate` yok (partition yok, hizalama kazancı yok). 30 gün: dedektörün bilinen-şablon
ufku 7 gün, 1113 kanıtı problem başlangıcı çevresini ister. Hâlâ görülen şablonun last_seen'i her tik tazelenir —
yalnız 30 gün HİÇ örneklenmemiş şablon düşer. Ayar değil sabit: `retention.*` yalnız sinyal tablolarını listeler,
state tabloları DDL'de sabit TTL taşır. `ttl_only_drop_parts` UYGULANAMAZ (partition yok); satırlar TTL merge'lerinde
silinir (`merge_with_ttl_timeout`, CH varsayılanı 4 sa gecikme).
**Yeni kurulum:** CREATE TTL'i taşır. **Mevcut kurulum:** tek seferlik `ALTER TABLE log_templates MODIFY TTL …
SETTINGS materialize_ttl_after_modify = 1, alter_sync = 0`, `system_settings[log_templates_ttl_v1]` işaretiyle.
New()'in sonunda, DDL ertelemesi kapandıktan SONRA ayrı goroutine'de koşar — migrate() içinde küme kipinde execDDL
ifadeyi erteleyip nil döndüğü için işaret ALTER uygulanmadan yazılırdı. Tablo zaten TTL taşıyorsa (`engine_full`'da
TTL cümlesi; operatörün başka TTL'i de ezilmez) yalnız işaret; tablo yoksa işaretsiz bekler; hata → işaret yok,
sonraki boot. Küme: state tablosu → adaptDDL yalnız `ON CLUSTER` ekler (`_local` / Distributed yok); code 159
"kuyruğa alındı" başarı sayılır. Materialize = 1 bilinçli (retention.go'nun tersi): tek seferlik, arka planda, küçük
state tablosu; 0 ile birikim yalnız eski part'lar bir gün birleşince düşerdi (clickhouse local'de ölçüldü: 0 →
6000/6000 satır kalır, 1 → süresi dolan yarı düşer).

**Beklenen etki:** tablo 30 günlük şablon kayması kadar sınırlanır; tam FINAL taramaları aynı şekilde ama bu sınırlı
tabloda. Prod boyutu ölçülmedi — kazanç birikimin büyüklüğü kadar, rakam iddia edilmiyor. **Anlam değişikliği:** 30 gün
susup dönen şablon yeniden doğar (yapışkan first_seen satırı bulamaz) — dedektör zaten 7 gün susanı bilinen saymıyordu;
1113 kanıtında dönüş anı "doğum" görünür. 30 günden eski bir problemin kanıtı, o şablonlar o günden beri görülmediyse
boşalabilir. Çok pod aynı anda boot ederse ALTER birden çok gidebilir (idempotent, ek MATERIALIZE mutasyonu).
**Değişmeyen:** okuma sorguları, puller, purge sınıflaması (`log_templates` korunan listede).

## 2026-10-07 — /endpoints → Traces: rota şekli süzgeci ve RPC linki (boş liste düzeltmesi) (v0.10.1117)

**Kök neden (1115'in bıraktığı iki kardeş):** (1) /endpoints "Group by shape" satırı bir ŞEKİLDİR — okuma anında
`opSigWrap(http_route)` (`/users/8421` → `/users/:id`); satırın ve endpoint detay sayfasının Traces / Explore pivotu
`http.route = <şekil>` taşıyordu: kesin kolon eşitliği, span'ler ham id taşıdığında boş liste ve boş şerit. (2) RPC &
Messaging sekmesinde satır kimliği span ADI ve `http_route` tanım gereği `''`; aynı pivot `http.route = <ad>` gönderdiği
için liste HER ZAMAN boştu. **Karar:** yeni süzgeç anahtarları `http.route_shape` → `opSigWrap(http_route)` ve
`name_shape` → `opSigWrap(name)` (RPC sekmesi + şekil; aynı derleyici, `filterexpr_routeshape.go`). İfade endpoints
sayfasının gruplamada kullandığının ve detay çekmecesinin `endpointRoutePred` yükleminin KENDİSİ (bayt bayt aynı,
testli) — op_group'un ingest-zamanı `NormalizeOperation`'ı değil (base64url / ünsüz-yalnız kimlikleri opSig katlamaz,
eşleşme kayardı). Yalnız `=`, `!=`, `IN`, `NOT IN`; diğerleri sınırda 400. Kolon fallback'i yok: `http_route` ve `name`
spans'in CREATE TABLE kolonları, `http.route` / `name` anahtarları bugün de koşulsuz onlara derlenir.

**v0.8.356 parametre tuzağı:** clickhouse-go ham sorgu metni bir satırda `{…:…}` içerirse sunucu tarafı parametre
kipine geçer ve konumsal argümanlar düşer. opSigWrap metninde `':id'` sabitleri durur; desenler (süslü parantezli
regex'ler) ve kullanıcı değeri (`/users/{id}` dahil) `?` ile BAĞLANIR, metne girmez — yeni metin üretilmez, mevcut
opSigWrap + opSigArgs yeniden kullanılır. Kanıt: `ch_bind_roundtrip_test.go` gerçek sürücüyü (HTTP protokolü + sahte
RoundTripper, el sıkışmaya Native blok) Store metotlarıyla koşturur — liste, arama+çip HAVING, Errors şeridi, hacim
şeridi; harness kontrolü v0.8.356 öncesi satır içi metnin `ErrUnsupportedQueryParameter` ile düştüğünü doğrular.
`TestOpSigWrapBindSafety` şekil anahtarlarının her op'unu kapsayacak şekilde genişletildi.

**Okuma yolları:** 1115 ile aynı — liste ham WHERE, arama+çip HAVING, Errors şeridi liste paritesi, herhangi bir çip
`trace_summary_5m` hızlı yolunu kapatır, hacim şeridi op-MV / dar rollup / spanmetrics kademesi şekli tanımadığı için
ham spans. Şerit kapsamı: `http.route_shape` `http.route` gibi giriş kapsamı (kind kısıtı); `name_shape` `name` sınıfı
(spans). **FE:** `endpoints/links.ts` `endpointIdentityFilter` — HTTP ham `http.route`, HTTP şekil `http.route_shape`,
RPC `name`, RPC şekil `name_shape`; liste satırı, detay Traces ve Explore aynı üreticiden. Çip "route shape" / "name
shape", ✕ ile kalkar; `http.route_shape` /traces öneri listesine `extraKeys` ile girer (SUGGESTED_KEYS'e değil);
`name_shape` yalnız pivot (elle yazılan ad şekli için `op_group` var). Ek: liste sayfasında şekil kipinde ⚠ route
alarmı gizlendi (detay sayfasının `!sig` kuralı) — kural `http_route = <şekil>` eşler, hiç tetiklenmezdi.

**Bırakılan:** endpoint detay sayfasının kendi bölümleri (`/api/endpoints/detail`, callers, where-the-time-goes)
`entry` parametresi almıyor ve `endpointRoutePred` ile `http_route` eşler — RPC satırının detay bölümleri bu yüzden
boş kalır (Traces/Explore pivotları düzeldi; bölümler ayrı iş). Bilinen artık: cluster/env süzgeçli ham yol
(`getEndpointsRaw`) `opSigWrap(coalesce(http_route, attr http.route, url.path, http.target, ''))` ile gruplar; satır
yalnız alt katmanlardan (http_route boşken url.path / http.target) geliyorsa `http.route_shape` (kolon üzerinde)
eşleşmez — aynı sınır bugün ham kipte `http.route` için de geçerli (detay çekmecesi de yalnız `http_route` eşler).

**Sınır doğrulaması (inceleme ek):** `parseFiltersAndDSL` `dsl=` yapraklarını `ValidateFilters`'tan geçirmeden
ekliyordu; op kısıtlı anahtarda (`dsl=http.route_shape ~ x`, op_group, db_stmt_hash) derleme hatası ApplyFilters'ta
loglanıp atlanıyor, sorgu SÜZGEÇSİZ koşuyordu. Artık `errBadRequest` → 400 (`dsl_validate.go`; api.go büyümedi).
Dashboards bundle'ı hatayı slot gövdesine yazar (JSON `filters` ile aynı sözleşme).

## 2026-10-07 — Op-latency / servis yavaşlama: 24 sa taban önbelleği, tur başına yalnız güncel kovalar (v0.10.1118)

**Ölçüm (operatör prod `system.query_log`, son 1 sa):** en pahalı sorgu `operation_summary_5m` operasyon pivotu
(`chstore.OpP99PivotQuery`) — n=13/sa, ort. 1692 ms, en çok 2524 ms, ort. okuma ≈ 974 MiB; kardeş biçim (yaygın
yavaşlama, p95'li) n=3/sa, 774 ms, 977 MiB; toplam ≈ 16 GB/sa. Her tur ~24 sa'lik tDigest durumlarını YALNIZ taban
(slot 0) için yeniden birleştiriyordu; taban turlar arasında neredeyse kıpırdamaz.

**Karar:** iki tüketici (`trace_op_latency` dedektörü ve `svc-slowdown` operasyon kolu) okumayı `Store.OpP99Pivot`'tan
yapar. (1) **Taban önbelleği** (`op_p99_cache.go`): çift başına base_p99 + base_p95 (aynı tDigest birleşimi), base_calls,
base_buckets; TEK sınırlı sorgu (`OpBaselineQuery`: zaman aralıklı WHERE, `HAVING base_calls ≥ MinCalls`, LIMIT 500 000,
`max_execution_time = 60`), pencere `[E − 24 sa, E)`. Liderin belleğinde; ilk cari kova − E ≥ 1 sa olunca ARKA PLANDA
tazelenir (kendi 70 s bağlamı — svc-slowdown'un 10 s bütçesine ve recorder turuna binmez), anahtar başına tek uçuş.
**İki kapsam TEK tabanı paylaşır** (saatte bir 24 sa okuması, iki değil): anahtar yalnız tabanın değerini değiştirenler —
pencere uzunluğu + çağrı tabanı (vida farklıysa iki giriş). svc-slowdown'un batch dışlaması tabanda uygulanmaz: servis
bazlı satır süzgeci bir çiftin taban değerini değiştirmez, tur sorgusunun iç WHERE'inde kalır → dışlanan çiftin tabanına
hiç bakılmaz (canlı motor testi: batch çiftleri tabanda varken eskiyle aynı satırlar). Kapsamların ilk cari kovaları ~1
kova farklı (op_latency sürdürme penceresi başı, svc-slowdown tek cari kova): E = son 15 dk'da görülmüş kapsamların ilk
cari kovalarının EN ERKENİ — ikisi de zamanla yalnız ilerler, E hiçbirinin cari kovasını tabana sokmaz; yeni / büyümüş
(dwell) kapsam E'den önce başlarsa "ahead" → o tur eski yol + tazeleme.
(2) **Tur sorgusu** (`OpP99CurrentQuery`): yalnız `[SlotStarts[dwell−1], AlignedNow)`, aynı kova numarası ve tDigest
birleşimi; tabandan bağımsız tabanlar HAVING'de (cur_calls, cur_p99, cur_p95, önceki kovaların çağrı + mutlak p99'u, aktif
çift muaf); batch kalıbı `toUInt8(<BatchServiceSQL>) AS is_batch` — eski HAVING'in AYNI SQL yüklemi; LIMIT 50 000.
(3) **SAF birleşim** `JoinOpP99Pivot`: eski HAVING'in tabana bağlı koşulları (base_calls, base > 0, her kovada
cur ≥ kat × taban çarpım biçimiyle, aktif muafiyeti, CurP95MinMs, batch yük kapısı + muaf çiftler), ORDER BY oran DESC,
LIMIT 200 — birebir. Eşit oranların sırası SQL'de tanımsızdı; artık (servis, operasyon) artan, kararlı.

**Eşdeğerlik:** `op_p99_cached_test.go` — eski SQL'in metinden yazılmış Go referansı ile yeni yol (tur HAVING'li ve
HAVING'siz) dwell 1/2/3, histerezis, batch + muaf, BaseP95, CurP95MinMs, sınır eşitlikleri, NaN, eşitlik + LIMIT kesimi ve
400 rastgele vakada aynı satırlar; `clickhouse local` testinde gerçek üç sorgu metni aynı fikstürde aynı satırları
döndürür. Ön şart: tabanlardan en az biri > 0 (cari verisi olmayan çift eski HAVING'den geçemez) — değilse eski yol.

**Okuma beklentisi: ~2–7× az (≈ 2.3–7 GB/sa, parça yerleşimine bağlı) — kesin sayıyı YALNIZ prod `query_log` verir**
(`read_bytes` + `op_pivot_reads_total{path}`). Mantıksal pencere 24 sa + dwell × 5 dk (290 / 289 kova) yerine dwell
kovası (2 / 1), artı paylaşılan tabanın saatte bir 24 sa okuması (≈ 1 GB). Ama ORDER BY `(service_name, name,
time_bucket)` — zaman ÜÇÜNCÜ kolon, birincil indeks son 10 dk'yı daraltamaz; budama yalnız gün bölümü + parça başına
time_bucket minmax'ı ile: okunan = cari pencereyle kesişen parçalar (taze küçük parçalar + şimdiye dek birleşmiş büyük
bir parça varsa onun TAMAMI; en kötü durumda bugünkü bölümün çoğu). Yetmezse kuyruk adayı: time_bucket önde projeksiyon.

**Davranış değişiklikleri (bilinçli):**
- **Taban gecikmesi:** önbellek penceresi gerçeğin ≤ 1 sa (+ tazeleme süresi) gerisinde; aradaki dilim ne tabana ne cari
  kovalara girer. Pencere sonu hiçbir zaman bir kapsamın ilk cari kovasından sonra değil: olay kendi tabanını eskisinden
  fazla seyreltemez.
- **Son bir saatte ilk kez görülen operasyonun tabanı yok → alarm üretemez** (eski yolda o saatte ≥ 30 çağrısı varsa
  tabanı olurdu); bir sonraki tazelemede (≤ 1 sa) tabana girer.
- **Uzun olayda taban olayı ~1 sa GEÇ emer:** 24 sa pencere olay kovalarını eskisinden ≤ 1 sa sonra içermeye başlar —
  uzun yavaşlamada sinyal en çok ~1 sa daha uzun sürer.

**Geri dönüş:** iyi taban yok (ilk tur, lider değişimi) / taban LIMIT'e dayandı / taban > 6 sa bayat / ileride / tur
sorgusu LIMIT'e dayandı → o tur ESKİ tek geçiş (tespit durmaz). **Bekleme:** tazeleme hatası → son iyi taban korunur,
yeniden deneme 15 dk sonra (iyi taban yokken her tur eski yol + 24 sa tazeleme = eskinin ~2 katı okuma olurdu, CH
zorlanırken daha kötü); LIMIT'e dayanan taban → 6 sa yeniden deneme yok (log + `op_baseline_refresh_duration_seconds{
result="capped"}` + `op_pivot_reads_total{reason="capped"}`). Tur sorgusu hatası çağırana döner (zorlanan CH'ye ikinci
ağır sorgu yok). **Bilinen boşluk:** liderliği kaybeden pod önbelleği belleğinde tutar (`LeaderHolder`'da kayıp kancası
yok); yeniden lider olursa 2 sa'ten uzun kullanılmamış giriş sıfırlanır, daha kısa aradaki taban 6 sa bayatlık sınırında
zaten geçerli. Öz-gözlem: `op_pivot_reads_total{scope,path,reason}`, `op_baseline_refresh_duration_seconds{scope,result}`,
`op_baseline_rows`, `op_baseline_age_seconds` (gecikme).

## 2026-10-07 — Kök-neden paneli soğuk açılış: çekirdek + bubbleUp aşamalı çizim (v0.10.1119)

**Operatör (prod):** Problem kök-neden paneli (`/api/problems/{id}/rootcause`) ilk açılışta / önbellek ıskasında
30–45 sn boş bekliyor, sıcakta hızlı. **Kök neden:** demet TEK yanıt ve yanıt en yavaş alt-okumayı bekliyordu.
Fan-out'taki okumaların hepsi MV ya da nokta okuması (deploy `service_version_5m`, korelasyon
`service_summary_5m`, topoloji `topology_edges_5m`, blast radius `service_callers_5m`, hipotez FINAL;
exemplar servis+zaman önekli top-1) — tek istisna BubbleUp (`chstore/bubbleup.go`): 1 saate (gecikme ailesinde
önceki eş-boy baseline ile 2 saate) kadar ham `spans` üzerinde ÜÇ ardışık aşama — totals (25 sn tavan) →
`arrayJoin(attr_keys)` anahtar keşfi (25 sn tavan) → 30'a dek anahtar başı `attr_keys/attr_values` taraması, 6'lı
dalgalar (15 sn tavan; v0.9.1082 ölçümü anahtar başı medyan 1,3 sn). Üstüne deploy zenginleştirmesi fan-out'tan
ÖNCE ardışık koşuyordu. İkinci sınıf: SWR arka plan tazelemesinin 20 sn'lik context'i (`cache.go` `refreshKey`)
~40 sn'lik demeti her seferinde kesiyor, bubbleUp'sız/exemplar'sız KISMİ demeti taze diye önbelleğe yazıyordu;
180 sn sonra yine sert ıska.

**Karar:** (1) Demet iki uca bölündü (`api/rootcause_progressive.go`, defter kaydı; api.go büyümedi):
`/rootcause/core` = tam demetin bubbleUp HARİÇ aynı fan-out'u, `/rootcause/bubbleup` = tam demetteki aynı
`ServiceBubbleUp` çağrısı (aynı pencere işlevi, aynı aile), anomali şeridi için `/api/anomalies/{id}/rootcause/core`.
Panel ikisini AYNI ANDA ister, çekirdeği gelir gelmez çizer; bubbleUp bölümü yerinde "yükleniyor" satırıyla bekler,
manşet bubbleUp'a bağlıysa (deploy / sıcak rollout yok) "inceleniyor" der — önce "localized" deyip fikir
değiştirmez. Şerit ve dış kanıt paneli bubbleUp çizmediği için yalnız core okur. Tam `/rootcause` API sözleşmesi
olarak aynen durur. (2) Deploy zenginleştirmesi fan-out'un içine alındı (paralel). (3) BubbleUp'ta totals ile
anahtar keşfi aynı anda koşar (keşif totals'a bağımlı değil; boş taraf / totals hatasında keşif iptal edilip
beklenir) — bir ham tarama aşaması duvardan düşer; SQL metinleri bayt bayt aynı, 6'lı tavan aynı. (4) Kök-neden
hesapları istek iptalinden koparıldı (`context.WithoutCancel` + bütçe: core 30 sn, tam 90 sn, bubbleup 85 sn):
çekmeceyi kapatan singleflight lideri paylaşılan hesabı öldürmez, SWR tazelemesi kısmi demet yazmaz. Core bütçesi
aşılırsa WARN log (`logRootCauseBudget`). (4b) **Kopuk işe tavan** (`api/rootcause_bubble_gate.go`, inceleme
düzeltmesi): kök-neden yollarındaki `ServiceBubbleUp` taramaları (bubbleup ucu, tam demet, ikisinin SWR
tazelemesi) pod başına süreç geneli `rootCauseBubbleSlots = 3` yuvayı paylaşır. Yuva ÖZGÜN context yaşarken
beklenir (≤ 8 sn); bekleyen iptal edilirse tarama hiç başlamaz, sonuç önbelleğe girmez; kopma yalnız yuva
alındıktan sonra. Bekleme dolarsa bubbleup ucu hata döner (önbelleğe yazılmaz); tam demet eski davranışla
bubbleUp'sız döner ama gövde `uncacheable()` ile işaretlenir (`cache.go`: L1/L2'ye yazılmaz, `X-Cache:
MISS-NOSTORE`, SWR bayat girdiyi bununla ezmez). Bekleme / ret / iptal sayaçları + log satırı. (5) bubbleup
ucunda HATA önbelleğe yazılmaz (sonraki açılış yeniden dener). İstemci zaman aşımı 95 sn (85 sn bütçe + 8 sn
yuva beklemesi altında kalır; varsayılan 60 sn uzun taramayı keserdi). İstek düşerse bölüm "Attribute kıyası
okunamadı" der (TR/EN, `rootCause.bubbleUnavailable`) — "ayrışma yok" ile karışmaz; manşet "Comparing…"de
takılmaz, bubbleUp'sız tam demetin manşetine döner, "ilişkili sinyal yok" boş hâli iddia edilmez. Önbellek
60 sn, anahtar tam demetin kimlik anahtarı + parça öneki (saat bileşeni yok, v0.9.1082). Eşdeğerlik testleri:
`rootcause_progressive_test.go` (core + bubbleup = tam demet alan alan, core bubbleUp koşmaz, hata önbelleğe
girmez, singleflight tek hesap, iptal edilen istemcinin hesabı önbelleğe girer), `bubbleup_parallel_test.go`
(ardışık referansa karşı sonuç + hata sırası, örtüşme, iptal + bekleme, eşzamanlılık tavanı),
`RootCausePanel.progressive.test.tsx`.

**Beklenen etki:** ilk anlamlı içerik (manşet hariç tüm bölümler: deploy, blast radius, korelasyon, exemplar,
rollout'lar, hipotez izi) soğukta MV okumalarının süresi kadar — tipik ≤ 1–3 sn (exemplar ham top-1 dahil);
bubbleUp bölümü kendi süresinde gelir, bir tam tarama aşaması kadar (≈ birkaç sn) kısalmış olarak. Şerit
genişletmesi ve dış kanıt paneli artık hiç ham bubbleUp taraması tetiklemez.

**Artık risk:** bubbleUp'ın kendisi hâlâ ham spans (anahtar başı sorgu kararı v0.9.1082'den duruyor) — yoğun
serviste bölüm onlarca saniye sürebilir; panel o sürede kullanılabilir ama manşet deploy/rollout yoksa bekler.
Hata önbelleğe girmediği için CH kalıcı zaman aşımında her açılış bir tarama dener (pod başına singleflight ile
tek, toplamda 3 yuva ile sınırlı). Yoğun anda (≥ 4 farklı problem aynı anda soğuk) dördüncü panel 8 sn sonra
"okunamadı" görür; tavan pod başına olduğundan N api pod'u ≤ 3N eşzamanlı tarama demek. Totals ∥ keşif bubbleUp başına anlık eşzamanlı CH sorgusunu 1 artırır. Açık problemde core ve bubbleup
pencereleri kendi hesap anlarından türer (`end = now`) — saniyeler mertebesinde ayrışabilir; bölüm alt başlığı
kendi penceresini gösterir. Ön-ısıtma (lider işçide açık problemler için) bilinçli olarak eklenmedi: bubbleUp'ı
her açık problem için periyodik koşturmak tık-yolundan bağımsız ham spans yükü demek.

## 2026-10-07 — OIDC: doğrulanmamış e-postaya güven seçeneği (trustUnverifiedEmail) (v0.10.1120)

**Operatör (prod, 2026-10-07): SSO girişi `[oidc] callback failed: class=email_unverified` ile düşüyor.**
Kurumsal IdP (AD/LDAP federasyonlu Keycloak) e-postayı doğrulamadan `email_verified=false` gönderiyor; v0.10.1067
sıkılaştırması (claim VARSA ve false ise giriş yok) bu kurulumda herkesi dışarıda bırakıyor. **Tercih edilen
düzeltme IdP tarafında:** Keycloak "Trust Email" — User federation → LDAP → Advanced settings → Trust Email (+ Sync
all users), dış IdP için Identity providers → Trust Email. IdP yöneticilerinden bu istenecek; Coremetry tarafına
yine de **açık-seçim** bir anahtar eklendi. **Karar:** `auth_oidc` blobuna `trustUnverifiedEmail` (varsayılan
kapalı), Settings > SSO'da TLS kutusunun altında "Doğrulanmamış e-postaya güven (önerilmez)". **Anlam:** açık VE
izinli alan adı listesi DOLUYKEN `email_verified=false` kabul edilir; alan adı kontrolü aynen uygulanır (doğrulanmamış
e-posta yalnız kurumun alan adlarıyla girebilir — e-postasını kendisi yazabilen bir dış hesap başka alan adıyla
gelemez). Claim true ya da hiç yoksa davranış değişmedi. **Neden alan adı şartı:** liste boşken anahtar, IdP'deki
herhangi bir hesabın e-postasını doğrulamadan herhangi bir Coremetry kullanıcısının adresine bağlanmasına izin
verirdi (v0.10.1067'nin kapattığı açık). Bu yüzden iki katmanlı savunma: (1) PUT 400 — "İzinli alan adları boşken
doğrulanmamış e-postaya güvenilemez" (TR/EN) — yalnız SSO açıkken; kapatma anahtarı mutlak kalır (`enabled:false`
her zaman kaydedilir, çift o hâliyle yazılabilir), ama bu çiftle SSO'yu yeniden açan kayıt 400 alır; (2) blob
elle yazılmışsa yüklemede bayrak KAPALI sayılır, SSO düşmez, `lastError` yazılır (TLS CA / N4 kalıbı) ve
`Exchange`'teki saf karar (`decideOIDCEmail`, `internal/auth/oidc_email_trust.go`) listeyi ayrıca denetler.
**Log:** yalnız bu anahtar sayesinde kabul edilen giriş `[auth] WARNING: OIDC email_verified=false accepted
(trustUnverifiedEmail) domain=<alan adı>` bırakır — tam e-posta DEĞİL; alan adı başına saatte en çok bir satır
(anahtarlar izinli listeden, kardinalite sınırlı). Anahtar açıkken her ayar yüklemesinde (PUT + blob değişimi) bir
kez WARN (TLS skip-verify emsali). **Audit:** `settings.oidc.update` eski→yeni `trustUnverifiedEmail`
(`SaveSettingsWithPrev`, aynı kilit). **UI:** izinli alan adları boşken kutu pasif + ipucu; liste silinirse gövdeye
false gider (`formToInput`); kırmızı açıklama hep görünür (karar vermeden önce okunmalı); metinler i18n
(`sso.trustEmail.*`, TR/EN). **Ön koşul ve risk (güvenlik incelemesi):** anahtar yalnız IdP kullanıcının
e-postasını kendisinin belirleyemediği/değiştiremediği kurulumda güvenlidir (self-registration yok, e-posta düzenleme
yok, sosyal/brokered IdP yok); aksi hâlde risk **e-posta çarpışmasıyla hesap ele geçirme**. Bu yüzden: (a)
`OIDCClaims.ViaTrust` (yalnız anahtar sayesinde kabul — `EmailVerified` yeniden kullanılmadı) taşıyan giriş
`oidcLoginUser`'da (`internal/api/auth_permissions.go`) `AuthProvider != "oidc"` (yerel/LDAP, boş = local) ya da
admin hesaba **bağlanmaz** → `email_unverified_privileged`, giriş sayfasında "Bu hesap doğrulanmamış e-posta ile
SSO'dan açılamaz; parola/LDAP ile girin veya IdP'de Trust Email açılsın" (TR/EN), loga yalnız kullanıcı id'si; yeni
kullanıcı varsayılan rolle açılır, mevcut oidc viewer/editor girer; (b) YALNIZ `ViaTrust` girişte ASCII dışı
karakterli e-posta `email_invalid` (EqualFold / ToLower Unicode katlaması — ör. KELVIN SIGN → "k" — alan adı ya da
hesap çarpışması üretmesin; izinli alan adları zaten ASCII/punycode). Doğrulanmış ya da claim'siz giriş birebir
eskisi gibi (Türkçe karakterli e-posta mümkün — operatör kararı). Bu iki ek davranış rol mantığını ve yetki servisini değiştirmez.
**Değişmeyen:** yetki servisi (`/api/auth/permissions`), rol mantığı, doğrulanmış girişte kullanıcı eşleme, TLS
kuralları. **Kapsam dışı:** config.yaml/Helm kaynaklı OIDC'ye alan eklenmedi (Settings'e özgü; config kaynağında eski
sıkı davranış).

## 2026-10-07 — OIDC: e-posta çözüm zinciri — UserInfo + kullanıcı adıyla AD/LDAP eşleştirme (v0.10.1121)

**Operatör (prod, 2026-10-07): SSO girişi HERKES için `[oidc] callback failed: class=email_missing` ile düşüyor.**
Kurumsal IdP (LDAP federasyonlu Keycloak) id_token'a `email` koymuyor; `preferred_username` AD sAMAccountName'i
(sicil, ör. `n0000001`) taşıyor. Aynı kullanıcılar Coremetry LDAP girişiyle sorunsuz giriyor (dizin kaydında
`sAMAccountName=N0000001`, `mail=first.last@corp.example.test`; LDAP girişi kullanıcıyı bu e-postayla açıp
`ldap_username`'i yazıyor). **Tercih edilen düzeltme IdP tarafında:** Keycloak → User federation → LDAP → Mappers:
`mail` LDAP özniteliği → `email` kullanıcı özniteliği (user-attribute-ldap-mapper); Client scopes → `email` scope'u
istemciye **Default** olarak bağlı ve `email` mapper'ında "Add to ID token" açık. **Karar (Coremetry tarafı):**
id_token'da e-posta YOKSA callback bir **çözüm zinciri** koşar, ilk isabette durur
(`internal/auth/oidc_email_resolve.go`): (1) id_token `email` — davranış birebir aynı; (2) **UserInfo** `email` —
her zaman denenir (keşifte userinfo ucu varsa), aynı sınırlı HTTP istemcisi ve TLS ayarı (`newOIDCHTTPClient`,
ClientContext), ≤5 sn; UserInfo `sub` ≠ id_token `sub` ⇒ **red** (`userinfo_sub_mismatch`, OIDC Core §5.3.2);
`email_verified` UserInfo'dan — UserInfo göndermiyorsa id_token'daki değer taşınır; kapı id_token'la aynı
(`decideOIDCEmail`: güven anahtarı + izinli alan adı); uç hatası yumuşak (sonraki adım); (3) **kullanıcı adıyla eşleştir** — yalnız yeni `usernameFallback` anahtarı açıkken
(varsayılan KAPALI): yapılandırılan claim (`usernameClaim`, varsayılan `preferred_username`; YALNIZ o claim okunur,
başka ad tahmin edilmez) imzası/issuer'ı/nonce'u doğrulanmış id_token'dan, yoksa sub'ı eşleşmiş UserInfo'dan.
(3a) LDAP yapılandırılmışsa servis hesabıyla **tek öznitelikte tam eşleşme** (`internal/ldap/oidc_lookup.go`:
`(&(objectClass=person)(sAMAccountName=…)(!(userAccountControl:1.2.840.113556.1.4.803:=2)))` ya da ayarlı benzersiz
`userAttribute` — `mail`/`userPrincipalName`/`cn`/`displayName` gibi e-posta biçimli ya da benzersiz olmayan
öznitelik anahtar olamaz, sAMAccountName'e düşülür; AD devre dışı koşulu yalnız sAMAccountName aramasında, çünkü
şemasında `userAccountControl` olmayan dizinde filtre hiçbir kaydı döndürmezdi; kaçışlı filtre, sizeLimit 2, ≤5 sn);
iki kayıt ⇒ red (`username_ambiguous`), dizin hatası ⇒ red (`directory_lookup_failed`) + kategori başına dakikada
bir `[oidc] directory lookup failed: category=bind|timeout|search|ambiguous|other` (kullanıcı adı / hata metni yok).
Dizinde kayıt YOKSA ⇒ `email_missing` — dizin açıkken `users` tablosundaki bayat bir `ldap_username` satırına
düşülmez (dizinden silinmiş hesap girmesin).
E-posta LDAP girişinin kullandığı öznitelikten (`emailAttribute`, boşsa `mail`) — aynı kullanıcı satırına düşsün diye.
Dizinden gelen e-posta **doğrulanmış** sayılır (dizin yetkili kaynak) — `ViaTrust` DEĞİL; izinli alan adı listesi
uygulanır. (3b) LDAP yoksa ya da dizin kaydında e-posta yoksa: callback MEVCUT kullanıcıyı `users.ldap_username` ile
bulur — adanmış okuma `GetActiveUsersByLdapUsername` (büyük-küçük harf duyarsız, devre dışı satırlar yok sayılır,
`LIMIT 2` + `max_execution_time`; iki satır ⇒ `username_ambiguous`, keyfî ilk satır seçilmez); kayıtlı e-postaya
izinli alan adı listesi uygulanır; e-postasız **yeni kullanıcı açılmaz**. 3a/3b'de kimlik kullanıcı adıyla kurulur
(`OIDCClaims.ViaUsername`) — güven anahtarı değil: yerel / LDAP / oidc viewer-editor hesap açılır, **admin hesap
açılmaz** (`username_admin_refused`, giriş sayfasında TR/EN metin) — yalnız ayrı açık-seçim
`usernameFallbackAllowAdmin` (varsayılan kapalı, yalnız eşleştirme açıkken seçilebilir/geçerli, audit'li, UI'da
kırmızı uyarı) bunu açar. Kullanıcı adı `@` içeremez (UPN/e-posta biçimi kullanıcı adı sayılmaz). Hiçbiri isabet etmezse sınıf
`email_missing` (değişmedi) + tek log satırı `[oidc] email resolution failed: class=… tried=id_token,userinfo,…
claims=<claim ADLARI>` — değer asla. Başarıda tek satır `[oidc] email resolved via userinfo|ldap|ldap_username user
id=<id>` (e-posta yok). **Davranış farkı:** id_token'da e-posta yokken `email_verified=false` artık `email_unverified`
değil zincire gider (e-postasız `email_verified` anlamsız); anahtar kapalı ve UserInfo e-posta vermiyorsa sonuç eskisi
gibi `email_missing`. **Güvenlik:** claim adı güvenli karakter kümesi (harf/rakam `_ - . :`, ≤64) ve kullanıcının
düzenleyebildiği profil / e-posta / protokol claim'leri (`email`, `name`, `nickname`, `aud`, `nonce`…) seçilemez;
kullanıcı adı değeri yalnız yazdırılabilir ASCII ≤256 (Unicode katlamasıyla — KELVIN SIGN → "k" — başka hesaba
çarpışmasın). **Ön koşul:** kullanıcı bu claim'i IdP'de değiştirememeli (LDAP federasyonu salt-okunur, "Edit
username" kapalı); brokered (dış/sosyal) IdP, self-registration ya da düzenlenebilir kullanıcı adı varsa bu adım
GÜVENSİZ — UI'da uyarı. **Güvenlik incelemesi (SHIP-WITH-FIXES) düzeltmeleri:** F1 tek öznitelik + `@` reddi + AD
devre dışı hesap dışlama; F2 admin bağlama varsayılan kapalı; F3 dizin açıkken kayıt yoksa bayat satıra düşmeme;
F4 adanmış LIMIT 2 / disabled-dışı okuma; F5 `email_verified` taşıma; F6 kategorili, seyreltilmiş dizin hata logu. **Ayar/audit:** `auth_oidc` blobunda `usernameFallback` + `usernameClaim` + `usernameFallbackAllowAdmin`; PUT'ta
geçersiz claim 400 (SSO kapalıyken de — blob'a çöp yazılmasın), elle yazılmış blobda SSO düşmez, eşleştirme kapanır
ve `lastError` yazılır; `settings.oidc.update` audit'i eski→yeni (`SaveSettingsWithPrev`). **UI:** Settings > SSO'da
güven kutusunun altında "E-posta yoksa kullanıcı adıyla eşleştir (AD/LDAP)" + claim kutusu (kapalıyken pasif);
açıklama duruma göre değişir — kapalıyken `email_missing` + IdP düzeltmesi, açıkken zincirin tamamı (i18n
`sso.usernameFallback.*`, TR/EN). **Yerleşim:** `api.go` büyümedi (auto-provision log satırı
`auth_oidc_email_resolve.go`'daki yardımcıya döndü); dizin `main.go`'da `oidcSvc.SetDirectory(ldapSvc)`.
**Değişmeyen:** id_token e-postalı giriş, `trustUnverifiedEmail` kuralları (ViaTrust bağlama yasağı), yetki
servisi, rol mantığı, TLS kuralları. **Kapsam dışı:** UserInfo için ayrı anahtar yok (yalnız e-posta yokken,
eskiden kesin red olan yolda çalışır); config.yaml/Helm kaynağına alan eklenmedi.

## 2026-10-08 — CoSRE kurum wiki'sinden cevaplar: Azure DevOps wiki senkronu + search_wiki / read_wiki_page (v0.10.1122)

**İstek ("karma", operatör onaylı tasarım):** CoSRE sohbeti şirketin on-prem Azure DevOps wiki'lerindeki
runbook / nasıl yapılır / mimari / sahiplik sayfalarından kaynak bağlantılı cevap versin; gerekirse Azure
DevOps Search'ü de kullansın. Genel URL tarayıcısı (v0.8.442) HTML kazıyordu, wiki API'sinin yapısını
(sayfa ağacı, Markdown, sürüm) bilmiyordu ve PAT'i kaynak başına ayrıca istiyordu.

**Karar:**
- **Bağlantı:** YENİ kimlik yok — Kod entegrasyonu'ndaki `devops_connection` (URL + koleksiyon + PAT + TLS)
  aynen kullanılır (`internal/devops/wiki.go`). api-version: kodun geri kalanının aday sırası (tespit edilen,
  6.0, 4.1); eski sunucu "preview" isterse aynı sürüm `-preview.1` ekiyle bir kez daha, çalışan sürüm
  süreçte hatırlanır. 401/403 "Wiki: Read" kapsamını adlandırır; JSON yerine HTML oturum açma sayfası hata.
  PAT hiçbir hata metnine girmez (sanitize), sayfa içeriği hiçbir log satırına yazılmaz.
- **Senkron (`internal/wiki/sync.go`):** projeler → wiki'ler (izin listeleri; boş = PAT'in gördüğü hepsi) →
  sayfa ağacı (`recursionLevel=full`, toplam tavan varsayılan 5000) → değişim tespiti → yalnız DEĞİŞEN
  sayfa okunur. Tespit iki basamaklı: wiki deposunun git öğe listesi (wiki başına TEK istek, blob objectId)
  ya da o alınamazsa sayfa başına koşullu GET (If-None-Match → 304). Silme mezar taşıyla; bir wiki ancak
  ağacı hatasız ve tavansız alındıysa budanır, kapsamdan çıkan wiki ancak listeleme hiç hata vermediyse —
  geçici bir 500 indeksi boşaltmaz. LİDER pod'da (Redis `coremetry:lock:wiki-sync`; yalnız api/worker rolü,
  ingest pod'u yarışa girmez), ≤4 eşzamanlı, ≤8 istek/sn, istek başına 20 sn, gövde tavanları (sayfa 2 MB,
  ağaç 16 MB). Geçiş liderlik süresince koşar: kilit kaybedilirse türetilmiş ctx iptal edilir ve geçiş yarıda
  durur (durum yine yazılır). Aralık varsayılan 60 dk, en az 15. "Şimdi senkronize et" (yalnız oturum açmış
  admin — admin rollü API token'ı da reddedilir; audit `wiki.sync`) istek pod'unda koşmaz: durum blobuna damga
  yazar, lider ≤15 sn içinde alır — iki pod aynı anda SENKRON GEÇİŞİ koşmaz. Senkrondan bağımsız tekil
  yazımlar ise her api pod'undan olabilir: canlı aramanın bulduğu ya da read_wiki_page'in yerelde bulamayıp
  API'den okuduğu sayfa o pod'da indekse upsert edilir. İkisi de ReplacingMergeTree(version) üzerinde aynı
  anahtara son-yazan-kazanır yazımıdır; en kötü hâl, bir sonraki senkronun hash/sürüm farkıyla düzelttiği
  kısa ömürlü bir eski sürümdür. Durum `system_settings[wiki_sync_status]`'ta (hangi pod'a düşülse aynı
  kart), ayar `system_settings[wiki_knowledge]`'da.
- **Depo:** iki state tablosu `wiki_pages` / `wiki_chunks`, `ReplacingMergeTree(version)` + FINAL, mutasyonsuz
  (`deleted=1` mezar taşı satırı; ALTER DELETE yok). Mezar taşları `TTL … + INTERVAL 30 DAY DELETE WHERE
  deleted = 1` ile düşer (canlı satırların TTL'i yok; 30 günde RMT birleşmeleri eski sürümü çoktan eritmiştir).
  saved_views'a gitmiyor — rag_chunks'ın aynı savunması (içerik + dizi kolonları). Purge'dan korunur
  (`configPreserveTables`).
- **Parçalama:** Markdown başlık sınırları (kod çiti içi `#` başlık değil), başlık YOLU parçaya bağlam;
  uzun bölüm rag.ChunkText'ten (tek bölücü); `[[_TOC_]]` makroları ve HTML yorumları soyulur.
- **Arama:** jetonlar Go'da — Türkçe-duyarlı katlama (İ/I/ı→i, ş→s, ğ→g, ü→u, ö→o, ç→c; Türkçe küçültme
  "INFO"yu bozardı) + teknik bileşikler ayrı jeton (`svc-orders`, `err-1042`, `orders.v2`; CH hasToken bunları
  ayraçtan bölerdi). CH `tokens Array(String)` + bloom_filter atlama indeksi; aday sorgusu `hasAny`, LIMIT'ten
  ÖNCE eşleşen farklı terim sayısına göre sıralı (`ORDER BY length(arrayIntersect(tokens, terimler)) DESC`,
  tavan 300 — sık bir terim tüm terimleri taşıyan parçayı tavanın dışına itemez), terim frekansı `countEqual`.
  FINAL altında atlama indeksi: CH 25.x+ varsayılanı (`use_skip_indexes_if_final=1` +
  `use_skip_indexes_if_final_exact_mode=1`) doğru sonuçla kullanır; 24.x'te varsayılan kapalı ve exact_mode yok
  — orada 1'e zorlamak mezar taşını taşımayan granülü atlayıp silinmiş sayfayı geri getirebilirdi, bu yüzden
  sorguda ayar VERİLMEZ (24.x'te küçük state tablosunun 5 sn tavanlı taraması). Skor Go'da: kapsama (eşleşen idf / toplam idf) × (0.6 + 0.4 × BM25/max) — mutlak eşik
  kapsamaya, sıralama BM25'e dayanır. Embedding (RAG ayarındaki uç) varsa parçalar embed edilir ve skor
  0.6·lexical + 0.4·kosinüs harmanlanır; YOKSA her şey lexical çalışır.
- **Canlı yedek:** Azure DevOps Search (`wikisearchresults`, POST) yerel sonuç zayıf/boş ya da indeks boş/bayat
  olduğunda; uç bir kez tespit edilir (404 / sürüm aralığı dışı → 6 saat "unavailable"), sonuç sayfa yoluna
  çevrilir, içerik API'den okunup yerel depoya yazılır. Her türlü geçici hata (401/403/5xx/zaman aşımı) 15 dk
  geri çekilme alır — kırık bir uç her soruya 8 sn eklemez. RAG kademesinde YALNIZ indeks boş/bayatken (her
  serbest sorunun önüne canlı arama gecikmesi eklenmesin); araç yolunda zayıf sonuçta da.
- **Sohbet:** (1) RAG kademesi wiki parçalarını doküman parçalarının ardına ekler (kendi tabanı 0.5; sayfa adı
  modele verilmez — v0.9.515 kuralı; cevapta sayfa çipleri "Wiki · başlık" tıklanır kaynak). Bu kademede wiki
  araması YALNIZ lexical: soru ikinci kez embed edilmez ve FINAL kosinüs tam taraması koşmaz (hibrit sıralama
  search_wiki aracında). Wiki çipleri request-ID çiplerinin ÖNÜNE eklenir, request-ID listesi kendi tavanıyla
  aynen kalır; wiki kapalıyken çip listesi bayt bayt eskisi. Prod varsayılanı
  `on_no_loop` olduğu için serbest döngü çoğu kurulumda koşmaz — wiki'nin varsayılan yolu bu kademe.
  (2) Serbest döngüye `search_wiki(query, project?, limit≤10)` ve `read_wiki_page(project, wiki, path, offset?)`
  — read_source_code'un aynası: SOHBET-YALNIZ (dış MCP'de kayıtlı değil), KOŞULLU (wiki açık + DevOps bağlı),
  YALNIZ oturum kullanıcısına (cmk_ token'ı rolden bağımsız dışarıda; RAG kademesindeki wiki yarısı da aynı
  kapıda), viewer tabanı. Sayfa 40 KB'ta kesilir ve ~4800 karakterlik pencerelerle (offset) okunur — sohbetin
  tek sonuç bütçesi 6000 rune. Sonuçtaki sayfa url'leri cevap çiplerine dönüşür; prompt eki modele url'yi
  "Kaynak:" diye yazdırır.

**Exfil kapısı (read_source_code'dan fark):** wiki araçları bağımsız sohbette de sunulur (özelliğin amacı bu);
read_source_code yalnız panel takibinde. AMA bağımsız döngüde dış MCP araçları olabilir ve onay adımı yok —
ekilmiş bir log satırı modeli wiki metnini bir dış aracın argümanına koymaya yönlendirebilirdi. Bu yüzden dış
MCP kataloğu wiki kararından ÖNCE kurulur ve turda TEK bir dış araç bile varsa `search_wiki` / `read_wiki_page`
o turda SUNULMAZ (`wikiToolsFor(tools, c, hasExternal)`; kaynak pini `TestWikiGateWiredIntoChat`). O kurulumda
wiki bilgisi yalnız RAG kademesinden gelir — araçsız tek anlatım çağrısı, dış araç yok. Dış MCP
yapılandırılmamış kurulumda araçlar sunulur. Tur arası taşımayı da kapatmak için kapı katalog o tur boş düşse
bile dış MCP YAPILANDIRILMIŞSA kapalıdır; istemci iptali (sekme kapandı) canlı aramayı 15 dk kapatmaz.

**Değişmeyenler:** genel URL tarayıcısı ve rag_chunks, RAG kademesinin sırası (guided > drawer > RAG > niyet >
döngü), embedding'siz kurulumun davranışı, dış MCP yüzeyi (kayıt defteri 62 → 64, tools/list aynı), api.go.
**Erişim:** oturum açmış her Coremetry kullanıcısı (viewer dahil) kapsamdaki TÜM wiki'leri sohbet üzerinden
okuyabilir — Azure DevOps'taki sayfa izinleri kullanıcı başına uygulanmaz (okuma PAT'in kimliğiyle). Bu yüzden
operatör dokümanı izin listesi tanımlamayı öneriyor. API token'ları (cmk_) ve dış MCP istemcileri wiki
içeriğini göremez.
**Sınırlar:** TFVC tabanlı eski wiki desteklenmez (Git wiki'leri); sayfa ekleri (resim/PDF) indekslenmez;
Türkçe ek çekimi (sipariş/siparişler) kök indirgenmez — embedding yoksa tam sözcük gerekir; `wikisearchresults`
yanıt şekli yalnız belgelere göre yazıldı, operatörün sunucusunda doğrulanmalı.
Operatör dokümanı: [docs/WIKI-KNOWLEDGE.md](WIKI-KNOWLEDGE.md).

## 2026-10-08 — OIDC girişi sonrası derin bağlantıya sunucu tarafı dönüş (`?next=`) (v0.10.1123)

**Sorun:** OIDC callback başarıda her zaman `/`'e yönlendiriyordu; derin bağlantı (v0.8.367) yalnız
`sessionStorage`'da tutulup `/`'te tüketiliyordu. sessionStorage sekme kapsamlı: IdP akışı başka sekmede/pencerede
bitince (MFA uygulaması atlaması, tarayıcı geri yüklemesi) kayıt kayboluyor, ayrıca `/` görünür biçimde yanıp
sönüyordu.

**Karar:** dönüş yolu sunucuda taşınır. Login SSO düğmesi kayıtlı derin bağlantıyı TÜKETMEDEN okur
(`peekPostLoginRedirect`) ve `/api/auth/oidc/start?next=<encode>` çağırır (yoksa `next` yok). `oidcStart` değeri
süzer ve diğer OIDC çerezleriyle aynı nitelik/TTL'de (HttpOnly, Lax, Path `/api/auth/oidc/`, 10 dk) HttpOnly
`coremetry_oidc_next` çerezine base64url olarak yazar (net/http çerez değerinden `;` `"` boşluk düşürür); `next`
yok/geçersizse yarım kalmış önceki bir girişten kalan çerez silinir (bayat hedefe dönülmez). `oidcCallback` başarıda çerezdeki yolu YENİDEN süzüp oraya, yoksa `/`'e gider; uçuştaki
dört çerez callback'in en başında silinir, yani başarı ve tüm hata çıkışlarında. Yardımcılar `internal/api/oidc_next.go`
— api.go büyümedi (satırlar yerinde değişti).

**Açık yönlendirme sınırı:** `sanitizeOIDCNext` (Go) ile `sanitizeRedirect` (FE) aynı kuralları uygular: tek `/` ile
başlar (`//` değil), ters bölü yok, şema/host yok, kontrol karakteri (CR/LF dahil) yok, `.`/`..` segmenti yok (ham ya
da `%2e` — `/x/../api/foo` blok listesini atlamasın); yol kısmının yüzde-çözülmüş hâli de aynı denetimden geçer
(`/%2F%2Fevil`, `/%61pi/x`) ve `/login`, `/public`, `/api` altında olamaz. Sorgu dizgesi çözülmez (meşru `%5C` taşıyan
süzgeç bağlantıları bozulmasın). 2048 sınırı sunucuda ve FE'de yalnız `oidcStartHref`'te: sessionStorage yedeği her
uzunluğu tutar, daha uzun bağlantı `next`'siz gider ve `/` yedeğiyle döner. İki tarafta tablo testleri.

**Yan düzeltme:** callback eskiden çerezleri `Path: "/"` ile siliyordu; çerezler `/api/auth/oidc/` yolunda
set edildiği için tarayıcı onları silmiyordu (yalnız 10 dk TTL kurtarıyordu). Silme artık aynı yolla.

**FE yedeği:** sessionStorage kaydı kalır; kararı yalnız İLK kimlikli render verir (`firstAuthedRenderAction`, saf):
kayıt mevcut konumla (path + query + hash) eşitse silinir (sunucu zaten oraya indirdi); `/`'teysek geri yüklenir
(çerez süresi doldu, uzun bağlantı, eski sekme); başka bir sayfadaysak silinir — sonraki bir `/` ziyareti bayat
bağlantıya atlamaz. Çıkışta (user null) karar yeniden kurulur. **Bırakılan:** yerel (parola) girişi zaten SPA içi, değişmedi; LDAP aynı.

## 2026-10-08 — Wiki bilgisi: senkronsuz canlı arama, açık wiki kademesi, mod ayarı ve tanı (v0.10.1124)

**Operatör bildirimleri (prod, on-prem Azure DevOps Server):** "Wiki içeriğini search etmiyor eğer senkron
değilse" (durum kartı "Azure DevOps Search: henüz denenmedi"), "CoSRE wiki içeriğini LLM ile yorumlayamıyor",
"wiki içeriğini sayfada göremiyorum", "senkron şart mı, senkronsuz arayarak cevap versin".

**Kök nedenler (kanıtlı):**
1. *Canlı sorgu soru cümlesiydi* — `wiki/search.go liveSearch` `api.SearchWiki(lctx, query, …)` ile operatörün ham
   sorusunu gönderiyordu. ADO Search çok terimli sorguyu varsayılan **AND** ile birleştirir; "nasıl", "ederim",
   "nedir" hiçbir sayfada geçmediği için tipik Türkçe soru SIFIR sonuç döndü. Düzeltme: `LiveSearchQueries` —
   stopword'süz özgün yazımlı sözcükler, önce AND, boşsa ` OR `.
2. *Canlı isabet tam-jeton lexical skorla puanlanıyordu* — sentetik korpus istatistiğiyle `RankLexical`; ADO'nun
   kök/ek-duyarlı bulduğu sayfa ("servisini" ↔ "servis") kapsamı düşük kalıp `ragWikiFloor` (0.5) altında düştü,
   hiç eşleşmeyen parça tamamen elendi. Düzeltme: `scoreLivePage` — ADO sırasından taban skor (0.66 × 0.88^sıra;
   OR'da × (0.8 + 0.2·kök-kapsamı)), yalnız KANITLI parçaya (terim kökü geçiyor ya da ADO vurgusu var); kanıtsız
   sayfa yükseltilmez. Yerel skor tam-jetonlu kalır. Stopword listesi soru ekleri/fiilleri ve "wiki'de anlatılıyor"
   meta sözcükleriyle genişledi (teknik jeton listeye girmez).
3. *Durum kartı pod-yerel bellekti* — `searchState` bellekte, senkron lideri aramayı hiç denemediği için blobu
   hep "unknown" yazıyordu; `Status()` blob doluyken yerel durumu hiç okumuyordu. Düzeltme: son canlı aramanın
   içerik-siz özeti ayrı `wiki_search_status` blobunda (durum değişince ya da 10 dk'da bir yazılır), kart onu okur.
   Ayrı blob, çünkü senkron blobunu lider bütün olarak yazar (yarış).
4. *Sürümle ilgisiz 400 = 6 saat "unavailable"* — her 400 sonraki sürüme geçiyordu, hepsi 400 → kapalı. Düzeltme:
   yalnız gövdesi sürüm reddi olan 400 (`api-version`, `out of range`, `preview`, `VssVersion…`) sürüm atlatır;
   diğer 400 `bad_request` — sorguya özgü, GENEL geri çekilme kurmaz (inceleme F3); AND reddedildiyse bir kez OR. Sürüm sırası 4.1-preview.1'e dek genişledi, önizleme eki
   isteyen sürüm `-preview.1` ile tekrar denenir, çalışan sürüm hatırlanır; gövde `includeFacets:false` + `$skip`;
   yanıtta `fileName` yedeği ve içerik vurgusu önceliği. Uç on-prem'de koleksiyon adresi (almsearch yok) — testle pinli.
5. *Wiki içeriği sohbette modele çoğu zaman ulaşmıyordu* — kademe sırası guided > drawer > RAG > niyet > döngü:
   servis adı taşıyan "X runbook'u nedir" guided'a düşüyor; RAG wiki yarısı LiveOnStale + 0.5 tabanla çoğu soruyu
   eliyor; prod niyet modu `on_no_loop` serbest döngüyü (search_wiki/read_wiki_page) hiç koşturmuyor; dış MCP
   yapılandırılmışsa araçlar zaten düşüyor.

**Kararlar:**
- **Açık wiki kademesi** (`api/chat_wiki_tier.go`) guided'dan ÖNCE, yalnız bağlamsız pencerede (Explain/Subject/
  Trace/Page.TraceID/Service doluysa HİÇ girmez — inceleme F1) ve soru wiki'yi işaret ederse. Güçlü işaret yalnız açık
  sözcük (wiki/runbook/playbook/prosedür/procedure/kılavuz/howto/dokümantasyon); zayıf işaret ("how to", "how do
  I/we", "nasıl yapılır/yaparım…", doküman, docs) telemetri sorularında da geçtiği için yalnız en iyi isabet
  ≥ ragWikiFloor (0.5) ise cevaplar, değilse kademe SESSİZ düşer (inceleme F2). Wiki metni `<wiki_data>` çitinde
  (içerideki etiketler silinir), RAG kademesinde de.
  Canlı yedek `LiveOnWeak`, taban 0.3, baskın sayfa (ikinciyi 1.35× geçen) varsa ~6000 karakter tam metin, değilse
  ≤4×1500 parça; araçsız TEK anlatım (`SystemPromptWikiChat`: özetle/yorumla, link listeleme). Bulunamazsa güçlü
  işarette açık "Wikide bulunamadı" (telemetriye kaçmaz). Canlı arama hatasında sohbet genel not görür, ayrıntı
  yalnız tanıda. İşaretsiz
  soru bayt bayt eski yolda. Yalnız oturum kullanıcısı. RAG kademesinin wiki yarısı da aynı okuma derinliğini kullanır.
- **Mod ayarı** `wiki_knowledge.mode`: `hybrid` (varsayılan, eski davranış) | `live` | `sync`. `live`: senkron
  döngüsü koşmaz (`SyncDue` false, manuel senkron 409), yerel indeks okunmaz, en iyi ≤3 sayfa eşzamanlı okunur,
  süreç-içi LRU (10 dk, 64 sayfa, 16 MB; yol normalize anahtar) — **içerik yazılmaz** (yalnız durum blobları);
  geçiş sürerken mod canlıya alınırsa senkron sayfa döngüsünde durur ve budamaz; eski indeks temizlenene dek kalır,
  `POST /api/wiki/purge` ("İndeksi temizle": oturumlu admin, onaylı, audit `wiki.purge`, iki tabloda `ALTER … DELETE`
  — nadir yönetici eylemi, senkron yolu mutasyonsuz kalır) siler (inceleme F4); işaretsiz serbest soru (LiveOnStale) canlı
  modda wiki'ye gitmez (her soruya 1–3 sn eklenmesin). `sync`: canlı arama yok. Eski `disableLiveSearch` mod
  boşken `sync` sayılır (kayıtlı bloblar için okuma; UI artık modu yazar). Canlı modda Search yoksa kart, kayıt
  uyarısı (`modeWarning`), tanı ve sohbet "Azure DevOps Search bu sunucuda yok; senkron modunu kullanın" der.
- **Tanı uçları** (`api/wiki_diag.go`, registerRoutesExtra): `POST /api/wiki/test-search` (oturumlu admin, audit
  `wiki.test_search` — sorgu metni değil uzunluk + sayılar; geri çekilmeyi atlar; ≤160 rune kesit, içerik yok) ve
  `GET /api/wiki/pages` (oturum kullanıcısı; ilk 1000 karakter önizleme yalnız admin rolüne SQL'de seçilir;
  FINAL + deleted=0 + LIMIT/OFFSET ≤100 + max_execution_time; URL yalnız http(s)).

**Değişmeyenler:** wiki araçları dış MCP aracı olan turda sunulmaz; API token'ları wiki'ye erişemez; istek tavanları
(8 sn canlı bütçe, 20 sn istek, gövde tavanları), 15 dk geri çekilme ve 6 sa "unavailable" yeniden denemesi; içerik
ve PAT hiçbir log/audit satırına girmez.

**Bilinen sınırlar / şüpheler:** sahte ADO sunucusu gerçek on-prem sürümleriyle canlı doğrulanmadı (sürüm-reddi
gövde anahtar sözcükleri dokümana + kod aramasının v0.10.98 deneyimine dayanıyor). Kök eşleşmesi kaba (5 harf
öneki) — yalnız canlı isabetin KANIT kontrolü. Zayıf işaretli telemetri sorusu ("how do I see errors for
svc-orders"), wiki'de o servisin runbook'u varsa OR sorgusuyla tabanı geçip wiki'den cevaplanabilir.

## 2026-10-08 — /cosre: bağımsız, kromsuz CoSRE sohbet sayfası (v0.10.1125)

**İstek (operatör):** CoSRE sohbeti `<host>/cosre` adresinde tek başına açılabilsin — sidebar / uygulama kabuğu
olmadan yalnız sohbet; yer imine eklenip ayrı pencerede tutulabilsin.

**Karar:** Yeni sohbet bileşeni YOK. `CopilotChat` `variant="page"` aldı: başlık (CoSRE + model çipi + profil seçici +
Geçmiş + Temizle) ve gövde (geçmiş bölümü, ✨ Explain kipi, mesajlar, follow-up çipleri, composer) tek yerde
`headerNode` / `bodyNode` olarak kurulur; çekmece `<Drawer>` ile, sayfa `.cosre-page` ile sarar — çatal yok, iki yüzey
aynı state'i ve aynı eylemleri taşır. Sayfa kipinde pencere hep "açık" sayılır (geçmiş listesi, karşılama, `?chat=`
URL senkronu açık çekmece gibi çalışır), FAB / nudge / kritik-problem poll'u yok, Genişlet düğmesi yok; yerine
"Coremetry'yi aç" (`/`) linki ve `ThemeToggle`. Sekme başlığı "CoSRE" (markalama başlığından sonra yeniden yazılır,
çıkışta marka başlığı döner); favicon aynı `/favicon.svg`.

**Kabuk:** `lib/cosrePage.ts` `isCosrePage` → AppShell'de ayrı kromsuz dal (`.cosre-bare`): Sidebar, duyuru, ⌘K,
kısayollar, FAB'lı CopilotChat, Toaster mount edilmez, `/api/events` aboneliği kapalı (pencere başına bir akış bütçesi;
sohbet kendi istek akışını kullanır). Kiosk-çıplak daldan (`/trace?kiosk=1`) bilinçli AYRI: kiosk 401'de satır-içi
"oturum bitti" kartı çizer, /cosre ise normal korumalı rota gibi `/login`'e düşer. PUBLIC_PATHS'e eklenmedi — giriş
ister; derin bağlantı dönüşü (`postLoginRedirect` + v0.10.1123 OIDC `?next=`) /cosre'yi kabul eder (iki süzgeç de
test edildi). RBAC: çekmece custom-rol dahil her kimlikli sayfada mount olduğu için (sayfa ızgarasında girişi yok, API
`requireCopilot`) `/cosre` `ALWAYS_ALLOWED`'a girdi — kısıtlı rol ilk izinli sayfaya ışınlanmaz. Copilot kapalıysa
çekmece hiç çizilmiyordu; sayfada bu boş ekran olurdu, o yüzden yükleniyor / "CoSRE bu kurulumda kapalı" + dönüş linki.

**Linkler:** /cosre'de uygulama kabuğu yok; cevaptaki iç linkler aynı sekmede gezseydi operatör sohbetten koparırdı.
`components/ai/chatLinkTarget.ts` bağlamı (yalnız sayfa kipi sağlar) → ChatBubble iç link çipleri, EvidenceCard,
ChatTraceList ve mdLite trace-id linkleri `target=_blank rel=noopener` (href aynı-köken yol kalır; mdLite dizesi ve
"innerHTML tek yer" pini değişmedi — nitelik commit sonrası MdInline'ın kendi `<a data-nav>`larına yazılır). Dış linkler
zaten yeni sekmedeydi. Sunucunun `open` önerisi (cevap sonrası otomatik gezinme) sayfa kipinde yalnız aynı sayfa
hedefinde uygulanır; başka sayfaya otomatik gidilmez (hedef zaten link çipinde). Çekmece davranışı değişmedi.

**Sunucu:** değişiklik yok — `spaHandler` uzantısız her yolu index.html'e düşürüyor, yol listesi yok.
`cosre_spa_test.go` /cosre (sorgulu, sonda `/`) yedeğini ve OIDC `?next=/cosre` süzgecini çiviler. `api.go` büyümedi.
`pageContext` `/cosre` → `cosre` (bağlamsız, sabitlenemez: filo geneli sorular).

## 2026-10-08 — CoSRE: nasıl-yapılır soruları wiki'ye, netleştirme yerine wiki kurtarması (v0.10.1126)

**Sorun (operatör, prod):** "BSA cache refresh nasıl girilir", "Config cache refresh nasıl girilir?" ve "Coremetry
adresleri" wiki'ye hiç uğramadı. v0.10.1124'ün zayıf işareti yalnız "nasıl" + yap- fiilini tanıyordu; guided "nasıl"ı
sağlık sinyali (`hasHealthSignal`) saydı, "BSA"yı servis öneki olarak `serviceCandidates`'a verdi ve router
`guidedAskService` döndü (`copilot_guided.go` ~1161) → `guidedAskServiceEvidence` `askServiceAnswerTR` ile
DirectAnswer kurdu → `runGuidedRoute` "Hangi servisi kastettin? Adaylar: …" + sağlık çipleriyle bitirdi. "Coremetry
adresleri" aynı sınıf (find_entity / entity scan aday çipleri). Ayrıca "Aramayı test et" başarılı canlı arama
gösterirken durum kartı "henüz denenmedi"de kaldı.

**Karar:** (1) Zayıf işaret genişler (`chat_wiki_howto.go`, katlanmış metin): "nasıl"dan sonraki ≤3 sözcükte
EDİLGEN/kişisiz fiil (ünsüz gövde + -ıl/-il/-ul/-ül + -ır/-ir/-ur/-ür: girilir, açılır, yapılır; ünlü gövde + -n +
-ır/-ir: eklenir, tanımlanır, yenilenir) ya da 1. kişi yeterlik/gereklilik (-abilirim, -malı/-meli, yaparım);
"ne yapmalı", "adımları", "adres(ler)i", "kim sorumlu", "sahibi kim"; İngilizce "how do/can/to/should", "steps to",
"who owns". Telemetri ele geçirilmesin (inceleme): çıplak "nasıl" işaret değil; geçişsiz durum fiilleri (gelir,
yükselir, düşer, düzelir, görünür, artar, azalır, değişir, çalışır) dışarıda; "nerede/nereden/erişim/where is"
işaret değil ("hata nerede", "svc-orders erişim hatası"); soru telemetri sinyali (`hasGuidedSignal`) ya da sahiplik
kalıbı (find_entity kartı) taşıyor VE servis adı çözülüyorsa zayıf işaret hiç uygulanmaz (`wikiWeakCueVetoed`, ad
listesi yalnız sinyal varken okunur). İşaret ZAYIF kalır: kademe yalnız en iyi isabet KANITLA ≥ `ragWikiFloor`
(0.5) ise cevaplar — yerel skor ≥ 0.5 ya da canlı isabetin kök kapsamı (`stemCoverage`, yeni `wiki.Hit.Coverage`)
≥ 0.5 (`Hit.EvidencedAt`); AND sorgusunun ilk sonucu kapsamdan bağımsız yüksek sıra skoru aldığından sıra tek
başına kanıt değil. Güçlü işaret aynen. (2) Netleştirme kurtarması
(`chat_disambig_rescue.go`): netleştirmeyi üreten kod `markDisambiguation(ctx)` der (ask_service DirectAnswer —
AskIntent kök-neden/sağlık/pencere-kıyası DEĞİLSE, find_entity aday cevabı, namespace/entity-scan aday tabloları) —
dize eşleştirme yok. Soru telemetri sinyali (sağlık/hata/yavaşlık/neden/kıyas/problem/mutlak pencere) taşıyorsa
kurtarma hiç kurulmaz; isabet aynı kanıt kuralıyla geçer. `armDisambigRescue`'nun sardığı
emit işaretli "answer"ı yakalar; wiki LiveOnWeak + `wiki.LiveBudget` ile aranır, iyi isabet → kademeyle AYNI anlatım
(`wikiNarratedAnswer`) + altında "**Telemetri için:**" ve özgün netleştirme metni/çipleri/linkleri. İsabet yok →
özgün olay değişmeden. Kademe bir soruyu wiki'den cevaplarken router'ın aynı soruya netleştirme rotası verip
vermediğine bakar (`guidedDisambigProbe` — router'ın kendisi, rota çalıştırılmaz) ve aynı bloğu ekler; BSA sorusu
böylece hem wiki cevabını hem aday çiplerini taşır. (3) Arama durumu: test yanıtı tazelenmiş paylaşılan durumu
(`status`) taşır, kart onu çizer (durum yalnız sayfa açılışında okunuyordu); `recordSearch` force'ta (yönetici testi)
kısma kuralını atlar — imza yalnız bu pod'un son yazdığıyla kıyaslandığından araya başka pod'un "error"ı girince aynı
imzalı başarı 10 dk blobu güncellemiyordu.

**Gecikme:** canlı arama yalnız (1) ya da (2)'ye takılan soruda, alışveriş başına en çok bir kez; soru wiki işareti
taşıyorsa kademe zaten aradığından kurtarma aramaz. Geri çekilme/unavailable önbelleği aynen (`liveSearch`).
**Kapı değişmedi:** API token'ı, panel/çekmece/trace/servis bağlamı ya da wiki kapalı → ctx ve emit aynen döner,
olaylar bayt bayt aynı (testte pinli). **Bırakılan:** kademe-öncesi sonda yalnız router düzeyindeki netleştirmeyi
görür (ask_service / find_entity adayları); katalog indeksi taraması (entity scan) gerektiren netleştirme o sorularda
eklenmez — kurtarma yolu ise hepsini kapsar. Niyet sınıflandırıcısının ask_service'i de aynı sarmaldan geçer.

## 2026-10-08 — Wiki araması: Türkçe ek duyarlı eşleşme + saklı içerikten yeniden jetonlama (v0.10.1127)

**Bağlam (operatör, prod, ~12k sayfa):** "sbox sunucuları neler" ilgisiz sayfalar getirdi, anlatım "bulunamadı"
dedi; "Sbox Sunucu Listesi" doğru sayfayı buldu. Kök neden: jetonlar katlanmış TAM sözcük; "sunucuları"
(`sunuculari`) hiçbir zaman "sunucu" ile eşleşmiyordu, "neler" de terim sayılıp kapsama paydasını şişiriyordu.

**Karar:** sözlüksüz hafif Türkçe kök bulucu (`internal/wiki/stem.go`, SAF): katlanmış a-z jetondan çekim ekleri
en uzundan başlayarak tekrar tekrar soyulur, her ekin ses koşulu (ünlü / ünsüz / sert ünsüz sonrası) ve en kısa kök
(4; çoğulda 3) denetlenir. Tek ünlü ekler (-ı/-a) ve soru eki -mı soyulmaz ("sunucu"/"kafka"/"sistemi" kökünü
kemirirdi); yerine eşleşme biçimi olarak "çıplak kök" (son ünlü düşmüş) üretilir. "-sı" belirsizliği ("servisi" =
servis+i mi, servi+si mi?) iki yorumla da biçim olur. Yapım eki -lık/-lik korunur. Teknik terim (rakam/ayraç), ≤4
harf, özgün metinde TAMAMI BÜYÜK HARF sözcük ve bileşik tanımlayıcı parçası köklenmez; kesme işaretli ek (`IP'ler`)
ayrı jeton değil.

İndeks her konum için yüzey + kök biçimlerini (en çok 4, konum başına tekrarsız → terim frekansı korunur) aynı
`tokens` / `head_tokens` dizisine yazar — yeni kolon / şema YOK, bloom indeksi aynen. Sorgu terimi aynı biçimlere
genişler (`ExpandTerms`, tavan 36 jeton → CH sorgu şekli sınırlı). Skor özgün terim başına: biçimlerden biri geçiyorsa
eşleşmiş, tf = biçimler arası en büyük, idf = biçimler arası en büyük df'ten; yalnız kökle eşleşme ×0.85 (tam yazım
hafifçe önde). Liste/soru kalıpları stopword ("neler", "hangileri", "listele", "adları"); kökü stopword olan çekimli
biçim de ("bilgileri"). "listesi" BİLİNÇLİ içerik sözcüğü. Canlı ADO OR sorgusu ≥5 harfli kökleri seçenek olarak
taşır (AND sorgusu aynen; kısa kök "yeni" gibi aşırı genişletmez).

**Aday sırası (inceleme F3):** CH aday sorgusu LIMIT 300'den önce "eşleşen FARKLI jeton sayısı"yla sıralıyordu; kök
biçimleriyle yaygın kök ("sunucusu" → 3-4 biçim) nadir tanımlayıcıyı (WSBXAKFP01, 1 biçim) tavanın dışına itiyordu.
Artık özgün terim başına idf ağırlıklı kapsama: `Σ w_i·hasAny(tokens, biçimler_i) + 0.1·w_i·has(tokens, yüzey_i)`
(w_i WikiTermStats'tan, bağlı parametre; ≤12 grup). Saf ikizi `wiki.CandidatePriority` — bellek-içi test deposu aynı
sırayı uygular; ifade `clickhouse local` ile doğrulandı.

**Yeniden jetonlama:** `tokenizerVersion` (şimdi 2) `wiki_sync_status` blobunda; farklıysa senkron, ADO'ya gitmeden
`wiki_pages.content`'ten parçaları yeniden kurar (geçiş başına 5000 sayfa, imleç blobda, 4 işçi, iptal/mod değişimi
durdurur; iş sürerken SyncDue en az 2 dk arayla art arda geçiş ister). Yazılamayan sayfa atlanmaz (inceleme F1):
yeniden deneme listesi (≤200 girdi, sayfa başına ≤3 deneme) blobda; sürüm ancak tarama bitmiş ve liste boş ya da
kalanlar tükenmişken kaydedilir, tükenenler durum hatası. Embedding yeniden hesaplanmaz: `WikiPageChunks` saklı
vektörü okur, metni + başlığı aynı parçaya taşır; parça sayısı aynıysa `wiki_pages` satırı yazılmaz
(`UpsertWikiChunks`). Canlı mod zaten yeni jetonlayıcıyla parçalar.

**Yuvarlanan dağıtım (inceleme F2):** sürüm geçişinde eski pod'lar sorguyu yalnız yüzey biçimiyle, yeni pod'lar
genişletilmiş biçimlerle sorar. Yeni jetonlu indekste eski pod sorusu eskisi kadar bulur (yüzey jetonları duruyor);
eski jetonlu indekste yeni pod sorusu yalnız yüzey eşleşmesiyle bulur (kök kazancı yeniden jetonlama bitince gelir).
Risk: yeniden jetonlama bittikten SONRA lider hâlâ eski sürümde koşan bir pod'a geçerse o pod'un yazdığı (değişen)
sayfalar kök biçimsiz kalır — sürüm damgası zaten 2 olduğu için yeniden jetonlanmaz; o sayfa bir sonraki içerik
değişiminde düzelir. Dağıtım hızlı tamamlanırsa pencere küçük; gerekirse "İndeksi temizle" + senkron tam kurar.

**Yan düzeltme — kaynak çipleri:** RAG/wiki cevabı kaynakları PARÇA başına listeliyor, çip yalnız "Kaynak §N"
gösteriyordu → bir cevapta birden çok özdeş "Kaynak §1". Artık hedef (bağlantı, yoksa doküman) başına tek çip,
"Kaynak 1/2/…" etiketi, birleşen bölümler `sections`'ta (ipucu "§1, §3") — sunucu `dedupeChatSources` + FE
`sourceChips` (arşivden gelen etiketsiz liste de). Modelin bağlam blok numarası `[n]` artık parça sırası değil,
aynı anahtarla verilen çip numarası (inceleme F6: `sourceNumbers`, RAG doküman + wiki blokları): model "[2]" derse
operatör "Kaynak 2"yi görür.

**Bedeller / bırakılan:** indeks jeton dizisi büyür (biçim başına ek jeton); sözlüksüz kök bulucu bazı İngilizce
sözcükleri de kırpar ("handler" → "hand") — iki taraf aynı biçimi ürettiği ve tam yazım önde olduğu için zararı
kapsamla sınırlı. TAMAMI BÜYÜK HARF Türkçe başlıklar ("SUNUCU LİSTESİ") köklenmez (tanımlayıcı koruması bedeli).

## 2026-10-08 — CoSRE karşılaması: "neler yapabilirim" ipucu + wiki çipi (v0.10.1128)

**Sorun (operatör):** boş CoSRE sohbeti (çekmece ve /cosre) "Merhaba / P1 durumu / Sana nasıl yardımcı
olabilirim?" + hazır soru çiplerinden ibaretti; kullanıcı asistanın wiki'de arayabildiğini, bir servisin ya da hata
veren bir operasyonun adını yazınca incelediğini ve ilgili sayfaya yönlendirdiğini bilmiyordu.

**Karar:** "Sana nasıl yardımcı olabilirim?"in altında 3–4 kompakt satır (`ai/CosreCapabilities.tsx`, lucide
ikonlu, tema token'ları, dar ekranda satır kırılır): wiki (yalnız açıksa) · servis trace/hata/gecikme (@ ile
tamamla) · hata veren teknik operasyon · nereye bakmalı/yönlendirme. Her satır composer'ı ÖRNEK soruyla DOLDURUR,
GÖNDERMEZ (v0.10.702 `prefillEndpoint` deseni `prefill(text, caret)`e genelleşti; örnekte `|` imleç yeri — ad
operatörden). Wiki açıkken hazır çiplere "📚 Wikide ara: …" eklenir ("wikide " doldurur + odaklar). Yalnız boş
sohbette; kapatma düğmesi yok (ilk soruyla zaten kaybolur). Metinler i18n (EN/TR, `cosre.cap.*`).

**Wiki sinyali:** yeni istek YOK — sohbetin zaten çektiği `/api/copilot/config` yanıtına `wiki: boolean` eklendi
(omitempty; false iken şekil bayt bayt eski). Kapı sohbetin wiki kademesiyle aynı üçlü (`chatWikiAvailable`):
copilot aktif + wiki bilgisi açık/DevOps bağlı + oturum kullanıcısı (API token'ı değil). Proje/wiki adı, mod, adres
admin olmayana ASLA — şekil testi pinler. api.go büyümesin diye `copilotConfig` handler'ı + dar yanıt tipleri
`internal/api/copilot_config.go`ye taşındı (api.go 11576 → 11521, taban indirildi).

**Bırakılan:** sunucu tarafı başlangıç çipleri (`copilot_starters.go`) dokunulmadı — ipucu istemci tarafında,
LLM'siz. Sohbetin geri kalan TR sabit metinleri i18n'e taşınmadı (kapsam dışı).

## 2026-10-09 — Wiki senkronu: içeriksiz klasör ve çok büyük sayfa hata değil "atlandı" (v0.10.1129)

**Bildirim (operatör):** durum kartı "12044 sayfa … — 3 hata" diye kırmızıydı. İki hata kod wiki'sindeki (git
klasöründen yayımlanan) `.md`'siz klasörlerdi: ağaçta görünüyorlar ama sayfa GET'i 404 ve ham ADO JSON gövdesi
hata metnine giriyordu. Üçüncüsü 2 MiB okuma tavanını aşan bir sayfaydı: `LimitReader` gövdeyi kesiyor, kesik JSON
"beklenmeyen yanıt (sayfa değil)" oluyordu. **Karar:** ikisi de HATA değil, ayrı sayılan "atlandı".
(1) Ağaçtaki her düğüm istenir; 404 → `devops.ErrWikiPageNotFound` (ham gövdesiz). `gitItemPath`'e bakıp
(".md" yok → klasör) ÖN-ATLAMA bilerek YOK: alanın biçimi sunucu sürümüne / wiki türüne göre değişebilir ve
yanlış sezgi tüm sayfaları atlayıp indeksi budardı; bedel klasör başına tek istek. Senkron 404'ü `skippedEmpty`
sayar; sayfa GÖRÜLMEDİ sayılır, önceden indekslendiyse normal budanır — 404 "kaynakta şu an yok" demektir.
**Korkuluk:** bir wiki'nin ağacı ≥10 düğüm ve GET'lerin yarısından fazlası 404 ise o wiki o geçişte BUDANMAZ;
tek hata yazılır ("çok sayıda 404 — budama atlandı"). (2) `wikiDo` artık `limit+1` okur; aşan gövde `truncated` işaretlenir ve
`GetWikiPage` `devops.ErrWikiPageTooLarge` döner. Tavan 2 → 4 MiB (≤4 işçi → ≤16 MiB anlık gövde, bellek
sınırlı). Senkron bunu `skippedLarge` sayar ve sayfayı GÖRÜLDÜ sayar: önceden indekslenmiş içerik korunur, mezar
taşı yok. (3) Durum blobu `skippedEmpty` / `skippedLarge` + tavanlı (≤20) `skipped[] {page, reason}` taşır;
`errors` ve `lastOk` bunlardan etkilenmez. Kart: hatalar kırmızı kalır; atlananlar nötr gri, açılır satır
"Atlanan: 2 içeriksiz klasör, 1 çok büyük sayfa" (≤5 ad + "… N sayfa daha"). (4) `read_wiki_page` / `ReadPage`:
404 → "bulunamadı" yolu (nil, nil — mevcut ipucu); çok büyük → `wiki.ErrPageTooLarge` ve MCP'de `tooLarge:true` +
anlaşılır not (ham JSON yok). Canlı aramada okunamayan sayfa zaten sessizce düşer. **Yerleşim:** `api.go`
büyümedi; değişiklik `internal/devops/wiki.go`, `internal/wiki/{sync,config,search}.go`,
`internal/mcptools/wiki_tools.go`, `WikiKnowledgeSection.tsx` + `wikiKnowledge.ts`. **Testler:** sahte ADO'da kod
wiki'si klasör düğümü + 404 dönen ara düğüm (ikisi de 404 → atlandı) + çoğunluk-404 korkuluğu + tavanı aşan sayfa → 0 hata, sayaçlar doğru; önceden
indekslenmiş dev sayfa korunur; 404 olan budanır; kart gri/kırmızı ayrımı.

## 2026-10-09 — CoSRE yetenek ipuçları sohbetin dilinde: sabit Türkçe, UI dilinden bağımsız (v0.10.1130)

**Hata (prod):** v0.10.1128'in boş-sohbet "neler yapabilirim" ipuçları (`CosreCapabilities`), title'ı
("Fills the box with an example…") ve wiki şablon çipi ("Search the wiki: …") `useT` ile UI dilinde
çiziliyordu. UI dili marka/kullanıcı Türkçe seçmedikçe İngilizce; CoSRE sohbetinin geri kalanı (karşılama
"Merhaba", "Sana nasıl yardımcı olabilirim?", başlangıç çipleri, yol şablon çipi) ise `CopilotChat`'te sabit
Türkçe. Sonuç: operatör karışık dilli bir karşılama görüyordu.

**Karar:** CoSRE sohbet yüzeyi Türkçe-öncelikli; ipuçları karşılamayla AYNI dilde çizilir.
`capabilityHints.ts`'e `COSRE_LANG = 'tr'` + `tCosre(key)` (= `t(key, COSRE_LANG)`, sabit-dil katalog
araması) eklendi; `CosreCapabilities` (satırlar, title, aria-label, composer'a dolan örnekler) ve
`CopilotChat`'teki wiki çipi (etiket + "wikide " doldurma) `useT` yerine bunu kullanır. Doldurulan örnekler
de Türkçe ("wikide cache refresh nasıl yapılır", "… operasyonu neden hata veriyor?" …).

**Değişmeyen:** `cosre.cap.*` / `cosre.chip.wiki*` EN metinleri katalogda KALIR — sohbet-geneli i18n
geldiğinde (karşılama + çipler dahil hepsi birlikte) `tCosre` → `useT`'ye geçilir; o güne kadar burada
kullanılmaz. Uygulamanın geri kalanının dil seçimi etkilenmez.

**Test:** `CosreCapabilities.test.tsx` — UI dili EN iken çekmecede satırlar, title, aria-label, wiki çipi ve
doldurulan örnekler Türkçe, İngilizce metin yok; `tCosre` saf testi. `pages/CoSRE.test.tsx` — /cosre (UI dili
varsayılan EN) ipuçları ve wiki çipi Türkçe.

## 2026-10-09 — Problems: ortam seçiliyken dış kaynak sayıları listeyle aynı (v0.10.1131)

**Operatör:** env seçiliyken Problems sayısı listeyle tutmuyor; fark dış kaynak (Oracle / Influx, `kind=external`)
problemlerinde. **Kök neden:** `/inbox` derlemesi (`api/inbox.go` `inboxView`) env'i birleştirilmiş satırlar üstünde
Go'da uyguluyor (`chstore.EnvScopeKeepsRow`): çözülmemiş `ext:<kaynak>/…` öznesi hiçbir env'in üyesi değil, satır
gizli. Şerit çipi ("Dış kaynak (N)", "Veritabanı (N)") ve problem türü kapalıyken "Problems N" çipi ise
`CountProblemsBySubject`'ten geliyor ve bu sayıma yalnız takım kümesi geçiyordu, env hiç inmiyordu. Sonuç: env
seçiliyken çip, listenin göstermediği dış kaynak problemlerini sayıyordu. Kenar çubuğu rozeti (`count`, v0.10.1086'dan
beri varsayılan görünümün `total`ı) aynı derlemeden okunduğu için zaten listeyle aynıydı. Env üyeleri yine de iki
ayrı yerde çözülüyordu (`inboxView` ve `computeInboxCountFor`).

**Karar:** (1) Env kapsamı tek çözücüden gelir (`api/inbox_env_scope.go` `resolveInboxEnvMembers`). Liste
daraltması (`inboxEnvScopeItems`), çip sayımı ve rozet aynı üye kümesini kullanır. (2) Sayım kapsamı tek yapıda:
`chstore.ProblemCountScope{Exclude, Team, Env}` (`problem_count_scope.go`). Env ekseni listenin SQL ikizi
`envScopeConjunct` ile yazılır. Go ⇔ SQL eşitliğini `TestEnvScopeSQLAndGoAgree` kanıtlıyor, dış kaynak satırları da
artık bu testin içinde. İkinci bir env kuralı yazılmadı. (3) Semantik değişmedi ve artık açıkça yazılı: problemlerin
kendi env boyutu yok. Dış kaynak problemi gerçek bir servise çözüldüyse (`Kind=service`) o servisin env üyeliğiyle
eşleşir. Çözülmemiş `ext:` öznesinin env'i yok; liste, çip ve rozette yalnız env seçili değilken görünür. Global
(servissiz) ve db öznesi satırlar eskisi gibi her env'de görünür. Problems sayfasındaki env çipinin tooltip'i bunu
söylüyor. (4) Rozetin istemci sorgu anahtarı (`keys.inbox.count(env)`) env'i zaten taşıyordu. Env değişince yeniden
çekildiği artık testle sabitlendi.

**Değişmeyen:** takım ekseninin sayım yazımı (servissiz satır kaçışı; takım süzgecinde belgeli hafif şişkinlik),
Exceptions rozeti (`CountExceptionGroups` katı `service IN`), CH okumaları (`problems FINAL` sayımı
`max_execution_time = 5`, yeni sorgu yok).

**Test:** `chstore/problem_count_scope_test.go`: env seçili/değil × iç/dış kaynak × env'i çözülebilir/çözülemez
matrisinde sayım == liste; kapsam WHERE'i ve argüman sırası. `chstore/env_members_test.go`: SQL ⇔ Go eşitliğine dış
kaynak satırları eklendi. `api/inbox_env_scope_test.go`: liste daraltması, sayım kapsamının env'i taşıması, kaynak
pini (tek çözücü). `lib/queries/inboxCount.test.tsx`: env değişince rozet yeniden çekilir.

## 2026-10-09 — Helm: Grafana MCP sunucusu + açılışta MCP kaydı tohumu (v0.10.1132)

**İstek:** CoSRE, Grafana'yı (dashboard, Prometheus, Loki, alarm kuralları, incident) mevcut dış MCP
istemcisiyle kullanabilsin; operatör Grafana'nın resmi MCP sunucusunu (`grafana/mcp-grafana`) chart'tan
kurabilsin. **Karar:** `grafanaMcp.*` bloğu (varsayılan kapalı) + `templates/grafana-mcp.yaml`: tek replikalı
Deployment, ClusterIP Service, isteğe bağlı token Secret'ı ve NetworkPolicy. Ingress yalnız bu sürümün
Coremetry pod'larından gelir (`coremetry` / `coremetry-api` / `coremetry-worker`). İmaj `2.0.1` sabit
(Docker Hub etiketi v'siz), `global.imageRegistry` ile aynalanır. Varsayılan `--disable-write`,
`--usage-stats=disabled` (banka/hava boşluğu). Grafana kimliği Viewer servis hesabı token'ıdır,
`existingSecret` tercih edilir. **Upstream'den doğrulanan iki tuzak:** (1) mcp-grafana ana dinleyicide
`Host` başlığını allowlist'le doğrular; varsayılan yalnız localhost'tur ve kümeiçi çağrı 403 alır. Bu
yüzden chart Service'in kısa/ns/svc/svc.cluster.local adlarını port'lu ve port'suz `--allowed-hosts`'a
yazar, yoklamalar da TCP'dir. (2) İmajın `USER`'ı sayısal değil (`mcp-grafana`), vanilla k8s'te
`runAsNonRoot` doğrulanamaz. `grafanaMcp.openshift: true` (varsayılan) UID'yi SCC'ye bırakır; `false`
imajın UID/GID'sini (1000) ekler. Bu chart'ın OpenShift-öncelikli varsayılan geleneğine uyar. Upstream
artık çağıran doğrulaması da veriyor (`MCP_GRAFANA_SERVER_TOKEN`), `callerAuth.existingSecret` ile
NetworkPolicy'ye ek savunma olarak bağlandı.

**autoRegister:** `COREMETRY_MCP_SEED_JSON` env'i (`internal/mcpclient/seed.go`) monolitik/api/worker
pod'larına gider. Tohum her ad için ömür boyu yalnız bir kez uygulanır. Uygulanan adlar `system_settings`
`mcp_client_seeded` işaretinde tutulur: o adla kayıt varsa hiçbir alanına dokunulmaz, operatör silerse
kayıt geri gelmez, liste 8'de doluysa işaretlenmeden atlanır. Yalnız `http` taşıması tohumlanır, çünkü
env'den gelen stdio tohumu açılışta keyfî komut çalıştırmak olurdu. Token JSON'a girmez: `tokenEnv` adı
`COREMETRY_MCP_SEED_` önekini taşımak zorunda. Böylece tohum `COREMETRY_JWT_SECRET` gibi bir sırrı Bearer
başlığıyla keyfî URL'ye taşıyamaz. Audit `settings.mcp_servers.seed`, aktör `system`. Çok-pod: tüm pod'lar
aynı girdiden aynı blob'u üretir (idempotent), bu yüzden lider kilidi gerekmedi. `api.go` değişmedi;
`maxMCPServers` artık `mcpclient.MaxServers`.

**Bilinen davranış:** dış MCP sunucusu yapılandırılınca CoSRE `search_wiki` / `read_wiki_page`'i gizler
(v0.10.1122); wiki kendi kademesinden cevap vermeyi sürdürür. README "Grafana MCP" bunu ve önerilen salt-okur
allow-list'i belgeler. Compose için `docker-compose.grafana-mcp.yml` örneği eklendi.

**Test:** `mcpclient/seed_test.go`: ayrıştırma (dizi/sarmal, varsayılanlar, tokenEnv önek kısıtı, stdio
reddi, tekrar), plan (taze kurulum, operatör kaydı korunur, silinen geri gelmez, dolu liste), üç açılış
boyunca idempotentlik. `helm template` iki token kipi × openshift açık/kapalı × NetworkPolicy açık/kapalı ×
monolitik/distributed.

## 2026-10-09 — CoSRE: Oracle operasyon adıyla trace araması fonksiyon koduna çevrilir (v0.10.1133)

**Operatör:** CoSRE'ye büyük harfli, alt çizgili bir Oracle operasyon adıyla ("…_INQUIRY_REST_… operasyonuna
ait trace'leri getir") sorulunca "trace bulunamadı" diyordu. Komut paleti aynı adı doğru çeviriyordu.
**Kök neden:** operasyon adı span'lerde yok, span'ler yalnız fonksiyon kodunu taşır (`FUNCTION_CODE`).
Köprü Oracle hata satırlarıdır. Palet bu çeviriyi `/api/oracle/operations` ile yapıyordu, CoSRE ise adı
haystack'te arıyordu.

**Karar:** tek çözümleyici. `getOracleOperations` içindeki kaynak, kod kaynağı
(`oracle.CodeIsFunctionCode`) ve span anahtarı yazımı (`chstore.PromotedAttrSpelling`) çözümü
`oracleOpScope`'a taşındı. Handler, fonksiyon kodu sözlüğü ve yeni `ResolveOracleOperation` onu
paylaşır. SAF çekirdek `mcptools.ResolveOracleOpHits` yalnız TAM eşleşmeden (kırpılmış, harf duyarsız)
kod üretir, en çok 5 tekil kod. Tam ad önce ayrı bir eşitlik sorgusuyla (`OracleOperationExact`) okunur.
Böylece adı içeren daha kalabalık adlar, alt-dize listesinin LIMIT 20'si yüzünden tam eşi dışarıda
bırakamaz. Alt-dize araması yalnız ayrı bir "yakın adlar" listesi verir ve hiçbir yüzey onlarla
kendiliğinden aramaz, çünkü yanlış operasyonun trace'leri doğru cevap gibi görünürdü. İki okuma da
`oracle_error_log` üzerindedir (7 gün, LIMIT, `max_execution_time`). Canlı Oracle'a gidilmez. Oracle kapalıysa sonuç boş
döner, hata vermez.

**Guided:** `guidedTraceSearchBundle` okumaları enjekte edilen `traceSearchIO` ile yapar. Anahtar keşfi
boş döner ya da servis yoksa, haystack'ten önce metnin operasyon adı şekline bakılır (büyük harf, rakam,
alt çizgi, ≥6, en az bir alt çizgi). Şekle uyuyorsa çözümleyiciye sorulur: `resolve_oracle_operation` adımı ("SAMP01 (1 kod)")
gelir, ardından her kod için `FUNCTION_CODE = kod` süzgeçli bir `trace_search` çalışır (servissiz de
olur) ve sonuçlar `mergeTraceRows` ile birleşir. Kanıt ve kaynak satırı çeviriyi açıkça söyler. Kod
bulunup trace çıkmazsa Oracle satırındaki son trace "son hata trace'i" olarak eklenir. Link ve sohbet
bağlamı süzgeci adla değil kodla kurar (`guidedRoute.OracleCodes`). Eşleşme yoksa haystack aynen çalışır,
yalnız yakın adlar varsa "Bunu mu demek istediniz?" notu eklenir. Oracle'sız kurulumda adım hiç çıkmaz.
Router'a dar bir kural eklendi, yalnız etkin Oracle kaynağı varken (`routeGuidedIntentOpts` bayrağı;
Oracle kapalıyken rota bayt-bayt eskisi). Kural: trace kökü, "operasyon/operation" sözcüğü ve o sözcüğe
bitişik tek bir alt çizgili büyük harf adı → `trace_search`. Bilinen servis ya da ortam adıyla (harf
duyarsız) eşleşen kelime aday sayılmaz: "PAYMENT_SERVICE operasyonlarının hatalı trace'leri" aile
trace'lerine kalır. Bitişiklik şartı "CONNECTION_TIMEOUT hatası veren operasyonların trace'leri" gibi
hata kodlarını eski rotasında bırakır. Bu soru önceden sınıflandırıcıya kalıyordu.

**Araç:** `resolve_oracle_operation` (mcptools/oracle_operation.go) ad → kodlar, span anahtarı, satır
sayısı, son trace ve yakın adlar döndürür. Salt okumadır, `MinRole=""` (REST eşi kapısız). Dış MCP'de
kayıtlıdır. Sohbette koşullu: etkin Oracle kaynağı yoksa sunulmaz. Kompakt katalog 9.1 KB'ta kaldı.
Koşullu tavan 650 → 820 B oldu, bedelini yalnız Oracle'lı kurulum öder. Katalog pini 64 → 65.

**Test:** `mcptools/oracle_operation_test.go` (çekirdek tablosu: tam eşleşme, harf duyarsızlık,
alt-dize asla kod üretmez, çok kod, harf varyantı birleşimi; araç şeması, geçersiz girdi → bad_args,
Oracle kapalı), `api/oracle_op_resolve_test.go` (kapalı çözümleyici, kapsam, şekil kapısı, router,
servissiz/anahtar keşfi boş guided akış, sıfır trace → son hata trace'i, eşleşmesizlikte haystack
süzgeci/kanıtı/kaynağı birebir). `/api/oracle/operations` cevap şekli değişmedi.

## 2026-10-09 — CoSRE wiki takip sorusu: önceki cevabın sayfası yeniden okunur (v0.10.1134)

**Operatör (prod, CoSRE):** 1. tur "production clusterlarında yeni namespace nasıl oluşturabilirim"
wiki sayfasından doğru cevaplandı ("ilgili namespace pipeline'ı tetiklenmelidir"). 2. tur "pipeline
linki nedir" bağlamı kaybetti: wiki'de yalnız "pipeline linki" arandı ve alakasız sayfaların linkleri
listelendi.

**Kök neden:** Her wiki yolu yalnız son kullanıcı metnini görüyordu. `wikiChatAnswer` önce
`lastUserText` alır, sonra işaret kapısına bakar; takip sorusu işaret taşımadığı için kademe atlanır.
RAG kademesi `ragWikiHits(ctx, question)` ile çıplak takip sorusunu arar. Anlatım prompt'u ("SORU: …")
önceki turu hiç taşımaz. İstemci geçmişi de yalnız `{role,text}` gönderdiği için önceki cevabın
"Wiki · " çipleri sunucuya hiç ulaşmıyordu.

**Karar:** Wiki kademesine kapıların ardında, işaret kontrolünden önce bir takip adımı eklendi
(`chat_wiki_followup.go`). Takip sayılması için iki koşul gerekir. Önceki asistan turu bir wiki cevabı
olmalı, yani kaynak sayfası çözülebilmeli. Yeni soru da ≤8 sözcük olmalı ya da geri atıf taşımalı
("bu", "linki", "peki", "nerede"…). Telemetri sinyali taşıyan soru ve servis adı + sinyal taşıyan soru
takip sayılmaz. Önceki sayfa şu sırayla çözülür:

1. Yapısal veri: istemci son turun wiki href'lerini `context.wikiRefs` alanında gönderir.
2. Sunucu hafızası: cevap metninin FNV özetinden sayfa kimliklerine; Redis'te ve süreç içinde, 24 saat.
3. Önceki cevap metnindeki wiki url'leri.

URL'yi sayfaya eşlemek için önce hafızaya bakılır, sonra `pagePath` ayrıştırılır, en son başlıkla arama
yapılır. Akış üç adımdır:

- **(a)** Önceki 1–2 sayfa `ReadPage` ile yeniden okunur; bu karma, senkron ve canlı modda çalışır.
  Modele sayfa, önceki soru-cevap ve takip sorusu gider. Model ya sayfadan cevaplar ya da
  `[[WIKI_NOT_IN_PAGE]]` döner; tahmin yapmaz.
- **(b)** Sayfa cevaplamazsa bağlamlı arama yapılır. Sorgu takip terimleri, sayfa başlığı ve önceki
  sorudan oluşur. Önce aynı projede aranır, önceki sayfa hariç tutulur. "Wikide bulunamadı" cevabı
  kabul edilmez.
- **(c)** O da cevaplamazsa bugünkü akış aynen sürer.

Tüm wiki anlatımları (kademe, netleştirme kurtarması, RAG'ın wiki yarısı) son soru-cevap çiftini kırpılmış
ve `<onceki_konusma>` çitiyle taşır. İlk turda prompt bayt bayt eskidir. Adım çipi `wiki_followup · <sayfa>`
olarak görünür, önceki sayfanın çipi ilk sıradadır. Yeni prompt `systemWikiFollowUp`, `prompts.go`'dadır.

**Değişmeyen:** API token'ı, panel/çekmece bağlamı ve kapalı wiki için kapı aynıdır. Önceki tur wiki
değilse hiçbir okuma ya da arama yapılmaz. Telemetri takipleri guided'a gider. `api.go` büyümedi.

**Test:** `chat_wiki_followup_test.go` sentetik sayfalarla çalışır. A sayfası namespace pipeline
`definitionId=123` içerir, B/C başka pipeline linkleri taşır. Senaryolar: iki turlu akış (yapısal ref ve
yalnız hafıza); sayfada yok → bağlamlı arama; hiçbiri cevaplamıyor → akışa bırakma; wiki olmayan önceki
tur; kapılar (kapalı, token, çekmece, telemetri); anlatımda önceki tur. Saf yardımcılar da ayrıca test
edilir. FE tarafında `chatWikiRefs.test.ts` var.

## 2026-10-09 — Go 1.26 toolchain + x/net v0.60 (govulncheck: GO-2026-6610…6617) (v0.10.1135)

1133'ün CI'ı Security adımında kırmızıya düştü: yeni yayımlanan GO-2026-6610/6611/6612/6613/6617
(net/http, net/textproto, crypto/tls, html/template, x/net/http2) erişilebilir yollarda. Stdlib
düzeltmesi yalnız go1.26.9'da (1.25 dalı artık yama almıyor), x/net düzeltmesi v0.60.0'da ve
o sürüm `go 1.26` istiyor. Karar: go.mod `go 1.26.0`; CI/CodeQL/Release `go-version: '1.26'`
(check-latest açık → en son 1.26.x yaması); Dockerfile ve demo Dockerfile `golang:1.26-alpine`;
govulncheck v1.7.0 → v1.8.0 (go >= 1.26). Kod değişikliği yok; x/sync, x/sys, x/text bağımlılık
olarak yükseldi. On-prem derleme ortamı golang:1.25 imajını aynalıyorsa 1.26 imajı da aynalanmalı.

## 2026-10-09 — CoSRE wiki: çok kaynaklı okuma, modelden bütçe ve iki aşamalı sayfa seçimi (v0.10.1136)

**Operatör:** wiki cevapları tek sayfaya ya da birkaç çıplak parçaya sıkışıyor; prosedür ve tablolar
ortadan kesiliyor, birden çok sayfaya dağılan bilgi birleşmiyor. **Kök neden:** bağlam ya baskın sayfanın
ilk ~6000 karakteri (cevap sayfanın sonundaysa hiç görünmüyordu) ya da ≤4 parça × 1500 karakterdi; RAG
yarısı ≤3 parça × 1500. Komşu adımlar ve aynı bölümün devamı modele hiç ulaşmıyordu. Bütçe sabitti ve
modelin bağlam penceresinden habersizdi.

**Karar:**

- **Sayfa düzeyinde çok kaynak.** Tabanı geçen isabetler sayfa başına gruplanır ve sayfa düzeyinde
  tekilleşir. En iyi sayfanın 0,5 katının altındaki sayfa düşer. Skor sırasıyla en çok 5 sayfa okunur.
- **Genişletme.** Her sayfada isabet parçası, komşuları (önceki/sonraki) ve aynı başlık bölümünün geri
  kalanı (alt başlıklar dahil) öncelik sırasıyla bütçeye sığdığı kadar alınır. Parçalar belge sırasında
  ve tekrarsız birleşir, atlanan aralık `…` ile gösterilir. Baskın sayfada ve model-seçimli sayfalarda
  sayfanın geri kalanı da girer, sığıyorsa tamamı. Parçalar `wiki.Service.PageChunks` ile okunur. Karma
  ve senkron modda bu sohbete özel `WikiPageChunkTexts` sorgusudur: tek sayfa, embedding ve jeton
  okunmaz, WHERE + LIMIT 2000 + max_execution_time 5. `FINAL` kalır, çünkü ReplacingMergeTree'nin
  birleşmemiş eski sürümleri ve mezar taşları ancak onunla doğru elenir. Canlı modda bellek önbelleği ya
  da API okunur, CH'ye yazılmaz. Kapsam her yolda denetlenir; wiki adı bilinmiyorsa saklı parça okunmaz,
  `ReadPage` kaydın kendi adıyla denetler.
- **Bütçe modelden, pencere her zaman kazanır.** Bütçe = (pencere − completion − 1500 jeton ek yük) ×
  3,0 karakter/jeton. Completion, o yüzeyin çözülen profilindeki max token'dır (varsayılan 4096).
  Pencere `modelcaps.ContextWindow` ile bulunur; bu tutucu bir tablodur ve yalnız adı açıkça uzun bağlam
  söyleyen modeller büyük pencere alır, küçük ya da belirsiz varyantlar 8192 sayılır. Pencere biliniyorsa
  otomatik bütçe en çok 24000'dir, elle değer de pencereyle kapaklanır (`min(elle, pencere tavanı)`).
  Pencere bilinmiyorsa otomatik bütçe 16000'dir, ama 8192 pencere varsayımıyla kapaklıdır; 4096
  completion'da bu 7788 karakter eder. Bilinmeyen modelde elle değer kapaklanmaz, çünkü operatör modelini
  bilir; bu bilinçli bir karardır. Bütçe çıktının tamamını kapsar: `[n]` işareti, "Sayfa:" satırı,
  "## bölüm" ve `…` satırları ile 600 rune'luk sayfa tabanı dahil. Blok sığmazsa metin payı küçültülüp
  parça sınırından yeniden kurulur; yine sığmazsa sondaki sayfa düşer ve çipe de girmez. Sayfalara skorla
  orantılı dağıtılır; en iyi sayfa en büyük payı alır. Sayfa başı tavan %45, baskın sayfada %70, tek
  sayfada %100. Kullanılmayan pay sonraki sayfaya akar. RAG yarısı bütçenin %50'sini kullanır ve en çok 3
  sayfa okur. Takip (a) blok ek yükünü düştükten sonra tek sayfada bütçenin tamamını, iki sayfada 65/35
  alır.
- **Ayar.** Ayarlar → Bilgi (RAG) → Azure DevOps Wiki altında iki alan var. "Wiki bağlam boyutu
  (karakter)" boşsa otomatiktir, değilse [4000, 48000] olmalı; aralık dışı değer 400 döner. Model
  penceresi biliniyorsa bu değer pencereyle kapaklanır. "İki aşamalı
  okuma (sayfa seçimi)" varsayılan açıktır. İkisi de `wiki_knowledge` blobunda tutulur ve
  `settings.wiki.update` audit'ine girer.
- **İki aşamalı okuma.** Aramadan sonra en çok 10 aday sayfanın başlığı, bölüm yolu ve ~200 karakterlik
  çitli kesiti, soru ve önceki turla birlikte modele verilir. Model `{"pages":[…]}` JSON'u ile 1–5 sayfa
  seçer (niyet sınıflandırıcısının JSON deseni, `wiki-select` yüzeyi). Yalnız seçilen sayfalar tam okunur
  ve sayfa başına daha büyük pay alır (`min(0.8, 1.6/n)`). Görünür adım
  `wiki_select · N aday → seçilen: A, B` biçimindedir. Çağrı en çok 8 sn sürer (istemci zaman aşımı daha
  kısaysa o). Hata, zaman aşımı, geçersiz ya da boş çıktıda skor tabanlı seçime düşülür; liste dışı
  numaralar yok sayılır. Tek aday sayfa varsa çağrı hiç yapılmaz. Seçim açık wiki kademesinde ve takibin
  bağlamlı aramasında (b) çalışır. RAG yarısı ve netleştirme kurtarması tek geçişli kalır.
- **Prompt.** Her kaynak `[n] Wiki · <temiz başlık>` işaretiyle ve `<wiki_data>` çitinde gelir; n,
  "Kaynak n" çipinin numarasıdır (sayfa başına tek numara, `sourceNumbers`). Çit dışındaki başlıktan
  `[]#*`, backtick, `<>` ve satır sonları atılır. Sayfa başlığı çitin ilk satırıdır ("Sayfa: …"), bölüm
  başlıkları gövdede yer alır; yani başlık da veridir. Seçim çağrısında çit dışında yalnız aday numarası
  `[i]` durur; başlık ve bölüm yolu çitin içindedir. `systemWikiChat` ve `systemWikiFollowUp`
  şunları ister: kaynakları birleştir, her bilgiyi [n] ile atfet, çelişkiyi iki numarayla göster, wiki
  dışı çıkarımı ayrı bir "Wiki'de yok, tahmin:" satırında ver. Kaynakta olmayan link, host ya da komut
  uydurulmaz. RAG kademesi wiki isabeti varken `systemRAGChatWiki` kullanır. Bu prompt doküman gövdesinin
  üstüne kurulur ve gövdenin "asla tahmin etme" ile "doküman adı verilmez" kurallarını yalnız wiki
  kaynakları için açıkça istisna eder; doküman parçaları için bu kurallar aynen geçerlidir. Wiki yokken
  `systemRAGChat` bayt bayt eskidir. `systemWikiSelect` yeni JSON prompt'udur.
- **Çipler.** Okunan bütün sayfalar `[n]` sırasıyla "Kaynak n" ve "Wiki · başlık" olarak görünür. Takip
  (b)'de de bağlantılar [n] sırasındadır; önceki sayfanın bağlantısı (bağlam çıpası, v0.10.1134) artık
  sondadır.

**Değişmeyen:** API token'ı, panel/çekmece bağlamı, telemetri sinyali ve kapalı wiki kapıları aynıdır.
Wiki kapalıyken bu yolların hiçbiri koşmaz ve RAG prompt'u bayt bayt eskidir. `api.go` büyümedi.

**Test:** `chat_wiki_multi_test.go` sentetik P1…P7 sayfalarıyla çalışır. Senaryolar: 5 sayfa seçimi
(mutlak ve göreli taban, sayfa tekilliği); komşu ve bölüm genişletmesinin belge sırasında, tekrarsız
olması; bütçe (modelden otomatik, elle değer, kelepçeler, dağıtım, kademe payı); canlı mod (önbellek, CH
yazımı yok); bağlamın [n] = çip sırasında olması; iki aşamalı seçim (kullanım; geçersiz, boş, liste dışı
ve zaman aşımında düşüş; tek adayda ve ayar kapalıyken çağrı yok); ayar doğrulaması ve audit. Ayrıca
`modelcaps` pencere tablosu, prompt pinleri (`prompt_wiki_multisource_test.go`, dil ve enjeksiyon
sicilleri) ve FE alanı (`WikiKnowledgeSection.test.tsx`) test edilir.

## 2026-10-09 — CoSRE sohbeti: güvenli tıklanır linkler, Claude benzeri görünüm, sol kenar çubuğunda geçmiş (v0.10.1137)

**Operatör (prod, wiki cevabı ekran görüntüsü):** cevaptaki Jenkins adresi düz metin çiziliyor; sohbet
"Claude gibi" profesyonel görünmeli; /cosre'de geçmiş solda dursun. Sohbet balonu v0.9.1148'den beri
bilinçli olarak hiç link çizmiyordu. Sebep prompt-injection: wiki sayfasına gömülü bir talimat modele
"şu adrese ?q=<gizli> ekle" dedirtebilir ve tıklanır bir link veriyi dışarı sızdırır.

**Karar 1, link güvenliği (`chatLinks.ts`, `chat_allowed_links.go`):** sunucu her wiki, takip, kurtarma
ve RAG cevabına `allowedLinks` ekler. Liste şunlardan kurulur: modele GERÇEKTEN verilen wiki/doküman
bağlamındaki URL'ler (katı regex; tekilleştirilir; en çok 50 adres; önceki soru/cevap bloğu HARİÇ, çünkü
orası model çıktısıdır), çip href'leri, kaynak ref'leri ve aynı-köken yollar. Arayüz bir linki üç
durumda tıklanır çizer: (a) normalize adresi listede ise (host küçük harfe çevrilir, sondaki noktalama
kırpılır, `#` parçası yok sayılır), (b) göreli ya da aynı-köken bir yol ise, (c) host'u bu cevabın
KAYNAK veya ÇİP host'u ise. Host kümesi bilinçli olarak bağlamdaki her URL'nin host'undan kurulmaz.
Aksi hâlde enjekte bir sayfanın andığı saldırgan host'una modelin uydurduğu sorgu dizesi geçerdi. Bu
koşullara uymayan adres düz metin olarak, tam hâliyle ve "doğrulanmamış bağlantı" ipucuyla çizilir;
markdown linkinde metin ile adres birlikte gösterilir. `javascript:`, `data:` ve `//host` hiçbir
koşulda link olmaz, görsel hiç çizilmez. Arşivden açılan konuşma `allowedLinks` taşımaz, bu yüzden
orada dış link doğrulanmamış sayılır (güvenli varsayılan).

**Karar 2, çizim:** satır içi çizim artık innerHTML ile değil, saf bir düğüm çözücüsüyle (`chatInline.ts`)
ve React çocukları olarak yapılıyor; balonda `dangerouslySetInnerHTML` hiç kalmadı. `chatMarkdown.ts` (saf)
şunları biliyor: h1–h4, iç içe ve N'den başlayan listeler, görev listesi, alıntı, çizgi, GFM uyarı
kutuları (NOTE/TIP/WARNING/IMPORTANT/CAUTION ve NOT/İPUCU/UYARI/ÖNEMLİ/DİKKAT), en üstteki
"**Özet:**" satırı ya da `> [!ÖZET]` bloğundan özet kutusu, `<details><summary>` (yalnız bu iki etiket;
geri kalan her şey metin), dosya bloğu, diff renkleri, uzun satır sarma, ~~üstü çizili~~, `[[Ctrl+C]]`
tuş kapağı ve "**Anahtar:** değer" tanım satırları. Dosya bloğu `title=`/`lang:yol` başlığı, satır
numarası, Kopyala ve İndir düğmeleri taşır. İndirilen dosyanın adı süzülür ve yalnız son yol parçası
kalır. 25 satırı aşan blok "Tümünü göster" ile katlanır. Sunucudaki `fenceLang` da aynı bilgi dizesi
kuralıyla güncellendi (ikiz pin). Kaynaklar şöyle çiziliyor: [n] atıfı üst simge hap olur ve ipucunda
kaynak başlığı ile host görünür; "Kaynak n" çipleri yerine numaralı ve katlanır bir "Kaynaklar" listesi
geldi. Adımlar tek bir "Nasıl cevapladım · N adım" açılırında toplandı: akış sürerken açık, bitince
kapalı. İlk token gelene dek "Düşünüyor…" görünür. Kaydırma yapışması kap değişince yeniden bağlanıyor;
önceden çekmece kapalıyken dinleyici hiç kurulmuyordu. Kullanıcı yukarı kaydırınca "↓ En alta"
düğmesi çıkar. Asistan cevabı kart değil, düz metin olarak çizilir; operatörün mesajı sağa yaslı
haptır. /cosre'de okuma sütunu 760px'dir. Kopyala düğmesi düz metin kopyalar. Yeniden üret düğmesi
eklenmedi, çünkü yalnız hata sonrası "Yeniden dene" yolu var.

**Karar 3, geçmiş:** /cosre'de "🕘 Geçmiş" düğmesinin yerini kalıcı bir sol kenar çubuğu aldı (260px).
Çubukta "Yeni sohbet", istemci tarafı başlık araması ve "Bugün / Dün / Son 7 gün / Daha eski" grupları
var; etkin konuşma vurgulanır, tıklanınca `?chat=` yazılır. Daraltma tercihi localStorage'da tutulur.
Telefonda çubuk ekran dışı bir çekmecedir ve ☰ ile açılır. Veri kaynağı aynı (`/api/ai/conversations`).
Yeniden adlandırma ucu olmadığı için menüde yalnız silme var; yeni uç eklenmedi. Uygulama içi çekmece
kompakt Geçmiş düğmesini korur.

**Karar 4, cevap üslubu:** `ChatAnswerStyle` eki wiki, wiki takip, RAG ve serbest döngü anlatım
prompt'larına eklendi: tek satırlık özet, prosedür için liste, karşılaştırma için tablo, dil etiketli ve
`title=` başlıklı kod bloğu, uyarı için callout. Bulunamadı ve ret cümleleri özet satırı almaz, çünkü
`wikiDeclined` önek, `ragDeclined` içerik eşleşmesi kullanıyor. Guided ve çekmece prompt'ları değişmedi.
Giriş kutusu için zengin metin düzenleyicisi kapsam dışı; kuyruğa alındı.

## 2026-10-09 — CoSRE sohbeti: model seçici, yeniden üret/düzenle, @ ve / kapsamı, tam sayfaya geçiş (v0.10.1138)

**Operatör onaylı dört iyileştirme.** Ortak bileşen `CopilotChat` (çekmece + /cosre), `useChatThread`,
sunucuda `copilot_chat.go`. api.go büyümedi: yeni uç `chat_scope_names.go`, kapsam `chat_scope.go`,
profil kapısı `chat_profile_gate.go`.

**Karar 1, model seçici (hata + özellik):** Profiller sunucuda vardı ama `useCopilotEnabled` önbelleği
yalnız enabled/model/wiki tutuyordu; `profiles`/`defaultProfile` düşüyor, seçici hiç çizilmiyordu
(kök neden). Önbellek artık bütün yanıtı taşır (`normalizeCopilotConfig`). Başlıktaki model rozeti
`ModelPicker`'dır: >1 izinli profilde "model ▾" menüsü (ad + model + kısa açıklama), tekte tıklanamaz
rozet. Seçim kullanıcı başına localStorage'da (try/catch, bellek yedeği) ve mevcut `context.profile`
alanıyla gider. Profile `description` (≤120) ve isteğe bağlı `roles` allowlist'i eklendi (admin AI
ayarlarından düzenler, audit satırı `roles=` taşır); boş = herkes. `/api/copilot/config` yalnız çağıranın
rolüne açık profilleri listeler. Sunucu açık seçimi doğrular: bilinmeyen 400 `profile_unknown`, rolüne
kapalı 403 `profile_forbidden` (SSE'den önce); eskiden bilinmeyen kimlik sessizce varsayılana düşüyordu.
İstemci iki kodda seçimi varsayılana döndürür. Allowlist yalnız AÇIK seçimi kısıtlar; seçimsiz istek
varsayılan profil / yüzey eşlemesiyle koşar.

**Karar 2, yeniden üret ve düzenle:** "↻ Yeniden üret" yalnız son tamamlanmış cevapta, akarken kapalı:
aynı soru, aynı geçmiş, aynı kapsam/komut yeniden gider ve cevap yerine geçer; önceki cevaplar
`alternatives`te (en çok 5) "önceki cevap (1/2)" geçişiyle durur. Her istek yeni exchangeId alır,
👍/👎 her cevabın kendi kimliğine yazılır (çift sayım yok). Son kullanıcı mesajında ✎: satır-içi
düzenleyici, "Gönder" sonrasını kırpar ve yeniden koşar. Arşiv ucu (`ai_conversations.go`) tam
transkripti upsert ediyor (ekleme değil), yani kırpılmış transkript aynı kimlikle yazılır; ek uç yok.

**Karar 3, @ ve / kapsamı:** Metin yazıldığı gibi gider, yanına yapısal `context.scope`
(`services/trace/problem/env/team/wiki`) ve `context.command` (`wiki|trace|rca|logs|help`) konur.
`@ad` yalnız tamamlamadan seçildiyse ya da servis biçimindeyse (tireli) kapsam olur: yapıştırılan
stack trace'teki `@Override` kapsam değildir. Komut yalnız mesaj başında ve bilinen adsa (`/api/x` değil).
Sunucu kapsamı router sezgilerinden ÖNCE uygular: `/help` ve bilinmeyen komut LLM'siz liste; `/wiki` ve
`@wiki` telemetri yönlendirmesini atlar (LiveOnWeak; wiki kapalı / API token'ı açık mesaj); `@trace:` →
trace_by_id (Explain çekirdeği); `@problem:` → problemin servisi + penceresinde kök neden; `/trace`,
`/rca`, `/logs` mevcut kılavuz rotalara iner. Servis/env/takım canlı kataloglarla (router'ın AYNI 60 s
listeleri) doğrulanır; bilinmeyen değer LLM'siz "şunu mu kastettin?" + çip. Geçerli kapsam ctx'e konur,
`runGuidedRoute` her rotaya uygular: kapsam verilen boyutta "Hangi servisi kastettin?" sorulmaz. Kapsam
erişimi genişletmez: yalnız sorguyu daraltır, bundle'lar kendi yetki/limit yolunu kullanır. Kapsamsız,
komutsuz serbest metin bayt bayt eski yoldadır (testle pinli). `@team:` tamamlaması için
`GET /api/copilot/scope-names?kind=team&q=` eklendi (sunucu araması, ≤20); env mevcut
`/api/environments?q=` aramasını kullanır.

**Karar 4, tam sayfaya geçiş:** Uygulama içi çekmece başlığında "Tam sayfada aç ↗" gerçek bir link
(`/cosre?chat=<id>`): Ctrl/Cmd/orta tık yeni sekme açar. Düz tıkta bekleyen kayıt hemen yazılır (kimliği
sunucu basar), çekmece kapanır, aynı sekmede gezilir; boş konuşmada yalnız `/cosre`. Akarken kapalı,
çünkü gezinme akışı keserdi. /cosre'deki "Coremetry'yi aç" aynen kaldı.

## 2026-10-09 — CoSRE @-kapsamı: köken işareti, çekmece kapısı, yeniden üretimde yeniden çözüm (v0.10.1139)

v0.10.1138 incelemesinin bulguları.

**Karar 1, yeniden yüklenen konuşmada yeniden üret:** Kalıcı turlar yalnız `{role,text}` saklar; geçmişten
açılan konuşmada kullanıcı turunun kapsamı/komutu yoktur. `regenerateBase` bu durumda boş `{}` yerine
`parsed: undefined` döner ve `send` metni yeniden çözer: "/rca @svc-orders" yeniden üretildiğinde
`context.command`/`context.scope` yine gider. Oturum içindeki turda kayıtlı kapsam aynen kullanılır.

**Karar 2, anmanın kökeni:** Servis adı biçimi (küçük harf + nokta/tire) "@john.doe", "@spring-boot" gibi
kişi/kütüphane anmalarını da yakalıyor. İstemci tamamlamadan SEÇİLMEMİŞ, yalnız biçimce eşleşen adları
`scope.shaped` ile işaretler. Sunucu bilinmeyen biçim-eşleşmesini sessizce düşürür; sert "X adında bir
servis yok" cevabı yalnız tamamlamadan seçilen adda ve tek servisli `/rca`'da kalır. Biçim-eşleşmeleri
düşünce kapsam boş kalır ve komut da yoksa kademe hiçbir şeye dokunmaz, serbest metin yolu bayt bayt
eskidir. `shaped` alanı olmayan eski istemcide her ad açık seçim sayılır (v0.10.1138 davranışı).

**Karar 3, çekmece kapısı:** İstek çekmece bağlamı taşıyorsa (explain, subject, ekrandaki trace ya da
sayfanın traceId'si), istemci @-kapsamını göndermez, sunucu da kapsam kademesine almaz
(`chatScopeDrawerGate`). Böylece trace/exception çekmecesi takipleri kendi akışlarında kalır. Mesaj
başındaki açık `/komut` ise yine uygulanır. Composer çipleri gönderilecek hâli gösterir.

**Karar 4, tam sayfaya geçişte kayıt hatası:** "Tam sayfada aç" bekleyen kaydı hemen yazar. Kayıt
başarısızsa artık gezinilmez: çekmece açık kalır, hata toast'u gösterilir, operatör yeniden deneyebilir.
Arka plandaki debounce kaydı hata durumunda eskisi gibi sessiz kalır.

**Karar 5, varsayılan profilde rol kısıtı:** Varsayılan profilin rol allowlist'i yalnız açık seçimde
uygulanır; profilsiz istekler yine bu profille çalışır. AI ayarları paneli, varsayılan profilin rol listesi
doluyken bunu bir uyarıyla söyler.

## 2026-10-09 — Services namespace süzgeci: üyelik telemetriden, azınlık namespace kaybolmuyor (v0.10.1140)

**Operatör (test ortamı):** Services sayfasında X-prep seçilince servisler geliyor, X-uat seçilince hiçbiri.
Aynı servisler (ör. bir gateway) uat'tan da OTel ile trace gönderiyor, servis detayında env=uat trace
gösteriyor. Prod'da sorun yok, çünkü orada her servis tek namespace'te koşuyor.

**Kök neden:** `service_metadata` servis başına TEK namespace tutar. Deriver en sık görülen değeri yazar
(`chstore/service_metadata.go` `marginalModes` → `populateNamespaces`). `getServices` süzgeci bu tek
değerle eşitlik arıyordu (`md.Namespace != namespace`), dolayısıyla prep'te yoğun, uat'ta seyrek koşan
bir service.name uat süzgecinde hiç çıkmıyordu. `getNamespaces` de aynı katalogdan besleniyordu, yani
yalnız azınlıkta yaşayan bir namespace açılır listeye de giremiyordu.

**Karar:** Namespace → servis üyeliği telemetriden okunuyor: `entity_seen_5m` içinde pencerede görülen
(k8s_namespace, service_name) çiftleri (`ServicesSeenInNamespace`, cluster seçiliyse cluster koşulu da
ekleniyor). Buna katalog değeri BİRLEŞİMLE ekleniyor: operatörün elle sabitlediği namespace hiç
kaybolmuyor, `service.namespace` kaynaklı (k8s dışı) namespace'ler de korunuyor. Süzgeç hâlâ `serviceIn`
allowlist'ine çözülüyor, yani MV hızlı yolu kapanmıyor (v0.9.189 sözleşmesi). Seçenek listesi de
"pencerede görülen namespace'ler ∪ katalog" oluyor; anahtar `namespaces:v2:<from/to kovası>` (tek girdi
from/to, v2 öneki dağıtım sırasında eski listenin servis edilmesini önlüyor). Telemetri okuması düşerse
(`entity_seen_5m` yok, 0011 uygulanmamış) eski katalog-yalnız davranışa dönülüyor (fail-open). Kod:
`internal/chstore/service_namespace_membership.go`, `internal/api/services_namespace.go`; api.go
küçüldü (11521 → 11484).

**CH maliyeti:** `entity_seen_5m`: AggregatingMergeTree, `PARTITION BY toDate(time_bucket)`,
`ORDER BY (service_name, cluster, k8s_namespace, k8s_pod, time_bucket)`, TTL 30 gün, küme kipinde
Distributed, shard anahtarı `cityHash64(service_name)`. Namespace okuması ORDER BY önekinde değil, yani
budamayı gün partition'ı yapıyor. Okunan yalnız 3-4 dar kolon (agregat state'ler okunmuyor). 24 saatlik
pencerede yaklaşık 1M satırın altı (~2.5k pod × 288 kova/gün), 30 sn (liste) ve 5 dk (seçenekler)
önbellekli. Rozet sayımı `service_name IN (sayfa ≤500)`, yani önekten okuyor ve en ucuzu. Hepsinde zaman
sınırlı WHERE + LIMIT (5000 servis / 1000 namespace / 500 ad) + `max_execution_time = 10` var. Ham
`spans` taranmıyor. Ayrı, namespace önde bir MV tek bir süzgeç için her span INSERT'ine ikinci yazım
yükü getirirdi; bu yüzden reddedildi.
Bilinen sınır: `k8s_namespace` yalnız `k8s.namespace.name` / `kubernetes.namespace.name`'den geliyor,
`service.namespace` azınlık üyeliği görünmüyor. Bu yüzden katalog birleşimi kalıcı.

**Metrik doğruluğu (ayrı karar):** `service_summary_5m` `ORDER BY (service_name, time_bucket)`, ikizi
`service_env_summary_5m` ise `(service_name, cluster, deploy_env, time_bucket)`. İkisinde de namespace
boyutu yok. Ham yol da namespace yüklemi uygulamıyor. Sonuç: süzgeç açıkken satırdaki RED değerleri
servisin TÜM namespace'lerinin toplamı (uat satırı prep trafiğini de taşıyor). Seçenekler:
(a) boyutlu MV, `service_ns_summary_5m` `(service_name, k8s_namespace, time_bucket)`: doğru p99 ve
apdex verir. Maliyeti her span INSERT'inde bir MV daha (+%1-2 ingest CPU), satır başına ~1-1.5 KB
tDigest ve namespace/servis çarpanı kadar satır. Geriye dolmaz, `EnvSummaryCovers` benzeri bir kapsama
probu gerekir. (b) Namespace seçiliyken env/cluster gibi ham yola düşmek: kod olarak ucuz, ama MV
sözleşmesini bozar ve 1B span/gün'de pencere taraması `/api/services` 50 ms bütçesini aşar.
(c) Satır rozeti. Bu sürümde YALNIZ (c) uygulandı: süzgeç açıkken servis >1 namespace'te koşuyorsa
`namespaceCount` geliyor ve FE "N ns · toplam" rozetini gösteriyor; title "bu servis N namespace'te
çalışıyor; metrikler toplamdır". Sayım aynı `entity_seen_5m`'den ve sayfayla sınırlı.
**Öneri:** (a). Ara adım olarak `entity_seen_5m`'in kendi span/error/duration_sum state'lerinden
namespace başına sayı / hata / ortalama verilebilir; p99 ve apdex verilemez, `k8s_pod` boşsa da eksik
sayar.

**Kuyruk:** (1) `service_ns_summary_5m` boyutlu MV + kapsama probu + namespace süzgecinde MV okuması
(seçenek a). (2) Kapsama dışı pencerede ham yola düşme (seçenek b), yalnız (a)'nın geriye dolmayan
penceresi için ve rozet korunarak.

## 2026-10-09 — CoSRE model seçici composer'ın içinde (v0.10.1141)

**Operatör:** model seçici Claude'daki gibi mesaj kutusunun içinde olsun; başlık sade kalsın.
**Karar:** `ModelPicker` sohbet başlığından composer'a taşındı — üç yüzeyde de (CoSRE çekmecesi,
`/cosre` sayfası, ✨ Explain çekmecesi sohbeti) Gönder/Durdur'un solunda kompakt hap
(`.cm-composer__actions` › `.cm-model-pill`, yalnız model adı + ▾). Başlıkta yalnız başlık, Geçmiş/kenar
çubuğu, Temizle, Tam sayfada aç, tema kalır. Menü YUKARI açılır: `ui/Popover`'a isteğe bağlı
`placement` ({prefer:'top', align:'start'}) ve `initialFocus` eklendi, `placeMenu` saf işlevi aynı
tercihleri alır (verilmezse eski kural bayt bayt aynı; tüm diğer menüler etkilenmez); üst sığmazsa alta
döner, telefon genişliğinde viewport kenar payında kıstırılır. Satır: ✓ + ad + model + açıklama,
seçili `menuitemradio aria-checked`; açılışta odak seçili satırda. Klavye: yerel düğme (Enter/Space),
↑/↓ hapı da açar, menüde ↑↓ Home End, Esc kapatır ve odak hapa döner (tek Esc kanalı escLayer). Akış
sürerken hap `disabled` ve açık menü kapanır — model yalnız boştayken değişir. ≤1 izinli profilde aynı
yerde tıklanamaz `model <ad>` etiketi (v0.9.1037 sözleşmesi; çekmecede de tutuldu, gizlenmedi). Kalıcılık
(`chatProfileStore`) ve 400/403 → varsayılana dönüş aynen. Renkler yalnız token (açık/koyu).
**Reddedilen:** Claude'un iki satırlı düzeni (metin üstte, araç çubuğu altta) — composer'ı bir satır
uzatırdı, dar çekmecede sohbete yer kalmıyordu; hap aynı satırda Gönder'in yanında. Ayrı bir menü
bileşeni yazmak — Popover zaten rol/klavye/Esc/odak dönüşü sözleşmesini taşıyor; yalnız yerleşim
tercihi eksikti.
**Testler:** `modelPicker.composer.test.tsx` (üç yüzeyde composer içinde, yukarı açılış, klavye,
akarken devre dışı, tek profil etiketi), `menuPlacement.test.ts` (prefer top / align start / telefon),
başlık pinleri güncellendi (`drawerParity`, `chatImprovements`, `modelChip`).

## 2026-10-09 — CoSRE tip ölçeği: sohbet yüzeyleri tek tipografi + 4/8 ritmi (v0.10.1142)

**Operatör:** `/cosre` sol geçmiş çubuğunun yazısı çok küçük (11px düğme metni); genel tipografi
tutarsız — "gerçek bir sohbet asistanı (Claude) gibi" olsun.
**Karar:** CoSRE'ye ait küçük bir tip ölçeği `.cosre-root` (+ başlık satırı `.cosre-head`) üzerinde CSS
değişkeni olarak tanımlandı, tema-bağımsız (yalnız boyut/ağırlık/aralık; renkler tema token'ı):
`--cosre-fs-body` 14px (çekmece) / 15px (`.cosre-root--page`), `--cosre-fs-ui` 14px, `--cosre-fs-small`
13px, `--cosre-fs-meta` 12px, `--cosre-fs-h1..h4`, satır yüksekliği 1.6 gövde / 1.35 başlık, ağırlık ve
harf aralığı kademeleri, 4/8 boşluk (`--cosre-s1..s5`) ve `--cosre-row-h` 38px. Yazı ailesi yeni değil:
gövde body yığınından miras, teknik ayrıntı `--font-mono`. Uygulama: kenar çubuğu satırı 14px (uzun
başlık ellipsis + `title` ipucu, etkin satır zemin + sol vurgu çizgisi), grup başlığı 12px büyük harf
soluk meta ve kaydırırken yapışkan, arama + "Yeni sohbet" 14px, çubuk 260→280px; başlık markası 16px,
meta 12px; mesaj gövdesi + composer gövde boyutunda; çip/hap/kaynaklar/kod 13px (kod mono), tablolar
14px, adımlar 12px (mono yalnız araç adı/argümanda). Çekmece gövdesi `.cosre-root--drawer` sarmalına
alındı. Telefon davranışı (ekran dışı çekmece, ☰) ve sohbet mantığı DEĞİŞMEDİ; odak halkası evin
`:focus-visible` kuralı (kenar çubuğu satırında içe kaydırılmış, kırpılmasın diye).
**Reddedilen:** Ölçeği evin `--fs-*` merdivenine eklemek — o merdiven yoğun tablo yüzeylerinin (gövde
12px) ölçülmüş dağılımı; okuma yüzeyi 15px gövde ister, karıştırmak iki tarafı da bozardı. Kuralları
`.cm-*` tabanına doğrudan yazmak — `ChatBubble`/`cm-md-*` RCAVerdictPanel, CopilotExplain ve ✨ Explain
çekmecesinde de kullanılıyor; kapsam `.cosre-root` altında tutuldu, oralarda görünüm aynı. İkon-yalnız
daraltılmış ray — mevcut ☰ gizle/göster davranışı korundu (isteğe bağlıydı).
**Testler:** `cosreTypeScale.test.tsx` (kök değişkenleri tanımlıyor, sayfa 15px, kenar çubuğu/composer/kod
kuralları değişkenleri okuyor, sayfa+çekmece kökleri `.cosre-root` taşıyor, kenar çubuğu başlık span'ı +
ipucu + etkin satır).

## 2026-10-09 — CoSRE: işaretsiz soruda İÇERİK tabanlı wiki yoklaması (v0.10.1143)

**Operatör (prod):** /cosre'de "Sık karşılaşılan cache refresh hataları nelerdir?" telemetriden
cevaplandı ("paylaşılan veriler içerisinde cache refresh hatalarına dair bilgi bulunmamaktadır… Kaynak:
açık problemler + triage önceliği + kök-neden hipotezleri (canlı)"); wiki'de başlığı "Cache Refresh
Akışı" olan sayfa vardı. **Kök neden:** `wikiChatAnswer` (chat_wiki_tier.go) wiki işareti olmayan soruda
sessizce çekiliyordu ("nelerdir" işaret değil); guided router (`routeGuidedIntentOpts`) servis adsız +
"hata" sorusunu son dal `case hasErrorSignal(toks)` ile filo geneli `guidedProblems`'e verdi →
`runGuidedRoute` → `guidedProblemsBundle`; wiki hiç aranmadı.
**Karar:** karar KALIP sözcükten değil İÇERİKTEN. İşaretsiz soruda — bağlamsız pencere, servis adı +
telemetri sinyali yok, açık telemetri çapası (zaman penceresi, trace/span/istek/problem kimliği,
sayısal eşik) yok — TEK yerel lexical arama koşar (`wiki.LiveOff`, `NoSemantic`; ADO'ya istek yok,
5 sn tavan). Kapı (`wikiProbeStrong`, saf): en iyi yerel parçanın idf-ağırlıklı terim kapsamı ≥ 0.7
VE eşleşen terimlerden biri başlık/başlık yolunda. Genel telemetri sözcükleri (hata, servis, problem,
yavaş, error, oran, latency…) ve soru niteleyicileri (sık, karşılaşılan, yaygın, bilinen) kapsamaya
sayılmaz (tek liste, `chat_wiki_probe.go`); ayıklama sonrası < 2 içerik terimi kalan soru hiç aramaz.
Kapı geçerse kademenin anlatım yolu aynen (çok-sayfa okuma + wiki_select) ve guided'ın bu soruya
seçeceği rota "Telemetri için:" çipi olarak eklenir (router yalnız; veri okuması/model çağrısı yok).
Geçmezse hiçbir olay yok, akış bayt bayt eski. Ters yön: `guidedProblems` servis adsız soruda sıfır
problem döndürürse (`markFleetZeroMatch`, üretim noktasında işaret) aynı yoklamanın sayfası cevabın
sonuna yalnız bağlantı olarak eklenir ("Wiki'de ilgili olabilir: <başlık>"); yoklama alışveriş başına
en çok bir kez. `wiki.Hit` yerel isabette `TermCoverage` + `HeadMatch` taşır (skor değişmedi).
**Eşikler:** 0.7 — iki terimli soruda iki terim de aynı parçada olmalı (biri ≈0.5); üç terimde eksik
terim ancak belirgin yaygınsa geçer. En az 2 içerik terimi — tek terimde kapsam terim geçtiği an 1.0
olur ("kafka hataları" Kafka sayfasına kaçardı). Ayar/env yok.
**Reddedilen:** "nelerdir", "sık karşılaşılan" gibi kalıp sözcükleri wiki işaretine eklemek — kalıp
telemetri sorusunda da geçer ("açık problemler nelerdir"); karar sayfa içeriğine bağlanmalı. Canlı ADO
yedeği — her işaretsiz soruya dış istek eklerdi. **Testler:** `chat_wiki_probe_test.go` (terim
ayıklama, çapa, kapı tablosu, cache refresh → wiki + çip, kapı geçmezse sessiz, telemetri soruları hiç
aramaz, kapılar, sıfır-eşleşme ipucu, çiplerin rotaya dönüşü, kaynak pinleri),
`internal/wiki/rank_probe_test.go`.

## 2026-10-02 — Log deseni anomalisi: servis adı olmadan da loglara geçiş (v0.10.1062)

**Operatör (prod, ES):** servissiz log deseni anomalisinde "Ne yapabilirim" yalnız "servis adı taşımıyor"
diyordu; loglara Kibana'dan elle gidiliyordu. **Neden servissiz:** ES dedektörü servisi tek alanda
(`fields.service`, varsayılan `service.name`) terms agg ile arar; cluster-logging dokümanında kimlik
`kubernetes.container_name`'de, terms boş döner. CH servisi `service_name` kolonundan alır, pratikte hep
dolu. **Karar:** servissiz `log_pattern`'da kart tek eylem verir: "Logları aç" — olay penceresi + desenin
arama metni (`"t1" OR "t2"`). Metin sunucudan, dedektörün token'larından: `log-pattern-series` cevabına
`logsQuery` eklendi, istemcide token yok. ES'te dedektörün yüklemiyle birebir; CH'de token ön süzgeci
(arama dilinde regex yok — üst küme, desenin her satırı içinde). Aynı ES okumasına servis atfı zinciri
(servis süzgecinin aday alanları, sırayla, ≤5): "En çok: …" satırı. CH'de atıf yok (ikinci regex taraması
olurdu, olay zaten servisli). Dedektör ve kayıt değişmedi; diğer servissiz türlerde cümle aynen.

## 2026-10-02 — Yeni log deseni (log_template_new) anomalileri varsayılan kapalı (v0.10.1061)

Operatör onaylı: "Bu log anomalileri de false pozitif geliyor." Drain'in ilk kez gördüğü log biçimi
(`log_template_new`, `internal/anomaly/log_templates.go`) prod'da yanlış alarm üretiyordu. Öbür log anomalisi
`log_pattern` (seçilmiş desen sıçraması, `log_patterns.go`) operatörce doğru yakalama sayıldı — dokunulmadı.

**Karar:** `anomaly_sensitivity.logTemplateNew` (`*bool`, nil = KAPALI), v0.10.1056 `opLatency`'nin birebir emsali:
aynı blob, aynı yükleme / son-iyi-değeri-koru yolu, Normalize somutlaştırır (nil → false), eski blob kapalı okunur.
Kapalıyken recorder adımı (`recordNewLogTemplates`) dedektörü hiç çağırmaz; dedektör (`DetectNewLogTemplates`) de
her G/Ç'den önce anahtara bakıp boş liste döner — aday / bilinen-şablon okuması ve upsert yok. Dedektör
`log_templates` defterini yalnız OKUR; defteri templater puller'ı yazar ve anahtardan bağımsız sürer (Logs
"patterns" görünümü etkilenmez). Eşikler ve aile süzgeci (v0.10.1030) silinmedi.

**Açık satırlar — göç yok (1056 gibi):** yazılmayan olay 10 dk aktif yaştan sonra düşer; terfi etmiş
`anomaly-auto:` Problem'i `resolveClearedAnomalyPromotions` ile "anomaly cleared" kapanır; satırlar 30 gün TTL ile
tarihte kalır. **Geri açmak:** Settings → Anomaly → "Yeni log deseni anomalileri". Varsayılanı yeniden açmayı önerme.

## 2026-10-02 — Log deseni anomalisi: zaman içinde sayım grafiği (v0.10.1060)

**Operatör:** "Bunu doğru yakalamış ama artışın ne zaman başladığını göstermiyor. Elastic'e gidip bakınca
barlardan net görüyorum." **Karar:** `log_pattern` detayının TEK grafiği artık desenin kendi sayısı — "Desen
sayısı" bar grafiği (CorePanelMulti `bars`, kova genişliği başlıkta, olay penceresi "başladı" bölgesiyle),
"Teknik ayrıntı"nın üstünde, açık. Servisin genel log hacmi bu türde çizilmez ("Logları aç" duruyor); yeni
log biçimi / Elastic ML log hacmini korur (zaman kovalı sayımları yok). **Okuma:** var olanlar ifade
edemiyordu (`/api/logs/timeseries` serbest metin; CH'de dedektörün regex'i değil), yeni
`GET /api/anomalies/log-pattern-series` desen ADINI dedektörün tanımına çevirir ve `logstore.PatternHistogram`
ile sayar — CountPatterns'ın yüklemi, CH + ES. Sınırlar: pencere başlangıç − 1 sa'ten (ya da olay
penceresinden), kova basamaklı ve ≤120, pencere ≤7 gün, 60 s önbellek; yalnız sayfa açıkken, aktif olayda
60 s yoklama. Sayım TÜM servislerin (dedektörün oranı da öyle). Terfi Problem'i (`anomaly-auto:`) detayına
taşınmadı: kaynak olayı ayrıca okumak gerekir, basit yeniden kullanım değil.

## 2026-10-02 — Operations: trend üzerine gelince tek, okunur ipucu (v0.10.1059)

**Operatör (prod, servis → Operations):** "Operations sayfasında bir servisin herhangi birinin üzerine gelince bir şey
çıkıyor ama anlaşılmıyor." Trend hücresinde iki şey açılıyordu: TrendSpark'ın hücre İÇİNDE absolute çizilen kova okuması
(`tbody td { overflow: hidden }` onu yarıdan kesiyordu, "kova 16/30" — saat yok) ve düğmenin `title`ı (pencere toplamları,
başka şey). Karar: tek okuma, `SparkReadout` — body portalı + `position: fixed`, grafik ipucu şablonu `.ov-tt`, yerleşim
`tipPlacement.placeTip` (üstte, sığmazsa alta; yatayda kıstırma); kovanın saat penceresi + calls · errors · p99 + soluk
"tıkla: grafik". `title` kalktı (ad aria-label'da), "All" satırı Sparkline `readout` ile aynı okumayı alır. Klasik düzen
aynen: kolonlar, çizim ve tık davranışı değişmedi; durum hover edilen hücrede yerel, tablo yeniden çizilmez.

## 2026-10-02 — Statement detail: trend grafikleri standart zaman grafiğine geçti (v0.10.1058)

**Operatör:** "Statement detail grafikleri de çok kötü, Coremetry geneline uymuyor. Ayrıca zaman yok vs., hiç olmamış."
Trend bölümü üç Sparkline şeridiydi: x = kova sırası (ipucu "bucket 17/37"), değer ekseni ve birim yok. Artık
Databases detayın düzeni: `ov-charts-3` ızgarasında üç CorePanelMulti (Calls / s reqps · Errors / <kova> adet ·
P95 latency ms), tek crosshair senkron grubu, brush sayfanın `?range=`'ini yazar. Zaman damgası backend'de zaten
vardı (`trend[].tsNs` = kova başı); `densifyTrend` onu atıyordu — yerine `stmtTrend.ts` (yarı-açık kova, oran böleni
kovanın MV kapsamı, çağrısız kovada P95 boşluk). Backend değişmedi. "vs prior" grafiğe hayalet çizmez: uç önceki
pencerenin kova serisini döndürmüyor; fark özet karolarında kalır ve başlık bunu söyler.

## 2026-10-02 — GitOps sekmesi: sütun genişletme çalışır, ayrı "İş yükleri" bloğu kaldırıldı (v0.10.1057)

**Operatör:** "GitOps sekmesinde de sütun başlıkları kaymıyor." + "İş yükleri ayrıca yazmasına gerek yok."
**Kök (paylaşılan primitif):** `DataTableColgroup`ın sığdırması (`fitColumnWidths`) SÜRÜKLENEN kolonu da küçültüyordu:
kabı aşan tabloda (Argo: 1650px beyan, 1178px kap) +100px sürükleme ~+22px, tabanlar bile sığmayınca (838px kap) sıfır
etki — genişlik yazılıyor, ekrana yansımıyordu. Tutamak dışında biten sürükleme de başlık tıkı olup sıralamayı çeviriyordu.
**Çare:** sürüklenen kolon `pinned` (küçültülmez; tabanda tablo taşar, kap kaydırır), sürükleme çizilen genişlikten başlar,
sürükleme sonrası tık yutulur; Autosync tabanı 76 → 90. Başlık/gövde hizası zaten doğruydu (col = th = td). Rozet bloğu
gitti; veri tablolar için gerekli (istek durur), eşlenemeyen cluster notu Argo bölümünde. **Kapsam:** `DataTableColgroup`
kullanan her tablo; tarayıcıda GitOps'un iki tablosu + SlowQueries, pin `resizeFit.contract.test.tsx`.

## 2026-10-02 — Operasyon gecikmesi (trace_op_latency) anomalileri varsayılan kapalı (v0.10.1056)

**Operatör (prod, "Operasyon gecikmesi" anomali detayı, iki ekran görüntüsü):** "Trace op latency false pozitif
geliyor, gerek yok gelmelerine bence." İki örnek de normalde milisaniyenin altında / ~20 ms p99'lu bir operasyon; tek
kovalık 200 ms ve 350 ms sıçramalar "gecikme normalin 3.6 / 4 katına çıktı" diye üçüncü kez yineleniyordu.

**Neden gürültü:** dedektör (`internal/anomaly/op_latency.go`, recorder'dan 60 sn'de bir) TEK 5 dk kovanın p99'unu 24 sa
p99'una karşı koyar; eşikler ×3, ≥ 200 ms, iki pencerede ≥ 30 çağrı. Düşük gecikmeli bir operasyonda 30–100 çağrılık
kovanın p99'u fiilen en yavaş bir-iki çağrıdır: tek yavaş çağrı (GC duraklaması, soğuk bağlantı) ×3'ü ve 200 ms'yi kendi
başına geçer. Dwell yok — tek kova olay açar.

**Karar:** `anomaly_sensitivity.opLatency` (`*bool`, nil = KAPALI) — bilinçli varsayılan davranış değişikliği, emsaller
`service_silent` (v0.10.543) ve `self-disk-eta` (v0.10.1031). Eski her blob kapalı okunur; Normalize somutlaştırır
(nil → false), açıkça true tur atar. Kapalıyken recorder adımı (`recordOpLatency`) dedektörü HİÇ çağırmaz; dedektörün
kendisi de (`DetectOpLatencyAnomalies`) her G/Ç'den önce anahtara bakar ve boş liste döner (hata değil) — MV sorgusu,
v0.10.1046 batch kapısının aktif-olay okuması, örnek-trace sorgusu ve upsert yok. Bugün tek çağıran recorder; kapı
dedektörde olduğu için ileride eklenecek çağıranlar da uyar. Açıkken gövde bayt bayt aynı (batch kapısı dahil).
Dedektör kodu, eşikleri ve v0.10.1046 batch kapısı SİLİNMEDİ — kapı yalnız dedektör açıkken anlam taşır.

**Açık satırlar — göç yok:** yazılmayan olay `last_seen`'den 10 dk sonra (`anomalyActiveAge`) aktif görünümden düşer
(yeni sürümün ilk tikinden en geç ~10 dk sonra); ondan terfi etmiş `anomaly-auto:` Problem'ini evaluator'ın
`resolveClearedAnomalyPromotions`'ı bir sonraki tikte "anomaly cleared" ile kapatır. Satırlar 30 günlük TTL ile tarihte
kalır.

**Etkilenmeyenler:** servis düzeyi p99 anomalileri (metrik dedektörü `anomaly:<svc>:p99_ms`, davranış motoru
`behavior_change`), `trace_op` hata anomalileri (`error_spike` / `new_error`), log desen anomalileri.

**Geri açmak:** Settings → Anomaly → Dedektör hassasiyeti → "Operasyon gecikmesi anomalileri" (kayıt açık boolean
gönderir). Yeniden varsayılan açık yapmayı önerme; gürültünün çaresi dwell / mutlak fark tabanı olur, varsayılanı
çevirmek değil.

## 2026-10-02 — Problems: deploy çipi ipucu ve terfi Problem'inde ANOMALY rozeti (v0.10.1055)

**Operatör onayı:** "Okdir". **(1)** Problems kuyruğunda deploy çipinin ipucu "undefined v…" başlıyordu:
`InboxItem.recentDeploy` tipinde `service` vardı, sunucu `chstore.RecentDeploy` (version / timeUnixNs / ageSeconds)
gönderiyor. Tip ortak `PriorDeploy` şekline indi, ipucu servisi satırın `service`'inden alır; başka okuyucu yoktu.
**(2)** ANOMALY rozeti iki yüzeyde `startsWith('anomaly:')` idi, terfi öneki `anomaly-auto:` onu ıskalıyordu
(backend'in v0.10.814 bildirim türü hatasının ikizi). Tek yüklem `isAnomalyProblem` (`lib/problemSubject.ts`)
`ProblemNotifyKind`'in anomali motoru öneklerini aynalar: `anomaly:`, `anomaly-cluster:`, `anomaly-auto:`. "Olağan
değer" kelimesi ve detaydaki Description gizlemesi rozete BAĞLANMADI (`isAnomalyDetectorRule`, yalnız `anomaly:`):
terfi/küme Problem'inde threshold gerçek kapı, açıklama sayfadaki tek insan-okur metin — kaybolmaz.

## 2026-10-02 — Anomaliden terfi eden Problem de yinelenen kuralına uyar (v0.10.1054)

**Operatör:** "Anomaliden terfi eden problem de 'yinelenen' kuralına uysun; bugün deploy'a hâlâ eski kurala göre
bağlanıyor." **Önce:** v0.10.1049 deploy atfının tek kuralını (`AnomalyPredatesDeploy`) yalnız anomali olayına bağladı
(bilinen sınır diye kuyruğa yazıldı). Güçlü olaydan terfi eden Problem (`anomaly-auto:<fp>`) bölüm sayacını bilmiyordu:
Problems kuyruğunda anomali satırı "yinelenen" der ve deploy çipi göstermezken hemen yanındaki terfi Problem'i aynı
deploy'u çip, detay sayfasının deploy kutusu, kök-neden adayı (0.80 taban) ve deploy raporu satırıyla "olası neden"
gösteriyordu.

**Eşleme (sorgusuz):** saklanan rule_id = `anomaly-auto:` + `FingerprintAnomaly` (16 küçük harf hex) = olay kimliği;
Problem kimliği aynısı + `:<servis>`. `chstore.PromotedAnomalyEventID` ikisini de kabul eder; önek ya da şekil tutmazsa
terfi Problem'i sayılmaz. Bölüm eşleşmesi: terfi Problem'inin `StartedAt`'i olayın bölüm başlangıcıdır ve bölüm içinde
değişmez; olay o zamandan beri yeni bölüme geçtiyse (eski, kapanmış terfi) eşleşme yok.

**Okuma:** `Store.PromotedAnomalySources` — çağrı (sayfa, rapor, tik) başına TEK toplu okuma: `SELECT id, started_at,
last_seen, episode_count, first_started_at FROM anomaly_events FINAL WHERE id IN (…) LIMIT 2n SETTINGS
max_execution_time = 2` (yalnız kullanılan beş kolon, PK, bind değer listesi, en çok 1000 tekil kimlik; sıcak okuma
yolunda 2 sn tavan). Terfi Problem'i yoksa ya da bölüm kolonları bu süreçte görülmediyse okuma YOK. Satır başına okuma
yok.

**Tüketiciler — yeni mekanizma yok, 1049'unkiler:** (1) **`EnrichProblemsWithDeploys`** (her problem okuma zinciri
buradan: Problems kuyruğu, alarm detayı, /rootcause ucu, Insight kartı, açıklama/runbook istemleri, vardiya, sohbet ve
MCP listeleri): eşleşen terfi Problem'ine olayın sayacı (> 1) ve ilk başlangıcı iliştirilir (`episodeCount` /
`firstStartedAt`, saklanmaz); seçim `pickProblemDeploy` → `pickAnomalyDeploy` — kural true ise deploy "olası neden"
(`recentDeploy`) olarak iliştirilmez. Ekran: kuyruk satırı (birleşik akış + Alert rules tablosu) anomali satırıyla aynı
nötr `RecurringMarker`; alarm detayının "ne zaman" satırına anomaliyle aynı tek ek (`problemWhenLine`). Yeni bölüm yok.
İşaret anomali satırının kuralını izler (sayaç > 1): vaka A'da iki satırda da çip + işaret. (2) **Kök-neden işçisi,
PROBLEM çıpası:** tik başına tek okuma; `promotedDeployRecurrence` anomali çıpasıyla ORTAK `deployRecurrence`'ı çağırır
→ `DeployRecurring`: aday `recurringDeployScore`'a iner, düz gerekçe, ölçülen gerilemede geri; aynı imajın rollout
kaydı yükseltmez (mevcut dal). (3) **Deploy raporu + rollout çekmecesi:** tek seçim `problemsSinceDeploy`; dahil etme
kapısı aynen `StartedAt >= since`, HİÇBİR satır düşmez, satır servisi nitelemeye devam eder; kural o deploy'a göre true
ise `predatesDeploy` ve çekmecede aynı "yinelenen" işareti. (4) **Arka plan ProblemExplainer:** tik başına tek okuma
(yalnız aday varken); kanıt paketinin DEPLOY satırı kural true ve işçi deploy'u geri ALMADIYSA nötr: "DEPLOY <servis>
<sürüm> … — yinelenen anomali, deploy'dan önce de görülüyordu; ana şüpheli değil" (sayfadaki hipotez bloğuyla çelişmez).

**Hiçbir şey kaybolmaz (inceleme):** kural deploy'u "olası neden" saymadığında deploy ATILMAZ: aynı seçici
(`pickAnomalyDeploy`, problem ve anomali yolu) onu ayrı, saklanmayan `priorDeploy` alanına koyar (`recentDeploy` ile aynı
şekil; `recentDeploy` anlamını korur: olası neden — istemler, Insight kartı ve /rootcause tüketicileri değişmez). Renksiz,
yeni bölümsüz gösterilir: satır işaretinin ipucunda "deploy <sürüm> <N> dk önce — öncesinde de görülüyordu"; alarm ve
anomali detayının "ne zaman" ekinde "… · yinelenen · bu N. kez · ilk kez <tarih> · deploy <sürüm> <N> dk önce (öncesinde
de görülüyordu)"; alarm detayının zaman çizelgesinde nötr (warn değil) "Deploy <sürüm>" satırı. **Ölçülen gerileme:**
`chstore.RestoreMeasuredDeploy` — `priorDeploy` doluyken (kural o satırda gerçekten bir deploy bastırdı) ve kök-neden
işçisinin hipotezi deploy'u ölçülen gerilemeyle geri aldıysa (hipotezin `RecentDeploy`'u dolu) o deploy yeniden
`recentDeploy` olur, `priorDeploy` boşalır. Uygulandığı yerler: problem ve anomali kök-neden özeti
(`Enrich*WithRootCause` — aynı toplu hipotez okuması, ek sorgu yok; liste, by-id, sohbet ve MCP yolları) ve /rootcause
uçları (problem + anomali, paralel okumalardan sonra; anomali ucundaki boşluk v0.10.1049'dan beri vardı). `priorDeploy`
boşsa yanıt bayt bayt bugünkü. **Warning terfi Problem'i:** kök-neden işçisi yalnız critical açık problemleri çıpa alır,
yani warning'de hipotez yoktur — operatör çip görmez, yalnız nötr deploy metnini (ipucu, ne zaman eki, zaman çizelgesi)
ve "yinelenen" işaretini görür; ölçülen-gerileme geri alımı o satırda çalışmaz. Problems kuyruğunun anomali satırı
bugün de deploy okuması yapmaz (çip yok), ipucunda deploy metni yoktur; /anomalies satırı ve anomali detayı taşır.

**Hata yönü:** okuma hatası, TTL ile düşmüş olay, başka bölüm, kolon yok, tavan aşımı → iliştirme yok, atıf BUGÜNKÜ gibi
(okunamayan süzgeç süzmez): yanlış "yinelenen" gerçek bir gerilemeyi gizler, eksik işaret yalnız bugünkü atfı bırakır.

**Değişmeyen:** diğer her Problem'in (alarm kuralı, exception, `anomaly:` metrik dedektörü, küme) deploy seçimi eski
döngüyle bayt bayt aynı (tablo testi eski döngüyü referans tutar), `priorDeploy` hep boş, teli aynı (omitempty); öncelik
/ şiddet, terfi kapısı, bölüm kuralı; rollout listesinin "problemler" rozeti (çekmeceyle aynı sayım, gizleme yok).
**Bilinen sınırlar (kuyrukta):** açıklama / runbook istemleri (`api.go` explain-problem / runbook) ve MCP problem haritası
bu Problem'lerde "Recent deploy" satırını kaybeder, yerine yinelenme satırı gelmez (hipotez bloğu indirgenmiş gerekçeyi
taşır); ertelenen ALTER'dan önce açılmış pod'lar bölüm kolonlarını yeniden probe / restart'a dek görmez (1049'la aynı —
o pod'da atıf bugünkü gibi); kapanmış eski terfi Problem'i, olay yeni bölüme geçince bugünkü atfa döner; derin kanıt
kapısı (`shouldDeepInvestigate(…, bundle.Deploy != nil)`) kurala bakmaz; problem çıpasında imaj etiketi deploy sürümüyle
eşleşmeyen rollout adayı ayrı aday olarak kalır.

## 2026-10-02 — Exception takip sohbeti de kaynak kodu okuyabilir (v0.10.1053)

**Operatör:** "Exception panelindeki takip sohbeti de kod okuyabilsin." **Önce:** `read_source_code` (v0.10.1050) yalnız
trace/span öznesinin panel takibinde (serbest döngü) sunuluyordu. Exception'ın "Explain"inden açılan panelde takip
`drawerTraceFollowUp`'a girmez, çekmece anlatımına düşer (`copilotChatDrawer`): `SystemPromptDrawerChat` + açıklama
(≤3000 rune) + HAM KANIT (`BuildExceptionExplainInput`: grup meta, trend, temsilî stack, örnek trace, loglar, deploy, pod;
≤6000 rune) + son 6 tur, araçsız TEK akan çağrı. Kod okuma yolu yoktu.

**Karar — tek araçlı döngü (`api/chat_exception_followup.go`):** araç bu çağırana sunulabiliyorsa — sohbetin kendi
kapıları: DevOps bağlı (`ChatToolList`), aracın kendi MinRole'ü (`toolsForRole`; `SourceCodeMinRole`, viewer düzeyi),
oturum kullanıcısı (`sourceCodeToolsFor`; token ve kimliksiz çağrı rolden bağımsız dışarıda) — exception takibi AYNI
çekmece prompt'u ve kullanıcı bloğuyla koşar; önüne yalnız `SourceCodeChatAddendum` gelir (çatal yok, DataNotInstruction
sonda) ve modele YALNIZ `read_source_code` sunulur. Döngü serbest döngünün parçalarıyla kurulur: 5 tur / 6 çağrı,
Executor (tekrar muhafızı, 20 sn, audit `mcp.tool.call`, `ai.tool` span'ı), step/step-result, aynı maskeler (önizleme
yalnız referans, ai_calls `[kod: …]`), tavan turu; taşmada ilk blok (soru) korunur. Bütçe çipi tek araca göre: "kod okuma:
en çok 6 dosya penceresi". Dış MCP araçları ve öteki yerli araçlar YOK (sunulmayan bir ad — ör. `get_trace` — bilinmeyen
araç olarak reddedilir, yürütülmez). **Neden tam döngü değil:** küçük modelde otuzu aşkın şema "schema soup"
(v0.10.172/194); exception'ın kanıtı zaten prompt'ta, eksik olan yalnız kod. Cevap sözleşmesi çekmeceninki: künye aynen (+
veri döndürdüyse "read_source_code (kaynak kod)"); ai_calls yüzeyi `chat-drawer`, döngü başına tek satır.

**Sayı denetimi YOK:** çekmece anlatımında hiç yoktu. Trace takibindeki gerekçe ("ilk cevap zaten denetlendi") exception
açıklamasına uymaz; açıklamadaki bir sayıyı ya da ekin istediği gibi aynen aktarılan kod sabitini / satır numarasını
tekrarlayan takip, yalnız DevOps'lu kurulumlarda yanlış "kanıtta bulunamayan sayı" kuyruğu alırdı
(`TestExceptionFollowUpNoNumericTail`).

**Düşüş (döngü cevap üretemezse):** cevap çıkmadı, istek bağlamı canlı ve arıza sağlayıcıdan — araç tanımını reddeden uç
(araç desteksiz yerel modeller 400 döner), küçültülemeyen bağlam taşması (tek araç turunda küçültülecek çift yok), tavan
turu hatası. Hata olayı YAYINLANMAZ; operatör tek düz çip görür ("kod okuma kullanılamadı — araçsız yanıt") ve bugünkü tek
akan anlatım aynen koşar (aynı sistem/kullanıcı metni). Döngünün ai_calls satırı `status=error` kalır (/ai arızayı görür),
anlatım kendi satırını yazar. İstemci iptali ve alışveriş tavanı düşmez: hata yayınlanır, ikinci çağrı yakılmaz
(`TestExceptionFollowUpFallsBackToNarration`).

**Kapsam (trace kuralının yerine):** servis YALNIZ exception'ın kendi kod incelemesinin okuyacağı servisler — grubun servisi
ve explain'in stack'ini basan servis (`ExceptionExplainInput.CodeService`: log-fallback'te logu atan servis; kural artık
tek yerde, "Kodu da incele" de oradan okur). Örnek trace'teki öteki servisler kapsam DIŞI. Kapsam dışı servis argüman
hatası (izinli servisler anılır), git'e istek yok. **Döngüye yalnız grup yüklendiyse girilir** (`exScope.loaded`); grup
okunamadıysa takip bugünkü anlatımdır. Okuyucu yine de `exception_unreadable` "exception okunamadı" ıskasıyla savunur, git'e
istek yok. **Sürüm:** stack'i veren olayın çalışan sürümü (`StackVersion`, v0.10.1044) ve YALNIZ o servis için; stack'i
basmayan grup servisi dal ucundan. Model seçemez.

**Sunulamıyorsa bayt bayt eski:** DevOps yok / yapılandırılmamış, API token'ı, kimliksiz çağıran, aracın rol kapısı altı →
aynı tek AKAN çağrı, aynı sistem ve kullanıcı metni, aynı olay dizisi (golden: `TestExceptionFollowUpFallbackByteIdentical`).
Trace/span takibi, bağımsız sohbet (araç yine yok) ve MCP sunucusu (araç yine yok) değişmedi. **Araç açıklaması
özne-nötr:** kısa açıklama "konuşmanın konusundaki servis (trace'teki servisler ya da exception'ın servisi)" der — model
kapsam dışı bir servise çağrı harcamasın; bu, trace takibinin araç tanımı baytlarını da değiştirir (kısa açıklama 271 → 321
B, araç başına tur bedeli 901 → 1003 B; tavanlar 330 / 1050 B).

**DevOps'lu kurulumda KOD SORMAYAN exception takibi için görünür farklar:** cevap akmaz, tek blok hâlinde gelir (döngü
tamponlu, trace takibiyle aynı); model gerekmediği hâlde bir dosya okuyabilir (bir tur + git isteği) — ölçülecek; her tur
ek ~1,4 KB (şema + kısa açıklama + prompt eki) ve çekmece bloğu (≤~9 K rune) her turda yeniden gider. Araç desteksiz
modelde fark düz çip + araçsız cevaptır.

**Maliyet:** ek CH/ES okuması YOK — kapsam ve sürüm çekmecenin her takipte zaten kurduğu explain girdisinden
(`GetExceptionGroup` + `BuildExceptionExplainInput`, alışveriş başına bir kez; testte sayılır); git isteği yalnız model
aracı çağırınca (v0.10.1050 bütçesi). Yüzey→profil eşlemesi `chat-drawer` profilini kullanır.

**Geçersiz kılınanlar:** v0.10.1050'nin "Exception öznesinin panel takibi bugün çekmece anlatımına gider (araçsız) — araç
orada yok" kalıntısı ve v0.10.1052 (viewer rolü) kaydındaki "yalnız panel trace takibi" ifadesi: araç artık exception
takibinde de sunulur. Rol kapısı aracın kendi MinRole'ü olduğundan viewer'lar exception takibinde de kod okutabilir.

**Kalıntılar:** log-fallback servisi telemetriden gelir (trace kapsamıyla ve "Kodu da incele" ile aynı güven düzeyi).
Örnekler her takipte yeniden okunduğu için kapsam, açıklamanın okuduğu örnekten kayabilir.

## 2026-10-02 — Sohbet kod okuma aracı viewer'lara da açık (v0.10.1052)

**Operatör:** "Kod okuma aracı viewer'lara da açılsın (bugün editor ve admin)." **Değişen YALNIZ rol:**
`mcptools.SourceCodeMinRole` "editor" → "" (diğer salt-okunur tool'larla aynı viewer tabanı; aşağıdaki v0.10.1050 madde 9'un
"operatörün değiştirebileceği varsayılan"ı). "Hepsi viewer" duruşunda (`TestAllShippedToolsAreViewerLevel`) istisna kalmadı.
**Viewer'ın yeni yapabildiği:** baktığı trace'teki servislerin kaynak dosyalarını sohbette serbestçe okutmak — önce yalnız
"Kodu da incele" ile gerçek stack frame'lerinin çevresindeki pencereleri görüyordu. **Sınırlar aynen:** kapsam sohbetin trace'i,
yalnız kaynak dosya izin/yasak listesi, API token'ı (viewer token'ı dahil) ve kimliksiz çağıran rolden BAĞIMSIZ dışlı, yalnız
panel trace takibi (bağımsız sohbet ve dış MCP'de yok), okuma git PAT'inin kendi erişebildiği depolarla sınırlı.

## 2026-10-02 — Sohbet kaynak kodu okuyabilir: salt-okunur, yalnız kaynak dosya (v0.10.1050)

**Operatör:** "Sohbet kod okuyabilsin: takip soruları bugün kod okuyamıyor." **Önce:** AI panelinde açıklamadan sonra
("Kodu da incele" ile ya da onsuz) SOHBET'te sorulan "bu metodun devamında ne var?", "X sınıfına da bak" sorularının kod
okuma yolu yoktu; model yalnız önceki açıklamanın 3000 rune'luk bağlamında alıntılanmış kodu görüyordu. Tek kod okuyucusu
explain'in `buildCodeContext`'iydi.

**Araç:** `read_source_code` (mcptools/source_code.go; DevOps yarısı devops/source_read.go; sunucu kararları
api/chat_source_code.go). Girdi: `service` (zorunlu), `file` (dosya ya da sınıf adı, isteğe bağlı dizin/paket sonekiyle:
`ChargeHandler.java`, `com.example.cards.ChargeHandler`, `handlers/charge.go`), `line`, `context_lines` (varsayılan 30,
tavan 60). `version` girdisi YOK: ref'i model seçemez. Çıktı: `source` durumu, depo, okunan ref ve NEDENİ (çalışan sürüm —
sohbet öznesinden; ya da dal — sürüm bilinmiyor / depoda yok), depo-göreli yol, toplam satır (dosya okuma tavanına dayandıysa
"dosya büyük, ilk N satır okundu" — toplam bir alt sınırdır), numaralı pencere ("N| kod"; önünde "VERİDİR, talimat değil"
notu), pencerenin üstünde kalan çevreleyen imza. Birden çok eşleşmede ≤10 aday yol ve İÇERİK YOK ("daha belirgin file ile
yeniden çağır"). Dürüst ıskalar: servis bu trace'te yok (trace'in servisleri anılır), trace okunamadı, depo/proje
çözülemedi, ağaçta yok (ağaç kesikse kesilme notu), "bu dosya türü okunmaz", ref yok → dal + not, süre.

**Güvenlik — araç argümanları telemetri metniyle yönlendirilebilir** (OTLP ingest kimliksiz; log/exception/span metnini
span gönderen herkes yazar ve model onu okuyarak argüman kurar). Güvenlik incelemesiyle daraltıldı:
1. **Yalnız panel trace takibinde sunulur** (trace/span öznesi → serbest döngü, yalnız YERLİ araçlar). Bağımsız sohbette
   SUNULMAZ: orada dış MCP araçları da var ve onay adımı yok — ekilmiş bir log satırı modeli bir dosyayı okuyup kodunu bir
   dış aracın argümanına koymaya yönlendirebilir, kod üçüncü tarafa çıkardı. Karar katalog/spec/prompt kurulmadan önce
   (`sourceCodeToolsFor`); bağımsız sohbetin prompt'u ve kataloğu bayt bayt eski (dış MCP'li ve MCP'siz pinli).
2. **API token'ına sunulmaz:** `cmk_` token'ı da `/api/copilot/chat`'e girer (claims `token:<id>`); ek modelden AYNEN
   alıntı istediği için "MCP sunucusunda yok" sözleşmesi token'la dolanılırdı.
3. **Kapsam = sohbetin trace'i:** `service` öznenin trace'indeki servislerden biri olmalı (öznenin span'leri alışveriş başına
   BİR kez okunur — sürüm türetimiyle aynı okuma; okunamazsa "trace okunamadı" ıskası, git'e istek yok). Önceki "servis
   Coremetry'de biliniyor" kataloğu denetimi SINIR DEĞİLDİ ve kaldırıldı: telemetri gönderen herkes bir servis adı yaratabilir
   ve ad konvansiyonu onu projedeki HERHANGİ bir depoya çevirirdi; "operatörün baktığı trace'in parçası" sınırdır. Servis
   adı biçimi (`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`) istekten önce denetlenir.
4. Depo YALNIZ servis → depo çözümünden (katalog pini / ad konvansiyonu; explain'in `pinReadDecision` + `ResolveRepo`'su).
   Model depo/proje/ref/URL/sürüm adlandıramaz: şema `additionalProperties:false`, bilinmeyen alan (version dahil)
   reddedilir; organizasyon araması (CodeSearch) bu yolda HİÇ koşmaz.
5. Dosya YALNIZ depo AĞACINDAKİ bir yolla eşleşerek seçilir (ad + tam parça soneki, BestPathForFrame ailesi; harf-duyarsız,
   tek harf-duyarlı eşleşme kazanır); ağacın listelemediği yol istenmez. `..`, mutlak yol, joker, ters bölü, kontrol
   karakteri ve 200 bayt üstü İSTEKTEN ÖNCE reddedilir.
6. Yalnız kaynak dosya: izin listesi .java .kt .kts .scala .groovy .cs .go .py .js .jsx .ts .tsx .rb .php; `.sql` YALNIZ
   `mapper/` ya da `mappers/` dizininde (dökümler, seed'ler, migration'lar okunmaz — explain'in SQL avının dizin listesi
   olmadığı için mapper dizini seçildi); `.xml` YALNIZ adı `…Mapper.xml` (mybatis-config.xml değil). Yasak liste izni EZER:
   yapılandırma/anahtar uzantıları (properties, yml/yaml, json, toml, ini, conf, pem, key, pfx, jks…), `.env*`, yolda
   secret/credential/password/passwd/privatekey/keystore, adda apikey/api-key/api_key/datasource, `settings` / `config` /
   `conf` / `appsettings` / `wp-config` / `localsettings` / `environment` / `ormconfig` / `knexfile` adlı dosyalar,
   `*.config.*`, gradle; dizin kuralları: `.py` için `settings/`, `.js/.jsx/.ts/.tsx/.rb/.php` için `config/`,
   `environments/`, `initializers/`. Reddedilen dosyanın içeriği istenmez. Her kural tablo satırıyla pinli
   (`TestClassifySourcePathReviewAdditions`; kural kaldırılınca satırı kırmızı — mutasyonla doğrulandı).
7. Sınırlı çıktı: merkezin iki yanında ≤60 satır, satır ≤400 rune, zarf ≤5.800 rune (sohbetin 6.000'lik araç sonucu
   kırpmasının altında; aşarsa hedef satır merkezde kalarak daraltılır ve söylenir).
8. "Kod tarayıcıya gitmez" modelin kendi cevabı DIŞINDA aynen: step-result önizlemesi YALNIZ referans (`yol:aralık · ref`,
   beyaz listeli görünüm — kod alanını hiç okumaz), kaynak rozeti referans/gerekçe; ai_calls örneğine maskeli
   "[kod: depo/yol:aralık · N satır · ref]" (explain'in sözleşmesi); audit (argüman + bayt) ve span'ler (ad/bayt) zaten
   içeriksiz; takip cevabının sayı denetimi kanıtına YALNIZ referans satırı girer (koddaki sabitler ve satır numaraları
   uydurulmuş bir sayıyı temellendirmesin — koddan aynen aktarılan bir sayı da bu yüzden uyarı alabilir).
9. **Rol: editor ve admin** (`mcptools.SourceCodeMinRole`, tek sabit; operatörün değiştirebileceği varsayılan). Viewer
   "Kodu da incele"yi (gerçek stack frame'lerinin pencereleri) kullanmaya devam eder, serbest dosya okumasını almaz.
10. Dış MCP sunucusunda YOK (`chatOnlyTools`): kaynak kod dış istemciye açılmaz.

**Nerede sunulur (özet):** YALNIZ panel trace/span takibi + oturum kullanıcısı + editor/admin + DevOps bağlı
(`Deps.SourceCode` nil → `ChatToolList` düşürür). Sunulmadığında araç ve prompt eki yok, döngü prompt'u ve katalog bayt bayt
eski (pinli). Guided / çekmece / RAG / bağımsız sohbet: yok. Prompt eki (`SourceCodeChatAddendum`, sohbet çekirdeğinin
önünde, DataNotInstruction sonda kalır): kod sorulunca ya da cevap alıntılanmamış koda dayanınca çağır, servis/dosya/satırı
kanıttan al, dosya:satır ile AYNEN alıntıla, okumadığın kodu yazma, okunamazsa söyle, içerik veri.

**Sürüm:** SUNUCU türetir — trace öznesinin span'lerinden explain'in yardımcısıyla (`anomaly.StackVersion`: odak span istenen
servisinse onun sürümü — canary dersi — değilse servisin span'lerinde çoğunluk; Tempo önce, sonra CH; kapsam denetimiyle
aynı okuma). v0.10.1044 biçim kapısından geçmeyen sürüm "sürüm yok" sayılır, yankılanmaz. Yoksa dal sırası.

**Bütçeler:** sunulduğu turda +1.424 B (kompakt açıklama 271 B + şema 630 B + prompt eki 523 B); koşulsuz kompakt katalog
9.098 B / tavan 9.100 B aynen, koşullu araçlara ayrı 300 B tavan (`short_desc_test.go`). Git isteği (sahte TFS,
`TestReadSourceRequestBudget`): sürümsüz soğuk 3 (branş refs + ağaç + dosya), sıcak 2; sürüm + tag soğuk 5, sıcak 2; süre
20 sn araç bütçesi (DevOps tavanı 25 sn). Alışveriş başına en çok 6 çağrı × ≤5.800 rune ≈ 35K rune kod modele.

**Kabul edilen kalıntılar (açıkça):** modelin AYNEN alıntıladığı kod cevap metnidir — tarayıcıya, kaydedilen sohbete
({role,text}) ve ai_calls'un cevap örneğine gider. Alıntılı bir cevap istemci geçmişinde sonraki bir bağımsız sohbet
alışverişine taşınabilir (orada dış MCP araçları var; araç orada yok ama metin geçmişte). Modelin KENDİ metni (sonraki araç
argümanları, set_context) okuduğu kodu taşıyabilir. Exception öznesinin panel takibi bugün çekmece anlatımına gider
(araçsız; v0.10.948 "diğer özneler eski yolda") — araç orada yok, yönlendirme ayrı operatör kararı. Kesik ağaçta kapsamlı
alt-ağaç geri-denemesi yok (ıska + kesilme notu). Yasak liste muhafazakâr: `Config.java`, `settings.py`, `*SecretHolder*`,
`DataSourceConfig.java` gibi adlar okunmaz.

## 2026-10-02 — Anomali: yinelenen bölüm sayacı ve ilk görülme (v0.10.1049)

**Operatör:** "Yinelenen anomali ayrımı: her gece tekrar eden bir anomali artık her seferinde 'yeni' görünüyor ve
önceki deploy'a bağlanıyor. 'Yeni mi, yinelenen mi' ayrımı için küçük bir şema eki gerekir." **Önce:** v0.10.1045'ten
beri 22 dk 30 sn'yi aşan boşluk yeni bölüm açar ve hiçbir şey taşınmaz; satırda "bu daha önce de ateşledi mi"
bilgisi yoktu. Sonuç: gece işi her gece yeni bir `started_at` taşıyor, deploy raporu / rollout çekmecesinin "deploy
sonrası anomaliler" listesine, kök-neden işçisinin deploy şüphelisine (0.80 taban, en güçlü katman) ve satırın
deploy çipine bir önceki deploy'un hanesine yazılıyordu.

**Şema (`anomaly_events`, `migrate()` `alters` dilimi):** `episode_count UInt32 DEFAULT 1`, `first_started_at
DateTime64(9) DEFAULT toDateTime64(0, 9)` — `ADD COLUMN IF NOT EXISTS`, ORDER BY / TTL'e girmez, CREATE TABLE'a
eklenmedi (problems.comparator/kind emsali: taze ve yükseltilmiş kurulumda kolon sırası aynı). DEFAULT'lar
"bilinmiyor = bugünkü davranış": eski satır 1. bölüm, ilk görülme 0 → okuyucu `started_at` sayar.

**Taşıma (`MergeAnomalyCarry`):** ilk görülme → sayaç 1, ilk = gelen `started_at`; aynı bölüm → ikisi saklı satırdan
değişmeden; YENİ bölüm → sayaç = saklı + 1, ilk = saklı ilk (0 ise saklı `started_at`). Bölüm kuralı (boşluk,
sınır, sırası bozuk yazım) v0.10.1045'in aynısı — sırası bozuk yazım bölüm açmadığı için sayacı da artırmaz.

**Ekran (yeni bölüm yok):** anomali detay sayfasının "ne zaman" satırına yalnız sayaç > 1 iken tek ek: "· yinelenen ·
bu N. kez · ilk kez <tarih>" (sayaç ≤ 1 / yok → metin bayt bayt aynı). Problems kuyruğunun anomali satırı ve
`/anomalies` geçmiş satırı (Started hücresi) nötr tek kelime "yinelenen" (`Badge tone="neutral"`, renk yok — renk
yalnız sapan değerde); sayı ve ilk tarih ipucunda. API: `AnomalyEvent.episodeCount/firstStartedAt` ve inbox
`anomaly.episodeCount/firstStartedAt` (omitempty).

**Deploy atfı — tek kural "düzenli yinelenme", `chstore.AnomalyPredatesDeploy(first, started, count, deploy)`:**
`earliest = min(first (> 0 ise), started)`; `earliest ≥ deploy` → false; `started < deploy` → true (bugünkü kural,
tek bölümlü satırda birebir); aksi hâlde YALNIZ `count ≥ 3` VE ortalama aralık `(started − earliest) / (count − 1)
≤ 48 sa` iken true (`anomalyRegularMinEpisodes`, `anomalyRegularMaxMeanGap`). Gece işi üçüncü gecesinden itibaren
"deploy'dan önce de görülüyordu"; **vaka A** (deploy kırdı → 1. bölüm; rollback; "düzeltme" yeniden deploy hâlâ
bozuk → 2. bölüm) ve **vaka B** (20 gün önce tek kıpırtı, bugün deploy gerçekten bozuyor) atfı KORUR. İlk taslak
"herhangi bir eski bölüm"ü yeterli sayıyordu; parmak izi `kind|pattern|service` ve satır 30 günde bir ateşledikçe
yaşadığı için birkaç haftada sık görülen her operasyonun ilk görülmesi her yeni deploy'dan önce düşer ve gerçek bir
deploy gerilemesi rapordan, kök-neden adayından (ölçülen etkisiyle birlikte) ve çipten kaybolurdu (inceleme).
**Kabul edilen bedel:** ortalamaya deploy'dan SONRAKİ bölümler de girer — deploy'dan bir gün önce tek bölüm görülüp
deploy sonrası saatte bir yeniden tetiklenen anomali 3. bölümünde (ortalama ~12 sa) düzenli görünür; o hâlde bile
rapor satırı işaretli KALIR ve kök-neden adayı ölçülen gerilemeyle normal puanını geri alır.

Üç tüketici: (1) **deploy raporu + rollout çekmecesi** `anomaliesSinceDeploy` — seçim bugünküyle aynı
(`StartedAt >= since`), HİÇBİR satır gizlenmez; kural true olan satır `predatesDeploy` taşır ve rollout
çekmecesinin "Aktif anomaliler" listesinde aynı nötr "yinelenen" işaretiyle çizilir (işaretli, gizli değil).
(2) **kök-neden işçisi** `synthInputForAnomaly` — deploy adayı DÜŞMEZ, İNER: `SynthesisInput.DeployRecurring`
doluyken `correlator.Synthesize` adayı `recurringDeployScore` = 0.10'a indirir (eş-ateşleme 0.20, sinyal bandı
≥ 0.30, komşu-sinyal ≥ 0.168 altında; yayılımda yalnız %14'ün altındaki zayıf bir komşu tahmini daha aşağıda),
gerekçe düz: "yinelenen anomali: N. kez, ilk <tarih UTC> — deploy'dan önce de görülüyordu (deploy <sürüm> Nm
önce)", breadth'e sayılmaz, `RecentDeploy` boş (ribbon / istem onu "olası neden" göstermesin). Ölçülen etki adımı
(`enrichDeployImpact`) yine koşar; gerileme gösterirse (p99 ≥ +%20 ya da hata ≥ +1 puan — Insight kartının deploy
kırmızısıyla aynı eşik) aday normal puanını geri alır ve gerekçeye yinelenme notu eklenir. İndirgenen aday aynı
imajın rollout kaydıyla yükselmez. (3) **çip** `EnrichAnomaliesWithDeploys` (`pickAnomalyDeploy`) — yalnız kural
true iken o deploy iliştirilmez (satır çipi, detay deploy kutusu, `/anomalies` çekmecesi, kök-neden ucu aynı
seçimden); aksi hâlde bugünkü gibi.

**Sınırlar:** "yinelenen" = satırın ömrü içinde yeniden tetiklenmiş — satır son bölümün başlangıcından 30 gün
sonra TTL ile düşer, 30 günden uzun sessizlik sayacı 1'e döndürür (her gece ateşleyen iş için satır hiç düşmez,
ilk görülme aylar öncesi olabilir). Eski satırlar 1. bölüm ve ilk görülme bilinmiyor diye başlar; ilk yeni
bölümde ilk = o anki saklı `started_at`. Bölüm başına geçmiş YOK (yalnız sayaç + ilk başlangıç). Kapsam dışı:
yinelenen anomaliye öncelik/terfi farkı ve anomali AI açıklama isteminin deploy listesi
(`PickDeploysAroundStart`, `started_at`'e bakar). **Bilinen sınır (kuyrukta):** anomaliden terfi etmiş Problem'ler
(`anomaly-auto:`) deploy'u hâlâ bölüm başlangıcına göre bağlar (problem çıpası, problem deploy zenginleştirmesi,
deploy raporunun problem kapısı) — problem satırı deploy çipi gösterirken anomali satırı "yinelenen" diyebilir.

**Rolling deploy:** kolon ekleme düşük hacimli state tablosu sınıfı; probe `hasAnomalyEpisodeCols` (`atomic.Bool`,
system.columns metadata, iki kolon tek sorguda — ai_calls emsali). Küme kipinde kolonu EKLEYEN boot DDL'i
ertelediği için false okur: taşıma okuması, INSERT ve okumalar iki kolonu atlar (yinelenme işareti yok, deploy
atfı `started_at` ile — bugünkü davranış). **Probe'u false olan pod'un her yazımı sayacı ve ilk görülmeyi
DEFAULT'a (1 / 0) geri yazar** — sayaç birikimli olduğu için bu bir sıfırlamadır; ertelenen DDL indikten sonra
`reprobePromotedAttrs` (ddl_defer.go → `reprobeAnomalyEpisodeCols`; 0 / 1 / 5 / 15 dk denemeleri) bayrağı restart
beklemeden çevirir, geçiş bir kez loglanır, sıfırlama penceresi o ana dek sürer. Her çağrı bayrağı başında bir kez
okur (yarım kolonlu sorgu yok). ESKİ binary açık kolon listeleriyle yazıp okur (`SELECT *` / çıplak INSERT yok) →
hata yok, ama yazdığı her satırda sayaç 1'e, ilk görülme 0'a döner (tam-satır replace); yön güvenli (bugünkü
atıf). Gerçek ClickHouse gidiş-dönüşü (UInt32 ↔ uint32, DateTime64(9) ns) `chsmoke_test.go`
`TestCHSmokeAnomalyEpisodeRoundTrip`'te.

## 2026-10-02 — Exception sayfası dosya bağlantıları çalışan sürüme gider (v0.10.1048)

**Operatör:** "Exception sayfasındaki dosya bağlantıları hâlâ daldan açılıyor; kod incelemesi artık sürümden okuyor. İkisi
aynı yere baksın." (v0.10.1044 "Bilinen sınırlar" maddesi.) **Kural:** frame linkleri stack'i GÖSTERİLEN örneğin kendi sürümünü
yollar (`representativeStack`: stack ve sürüm aynı örnekten, en yeninin sürümü eski bir stack'e yamanmaz). Sürüm sunucuda: örnek
sorgusu aynı satırlardan üç resource değeri daha okur (`exSampleVersionCols`, ikinci sorgu yok), `runningVersion` alanını
`devops.RunningVersion` doldurur — kod incelemesinin `exceptionStackVersion → SpanVersion` yardımcısı; anahtarlar kaynaktan pinli.
Sürüm yok → gövde bayt bayt eski, dal ucu. Kart alt yazısına ek: "· 1.4.2 (çalışan sürüm)" / "· release (dal)" (`codeSourceRef`,
yalnız link varken). `useStackFrameLinks` keepPreviousData'dan çıktı: temsilî örnek değişince eski linkler yeni satırlara yapışmaz
(aynı kanca, span çekmecesi de). **Sınır:** stack'li örnek ilk trace'li örnek değilse inceleme dal ucundan okur, linkler sürümden.

## 2026-10-02 — Sürüm → ref eşlemesine {service} yer tutucusu (mono-repo) (v0.10.1047)

**Operatör:** "Mono-repo'da sürüm etiketi: aynı depoda birden çok servis varsa, başka servisin etiketi bu servisin
sürümü sanılabiliyor." Desenin tek yer tutucusu `{version}`'dı; başka servis için kesilmiş düz `1.4.2` tag'i bu servisin
çalışan kodu sanılıyordu (v0.10.1044 "Bilinen sınırlar"). **Kural:** isteğe bağlı ikinci yer tutucu `{service}`
(`tags/{service}-{version}`, `tags/{service}/v{version}`) = konvansiyonun soyduğu servis adı (önek + -prod/-int/-uat/-prep;
ResolveRepo ile tek fonksiyon `conventionName`; pinli mono-repoda da depo değil servis adı). Soyma tek yerde, `resolveRevision`:
frame linkleri (ucun service alanı) ve AI kod incelemesi (`buildCodeContext` → `FetchCodeAt`) aynı ham adı verir, aynı ref'i
sorar. Servis adı da telemetri: sürümle aynı ref-güvenli kapı; boş/güvensiz ad → ref yok, dal ucu, istek ve yankı yok
(`tags/-1.4.2` asla). **Doğrulama** (`NormalizeVersionRef`, kayıt ve çözüm aynı kapı): `{version}` zorunlu, başka `{…}` 400.
**Varsayılan aynen** `tags/{version}`: `{service}`'siz kurulumda istekler bayt bayt eskisi (golden); ref cache anahtarı zaten
tam ref'i taşıyor. Ayarlar → Kod entegrasyonu ipucu güncellendi.

## 2026-10-02 — Batch: yük altında gecikme artışı anomali açmaz (v0.10.1046)

**Operatör (prod):** "Batch servislerde yük altındaki gecikme artışı da anomali sayılmasın." v0.10.1039
yükün kendisini susturmuş, gecikmeyi bilinçli olarak dışarıda bırakmıştı (o kaydın "KAPSAM DIŞI" maddesi).

**Kural:** batch kalıbına giren serviste (aynı `batchServicePatterns` / `IsBatchService`; boş liste ya da
doğrulanmamış ayar = kural kapalı) gecikme artışı, istek hacmi işin **olağan çalışma hacminin en az 2 katındayken**
YENİ olay açmaz. Tek sabit `batchLoadSurgeFactor = 2`; iki saf kol: ondalık `batchLoadSurge` (medyanlar) ve
tamsayı `batchLoadSurgeCounts` (trace_op_latency; `internal/anomaly/batch_latency.go`). "Olağan çalışma hacmi"
her noktada gecikme kıyasının KENDİ tabanından:
1. **`trace_op_latency`:** çiftin cari kova başına çağrısı ≥ 2 × taban penceresinin (24 sa) AKTİF kova başına
   çağrısı — `cur_calls × base_buckets ≥ 2 × base_calls × cur_buckets`, `base_buckets = uniqExact(time_bucket)`
   aynı geçişten. Boş kovalar sayılmaz: günde bir saat koşan işin olağan hacmi koşu hacmidir (24 sa ortalaması
   her koşuyu "sıçrama" yapardı). Koşul SQL HAVING'de (LIMIT 200 yük altındaki batch çiftleriyle dolmaz) + aynı
   tamsayı aritmetiğiyle Go kemeri. Kural kapalıyken ya da aktif küme okunamadığında SQL metni ve argümanlar
   birebir eski (golden); ek kolon yalnız batch kolunda.
2. **Metrik dedektörü `p99_ms`:** dwell kovalarının HER BİRİNİN istek hızı ≥ 2 × p99 tabanının KENDİ kovalarının
   hız medyanı: mevsimsel tabanda aynı mevsimsel okumanın yeni `rate` kolonu (aynı slot ±15 dk, 14 gün — işin o
   saatteki olağan hacmi), ardışık tabanda 24 sa ardışık kovalar. Mevsimsel okumaya tek kolon eklendi (aynı
   satırlar/granüller, satır kümesi değişmez; ikinci sorgu yok).
3. **Davranış motoru `p99_ms`:** adayın penceresindeki HER dilimin span hacmi ≥ 2 × kendi haftanın-saati
   kovasının hacim medyanı (aynı satırlar). Yalnız ARTIŞ yönü; rejim adayı yük altındaysa mevsimsel pencere
   ayrıca sorulur ve yükün açıklamadığı dilim taşıyorsa rejim adayı (alanları aynen) kalır.

**Yalnız açılışı keser — zaten aktif olana dokunmaz:** metrik dedektöründe açık `anomaly:<svc>:p99_ms` satırı
(open/acknowledged) varsa kapı koşmaz. `trace_op_latency`'de o çiftin olayı son **15 dk** içinde yazılmışsa çift
muaf (`opLatActiveAge` = 10 dk aktiflik + bir 5 dk kova: olay k kovasında yazıldıysa ve k+1 eşiği bir kez
kaçırırsa — ör. p99 oranı 2.9× — k+2 en erken 10 dk sonra değerlendirilir; 10 dk'lık pencere çifti tam o anda
muafiyetten düşürür, kapı tepenin geri kalanında onu susturur ve terfi Problem'i "anomaly cleared" ile kapanırdı).
Recorder tikinde sorgudan önce TEK sınırlı okuma (tür + son 15 dk + batch servis koşulu SQL'de, en çok 201 satır
DÖNER, max_execution_time 5), muaflar HAVING'de tuple `NOT IN`; muaf liste 200 çift ve 64 KiB servis+operasyon
metniyle sınırlı (operasyon adları sınırsız) — tavan aşılırsa en tazeler muaf, ötesi muaf değil (bir kez log,
sayılarla). Davranış motorunda servisin p99 davranış olayı aktifse (son 10 dk; olaylar her taramada yeniden
yazılır) aday kalır; okuma TEMBEL (yalnız bir aday susturulacakken, tik başına en çok bir kez, WHERE'de p99
pattern'i, tavan 200); tavan aşımı da okuma hatası gibi sayılır. Aktif küme
okunamazsa o tik kapı hiç uygulanmaz (bir kez log). Kapatma geçişi yok. Sonuç: yük tepesinden ÖNCE başlamış,
yükle ilgisiz bir gerileme yük gelince "anomaly cleared" diye kapanıp tepe sonrası yeni bildirimle açılmaz.

**Bilinmeyen taban = susturma yok:** taban sıfır/yok, aktif kova 0, kova yetersiz, hacim serisi hizasız ya da
mevsimsel hacim yok → bugünkü gibi açılır.

**Hâlâ açılan:** batch serviste olağan çalışma hacminde (ya da yükten önce başlamış / yükün açıklamadığı bir kova
taşıyan) gecikme artışı; zaten aktif her gecikme olayı/problemi; `error_rate` (metrik + davranış), `new_error`,
trace_op `error_spike` (v0.10.1039 kuralı aynen); davranış motorunda p99 düşüşü. Batch olmayan servislerde her
karar birebir aynı.

**Bedeller (açıkça):** (a) Orantı denetimi YOK: yük ≥ 2× iken gelen gecikme artışı büyüklüğünden bağımsız susar —
2× yükle 50× gecikme de yeni olay açmaz; yükten bağımsız bir neden (yavaş bağımlılık) aynı anda gelirse de susar;
aynı anda hata oranı artarsa `error_rate` açılır. (b) İkinci derece etki: susturulan batch p99 adayı kümeleme
adayı da değildir, yani bir kümenin en az üye sayısına (3) sayılmaz — üç servislik bir kaskad o tik kümelenmeyip
üyeleri bireysel açılabilir. (c) `trace_op_latency` tabanı aktif kova başına ORTALAMA — üç doğrulanmış şekil:
damla profili (kovaların çoğunda birkaç çağrı + gerçek bir koşu, ör. 276 kovada 1 çağrı + 12 kova × 1.000 →
ortalama ~43) koşu yükünün çok altında kalır, AYNI yükteki bir koşu da "sıçrama" sayılır ve o operasyon için
operasyon düzeyi gecikme olayı hiç açılmaz (servis düzeyi p99 yine görür); rampa profilinde (50, 50, 50, 2.000)
olağan büyük kova sıçrama sayılır, küçük kovalar olayı yine açabilir ve açıldıktan sonra çift muaftır; 7/24 batch
servislerde 24 sa ortalamasının ≥ 2 katı günlük tepe, tepe saatlerinde YENİ olayları susturur. Ortalama max / p90
yerine bilinçli seçildi: onlarla kayan taban gerçek bir sıçramayı birkaç dakikada kendine katar ve susturma
sıçramanın ortasında kalkıp olay açılırdı. (d) Ek okumalar: recorder dakikada bir küçük `anomaly_events` tablosunda
bir FINAL taraması (en çok 201 satır DÖNER); davranış taraması 2 dk'da en çok bir kez, yalnız gerektiğinde.

**Bilinen sınırlar:** son yazımdan 15–22,5 dk sonra yeniden ateşleyen bir `trace_op_latency` olayı yaşam döngüsü
kuralına göre aynı epizottur ama muaf değildir (yük tepesinde susar) — o ana dek terfi Problem'i "cleared"
geçişiyle zaten kapanmış olur.

Settings → Anomaly: "Batch servis ad kalıpları" ipucu ve alttaki kapsam notu buna göre düzeltildi.

## 2026-10-02 — Anomali olayı: yeniden tetiklenme yeni bölüm, eski tepe taşınmaz (v0.10.1045)

**Operatör:** "Eski yüksek oran taşınmasın: kapanıp yeniden tetiklenen anomali, eski en yüksek oranıyla (ör.
'66×', P1) görünüyor. Yeni tetiklenme sıfırdan başlasın." **Önce:** `UpsertAnomalyEvents` aynı parmak izinin
en yüksek `peak_ratio`'sunu ve İLK `started_at`'ini olay "cleared" olduktan sonra da, satır TTL'i
(`toDate(started_at) + 30 gün`) bitene dek taşıyordu. Günler sonra 3.2× ile yeniden tetiklenen olay eski, yük
kaynaklı 66× ile /inbox'ta P1 görünüyor; terfi kapısının 300 sn sürme şartını eski `started_at` yüzünden ilk
tikte geçip eski tepeyle (≥ 20) ve eski StartedAt'in yaş tabanlı eskalasyonuyla doğrudan critical açılıyor,
açıklaması eski tepeyi alıntılıyordu (v0.10.1039 bedel (d) bunun özel hâliydi).

**Kural:** gelen olayın `last_seen`'i saklı satırınkinden `anomalyEpisodeGap`'ten (2 × aktif yaş + 150 sn =
22 dk 30 sn) FAZLA ilerideyse → YENİ BÖLÜM: `started_at` ve `peak_ratio` yalnız gelen olaydan, hiçbir şey
taşınmaz; tam sınır = aynı bölüm. Sınır aktif yaşa (10 dk, status) bilinçli EŞİT DEĞİL: 5 dk kovalı yazıcılar
(`trace_op`, `trace_op_latency`) TEK kova kaçırınca ~10 dk ± saniyelik boşluk üretir; sınır 10 dk olsaydı
sürekli ateşleyen anomali yazı-turayla sıfırlanır, satır yalnız saniyelerce "cleared" göründüğü için
evaluator çoğu zaman görmez, terfi Problem'i tazelenmeden bayat süpürmede "source silent" ile (ack, atanan,
AI özeti kaybıyla) kapanır ve taze bir sayfayla yeniden açılırdı. 22 dk 30 sn 5 dk'nın katı değil: 1–3
kaçırılmış kova aynı bölüm, 4+ yeni bölüm; her sıfırlamadan önce satır ≥ 12 dk 30 sn "cleared" görünür, değişmeyen
`resolveClearedAnomalyPromotions` terfi Problem'ini o sürede "anomaly cleared" ile kapatmıştır — sıfırlama
yalnız YENİDEN terfiyi etkiler (taze başlangıç, 300 sn bekleme, yeni tepe). Saat OLAY saati, duvar saati
değil (ingest/tik gecikmesi ve pod–CH saat kayması karara girmez; sırası bozuk yazım yeni bölüm açmaz).
`anomalyActiveAge` yalnız status sabiti (dört ayrı literal'in yerine).

**Bölüm içinde değişmeyen:** started_at korunur, tepe yalnız yükselir. Ekler: `last_seen` geri gitmez (max) —
0'lı ya da sırası bozuk bir yazım saklı değeri geri çekseydi sonraki normal yazım sahte boşluk görürdü; taşıma
okuması aynı id için iki sürüm döndürürse (0010'suz kurulum) last_seen'i büyük olan alınır. Taşıma okuması
HATA verirse o tik YAZILMAZ, hata çağırana döner (eskiden her olay "ilk görülme" gibi yazılıyordu: çağrıdaki
tüm bölümler sıfırlanırdı, davranış motorunda bütün filo); yazıcılar durumsuz, sonraki tikte yeniden üretir.
Şema değişmedi; taşıma okuması aynı sorgu, bir kolon fazla.

**Terfi:** kapı (`promotionGate`: tepe, sayım, 300 sn) yalnız YENİ Problem açılışını yönetir. Olayı aktif ve
susturulmamış açık bir `anomaly-auto:` satırı kapı o tik geçmese de tazelenir (`anomalyPromotionStep`; mevcut
tazelemeyle aynı alanlar, bildirim YOK) — eskiden sayım ~3 dk (3 × evaluator aralığı) tabanın altında kalınca
olay sürerken satır bayat süpürmede "source silent" ile kapanıyordu. Cleared ve susturulmuş olaylar eski
yollarından kapanır.

**Sonuçlar:** yeniden tetiklenen olayın terfisi 300 sn'yi YENİDEN bekler; /inbox önceliği yeni bölümün
tepesinden; karar ve susturma (parmak izi anahtarlı) değişmedi. `/anomalies` geçmiş satırı (parmak izi başına
tek satır) SON bölümün başlangıcını ve tepesini gösterir — ilk-ever başlangıç ve tüm zamanların tepesi artık
yok (bilinçli). Süre, grafik bandı, deploy çipi, annotation şeridi ve kök neden çıpası da son bölümden okunur;
TTL son bölümün başlangıcından sayılır. **Tekrarlayan anomaliler** (ör. gece işi) her seferinde YENİ bölüm
görünür: deploy raporu / rollout "deploy sonrası" listeleri ve kök nedenin deploy şüphelisi her yeniden
tetiklenmeyi ondan önceki deploy'a bağlar; "yeni" ile "tekrarlayan"ı ayırmak bölüm sayacı ya da ilk-ever
kolonu (şema değişikliği) ister — kuyrukta.

**Yazıcılar ve kısıtlar:** kayıtçı 60 sn — `log_pattern` kayan 5 dk pencere; `trace_op` / `trace_op_latency`
5 dk hizalı kova; `log_template_new` şablonun ilk ~10 dk'sı, tepe 0. Davranış motoru dedektör tiki 2 dk,
started_at = kaymanın başlangıcı. **Kısıt:** yazım aralığı 22 dk 30 sn'yi aşan yazıcı (ör. yapılandırılmış
`anomaly_record_interval` / `anomaly_interval`) her yazımda yeni bölüm açar — kayıtçı olayları hiç terfi
etmez (started_at = tik anı, 300 sn dolmaz). `elastic_ml` 5 dk poll, last_seen = kova sonu: 15 dk'lık
bucket_span'de ardışık kayıtlar (boşluk 15 dk) AYNI bölüm, en az bir kova atlayan kayıt (≥ 30 dk) yeni bölüm
(tepe = skor/100 ≤ 1 → P3, varsayılan terfi tabanının altında); `exclude_interim` verilmediği için ara
kayıtlar gelecekteki bir last_seen taşıyabilir ve max onu tutar — satır en çok bir kova daha uzun aktif
görünür (kozmetik, kuyrukta). migrations/0010'u uygulamamış kurulumda farklı günde açılan bölüm aynı id'yi
iki gün-partition'ında bırakabilir (eski kopya kendi TTL'ine dek); FINAL varsayılan ayarla doğru okur, taşıma
okuması büyük last_seen'i alır.

## 2026-10-02 — Kod inceleme çalışan sürümden okur (v0.10.1044)

**Operatör:** "Kod, dalın ucundan değil çalışan sürümden okunsun." **Önce:** "Kodu da incele" (trace ve exception
açıklaması) kaynağı dalın UCUNDAN okuyordu (BranchOrder: release → master → deponun varsayılanı; ağaç ve dosya
`versionType=branch`); stack'teki satır numarası koşan koddan başka bir satıra düşebiliyordu. Frame linkleri v0.10.590'dan
beri sürümü commit'e bağlıyordu, kod incelemesi bağlamıyordu.

**Kural — tek çözücü:** frame linklerinin mekanizması aynen; `resolveRevision` (frame_links.go'dan çıkarıldı, iki yüzey
de onu çağırır). Dal zinciri önce koşar (api-version, depo adı düzeltmesi); `VersionRef` deseni sürümü ref'e
(`ResolveVersionRef`), `refs?filter=<ref>&peelTags=true` ref'i commit'e bağlar; ağaç, dosyalar, kapsamlı alt-ağaç ve pencere
linkleri o COMMIT'ten (`versionType=commit`, link `GC<sha>`). Ref yok / sorgu hatası / çözülemeyen yanıt / commit ağacı
okunamadı → bugünkü dal sırası ve gerekçe satırında düz cümle: "çalışan sürüm 1.4.2 depoda bulunamadı (tags/1.4.2), release
dalından okundu" (hatada "… okunamadı (…), release dalından okundu"). Kod çekimi sürüm yüzünden düşmez, sınıf (outcome)
değişmez.

**Annotated tag:** refs listesi commit'i (`peeledObjectId`) YALNIZ `peelTags=true` ile döner; parametresiz `objectId` TAG
NESNESİDİR, commit ağacı okuması düşer ve özellik release-plugin tag'lerinde sessizce hiçbir şey yapmazdı (her tıkta da
başarısız bir ağaç isteği öderdi). İstek artık parametreyi taşır; sahte sunucu da peeled'i yalnız parametreyle döner
(önceden koşulsuz döndüğü için testler bunu göremiyordu). v0.10.590 frame linkleri de aynı kusuru taşıyordu, birlikte düzeldi.

**Sürüm kapısı (güvenlik):** sürüm telemetriden gelir — span gönderebilen herkes `service.version` / image tag yazar — ve
PAT'lı bir isteğin sorgusuna, model bloğuna (çit dışında), gerekçe satırına ve panele gider. Tek kapı `ResolveVersionRef`'te:
yalnız `^[0-9A-Za-z][0-9A-Za-z._+\-/]{0,99}$` ve `..` içermeyen sürüm kabul; gerisi "sürüm yok" (istek yok, bugünkü başlık,
hiçbir yerde yankı yok). Süzgeç değerinin tamamı `QueryEscape`'li: eski `PathEscape` `& = + $`'ı bırakıyordu — `x&$top=1`
isteğe parametre ekliyor, geçerli semver `1.4.2+77`'nin `+`'sı boşluğa çözülüp hiç eşleşmiyordu. Kapı frame linklerini de
korur (gövdedeki sürüm de aynı çözücüden geçer).

**Bu sürüm HER kurulumu değiştirir:** `VersionRef`'in kapalı hâli yok — boş = varsayılan `tags/{version}` (v0.10.590) ve ayar
kaydı bu varsayılanı yazıyor. Varsayılan eşleme artık AI kod incelemesini de yönetir: stack'i basan servis gerçek bir sürüm
taşıyor ve depoda aynı adlı tag varsa kod o commit'ten okunur. Tag'leri desene uymayan kurulum dalı okumaya devam eder; bedeli
bir refs sorgusu (10 dk cache'li, yokluk dahil). Desen: Ayarlar → Kod entegrasyonu → "Sürüm → ref deseni"
(`heads/release/{version}` vb.).

**Sürüm nereden (ek okuma yok) — stack'i taşıyan KAYIT önce:** tek Go yardımcısı `devops.RunningVersion` —
`container.image.tag` → `k8s.container.image.tag` → `service.version`, yer tutucu atlanır; frontend `runningVersion.ts` ve
chstore `effectiveVersionExpr` ile aynı sıra, üçü kaynaktan pinli (`running_version_test.go`). Trace (`anomaly.StackVersion`):
(1) stack'li logun KENDİ span'i (eldeki span'lerden), (2) logun kendi resource attribute'ları, (3) son çare stack servisinin o
trace'teki span'lerinde çoğunluk (eşitlikte en yeni span, o da eşitse sözlük sırası). Neden çoğunluk önce değil: canary —
yeni sürümdeki tek pod düşer, eski sürümdeki iki deneme başarılı olur; çoğunluk tam da patlayan sürümü kaybeder (test: hatalı
span 1.5.0 + iki deneme 1.4.2 → 1.5.0). Exception: sürüm YALNIZ stack'i veren olaydan — stack bir örnekten geldiyse o örneğin
trace'i eldeki trace ise o örneğin span'i, değilse sürüm yok (bugünkü davranış); stack eldeki trace'in logundan geldiyse
trace'teki sıra. Bir olayın stack'i başka bir olayın sürümüyle asla eşleşmez. Sürüm yok ya da yer tutucu → istekler bayt
bayt bugünkü (golden, v0.10.1039 koduyla kaydedildi). Yer tutucu listesi SQL'le eşitlendi: `main`, `master`, `HEAD`, `null`,
`n/a`, `NULL` artık sürüm sayılmaz (frame linkleri dahil).

**Süre:** iki tam ağaç listelemesi (dal + commit) tek 25 sn tavanı paylaşıyor. Commit ağacı kalan sürenin YARISIYLA okunur;
yetişmezse dal ağacına (zaten elde) düşülür ve gerekçe "commit ağacı süre payında okunamadı" der — dal dosyalarına süre kalır,
"dalından okundu" cümlesi doğru olur. Ayrıca `doGetCapped` gövde okuması yarıda kesilirse (süre, bağlantı) artık HATA döner:
eskiden hata yutuluyor, yarım ağaç "kesik ama kullanılabilir" sayılıp 10 dk cache'leniyordu — dal ağaçlarında da var olan
gizli kusur.

**Panel ve model:** "Kaynak: <depo> · 1.4.2 (çalışan sürüm)" ya da "· release (dal)" (iki çizim yeri, `codeSourceRef`);
yanıtın `code` alanında `version`/`commit` yalnız commit'ten okunduysa dolu. Model bloğu başlığı "depo: X, çalışan sürüm:
1.4.2, commit c0ffee00" ya da "depo: X, branş: release; çalışan sürüm 9.9.9 depoda bulunamadı — satırlar çalışan koddan
farklı olabilir"; sürüm yoksa başlık (ve cevap önbelleği anahtarı) bayt bayt eski.

**Cache:** ref→commit cevabı ağaçla aynı ömür ve tavanda (10 dk, 8 girdi; taban adres/koleksiyon/proje/depo/ref anahtarlı);
yokluk da cache'lenir; hata ve çözülemeyen 2xx gövde cache'lenmez. Ağaç anahtarı zaten ref türüyle ayrıktı
(`refCacheName`); FetchCode'un kapsamlı alt-ağaç anahtarı da artık oradan (dal anahtarı bayt bayt eski). Cevap önbelleği
anahtarı model bloğundan kurulduğu için ref'e göre ayrışır.

**İstek sayısı (sahte TFS, `TestFetchCodeAt_ExtraRequestBudget`):** sürüm + tag var: soğuk +1 refs +1 commit ağacı, sıcak
+0; tag yok: soğuk +1 refs, sıcak +0; sürüm yok: +0. Dosya çekimi sayısı aynı, yalnız ref'i değişir.

**Audit:** `versionRef` artık DevOps ayar audit detayında — her AI kod cevabının hangi commit'ten okunduğunu belirliyor.

**Bilinen sınırlar:**
- Mono-repo: desende `{service}` yer tutucusu yok; başka bir servis için kesilmiş düz bir tag (ör. `1.4.2`) bu servisin
  sürümü sanılır.
- Zincir İLK yer-tutucu-olmayan anahtarda durur (frame linkleriyle kilit adım): depoda karşılığı olmayan bir image tag,
  çözülebilecek bir `service.version`'ı gizler — dal ucuna düşülür.
- Organizasyon araması isabetleri (başka depo) dal ucundan okunur ama "çalışan sürüm" başlıklı bloğun içinde durur.
- 10 dk ref cache'i artık frame linklerinde de geçerli: zorla taşınan (force-moved) bir tag en çok 10 dk eski commit'i gösterir;
  yeni basılan tag en çok 10 dk görünmeyebilir.
- Commit ağacının alt süre tavanı frame linklerinde de geçerli (10 sn tavanın kalan yarısı).
- Exception detay sayfasının frame linkleri hâlâ sürüm göndermiyor (`ProblemDetail.tsx`): o sayfada linkler dal ucundayken
  "Kodu da incele" commit'ten okuyabilir — ayrı iş.
- SQL `placeholderVersionList`'te SNAPSHOT alt-dizgi kuralı yok (`2.3.0-SNAPSHOT` deploy tespitinde sürüm sayılır) —
  dokunulmadı.

Kod bütçesi, pencere seçimi, kesik-ağaç notu, sohbet ve kanıt değişmedi.

## 2026-10-02 — Batch: seyrek koşan işte "yeni hata" artık uzun tabana bakar (v0.10.1043)

**Operatör (prod):** "Batch'te 'yeni hata' gürültüsü: son 24 saatte hiç koşmamış bir iş her koşuda 'yeni hata'
diye açılıyor." **Neden her koşu yeniydi:** `trace_op` cari 5 dk'yı 24 sa tabanla kıyaslar; tabanda hata yoksa
(`base_errs = 0`) `new_error` der ve oranı cari hata SAYISI yazar. 24 saatten seyrek koşan bir iş (haftalık ya da
koşusu taban penceresinin hemen dışına düşen günlük iş) tabanda hiç çağrı ve hata taşımaz → her koşunun her
zamanki hata payı "yeni hata ×300" olur (Problems'ta P1 ≥5×, terfi kapısında critical `anomaly-auto`). v0.10.1039
bunu kapsam dışı bırakmıştı (bedel (c)).

**Kural (yalnız batch servisler — aynı `IsBatchService` / `BatchServiceSQL` listesi):** batch çiftinde 24 sa
tabanda hata yoksa new_error demeden önce aynı MV'den UZUN taban okunur: [hizalı şimdi − 8 gün, 24 sa tabanın
başı), yarı açık — üst sınır ana sorgunun taban başıyla aynı değer, kova iki kez sayılmaz (8 gün = haftalık iş +
1 gün kayma). Uzun tabanda hata VAR → taban payı = uzun taban hatası / (uzun taban çağrısı + 24 sa tabanın
çağrısı) — 24 sa penceresinde hata yok, yani oradaki çağrılar TEMİZ koşudur ve paydaya girer (dün 1M temiz çağrı
+ altı gün önce 10/1.000 hatalı iş %1 değil ≈%0.001 tabanla kıyaslanır; iş 24 sa'de koşmadıysa payda değişmez —
bu yalnız ateşlemeyi artırır). Sonra: (1) **mutlak artış kaçışı** — cari pay − taban payı ≥ 25 puan
(`traceOpBatchExtAbsRise`) → bugünkü `new_error` (sayı oranı; 3×'ün altında pay oranlı bir error_spike terfi
tabanını hiç geçemezdi); (2) değilse cari pay ≥ 3 × taban payı → `error_spike`; (3) değilse olay yok. Kaçış 3×'ten
önce: %2 → %30 gibi büyük sıçrama bugünkü gibi yüksek sesle açılır. Olay dürüst raporlanır: error_spike oranı =
PAY oranı (sayı değil); UI'nin "N prev"i = taban payı × cari çağrı ("her zamanki payında N hata olurdu"; zamana
bölünmüş taban seyrek işte ≈0 çıkıp yalan söylerdi) — Ratio ≈ cari/prev sözleşmesi korunur, yeni alan yok. Uzun
tabanda hata YOK (çağrı olsun olmasın) → `new_error` AYNEN: hiç hata vermemiş bir işin hata vermeye başlaması
gerçek sinyal. Sayım (≥10) ve pay (≥%1) tabanları önce, aynen.

**Ek okumanın bedeli:** tik başına EN ÇOK BİR `operation_summary_5m` sorgusu, YALNIZ en az bir batch new_error
adayı varken; SQL'de batch koşulu + aday çiftler birebir `(service_name, name) IN (tuple(?, ?), …)` (birincil
anahtar önekini budar; clickhouse-local EXPLAIN, 2 çift / 8 gün: 7.813 granülün 5'i), zaman sınırlı, `LIMIT 50`,
`max_execution_time = 10`. Aday tavanı **50** (cari hata çoktan aza; sınıflandırıcının çıktı tavanıyla aynı — ana
sorgunun LIMIT 200'ünde bu kural en çok 50 satırı susturur, ilk 50'yi boşaltamaz); tavan dışı adaylar bugünkü
new_error, tik başına tek satır sayı logu. `/api/anomalies/trace-ops` aynı işlevi çağırır (60 sn önbellek).

**Hata yönü (süzgeç kuralı: okunamadıysa süzme):** uzun okuma ya da taraması başarısız → hiçbir aday susmaz,
yarım sonuç da uygulanmaz; bugünkü new_error + tek log satırı. Taban başı = şimdi − pencere − max(24 sa, 12 ×
pencere); 13 × pencere ≥ 8 g olunca (5 dk hizalı pencere ≥ 14 sa 50 dk — yalnız API `?window=` buraya ulaşır) uzun
pencere boştur, okuma yapılmaz.

**Değişmeyen:** batch olmayan servislerde her karar; kural kapalıyken (boş liste) ana SQL metni birebir ve ek
okuma yok; `base_errs > 0` batch çiftlerinde v0.10.1039 sayım+pay kuralı; şema yok (MV TTL'i 90 gün, 8 gün
kapsanıyor; MV saklama düşürücüsü bu MV'ye dokunmuyor).

**Bedeller:** (a) aylık (8 günden seyrek) işler kapsam dışı — her koşu hâlâ new_error; (b) **seyrek işte başka
emniyet YOK**: `error_rate` metrik/davranış dedektörleri ≥15 dolu kova / ≥3 farklı gün taban ister, haftalık bir
işte bu tabanı hiç kuramaz. Haftalık işte uzun taban TEK koşudur; kaçış olmasa geçen hafta %40 bozuk iş bu hafta
%100 bozulunca 2.5× der ve susardı, taban ≥ %33.4 iken hiçbir şey ateşlenemezdi ve susan kötü koşu gelecek
haftanın tabanı olurdu. Mutlak artış kaçışı (≥25 puan → new_error) bunu kapatır (%40 → %100, %60 → %100 açılır);
KAPSAMADIĞI: zaten yüksek bir tabandan 25 puanın altında kalan artış (%40 → %60 gibi; 3× de tutmaz) SUSAR, kronik
%60 → %60 da susar (kasıtlı); (c) uzun-taban olayı aynı `trace_op` parmak izini günceller — yükseltmeden önceki
"×300" tepe oranı satırda kalabilir (v0.10.1039 bedel (d) yaşam döngüsü).

**Bilinen kalan gürültü (ayrıca kuyrukta):** hataları koşunun ORTASINDA, ≥ 2 temiz kovadan sonra başlayan işte
yalnız ilk hatalı kova bu dala girer; sonraki kovalarda 24 sa tabanda aynı koşunun hatalı kovaları vardır
(base_errs > 0) ve v0.10.1039 sayım+pay yolu onları koşunun KENDİ seyreltilmiş 24 sa payıyla kıyaslar → her koşuda
sayı oranlı `error_spike` açılır.

**Testler:** saf sınıflandırıcı tablosu + özellik ızgarası (olay sayısı artmaz, batch olmayan / kural kapalı
birebir, dönen olay pay oranında, batch olmayan çift ilk 50'den düşmez); sorgu kurucu şekli (batch koşulu, çift
kısıtı, yarı açık sınır, LIMIT, max_execution_time); sahte bağlantıyla davranış (okuma hatası ve rows.Err → bugünkü
new_error + tek log, tavan aşımı, boş listede eski SQL ve ek okuma yok); `clickhouse local` uçtan uca sınır kovaları
(ikili yoksa atlanır — CI'da atlanır, CI'yı şekil + davranış testleri kapsar).

## 2026-10-02 — Problems: susturulan anomali kuyruktan düşer (v0.10.1042)

**Operatör:** "Anomalide 'Mute' sonrası satır listeden düşsün." **Önce:** anomali detayında Mute… susturmayı
yazıyor, sayfa kuyruğa dönüyor ve satır HÂLÂ orada duruyordu. Inbox anomali satırlarını aktif `anomaly_events`'ten,
rozeti `CountActiveAnomalyEvents` SQL sayımından kuruyordu; ikisi de susturmalara bakmıyordu (terfi ve /anomalies
canlı uçları bakıyordu). Susturma yazım uçları da yalnız `anomaly:` önbelleğini düşürüyordu.

**Kural:** aktif susturmalar (`ActiveSilencedFingerprints`, `until_at > now`) inbox derlemesi başına TEK okunur,
anahtar olay kimliği (= susturma parmak izi; evaluator'ın `muted[ev.ID]` kapısıyla aynı). **open** görünümü:
susturulmuş anomali listelenmez ve sayılmaz — eleme SQL'de, LIMIT'ten ÖNCE (`ListAnomalyEventsFilter.ExcludeIDs`;
Go'da sonradan düşürmek v0.9.335'in kapattığı tarama-bütçesi açığını açardı); kind/öncelik çipleri ve `total`
aynı satırlardan; kenar çubuğu rozeti aynı kümeyi aynı SQL yüklemiyle (`anomalyExcludeIDsSQL`) eler. **all**
görünümü: satır kalır, durumu `muted` (durum sözlüğünde zaten nötr) — operatör bulur, susturmayı /anomalies
"Muted" şeridinden kaldırır; yeni UI yok. **Ignored** görünümü değişmedi (yalnız exception). Susturma süresi
dolunca okuma onu artık döndürmez; satır bir sonraki derlemede geri gelir (≤ önbellek TTL'i, ek iş yok).

**Önbellek:** ack / exception durumu / incident yazımlarının emsali — açık önek düşürme. Susturma oluştur / sil /
toplu sil uçları `invalidateSilenceReaders` ile `anomaly:` VE `inbox:` (liste + rozet, sürümsüz önek, v0.9.321)
önbelleğini düşürür; bir sonraki istek mute öncesi gövdeyi alamaz. Anahtara susturma özeti KATILMADI: anahtar
serveCached'in dışında kuruluyor, özet her isabette bir CH okuması demekti; yazım seyrek, düşürme ucuz.

**Hata yönü:** okunamayan süzgeç süzmez — susturma listesi okunamazsa hiçbir satır gizlenmez, rozet süzgeçsiz
sayar, derleme başına tek log (evaluator terfisinin "promoting unfiltered" yönüyle aynı). Kapsam dışı:
`anomaly-auto:` terfi Problem'leri (evaluator mute'ta zaten kapatıyor), /anomalies sayfası, susturma oluşturma UX'i.

**Susturma o olayın kimliğini taşır (aynı sürüm):** `log_template_new` (kimlik şablon kimliğinden) ve
`behavior_change` (kimlik ham metrik adından) türlerinde desen görüntü metni; sunucu susturmayı her zaman
`sha1(kind|pattern|service)`'ten yeniden hesapladığı için (`silenceFingerprint`, v0.10.162) yazılan parmak izi
olayla HİÇ eşleşmiyordu — operatörün en sık gördüğü iki türde Mute kuyrukta, terfide ve /anomalies'te etkisizdi.
**Kural:** bir olay için yazılan susturma o olayla eşleşir. Tek saf karar `silenceFingerprint`: gönderilen değer
`FingerprintAnomaly` ŞEKLİNDE bir olay kimliğiyse (`chstore.IsAnomalyFingerprint`: tam 16 karakter, yalnız
`[0-9a-f]`; şekil üreticiyle aynı sabitten) olduğu gibi saklanır; değilse desen+servis varsa bugünkü yeniden hesap,
o da yoksa raw (v0.10.162 köprüsü). Biçimi bozuk değer kimlik sayılmaz. Detay sayfası, inbox çekmecesi ve Cmd-K
zaten olay kimliğini gönderiyordu; servis sayfasının «Değil → sessize al» yolu `kind|pattern|service`
gönderiyordu, artık ortak kurucudan (`anomalyEventSilenceBody`) olay kimliğini gönderir. /anomalies canlı akışı
olay değil dedektör isabeti susturur (kimliği yok) ve yalnız `log_pattern` / `trace_op` gösterir — o türlerde
yeniden hesap = olay kimliği, değişmedi.

**Okuyucular, iki tür için düzeltmeden sonra:** inbox listesi ve rozet satırı düşürür; evaluator terfiyi atlar
(`muted[ev.ID]`) ve açık `anomaly-auto:<id>` Problem'ini "anomaly muted" gerekçesiyle kapatır; /anomalies "sessiz"
rozeti (`e.id` eşleşmesi) çizilir; "Muted" şeridi susturmayı listeler ve kimliğiyle (parmak iziyle değil) siler.
**Sonuç (bilinçli):** bir `behavior_change` olayını susturmak artık gerçekten terfisini durdurur ve terfi
Problem'ini kapatır — Mute'un anlamı bu, ve sessizce olmuyordu. **Eski susturmalar:** desenden türeyen türlerde
yeniden hesaplanmış eski kayıtlar kimlikle aynı, çalışmaya devam eder; iki bozuk türde eski kayıtlar hiçbir şeyle
eşleşmiyordu ve eşleşmemeye devam eder (düzeltilmez, süreleri dolar).

**Canlı uçlar:** /anomalies trace-ops ve log-patterns uçları susturma okuma hatasını `muted, _ :=` ile yutuyordu;
artık aynı tek kapıdan (`activeSilencedAnomalies`) geçer — yön aynı (süzgeçsiz), hata bir kez loglanır.

## 2026-10-02 — AI paneli: kod künyesi yalnız kod kartında (v0.10.1041)

**Karar (operatör: "'Kodu da incele → Evet' sonrası ilk kart da kaynak satırlarını basıyor; düzeltilsin."):**
her kart yalnız KENDİ isteğinin kod künyesini çizer (depo/branş satırı, dosya + "hata satırı N", bütçe notu,
"Kod okunamadı"). Kök neden: iki geçiş `CopilotExplain`'de tek `code` state'ini paylaşıyordu; Evet'in kod geçişi
yazınca kodsuz ilk kart okumadığı dosyaları kaynak gösteriyordu. Artık ilk kartınki `code` (yalnız `run()`),
"Kod incelemesi" kartınınki `codeCtx` (yalnız `runCode()`); çip ve `?aicode` yolunda ilk kart kendi künyesini
çizmeye devam eder. Kutunun altındaki depo linki kodu isteyen geçişi izler; sohbet bağlamı değişmedi.

## 2026-10-02 — Kod inceleme: "depo ağacı kesildi" uyarısı yalnız dosya bulunamadığında (v0.10.1040)

**Operatör:** "'Depo ağacı … kesildi' uyarısı yalnız dosya bulunamadığında çıksın." AI panelinin kod incelemesinde
"depo ağacı N yolda kesildi (yanıt tavanı 8 MB) — eşleşme kesik bölgede olabilir" notu ağaç tavana dayandığında HER
çekimde basılıyordu; her frame dosyasına eşlendiğinde de — cevabın yanında (panel Reason'ı + model bloğu) saf gürültü.
**Kural:** not yalnız ağaç kesikse VE kapsamlı geri-denemeden sonra da yolu bulunamayan en az bir frame varsa basılır
(`treeCapNoteApplies(capped, missed)`, saf, tablo-testli; `missed` = `huntOutcome.missedFrames`; "(okunamadı)" /
"(satır aralığı boş)" sayılmaz, yol ağaçta bulundu). Iskada not metni bugünkü gibi. Değişmeyen: tavanlar, geri-deneme,
kod bütçesi notu, dry-run'ın "liste kesildi" satırı, outcome taksonomisi. Bedel: her frame eşlendiğinde "N dosya
kapsamlı aramayla bulundu" izi de artık basılmaz.

## 2026-10-02 — Batch servislerde yük artışı anomali değil (v0.10.1039)

**Operatör (prod):** "Bazı batch işlerde ani yük artışı olabilir, onları anomali gibi düşünme — özellikle
`-batch` geçen servis isimlerinde." **Önce ne oluyordu:** `orders-batch` gibi bir serviste hata yüzdesi
SABİTKEN 20× istek artışı iki yoldan olay açıyordu: (1) davranış motoru `request_rate` için `behavior_change`
yazıyordu (Problems'ta P1, `promoteStrongAnomalies` ile `anomaly-auto:` Problem'e terfi; motor üç metriği
`anomaly_tracked`'den bağımsız ölçüyor); (2) `trace_op` hata SAYISINI (5 dk vs 24 sa pencere-normalize taban)
kıyasladığı için 20× yük = 20× hata = "error_spike 20×". Metrik dedektörünün `request_rate`'i (izleniyorsa;
varsayılan kapalı) ve `self-volume-spike` (24 sa hacim ×4) de aynı yükü olay sayıyordu.

**Kural:** `anomaly_sensitivity.batchServicePatterns` — adında kalıplardan biri ALT DİZGİ olarak geçen
servis "batch"tir; büyük-küçük harf yalnız ASCII'de katlanır (SQL ikizi `positionCaseInsensitive` ile birebir).
Alan yoksa varsayılan `["-batch"]` (bilinçli varsayılan davranış değişikliği); **boş liste kuralı kapatır**
(`*[]string`: nil = varsayılan, `[]` = kapalı; Normalize somutlaştırır, boş liste `[]` yazılır, `null` değil).
Normalize: kırp, küçült, <3 karakteri ve tekrarı at, en çok 10. Tek yüklem `IsBatchService`; trace_op SQL
koşulu aynı listeden üretilir. Ayar: Settings → Anomaly → Dedektör hassasiyeti → "Batch servis ad
kalıpları" (virgül ya da boşlukla ayrılır; kayıtlı liste boşsa "Kural kapalı" notu, normalizasyon giriş
düşürdüyse sayısı yazılır).

**Batch serviste ne değişti:** davranış motoru `request_rate`'i hiç değerlendirmez (iki yön); metrik
dedektörü `request_rate`'i atlar; `self-volume-spike` açılmaz (süzgeç saatlik önbellekten SONRA, ayar bir
sonraki tikte etkili); `trace_op` `error_spike` için bugünkü SAYIM kuralı (değişmedi) VE hata PAYI kuralı
birlikte gerekir: (cur_errs/cur_calls) ≥ 3 × (base_errs/base_calls) — pay iki toplamın oranı, pencere
normalizasyonu gerekmez. **Saf susturma:** batch olayları eski kümenin ALT KÜMESİ (pay kuralı sayımın yerine
geçseydi cari hacim 24 sa ortalamasının altındayken YENİ olay açardı — inceleme bulgusu); koşul SQL HAVING'de
(sabit-paylı batch çiftleri LIMIT 200'ü dolduramaz), Go aynı kuralı kemer olarak uygular; yalnız elediği için
batch olmayan çiftler LIMIT 200 / ilk 50'de yalnız yer KAZANIR. Raporlanan oran ve taban sayısı bugünkü
sayım değerleri. **Değişmeyen:** `error_rate` ve `p99_ms` (metrik + davranış), `new_error`, servisin diğer
metrikleriyle kümeleme adaylığı; batch olmayan servislerde her karar ve kural kapalıyken trace_op SQL metni
birebir aynı.

**Bedeller (açıkça):** (a) taban hata payı zaten ≥ %33 olan bir batch operasyonu `error_spike` açamaz (pay
üçe katlanamaz) — tam çöküşü `error_rate` dedektörleri ve `new_error` yakalar; (b) `self-volume-spike` aynı
zamanda ingest MALİYETİ alarmı: span sızdıran bir batch servisi artık bu uyarıyı almaz; (c) KAPSAM DIŞI ve
muhtemelen hâlâ gürültülü: 24 sa taban penceresinde koşmamış batch işlerinde `new_error` (taban yok → her
koşu "yeni" görünür), yük altındaki gecikme (`trace_op_latency`, p99 davranış/metrik), `log_pattern` sayım
sıçramaları (servis başına tabanı olmayan filo sayımı — batch'i dışlamak gerçek OOM/ORA satırlarını kör
ederdi), exception P1 hacim eşikleri (yapışkan P1 operatör direktifi), gömülü alarm kuralları, k8s
Job/CronJob tabanlı tespit (hiçbir MV iş kimliği taşımıyor); (d) gösterim: anomali satırı aynı parmak izi
için şimdiye dek saklanan EN YÜKSEK tepe oranını taşır — deploy sonrası yeniden ateşleyen bir batch olayı
30 güne kadar eski, yük kaynaklı bir tepe gösterebilir (tüm türlerde mevcut yaşam döngüsü; ayrıca kuyrukta).

**Ayar okunamazsa:** okuma hatasında son yayınlanan değer KORUNUR (varsayılan yalnız hiçbir şey
yayınlanmamışsa); hata geçişte bir kez loglanır. Ayar bu süreçte hiç doğrulanmadıysa (başarılı okuma ya da
PUT yok) batch kuralı her karar noktasında DEVRE DIŞI (`AnomalySensitivityForDetectors`; süzgeçsiz = eski
davranış) ve kapatma geçişi hiç koşmaz — tahmini varsayılanla tek yönlü kapatma yok. Ayar GET ucu okuma
hatasında varsayılan değil hata döner (varsayılanla dolan ekran bir Kaydet'le kayıtlı değeri ezerdi).

**Testler:** Go↔SQL fikstür testleri (yüklem + trace_op HAVING) `clickhouse` ikilisi gerektirir ve CI'da
ATLANIR; CI'yı şekil/golden testleri (SQL metni, argüman sırası, boş listede birebir eski SQL) ve saf
özellik tablosu (batch-yeni ⊆ batch-eski, batch olmayan çift ilk 50'den düşmez) kapsar.

**Açık satırların yaşam döngüsü:** metrik dedektörünün açık `anomaly:<svc>:request_rate` satırları (open ve
acknowledged) bir sonraki tikte açık gerekçeyle kapanır ("batch servis — yük sinyali anomali sayılmaz";
bayat süpürmenin yanıltıcı "source silent"ine bırakılmaz) ve o tik kümeleme adayı olmaz. `behavior_change` /
`trace_op` olayları yazılmayı bırakınca ~10 dk'da aktif görünümden düşer, terfi Problem'i
`resolveClearedAnomalyPromotions` ile "anomaly cleared" kapanır. Açık `self-volume-spike` satırı reconcile'ın
normal kapatma dalıyla kapanır (acknowledged olan bayat süpürmeyle). Kalıplar tik başına atomic ayardan
okunur (CH okuması yok); evaluator ve recorder ilk tikten önce bir kez hidrate eder. `/api/anomalies/trace-ops`
60 sn önbellekli ve anahtarı kalıpları taşımaz: ayar değişince o liste en çok 60 sn eski kalır.

## 2026-10-02 — Kod bütçesi ayar oldu, varsayılan 10.000 karakter (v0.10.1038)

**Operatör (prod, "Kodu da incele"):** "kod bütçesi (4000 karakter) doldu — 1 pencere düştü, kalanlar hata satırı
çevresinde kısaltıldı" notu rutindi. "Kod bütçesi daha fazla karakter olabilir bence, default 10k gibi, performans
sorunu olmayacaksa."

**Karar:** modele giden kodun toplam rune tavanı DevOps bağlantı blob'unda bir ayar: `codeBudgetRunes` (Ayarlar → Kod
entegrasyonu → "Kod bütçesi (karakter)", deneme/arama tavanlarının yanında). Yok/0 = varsayılan **10.000**; aralık
**2.000–20.000**, tek normalizasyon `devops.ClampCodeBudgetRunes` (PUT girdisi ve yürürlükteki değer `codeBudget()` aynı
fonksiyondan geçer; emsal `codeLookupLimit`). Mevcut admin GET/PUT ile gider-gelir (yeni uç yok); snapshot
`effectiveCodeBudgetRunes` da döner; audit detayına `codeBudgetRunes` girdi; PAT sözleşmesi (kayıtlı göstergesi, boş
girdi saklıyı korur) dokunulmadı. `FetchCode` yürürlükteki bütçeyi bağlama damgalar (`CodeContext.Budget`) ve "kod
bütçesi (N karakter) doldu" notu bu sayıyı söyler. Kayıtlı değer aralık dışındaysa (elle düzenlenmiş blob) kutu
yürürlükteki (sıkıştırılmış) değerle dolar — tarayıcının min/max doğrulaması Kaydet'i kilitlemez. Trace ve exception
açıklamasının kod yolu aynı çekimi paylaştığı için ikisi birden değişti.

**Taşma yarılaması — gönderilenin yarısı:** sağlayıcı bağlam taşması 400'ü dönünce `Halved` artık bütçeyi değil modelin
AZ ÖNCE taştığı kodu yarıya indirir: hedef `min(bütçe, gönderilen) / 2`, not gerçek sayıyı söyler ("gönderilen kod
yarıya indirildi (N karakter)"). Bütçe yarısı kuralı 10.000'de ≤ 5.000 rune'luk bir bloğu hiç küçültemiyor ve taşma
doğrudan kodsuz denemeye düşüyordu (4000'deki zarif düşüşten kötü). Yalnız yarısı 1.000 rune'un altında kalacak minik
blok (`halvedMinRunes`) ya da hiçbir penceresi kırpılamayan blok küçültülmez → kodsuz deneme (eski "küçültecek bir şey
kalmadı" kuralı). Örnek: 10.000'de 4.512 gönderildi → 2.256; 9.879 → 4.939; eski 4000 ayarında 3.998 → 1.999
(`TestClampAndHalvedUseEffectiveBudget`, `TestOverflowRetryKeepsHalfOfSentCode`).

**Neden 4000'dü, büyük bütçenin riski:** v0.9.830'da küçük yerel modelin (gemma4) bağlamında kod büyüdükçe kod-dışı
kanıt (trace, log, stack) sıkışmasın diye. Tipik Java ±30 satır penceresi satır numarasıyla ~2.300–2.750 rune
(repodaki JBoss demo kaynağı, medyan 2.520) — 4000 ikinci pencereyi kırpıp üçüncüyü düşürüyordu. Risk: bağlamı küçük
(~8K token) bir model 10.000 rune kodla taşabilir. Sağlayıcı 400 dönerse zincir (gönderilen → yarısı → kodsuz)
çalışır; ama girdiyi SESSİZCE kesen sağlayıcılarda hata yoktur — prompt'un kesilen ucu (kod bloğu ve arkasındaki
yönergeler; bazı yerel sunucularda system prompt) modele ulaşmaz. O kurulumda ayar düşürülür (4000 = eski davranış).

**Değişmeyenler:** ≤3 frame penceresi, ±30 satır, şema bölümü (800), hata span'ının SQL ifadesi (600, trace JSON'u
içinde), taşma zincirinin adımları (tam → küçültülmüş → kodsuz; en çok 2 LLM çağrısı), git ve LLM çağrı sayısı (bütçe çekimi değil, yalnız gönderileni kırpar). Mapper/statement
pencereleri zaten kod bütçesinin İÇİNDE kırpılıyordu; bütçe büyüyünce onlar da daha az düşer.

**Ölçüm (rune; `TestFetchCodeHonoursCodeBudgetSetting`, `TestTraceCodePromptByCodeBudget`):** gerçekçi 3 pencere
(2.586 + 2.609 + 2.626 = 7.821): 4000'de 2 pencere (ikincisi kırpık) + "1 pencere düştü" notu; 10.000'de üçü tam, not
yok. Eşik: 10.000'de üç tam pencere ortalama satır ≲ 48 karakterde sığar; daha geniş kodda üçüncü pencere hata
satırı çevresinde kısalır, pencere başına ~5.000 rune'u (satır ≳ 75 karakter) aşınca düşer. Klasik trace + kod user
prompt'u: küçük fikstür (12 span, 3 log, ~2.4K'lık 3 pencere) 8.835 → 12.054 (+3.219; kod 3.998 → 7.264); tavan
fikstürü (150 span, 100 log, şema, ~6.1K'lık pencereler) 56.265 → 62.433 (+6.168; kanıt 50.738 ve şema 832 aynı;
10.000'de bile 1 pencere düşer). Kodlu sistem istemi 4.423. Git çağrısı 5 = 5; LLM 1 = 1 (taşmada 2 = 2).

Bu kayıt `docs/plans/spec-ai-evidence-2026-08-28.md`'deki "kod 4000" satırlarının (Q6 tablosu "4000 rune (yarı:
2000)", "kod 4000 + şema 800 + SQL 400") yerine geçer; spec tarihçe olarak kalır.

## 2026-10-02 — Durum rozeti: NEW nötr, REGRESSED amber (v0.10.1037)

**Karar (operatör: "Exceptions'ta NEW ile REGRESSED renkleri aynı, düzelt."):** tek durum → ton sözlüğünde
(`features/anomalies/statusTone.tsx` `STATUS_TONE`) `new` amber'den nötre (`b-gray`) indi; `regressed` amber
(`b-warn`), `resolved` yeşil kaldı. **Neden:** NEW triaj görmemiş her grubun normal, varsayılan durumu — alarm
problemlerindeki OPEN gibi (o zaten nötr); K5 kuralıyla normal durum renk taşımaz. Neredeyse her satır amber
NEW iken amber bilgi taşımıyordu ve çözülüp geri gelen REGRESSED satırı aynı tonda kayboluyordu; artık tek amber
durum o. Sözlüğü paylaşan yüzeyler: Exceptions listesi (`StateBadge`), exception detayı, Problems kuyruğu
(Inbox `StatusBadge`); ayrı eşleme yok. Rozet kelimesi, NEW ipucu ve öncelik rozetleri değişmedi. Bu, v0.10.922 /
v0.10.929 palet kararlarının "new/regressed amber" satırını değiştirir; `statusPalette.pin` yeni eşlemeyi çiviler.

## 2026-10-02 — "CoSRE'ye sor" varsayılanı da klasik kanıt toplayıcısına döndü (v0.10.1036; v0.10.948 varsayılanının tersi)

**Karar (operatör: "Aslında CoSRE'nin eski explain trace'teki yapısı daha iyiydi, neden sonradan değişti. Kodu
incele kısmının da eski yapısı aynı şekilde güzel açıklama yapıyordu. Eski kanıt toplayıcı güzeldi."; nelerin
gideceği söylenip varsayılan da dönsün mü diye sorulunca: "dönsün"):** trace'te kodsuz "CoSRE'ye sor"
(`POST /api/copilot/explain-trace/{id}`) yine v0.10.948 öncesinin klasik tek atışı: `explainTraceClassicPrepared`
— `buildTraceExplainInput` (trace: Tempo önce, sonra CH; trace kimliğiyle loglar; Oracle satırları) +
`SystemPromptTrace`, akan cevap, klasik önbellek anahtarı `explainCacheKey(SystemPromptTrace(), in.User, "")`,
trace hiçbir yerde yoksa düz metin 404. v0.10.1035'in "Açık (operatör kararı)" maddesi böylece kapandı: iki yol
("CoSRE'ye sor" ve "Kodu da incele") yine tek toplayıcıdan geçer.

**Operatörün bilerek bıraktığı:** adım adım inceleme görünümü (canlı okuma listesi); dönem kıyası, pod ve
deploy/sürüm kanıtı; cevabın altındaki "Kaynak durumu" künyesi (`sources`, id'siz kanıt linkleri); kanıtta
bulunamayan sayı uyarısı (sayı denetimi). Ayrıca seçili span odağı: `?span=` istekte gider ama klasik toplayıcı
yok sayar, anahtara girmez.

**Kalan:** "Kodu da incele" (klasik + kod, v0.10.1035 aynen); waterfall kutulaması (`evidenceSpanIds`, klasik
toplayıcı `traceEvidenceSpanIDs` ile sunucuda hesaplar) ve Kanıt satırı; Oracle satır sayısı; kimlik köprüleri;
takip sohbeti (SOHBET: araç döngüsü + `TraceFollowUpAddendum`, `chat_trace_followup.go`) ve sohbetin "trace'i
açıkla" yönlendirmesi (balondaki "aynı motor, aynı önbellek" yeniden birebir doğru); exception explain. Ön yüzde
yeni arayüz yok: adım listesi ve dipnot veri gelmeyince zaten çizilmez; Durdur kalır, metni incelemeden söz
etmez ("Açıklamayı durdur — istek kesilir", "Durduruldu — açıklama yarıda kesildi"). Eski inceleme önbellek
satırları başka anahtarda (`traceInvestigationCacheKey` + `:inv` yan kaydı): hiç okunmaz, 1 saatlik TTL'le düşer.
`/ai`: yüzey yine `explain-trace`, istemi artık gerçekten `SystemPromptTrace` (evalset "Trace" eşlemesi yeniden
doğru); istem metni değişmediği için global istem sürümü aynı.

**İki adım:** bu sürümde v0.10.948 incelemesi uçtan ERİŞİLEMEZ ama silinmedi (testleri yeşil, doğrudan
çağrılır); operatör eski davranışı prod'da onaylayınca ayrı bir temizlik sürümü kaldırır. Kapsam:
`trace_explain_handler.go`'da `explainTraceInvestigation`, `traceInvestigationPrepared`, `invAnswerWithTail`;
`trace_investigate.go`'nun tamamı (`invCompareWindow` + `invCompareMin/Max` HARİÇ — takip sohbeti kullanır,
taşınmalı) ve `trace_investigate_test.go`; `SystemPromptTraceInvestigation` / `systemTraceInvestigation` (+
`promptRegistry`, `promptVersionRegistry` kaydı, `prompt_trace_investigation_test.go`); `copilot_explain_stream.go`'da
üreticisi kalmayan mekanizmalar (hazırlığın ikinci anahtarı, `onStore`, eklerin kendi linkleri, hazırlık adım
olayları). Ön yüz: `ExplainSteps.tsx`, `investigationSteps.ts` (+ testi), `ExplainEvidence.tsx` (Kanıt linkleri +
Kaynak durumu dipnotu), `explainAnatomy`'de `splitSourceFooter` ve inceleme şekli (Bulgu / güven satırı),
`api.ts`'te `explainStepFrame` / `onStep`, `CopilotExplain`'deki steps/sources durumu, tipler `ExplainStepEvent`,
`ExplainSourceStatus`, `ExplainTraceAnswer.sources`, CSS `cx-steps` / `cx-step*` / `cx-sources`,
`CopilotExplain.investigation.test.tsx`. `StateBadges` takip sohbetinde kullanılır, kalır.

**Tarihçe:** v0.10.944/948 (operatör araştırma asistanı istedi → varsayılan inceleme) → v0.10.986 (operatör kod
incelemesi tarzı cevabı tercih etti; yalnız biçim değişti, toplayıcı aynı kaldı) → v0.10.987/989 (okumasız
"Hızlı açıkla" düğmesi eklendi, sonra kaldırıldı) → v0.10.1034/1035 (kodlu yol incelemeye taşındı, geri alındı) →
bu kayıt: varsayılan da klasik.

## 2026-10-02 — "Kodu da incele" eski kanıt toplayıcısına geri döndü (v0.10.1035; v0.10.1034 kararının tersi)

**Karar (operatör: "Kodu incele kısmının da eski yapısı aynı şekilde güzel açıklama yapıyordu. Eski kanıt
toplayıcı güzeldi."):** v0.10.1034 commit'i bütünüyle geri alındı (`git revert`, uyumluluk katmanı yok). Trace'te
"Kodu da incele" yine KLASİK yoldan koşar: `buildTraceExplainInput` (trace + loglar + Oracle satırları) +
`buildCodeContext` + şema kanıtı + `SystemPromptTraceWithCode`, buffered üretim, anahtar kod çekiminden sonra.
`SystemPromptTraceInvestigationWithCode`, `trace_investigate_code.go` ve `mcptools.WithTraceLogsSink` kaldırıldı.

**Yanlış okunan şikâyet:** operatörün "kod inceleme çalışma mantığı ile direkt Ask CoSRE farklı" cümlesi "kodlu
yolu incelemeye taşı" diye okundu; kastedilen tersiydi — tercih edilen, kodlu yolun eski toplayıcısı. İpucu
kayıtlardaydı (v0.10.986: "kodu incele dediğimde daha iyi sonuç veriyor, o hali olsa daha iyi olacak";
v0.10.987: okumasız eski cevap ayrı düğme olarak istendi) ve atlandı. **Kural:** operatör iki yolun farklı
olduğunu söylediğinde yön varsayılmaz; önceki kayıtlardaki tercihe bakılır, belirsizse sorulur.

**Açık (operatör kararı):** varsayılan "CoSRE'ye sor" hâlâ v0.10.948 trace incelemesi (v0.10.986 biçimiyle);
operatör eski açıklamanın yapısını daha iyi buluyor. Varsayılanı da klasik toplayıcıya döndürmek ayrı karar —
inceleme adımları, dönem kıyası / pod / deploy kanıtı, "Kaynak durumu" künyesi ve sayı denetimi o yolla gider.
v0.10.1033 (kanıt span listesi kaldırıldı) etkilenmedi.

## 2026-10-02 — "Kodu da incele" artık Ask CoSRE incelemesinin üstüne kod ekler (v0.10.1034)

> **v0.10.1035'te geri alındı** (üstteki kayıt). Aşağısı tarihçe.

**Operatör (prod):** "Kod inceleme çalışma mantığı ile direkt Ask CoSRE farklı." **Ne farklıydı:** trace'te
iki ayrı kanıt toplayıcısı vardı. Ask CoSRE (varsayılan) trace incelemesiydi (get_trace, loglar, dönem kıyası,
pod, deploy, Oracle; seçili span'e odaklı; adımlar akar). "Kodu da incele" ise klasik toplayıcıdan
(`buildTraceExplainInput`: ≤100 span, ≤15 log) geçiyordu: kıyas/pod/deploy kanıtı yoktu, seçili span yok
sayılıyordu, istem de farklıydı (`SystemPromptTraceWithCode`).

**Karar — tek hat:** `includeCode` AYNI incelemeyi koşar (aynı okumalar, adımlar, odak; `inv.User` kodsuz
istekle bayt bayt aynı), sonra üstüne kod + şema kanıtı ekler. Stack ve onu basan servis incelemenin
get_logs_for_trace okumasının HAM kayıtlarından alınır; seçim kuralı klasik toplayıcınınki: önce yüksek severity,
yalnız ilk 15 kayıt aday (`traceExplainLogRows`, klasik yolla paylaşılan sabit — INFO'da yakalanmış bir exception
16. sırada kodu sürmez), stack taşıyan ilki. Araç çıktısı stack'i 200 runede kestiği için dikiş
`mcptools.WithTraceLogsSink` (ctx kancası; araç çıktısı ve MCP/sohbet sözleşmesi değişmez). Kanca ve stack
ayrıştırması YALNIZ kod istendiğinde kurulur; varsayılan (kodsuz) yol hiçbir ek iş yapmaz (inv.User, künye ve
çerçeve ekleri HEAD ile bayt bayt aynı — 9 senaryolu karşılaştırma).
**Seçili span geri düşüşü:** span seçiliyken L okuması o span'e süzülü; operatör çoğu zaman log basmayan bir
span'e (kırmızı CLIENT yaprağı) ya da köke tıklar, exception'ı aşağı akıştaki servis basmıştır. Seçili span'in
logunda stack YOKSA TEK ek trace geneli get_logs_for_trace (span süzgeçsiz, klasik limit 30
`traceExplainLogLimit`, L bütçesi, aynı koşucu → rol süzgeci + audit, aynı kanca) koşar; step + step-result olarak
görünür. Çıktısı inceleme kanıtına GİRMEZ, yalnız stack + servis alınır; kökeni prompt'un kod bölümünün başında ve
kod künyesinde (`code.stackOrigin`) tek satırla söylenir ("seçili span'in değil, trace'in en ciddi stacktrace'i —
basan servis: X"). Seçili span'in kendi stack'i varsa o kazanır, ek okuma yok. Ek okuma kalıcı değilse (zaman
aşımı, erişilemedi …) cevap saklanmaz. Hata metni (stack'i gömer) + SQL stack belli OLDUKTAN sonra get_trace'in
span listesinden kurulur; bütçe sırası kod > şema > SQL > log aynen.
**Kod çekimi bir adım:** `source_code` step (argüman: stack'i basan servis) + step-result (depo, dosya:satır,
gerekçe, `source` rozeti; kod İÇERİĞİ yok — "kod tarayıcıya gitmez"): 25 sn'ye varan çekim akışı sessiz bırakmaz.
**İstem:** `SystemPromptTraceInvestigationWithCode` = inceleme gövdesi + paylaşılan `systemCodeAddendum` + çerçeve;
mevcut `systemTraceInvestigation` sürüm kaydı anahtar ve metin olarak aynen, kodlu istem YENİ kayıt (gövde
literal'i kapı gereği ayrıca kayıtlı); global istem sürümü yeni istem eklendiği için değişir.
**Model çağrısı** BUFFERED, mevcut taşma zinciri (`copilotExplainEvidence`: tam kod → yarım kod — yarım da taşarsa
hata; yarıya inemezse kodsuz + "kod sığmadı" notu, düz inceleme istemiyle) değişmeden kullanılır; cevabı
incelemenin kuyruğu (sayı uyarısı + Kaynak durumu) kapatır. **Sayı denetimi kodla kandırılamaz:** cevaptaki çitli
kod blokları iddia sayılmaz; kanıta kod GÖVDESİ değil, yalnız cevabı üreten denemede GERÇEKTEN gönderilen kodun
(tam / yarım / düştüyse hiç) pencere başına hata satırı, ilk/son satır, imza satırı ve şema bloğu eklenir
(`copilotExplainEvidenceSent` gönderileni bildirir) — pencere içindeki bir satır numarasına eşit uydurma "87 ms"
yine işaretlenir. Kodsuz yol bugünkü gibi akar; iki taşma stratejisi aynı istekte hiç birlikte koşmaz (eski
çekince buydu). Önbellek anahtarı okumalardan ÖNCE (`traceInvestigationCacheKey`, kodlu istemle → kodlu/kodsuz
ayrı satır); yan kayıt kod künyesini de taşır, isabette okuma da git çağrısı da yok. Saklama kuralı incelemeninki;
ek olarak kod çekiminin GEÇİCİ çıkmazında (deadline, iptal, backend hatası, katalog okunamadı) ya da kalıcı
olmayan ek stack okumasında saklanmaz. Ön yüz: üç kod girişi (çip, `ai=code`, "Kodu da inceleyeyim mi? → Evet")
seçili span'i taşır; kod kartı adımları, kaynak dipnotunu ve köken notunu ilk kartla aynı bileşenlerle çizer.

**Değişmeyenler:** klasik kodlu/kodsuz gövde YALNIZ Tempo yedeğinde (trace ClickHouse'ta yok) koşar; exception
explain'in kod yolu, sohbet, takip soruları, arka plan açıklayıcıları dokunulmadı; kod bütçesi (4000 rune) ve
inceleme bölüm bütçeleri aynı.

**Ölçüm (bütçe tavanlarında fikstür, rune; `TestTraceCodePromptSizeCeilings`):** eski klasik + kod user prompt'u
54.349 (kanıt 48.777 + kod 4.740 + şema 832; klasik toplayıcıda span adı ve durum mesajı tavansız — fikstürde
~56 / ~180 rune, 15 log 600 bayt gövde + 1500/900 bayt stack; Oracle yok), sistem 4.423 → toplam 58.772. Yeni
inceleme + kod user prompt'u 16.199 (kanıt 10.627: T 2.237 · L 2.797 · K 2.061 · P 917 · D 736 · O 1.235;
Oracle'sız 14.964), sistem 6.546 → toplam 22.745. Kodsuz sistem istemleri: klasik 1.910, inceleme 4.033. Küçük
fikstürde (2 span, 3 log) yeni kodlu user 9.333 / eski 6.112. Çağrı sayısı (istek başına): yeni = 5 araç
çağrısı (+ seçili span'de stack yoksa 1 ek log okuması; + Oracle varsa 1 doğrudan okuma) + aynı git zinciri + 1
LLM (taşmada 2); eski = trace + log (+ Oracle) okuması + git zinciri + 1 LLM (taşmada 2). Kod sabit tavanlı kalırken kanıt bölüm bütçeli: küçük yerel modelde
kodlu istem eski klasik yolun tavanından kısa, kodsuz incelemeden (14.660) ~8 bin rune uzun (kod 4.740 + şema
832 + istem farkı 2.513).

Bu kayıt v0.10.948 (trace_explain_handler.go başlığı) ve v0.10.989 kayıtlarındaki "iki yol: varsayılan inceleme
ve 'Kodu da incele' (klasik istem + kod bağlamı)" ifadesinin yerine geçer.

## 2026-10-02 — AI paneli: "Kanıt span'leri" listesi kaldırıldı (v0.10.1033)

**Operatör (prod, trace'ten açılan AI paneli):** "Kanıt span'lere gerek yok." Açıklamanın altındaki "Kanıt span'leri (N)"
bölümü (≤ 6 satır ham hex span kimliği, "Waterfall'da bu span'e git") gürültüydü; kimse okumuyordu.
**Kaldırılan:** o bölüm (`AIDrawerBody`, trace/span özneleri) ve yalnız ona hizmet eden span-seçim köprüsü (`useAiFocus` +
`Trace.tsx` dinleyicisi). Kartın Kanıt satırı olmayan listeyi göstermiyor: "Kanıt: N span · waterfall'da kutulu"; exception
varyantı ("kimlikler çekmecenin altında") liste durduğu için aynen, yalnız-Oracle satırı ipucusuz.
**Kalan:** v0.9.408 waterfall kutulaması (`onEvidence` → `emitAiEvidence` → `Trace.tsx` → `.wf-evidence`; operatör: "kök neden
soruşturulması gereken kısımlar kutulanmıyor"), exception'ın "Kanıt trace'leri" listesi ve kimliklerin takip sohbeti bağlamı
(`buildExplainContext`, "Kanıt span'leri: …" ≤ 10; operatöre görünmez, "Hangi kanıta dayanıyorsun?" çipini besler).
**Neden yalnız ön yüz:** kimlikler sunucuda türetiliyor (`invEvidenceSpans`: hata span'leri ≤ 5 + en büyük öz süre), modelden
istenmiyor; alan cevap çerçevesinde, istemde değil — kaldırmak tek prompt token'ı kazandırmaz, kutulamayı kırardı. Bilinen
boşluk (önceden var): kiosk görünümü (`TraceKiosk`) kutulamayı bağlamıyor. Pin: `AIDrawerBody.evidence.test.tsx`.

## 2026-10-02 — Problems: anomali ve alarm kuralı satırı tam sayfa detay açar (v0.10.1032)

**Köken (operatör, prod):** "Anomali ve alert rule'lara girdiğimde drawer çıkıyor. Exception gibi detay
gözükmüyor." Problems kuyruğunda (`/inbox`) exception satırı v0.9.341'den beri tam sayfa açıyordu; alarm
kuralı ve anomali satırı 560px triyaj çekmecesini (başlık + kök-neden şeridi + birkaç düğme). Çekmece alarm
kuralında VAR OLAN bir tam sayfayı saklıyordu: v0.9.837'den beri aynı sayfa `?problem=` ile
AlertProblemDetail'i barındırıyor, ona yalnız çekmecenin "Open source →"u götürüyordu. Anomali olayının ise
çekmeceden zengin hiçbir sayfası yoktu (yalnız /anomalies çekmecesi). v0.9.341 yorumundaki "ikisinin de
çekmecenin sakladığı daha zengin bir hedefi yok" cümlesi v0.9.837'de yanlışlaşmıştı.

**Karar:** satır tıkı (ve klavye Enter/o) türe göre: exception ailesi `/problems?exc=` (değişmedi); alarm
kuralı `?problem=<id>`, anomali `?anomaly=<id>` — ikisi de YERİNDE tam sayfa (kuyruk `display:none` ile
mount'lu kalır, "← Problems" / Esc geri döner). `?problem=` ile `?anomaly=` karşılıklı dışlayıcı: biri açılınca
öteki ve `?item=` silinir, ikisi birden gelirse problem okunur. Karar saf ve tablo testli
(`lib/inboxHref` `inboxRowOpen` / `withInboxDetail` / `readInboxDetail`). Detayı kapatmak `problem`,
`anomaly` ve `item`'ın HEPSİNİ siler (elle yazılmış link geri dönüşte başka detaya / çekmeceye sıçramaz);
detay açıkken gizli kuyruğun (ve Alert rules tablosunun) klavye gezinmesi kapalı. `inboxItemHref` anomali
için artık `/inbox?anomaly=` (servis dikkat şeridi ve "Open source" oraya iner). Bu, v0.8.292 / v0.9.341'in
"exception dışı türler çekmecede" kararını BU İKİ TÜR için geçersiz kılar.

**Anlaşılırlık (operatör, aynı sürüm):** "Anlaşılır olsun. Çok detay verince daha anlaşılır olmuyor — alert ve
anomaliler de." İki sayfanın ilk satırı tek Türkçe cümle + "ne zaman" satırı (saf kurucular `detailSummary.ts`;
eksik alanda daha sade cümleye düşer, NaN / "0 katına" / boş parantez basmaz; bitmiş olayda bitişi söyler;
`anomaly:` önekli kuralda ikinci sayı "eşik" değil "olağan değer"). Hemen altında TEK satırlık triyaj — kutu ve
paragraf yok: alarmda "Atanan ‹çip› [Assign…] · Gerçek problem mi? [Gerçek problem] [Problem değil]",
anomalide "[süre] [Mute…] · Gerçek problem mi? …"; uzun öğretme açıklaması yerine tek kısa cümle ("Aynısı
yeniden gelirse aynı sınıfa düşer; geri alınabilir.", bildirim cümleciği yalnız yönetici politikası açıkken).
Triyaj çekmecesi uzun biçimi korur; exception detayı aynı kompakt satırı kendi kartında kullanır.
Anomali sayfası yalın: özet, türün TEK grafiği (aşağıda), kapalı kök-neden şeridi, "Ne yapabilirim" (log /
trace / servis); gerisi tek kapalı "Teknik ayrıntı"da; tür rozeti ve grafik başlığı düz Türkçe. Alarm sayfası:
kök neden / blast radius / correlated signals görünür; Metric (özet değeri zaten söylüyor), zaman çizelgesi,
Bildirim, Runbook, Description kapalı gelir (`Sect collapsible`, kapalıyken mount edilmez → bildirim geçmişi
ve runbook koşuları açılınca çekilir). "Log kanıtı" zaten kendi açıcısıyla kapalı; ikinci kapak eklenmedi.
Kök-neden kartındaki kural adı satırı kalktı (özet onunla başlıyor). Hiçbir bölüm silinmedi.

**Seyir grafiği:** mevcut CosreChart (tek sınırlı span sorgusu, yoklama yok). `trace_op` için HATA SAYISI (agg
`errors`; sunucu `aggToSQL` zaten destekliyor) — dedektör hata sayısını tabanla kıyaslar; hata ORANI çizmek,
yüzdesi sabitken trafiği artan operasyonda "5 katına çıktı"nın altına düz çizgi koyardı. `trace_op_latency` →
p99; davranış → kayan metrik; dış kaynak / ayrıştırılamayan kanıt / log türleri → Seyir yok (log türlerinin
grafiği log hacmi). Başlık / lejant / boş notu kodun verdiği `presentation` prop'u taşır, spec DEĞİL (sohbet
çitinin modelin yazabildiği spec'i başlık belirleyemez — pin güncellendi, değişmez aynı). Pencere dakikaya
yuvarlanır (açık olayda sorgu anahtarı kaymaz) ve giriş en çok 6 sa.

**Tekil okuma zenginleşti (backend):** `GET /api/anomalies/event` artık liste ucuyla AYNI zincirden geçer
(cluster → son deploy → kök-neden özeti → karar; `internal/api/anomaly_event_get.go`
`enrichAnomalyEvents`, sıra ve paylaşım Go testli). /inbox liste önbelleğini hiç doldurmadığı için tam
sayfanın ana yolu bu okuma ve kök-neden çipi hipotez varken "no clear cause yet" diyordu. Handler api.go'dan
taşındı (api.go küçüldü, taban indirildi). Sayfada tekil okuma HER ZAMAN koşar; /anomalies liste önbelleği
yalnız anında ilk boyamadır (eskiden okumayı kapatıyor, bitmiş olayı "sürüyor" diye donduruyordu).

**Taşınanlar (hiçbir yetenek kaybolmasın):** alarm sayfasına Assign… + "Gerçek problem / Problem değil"
(Acknowledge şeritte kaldı, artık kuyruğu da tazeler; çekmecedeki P-xxxxx görüntü kimliği şeritte); anomali
sayfasına Mute… (çekmeceyle ortak gövde kurucusu `anomalyEventSilenceBody`) + öğretme; exception detayına da
öğretme (v0.10.1015'ten beri bir exception arayüzden "problem değil" diye işaretlenemiyordu).
`ProblemVerdictActions` InboxItem olmadan da sürülür; kayıttan kurulan imza (`problemSignature` /
`exceptionSignature` / `anomalySignature`) satırdan kurulanla aynı dizgedir (test alan alan çiviler).
`AlertProblemHost` iki sayfada da kimlikle anahtarlı; `useProblemByID` önceki problemin kaydını yer tutucu
olarak göstermez.

**Değişmeyenler:** incident satırları ve eski `?item=` linkleri çekmeceyi açar; /anomalies (ve servis Overview)
çekmecesi yerinde — gövde parçaları tam sayfayla ortak modüle (`anomalyDetail.ts` / `anomalyDetailParts.tsx`)
çıktı ve "Tam detay →" bağlantısı kazandı; /problems exception yönlendirmesi.

**Ertelenen (kuyrukta):** Mute'un satırı kuyruktan düşürmemesi (/api/inbox tarafı), tarayıcı Geri anlamı,
alarm sayfasının İngilizce bölüm başlıkları, anomali "Error logs" / "Desenler" pivotları, alarm özetinde birim.

## 2026-10-02 — "Coremetry · disk dolacak" alarmı varsayılan kapalı (v0.10.1031)

**Operatör (prod, ekran görüntüsüyle):** "Disk dolacak niye geliyor, gerek yok." Problems sekmesinde disk/düğüm
başına birer "Coremetry · disk dolacak" satırı (kaynak alarm kuralı, RESOURCE, servis yok; ör.
`self.disk_eta_days = 6.83 / 7.00 · warning`).

**Kural ne yapıyordu:** `self-disk-eta` (`internal/evaluator/selfhealth.go`, v0.9.1279) liderin belleğinde
(host, disk) başına son ≤ 6 saatin (360 × 1 dk) kullanılan bayt serisini tutar, `forecast.Fit` ile düz OLS doğrusu
uydurur (≥ 4 nokta, ≥ 30 dk, R² ≥ 0.6) ve "kaç gün sonra dolar" projeksiyonu `diskEtaDays`'in (varsayılan 7)
altına inince disk başına problem açar (< 2 gün critical).

**Neden yanıltıyor:** projeksiyon retention TTL'ini bilmiyor — girdisi yalnız seri + kapasite; son altı saatin
eğimini ileri uzatıyor. Tablolar gün bölümlü ve TTL süresi dolan parçaları topluca düşürüyor; disk doluluğu bu
yüzden testere dişi: gün içinde ingest'le düzgün tırmanır (yüksek R²), eski bölüm düşünce iner. Altı saatlik
pencere çoğu zaman yalnız tırmanışı görür ve "6.8 gün sonra dolacak" der; dengede retention aynı hızda siler.
Düz/inen ya da gürültülü seride ETA üretilmez, ama tırmanış yeniden başlayınca satır yeniden açılır — TTL'li
depoda yapısal yanlış pozitif.

**Karar:** `SelfHealthConfig.DiskEta *bool` (`diskEta`), **nil = KAPALI** — bilinçli varsayılan değişikliği,
emsal `service_silent` (v0.10.543, "olmasınlar"). Sahadaki kayıtlı bloblar alanı taşımaz → kapalı okunur;
`patchSelfHealth` alana dokunmaz. Kapalıyken `selfDiskETA` diski yine okur, kalıcı seriyi
(`coremetry.self.disk_used_bytes`, v0.10.911) yazar, bellek serisini tazeler; yalnız problem üretmez ve ok=true
döner (kural kapsanmış, hiçbir şey istemiyor). Kapı saf `diskETAProblems`'ın ilk satırı — ısınma taşıması
(`diskCarryOver`) da arkasında. **Açık satırlar:** bir sonraki evaluator tikinde (≤ 1 dk) `resolved` olur
(açıklamaya ek yok; self-health reconcile çözümde bildirim göndermez). Satırların bağlı olduğu incident'ta başka
açık problem kalmadıysa incident kaskadı onu kapatır ve olağan incident "resolved" bildirimi gider (incident
başına bir). Acknowledged satırları reconcile kapatmaz; tazelenmedikleri için bayat süpürme ~3 tikte "source
silent" ekiyle kapatır. **`/admin/stats` rozeti:** açık problem kalmayınca v0.10.911 yolu — 7 günlük saatlik
kalıcı tarihçeden tahmin (problem bağlantısı yok); pencere günlük düşüşleri de içerdiği için net eğilimi görür.

**Yeniden açmak:** `PUT /api/settings/self-health` (admin). Uç blobu BİRLEŞTİRMEZ, gönderileni aynen kaydeder —
yalnız `{"diskEta":true}` özelleştirilmiş eşikleri varsayılana döndürür. Doğrusu: `GET` cevabına
`"diskEta":true` ekleyip tamamını PUT etmek; varsayılan kurulumda
`{"enabled":true,"ingestStallMin":10,"spoolMaxFiles":100000,"spoolMaxBytes":10737418240,"diskEtaDays":7,"channelConsecFails":3,"volumeSpikeFactor":4,"volumeSpikeMinSpans":100000,"diskEta":true}`.
Bir sonraki tikte devrede; bellek serisi kapalıyken de beslendiği için ısınma beklemez (lider değişimi hariç).
Denetim kaydı `settings.self_health.update` artık `diskEta`'yı da yazar (api.go satır sayısı aynı). PUT tam
değiştirme olduğu için `diskEta`'yı içermeyen SONRAKİ her PUT (ör. 1031 öncesi blobu gönderen eski bir betik)
kuralı sessizce yeniden KAPATIR; GET-değiştir-PUT güvenlidir, çünkü alan bir kez yazıldıktan sonra GET
`"diskEta":true` döndürür.

**Değişmeyenler:** diğer dört self-* kuralı, eşikler (`diskEtaDays` — kural açılınca geçerli), runbook haritası,
kuralın kodu, `/admin/stats`, ön yüz (self-health ayar sekmesi hâlâ yok). **Sonuç (bilinçli kabul):** kural
KAPALIYKEN Coremetry'nin kendi ClickHouse diski gerçekten dolarken hiçbir şey önceden alarm vermez —
`db_capacity` yalnız dış veritabanlarını kapsar; operatörün göreceği ilk problem `self-ingest-stall`, yani
kesintinin kendisi olur. Geriye kalan tek erken sinyal `/admin/stats` disk tahmin rozetidir; bakılması gerekir,
bildirim göndermez. Pin: `chstore/selfhealth_disk_eta_switch_test.go`,
`evaluator/selfhealth_disk_eta_switch_test.go` (saf tablo + "anahtar seri yazımından SONRA" kaynak pini).

## 2026-10-02 — "Yeni log şablonu" anomalisi yalnız gerçekten yeni şekilde açılır (v0.10.1030)

**Operatör (prod, ekran görüntüsüyle):** "Çok fazla problem geliyor." Problems sekmesi (v0.10.1014'ten beri
her türü gösteriyor) `log_template_new` satırlarıyla doluydu: TEK servis için on+ satır, hepsi aynı saniyede
doğmuş, hepsi "peak 0.0x · now 0.0x · no signal", desenleri aynı uzun JSON önekiyle
(`{"Timestamp":"<*>","Level":"Warning","MessageTemplate":"U…`) başlıyor.

**Kök neden:** dedektör (`anomaly/log_templates.go`) `log_templates` defterinde pencerede (2 × 5 dk) doğmuş ve
`total_count ≥ 3` olan HER şablonu ayrı olay yapıyordu (parmak izi = şablon kimliği). Defteri yazan puller
her 5 dk'da ~1000 satırlık ÖRNEKTEN Drain ağacını soğuk kurar (`Reset`); küme kimliği şablon belirteçlerinin
sha1'i ve her inceltmede YENİDEN hesaplanır. Aynı satır ailesi her tikte hangi satırlar örneklendiyse ona
göre farklı `<*>` konumlarında biter → yeni kimlik → `first_seen = şimdi` olan yeni defter satırı → "yeni
şablon"; serbest metinli uzun JSON satırlarında çok daha sık. Kimlik eşitliği "yeni" için kurgu gereği
kararsızdı.

**Karar:** "yeni" = servisin BİLİNEN hiçbir şablonu onunla Drain'in kendi kuralına göre aynı kümeye
girmezdi. (a) `templater.SameTemplateFamily(a, b)` (ayrılmış hâli `ParseTemplate(..).SameFamily`): saklanan
dizgeyi Tokenize'ın ayırıcılarıyla geri böler (yalnız boşluk/sekme, yeniden maskeleme yok); eşit belirteç
sayısı + uyumlu yönlendirme öneki (ilk Depth − 1 = 3 belirteç) + mevcut `similarity ≥ 0.4`; ayarlar
`NewDrain` ile ORTAK sabitler. Yaprakta `<*>` iki tarafta da eşleşir (iki girdi de şablon). Yönlendirmede
Drain değişmezi KENDİ çocuğuna yollar, "*"a yalnız maske ya da MaxChildren taşması düşer: konumlar birebir
aynı olmalı (`<*>` == `<*>` dahil); `<*>` ↔ değişmeze yalnız iki şablon bir yönlendirme konumunda aynı
DEĞİŞMEZİ paylaşıyorsa izin var (taşma) — öneki tümü joker bir şablon aynı boydaki her şeyi yutamaz. (b)
Tik başına en çok iki okuma, AYNI pencere başıyla ve tam tümleyen (`first_seen >=` / `<`, ns kesin bind
`fromUnixTimestamp64Nano`; konumsal `time.Time` saniyeye kesilirdi). Aday: `first_seen` pencerede (eskiden
`last_seen` üzerinden; puller örneği yeniden eskiye okuduğundan `first_seen > last_seen` olan satır iki
kümeye de girmiyordu), `total_count ≥ 3` SQL'de, en ERKEN doğan önce, 500 satır. Bilinen (yalnız aday
varsa): `first_seen <` pencere başı, `total_count ≥ 3` (bir-iki kez görülmüş blip "bilinen" olup kendi
gerçek ≥3 şablonunu 7 gün bastırmasın — v0.9.47 sözü), `last_seen` son 7 gün, adayların servisleri
(`hasAny`; servissiz aday varsa `OR empty(services)`), adayların belirteç sayıları (`countSubstrings(template,
' ') + 1`; saklama biçimi tek boşluk olduğundan kesin), `last_seen DESC`, 500 satır. Süzgeçler SQL'de
(`ListLogTemplatesFilter`'a isteğe bağlı alanlar + `first_seen_asc` sırası) ki LIMIT ilgili satırlara
harcansın (`ListLogTemplates` 500 üstünü sessizce 100'e indirir). Go'da aynı sınırlar emniyet kemeri olarak
tekrar. (c) En az bir servisi paylaşan bilinen bir şablonun ailesi olan aday bastırılır; servissiz aday
okunan her bilinenle karşılaştırılır. (d) Kalanlardan aynı tikte aile başına TEK aday: en erken
`first_seen`, sonra ID — temsilci kayıtçı tikleri arasında kararlı kalmalı; `total_count` her puller
tikinde üzerine yazıldığından sıralamaya girmez. Olayın servisi şablon servislerinin sözlükte en küçüğü
(`Services[0]` varış sırasını izleyip parmak izini değiştiriyordu). (e) Bilinen okuması düşerse
FAIL-CLOSED: bu tik bu dedektörden hiçbir şey çıkmaz, hata tik başına bir kez loglanır, kayıtçı tiki
düşmez (kaçan bir not selden ucuz; aday 10 dk pencerede kalır, sonraki tik yakalar). Bastırma olan tikte
tek özet log satırı, yalnız sayılar.

**Değişmeyen:** templater'ın öğrenme/sıfırlama davranışı, defter şeması, olay biçimi ve parmak izi
formülü, kayıtçının öbür dedektörleri, API ve ön yüz.

**Sınırlar:** Drain'in mevcut bir şablonla aynı kümeye koyacağı gerçekten yeni bir satır (aynı yönlendirme
öneki, aynı belirteç sayısı, belirteçlerin ≥ %40'ı ortak — ör. "established" yerine "failed") Drain'in
tanımıyla yeni biçim DEĞİLDİR ve duyurulmaz. Satırları BELİRTEÇ SAYISINDA oynayan aileler (serbest metinli
mesajlar) her sayı için ayrı ailedir: her sayı bir kez duyurulur, yani bir miktar tekrar kalabilir.
Kabalık: sabit önekli metin biçimlerinde aynı kelime sayılı, aynı seviye/logger önekli kısa mesajlar 7 gün
boyunca tek aile sayılır (Drain'in 0.4 benzerliği tikler arası uygulanıyor) — daha az satır yönünde
bilinçli bir takas. Bilinen kümesi 500 satır: 7 gün susup dönen aile yeniden "yeni" sayılır; o servis ve
sayılarda 500'ü aşan daha taze şablon varsa eski bir ailenin varyantı çıkabilir (yine aile başına tek).
Mevcut sel satırları artık yeniden yazılmıyor: durumları kendi `last_seen`'lerinden 10 dk sonra "cleared"
olur, yani varsayılan Problems görünümü (`status=open`, yalnız aktif) onları dağıtımdan en geç ~10 dk sonra
göstermez; "all" görünümünde 24 sa "cleared" (P3) kalır, tablo TTL'i 30 gün (`started_at`). Temizlik işi
eklenmedi.

## 2026-10-02 — OpenTelemetry 1.45.0: GO-2026-6505 (v0.10.1029)

**Köken:** CI'ın govulncheck adımı GO-2026-6505'te kırıldı (yayın 2026-10-01, geri çekilmedi):
"OpenTelemetry-Go: Exporter config logging may leak endpoint URLs in info logs". `otel/sdk` < 1.45.0 ve
`otlptrace`/`otlptracegrpc` < 1.45.0, TracerProvider kurulurken yapılandırmasını OTel iç günlüğüne
("TracerProvider created", `global.Info` = V(4)) yazıyor; satırda dışa aktarıcının uç adresi var. Düşük
önem: bize yalnız self-observability'den (`internal/selfobs`, `COREMETRY_SELF_OBS_OTLP_ENDPOINT`;
govulncheck izi `selfobs.go:132` `otlptracegrpc.New`, `:150` `WithBatcher`) erişiliyor ve OTel'in varsayılan
günlükçüsü V(4)'ü basmıyor (repoda `otel.SetLogger` yok) — pratikte uyuyan sızıntı, ama erişilebilir.

**Karar (operatör: "1 evet yükselt"):** en küçük yükseltme — yalnız `otel/sdk`, `otlptrace`, `otlptracegrpc`
→ v1.45.0, ardından `go mod tidy`. Taşınan modüllerin TAMAMI:

| Modül | Eski → Yeni | Kim zorladı / not |
|---|---|---|
| `otel`, `otel/metric`, `otel/trace`, `otel/sdk`, `otel/sdk/metric` | v1.44.0 → v1.45.0 | çekirdek birlikte (beklenen) |
| `exporters/otlp/otlptrace/otlptracegrpc` (doğrudan), `otlptrace` (dolaylı) | v1.32.0 → v1.45.0 | düzeltmenin kendisi |
| `otel/metric/x` (yalnız modül grafında) | v0.66.0 → v0.67.0 | `sdk/metric` 1.45.0 |
| `proto/otlp` | v1.10.0 → v1.11.0 | `otlptrace(grpc)` 1.45.0; YALNIZ yorum farkı, tanımlayıcı baytları aynı → alıcının (`internal/otlp`) tel biçimi değişmedi |
| `genproto/googleapis/rpc` (doğrudan), `/api` (dolaylı) | `20260526163538` → `20260803160001` | `otlptracegrpc` 1.45.0; rpc kodu bayt bayt aynı, api'de yalnız go.mod |
| `grpc-gateway/v2` (dolaylı) | v2.28.0 → v2.29.0 | `otlptracegrpc` + `proto/otlp`; yalnız bağlanıyor, kullanılmıyor; fark isteğe bağlı yeni seçenek |
| `go-logr/logr` (dolaylı) | v1.4.3 → v1.4.4 | otel 1.45.0 |
| `cenkalti/backoff/v5` (YENİ, dolaylı) | — → v5.0.3 | `otlptracegrpc` yeniden denemesi v4 → v5; v4.3.0 `otlpmetricgrpc` için kalıyor |

**Bilinçli DOKUNULMAYANLAR:** `otlpmetricgrpc` v1.32.0 (advisory'de yok), `contrib` otelhttp v0.61.0 /
runtime v0.57.0, grpc v1.83.2, x/net v0.58.0, x/sys v0.47.0, protobuf v1.36.11, auto/sdk v1.2.1 —
hiçbiri sürüklenmedi. `go 1.25.0` aynı, `toolchain` satırı yok. Kaynak kodda değişiklik yok.
`selfobs.go`'nun kendi `[selfobs] enabled — endpoint=…` satırı da uç adresini yazıyor; advisory'nin
konusu değil, dokunulmadı (ayrı karar).

**Davranış farkları (exporter 1.32 → 1.45, sdk 1.44 → 1.45):** açılış yolu aynı — `otlptracegrpc` iki
sürümde de tembel `grpc.NewClient`; yeni öz-ölçüm (`observ.NewInstrumentation`) `OTEL_GO_X_OBSERVABILITY`
yokken no-op (chart/compose'da yok). Zaman aşımı, sıkıştırma, yeniden deneme varsayılanları aynı; yeni 64 MiB
istek tavanı (512 span'lik batch'in çok üstü). Kısmi başarı artık `otel.Handle` yerine hata olarak döner
(yalnız günlük). `Span.Flags` W3C trace-flags bitlerini de taşır (alıcımız `Flags` okumuyor).
`resource.Default()` şeması semconv 1.41.0 → 1.43.0; `buildResource` `NewSchemaless` kullandığı için
Merge çakışmaz.

**Doğrulama:** Go 1.25.0 ve 1.25.14 (CI'ın `'1.25'`i) ile `go build ./...` + `go vet ./...`; yerel
1.26.2 ile build/vet, `go test ./...` (FAIL yok), `make audit` temiz, gofmt temiz. govulncheck v1.7.0 +
Go 1.25.14: önce GO-2026-6505, sonra 0 erişilebilir bulgu (erişilemeyen 4 modül bulgusu — x/crypto v0.55.0
×3, klauspost/compress v1.18.3 — önceden de vardı). Eski/yeni karşılaştırması: kapalı port ve çözülmeyen
adla `selfobs.Init` iki tarafta < 1,1 ms, paket init toplamı < 1,5 ms, kapanış aynı; OTel günlükçüsü V(8)'de
eski sürüm "TracerProvider created" satırına uç adresi yazıyor, yeni yazmıyor.

**Doğrulanmayan:** prod açılışı. 2026-07-16 olayı kuralı gereği operatör deploy edip pod'ların sağlıklı
ayağa kalktığını (ready, restart yok, `[selfobs] enabled` satırı) teyit edene dek bitmiş sayılmaz.
Geri dönüş: v0.10.1028'i yeniden deploy.

## 2026-10-02 — Karşılaştırma okumalarında ortak kova hatası: kalan yüzeyler (v0.10.1028)

**Köken:** v0.10.1025 /databases ve /messaging listelerinde "önceki pencere"nin `[from − (to − from),
from)` diye kurulduğunu, current okumanın ise alt sınırı 5 dk MV kovasına indirdiğini (`time_bucket >=
floor5(from)`) buldu: hizasız `from`'da (10:03) 10:00 kovası İKİ pencerede sayılıyor, her delta sıfıra
doğru sulanıyordu. Düzeltme tek saf kuraldı (`chstore.PriorWindow`). O incelemenin listelediği ama
bakılmamış aynı biçimli yüzeyler bu sürümde tek tek, okuyucunun SQL sınırlarından okunarak sınandı:

| Yüzey | Current okuma sınırı | Hüküm | Değişiklik |
|---|---|---|---|
| `/databases/statements/detail?compare=prior` (`dbstmt_detail.go`) | `db_statement_summary_5m`, `>= From.Truncate(5m)`, `< to` | ETKİLİ (hizasız from) | `chstore.PriorWindow` |
| `/endpoints?compare=prior` MV yolu (`api.go`) | `spanmetrics_10s` (< 2 sa ve ≤ 24 sa yaşında) / `spanmetrics_1m`; alt sınır İKİ katmanda `floor1m(from)`, `< to` | ETKİLİ (hizasız from; 10 sn katmanında 6 kovaya kadar) | `chstore.EndpointsPriorWindow` |
| `/endpoints` ham yol (cluster / env) | `spans`, `time >= from AND time <= to` | ETKİLENMEZ (yalnız `from` ANI) | birebir kayma korunur, dal `forcesRaw` ile seçilir |
| `/endpoints/metric` (`endpoints_metric.go`) | `metric_points` ham satır sınırı (delta) / emit kovası `>= from`, seed atılır (kümülatif) / VM `query_range` start örneği atılır | ETKİLENMEZ (ortak kova yok; kümülatifte < 1 adımlık sayılmayan aralık) | yok |
| `/services?compare=prior` (`api.go`) | MV: `service_summary_5m` / `service_env_summary_5m`, `>= alignBucketStart(from)`, `< to`; ham: `spans` kapalı sınırlar | YOLA BAĞLI: MV yolu ETKİLİ, ham yol (pencere < 5 dk ya da env MV'nin kapsamadığı cluster/env) değil | `servicesPriorWindow` (MV → PriorWindow, ham → birebir kayma; aynı `useMV`) |
| `/topology/service` + `/servicegraph` (global + odak) | `topology_edges_5m FINAL`, `>= toStartOfFiveMinute(from)`, `< toStartOfFiveMinute(to) + 5 dk` (to'nun kovası dahil) | ETKİLİ, HER pencerede — hizalı from dahil (okuyucu üst ucu dışa yuvarlıyor) | `chstore.TopologyPriorWindow` |
| AI analiz baseline'ı (`copilot_aianalyze.go`) | `ServiceWindowRED`: `service_summary_5m`, `>= alignBucketStart(from)`, `< to` | ETKİLİ | `chstore.PriorWindow` |
| Deploy raporu önce/sonra (`deployment_report.go`) ve Rollouts çekmecesi V1 + V2 (`rollout_detail.go`, `RolloutDrawer` önce → sonra throughput; 6 sa kelepçe) | aynı `service_summary_5m` okuyucusu; deploy/rollout anı neredeyse hiç kova sınırında değil | ETKİLİ | ortak `redComparisonPlan`: before = `PriorWindow(since, end)` + iki tarafın throughput paydası |
| Log kalıpları tabanı, `GetCorrelatedChanges(MV)` (vardiya sayfası), `ServiceBubbleUp`, MCP `compare_periods` "previous", guided `window_compare` | ham log araması / tek taramada hizalı `is_cur` / ham spans tek an / `periodGrid` / kullanıcının iki mutlak penceresi | ETKİLENMEZ | yok |

**Kararlar:** (a) Kuralın tek uygulaması `PriorWindowGrid(from, to, bucket)` (`pTo = floorB(from)`, `pFrom =
pTo − N × B`, `to` saniyeye iner); `PriorWindow` onun 5 dk hâli (testle ≡). `bucket ≤ 0` = ızgarasız ham
okuma: birebir süre kaydırması. (b) `/endpoints` MV okuyucusu alt sınırı katmandan bağımsız DAKİKAYA
indiriyor, kova greni ise 10 sn ya da 1 dk: 5 dk kuralı orada yanlış olurdu, düz 1 dk kuralı da varsayılan
yolda (1 sa = 10 sn katmanı) prior'u 50 sn'ye kadar uzun okurdu. `EndpointsPriorWindow`: `pFrom =
floor1m(from) − n1 × 1 dk` (dakika hizalı — okuyucu prior'un From'unu da indiriyor), `pTo = pFrom + n10 ×
10 sn`; iki katmanda da ortak kova yok ve kova sayısı eşit (1 dk katmanında ceil(n10/6) = n1). Bedeli 10 sn
katmanında prior ile current arasında en çok 50 sn'lik okunmayan aralık — bitişiklik değil eşit kova sayısı
seçildi. Ham yol aynı `forcesRaw` yüklemiyle seçilir. (c) Topoloji okuyucusu `to`'nun kovasını da okuyor;
`TopologyPriorWindow` current'ın kova kümesini (`floor5(from) … floor5(to)`) PriorWindow'a verir ve
okuyucunun ARGÜMANINI döndürür (pTo = prior'un son kovasının etiketi); prior grafın dakika paydası current'ınki
(aynı kova sayısı — argüman bir zaman aralığı değil). (d) **Önce/sonra throughput paydası** (deploy raporu ve
Rollouts çekmecesi, TEK saf plan `redComparisonPlan(since, end, now)`; eski `redComparisonWindow` kalktı):
throughput = sayı ÷ o tarafın GERÇEKTEN kapsadığı süre. Before N tam kova okur → N × 300 sn. After
floor5(since)'ten (deploy kovasının deploy öncesi dakikaları dahil) okunan son kovanın sonuna ya da now'a —
hangisi önceyse — kadar veri taşır → `min(now, floor5(since) + N × 5 dk) − floor5(since)`, en az 1 sn
(çekmecenin 6 sa kelepçesinde son kova tamdır; canlı pencerede now'da biter; sürücünün saniye kesmesi
yalnız hangi kova etiketlerinin okunacağını belirler, N'ye öyle girer). Düz yükte iki taraf her an aynı
hızı verir. Eski paydalar (iki tarafta `end − since`; çekmecede before için pencere boyu) bu pencerelerle
sahte sıçrama/düşüş basardı — düz 100 rps, rollout 10:03:17, 2 dk sonra: eski pencere+payda "250 → 264",
yeni pencere + eski çekmece paydası "100 → 264" (+%164), yeni pencere + eşit payda (bu sürümün ilk
taslağı, rapor) "500 → 264" (−%47); plan "100 → 100". Bu satır Rollouts çekmecesinde görünür.
(e) Her yüzeyin yumuşak-hata davranışı aynen (prior düşerse delta yok, 500 yok); hiçbir yüzeyde current okuma, alan adı ya da URL paramı değişmedi. `api.go`
3 satır kısaldı (taban 11609 → 11606). `PriorWindowGrid` girdi kısıtı: kova 86400 sn'yi TAM bölmeli (Go
Truncate ızgarası 1. yıla, ClickHouse'unki epoch'a göre; arada 719162 tam gün) — bölmeyen kova (7 sn)
birebir süre kaydırmasına düşer; pencere boyu Duration taşma aralığının çok altında varsayılır.
**Doğrulama:** tablo + 5000 pencerelik özellik testleri (okuyucu modeli, kaynak pinleri; dbstmt'te gerçek
builder'ın bağ argümanlarıyla), önce/sonra planı için düz-yük testi (iki çağıranın kelepçesiyle; 30 sn /
2 dk / 10 dk / 1 sa / 7 sa sonra, hizalı an, saniye-altı now: iki taraf 1e-9 içinde eşit), mutasyon kontrolü; yerel ClickHouse
(okuyucunun birebir yüklemi): topoloji 10:03–11:03 ve HİZALI 10:00–11:00 — eski prior 1 ortak kova
(10:00), yeni 0, 13 = 13 kova; /endpoints 10:03:35–11:03:35 — 10 sn katmanı eski 4 ortak / yeni 0, 364 =
364 kova; 1 dk katmanı eski 1 / yeni 0, 61 = 61.

**Görünür etki:** hizasız pencerelerde (hazır aralıklarda `to = now`, yani pratikte hep) deltalar biraz
BÜYÜR — artık sulanmıyor. Örnek: /services 15 dk, trafik 10:00'da ikiye katlandı → eski "+%60", yeni
"+%100"; topoloji 1 sa → eski "+%86", yeni "+%100". /endpoints'te ortak kısım en çok 1 dk: 1 sa'te
≈ %1,6, 15 dk'da ≈ %6'ya kadar sulanma kalkar. Deploy raporu ve Rollouts çekmecesinde rollout'tan hemen
sonraki "sonra" throughput'u artık deploy kovasının fazla dakikalarıyla şişmez; düz yükte önce = sonra.

**Bilinçli YAPILMAYANLAR (ayrı karar ister, açık kalemler):** v0.10.1025'in canlı kenar ölçeği
(`PriorCoverage`) ve okunabilirlik kapıları (`PriorReadable` + kaynak kapsama probu) bu yüzeylere
EKLENMEDİ — görünür davranış değişikliği. Durum: (1) **canlı kenar sayaç yanlılığı** (prior N tam kova,
current'ın son kovası doluyor; yalnız sayaçlar): dbstmt detayı PriorCalls/PriorErrors (düz yükte 15 dk
−%12,5, 1 sa −%3,8); /services PriorSpanCount (aynı); topoloji PriorCalls/PriorErrors (`to`'nun kovası
canlı agregatörün dakikada bir yeniden yazdığı kova: 15 dk ≈ −%15, 1 sa ≈ −%5); AI baseline Spans/Errors/
rate (30 dk ≈ −%7); /endpoints küçük (10 sn katmanı 1 sa ≈ −%0,1, 15 dk ≈ −%0,5; 1 dk katmanı 2 sa ≈
−%0,4). /servicegraph yalnız p99 birleştirir — sayaç yanlılığı yok. Deploy raporu / Rollouts throughput'u
bu listede DEĞİL: paydası (d) kapsamayı zaten ölçüyor. (2) **Saklama /
kapsama kapısı yok:** topoloji + servicegraph (`topology_edges_5m` TTL 14 gün → 7 günden uzun pencerede
prior kısmen silinmiş, sahte kötüleşme); /endpoints MV (`spanmetrics_1m` 30 gün → ~15 günden uzun pencere)
ve ham yol (span saklaması: env + 7d prior'u silinmiş aralığa koyar); dbstmt ve /services (90 gün; MV'nin
ilk kovası probu da yok — taze kurulumda bir pencere boyu sahte kötüleşme); deploy raporu (before
`2 × yaş` geriden başlar: ~45 günden eski deploy'da 90 gün ufkunu aşar; çekmecenin 6 sa kelepçesi before'u
`since − 6 sa`'te tutar). (3) **Ayrı bulgular:** /services
env/cluster süzgeci env MV'den okunurken prior `GetServicesAggFiltered2` ile KAPSAMSIZ okunuyor (tüm
ortamlar — v0.10.882'den beri; düzeltme prior başı için `EnvSummaryCovers` ister); topoloji okuyucusunun
üst sınırı `to`'nun kovasını hizalı `to`'da da okuyor (v0.9.823 sınıfı, current'ı değiştirdiği için
dokunulmadı); /endpoints'te katman karışıklığının TEK kaynağı 24 sa YAŞ kapısı: current başı 24 saatten
genç (10 sn), prior başı yaşlı (1 dk) ise prior 0–50 sn fazla okur — ≥ 1 sa pencerede ≤ %1,4, yalnız birkaç
on saniyelik pencerede belirgin; 2 sa BOY sınırında fark 0 (n10 × 10 = n1 × 60; testle pinli,
`TestEndpointsPriorWindowMixedTier`); FE `MetricsExplorer` / `LogsExplorer` karşılaştırma bindirmesi
`[from − (to − from), from]`'u istemcide kuruyor (seri bindirmesi, delta sayacı değil — incelenmedi).

## 2026-10-01 — Databases dilim 4: listede satır başına açık problem işareti (v0.10.1027)

**Operatör yönü:** Databases iyileştirmelerinde "Dynatrace'in Databases bölümünü baz al" (v0.10.1019
programı, madde 4: `/databases` listesinde satır başına açık problem işareti). Dynatrace'in veritabanı
listesi her satırda varlığın açık problemini söyler; bizde bu bilgi yalnız `/database` detayının problem
kartında (v0.10.1019) ve Problems sekmesinin Veritabanları şeridindeydi. **Uç:** `GET
/api/databases/problems` (kendi dosyası `internal/api/database_problems.go`, parametresiz — açık problem
"şimdi"dir ve db özneleri env'e bağlı değildir; önbellek 15 sn, statik anahtar `db-problems:v1`; rol kapısı
yok, `/api/databases` duruşu). Tek okuma `ListProblems(kind=db, status NOT IN …)`; "bitti" tanımı Problems
sekmesininkiyle AYNI değişkenden (`pickExcludedStatuses("open")` → `inboxDoneStatuses`), iki daraltma da
SQL'de, LIMIT'ten önce. **Biçim farkında eşleştirme (neden):** özne dizgisi `db:<system>@<X>` X'in instance
mı veritabanı adı mı olduğunu söylemez ve iki uzay gerçekten çakışır — span satırlarının instance'ı çoğu kez
"oracle" / "postgres" gibi genel bir ad; aynı adlı bir veritabanının yavaş ifadesi tek havuzla eşleştirmede
instance'ı o ad olan HER satırı işaretlerdi. Biçim kuraldan türer: rule_id `db-capacity:` önekli (tek tanım
`chstore.RuleDBCapacityPrefix`; üretici `capacityRuleID` onu kullanır, FE ikizi testle pinli) → instance
biçimi, gerisi (yavaş ifade, hedefli kural) → dbName biçimi (`chstore.DBProblemSubjectForm` / FE
`dbProblemForm`). Cevap özne başına iki biçimi AYRI taşır (`{instance?, dbName?}` → `{open, topSeverity}`);
istemci instance biçimini YALNIZ satırın instance'ıyla, dbName biçimini YALNIZ satırın db.name'iyle ve yalnız
span satırında eşleştirir; toplam ikisinin toplamı (bir problemin tek biçimi var, çift sayım yok). Detay kartı
AYNI kuralı uygular: aday kümesi tek fonksiyon (`dbProblemSubjectForms`), instance kimliğinden çekilenlerden
yalnız kapasite, dbName kimliğinden çekilenlerden yalnız kapasite dışı problemler kalır (`keepProblemForms`;
kimlikler aynıysa ikisi). **Önem, öncelik değil (neden):** `/inbox` exception dışı her satırı P3'e çiviliyor
(`forceNonExceptionP3`, operatör kararı v0.9.487); listede kırmızı "P1 · 2" Problems sekmesinde aynı satırın
P3'üyle olağan biçimde çelişirdi. İşaret "N problem" yazar, tonu eşleşen problemlerin saklanan en ağır
önemi: critical `b-err`, warning `b-warn`, gerisi nötr (renk yalnız sapan değerde). Önem saklanan kolon —
uç öncelik zenginleştirmesi (ve deploy okuması) yapmaz. Detay kartında da "Öncelik" sütunu "Önem" oldu
(sıra critical > warning > info, aynı ton kuralı). **"Problem değil":** operatörün "problem değil" diye
işaretlediği imzalar (`p:<kural>|<özne>`, sunucuda tek tanım `chstore.RuleProblemVerdictSignature` —
bildirim hunisi de onu çağırır) işareti saymaz/renklendirmez, çünkü Problems sekmesi onları varsayılan
listede gizliyor. Karar listesi okunamazsa her şey sayılır (açık kapı, hata serisinin ilki loglanır).
Detay kartı bu satırları listelemeye devam eder (bilinçli fark; kart pencerede ne olduğunu anlatır). İpucu
bunu söyler ve pencere vaadi vermez: "Şu an açık N problem (“problem değil” işaretliler hariç). Tıklayınca
veritabanı detayı açılır."; `aria-label` görünen metinle başlar. **Görünüm:** YENİ KOLON DEĞİL — kolon
kümesi değişseydi `useDataTable` `deps-db` altında kayıtlı kolon genişliklerini sıfırlardı. İşaret mevcut
ad (Instance) hücresinde, adın ÖNÜNDE (hücre nowrap + `overflow: hidden`; arkasında uzun adlı satırda
kırpılırdı), satır tıkının gittiği detay sayfasına bağlı (tablonun `rowHref`'i; adres satır tıkıyla tek
fonksiyondan), yayılımı keser. Sorgu anahtarı statik (`['database-problems']`), polling yok; okuma
beklerken satırlar işaretsiz; İLK okuma düşerse span tablosunun üstünde tek nötr satır "Problem işaretleri
yüklenemedi." (hata kutusu değil, liste engellenmez); eski veri varken düşen tazeleme işaretleri korur.
**Eşleşmeyenler:** (1) `default` db.name — MV'lerin "span db.name taşımıyordu" nöbetçisi; `db:<system>@default`
öznesi ne listede ne detay kartında sorulur (kural ortak aday kümesinde), yalnız Problems sekmesinin
Veritabanları şeridinde görünür. (2) **Bilinen açık — PostgreSQL takma adı:** kapasite denetimi motoru
"POSTGRES" yazar, özne `db:postgres@<instance>` olur; satırlar `postgresql` taşır. Bu problemler İKİ yüzeyde
de hiçbir satıra bağlanmaz (liste ve detay tutarlı); düzeltme üreticide (özne kimliği kanonikleştirme) ayrı
bir değişiklik — bugünkü davranış iki yüzey için aynı testle pinli, tek taraflı bir düzeltme onu kırar.
(3) Bu pencerede listede satırı olmayan veritabanlarının problemleri (trafik yok, receiver yok) çizilecek
yer bulamaz — Veritabanları şeridinde görünmeye devam eder. **Sınırlar:** dbName biçimli bir problem o
veritabanı adına hizmet eden HER instance satırında görünür (özne host bilmez); tarama tavanı 2000 — dolarsa
span tablosunun üstünde "Problem işaretleri eksik olabilir" notu; işaret "şimdi"yi anlatır, seçili pencereyi
değil.

## 2026-10-01 — Problems: dış kaynak (Oracle) problemleri varsayılan listede de görünür (v0.10.1026)

**Operatör:** kuyruğun 14. maddesine ("Oracle satırları varsayılan Problems listesine de girsin mi")
"14 girsin". Bu karar, v0.10.1017 girdisinin **Reddedilen** notunu ("external satırları varsayılan
(servis) listeye katmak … istenirse ayrı karar") operatör kararıyla TERSİNE çevirir — not tam olarak bu
ayrı kararı bekliyordu. **Karar:** `/inbox` varsayılan şeridi (`subject=service`; bilinmeyen değer de buraya
düşer) artık servis **ve** dış kaynak (`kind=external`, özne `ext:<kaynak>/…`) problemlerini birlikte
gösterir. Daraltma yine SQL'de, LIMIT'ten önce: chstore'a ayrı bir şerit değeri
(`ProblemLaneServiceOrExternal`) iner ve `(kind = 'service' OR kind = 'external')` üretir — paket
sabitlerinden iki literal eşitlik, IN-listesi değil; boş değerle eşleşmez, yani şerit CH'nin
`DEFAULT 'service'` garantisine dayanmaya devam eder (`TestSubjectLaneDoesNotHideTheColumnDefault` yeni
yazımı açıkça pinler, boş-dize yasağı aynen durur). Kolon yokken (iki-boot) varsayılan şerit bugünkü gibi
hiç daraltılmaz. chstore'daki `"service"` değeri bilerek SIKI kaldı (yalnız servis): çağıran taramasında
`ProblemFilter.SubjectKind`'ı dolduran tek yer `/inbox` çıktı, ama bir tür sabitine iki anlam yüklemek
v0.9.1339'un ad-çakışması sınıfıdır. **Sayılar:** tür facet'inde Problems kapalıyken çipin sayısı varsayılan
şeritte servis + dış kaynak kovalarının toplamı (`inboxLaneProblemCount`); `dbSubjectCount` /
`externalSubjectCount` alanları değişmedi. Kenar çubuğu rozeti (`/api/inbox/count`) zaten her özne türünü
sayıyordu — dokunulmadı; liste ona yaklaştı (kalan fark ayrı şeritteki db satırları, önceden de vardı).
Liste cache anahtarı `inbox:v8:` (satır kümesi değişti, `:v6:` emsali). **FE:** yeni şerit yok; "Servisler"
çipinin ipucu "Servisler ve dış kaynak (ör. Oracle) problemleri", "Dış kaynaklar (N)" çipininki "Yalnız dış
kaynak problemleri — varsayılan listede de görünürler". Satırın özne hücresi `SubjectLink`: `ext:` öznesine
servis linki kurulmaz (EXT rozeti + neden-link-yok ipucu), takım çipi çizilmez. **Değişmeyen:** db şeridi
(yalnız db) ve Dış kaynaklar şeridi (yalnız external — artık varsayılanın daraltılmış görünümü) ve çipi;
diğer kaynaklar (exception / anomali / incident) yine yalnız varsayılan şeritte çekilir; öncelik hesabı ve
inbox'ın "exception dışı türler P3" görünüm kuralı (v0.9.487). **Sınırlar:** (1) takım süzgeci
(owner/SRE/team) seçiliyken dış kaynak satırları düşer — katalog sahipleri yok ve sahiplik uydurulmadı
(db'nin türetilmiş sahipliği gibi bir kural yok); kendi şeritlerinde de böyleydi. (2) Dış kaynak satırları
artık servis satırlarıyla aynı öncelik sırasında yarışır — kabul edilen sonuç; v0.9.1342'nin "yarışmasın"
gerekçesi db için geçerli kalıyor. (3) env süzgeci seçiliyken dış kaynak satırları satır düzeyinde düşer
(`EnvScopeKeepsRow` yalnız db'ye kaçış tanır) — Dış kaynaklar şeridinde de önceden böyleydi; ayrı kalem.

## 2026-10-01 — Databases dilim 3: önceki pencereyle karşılaştırma (v0.10.1025)

**Operatör yönü:** Databases iyileştirmelerinde "Dynatrace'in Databases bölümünü baz al" (v0.10.1019
programı, madde 3: detayda önceki pencereyle karşılaştırma). **Bulgular (koddan; yerel ClickHouse'ta
örnek satırlarla doğrulandı):** (1) `/databases` listesindeki "Compare vs prior" kutusu v0.9.433'ten beri
ÖLÜYDÜ: sunucu prior'u okuyup satırlara yazıyor (`mergeDBPrior`), sayfa `prior*` alanlarını satıra
kopyalıyordu, ama iki `<DependenciesTable kind="db">` mount'unun hiçbiri `compare` geçmiyordu ve tablonun
her delta rozeti `compare &&` kapılı — /messaging geçiyordu. (2) Prior ile current BİR 5 dk kovayı
paylaşıyordu: current alt sınırı kovaya indiriyor (`time_bucket >= floor5(from)`), prior `[from − dur,
from)` ise hizasız `from`'da bitiyordu; `from` kova sınırına oturmadığında o kova iki pencerede sayılıyor,
delta sıfıra doğru suluyordu (doğrulamada: 10:00 kovasındaki 5 hata prior'a da sızdı, "0 → 5" yerine
"5 → 5" okundu). /messaging listesinde aynı kusur vardı ve aynı düzeltmeyi aldı. `mergeDBPrior` testsizdi.
**Kararlar:** (a) Prior pencerenin TEK türetimi `chstore.PriorWindow(from, to)`: `pTo = floor5(from)`,
`pFrom = pTo − N × 5 dk` (N = current'ın okuduğu kova sayısı; `to` saniyeye inerek sayılır çünkü
clickhouse-go konumsal bağı saniyeye keser). Ortak kova yok, iki pencere aynı sayıda kova; özellik testi
5000 rastgele pencerede sınıyor. Kullananlar: db listesi, messaging listesi, /database detayı. /databases'in
env süzgeci (ham spans, kova ızgarası yok) prior'u birebir süre kaydırmasıyla alır (`dbListPriorWindow`).
(b) Liste kutusu bağlandı: span satırları mount'u `compare` geçer; receiver mount'u BİLEREK geçmez (RED
tanım gereği sıfır, prior okuması receiver keşfi yapmaz). Kaynak pini + render testi. (c) `/database`
detayı önceki pencerenin agregesini HER İSTEKTE okur (toggle / URL paramı yok): aynı agrege SQL'i
(`dbDetailAggregate`'e çıkarıldı) aynı birincil anahtar önekinde tek satır daha; çağıran ve ifade
okumalarından SONRA koşar (`defer`), yavaş bir prior onların bütçesini yiyemez. `hasPrior` yalnız okuma
başarılı ve çağrılıyken true; hata yumuşak (log, delta yok, yükün geri kalanı aynen). (d) **Canlı kenar
ölçeği:** hazır aralıklarda `to = now`, current'ın son kovası henüz doluyor, prior ise N tam kova — düz
iş yükünde sayaç deltası ortalamada −%25 (5 dk) / −%12,5 (15 dk) / −%3,8 (1 sa) kayıyordu. Prior SAYAÇLARI
(çağrı, hata, messaging'de üretim/tüketim) `PriorCoverage = (min(now, son kova sonu) − floor5(from)) /
(N × 5 dk)` ile sunucuda ölçeklenir (detay, db listesi, messaging listesi); oranlar ve gecikmeler
ölçeklenmez. Yuvarlama en yakın tamsayı, sıfır olmayan prior için TABAN 1: "0" bu yüklerde bir iddia
("önce 0", listede ise omitempty ile satırın eşleşmemiş görünmesi) — beklenen < 0,5 olsa da (yalnız çok
kısa canlı pencerelerde) yazılmaz; bedeli o pencerelerde hafif iyimser bir delta. Detay `priorScale`
taşır. (e) **Okunabilirlik kapıları** (liste + detay): prior'un başı okunan tablonun saklama ufkunun
dışındaysa okunmaz (`PriorReadable`, 1 gün paylı; MV yolu 90 gün, /databases env yolu span saklaması —
env + 7d + Compare eskiden [now−14g, now−7g] okuyup Calls ↑%500 basıyordu; ufuk bilinmiyorsa okunmaz).
MV ileriye dönük olduğundan kaynağın ilk kovası da ölçülür (60 s önbellekli `min(time_bucket), count()`,
`EnvSummaryCovers` deseni; ilk kova pFrom'dan KESİNLİKLE önce olmalı); env yolunda bunun yerine
zaman-sınırlı varlık probu (pFrom'dan önceki 24 sa'te span var mı, `LIMIT 1`). Yön: kapsamayan ya da
kısmen silinmiş prior EKSİK sayar ve current'ı sahte biçimde KÖTÜ gösterir (kırmızı ↑) — iyileşme değil.
(f) Karolar fark taşır: Calls ve Total time `neutral`, Errors / Avg / P50 / P95 / P99 `lowerBetter`
(current'ta 0 çağrı varsa gecikme farkı çizilmez — "0.0 ms" ölçüm değil); Err rate farkı YÜZDE PUAN
(`+0.42 pp`), renk ve gizleme GÖSTERİLEN iki ondalıklı değerden (≥ 0,05 pp kötüleşmede renk, "0.00"
çizilmez). `TrendDelta`'ya `zeroPrior` eklendi (varsayılan değişmedi): detayda ve listede eşleşen satırın
sıfır prior'u (hata, üretim/tüketim) "listede yeni" değil "önce 0" der — liste eskiden 0 → N hata
regresyonunu mavi "listede yeni" rozetiyle gösteriyordu. Renk yalnız kötüleşen değerde (K5/T9); şeridin
altında tek satır "Karşılaştırma: bir önceki pencere" (+ ölçeklendiyse "sayılar, süren pencerenin dolu
kısmına oranlandı"); liste kutularının ipucu aynı notu taşır.
**Değişmeyen:** detay cache anahtarı (`db-detail:v2:`), URL, mevcut yük alanları (yalnız `hasPrior`,
`prior*`, `priorScale` eklendi); liste uçlarının anahtar ve sözleşmesi. **Sınırlar:** prior'un ufku ya da
kapsaması tutmuyorsa, kapsama probu düşerse ya da o pencerede çağrı yoksa delta yok; ölçek MV'ye yazım
gecikmesini hesaba katmaz (now'a kadar okunur varsayılır); env yolunun varlık probu pFrom öncesi 24 sa'te
hiç trafik yoksa kıyası kapatır; Err rate puanla, diğer karolar göreli; listede P95 için delta yok
(mevcut tasarım: liste `PriorP95Ms` taşımaz).

## 2026-10-01 — "Problem değil" kararları yalnız kendi ucundan yazılır: genel kayıtlı-görünüm ucu sistem sayfalarını reddeder (v0.10.1024)

**Kusur (kod incelemesiyle bulundu; prod'da gözlenmedi):** v0.10.1015 kararları ortak durum tablosunda
saklar (`saved_views`, page=`problem-verdict`, id=`pv:<imza>`, owner_id boş, ad=karar). Yazım yolu
`PUT /api/problem-verdicts` editor+ kapılı ve `problem.verdict` diye denetlenir. Ama genel kayıtlı-görünüm
ucu `POST /api/views` rol kapısızdır ve HER page değerini kabul ediyordu; okuma (`ListProblemVerdicts`)
yalnız `page = 'problem-verdict' AND name != ''` ile süzüyordu. Oturum açmış bir viewer page=`problem-verdict`,
ad=`noise` ve elle kurulmuş bir gövdeyle satır yazınca o satır ekip kararı sayılıyordu: editor kapısı ve
denetim kaydı atlanıyor, susturma politikası açıksa (v0.10.1016) o imzanın bildirimi de susuyordu.
`DELETE /api/views/{id}`: karar satırında owner_id boş olduğundan viewer/editor zaten 403 alıyordu; admin
ise "her şeyi silebilir" dalından gerçek bir kararı mezar taşıyla kaldırabiliyordu — `problem.verdict`
yerine `saved_view.delete` denetimi düşüyor, karar okuma önbelleği düşürülmüyordu. **Karar (üç katman):**
(1) okuma yalnız SİSTEM satırına güvenir — SQL'e boş owner_id ve `startsWith(id, 'pv:')` koşulları eklendi
(sahte satırlar 5000'lik LIMIT penceresine girip gerçek kararları dışarı itemez, PUT'un tavan denetimini
dolduramaz); satır çözücü de kimliği gövdedeki imzanın kimliği (`pv:<imza>`) olmayan satırı atar.
(2) chstore'da küçük bir sistem sayfası defteri (`IsSystemSavedViewPage`; bugün yalnız `problem-verdict`,
yeni sistem sayfası = bir satır). Genel uç bu sayfalara yazmayı her rolde depoya ve denetime dokunmadan
**400 "reserved page"** ile, silmeyi **admin dahil 403** ile reddeder — kararın tek denetimli yolu kendi
ucudur (boş verdict = kaldır). Kişisel sayfalar (ai-chat, promql-history, `table:<key>` tercihleri,
dashboard-star, alert-template, SavedViewsBar sayfaları) BİLEREK defterde değil: kendi uçları sahibin
owner_id'siyle yazar, genel uçtan yazılan satır en fazla yazanın kendi görünümünü etkiler. Genel liste
(`GET /api/views`) değişmedi: FE her çağrıda sabit bir page gönderir, karar satırları zaten
`GET /api/problem-verdicts`'te her role açık. (3) Testler: çözücü (geçerli / başka imzanın kimliği / rastgele
kimlik), defter tablosu, uç (viewer/editor/admin, paylaşımlı dahil → 400, denetim yok), silme yetkisi tablosu
ve kaynak pinleri. **Etkisi:** mevcut meşru kararlar etkilenmez — `SetProblemVerdict` onları zaten boş
owner_id ve `pv:<imza>` kimliğiyle yazıyor. Daha önce genel uçtan yazılmış sahte bir satır varsa artık
okunmaz (rastgele kimlik) ve zararsız çöp olarak kalır.

## 2026-10-01 — Operations: çıplak HTTP fiili satırları rotaya göre açılır (v0.10.1023)

**Operatör:** "Operation kısmında POST GET neden detail gözükmüyor, sonra trace'e girince çıkıyor."
Servis sayfasının Operations sekmesinde (Raw kip, varsayılan) satırlar yalnız `GET` / `POST`; satıra
tıklayınca Traces `name = GET` süzgeciyle açılıyor ve oradaki her satır `GET /metrics` (fiil + rota)
yazıyor. **Kök neden:** http.route'suz enstrümantasyonda span adı yalnız fiildir. Traces listesi
v0.10.756'dan beri gösterim adını tarayıcıda kuruyor (`opDisplayName`: çıplak fiil + kök span'ın
`http_route`'u). Operations satırları ise `operation_summary_5m`'den gelir (GROUP BY name, rota boyutu
yok) — v0.10.756 bu dilimi bilinçli olarak ertelemişti ("bir ad çok route"). **Karar:** yalnız Raw kipte,
çıplak fiil satırı `spanmetrics_1m`'den (fiil, rota) başına bölünür. Yeni uç
`GET /api/services/{name}/operations/routes` (kendi dosyası, 30 sn önbellek, anahtar servis + env + pencere
kovası + okuma ızgarası). Pencere ve sparkline ızgarası ops MV okumasıyla aynı hizalanır — "All" satırı
serileri eleman eleman topladığı için: alt uç 5 dk'ya aşağı; `to` SANİYEYE aşağı (ops okuması `winEnd`'i
konumsal `?` ile bağlar, clickhouse-go onu saniye hassasiyetinde yazar — `to` = B + 400 ms iken ops B
kovasını dışlar, plan da dışlar); ızgara `sparklineGrid(winSec, 300)`. Tarama `to`'nun dakikasında BİTER
(1 dk kovalardan yalnız başlangıcı `to`'dan önce olanlar): ops `to`'yu içeren 5 dk kovasını TAM alır ama
bundle o anda hesaplandı; rota okuması sekme açılınca, sonradan koşar ve 5 dk kovanın sonuna dek okusaydı
bundle'dan sonra gelen trafiği de sayardı (kısa pencerede %20-30'a varan şişme). Satırın `name`'i GERÇEK
span adı olarak kalır; rota yeni isteğe bağlı `route` alanında (`OperationSummary.Route`, omitempty).
Değiştirme tarayıcıda, yalnız tabloda (`pages/service/operationRoutes.ts`): fiilin en az bir dolu rotası
varsa ham satır çıkar, rota satırları girer (rotasız span'ler `route: ''` artık satırında). Hangi rota
yanıtının uygulanacağına TEK işlev karar verir (`routeRowsForBundle`): yanıt, bundle'ın çekildiği
(servis, from, to, env) üçlüsüyle damgalanır ve yalnız birebir eşleşen, gerçek (yer tutucu olmayan)
yanıt kullanılır — `main.tsx` küresel `keepPreviousData` koyduğu için rota sorgusu ondan açıkça çıkar,
yoksa aralık / zoom / servis değişiminde önceki pencerenin rota satırları yeni bundle satırlarına
uygulanırdı. Tablo satırları ve sekme rozeti yalnız bu çıktıdan. Gösterim, ad sıralaması ve süzgeç
`opDisplayName(name, route)`; satır kimliği ad + rota (+ artık satır işareti — ham "GET" ile artık "GET"
aynı kimliği paylaşmaz). Traces/Explore bağlantıları rota çipini taşır (`name = GET` + `http.route =
/metrics`; artık satırda `http.route NOT EXISTS`). Kapsamlı grafik simgesi (`?op=`) bölmeden gelen her
satırda (rota ve artık) gizli: o grafikler yalnız span adıyla daralır, satırın gösterdiği alt kümeyi
değil tüm GET'leri çizerdi; bölünmemiş ham satırlarda kalır. Bundle'ın operations dilimi ve
`GetOperationSummary` DEĞİŞMEDİ — copilot, SpanDetail taban çizgisi, ProblemDetail, Overview OpsCard ve
anomali incelemesi ham adı eşlemeye devam eder. Çıplak fiil kümesi Go'da tek yazıma indi
(`chstore.BareHTTPMethods`; trace_health regex'i ondan türer), templater ve frontend kümeleriyle eşitlik
testle pinli. **Bölme YAPILMAYAN durumlar (tablo bugünkü gibi görünür):** env seçili (`spanmetrics_1m`'de
deploy_env yok; uç sorgusuz `covered: false` döner — bölmek env'e daralmış satırı tüm env'lerin
rotalarıyla değiştirirdi); pencere `spanmetrics_1m` kapsamı dışında (forward-only MV, 30 gün TTL;
kapsama yardımcısı `spanmetricsCoverageStart`); pencere 5 dk'dan kısa (ops ham spans yolunda, ızgara
eşleşmez); fiilin hiç dolu rotası yok; satır tavanı (300) doldu (yarım bölme toplamı eksiltirdi);
Normalized kip; rota yanıtı yüklenirken, başka pencere / servis için olduğunda ya da çağrı düştüğünde.
Rota okuması yalnız Operations sekmesi açıkken yapılır (fetch-on-open); sekme rozeti Raw tablonun
gösterdiği satır sayısıdır. **Bilinen sınırlar:** aynı fiil + rotanın server ve client span'leri tek
satırda birleşir (çıplak satır da ayırmıyordu). Bölünmüş satırların toplamı çıplak satırınkinden İKİ
nedenle ayrışabilir: (1) kenar — ops `to`'yu içeren 5 dk kovasını tam alır, rota taraması `to`'nun
dakikasında biter; geçmiş (mutlak) pencerede bu, kovanın `to`'dan sonraki dakikaları kadar (≤ ~5 dk
trafik) çıplak satırdan az sayım demektir; canlı pencerede `to`'nun dakikasının rota okumasına kadar
dolan kısmı (≤ 1 dk trafik) fazla sayılabilir; (2) bundle'ın yaşı — bundle 60 sn önbellekli (+ SWR) ve
hesaplandığı andaki veriyi taşır; rota okuması (30 sn önbellekli) sonra koşar. Geniş pencerede (>10 sa,
slot >5 dk) rota satırının p50/p95/p99 çizgisi slotun birleşik yüzdeliğidir, ham satırınki 5 dk kova
yüzdeliklerinin slot içi maksimumu. Overview'daki Operations kartının "View all N" sayısı ham span adı
sayısıdır, Operations sekmesinin rozeti ise bölünmüş satır sayısı — ikisi farklı okuyabilir (kabul
edildi). Bundle'ın operations satırları ham spans geri düşüşünden geldiyse (ops MV boş/hatalı; 1 sn
ızgara, hizasız köken) rota satırları farklı bir sparkline ızgarasında durur (nadir; kabul edildi).
**AÇIK BULGU (bu sürümde düzeltilmedi):** Normalized kipte satır tıklaması `name = <op_group şekli>`
gönderir; şekil rotadan ya da normalize edilmiş yoldan türetildiyse ("GET /metrics", "GET /users/:id")
hiçbir span adıyla eşleşmez ve Traces listesi boş gelir. Düzeltme bir `op_group` süzgeç anahtarı ister.

## 2026-10-01 — Histerezis bandındaki açık anomali artık gerçekten tazelenir (v0.10.1022)

**Operatör:** "anomali alarmları çok geliyor" (ekran görüntüsü: saatte ≈130 anomali problemi, ~20 dk
arayla dalgalar hâlinde). **Kusur (kod okumasıyla bulundu ve doğrulandı; prod'da ölçülmedi):** açık bir
anomali problemi, değeri açılma eşiğinin altına inip çözülme bandına henüz dönmediğinde ("sürüyor",
karar `none`) her tik tazelenmelidir — v0.10.889 bunun için `applyOutcome`'a bir touch koymuştu. Ama
dedektör turunun birinci fazı `none` kararını (v0.9.1069 iki-faz bölünmesinden beri) kendisi eliyor,
`applyOutcome`'a hiç göndermiyordu: touch ölü koddu, testi de yalnız `applyOutcome`'un içini pinliyordu.
Sonuç: bantta iki tik (4 dk) kalan her açık anomali evaluator'ın bayat süpürmesiyle (3×1 dk) "kaynak
sustu" diye kapanıyor; değer eşiği yeniden geçince YENİ kimlikle yeni problem, yeni bildirim ve (vida
açıksa) yeni incident açılıyordu. Eşik çevresinde gezinen bir servis saatte birkaç "yeni" anomali
üretiyordu. **Karar:** faz 1 süzgeci saf bir işleve çıktı (`reachesApply`): `none` + açık satır uygulama
döngüsüne girer ve touch edilir; `skip` (veri yok) girmez — sustu ≠ düzeldi, süpürme kapatmaya devam eder
(v0.9.1051). Kümeye o tik katılan satır touch'tan önce atlanır (mevcut `merged` kontrolü). **Beklenen
etki:** açık anomali, değeri çözülme bandına dönene dek TEK satır olarak yaşar; yeniden açılma dalgaları
kesilir. Açık anomali satırları daha uzun açık kalır — bu, tasarlanan histerezistir (v0.8.220).
**Filo fırtına kapısı (operatör "evet olsun") GEMİYE ÇIKMADI:** iki tur Opus çelişkili incelemesi
tasarımda ciddi kusurlar buldu (v1: bantta kalan üyeler sayımdan düşüp fırtınayı erken kapatıyor, daha
çok bildirim; v2: açık bir fırtına saatler sonra gelen ilgisiz anomaliyi bildirimsiz yutabiliyor,
okunamayan bir tikte süpürme fırtınayı kapatıyor, kaybolan servisler fırtınayı sonsuza dek açık tutuyor).
Çalışma `.ai/parked/storm-v2/` altında bekliyor. Önce bu düzeltmenin prod etkisi ölçülecek; kapı hâlâ
gerekiyorsa üyelerin gerçek satırlarını koruyan (bastırmayan) bir tasarımla yeniden ele alınır.

## 2026-10-01 — Dış (Oracle) kümeleri servis dedektörünce kapatılmaz (v0.10.1021)

**Bağlam:** operatör "anomali alarmları çok geliyor" + "Oracle'dan gelenler Problems'te gözükmüyor";
fırtına kapısı için dedektör okunurken koddan bulundu (prod'da gözlenmedi). **Kusur:** `anomaly-cluster:`
önekini iki üretici taşır — servis topolojisi kümeleyicisi ve dış seri kümeleri (`anomaly-cluster:ext:…`,
dış tarayıcı). Servis dedektörünün `resolveStaleClusters`'ı önekin tamamını kendi kümesi sayıyor ve
öznesi bu tikin anomalili servisleri arasında olmayan her kümeyi "kaynak toparlandı" diye kapatıyordu.
Dış kümenin öznesi (`ext:…` ya da çözülmüş servis) orada olmadığından açık Oracle kümeleri her 2 dk'da
kapanıyor, dış tarayıcı bir sonraki poll'da (varsayılan 60 sn) aynı kümeyi YENİ başlangıç anıyla yeniden
açıp kanal bildirimini yeniden gönderiyordu (dedup anahtarı started_at taşır → geçer; ekip maili problem
kimliğiyle tekilleştiği için tekrar gitmez). Öznesi gerçek servise çözülmüş kümelerde döngü aralıklı.
Bağımsız bir Opus incelemesi kod okumasıyla doğruladı (çalışma anında yeniden üretilmedi). **Karar:** sahiplik saf bir işlevle ayrılır
(`ownsServiceCluster`): servis dedektörü yalnız kendi kümelerine dokunur; dış kümelerin yaşam döngüsü dış
tarayıcıda kalır (`applyExternalClusters` — anahtarında anomali kalmayınca kapanır). Servis kümelerinin
davranışı değişmez. **Ayrıca kayıt (aynı gün, operatör):** veritabanı motor sağlığı için "collector'a
Oracle receiver daha sonra ekleyeceğiz" — V$ görünümlerini mevcut Oracle bağlantısından okuma yazılmaz;
receiver gelince hazır motor panelleri dolar.

## 2026-10-01 — Databases dilim 2: veritabanı hata kırılımı — "hangi hata" (v0.10.1020)

**Bağlam:** Databases × Dynatrace programı (aşağıdaki kayıt), dilim 2 — hata analizi karşılığı. `/database`
hata ORANINI gösteriyordu, hangi hatanın olduğunu göstermiyordu. **Karar:** sayfaya "Errors on this
database" kartı: başarısız çağrılar imzaya göre gruplanır — en belirleyiciden kabaya üç basamak: Oracle
hata kodu (`ORA-/PLS-/TNS-NNNNN`; status mesajında ya da exception mesajında geçen ilk kod) → exception
tipi (event ya da `error.type`; exception hattıyla AYNI ifadeler) → hata mesajının ilk 80 karakteri;
hiçbiri yoksa "(mesajsız hata)". Satır: adet, pay, en çok üreten çağıran (+ diğerlerinin sayısı), son
görülme, örnek trace; altta "hatalı trace'leri aç" pivotu. **Veri yolu:** `GET /api/databases/errors`
(kendi dosyası; detayla aynı kimlik üçlüsü + pencere, 30 sn önbellek; detay yükünü geciktirmesin diye
ayrı ve paralel). İki okuma: (1) `db_caller_summary_5m` — bu kimliğe HATA üretmiş çağıranlar (yoksa ham
okuma yapılmaz); (2) ham `spans` — yalnız o çağıranlar (≤200), pencere, `status_code='error'`, ≤20 imza.
**Ham okuma bilinçli bir istisna** (mimari değişmez 3): hata mesajı / exception event'i hiçbir ön-toplamda
yok; tarama birincil anahtara (service_name, time) ve hata satırlarına budanır, `max_execution_time = 10`
— "Top statements" okumasının emsali. Sorgu yerel ClickHouse'ta örnek satırlarla doğrulandı. **Sınır:**
kod çıkarımı yalnız Oracle ailesi; diğer motorlarda imza exception tipine / mesaja düşer.

## 2026-10-01 — Databases: Dynatrace Databases baz alınır; dilim 1 "bu veritabanının problemleri" (v0.10.1019)

**Operatör:** Databases iyileştirmeleri için "Dynatrace databases kısmını baz al; o database ile ilgili
veriler gelebilir." **Mevcut `/database` sayfası:** kimlik, RED şeridi, üç trend kartı, çağıranlar (servis ·
pod + log pivotu), en ağır ifadeler (+ ifade detay sayfası), motor panelleri (yalnız receiver metriği
varsa — operatör motor metriği ingest ETMİYOR, v0.9.846). **Dynatrace'e göre eksikler ve sıra:**
(1) varlığın problemleri — **bu sürüm**; (2) hata kırılımı: başarısız çağrılar hata türüne (ORA kodu /
exception tipi) ve ifadeye göre + trace pivotu; (3) detayda önceki pencereyle karşılaştırma (RED delta);
(4) `/databases` listesinde satır başına açık problem işareti; (5) motor sağlığı (oturum, bekleme sınıfı,
kilit, tablespace, top SQL) — Dynatrace bunu veritabanının kendisinden alır; burada iki yol var ve seçim
OPERATÖRÜN: (a) collector'a oracledb receiver (paneller hazır, kod yok), (b) mevcut Oracle bağlantısından
V$ görünümlerini periyodik okumak (yeni kod; DB yükü + yetki kararı — "full scan / kilitli sorgu atmasın"
direktifi geçerli); (6) çağıran tarafı bağlantı havuzu metrikleri (uygulamalar yayıyorsa).
**Dilim 1 kararı:** `/database` sayfasına "Problems on this database" kartı — açık olanlar + seçili
pencereyle kesişen çözülmüşler; öncelik, durum, kural adı (problem detayına link), başlangıç, süre. Yeni uç
YOK: `/api/problems?service=<özne>` kesin eşleşme. Özne iki biçimde açıldığı için iki okuma:
`db:<system>@<instance>` (kapasite) ve `db:<system>@<dbName>` (yavaş ifade, hedefli kural). 2-6 ayrı
dilim; (5) operatör kararı gelmeden başlamaz.

## 2026-10-01 — Problems soğuk yolu: iki ham `spans` toplaması ön-toplama taşındı (v0.10.1018)

**Operatör:** "Problems sayfası biraz yavaş, acaba indeks yok mu tablolarda." **Bulgu (koddan; prod'da
ÖLÇÜLEMEDİ):** state tabloları (problems, exception_groups, anomaly_events, incidents) küçük ve anahtarlı —
eksik indeks yok. Yavaşlık adayı sayfanın soğuk yolundaki iki ham `spans` toplaması: (1) problem
satırlarına deploy eşleyen `fetchDeploysByService` — pencere açık problemlerin EN ESKİSİNE dek uzanır
(≤32 gün), ilgili tüm servisler, satır başına 14 dizi yoklaması, 10 sn tavan; tavana takılırsa sonuç
önbelleğe girmez ve her hesap bedeli yeniden öder; (2) `GetServiceClusterMap` — tüm filonun son 1 saati.
İkisi de v0.10.1014'e dek varsayılan görünümde koşmuyordu (varsayılan yalnız exception'dı, problem dalı
tek COUNT'tu); "hepsi gelsin" varsayılanı onları her soğuk hesaba soktu. **Karar:** ikisi de MV-önce
(mimari değişmez 3): deploy eşlemesi `service_version_5m` (v0.9.249, `deployMVCovers`), cluster haritası
`service_env_summary_5m` (v0.10.881, `EnvSummaryCovers`); MV pencereyi kapsamıyorsa ya da okuma düşerse
ham yol AYNEN çalışır. Anomali ikizi (`EnrichAnomaliesWithDeploys`) kendi ham kopyasını bırakıp ortak
okuyucuya geçti (MV + 15 sn önbellek). Yerel ClickHouse'ta iki yolun aynı sonucu verdiği doğrulandı.
**Küçük fark:** MV yolu pencere başını 5 dk kovasına hizalar; en eski problemde pencere başında zaten
koşan sürümün "az önce deploy" gibi görünmesi (hayalet) azalır. Deploy bilgisi yalnız gösterimdir,
önceliğe karışmaz. **Faydalanan diğer yüzeyler:** /problems listesi, incidents, anomalies, MCP
list_problems (aynı zenginleştiriciler). **Açık:** inbox soğuk yolu hâlâ ~10 ardışık CH gidiş-dönüşü;
kaynakları paralel çekmek ayrı dilim — önce bu değişikliğin prod etkisi ölçülmeli (/api/inbox süresi +
X-Cache). Not: v0.10.1017'nin CI'ı tek satırlık gofmt hizasından kırmızıydı; düzeltmesi bu sürümde.

## 2026-10-01 — Problems sekmesi: üçüncü özne şeridi "Dış kaynaklar" (v0.10.1017)

**Operatör:** "Oracle'dan gelenler de Problems'te gözükmüyor." **Kök neden:** dış metrik kaynağı
problemleri (Oracle / Influx; özne `ext:<kaynak>/…`) `kind=external` ile açılır (v0.10.228). `/inbox`
özne şeridi (v0.9.1342) ondan ÖNCE yazıldı ve iki değerliydi: Servisler `kind='service'`, Veritabanları
`kind='db'`. İkisi de tam eşitlikle süzdüğü için external satırlar HİÇBİR şeride girmiyordu — kenar
çubuğu rozeti ve bildirimler onları sayarken liste göstermiyordu. Kayıtlı bir karar değil, sonradan
eklenen türün düştüğü boşluk. **Karar:** üçüncü şerit **Dış kaynaklar (N)** — db şeridinin birebir emsali
(tek kaynak: problems; tür facet'i zorlanır; sayı aynı tek COUNT'un üçüncü kovası, sunucudan). Varsayılan
şerit Servisler kalır: özne türleri öncelik sırasında yarışmasın kararı (v0.9.1342) korunur, dış kaynak
satırları servis listesine KARIŞTIRILMADI. `?subject=external`; bilinmeyen değer yine Servisler.
**Reddedilen:** external satırları varsayılan (servis) listeye katmak — operatörün db için verdiği
"ayrı şerit" kararının tersi olurdu; istenirse ayrı karar.

## 2026-10-01 — "Problem değil" bildirimi de susturabilir — varsayılan KAPALI anahtar (v0.10.1016)

**Operatör:** v0.10.1015 sonrası önerilen sonraki adıma ("problem değil dediklerinin bildirimini de
susturmak") "devam". **Karar:** öğretme bildirime bağlanır ama YALNIZ yönetici politikayı açarsa —
`system_settings["problem_verdict_policy"].muteNotifications`, varsayılan **kapalı**. Kapalıyken hiçbir
bildirim değişmez (v0.10.1015 davranışı). Alarm kaybettirebilen bir değişiklik olduğu için açık bir
anahtar ve onay diyaloğu; tek tıkla kapanır. **Açıkken susan:** "problem değil" imzalı alarm kuralı
problemi (`p:<ruleId>|<servis>`) ve exception / HTTP hata grubu (`e:<fingerprint>`) — ekip maili, kanallar,
çözüm bildirimi ve P1 exception anonsu dahil. **Susmayan:** Problem kaydının kendisi (yine açılır, "Problem
değil" görünümünde listelenir), canlı akış (SSE), olaylar (incident — imzası yok) ve anomali imzaları
(`a:…`; bildirim hunisine ayrı kimlikle girmezler — kapsam dışı, ekran bunu söyler). **Kapı:**
`internal/notify/verdict_silence.go` — `SendProblemAlert` içinde SSE yayınından sonra, bakım penceresi /
ack / "Sustur bağlantısı" kapılarının yanında; exception kanal yolu ve P1 anons yolu da aynı kapıyı
çağırır. İmza sunucuda Problem'den yeniden üretilir; FE kalıbıyla aynı olduğu iki yönlü testle pinli.
Politika + noise imza kümesi 30 sn önbellekli (işaretleme / geri alma / anahtar en geç 30 sn'de etkir);
**ayar ya da karar listesi okunamazsa susturma YOK** (bildirim kaybetmek fazladan bir mailden kötü).
**Uç:** `PUT /api/problem-verdicts/policy` (yalnız admin, denetim `problem.verdict.policy`); politika
`GET /api/problem-verdicts` cevabında gelir (her rol görür). **Ekran:** "Problem değil" görünümünün
başında durum satırı ("Bildirim: gönderiliyor / susturuluyor") + admin düğmesi; çekmecedeki açıklama
duruma göre değişir. **Bilinen sınır:** imza işaretlenmeden ÖNCE açılıp bildirilmiş bir problemin çözüm
bildirimi de susar. **Hâlâ ayrı karar:** kararı kural / dedektör eşiklerine geri beslemek; toplu
işaretleme; noise satırlarını kenar çubuğu sayacından düşmek.

## 2026-10-01 — Problems sekmesinde öğretme: "gerçek problem / problem değil" (v0.10.1015)

**Operatör:** "…ben hangisi gerçek problem hangisi değil zamanla öğretelim" → önerilen tasarıma "tamam".
**Karar:** her satıra iki karar düğmesi (triage çekmecesi): **Gerçek problem** / **Problem değil** (+ işareti
kaldır). Karar OLAYA değil İMZAYA yazılır — alarm kuralı `p:<ruleId>|<servis>`, exception / HTTP hata grubu
`e:<fingerprint>`, anomali `a:<tür>|<servis>|<desen>`; olay (incident) öğretilmez. Aynı imza yeniden
geldiğinde kendiliğinden aynı sınıfa düşer: "öğrenme" deterministik ve geri alınabilir, model yok.
**Etkisi yalnız görünüm:** "problem değil" imzaları varsayılan listeden çıkar ve `?verdict=noise`
görünümünde toplanır (çipte sayısı hep görünür — gizlenen şey saklanmaz); "gerçek" satır rozet alır.
Bildirimlere, Problem yaşam döngüsüne, dedektörlere ve kenar çubuğu sayacına DOKUNMAZ (ilk aşama bilinçli
dar). **Depolama:** ortak durum tablosu `saved_views` (page=`problem-verdict`, id=`pv:<imza>`, ad=karar,
gövde JSON) — mimari değişmez 5, yeni şema yok; tavan 5000 imza. **Uçlar:** `GET /api/problem-verdicts`
(her rol; viewer görür) · `PUT` (editor+, denetim `problem.verdict`; cevap taze listenin tamamı, FE onu
doğrudan önbelleğe koyar). Kararlar yüklenemezse hiçbir satır gizlenmez. **Mevcut mekanizmalarla ilişki:**
exception "Ignore" ve anomali susturma (mute) AKIŞI değiştirir; anomali OLAYI kararı (v0.10.181, «anomali
/ değil», event başına) dedektör istatistiği içindir — üçü de aynen duruyor, bu karar onların yerine
geçmez. **Sonraki adımlar (ayrı karar):** "problem değil" imzalarının bildirimini de susturmak; kararı
kural / dedektör eşiklerine geri beslemek; toplu işaretleme.

## 2026-10-01 — Problems sekmesi: varsayılan görünüm HER ŞEY (v0.10.1014; üç kararın tersi)

**Operatör:** "Problems sekmesinde bütün hepsi gelsin, ben hangisi gerçek problem hangisi değil zamanla
öğretelim." **Karar:** `/inbox` varsayılanı tüm öncelikler (P1 + P2 + P3) ve tüm türler (alarm kuralı
Problem'leri, exception grupları, HTTP hataları, anomaliler, olaylar). Bu, üç önceki operatör kararının
TERSİDİR: v0.9.487 (yalnız P1) → v0.9.659 (P1 + P2), v0.9.328 (yalnız exception), v0.9.443 (HTTP hataları
kapalı). Gerekçe değişti: gürültü varsayılan süzgeçle GİZLENMEYECEK, operatör neyin gerçek olduğunu
işaretleyerek öğretecek. Çipler ve sayıları aynen; daraltmak tek tık ve `?prio=` / `?kind=` linke biner
(parametresiz eski linkler artık her şeyi gösterir). Kenar çubuğu rozeti ve sunucu tarafı değişmedi.
**AÇIK — öğretme mekanizması (ayrı dilim, operatör kararı bekliyor):** satır başına "gerçek problem /
problem değil" işareti; neyin imzasına bağlanacağı (aynı kural + servis / aynı exception grubu mu, daha geniş
mi), "problem değil"in yalnız listeden mi yoksa bildirimden de mi düşüreceği ve mevcut exception "Ignore" /
problem "mute" ile ilişkisi sorulmadan kurulmaz.

## 2026-10-01 — Argo CD: keşfedilen instance'lar kendiliğinden kaydedilir — anahtarla (v0.10.1013)

**Operatör ("Argocd entegrasyonu da autodiscover etse daha iyi olacak, şu anda tek tek ekle diyorum";
kuyruktan "devam sırayla"):** annex §7.2'nin kuralı "öner, asla otomatik yazma" idi. Prod'da ekip × ortam
başına ayrı instance var (hub başına ~190); her yeni ekipte Ayarlar'a girip "Tümünü ekle + Kaydet"
gerekiyordu, kaydı olmayan instance'ın uygulamaları eşleyicide yoktu. **Karar:** kural AÇIK BİR ANAHTARLA
tersine çevrildi — `autoRegister.enabled`, varsayılan KAPALI; entegrasyon kapalıyken saklanmaz (Validate).
**Tur** (api rolündeki pod'larda, lider kilidiyle tek pod, 30 dk'da bir; kapalıyken sorgu da Redis kilidi de
yok): her ETKİN hub için elle keşifle AYNI probe; hub'lar sırayla koşar ve bir hub'ın adayları sonrakinin
"mevcut" listesine girer (aynı namespace iki hub'da aynı turda çakışan kimlik almaz). Uygun aday varsa
`argocdPutMu` altında TAZE bloba eklenir, sonuç PUT ile aynı doğrulamadan geçer ve yazılır; doğrulama
düşerse hiçbir şey yazılmaz. **Yalnız ekler:** aday kuralı elle "Tümünü ekle" ile aynı (yeni, hatasız,
namespace'i belli); mevcut instance'ın hiçbir alanı değişmez; kimliği ya da (hub, namespace) yuvası dolu
aday atlanır; tavan 500. Pasif hub ve tokenRef'i çözülemeyen hub taranmaz. Yazım denetimde
`settings.argocd.autoregister` (aktör: system). **Bilinen sonuç (ekranda yazılı):** silinen instance Argo'da
hâlâ varsa geri eklenir — istenmeyen instance silinmez, devre dışı bırakılır. **Yan etki:** yazım
`updatedAt`'i ilerletir; o sırada açık bir taslak Kaydet'te 409 alıp yeniden yükler (yalnız gerçekten yeni
instance bulunduğunda). **Reddedilen:** "reddedilenler listesi" tutmak (silineni hatırlayıp eklememek) —
yeni bir kalıcı durum ve ayrı bir ekran; devre dışı bırakma aynı işi görüyor.

## 2026-10-01 — Logs histogramı çubuk tavanı + topoloji perf bütçesi makineye ölçekli (v0.10.1012)

**Kuyruk (operatör "devam sırayla"), iki cila işi tek sürümde.** **(1) Logs histogramı:** Traces şeridiyle
aynı tavan — `logsBucketSec` artık `traceStripMaxDataPoints` (≤100 çubuk) kullanır; geniş ekranda 3 saat
180 yerine 90 çubuk, ~1440px ve altı değişmez, ES `date_histogram` daha az kova üretir. **(2) Topoloji
yerleşim perf testi:** mutlak 1500 ms bütçe bir laptopta tanımlanmıştı; CI koşucusu ~7× yavaş ve aynı kod
orada istikrarlı 1512–1514 ms ölçüyor — gerileme YOK (yerelde 222 ms), yalnız sabit bütçe yavaş makinenin
sınırındaydı. Bugün iki sürümün (998, 1009) CI'ını kırdı; kırılınca arka uç / güvenlik / lint işleri de
atlanıyor. **Karar:** bütçe = max(1500 ms, 45 × kalibrasyon) — kalibrasyon, bileşenden bağımsız sabit bir
iş (sıralama + dize üretimi; yerelde ≈33 ms), 45 ise bütçenin tanımlandığı makinedeki oran. Sıkılık aynı,
cetvel makineyle uzuyor: hızlı makinede taban 1500 ms (gevşemez), yavaş makinede aynı orandaki gerileme
yine yakalanır. **Reddedilen:** bütçeyi düz 3000 ms'e çekmek — yerelde 5× gerilemeyi (≈1.9 s) kaçırırdı.

## 2026-10-01 — Traces şeridi: Errors + giriş-dışı çipte boş durum nedenini söyler (v0.10.1011)

**Kuyruk (operatör "devam sırayla"):** 1010'dan sonra `function_code` çipi + Errors'ta liste dolu, şerit
"No traces in view to bucket" — listeyle çelişen çıplak bir cümle. Şerit spans kapsamında Errors'u çiple
AYNI span'de arar ("süzgece uyan span hatalı mı"); fonksiyon kodunu taşıyan span'ler hata vermediği için
sayım gerçekten sıfır. **Karar:** sayı uydurulmaz, boş durum NEDENİNİ söyler (`volumeEmptyNote`): "Süzgece
uyan span'lerin hiçbiri hatalı değil — hata aynı trace'in başka bir span'inde. Liste trace düzeyinde
eşleşir; grafiği görmek için Errors'u kaldırın." Diğer hâllerde eski metin. **Reddedilen:** (a) spans
kapsamında Errors bayrağını şeritten düşürmek — db.statement gibi çiplerde "hata veren sorgular" anlamlı
serisini bozar ve hatasız span hacmini hata grafiği gibi gösterir; (b) trace düzeyi kesişim serisi (hata
trace'lerinin çipli span'leri) — span-metrik motoruna yeni bir süzgeç türü ve pencere boyu GLOBAL IN; talep
gelirse ayrı karar.

## 2026-10-01 — Traces: Errors + çip → hata trace düzeyinde (v0.10.1010)

**Operatör bildirimi:** `function_code = …` çipiyle liste dolu ve satırların çoğu ERROR; "Errors"
işaretlenince "Trace bulunamadı". **Kök neden:** çipler WHERE'de span düzeyinde, Errors ise başka span
yüklemi varken HAVING'de (v0.10.258) — ama o HAVING, WHERE'in bıraktığı span'lerde sayar: fiilî anlam
"çipe uyan span'in KENDİSİ hatalı". Prod'da fonksiyon kodunu taşıyan log-yayın span'leri hata vermiyor;
hata aynı trace'in başka span'inde. 1005 (satır) ve 1008 (Root) ile aynı sınıf. **Karar — iki basamak,
sayfadan bağımsız:** ① kapsamda çipe uyan VE hatalı bir span varsa eski anlam AYNEN (v0.10.812'nin "çipe
uyan hatalı span" yolu; mevcut dolu sonuçların hiçbiri değişmez). ② yoksa hata TRACE düzeyinde: "çipe uyan
span'i olan VE herhangi bir span'i hatalı olan trace" — eski cevap bu durumda kesin boştu. **②'nin
maliyeti:** iki indeksli taramanın kesişimi (çip: terfi kolonu / kvh; hata: idx_status). Tavanlı sayımla
küçük taraf seçilir ve KÜME olur (en yeni ≤300k satır), büyük taraf `trace_id GLOBAL IN (küme)` ile
süzülür, en yeni ≤6000 trace liste adayı olur; küme ya da aday tavana çarparsa `RankedWithin` ilan eder.
Adaylar hata-doğrulandığı için liste aşamalarında HasError kapanır. **Reddedilen:** çipleri HAVING'e
taşıyıp tam pencereyi GROUP BY trace_id ile taramak (v0.10.341'in kaçındığı maliyet) ve sınırsız GLOBAL IN
(v0.10.238'in 241 sınıfı). **Şerit değişmedi:** spans kapsamında Errors bayrağı "çipe uyan hatalı span"
sayar (db.statement için anlamlı); fonksiyon kodunda bu sıfırdır — şerit boş, liste dolu görünür.

## 2026-10-01 — Argo CD: pasif hub kaydı engellemez, taranmaz (v0.10.1009)

**Operatör ("hub'lardan biri aktif diğeri pasif"; kuyruk "devam et"):** Remote Cluster kaydı devre dışı olan
hub, Argo CD açıkken kaydı engelliyordu (`canonicalHubs`: "her hub etkin olmalı", karar 5) — aktif/pasif
çiftte pasif tarafın kaydı bilerek kapalı olduğundan Argo ayarlarının HİÇBİRİ kaydedilemiyordu. **Karar:**
devre dışı kayıt = PASİF HUB. Listede ve instance'larıyla blobda kalır; kaydı engellemez. Tek şart: bayrak
açıkken EN AZ BİR hub etkin olmalı (hepsi pasifse 400, alan `hubs`). **İşçi:** pasif hub'ın instance'ları
PLANLANMAZ (`Registry.Passive`, `planShards`); öncesinde her biri "hub devre dışı" diye sert atlanıyor,
190 instance'lık pasif hub koşuyu her tik kısmi gösteriyordu. Yerine hub başına TEK not ("pasif hub hub-2:
190 instance taranmadı") + `passive_hub_instances` teşhis sayacı; koşu durumu düşmez. Kayıt yeniden
etkinleşince instance'lar kendiliğinden planlanır (yeni instance gibi taban alır). Silinmiş / URL'siz kayıt
eski davranışta (sert atlama — o bir yapılandırma hatası). **FE:** hub satırı "pasif" rozeti + soluk bilgi
satırı (hata değil); istemci denetimi sunucuyla aynı kural. Keşif pasif hub'a istek göndermez (değişmedi).
Pasif hub'ın eski CH satırları (argocd_app_status) yaşlanarak düşer; aktif hub'daki ikizleri güncel kalır.

## 2026-10-01 — Traces: Root kutusu çipli listede kökü daraltılmış kümede aramaz (v0.10.1008)

**Operatör bildirimi ("Root seçiliyken neden gelmiyor"):** `function_code = …` çipi + "Root" → liste boş
(şerit de boş). **Kök neden:** daraltmasız ham şekilde kök-varlığı `countIf(kök)` ile WHERE'in bıraktığı
span'lerde aranır; çipler WHERE'de span düzeyinde olduğundan o küme yalnız çipi taşıyan span'lerdir ve
prod'da fonksiyon kodunu kök span taşımıyor → her trace düşüyor. v0.10.107 aynı hatayı SERVİS daraltması
için kapatmıştı (kök başka serviste). **Karar:** tek kural `rootScopeNarrowed` — servis, RequireServices
VEYA span-düzeyi çip WHERE'i daraltıyorsa kök, daraltılmamış kaynaktan sorulur: aday id'ler üstünde
`trace_summary_5m` nokta okuması (rootPostFilter; −2..+1 kova, MV-gap gününde ham ikiz), tek geçişte
`trace_id GLOBAL IN (MV)`. Yeni mekanizma yok; servis daraltmasının üretimde çalışan yolu çiplere açıldı.
Arama + çip şeklinde çipler zaten HAVING'de (WHERE daralmıyor) → değişmedi. **Şerit:** Root bayrağı kök
yüklemini çiple aynı span'de AND'liyordu; çip giriş span'inde yaşamıyorsa (spans kapsamı, v0.10.1006)
bayrak artık gönderilmez (`stripRootOnly`) — o kapsamda şerit zaten eşleşen span'leri sayar ve bunu
etiketler; giriş kapsamında bayrak aynen gider (v0.10.484). db.statement gibi diğer giriş-dışı çipler de
aynı düzeltmeyi alır.

## 2026-10-01 — Traces hacim şeridi: çubuk sayısı tavanı 100 (v0.10.1007)

**Operatör ("histogram bar sayısı daha iyi olabilir mi … çok"):** şeridin çubuk bütçesi ekran genişliğiyle
büyüyordu (`barPanelMaxDataPoints`: ~12px/çubuk, tavan 240) — geniş ekranda 3 saatlik pencere 1 dk kovayla
180 çubuk. Okunan şey trend ve tepeler; 180 ince çubuk ikisini de zorlaştırıyor. **Karar:** Traces şeridine
özel bütçe `traceStripMaxDataPoints` = min(100, eski bütçe). Çubuk sayısı ekran ne kadar geniş olursa olsun
100'ü geçmez; geniş ekranda çubuklar kalınlaşır. Rung'a snap sonrası tipik sayı 60–96 (3 saat → 2 dk kova,
90 çubuk). ~1440px ve altındaki ekranlarda bütçe zaten ≤100'dü — değişmedi. Step yine rung'da (cache
anahtarı kardinalitesi aynı). **Kapsam bilinçli dar:** Logs histogramı aynı eski bütçede kaldı (istenmedi).
v0.9.715 ("barlar çok küçülmüş" → 12px/çubuk) kararı yerinde; bu, onun geniş ekran ucunu kapatır.

## 2026-10-01 — Traces hacim şeridi: iş kimlikleri giriş span'i anahtarı değil (v0.10.1006)

**Operatör bildirimi + onayı ("olur düzelt"):** `function_code = …` çipinde tablo dolu, hacim şeridi "No
traces in view to bucket" (0 istek). **Kök neden:** şerit, çip giriş span'inde yaşayan bir anahtardaysa
kind IN (server, consumer) kısıtı ekler (v0.10.268 / 323); `channel_code`, `function_code`, `function_id`
`ENTRY_KEYS` listesindeydi ama prod'da bu iş kimliklerini giriş span'i taşımıyor — yalnız log-yayın MQ
span'leri (operatörün trace'inde 12 span'in 4'ü). Kısıt AND'lenince eşleşme sıfır. v0.10.323
(db.statement) ve v0.10.730 (name) ile aynı sınıfın üçüncü örneği. **Karar:** üçü de listeden çıkarıldı;
şerit bu çiplerde eşleşen SPAN'leri sayar, birim "spans", ipucu neyi saydığını yazar. `function_code`
için kanıt ekran görüntüsü; `channel_code` / `function_id` aynı yayın mesajının alanları olduğu için
birlikte çıkarıldı (yanılma bedeli: sayı trace sayısından büyük ve etiketli; tersi boş grafik). **Kabul
edilen bedel:** bir trace birkaç yayın span'i taşıyorsa şerit sayısı listedeki trace sayısından büyüktür.
1005'in listeyle ilişkisi: liste çipte trace düzeyinde eşleşir ve satırı tüm span'lerden kurar; şerit
span sayar — ikisi de etiketli.

## 2026-10-01 — Traces: çip trace'i seçer, satırı şekillendirmez (v0.10.1005)

**Operatör bildirimi (prod):** `function_code = …` çipiyle gelen listede Name kolonu kök span'in adı yerine
bir MQ "publish" span'inin adını gösteriyor; süre ve span sayısı da eksik (listede 21 ms / 4 span, trace
detayında 25 ms / 12 span). **Kök neden:** arama yokken çipler WHERE'de SPAN düzeyinde (terfi kolonu / kvh
indeksi budasın diye, v0.10.341) ve satır aynı WHERE'in bıraktığı span'lerden kuruluyor; çipi kök span
taşımıyorsa (prod'da fonksiyon kodunu yalnız log-yayın span'leri taşıyor) `anyIf(kök)` boş kalıp
`any(name)` eşleşen bir span'in adına düşüyor (v0.5.351 yedeği), süre/sayı da yalnız eşleşen span'lerden.
**Karar:** çip TRACE'İ SEÇER, satırı şekillendirmez — v0.10.258'in Errors için koyduğu kural çiplere
genellendi. Sayfa belli olduktan sonra satır alanları (ad, servis, rota, başlangıç, süre, span/hata sayısı)
çipsiz WHERE ile yeniden kurulur (`repairSpanScopedRows`): id listesi PREWHERE'de, pencere sayfanın kendi
zaman aralığı (alt sınır 5 dk geri — kök eşleşen span'den önce başlar); maliyet pencereden değil id
sayısından (≤ sayfa) gelir, extras'tan önce koşar. Servis / ortam / küme yüklemleri AYNEN kalır (satır
"çipler olmasaydı ne olacaksa" odur). Sıra ve sayfa üyeliği 1. geçişin kararıdır, değişmez. Yumuşak düşer:
onarım okuması hata verirse liste eski satırlarıyla döner. CSV dışa aktarımı (>500 id) onarılmaz.
**Reddedilen:** çipleri her zaman HAVING'e taşımak (trace düzeyi) — indeks budaması kaybolur, v0.10.341'in
kaçındığı tam pencere taraması. **Açık (operatör kararı):** aynı ekranda hacim şeridi boş — şerit
`function_code`'u giriş span'inde yaşıyor sayıyor (`ENTRY_KEYS`, volumeSeries.ts); v0.10.323 / 730 ile aynı
sınıf, düzeltme o anahtarı listeden çıkarmak (şerit eşleşen span'leri sayar, etiket "spans").

## 2026-10-01 — Oracle Problem'i: ilgili endpoint'ler kanıtta — özne servis KALIR (v0.10.1004)

**Karar (kuyruk "Problem'i endpoint'e bağlama", operatör "devam"):** Problem'in öznesi DEĞİŞTİRİLMEDİ —
`problems.service` gerçek servis olarak kalır (servis sayfası, ekip yönlendirmesi, bildirim süzgeçleri,
küme anahtarı hepsi ona bağlı; özneyi endpoint yapmak bunların tamamını sessizce değiştirirdi). Endpoint
ilişkisi EK KANIT olarak eklendi: kanıt panelinde "İlgili endpoint'ler" (≤5) — Problem'in Oracle
satırlarındaki trace'lerin geçtiği endpoint'ler, trace ve hatalı trace sayısıyla, /endpoint sayfasına
OLAY penceresiyle (−30 dk / +10 dk) bağlı. **Kaynak:** yeni sorgu yok — kanıtın zaten yaptığı span
okuması (`trace_id IN ≤50`, zaman sınırlı) iki kolon daha seçer (`kind`, `http_route`) ve aynı satırlardan
katlanır (`foldTraceEndpoints`). **Endpoint kimliği** /endpoints sayfasının kuralı: `http_route` doluysa
(giden çağrı hariç) yol = route; boşsa server/consumer span adı (RPC, link `entry=rpc`). **Trace başına
tek endpoint:** özne servis trace'te giriş span'i taşıyorsa onunki; taşımıyorsa trace'in EN DERİN hata
span'inin servisi (v0.10.892 kuralı — ilk hata span'i giriş noktasıdır, hep aynı gateway çıkardı); o da
yoksa en erken giriş span'i. Sentetik dış özne (`ext:…`) servis sayılmaz. Trace'i Coremetry'de olmayan
Problem'de blok hiç çizilmez. **Reddedilen:** Problem'i endpoint'e taşımak (yukarıdaki gerekçe) ve
fonksiyon kodundan endpoint çıkarmak (kodu taşıyan her serviste ayrı endpoint var; kanıt trace'i kesin).

## 2026-10-01 — Oracle: operasyon adı trace ve endpoint sayfasında (v0.10.1003)

**Karar (kuyruk 1, operatör "devam et"):** 1002'nin TERS yönü — ekranda görülen fonksiyon kodu Oracle
operasyon ADINA çevrilir. Tek kaynak bir SÖZLÜK: `GET /api/oracle/function-codes` (kod → en çok satırlı
≤3 operasyon adı + koda bağlı toplam ad sayısı; `oracle_error_log` son 7 gün, ≤5000 çift, sunucu 5 dk
önbellek, rol kapısı yok). **Neden sözlüğün tamamı tek cevapta:** trace başına istek atmamak için — FE onu
tek paylaşılan anahtarla (`useOracleFunctionCodes`) 5 dk tazelikle bir kez çeker ve YALNIZ ekranda bir
fonksiyon kodu varken. İki yüzey: **(1) Trace özet şeridi** — span'lerdeki `FUNCTION_CODE` / `function_code`
değerinden "Operasyon: <ad>" çipi; tıklayınca aynı fonksiyon kodunun diğer trace'leri (süzgeç anahtarı
span'de GÖRÜLEN yazımla). Trace kod taşımıyorsa ya da kod sözlükte yoksa hiçbir şey çizilmez — ad
uydurulmaz. **(2) Endpoint › Break down by** — beyaz listeye `function_code` boyutu (iki yazımı okuyan
DİZİ ifadesi: terfi kolonu her kurulumda yok ve harita statik; tarama zaten servis + rota + zamanla
sınırlı); değer hücresi "CAF0001 · <operasyon adı>". Kod birden çok operasyonda görülüyorsa en çok
satırlı ad + "(+N)" ve ipucunda diğerleri: 1:n ilişki gizlenmez. Sınır 1002 ile aynı: yalnız son 7 günde
Oracle'da hata satırı olan operasyonların adı bilinir.

## 2026-10-01 — Oracle: operasyon adıyla trace bulma — komut paleti (v0.10.1002)

**Karar (operatör: "operasyon ismiyle trace bulabilir miyim" → "yap"):** operasyon adı
(DIGITAL_TRANSFER_EFT_CONFIRM_SERVICE gibi) trace'lerde yok; arama KOMUT PALETİNE (⌘K / üst arama kutusu)
üçüncü sunucu taraflı kaynak olarak eklendi (`GET /api/oracle/operations?q=`, ≥3 karakter, 200 ms
debounce, 60 sn önbellek, rol kapısı yok — operasyon adı zaten Problem başlığında ve Trace › Logs'ta her
role görünür). Her isabet en çok iki sonuç: **(1) operasyon → trace'ler** — operasyonun son 7 gündeki
hata satırlarındaki fonksiyon kodları, Traces'te `FUNCTION_CODE = / IN` süzgeci olarak (başarılı + hatalı
TÜM trace'ler; fonksiyon kodu yoksa öğrenilmiş servisin hatalı trace'leri, ikisi de yoksa sonuç üretilmez);
**(2) son hata trace'i** — hata satırındaki en yeni trace kimliği, Logs sekmesinde (kesin eşleşme).
**Neden Traces sayfasına yeni süzgeç türü değil:** çeviri (operasyon → fonksiyon kodu) palette yapılıp
sıradan bir attribute süzgecine indiği için liste, sayım, grafik ve ısı haritası uçlarının hiçbiri
değişmedi; Traces'e özel bir `oraOp` parametresi her uçta ayrı çeviri isterdi. **Süzgeç anahtarının
yazımı sunucudan gelir** (`spanAttrKey` = terfi kolonu probe'unun doğruladığı yazım,
`chstore.PromotedAttrSpelling`): kullanıcı süzgeci harf duyarlı, sabit bir yazım v0.9.626 sınıfı sessiz
boş listeydi. Sınır: yalnız son 7 günde hata satırı olan operasyonlar bulunur (kaynak `oracle_error_log`);
hiç hata vermemiş operasyon adı Coremetry'de hiçbir yerde yok.

## 2026-10-01 — Oracle: fonksiyon kodu doğru kolondan okunur (v0.10.1001; 1000'in düzeltmesi)

**Bulgu (operatörün prod ekran görüntüsü):** güncel sorgu çıktısında hem `ERRORCODE` hem `FUNCTIONCODE`
var; `code` alanı (error.code) `ERRORCODE`'a bağlı (takma ad önceliği), `FUNCTIONCODE` eşlenmeyen kolon
olarak satırın attribute'u. v0.10.1000 fonksiyon kodunu `code` alanından okuyordu (v0.10.902 eşlemesi
varsayımı) → span'lerde hata kodu ("COR-…") aranıyor, hiçbir şey bulunmuyordu; kutu işaretlense de etkisi
sıfırdı. **Karar:** kod `oracle.FunctionCodeOf` ile okunur — `code` alanı bir fonksiyon kodu kolonuna
eşliyse (ad alt çizgisiz/büyük harf "FUNCTIONCODE" ile biter) oradan, değilse satırın aynı kurala uyan
attribute'undan. Yeni eşleme alanı / ayar YOK (sorgu çıktısında kolonun bulunması yeter). Sonuçları:
seri anahtarı (op, hata kodu, kanal) fonksiyon kodu taşımadığında seri, OPERASYONUNUN o tikteki fonksiyon
kodlarıyla çözülür; kapsam raporunun (operasyon, kod) dökümü aynı kuralı SQL'de uygular
(`oracleOpCodesSQL`, yerel ClickHouse'ta doğrulandı); satırlarda fonksiyon kodu hiç yoksa rapor bunu
söyler ("FUNCTIONCODE kolonu olmalı"). Hata kodu hiçbir durumda fonksiyon kodu sayılmaz.

## 2026-10-01 — Oracle: fonksiyon kodundan servis — öğrenen eşleme (v0.10.1000)

**Karar (operatör):** "Oracle'dan gelen DIGITAL_PAYMENT_EFT gibi operasyon adı trace'lerde yok, eşleştirebilir
miyiz" → evet, FONKSİYON KODU üzerinden; teyit: Oracle satırının kod alanı (özel SQL'de `FUNCTIONCODE` →
`error.code`) span'lerdeki `FUNCTION_CODE` ile aynı değer; "zaman içinde öğrendikçe güncellersin". Köprü:
satır (operasyon, fonksiyon kodu) → o kodu taşıyan span'lerin servisi; trace kimliğine gerek yok.
**Kaynak başına açılır** (`functionCodeMatch`, Ayarlar › Oracle › Problem üretimi; varsayılan KAPALI —
mevcut kaynakların öznesi değişmez). **Çözücü sırası:** trace → pod adı → öğrenilmiş → *fonksiyon kodu* →
bilinmiyor; yeni basamak yalnız öncekiler servis bulamadığında çalışır, çözülen hiçbir özneyi değiştirmez.
**Servis seçimi** (`oracle.PickFunctionService`): kodu taşıyan HATA span'lerinin (≥3 ise; yoksa tüm
span'lerin) ≥%70'i tek servisteyse o servis. Kod çağrı zinciri boyunca birden çok serviste taşınıyorsa
çoğunluk çıkmaz → servis uydurulmaz, not adayı söyler ("F200 3 serviste, çoğunluk yok"). **Öğrenme:** her
taze okuma (kod başına 10 dk önbellek), trace/pod oyu olmayan operasyonda haritaya (operasyon, kod) çifti
başına BİR oy yazar — aynı okuma üç tikte üç teyit sayılmaz; ≥3 teyit + ≥%70 ile "öğrenilmiş"e döner.
**Harita güncellemesi (yeni kural, tüm oy türleri):** mevcut servis hiç oy almazken aynı başka servis
ardışık ≥3 oy biriktirirse girdi ona döner (`LearnedEntry.alt/altHits`); mevcut servis oy alırsa sayaç
sıfırlanır. Eski kural tek tikte ≥3 oy istiyordu — tik başına 1-2 oy veren pod / fonksiyon kodu
kanıtında hiç sağlanmıyor, taşınan operasyon 30 gün "onaysız" kalıyordu. **Okuma yolu**
(`chstore.FunctionCodeServices`): ① geniş rollup (`rollup_spans_wide_1m/5m`, MV-first) varsa o; ② yoksa
terfi kolonu `attr_function_code` KAYITLIYSA ham spans (set(0) indeks, pencere ≤1 sa); ikisi de yoksa
basamak çalışmaz ve bunu söyler. Reddedilen: attribute dizisini açan servis süzgeçsiz ham tarama (1B
span/gün'de poll başına koşulacak sorgu değil). Geniş rollup prod'da önerilmediği için (2026-08-09 A/B)
beklenen prod yolu ②. **Ölçüm:** özne kapsamı raporu basamağı ayar KAPALIYKEN de ölçer ("açılırsa N satır
daha bağlanır, oran %X → %Y") — operatör açmadan etkisini görür; açıkken pay başlıkta. **Bu sürümde YOK
(ayrı karar):** operasyon adını trace / endpoint sayfasında göstermek, trace'leri operasyon adıyla aramak,
Problem'i servis yerine endpoint'e bağlamak.

## 2026-10-01 — Oracle: özne kapsamı raporu — önce ölç, sonra kapat (v0.10.999)

**Karar (operatör, Oracle odak "2" — "nasıl yapacaksın"):** trace'i Coremetry'de olmayan satırların
servissiz kalması (2026-09-23 canlı testinde ~%58) iki adımda kapatılır; bu sürüm yalnız ADIM 1.
**Adım 1 — ölç:** Ayarlar › Oracle'da kayıtlı kaynak kartında "Özne kapsamı"
(`GET /api/settings/oracle/{id}/subject-coverage`, admin, salt okuma, 60 sn önbellek): son 24 saatte
satırların yüzde kaçı bir servise bağlanıyor (öğrenilmiş eşleme / pod adından) ve bağlanmayanlar
NEDEN bağlanmıyor — satırlarda trace kimliği yok · trace Coremetry'de yok · çok servisli operasyon ·
eşleme henüz onaysız · öğrenilmiş servis canlı değil · operasyon kodu boş — artı en çok satırlı 20
çözülmeyen operasyon (en sık instance / host ile; instance pod adı biçiminde mi). Girdi
`oracle_error_log` (operasyon başına döküm, canlı Oracle'a gidilmez) + öğrenilmiş harita + canlı servis
adları; sınıflama saf (`oracle.BuildCoverage`) ve çözücünün kalıcı basamaklarını aynalar. **Neden önce
ölçüm:** özne yalnız Problem AÇILIRKEN çözülüyor ve cevap Problem notuna yazılıyordu; kaynağın bütününe
dair sayı hiçbir yerde yoktu, yani kapatma yöntemi tahminle seçilecekti. **Adım 2 — kapat (operatör
kararı, baskın nedene göre):** "instance pod adı değil" (host adı) baskınsa host adı → servis basamağı
(servisin span'lerindeki `host.name`); "trace Coremetry'de yok" + pod adı var ama servis canlı değilse
adlandırma kuralı (pod öneki ↔ servis adı) genişletilir; kalan az sayıda operasyon için elle op → servis
eşlemesi (pin); "çok servisli" operasyon tek servise bağlanmaz, servissiz kalması doğrudur. Çözücü,
eşikler (≥3 teyit, ≥%70) ve harita bu sürümde DEĞİŞMEDİ.

## 2026-10-01 — Oracle: canlıya geçiş önizlemesi (v0.10.998)

**Karar (operatör, Oracle odak "3": kaynak kipi gölge → canlı):** kipi operatör değiştirir; ürün
kararı VERİYLE verdirir. Ayarlar › Oracle'da kayıtlı her kaynak için "Canlıya geçiş önizlemesi"
(`GET /api/settings/oracle/{id}/live-preview`, admin, 60 sn önbellek, salt okuma): gölgede son 24 saat
/ 7 günde açılan Problem sayısı (kritik, küme, şu an açık, en çok açan 5 özne) ve bu Problem'lerin
bildirim türünü (**anomali** — `ProblemNotifyKind`) alan etkin kanallar + ekip maili. **Neden bu iki
soru:** canlı kip bildirimi yalnız AÇILIŞTA gönderir (`anomaly/external.go`), yani gölgedeki açılış
sayısı gidecek bildirim sayısının doğrudan ölçüsüdür; ve operatör anomali maillerini yanlış pozitif
yüzünden kapatmıştı (v0.10.814) — türü süzen kanal Oracle bildirimini de almaz, "canlıya aldım,
hiçbir şey gelmedi"nin en olası sebebi ekranda açıkça yazılır. Sayım `problems` FINAL'den, kaynağın
kural önekleriyle (`anomaly:ext:<kaynak>/`, `anomaly-cluster:ext:<kaynak>/`); seri kimliği her
açılışta yeni olduğu için açılışlar sayılabilir, küme kimliği anahtara sabit olduğu için küme sayısı
alt sınırdır ve öyle sunulur. Kip değişiminde zaten açık Problem'ler için bildirim gitmez (yalnız
yeni açılışlar) — önizleme bunu da söyler. **Değişmeyen:** tarayıcı, eşikler, tavan, kipin kendisi.

## 2026-10-01 — Argo CD: hub, instance'larıyla birlikte kaldırılabilir (v0.10.997)

**Karar (operatör: "Hub kaldıramıyorum instance varsa" — prod'da hub başına 190+ instance):**
Ayarlar › Argo CD'de instance'ları bağlı hub'ın "Kaldır"ı artık çıkmaz sokak değil. İlk tık hiçbir
şeyi kaldırmaz: ileti + "Hub'ı N instance ile birlikte kaldır" / "Vazgeç". Onay hub'ı ve ona bağlı
tüm instance'ları TEK taslak değişikliğiyle çıkarır; Kaydet'e kadar yazılmaz, "Değişiklikleri geri
al" geri getirir. Sunucu kuralı (BE4) DEĞİŞMEDİ: hub gövdeden çıkarken ona bağlı instance gövdede
kalırsa 400 — ikisi birlikte çıkınca kural zaten sağlanıyor, yani düzeltme yalnız arayüzde. Pin'i
olan instance kaldırılamaz kuralı da aynı: o hub'da onay sunulmaz, kaç pin olduğu söylenir (pin
editörü API'de). Kaldırılan instance'ların ClickHouse satırları (`instance_id`) TTL'e kadar durur.

## 2026-10-01 — MCP istemcisi çift dönemli: önce `server/discover`, olmazsa `initialize` (v0.10.995; denetim M2)

**Karar:** dış MCP sunucularına bağlanan istemci (`internal/mcpclient`) dönemi `Initialize`'da bir kez
belirler: `server/discover` yoklaması DiscoverResult ve `2026-07-28` döndürürse MODERN (el sıkışma
yok; her istek `_meta` + HTTP'de üç MCP başlığı, `Mcp-Name` gerekirse base64 nöbetçili; sonuçta
`resultType`), aksi hâlde LEGACY (`initialize` + `initialized`, v0.10.86 davranışı). **Düşüş kuralı
belirtimden:** tek bir hata koduna bağlanmaz (eski sunucular -32601, -32602 döner ya da hiç
yanıtlamaz → hepsi legacy); tanınan modern hata -32022 ise sunucu moderndir — ilan ettiği sürümlerde
konuşabildiğimiz varsa ona geçilir, yoksa "ortak sürüm yok" hatası (initialize'a körlemesine düşülmez);
-32020 / -32021 ve 401 / 403 düşüş üretmez. **Bedel:** legacy sunucuda bağlantı başına bir fazladan
istek; yanıt vermeyen legacy stdio sunucusunda ilk bağlantı yoklama tavanı (15 sn) kadar gecikir —
tavan bilinçli olarak kısa tutulmadı, çünkü yavaş açılan (`npx …`) modern bir sunucuyu legacy sanmak
initialize'da kalıcı hata üretirdi. **Desteklenmeyen:** MRTR (`input_required`) — sessizce boş sonuç
sayılmaz, açık hata. Legacy HTTP'de initialize'ın döndürdüğü sürüm sonraki isteklerin
`MCP-Protocol-Version` başlığıdır (2025-06-18+). Uçtan uca test kendi sunucumuza karşı: iki taraf
birbirinin başlık / `_meta` sözleşmesini doğrular.

## 2026-10-01 — MCP sunucusu çift dönemli: 2026-07-28 el sıkışmasız sözleşme (v0.10.994; denetim M1)

**Karar:** `POST /api/mcp` artık iki dönemi birden konuşur. İstek `params._meta` içinde
`io.modelcontextprotocol/protocolVersion` taşıyorsa (ya da yöntem `server/discover` ise) 2026-07-28'in
durumsuz sözleşmesiyle (`internal/mcp/modern.go`), aksi hâlde eski `initialize` yoluyla — o yol bayt
bayt aynı (testle pinli; Claude Code'un bugünkü istemcisi fark görmez). Modern yolda: zorunlu `_meta`
alanları (eksikse -32602 / 400), başlık ↔ gövde doğrulaması (`MCP-Protocol-Version`, `Mcp-Method`,
`Mcp-Name`; -32020 / 400), yalnız `2026-07-28` (başkası -32022 / 400 + `data.supported`), bilinmeyen
yöntem 404 (`initialize` ve `ping` dahil), her sonuçta `resultType: "complete"` + `_meta` içinde
`serverInfo`, liste / okuma / discover sonuçlarında `ttlMs` + `cacheScope: "private"` (uç kimlik
doğrulamalı), varlık bulunamadı -32602. Kapı (rol + hız) ve gözlem aynı handler'larda: modern yol
yalnız zarfı değiştirir. **Kaynak:** belirtimin kendisi okundu; denetim raporundaki M1 reçetesi alan
adlarında yanlıştı (`protocolVersions`, `_meta.protocolVersion`) ve rapor düzeltildi ("denetim
reçeteleri uygulanır" dersi — reçete değil birincil kaynak uygulanır). **Kapsam dışı:**
`subscriptions/listen` (list_changed yayınlamıyoruz), MRTR (sampling / elicitation yok),
`x-mcp-header`, logLevel; batch modern dönemde tanımsız → batch içindeki modern istek -32600.
İstemci tarafı (M2, `internal/mcpclient`) ayrı sürüm. CORS izinli başlıklarına üç MCP başlığı eklendi.

## 2026-10-01 — MCP `bubble_up` aracı yalnız dış istemcilere (v0.10.993; denetim V1 dilim 3)

**Karar:** BubbleUp MCP aracı olarak eklendi (`internal/mcptools/bubble_up.go`; kıyas
`chstore.ServiceBubbleUp`, yeni SQL yok) ama **uygulama içi sohbet kataloğuna GİRMEZ**. Yeni
ayrım: `externalOnlyTools` + `ChatToolList` — `Register` aracı dış MCP'ye kaydeder, sohbet ve
sunucu-yürütmeli inceleme `ChatToolList` okur (kaynak pinli). **Neden:** denetimin kararı "küçük
LLM için prefetch adımı, tool değil" (v0.10.992 o adımı getirdi); ayrıca sohbetin her tur yuttuğu
kompakt katalog 9.098 / 9.100 B'ydi — yer yoktu ve bütçeyi büyütmek her sohbet turunu
pahalılaştırırdı. Kompakt bütçe artık yalnız sohbetin gördüğü araçları sayar; alan kuralları
(dolu, taban/tavan, Türkçe) dış-yalnız araç için de geçerli. Maliyet dürüstçe ilan edilir: ham
spans taraması, pencere [300, 3600] sn (varsayılan 600), çağrı 15 sn tavanlı, yalnız ≥5 puan
ayrışan değerler; boş liste `note` ile gelir ("yoğunlaşmıyor" ≠ "bakılmadı"). Katalog 60 → 61.

## 2026-10-01 — CoSRE kök-neden demetine BubbleUp adımı (v0.10.992; dış skill denetimi V1 dilim 1)

**Karar:** "neden X bozuldu" demeti (guidedRootCauseBundle) RED'den sonra, deploy'dan önce bir
`bubble_up` adımı taşır: sorun hangi rota / pod / sürümde yoğunlaşıyor. Kıyas tek yerde
(`chstore.ServiceBubbleUp`; /rootcause'un iki fan-out'u ve verdict kataloğu da onu çağırır): hata
ailesi → hatalı span'ler aynı penceredeki tüm span'lere karşı, diğerleri → pencere önceki eş-boy
pencereye karşı. Metin en çok 3 boyut, yalnız ≥5 puan ayrışan; "ayrışma YOK", "kıyas kurulamadı"
ve "OKUNAMADI (sınıf)" ayrı cümleler (yokluk ≠ okunamadı). **Pencere bilinçli olarak 10 dk**
(`rca.ExtrasWindow`, katalogla aynı): açık problem varsa açılışını izleyen 10 dk, yoksa son 10 dk
— /rootcause paneli 1 saate kadar tarar ve ~40 sn sürebiliyor (v0.9.1082 ölçümü), sohbet cevabı
onu bekleyemez; üstüne 8 sn tavan (`rca.BubbleUpTimeout`), süre dolarsa adım "okunamadı" der.
Ortam süzgeci uygulanmaz (RED notu gibi, tüm ortamlar). **Uygulanmadı — karar ister (V1 dilim
2):** BubbleUp'ı `DeepEvidence`'a, yani sentezleyiciye taşımak; açık problem × tik başına ham
spans taraması ekler ("mevcutların hızlı ve doğru çalışması önce" direktifi). Dilim 3 (MCP
`bubble_up` aracı) ayrı sürüm. Yan bulgu v0.10.991: katalog satırı yüzdeleri oran (0–1) olarak
basıyordu ("%1 … %0").

## 2026-10-01 — Argo CD keşfi: 50 iş tavanı kalktı, "Tümünü ekle" (v0.10.990)

**Karar (operatör: "Argocd entegrasyonu da autodiscover etse daha iyi olacak, şu anda tek tek ekle
diyorum ve sadece ilk 50'yi bulduğu için eksikleri oluyor"):** ekip × ortam başına ayrı Argo CD
instance'ı (ayrı `job`) olan hub'da keşif 50 işte kesiliyor, 51. instance hiç aday olmuyordu.
Tavanlar: iş 50 → 500, iş başına namespace/exported_namespace değeri 100 → 500, çağrı 150 → 2000;
shard (`pod`) tavanı 100 kaldı; kaydedilebilir instance 100 → 500 (`maxInstances`). Süre bütçesi
aynı (60 s; istemci 75 s): sığsın diye iş başına çağrılar 4 eşzamanlı koşar
(`argocdDiscoverParallel`). **Tasarım değişmedi:** aday bulma yine `job` süzgeçli label-values
(§5.4 "asla süzgeçsiz"; hub genelinde tek toplu sorgu bilinçli olarak SEÇİLMEDİ), sayım yine iş
başına tek anlık count; bütçe ayırma ile sayım tek kilit altında (`argocdProbeRun.take`), yani
eşzamanlı kip tavanı aşamaz ve `calls` giden istek sayısıdır. Aday sırası iş listesinin sırası.
Arayüz: hub bloğunda "Tümünü ekle (N)" namespace'i belli tüm yeni adayları tek tıkla TASLAĞA koyar;
namespace'i bilinmeyen aday (durum B, çok namespace) elle kalır. "Öner, asla otomatik yazma"
(annex §7.2) duruyor: kayıt yine Kaydet'le yazılır. **Açık (operatör kararı):** arka planda
kendiliğinden kayıt (keşfi işçinin koşup blob'a yazması) bu sürümde YOK — instance kimliği kalıcı
CH `instance_id`'dir ve işçi açıkken her yeni instance hub'a tur başına sorgu ekler. Not:
argocd-metrics işçisi instance başına shard koşar; yüzlerce instance'ta tur maliyeti ölçülmedi
(bayrak varsayılan kapalı).

## 2026-10-01 — Trace: "Hızlı açıkla" düğmesi kaldırıldı (v0.10.989; v0.10.987 kararının tersi)

**Karar (operatör: "tracelere quick explain butonu koymuşsun onu kaldıralım"):** trace sayfasında
"CoSRE'ye sor"un yanındaki ikincil "Hızlı açıkla" / "Quick explain" düğmesi ve ona ait her şey
kaldırıldı: `?aiquick=1` paramı (AI_QUICK_PARAM, useAiSubject/AIExplainButton `quick`), gövdedeki
`quick:true` (explainInit, explainOptions.Quick), sunucunun `traceExplainPath` / `explainTraceQuick`
yolu, çekmecedeki "canlı okuma yok" notu, i18n anahtarları ve iki test dosyası. Uyumluluk katmanı
YOK (CLAUDE.md: özellik kaldırırken shim eklenmez): eski bir `?aiquick=1` linki artık yok sayılır ve
varsayılan incelemeyi açar; gövdede `quick` gönderen istemci de incelemeye düşer. Trace'te yine iki
yol var: varsayılan inceleme ve "Kodu da incele" (klasik istem + kod bağlamı). v0.10.986'nın klasik
üç başlıklı cevap biçimi değişmedi.

## 2026-09-29 — Trace: "Hızlı açıkla" ikinci düğme — tek atışlık klasik açıklama (v0.10.987)

**Karar (operatör: "3 seçenek yapalım"):** trace sayfasında "CoSRE'ye sor"un yanına ikincil
"Hızlı açıkla" / "Quick explain" düğmesi. Aynı özne (`?ai=trace`) + `?aiquick=1`; çekmece bunu
gövdede `quick:true` olarak gönderir, sunucu KLASİK tek atışlık yolu koşar (buildTraceExplainInput:
trace + loglar + Oracle satırları, SystemPromptTrace) — inceleme okumaları (dönem kıyası, pod,
deploy) ve adım akışı yok, tek LLM turu. Yol seçimi saf `traceExplainPath`: includeCode > quick >
inceleme (kod dalı zaten klasik istem + kod bağlamı). Önbellek anahtarı kodsuz klasik anahtar
(explainTraceClassicPrepared ile aynı satır). `aiquick` aicode/aisrc gibi yalnız o açılışta ve
paylaşılan linkte yaşar (useAiSubject özne değişiminde siler, quick ile yazar). Aynı trace'in iki
düğmesi aynı özneyi taşıdığından "açık" sayımı quick bayrağıyla ayrışır: biri açıkken ötekine tık
kapatmaz, kipe geçer. Panelde tek satır not: canlı okuma yok. **Neden:** v0.10.986 ilk cevabı klasik
biçime aldı; operatör yine de hızlı, okumasız eski cevabı ayrı bir düğme olarak istedi (seçenek 3).

## 2026-09-29 — CoSRE trace incelemesi: klasik üç başlık, kanıt kimliksiz, güven satırsız (v0.10.986)

**Karar (operatör: "trace'i açıkladığımda K1 T1 kesin gibi çıkarımlar yapıyor … kodu incele
dediğimde daha iyi sonuç veriyor, o hali olsa daha iyi olacak"):** "CoSRE'ye sor" ilk cevabı
"Kodu da incele" geçişinin biçimine geçer — `systemTraceBody` ile aynı üç başlık: «İşlem Akışı ve
Veri Özeti» / «Stacktrace Detayı» (yalnız kanıtta stacktrace varsa) / «Kök Neden ve Sonraki Adım»,
artı «Eksik veri» yalnız durumu ok olmayan kaynak varken. Kanıt kimlikleri ([T1], [L1], [K1] …)
sunucu istemde yine verir ama cevaba YAZILMAZ (modele yöneliktir); v0.10.972'nin "Güven: kesin /
olası" satırı kalktı. Arayüz kod değiştirmeden klasik davranışa döner: Kök Neden'in ilk cümlesi
Karar şeridi (`explainAnatomy` VERDICT_HDR; beşli önbellek metni için eski kural durur).
**Değişmeyen:** veri toplama (get_trace, loglar, dönem kıyası, pod, deploy, Oracle), kaynak durumu
künyesi ve sayı denetimi, dürüstlük kuralları (uydurma yok; ilişki ≠ neden; "log bulunamadı" ≠
"hata yok"; kanıt yetersizse söylenir). Takip eki de aynı başlıklara geçti. Önbellek revizyonu
inv-v0.10.986. **Neden:** v0.10.948/972'nin kanıt izi (kimlik + güven) cevabı okunmaz kılıyordu;
operatör kodlu geçişin düz anlatımını tercih etti. Uydurmaya karşı koruma istemdeki değer-aynen
ve "kanıt yetersiz" kurallarında sürer; kimlikler modele sunulmaya devam eder.

## 2026-09-27 — Argo CD: şimdilik yalnız metrik (API bağlantısı askıda)

**Karar (operatör: "argocd şimdilik metrikle"):** Argo CD entegrasyonu şimdilik yalnız hub'ların
Thanos'undaki `argocd_*` metrikleriyle çalışır; Argo CD API'sine bağlanılmaz. Ayarlar › Argo CD
sekmesindeki `apiUrl`/`tokenRef` alanları isteğe bağlı olarak KALIR (boş = bağlantı yok);
kaldırılmaz, çünkü karar kalıcı değil. Plan etkisi: Faz 3.3 (argocd-api işçisi: senkron
geçmişi, kesin uygulama↔iş yükü eşlemesi) ve ona dayanan Faz 4 (Azure DevOps zinciri) askıda;
Faz 3.1/3.2/3.4/3.5 metrikle sürer, sınıflandırma "tahmin" etiketiyle (karar 15). API açılınca
sekmeye alan girmek ve 3.3'ü başlatmak yeter; ayar şeması hazır.

## 2026-09-27 — Tablo standardı tamamlandı (dilim 5–7, v0.10.967 / 973 / 977)

**Karar (operatör: "Önerilerine evet", "Sırayla devam"):** 2026-09-26'da açılan tablo standardı
(dört tür, sessiz satır, renk yalnız sapmada, durumlar tablonun içinde) yedi dilimde bitti.
Dilim 5 primitife `detail` (durum satırında link/CTA) ve `colSpan` (statik tablo durumları)
ekledi; 6–7 kalan sayaçları süpürdü. `tableUnityRatchet` tavanları ölçülen değerde ve SON:
rawTable 47 (9 T1-muaf sohbet/lejant/iskelet + 38 gerekçeli statik), tdStyle 60 (lejant
tabloları, dinamik opaklık/maxWidth gerekçeli), containIntrinsicSize 3, trTitle 2,
inlineMonoStack 4 (uPlot canvas font dizgisi — var() çözülemez; çözümlü font token'ı
gelirse düşer), dtNoState 16 (kendini gizleyen "sağlıklı = boş" bölümleri, yorumla
gerekçeli); diğer altı sayaç 0. Kalan her sayı ratchet'in kendi yorumunda dosya+gerekçeyle
listeli; düşüş yeni bir olanak (KeyValue sıkı kip, canvas font token'ı) gerektirir, artış
suçlu dosyada düzeltilir, tavan yükseltilmez.

## 2026-09-27 — Dört küçük sertleştirme (v0.10.978)

**Karar (operatör: "Önerin" → dört öneri tek sürümde):**
1. **Boot Keeper muhafızı:** gözlem eksikse (roster'daki bir replika probe'a cevap vermediyse)
   hiç gözlenmeyen bir state tablosu kurulmadan önce Keeper'da `<önek>/<shard-dizini>/<t>/replicas`
   (gözlenen eski dizinler, yoksa `{shard}` makroları) ve `<önek>/state/<t>/replicas` okunur:
   yalnız eski yolda replika varsa ona katılır; birleşik varsa ya da hiçbiri yoksa birleşik (kural 4);
   ikisi de varsa birleşik + uyarı (kurulum zaten bölünmüş, sihirbaza yönlendirir); Keeper okunamazsa
   v0.10.971 davranışı + yüksek sesli log. Gözlem tamsa Keeper'a hiç gidilmez.
2. **Argo keşfi "yetki yok":** hub'ın Thanos'u 401/403 dönerse `errorType: unauthorized` +
   `upstreamStatus` (HTTP 502 kalır; URL/token yankılanmaz); sekmede token/rol yenileme adımı ve
   "Yeniden ara", diğer hub'ın sonucu durur.
3. **PUT /api/settings/argocd bayat yazım koruması:** sekme GET'teki `updatedAt`'ı
   `expectedUpdatedAt` olarak geri yollar; sunucu taze okuyup uyuşmazlıkta 409 `stale` döner;
   alan yoksa kabul (API/token çağıranlar) ama audit "önkoşul yok" der; pod içi mutex
   (pod'lar arası son yazan kazanır — belgeli kalan risk). Sekmede "başka biri değiştirdi —
   yeniden yükle" kutusu; yeniden yükleme sunucuda değişmemiş alanlardaki düzenlemeleri korur.
4. **CorePanel:** `title=""` ile boş `<h3>` basılmaz; `ariaLabel` prop'u (role=group) — Trace pod
   grafikleri çift duyurmaz.

## 2026-09-27 — §11 sorgu paketi shell script değil admin probe (v0.10.979)

**Karar (operatör: "Önerin" → "1: §11 sorgu paketini çalıştırılabilir hâle getir"):** Rollouts v2
Faz 2/3'ü açan §11 canlı sorgu paketi (K/D/R/H/N/T) `POST/GET /api/admin/rollouts-v2/probe` olarak
Coremetry'nin içinde koşar; shell script ya da Grafana tarifi değil. Gerekçe: token operatörün
eline geçmez (Remote Cluster `tokenRef`, `WorkerQuery` fail-closed; konsol ailesi fail-closed
olmadığı için çözülmemiş ref koşudan önce reddedilir); okuyucular mevcut olanlar (`WorkerQuery`,
`ConsoleQuery` etiketi silinmiş kopyaya — matcher'sız geçiş çifti yalnız matcher'da ayrışsın diye
`ConsoleInstantQuery.Dedup` eklendi —, `ConsoleLabels/LabelValues`, chstore T örneklemi); her
DEĞER koşu başına bir jetonlayıcıdan geçer (küme, host, namespace, job, ad, proje, repo, suffix,
pod, deploy_env; bilinmeyen etiket varsayılan-red), eşleme yalnız bellekte, rapor 1 saat pod
belleğinde, hiçbir yere yazılmaz. Asenkron (202 + yoklama): ~330 çağrı ve `[24h]` sorguları
Route'un 30 s'sini aşar. Rapor V1–V14'ü (v2detect.go başlığı) confirmed/refuted/unknown ile
yargılar; A (§11.6) ve V (§11.7) "skipped (metrics-only)". Bilinen sınırlar: rapor pod-yerel
(çok replikada 404 `none` + `pod`), H6 `[24h]` bölünmesi yok, arayüz yok (API + runbook).

## 2026-09-27 — Rollouts v2 P2.2 canlı KSM dedektörü §11'den önce kodlandı, bayrak kapalı (v0.10.982)

**Karar (operatör: "devam et … bitir işleri" — §11 probe'u henüz koşulmadan Faz 2'nin kodlanması):**
P2.2 (`internal/rollout/v2worker.go`, `v2fetch.go`, `v2thanos.go`, `worker_run.go`;
`internal/chstore/rollout_v2_store.go`) §11 K/D cevapları GELMEDEN yazıldı; V1–V14 doğrulanmadı.
Bu yüzden dedektör yalnız `system_settings["rollouts"]` **enabled=true VE source="v2"** iken koşar.
"rollout-detector" kilidi ve döngüsü bayrak İLK kez açılınca başlar (`WaitActive`, 30 s yoklama):
varsayılan kurulumda sorgu, CH okuması, koşu satırı, Redis anahtarı, kalp atışı ya da günlük satırı
yok. source=v2 P2.3'te okuma yolunu da çevireceği için bayrak §11 sonuçları + P2.3 ile birlikte
açılır. Doğrulanmamış varsayıma dayanan her yer fail-safe: yazmak yerine atla + koşu teşhisinde say.
- **Açmadan önce doğrulanacaklar:** K1.D/K1.S/K1.DS (V1: generation + observed_generation üç türde),
  K2.1/K2.3 (V2: `namespace`+tür etiketi, `owner_is_controller` değerleri — sorgu yalnız açık
  `"false"`'u dışlar), K1 (V2b STS revizyon serileri), K1.R/K3 (V3: RS owner/spec, 50k tavanı),
  K1/K6.4 (V4: `_created` yokluğu; varsa incarnation ondan), K0.5/K0.6 (V5: `timestamp()` iç
  toplamada scrape zamanı; V9 tazelik — dedektör `up`/ikinci KSM denetlemez, yerine toplu yokluk
  koruması), K0.4 (V6: `max by` + dedup=true sonrası tek seri), K2.3c (V7), K2.4b (V8 DS hash),
  K1.H/K5 (V10 yazım hacmi), D (V12: DC kapsam dışı), K2.2 (V14 imaj birleşimi; STS pod
  `controller-revision-hash` = `update_revision` adı). Ek: querier `max by (__name__, …)` ile
  metrik adını korumalı (§11 K1 biçimi; korumazsa bütün ölçüler `fetch_bad_sample`, satır yok) ve
  anlık sorguda `time=` parametresini kabul etmeli (dedektör tik sorgularını sabitler).
- **Tasarım kararları:** tik başına TEK `rollout_worker_runs` satırı (anahtar (worker, started_at,
  host) küme başına satırı çökertirdi; teşhis sayaçları `error` kolonunda, 0015 bayt-eşliği
  değişmez); hedefler etkin, URL'li **bütün** Remote Cluster'lar — Argo hub'ları DAHİL (karar 5 /
  §5.6: hub sıradan kayıttır, entity ve rollout işlemesi de alır; v1 reconciler da ayırmaz); bir
  kümenin bütün tik sorguları tek değerlendirme zamanında (tik − 15 s, `WorkerLimits.Time`) — aksi
  hâlde ardışık sorgular farklı scrape okuyup yeni RS'yi nesil artışından önce görebilir; lider
  belleği tikler arasında taşınır, edinim / liderlik kaybı / tik sırasında yeniden edinim /
  bayrak kapanışı / yazım hatası onu düşürür ve durum CH'den (FINAL keyset) kurulur —
  `V2Memory.Absent` DDL'de olmadığından boş başlar (çekirdek sözleşmesi). İmajlar açık olaylı,
  bekleyen, nesli değişmiş ya da ilk kez görülen iş yükleri için (≤100 iş yükü, ≤20 sorgu) +
  artan bütçeyle imajı boş durumlar (bootstrap baseline'ı; ≤20/tik; okuması boş dönen aday —
  ör. STS'de V8 etiketi yok — 2ⁿ tik, en çok 64, geri çekilir ki sonraki adaylar aç kalmasın);
  imaj okunamazsa fark atlanmaz, change_type çekirdeğin kuralıyla `rollout` kalır ve açık olayda
  sonradan düzelir.
- **`lockDegraded` (§10.3.1 mint öncesi okuma):** dedektör atlamaz (v1 gibi, §10.4) ama bu sürede
  durumu HER tik CH'den tazeler (yokluk sayaçları yereldir, korunur). Tek yazıcıda bellek CH'nin
  kendi yazdığımız görüntüsüdür; iki pod aynı yeni iş yüküne aynı tikte farklı incarnation basarsa
  sonraki tik ikisi de en yeni incarnation'a yakınsar (lost-state kuralı), eskisinin açık olayı
  kapanır — kalan risk yeni iş yükü başına en çok bir yinelenen `initial` satırı (`host`
  kolonuyla görünür). Maliyet yalnız degraded'da: pod × küme × tik başına iki keyset okuması.
  Redis reprobe'u gerçek kilidi takınca bayrak LeaderTTL(1 dk) = 3 dk daha doğru kalır
  (`cache.DegradedFlag`): holder'lar takası bir kalp atışı (≤60 s) sonra fark eder, diğer
  pod'lar kendi reprobe'larına dek Noop'ta liderdir — çok yazıcılı pencere bu kadar sürer.
- **Toplu yokluk koruması (V9 fail-safe, SÜRE SINIRLI):** taban = son kabul edilen tam okumada
  görünen iş yükleri; ailesi büsbütün yok okunan türde (çekirdek family_absent) taban düşmez —
  sonraki kısmi geri dönüş de korumayı tetikler. Yeniden kurulumdan sonraki ilk okumada taban
  last_seen_at'i son 25 sa (v2TouchEvery + 1 sa) içindeki durumlar, güncel incarnation'ının son
  olayı "gone" ile superseded kapanmış olanlar hariç; son 25 sa içinde açık olaysız silinenler yine
  sayılır (hata güvenli yönde: kapanış ≤30 dk gecikir, `mass_absence_rebuilt_base` teşhisi —
  tabanı ilk okumadan tohumlamak kısmi okumayla açılan yeniden kurulumda korumayı kapatırdı). Bir türde
  tabanın ≥20 iş yükünün yarıdan azı görünürse o türün YOKLUK işlemesi en çok 30 dk dondurulur
  (gone kapanışı ve yokluk sayacı geri alınır, koşu partial); görünen iş yükleri normal işlenir
  (START/SUCCEEDED gecikmez). 30 dk'yı aşan V9 ihlali yine sahte gone + dönüşte yeni incarnation
  üretir (bilinen sınır); süre dolunca taban sıfırlanır, sonraki düşüş korumayı yeniden tetikler.
- **P2.1 çekirdek düzeltmesi:** artışsız görülen yeni revizyon (`evidence_without_bump`) artık
  `known_revisions`'a katılmaz — katılırsa artış geldiği tik sahte ROLLBACK yazılır ve önceki
  başarılı rollout `rolled_back` olurdu (sorgu kayması ya da KSM informer kayması).

## 2026-09-27 — Rollouts v2 P3.1 argocd-metrics işçisi §11'den önce kodlandı, bayrak kapalı (v0.10.983)

**Karar (operatör: "devam et … bitir işleri" — §11 probe'u henüz koşulmadan Faz 3.1'in kodlanması;
"Argo CD: şimdilik yalnız metrik" kararıyla):** P3.1 (`internal/argocd/app_status.go`,
`metrics_worker.go`, `metrics_thanos.go`; `internal/chstore/argocd_status_store.go`) §11 H/N
cevapları GELMEDEN yazıldı. İşçi yalnız `system_settings["argocd"]` **enabled=true VE
metricsWorker.enabled=true VE ≥1 hub** iken koşar; "argocd-metrics" kilidi ve döngüsü bayrak İLK
kez açılınca başlar (`WaitActive`): varsayılan kurulumda hub sorgusu, CH okuması, koşu satırı ya da
Redis anahtarı yok. **Ayrı bayrak (inceleme):** üst düzey `enabled` P1'den beri "yalnız bayrağı
kaydeder" diye sunuldu; onu işçi anahtarı yapmak, P1/P2'de açmış kurulumlarda işçiyi deploy'la
(admin eylemi olmadan) başlatırdı — `metricsWorker.enabled` eski bloblarda yok = kapalı; Validate
entegrasyon kapalıyken onu da kapatır. Argo CD API'sine bağlanılmaz (P3.3 askıda). Ayarlar › Argo
CD'de entegrasyon kutusunun altında ayrı "Metrik işçisini çalıştır (argocd-metrics)" kutusu (kayıt
aynı admin PUT'u: audit + 409 bayat koruması). Doğrulanmamış varsayıma dayanan her yer fail-safe:
yazmak yerine atla + koşu teşhisinde say.
- **Açmadan önce doğrulanacaklar (§11 H):** H0.3 vs H0.5 her hub'da (küme etiketi Argo
  serilerinde var mı → `injectClusterLabel`; yanlışsa envanter 0 seri döner — boş envanter VERİ
  YOK sayılır: taban alınmaz, yokluk sayılmaz, `inventory_empty`; etiket düzeltilince okuma kapsamı
  değişir ve ilk dolu envanter bütün uygulamaları `baseline` yazar); `up` serisi aynı seçiciyle
  (namespace [+ job], aynı küme etiketi kararı) okunabilmeli ve `argocd_app_info` ile aynı `job`
  etiketini taşımalı — hedef sağlığı yalnız uygulama serisi üreten job'ların hedeflerinde sayılır
  (`up{sel} and on (job) group by (job) (argocd_app_info{sel})`; ikinci inceleme: job'suz seçicide
  ilgisiz kalıcı `up==0` hedefi — dex, redis-exporter — silmeyi sonsuza dek bekletiyordu); yokluk
  (`deleted`) yalnız bu hedefler sağlamken işlenir (hedef yok / `up==0` / sağlam hedef sayısı
  azaldı → o envanterde sayılmaz); `up` bulunamazsa silme HİÇ yazılmaz; bekletme 3 ardışık
  envanteri bulursa parça partial + not (`absence_held_streak`); H0.2/H0.4 (HA/shard kopyaları `max by`
  + dedup=true sonrası tek seri — aynı anahtarda iki farklı durum görülürse uygulama o tik
  `app_ambiguous`, satır yok); H1.2 (§5.2 durumu: A/C'de parça seçicisi `namespace=<instance ns>`
  doğru; B + apps-in-any-namespace KAPSANMAZ — eksik kapsama, yanlış satır değil); H1.1/H1.3
  (`metricsJob` tanımlıysa seçiciye eklenir; aynı job birden çok instance'ta sorun değil,
  namespace ayırır); H1.6 (parça başına uygulama ≤ `reader.maxSeries`; aşarsa parça her tik
  `shard_truncated` ile atlanır — dest_server alt parçalaması yok); H4 (`operation` etiketi ve
  değerleri; sabit olmayan küme = Synced+Healthy+işlem yok DIŞI; `autosync_enabled` yoksa '');
  H5 (`argocd_app_sync_total` da `exported_namespace` taşımalı — join anahtarı §5.3); H6
  (senkron hacmi ve `resets()`; controller yeniden başlayınca eski değere EŞİT yeni değer
  sıfırlanma olarak görülmez → o senkron kaçar, bilinen sınır); §5.1 `dry_run` (v3.1+) senkron
  seçicilerinde `dry_run!="true"` ile dışarıda (etiketsiz eski sürüm serileri de eşleşir). Ek:
  querier `changes()`, `offset`, `unless on (…)` ve `count_values` içeren anlık sorguyu `time=` ile
  kabul etmeli; okuma kapsamı ÖRTÜŞEN parçaların HEPSİ atlanır (birini okumak diğer hub'ın
  uygulamalarını yanlış instance/cluster_id altında yazardı): aynı Thanos + aynı namespace iki parça,
  ikisinde de dolu ve farklı `metricsJob` ya da ikisinde de aynı adlı farklı değerli enjekte küme
  etiketi yoksa çakışır (ikinci inceleme: job'suz seçici job'lunun, etiketsiz okuma etiketlinin üst
  kümesidir — yalnız birine job/etiket vermek ayırmaz).
- **Tasarım kararları:** parça = hub üzerindeki etkin instance (§5.4); ilk tik ve her
  `intervals.inventoryMin` tam envanter + tam sayaç tabanı, aradaki tikler (`intervals.metricsS`)
  yalnız sabit olmayan küme + senkron penceresi (değişen ya da yeni doğan sayaç, pencere
  `2×aralık…5 dk`) + sabite dönen / senkronu görülen ama gözlenmeyen uygulamalar için ada göre
  hedefli okuma (≤10×50 ad). Parçanın bütün sorguları tek değerlendirme zamanında (tik − 15 s);
  herhangi bir hata / kesik / uyarılı okuma parçanın farkını atlatır, bellek aynen. Satır yalnız
  tuple değişince ya da senkron bitince; uygulama başına tik başına TEK satır (senkron + değişim →
  tek `sync`); aynı aralıkta ikinci satır önceki satırın 1 ms sonrasına kayar (RMT anahtarı ezmesin).
  İlk koşu ve yeniden kurulumdan sonraki ilk tam envanterde bilinmeyen uygulama `baseline` (yeni mi,
  TTL'i dolmuş mu ayırt edilemez), sonrası `appeared`; taban envanterinde belirsiz kalan uygulama
  ilk gözlemde yine `baseline`; iki ardışık tam envanterde (hedefler sağlamken) yoksa `deleted`;
  bilinen uygulamaların yarıdan fazlası (≥20) birden yoksa silme en çok 30 dk dondurulur, HEPSİ
  birden yoksa (sayıdan bağımsız) hiç kabul edilmez (bilinen sınır: gerçekten tüm uygulamaları
  silinen instance'a `deleted` yazılmaz). Okuma kapsamı (hub, Thanos URL'si, etkin küme etiketi,
  seçici) değişirse envanter/sayaç/hedef tabanı yeniden alınır ve farklı tuple da `baseline`
  yazılır. Son satırı 150 günden eski değişmemiş uygulama tam envanterde `baseline` ile tazelenir
  (180 günlük TTL tek satırı düşürmesin). Başarısız/kesik envanter 2ⁿ × aralık (tavan
  inventoryMin) geri çekilir, arada ucuz okuma. Yeniden kurulum son satırı `deleted` olanları
  almaz (churn'lü instance 200k tavanına silinmişlerle dayanmasın); canlı lider de silinen
  uygulamayı bir tik sonra belleğinden düşürür (geri dönen yine `appeared`). Sayaç
  tabanı yokken yeni seri SAYILMAZ; pencerenin kaçırdığı artış envanterde `sync_missed` sayılır ama
  YAZILMAZ (zamanı bilinmeyen senkron satırı §7.5 penceresini yanıltırdı); tuple'ı bilinmeyen
  uygulamanın senkronu ertelenir. Lider edinimi / yazım hatası / bayrak kapanışı belleği düşürür,
  durum `argocd_app_status`'tan (FINAL, uygulama başına LIMIT 1 BY, keyset) kurulur; lockDegraded'da
  her tik yeniden okunur. Tik başına TEK `rollout_worker_runs` satırı (worker=argocd-metrics;
  `unmapped` = cluster_id'si boş yazılan satır). `thanos.WorkerLimits.NoClusterLabel` hub'ın
  injectClusterLabel=false kararını işçi yoluna taşır. Eşleyici (P3.2) ve metrics-only
  sınıflandırma (P3.4, §7.5) bu işçide değil. `/api/services/{name}/gitops` (bayraksız) bu
  sürümde v0.10.981 küme-içi kuralını korur (yalnız TAM `https://kubernetes.default.svc`);
  işçinin geniş kuralı (`:443`, sondaki `/`, `.cluster.local`) eşleyiciyle (P3.2) gelir.

## 2026-09-27 — Rollouts v2 P2.3 okuma yolu §11'den önce kodlandı, bayrak kapalı (v0.10.984)

**Karar (operatör: "devam et … bitir işleri" — §11 probe'u henüz koşulmadan Faz 2.3'ün kodlanması):**
P2.3 (`internal/api/rollouts_v2_read.go`, `internal/chstore/rollout_v2_read.go`,
`internal/rollout/v2read.go`; FE `lib/rolloutRow.ts`, `lib/serviceRolloutLabel.ts`, `pages/Rollouts.tsx`)
§11 K/D cevapları GELMEDEN yazıldı. Okuma yolu yalnız `system_settings["rollouts"]` **enabled=true VE
source="v2"** iken rollout_events'e döner — P2.2 dedektörünün koşma koşuluyla aynı bayrak; varsayılan v1'de
cevaplar bayt bayt bugünkü (handler testleri v1 gövdesini ve "v2 okuması çağrılmadı"yı pinler; değişen
yalnız önbellek anahtarlarının içi). Açma yolu arayüzsüz: `GET /api/settings/rollouts` → dönen
`settings` içinde `source:"v2"` → tam blobu `PUT` (audit `rollouts.settings.update`); geri dönüş `"v1"`.
P2.3 yazmaz; doğrulanmamış varsayımın bedeli yanlış GÖSTERİMDİR, yanlış satır değil.
- **Açmadan önce doğrulanacaklar:** P2.2 kaydındaki V1–V14 listesinin tamamı (gösterilen satırları o
  dedektör yazar) + `0015` sihirbazla uygulanmış olmalı (§2.5; tablo yoksa liste boş + dedektör notu,
  tail her tik hata loglar) + §10.6 "measure first": tail her 15 s (dinleyici varken) rollout_events
  FINAL taraması yapar — bölümsüz tabloda v1 workload_rollouts tail'iyle aynı sınıf; prod'da ilk hafta
  `system.query_log`'dan süre/okunan satır ölçülür + V14 (`kube_pod_container_info` `image` değeri
  `repo:tag`, `repo@digest` ya da `repo:tag@digest` — sonuncuda tag okunur; `SplitImageRef` bunu varsayar,
  başka biçimde imaj sütunu ham kalır) + sidecar'lı iş yükünde birincil imaj seçiminin (`PrimaryImagePair`:
  değişen repo > iş yükü adı > ilk) prod imaj adlarıyla tuttuğu +
  STS/DS `new_revision` doluluğu (boşsa Revizyon "—", kimlik yine 6 parça).
- **Tasarım kararları:** yeni rota YOK — FE kaynağı göremez (ayar ucu admin), cevap şekli aynı; liste ve
  istatistik gövdesine eklemeli `v2: true`, satıra eklemeli v2 alanları (`generation`, `incarnationAt`,
  `changeType`, `v2Status`, replikalar, tam imaj listeleri, `stuckReason`, …; `RolloutRow.V2` nil → anahtar
  yok). Durum v1 sözlüğünde (`progressing→in_progress`, `succeeded→completed`, `stuck→stalled`,
  `rolled_back`/`superseded` aynen; `?status=` sunucuda ters çevrilir, tablo testi `v2read_test.go`).
  Bağlantılar (karar 14): kodek parça sayısıyla ayırır — 6 parça (`cluster|namespace|kind|workload|
  incarnationAtMs|generation`) rollout_events, 5 parça eski bağlantı v2 açıkken de workload_rollouts'tan
  (TTL'e dek); v1'de 6 parçalı istek bugünkü 400. v2 kolon seti ayrı `storageKey` (`rollouts-live-v2`):
  "Kaynak" (detectedBy — v2'de hep ksm) ve Span düşer (karar 4; P3.4'ün tetikleyici kolonu "Kaynak"
  adını yeni kimlikle alır), Nesil + güncel/hazır/istenen replika gelir. Problem rozeti ve çekmecenin
  servisleri v2'de İŞ YÜKÜ düzeyinde (`RolloutWorkloadServicesBatch`, revizyonsuz, başlangıç − 1 sa):
  MV'nin STS/DS revizyonu imaj tag'i, v2'ninki controller revision — revizyonla eşleşmez. İstatistik
  süresi yalnız succeeded (`succeeded_at − started_at`); `initial` olaylar toplamda sayılır.
  `/api/rollouts/runs` ve boş liste notu v2'de `rollout_worker_runs` (worker="rollout-detector")
  okur (P2.2 yalnız yazıcıyı getirmişti). SSE tail kaynak başına ayrı kursör, kaynak değişince sıfırlanır.
  GitOps sekmesi (v0.10.981) aynı anahtarla rollout_events'ten. Her önbellek anahtarı kaynağı taşır.
- **§2.2 sapması (servis kapsamlı okuma):** audit "yeni rota, kendi dosyasında" diyordu; bunun yerine
  `/api/services/{name}/rollouts` sunucuda kaynağı seçer (`serviceRolloutsFor`): DeployHistoryPanel,
  sürüm çipi, ServiceCharts işaretleri, annotation şeridi ve CoSRE grafik anlatımı tek yardımcıdan
  beslenir, FE'de "hangi uç" dalı yok; pod-churn yolu P6'da emekli olur. KSM satırı pod-churn şeklinde
  `source:"ksm"` + iş yükü + durum taşır; `config` değişikliği `restart` sayılır (sürüm çipi ve deploy
  işaretleri saymaz), `podsRemoved`=0 (KSM emekli pod saymaz) — FE etiketleri `serviceRolloutLabel.ts`
  ("0 pods replaced" basılmaz). İş yükleri servisin son 7 günde span ürettikleri (MV): span'i olmayan
  iş yükünün rollout'u /rollouts'ta görünür, servis sayfasında görünmez (bilinen sınır, §10.6).
- **Ad:** «Deployment/Rollouts» — nav EN/TR, ⌘K etiketi + "deployments" takma adı, Topbar, kapalı
  durumu (metin artık olmayan "Settings → Rollouts" yerine sayfadaki «Etkinleştir»i söyler, §2.6 madde 5
  FE yarısı); `pageContext` `/deployment-report` → `rollouts` (ölü `deployment-report` PageId çıktı).
- **İnceleme düzeltmeleri:** v1 imaj/sürüm alanı sıralı listenin ilki değil `PrimaryImagePair` (istio
  sidecar'ı önde sıralanıp "1.20.1 → 1.20.1" + `VersionConstant` gösteriyordu); dedektör `updated_at`'i
  tik başı değil küme YAZIM anı basar (`v2StampWrite`; tik başı 15 s tail watermark'ını geçince keyset
  kursör satırı hiç görmezdi — v1 de yazım anını basar); tail kursörü yalnız başarıda ilerler
  (`rolloutTailPages`; v1'de de önceki anlam); v2 STS/DS "Traces →" süzgeci controller revizyonu değil
  `imageTag`, revizyonsuz Deployment deployment adıyla; DeployHistoryPanel KSM satırını zaman + iş yüküyle
  anahtarlar (aynı scrape zamanı).

## 2026-09-27 — Rollouts v2 P3.2 Argo uygulaması ↔ iş yükü eşleyicisi §11'den önce kodlandı, bayrak kapalı (v0.10.985)

**Karar (operatör: "devam et … bitir işleri" — §11 probe'u henüz koşulmadan Faz 3.2'nin kodlanması;
"Argo CD: şimdilik yalnız metrik" kararıyla):** P3.2 (`internal/argocd/mapping.go` saf çekirdek,
`mapper.go` işçi adımı; `internal/chstore/argocd_mapping_store.go`; `internal/api/service_gitops_mapper.go`)
§11 H/N/T cevapları GELMEDEN yazıldı. Eşleyici ayrı bayrak AÇMAZ: argocd-metrics işçisinin içinde koşar
(§10.4, lider "argocd-metrics"), yani yalnız **enabled=true VE metricsWorker.enabled=true VE ≥1 hub**
iken; varsayılan kurulumda sorgu, CH okuması ya da yazımı yok. GitOps sekmesi (`/api/services/{name}/gitops`)
yalnız aynı bayrak açıkken, kapsam kapısı açıkken (aşağıda, inceleme) VE servisin (cluster, ns)
çiftlerinde taze kenar varken tablodan okur; aksi hâlde v0.10.981 canlı yolu aynen (+ eklemeli `argo.source: "live"`). Resource (kesin) kenarı yalnız Argo
CD API'sinden gelir (P3.3, askıda) — yazılmaz; pod_label (§11 K2.4) yazılmaz.
- **Açmadan önce doğrulanacaklar:** P3.1 kaydındaki §11 H listesi (eşleyici o işçinin belleğini okur) +
  §5.3 dest_server biçimleri: Remote Cluster `apiServerUrls` Argo cluster Secret'ındaki yazımla
  normalleşince eşleşmeli (v2.x ham `spec.destination.server`, v3 çözülmüş; `""` = `destination.name`) —
  çözülmeyen uygulama ad kenarını HER kümeye alır (canlı yolla aynı kural), zayıf kenar almaz
  (`mapper_dest_unresolved`); §11 N5/N6 (suffix → küme tekilliği, `mapper_suffix_mismatch` ölçer —
  eşlemeyi değiştirmez) ve N1–N4 (ad ayrışma oranı `mapper_name_parsed/unparsed`, bileşen tam eşleşmesi
  `mapper_name_component_exact` vs `mapper_name_part_only`); §11 T / §9: iş yükü kimliği span'lerin k8s
  deployment/statefulset/daemonset adından (MV) ve span cluster değeri Remote Cluster'a eşlenmeli
  (`mapper_workload_cluster_unmapped`); `0015` sihirbazla uygulanmış olmalı (argocd_app_mapping); §10.6
  "measure first": filo iş yükü okuması (workload_revision_activity_1m, son 24 sa, GROUP BY, ≤200k satır,
  25 s) her mapperMin'de bir — prod'da ilk hafta `system.query_log`'dan süre/okunan satır ölçülür.
- **Tasarım kararları:** kural fonksiyonları canlı matcher'la ORTAK (`pinnedWorkloads`, `pinMatchesApp`,
  `nameEdgeMatches`; `TestMapperMatchesLiveMatcher` iki yolun aynı listeyi verdiğini pinler): manual (100)
  pinden, pinli (cluster, ns, workload) ad tahmini almaz, servis kartında manual kenarlı uygulamanın o
  servisteki ad kenarları düşer; name (mapping.nameConfidence) tire sınırlı ad parçası + dest_namespace +
  çözülmüşse küme; namespace (zayıf, mapping.namespaceConfidence) dest_server'ı çözülen her uygulama,
  candidates = o (cluster, ns)'e deploy eden uygulama sayısı — kartta yalnız "aynı namespace'te
  eşleşmeyen" sayısı. Kenar türü taşır (manual → pinin türü, name → MV `anyLast(workload_kind)`); türü boş
  iş yükü atlanır. Ad ayrıştırma (§6: envList + argoSuffix birleşimi, QuoteMeta, sona çapalı) kenarı
  DEĞİŞTİRMEZ, yalnız teşhis; base key / pairGroup çift uyarısı P3.5. Tur yalnız HAZIR instance'ları
  uzlaştırır (parça atlanmadı, bellek kurulu, son 3 × inventoryMin içinde dolu tam envanter); hazır
  olmayanın kenarı ne eklenir ne kaldırılır. Uzlaştırma tam satır: yeni kenar first_matched_at=şimdi,
  değişen ya da 20 saatten eski kenar first_matched_at taşınarak yeniden yazılır, istenmeyen canlı kenar
  removed_at=şimdi (bütün alanlar taşınır); okuyucu last_verified_at'i 26 saatten eski kenarı canlı
  saymaz (kopmuş hub / kaldırılmış instance kenarı 30 gün TTL'de kalsa da sekmeye gelmez). Fail-safe:
  iş yükü listesi tavanda kesikse, kenar sayısı 400k'yı aşarsa ya da okuma düşerse tur atlanır (yazım
  yok, koşu partial + "eşleyici: …"); hatalı tur da sonrakini mapperMin sonraya atar. Tur her mapperMin
  (10 dk), ayar kaydı ya da Remote Cluster kaydı değişince hemen. Koşu satırı: mapping satırları
  `rows_written`'a, iş yükü kenarı almayan uygulamalar `unmapped`'a eklenir (o tikte durum satırlarının
  cluster_id'siz sayısıyla toplanır; ayrım `mapper_*` teşhisinde). Sekmenin eşleyici yolu: son durum
  argocd_app_status'tan (son satırı 'deleted' ya da hiç satırı olmayan uygulama gösterilmez, not düşülür),
  "Senkron (24 sa)" metrikle görülen 'sync' satırlarının sayısı — işçi tik başına tek satır yazar ve sayaç
  tabanı yokken görülen artışı yazmaz, yani ALT SINIR (canlı yolun `increase()`'inden düşük olabilir);
  hub satırları sorgu sonucu değil kayıt durumu (kayıt yok/devre dışı ya da tokenRef çözülmüyorsa
  "skipped"). Önbellek anahtarı kaynak kararının girdisini (`mw`) açıkça taşır.
- **İnceleme (v0.10.985) — kapsam kapısı:** kenar tazeliği işçinin canlı olduğunu SÖYLEMEZ (değişmeyen
  kenar 20 sa'de bir dokunulur; hub Thanos'u düşen, parçası atlanan ya da envanteri eskiyen instance'ın
  kenarı 26 sa taze kalır, argocd_app_status yalnız değişimde yazıldığından donmuş durum "güncel"
  okunurdu, hub satırı "ok" derdi). Sekme tabloyu yalnız şu hâlde kullanır, aksi hâlde canlı yol +
  `argo.note`'ta sebep: hub'lardaki HER instance etkin ve her hub'ın ≥1 instance'ı var; son
  argocd-metrics koşusu (rollout_worker_runs, FINAL, 1 gün, LIMIT 1) var, `ok`, `started_at` ≤
  `argocd.MetricsRunFreshness` (2 × metricsS + tik bütçesi; varsayılan 7 dk) yaşında, son ayar kaydından
  sonra başlamış ve `scopes_total` ayardaki etkin instance sayısına eşit. İşçi, parçası tamam olduğu
  hâlde eşleyiciye hazır olmayan instance için her tik koşuyu partial yapar (`mapper_unready_instances`),
  yani `ok` = her instance okundu VE hazır. Başarısız eşleyici turu (okuma hatası/zaman aşımı, 200k iş
  yükü ya da 400k kenar tavanı, yazım hatası) da başarılı tura dek tur arası HER tiki partial yapar
  (`mapper_last_round_failed`; yalnız hatalı tik partial olsaydı 60 s tik / 10 dk turda koşuların ~%90'ı
  `ok` yazar, kapı son koşuya bakıp ≤26 sa bayat kenarla açılırdı). Kapı geçici bir Thanos uyarısında da kapanır (bir tik canlı
  yol) — bilerek: yanlış "güncel" yerine eski davranış. Devre dışı instance'ın kenarı kartta da atlanır.
  Servis kartı kenarları iki okuma, ayrı bütçe: iş yükü kenarları yalnız servisin (cluster, ns,
  workload) üçlüleri için, zayıf kenarlar ayrıca (ORDER BY'da önce geldikleri için ortak LIMIT'i doldurup
  sonraki kümelerin eşleşen kenarlarını kesiyorlardı); zayıf kesikte yalnız sayı alt sınır. "Senkron
  (24 sa)": lockDegraded'da iki pod'un bir tik arayla yazdığı aynı senkron, önceki 'sync' satırıyla aynı
  fazda ve ≤ metricsS sonraki satır sayılmayarak katlanır (lagInFrame; bitişik tikteki gerçek ikinci
  senkron da katlanır — sütun alt sınır kalır). Açmadan önce ayrıca doğrulanacak: prod'da argocd-metrics
  koşularının çoğunun `ok` olduğu (yoksa sekme hep canlı yolda kalır; sebep notta).
- **Bilinçli farklar (sekme, eşleyici yolu):** Ayarlar'da TANIMLI OLMAYAN bir Argo CD'nin (hub'da koşan,
  instance olarak eklenmemiş) uygulamaları tabloda yoktur — canlı sorgu hub'daki her argocd_app_info
  serisini görür (instance'ı çözülmeyen uygulamayı da gösterir); kapı bunu hub'ı sorgulamadan bilemez
  (inceleme). Küme-içi adres geniş kuralla (`:443`, sondaki `/`,
  `.cluster.local`) instance'ın kendi hub'ına çözülür (canlı yol bayraksız olduğu için TAM yazımda kalır —
  v0.10.983 inceleme); "aynı namespace'te eşleşmeyen" küme kapsamlı ve dest_server'ı çözülmeyen eşleşmemiş
  uygulamayı saymaz; canlı yolun topk(50) kesiği yok (tam envanter). İki hub aynı iş yükünü yönetirse iki
  kenar, iki satır (anahtar instance_id taşır; §5.6).
- **Kapsam dışı / sonraya:** iş yükü kaynağı yalnız span MV'si — KSM'de görülüp span üretmeyen iş yükü
  (rollout_workload_state) kenar almaz; P3.4 sınıflandırması rollout_events anahtarıyla birleşeceği için
  o birleşim P3.4'te eklenir. Pin düzenleyicisi yok (pinler API'den); Ayarlar › Argo CD metinleri artık
  "Faz 3" demiyor, pinlerin ne zaman kullanıldığını ve instance_id'nin durum + eşleme satırlarına bağlı
  olduğunu söylüyor.
