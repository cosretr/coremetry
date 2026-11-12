# Spec: Servis sağlık kontrolü (runbook)

Durum: ONAYLANDI 2026-11-12 (açık sorular önerilen varsayılanla bağlandı; dilim 1, 5, 6 bu onayla kapsanır) · 2026-11-12 · istek: operatör, birebir "Servis genel kontrollerini yapan bir runbook olsun; pod metriklerini, cluster sağlıklarını, datasource metriklerini kontrol edip sorun olup olmadığını söylesin. … Service healthcheck gibi olsun, ayrıca CoSRE'den tetiklensin."

## What

- **Ne yapacak:** Seçilen servis için tek tıkla salt-okunur bir kontrol turu koşar: servisin pod'ları, koştuğu cluster'lar, JBoss datasource bağlantı havuzları ve servisin kendi sinyalleri (RED, Problem, anomali, deploy, exception, Kafka lag, Oracle). Önce tek cümle yazar ("payment-api sağlıklı görünüyor" / "2 sorun var: pod restart, ORDERS_DS havuzu dolu"), altında kısa kontrol listesi gösterir.
- **Nerede görünecek:** servis sayfasında "Sağlık kontrolü" düğmesi, `/runbooks`'ta yerleşik "Servis sağlık kontrolü" satırı (servis seçicili), CoSRE sohbetinde ("X servisinin sağlık kontrolünü yap") ve MCP aracında. Dördü aynı Go fonksiyonunu çağırır.
- **Ne yapmayacak:** restart/scale gibi hiçbir eylem yok; zamanlanmış ya da otomatik koşu yok; sayfa açılışına istek eklemez; hükmü model vermez; maskeleme yok; yeni tablo yok.

"Datasource" = JBoss/WildFly uygulama sunucusunun veritabanı bağlantı havuzları (operatör doğruladı). Bir serviste birden çok datasource olabilir; her biri ayrı satırdır.

## Bugün ne var (koddan)

| Konu | Durum | Kanıt |
|---|---|---|
| Runbook | Ayrı `runbooks` tablosu (saved_views değil), adımlar çalıştırılabilir: manual / query / http / javascript / bash | `internal/chstore/runbooks.go:16,53-67,82`; koşular `internal/chstore/runbook_executions.go` |
| Runbook koşturma | `POST /api/runbooks/{id}/execute`, yalnız editor+ | `internal/api/runbook_exec.go:27`, `internal/api/api.go:912-927` |
| Yalıtım (v0.10.966) | bash adımı env izin listesi, `COREMETRY_*` geçmez | `docs/DECISIONS.md:949-961` |
| CoSRE ↔ runbook | Yalnız arar/okur (`search_knowledge`, `get_runbook`), çalıştırmaz | `internal/mcptools/knowledge_tools.go:222,330`, `knowledge_match.go:21` |
| **Servis taraması (çekirdek)** | `ReadServiceSweep`: paralel bölümler, toplam 15 sn bütçe, bölüm tavanları, "okunamadı ≠ yok" | `internal/mcptools/service_sweep.go:68-75,497,549-587,807`; önbellekli `:2039`; MCP `service_health_sweep` `:1961` |
| Sweep bölümleri | RED `:816`, problems `:876`, ops `:932`, pods `:1232`, JVM (OTel heap/GC, JBoss değil) `:1377`, deploys `service_sweep_checks.go:200`, faz-2 loglar ≤4 paralel `:1474` | aynı dosya |
| Pod sorguları | servis→pod `ServicePodKeys`; cpu/bellek/restart ailesi; waiting reason | `internal/thanos/pod_family.go:52,83,122`; `client.go:645` `PodMetrics` |
| Pod çağrısı | cluster başına paralel ≤6, cluster başına 6 sn, en çok 6 cluster | `internal/api/copilot_pods_thanos.go:24-25`, `mcp_service_pods.go:29,39` |
| Cluster | Özet ~10 skaler sorgu, ALERTS firing (2 sorgu); NotReady yalnız dashboard şablonunda | `internal/thanos/client.go:854,1476`; `promql.go:215,297`; `dashboard_templates.go:172` |
| Thanos işçi yolu | `WorkerQuery(ctx, clusterID, expr, lim)`, tek POST, 30/45 sn tavan | `internal/thanos/worker_query.go:78-83,154` |
| JBoss JMX | Yalnız keşif: `^(jvm|jboss)_` ad regex, datasource etiketi `data_source` / `xa_data_source`; üretim kodunda somut jboss metrik adı YOK | `internal/thanos/promql.go:536,549-575,586-602`; panel `frontend/src/pages/service/PodJmxInline.tsx:36` |
| Diğer sinyaller | Problem `mcptools/open_items.go:20`; ml_events `chstore/ml_events.go:201`; exception grupları `chstore/exception_inbox.go:790` (P1 api katmanında hesaplanır `api/inbox.go:2245`); Kafka lag VM `vmetrics/kafka.go:171`; Oracle `chstore/oracle_error_log.go:233` | — |
| CoSRE niyet | `intentAllowed` + `runGuidedRoute`; deterministik hüküm emsali `service_report` | `internal/api/copilot_intent.go:46-83`, `copilot_guided.go:2179,2932`, `copilot_service_report.go:683,1011` |

Sonuç: "çalıştırılabilir runbook" var ama genel amaçlı ve editor'e kapalı; servis sağlık taraması da var ama pod/cluster/JBoss DS kapsamı eksik ve yalnız CoSRE/MCP'den erişilir. İstek yeni bir runbook türü değil, **mevcut sweep'in genişletilip ürün yüzeyine çıkarılması**.

## Seçenekler

| | Seçenek | Artı | Eksi |
|---|---|---|---|
| A | Kayıtlı runbook + bash/http adımları | Bugünkü model | Hüküm yok, editor şart, bash yüzeyi, servis başına kopya |
| B | Yeni runbook adım türü `healthcheck` | /runbooks içinde doğal | Kayıt/validasyon değişir, koşu geçmişi `runbook_executions`'a yazılır, editor kapısı |
| **C (öneri)** | Sweep çekirdeği + yeni salt-okunur uç; `/runbooks`'ta kayıt dışı yerleşik satır | Şema yok, viewer koşabilir, CoSRE/MCP aynı fonksiyon | /runbooks'ta "yerleşik" ayrımı gerekir |

## Kontrol kataloğu (öneri: 13 kontrol)

Pencere varsayılan son 15 dk (sweep tavanı 6 sn içinde kalır). Durum: İyi / Dikkat / Sorun / Ölçülemedi. Eşikler sabit varsayılan, ayar yok.

| # | Kontrol | Kaynak | Kapsam | Dikkat / Sorun | Sorgu |
|---|---|---|---|---|---|
| 1 | Hata oranı + p95 (RED) | sweep RED (MV) | servis | hata ≥%2 / ≥%5; p95 önceki pencerenin 1.5x / 2x | 2 |
| 2 | Açık Problem | open_items | servis | P3 açık / P1-P2 açık | 1 |
| 3 | Süren anomali olayları | ml_events grupları | servis | ≥1 açık / ≥1 kritik | 1 |
| 4 | Son deploy | deploys | servis | son 30 dk'da deploy (bilgi, Dikkat) / — | 1 |
| 5 | Exception P1 | exception grupları | servis | P2 yeni / P1 açık | 1 |
| 6 | Pod restart + OOM | Thanos restarts, last_terminated_reason | cluster | ≥1 restart / OOMKilled veya ≥3 restart | ~3 |
| 7 | Bekleyen pod (CrashLoop, ImagePull) | waiting_reason | cluster | — / ≥1 pod | 1 |
| 8 | CPU / bellek limite oranı | PodMetrics | cluster | ≥%80 / ≥%95 bellek, CPU throttle bilgi | ~4 |
| 9 | Hazır replika | `kube_deployment_status_replicas_ready` (`promql.go:448`) | cluster | ready < desired / ready = 0 | 1 |
| 10 | Cluster sağlığı | node NotReady (yeni ifade) + ALERTS firing (servis namespace'i önce) | cluster | warning alert / NotReady node veya critical alert | 2 |
| 11 | JBoss datasource havuzu | Thanos jboss_ (aşağıda) | cluster × datasource | aşağıda | 1 |
| 12 | Kafka consumer lag | VM `kafka.consumer.records_lag_max` | servis | lag artıyor / 15 dk boyunca tavanda | 1 |
| 13 | Oracle hataları | oracle_error_log | servis | yeni ORA kodu / ≥10 olay | 1 |

Log hata oranı (ES) varsayılan koşuda YOK; yalnız 1, 6 veya 11 Sorun ise sweep faz-2 log yolu (≤4 paralel, kendi yüzey etiketiyle). Adında `-batch` geçen servislerde RED yük artışı hüküm üretmez (hata oranı kalır).

**JBoss datasource (kontrol 11).** Repoda yalnız test fikstürü ad var (`internal/metricsrc/resolver_test.go:165` `jboss_datasources_pool_active_count`). WildFly/EAP istatistik adlarından beklenen aile; ilk dilimde canlı keşifle (`jmxDiscoveryQuery`) doğrulanır, bulunamayan ad "Ölçülemedi" olur:

| Gösterge | Beklenen ad | Dikkat | Sorun |
|---|---|---|---|
| Doluluk = aktif / (aktif + kullanılabilir) | `jboss_datasources_pool_active_count`, `…_available_count` | ≥%80 | ≥%95 |
| Kullanılabilir bağlantı | `…_available_count` | — | 0 (5 dk boyunca) |
| Bekleyen istek | `…_wait_count` (artış) | >0 | sürekli artış 5 dk |
| Zaman aşımı | `…_timed_out` / `…_blocking_failure_count` (artış) | — | >0 |

Tek sorgu cluster başına: `{__name__=~"jboss_datasources_pool_(active|available|wait|timed_out|blocking_failure)_count.*", <servis pod seçicisi>}` gruplama `data_source, xa_data_source` (`promql.go:565-575` ile aynı). Satır adı: "Datasource ORDERS_DS (cluster-a)".

## Koşu bütçesi

- Toplam süre tavanı 15 sn (sweep `sweepBudget`); her bölüm kendi tavanında, yavaş kaynak diğerlerini bekletmez, süresi dolan satır "Ölçülemedi (zaman aşımı)".
- Sorgu: servis başına sabit ~9 + cluster başına ~13. Tipik (1-2 cluster) 22-35, tavan (6 cluster) ~90. Cluster sayısı sweep kuralı: span'lerden eşlenen cluster'lar, yoksa ≤6 (`service_sweep.go:1205`).
- Thanos: cluster başına ≤6 paralel istek, cluster başına 6 sn.

## Files (gönderim sırası)

1. `internal/mcptools/service_sweep.go` + `service_sweep_checks.go` — yeni opsiyonel bölümler: replicas, cluster (NotReady + ALERTS), JBoss DS; `SweepCheck…` bayrakları.
2. `internal/mcptools/service_healthcheck.go` (new file) — `ReadServiceHealthcheck`: sweep'i tüm kontrol bayraklarıyla çağırır, satırları 4 duruma indirger, deterministik özet cümlesi. Saf hüküm fonksiyonu + tablo testli.
3. `internal/thanos/promql.go` — NotReady ve JBoss DS ifade kurucuları (+ test).
4. `internal/api/service_healthcheck.go` (new file) — `registerServiceHealthcheckRoutes`, `registerRoutesExtra` ile.
5. `internal/api/copilot_service_healthcheck.go` (new file) — `guidedServiceHealthcheck` niyeti; `copilot_intent.go` `intentAllowed`/`intentNeedsService`; `copilot/prompts.go:2716` liste.
6. `internal/mcptools/` — `service_health_sweep` aracına `checks:"all"` girdisi (yeni araç yok); `testdata/toolset_catalog_golden.json` güncellenir.
7. `frontend/src/components/coremetry/ServiceHealthcheck.tsx` (new file) — sonuç paneli; `pages/Service.tsx:545-552` yanına düğme; `pages/Runbooks.tsx` yerleşik satır + `ServicePicker`.

## API surface

- `GET /api/services/healthcheck?service=&env=&window=` — viewer+ (salt-okuma; runbook execute'un editor kapısı uygulanmaz). Audit yok (okuma).
- `s.serveCached`, TTL 60 sn; anahtar `healthcheck:%x` = service, env, window, cluster listesi, kontrol bayrakları, sürüm digest'i (`correlateKeyDigest`, `internal/api/correlate.go:143`). Art arda tıklama kaynakları yeniden dövmez.
- ctx'e `chstore.WithQueryTag(ctx, "healthcheck")` (`internal/chstore/query_tag.go:35`): ES istek sayacında ayrı yüzey (`internal/logstore/es_meter.go:204`).
- Yanıt: `{verdict, sentence, rows:[{check, subject, state, seen, link}], measuredAt, partial}`.

## Schema changes

Yok. Koşu geçmişi ilk sürümde saklanmaz (gerekirse sonra `saved_views(page='healthcheck_run')`).

## UX surface

- Sonuç paneli (servis sayfasında açılır panel, /runbooks'ta sayfa içi): düz cümle, altında 4 sütun Kontrol · Durum · Ne görüldü · Nereye bakmalı. Renk yalnız Dikkat/Sorun satırında; "Ölçülemedi" gri ve nedeni yazar, asla "İyi" sayılmaz. Teknik kanıt (PromQL, ham değer) kapalı ayrıntıda. Statik `<table>` (≤ ~20 satır, sıralanmaz; gerekçe: kontrol listesi sabit sıralı).
- Yükleniyor: satırlar geldikçe değil, tek seferde (15 sn tavan) iskelet; hata: uç düşerse tek satır hata.
- CoSRE: önce deterministik cümle + tablo anında; model (gemma4) yalnız istenirse "kısa yorum" ekler, hükmü değiştiremez. Klasik açıklama düzeni.
- Viewer aynısını görür ve koşturabilir.

## Dilim planı (her dilim kendi sürümü, tek commit)

| Dilim | İçerik | Tek başına işe yarar | OK gerekir |
|---|---|---|---|
| 1 | Çekirdek (2) + uç (4) + servis sayfası düğmesi (7 kısmı); mevcut sweep bölümleriyle (kontrol 1-8) | evet | **evet**: servis başlığı düğme sırası (`Service.tsx:518-520` onaylı düzen) |
| 2 | Kontrol 9-10 (replicas, NotReady, ALERTS) | evet | hayır |
| 3 | Kontrol 11 JBoss DS + canlı ad keşfi | evet | hayır |
| 4 | Kontrol 12-13 (Kafka lag, Oracle) | evet | hayır |
| 5 | /runbooks yerleşik satır + servis seçici | evet | **evet**: /runbooks listesine kayıt dışı satır girer |
| 6 | CoSRE niyeti + MCP `checks:"all"` | evet | **evet**: niyet sınıflandırıcı listesi değişir, "sağlık" soruları yeniden yönlenebilir |

## Kapsam dışı

Otomatik onarım (restart, scale, rollback), zamanlanmış koşu ve bildirim, ayar düğmeleri, maskeleme, çok kiracılık, RAG, varsayılan koşuda ES taraması.

## Risk

Medium — yeni okuma yükü yalnız tıklamayla doğar ve önbellekli, ama cluster başına ~13 Thanos sorgusu 6 cluster'da ~90'a çıkar; CoSRE niyet listesi değişikliği mevcut `service_health` yönlendirmesini etkileyebilir.

## Estimate

~yarım gün (dilim başına ~1 saat).

## Open questions (önerilen varsayılanla)

1. CoSRE: yeni `service_healthcheck` niyeti mi, mevcut `service_health` niyetinin genişletilmesi mi? Öneri: yeni niyet, yalnız "sağlık kontrolü / healthcheck / runbook" ifadelerinde; mevcut davranış aynı kalır.
2. Servis sayfasında düğme yeri: "Sağlık" grubunun sağında ayrı düğme (`Service.tsx:545-552`). Öneri: evet, mockup ile.
3. /runbooks: listenin en üstünde "Yerleşik" etiketli tek satır mı, ayrı sekme mi? Öneri: tek satır.
4. Koşu geçmişi: ilk sürümde saklanmasın. Öneri: saklanmasın.

## Doğrulanmadı / ölçülmedi

- Prod Thanos'taki gerçek jboss_ metrik adları (yalnız test fikstürü; dilim 3 canlı keşifle doğrular). WildFly'ın `wildfly_` öneki kullanılıyorsa ad regex'i (`promql.go:536`) genişler.
- `PodMetrics` (`client.go:645`) gerçek sorgu sayısı; limite-oran ve hazır olmayan pod için hazır ifade bulunamadı.
- `ReadServiceSweepCached` (`service_sweep.go:2039`) anahtarının tüm girdileri hash'leyip hashlemediği.
- Exception P1 hesabı api katmanında (`api/inbox.go:2245`); mcptools'tan çağrılabilirliği.
- 15 sn bütçenin 6 cluster'da yettiği; sorgu sayıları tahmindir.
- "runbook yalıtımı v2 / v0.10.970" DECISIONS'ta bulunamadı (yalnız 966).

Spec'i onaylıyor musun? Açık soru varsa önce o.
