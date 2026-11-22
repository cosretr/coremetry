# Runbook — Coremetry MCP'yi Claude Code/Desktop'a bağlama

(v0.9.14, yetki katmanı v0.9.1136; tasarım:
docs/audit/mcp-claude-code-production-audit.md +
docs/plans/ai-assistant-design-2026-08-16.md §K7)

## 1. Token üret

Settings → API Tokens → **New token**, rol: **viewer** (bu sunucuya kayıtlı
tool'ların tamamı salt-okunur ve hepsi viewer seviyesinde — editor/admin
GEREKMEZ). `cmk_…` değeri yalnız oluşturma anında görünür; kasaya
koy. İptal: aynı ekrandan Revoke (anında, cache invalidation'lı).

### Token = rol, kullanıcı DEĞİL (bilinçli sınır)

`cmk_` token'ı bir kullanıcıya bağlı değildir; **kendi rolünü**
taşır (`Claims.Role` = token'ın rolü, `Claims.UserID` =
`token:<id>`). Sonuçları:

- **Yetki tamamen token'ın rolüdür.** Token'ı kim kullanıyorsa o rolle
  konuşur; kişi-bazlı kısıtlama YOK. Tek-kiracılı ürün duruşuyla
  tutarlı: kişiselleştirme değil, iptal edilebilir kimlik hedeflenir.
- **Rate bütçesi token başınadır** (aşağıdaki 60/dk). İki ajanı
  ayırmak istiyorsan iki token üret.
- **İzleme/denetim token adı üzerinden yapılır** (`token:<ad>`), kişi
  üzerinden değil. Ekip başına ayrı token üretmek en pratik ayrım.
- **Sıkma gerektiğinde**: en düşük rolle üret (viewer yeter), gerekmeyince
  Revoke et. Rol yükseltmek için yeni token üret — token'ın rolü
  sonradan değiştirilmez.

Rol zorlaması nerede: her tool/resource/prompt kaydı bir `MinRole`
taşır (`internal/mcp`), kapı (`internal/api/mcp_gate.go`) çağrı
öncesi token rolüyle karşılaştırır. Yetersizse JSON-RPC **-32001** ve
gereken rolü söyleyen okunur bir metin döner (model boşuna yeniden
denemez). Bugün bu sunucuya kayıtlı tool'ların tamamı `MinRole=""` (viewer tabanı; v0.9.1141'ta
beş keşif tool'u eklendi — list_operations / list_environments /
list_clusters / list_deploys / find_trace_by_span; v0.9.1142'de
find_trace_by_request_id — yapılandırılmış kurumsal istek numarası →
trace, penceresi kimliğin İÇİNDEKİ damgadan gelir; v0.9.1146'da üç analiz
tool'u — get_topology / get_blast_radius / get_log_histogram, üçü de
mevcut MV/logstore okumasını köprüler; v0.9.1147'de dört guided-parite
tool'u — get_db_health / get_messaging_health / get_pod_health /
list_problem_window_events, in-app guided sohbetin kanıt paketleriyle
AYNI veri katmanından; v0.9.1233'te get_exception_samples — bir istisna
grubunun gerçek oluşumları: stacktrace + trace_id pivotu, REST eşi
GET /api/exception-groups/{fp}/samples viewer'a açık; v0.9.1244'te iki
takım/sahiplik tool'u — list_teams / get_team_services, REST eşleri
GET /api/services-metadata ve GET /api/services?ownerTeam=… viewer'a
açık). Kayıt defteri v0.10.1050'te 62 oldu ama dış MCP yüzeyi
DEĞİŞMEDİ: eklenen `read_source_code` (panelde incelenen trace'in ya da
exception'ın servisinin deposundan kaynak kod okuyucusu) yalnız uygulama
içi panel takibinde (trace/span; v0.10.1053'ten beri exception da),
oturum kullanıcısına (cmk_ token'ına DEĞİL — token'ın rolü ne olursa
olsun) ve DevOps bağlıyken sunulur; bu sunucuya kayıtlı değildir
(`chatOnlyTools`, set/get/clear_context gibi). v0.10.1050'de tek MinRole
istisnasıydı (editor); v0.10.1052'te viewer'lara da açıldı — kayıt
defterindeki tool'ların tamamı viewer tabanında. v0.10.1122'de kayıt
defteri 64 oldu, dış MCP yüzeyi yine DEĞİŞMEDİ: `search_wiki` ve
`read_wiki_page` (kurumun Azure DevOps wiki'lerinden indekslenmiş runbook /
nasıl yapılır sayfaları) aynı sözleşmeyle yalnız uygulama içi sohbette,
oturum kullanıcısına ve wiki bilgisi açık + DevOps bağlıyken sunulur
(`chatOnlyTools`) — wiki içeriği MCP üzerinden dışarı çıkmaz.
v0.10.1133'te kayıt defteri 65 oldu ve dış MCP yüzeyi BİR araç büyüdü:
`resolve_oracle_operation` — Oracle operasyon adını (büyük harfli, alt çizgili;
span'lerde yok) span'lerin taşıdığı `FUNCTION_CODE` değer(ler)ine çevirir.
Salt okumadır (Coremetry'nin `oracle_error_log` kopyası, son 7 gün; canlı
Oracle'a gitmez), `MinRole=""` (REST eşi `GET /api/oracle/operations` rol
kapısız). Yalnız TAM ad eşleşmesi kod verir; alt-dize isabetleri `near_names`
olarak döner ve otomatik aranmaz. Etkin Oracle kaynağı yoksa `enabled:false`
döner; uygulama içi sohbette araç o durumda hiç sunulmaz.

v0.10.1189'da kayıt defteri 66 oldu: `similar_incidents` — CoSRE'nin vaka
hafızasından (kapanan Problem / çözülen exception grubu) benzer geçmiş olayları
döndürür; insan ONAYLI vakalar önce, onaysız taslak `confirmed:false` + uyarı ile.
Salt okumadır.

v0.10.1207'de 67 oldu: `resolve_business_code` — kanal kodu, fonksiyon kodu
(BQRZ001 gibi), operasyon adı ya da endpoint'i servis kapsamı OLMADAN
servislere çözer (servis başına çağrı/hata); belirsizde `ambiguous=true`.
Salt okumadır.

v0.10.1208'de 68 oldu: `search_code` — servisin deposunda Azure DevOps Code
Search (yalnız uygulama içi sohbet; DevOps + Code Search açıkken). Dış MCP
yüzeyi değişmedi.

v0.10.1271'de 71 oldu: `query_metrics` / `query_traces` / `query_logs` (CoSRE
adım 2) — PromQL/SQL/sorgu dili YAZILMAZ; aile/enum tabanlı yapısal istek,
sunucu sabit şablondan kurar. Her sonuç `evidence` {source, query, window,
env, cluster, rows|series, durationMs} taşır; boş sonuç `empty=true` +
"veri yok: …". pod_cpu/pod_memory/pod_restarts Thanos'tan, cluster kapsamlı
(çözülemezse `error.code=needs_cluster`). Salt okuma; sohbet + dış MCP. Eski
`query_metric` / `search_traces` / `search_logs` korunur.

v0.10.1275'te 72 oldu: `pivot_from_trace` (CoSRE adım 6) — trace → pod'ları
(Thanos, cluster eşlemeli) → aynı pod'ların JVM heap/GC'si (OTel; JMX
sorulmaz) → trace'in logları → kritik yol operasyonunun kendi p99'u, TEK
deterministik çağrıda (15 sn bütçe, halka başına tavan). `trace_id` ya da
`service` (+ operation/env/window/pick; en yavaş trace seçilir, ≤5 aday).
Zincir kopmaz: her halka `state` (ok|empty|partial|unmapped|degraded|error|
timeout|skipped) + notu taşır; `findings` ≤5, `playbook{symptom, families,
checked}`; halka başına `evidences[]` (ilki `evidence`). Salt okuma.
`get_problem_root_cause` artık her cevapta `playbook{families, source:
stored|on_demand|none, checked}` döndürür; `investigate:true` saklı denetim
izi yoksa aileleri son 60 dk'da canlı okur — hipoteze yazılmaz.

v0.10.1276'da 73 oldu: `service_health_sweep` (CoSRE adım 7) — TEK servisin
tam taraması: RED (önceki eş pencereyle), açık problemler, TÜM operasyonlar
(operation_summary_5m, env yok), tüm FUNCTION_CODE'lar (geniş rollup; yoksa
sınırlı ham kırılım), servisin eşlenen Thanos cluster'larındaki pod'lar (CPU/
bellek % limit, faz, son sonlanma, penceredeki restart artışı) ve JVM heap/GC.
Satırlar servis hükmüyle AYNI deterministik eşiklerle (internal/healthverdict)
puanlanır; ilk N bulgu (3; `deep:true` ile 5, `top_n` ≤8) sıralanır ve
YALNIZ onların logları okunur (pod → pod'un ERROR+ logları; operasyon/
fonksiyon → en yeni hatalı trace'in logları). `deep:true` ayrıca code.*
örneklemi + log desenleri. 15 sn bütçe; her bölüm `state` + not; bölüm
başına `evidences[]`. Salt okuma.

v0.10.1284'te 74 oldu: `explain_log_spike` (AI SRE Faz 1a) — log ARTIŞINI ne
açıklıyor: `spike_start_iso`/`spike_end_iso` verilmezse arama penceresinde
(`window` 15m|1h|6h|24h) en belirgin tepe bulunur (severity bantlı tek
histogram, medyan/MAD z-skoru, ≥1.5× + Poisson tabanı; düz seri →
`empty=true` "belirgin artış yok"); verilirse o aralık (≤6 sa) kıyaslanır.
Taban = önceki pencere [başlangıç − B, başlangıç), B = clamp(3×süre, 15 dk,
3 sa). Boyutlar: seviye bandı, pod / cluster (log alan eşlemesi rolleri;
eşlenmemiş → `unmapped`, okunmaz), servis + namespace (servis boşken),
logger, hata türü, mesaj DESENİ. `contributors` (≤5, boyut başına ≤2)
artışın FAZLASINI taşıyan değerler: `sharePct` (artıştaki pay), `spikePct`
/ `basePct`, `new` / `baseUnknown`, Türkçe `reason`. `level=error` yalnız
ERROR bandını açıklar. Maliyet: 1 histogram + 2 alan okuması (ES'te pencere
başına TEK `_search`) + ≤2 desen örneklemi (≤500 satır) + ≤12 örnek satır;
60 sn (artış 10 dk'dan eskiyse 5 dk) önbellekli. Salt okuma; sohbet + dış MCP.

### Kaynak araç setleri (toolset) — v0.10.1434

CoSRE faz D'de 75 oldu: `compare_baseline` — "ne değişti / neden yavaşladı": servis (ya da tek
endpoint) penceresini önceki dönem / dün / geçen hafta aynı saatle kıyaslar;
RED farkı, en çok değişen ≤5 endpoint, taban sonrası ilk görülen hata tipleri,
aradaki deploy'lar, bağımlılık ortalama gecikme farkı. YALNIZ 5 dk MV'ler
(service_summary_5m, operation_summary_5m, topology_edges_5m, service_version_5m);
60 sn önbellekli. Sonuç tablo + satır içi grafik bloğu taşır (pencere gölgeli).

v0.10.2449'da 80 oldu (operatör raporu 2026-11-22: prod soruları trace ve
endpoint ağırlıklı): `endpoint_lookup` (servis / operasyon / URL / domain →
giriş rotaları spanmetrics_1m'den, gözlenen host + yol + fiil + pod'lar sınırlı
ham okumadan, RED önce VM, domain → servis → pod entity modelinden),
`trace_where` (trace id ya da servis + errors|slow|messaging → span başına pod ·
cluster · karşı taraf · süre; hata kökeni, zaman aşımı, öz süre, dış çağrı),
`repo_lookup` (resource öznitelikleri → katalog pini → Azure DevOps → Argo CD;
yoksa dürüst unknown), `messaging_flow` (topic → üretici/tüketici + VM consumer
lag), `downstream_calls` (hedef başına VM client RED + topoloji kenarları).

v0.10.2453'te 81 oldu: `dependency_path` (servisten aşağı / yukarı ≤3 atlama
topology_edges_5m kenarı, dün aynı saatler ya da önceki dönem tabanına göre
fark, deploy işaretleri, kötüleşen kenarlar; sorulan servisten tümü kötüleşmiş
yolun en derin kenarı "olası kaynak" + sınırlı örnek trace; yukarıda etki alanı).

81 araç, okudukları veri kaynağına göre **12 toolset**'e bölünür
(`internal/mcptools/toolset.go`; her araç tam bir sette —
`TestToolsetsPartitionCatalog`). Sayı DEĞİŞMEDİ; gruplama yeni.

| Toolset | Araç | Kapatılabilir | Sağlık türevi (sorgusuz) |
|---|---|---|---|
| `services` | 18 | hayır | ClickHouse deposu bağlı mı |
| `traces` | 12 | hayır | ClickHouse deposu |
| `problems` | 10 | hayır | ClickHouse deposu |
| `changes` | 4 | evet | depo; rollouts bayrağı → not |
| `metrics` | 6 | evet | VM/CH yönlendirici; Thanos yok → `partial` |
| `logs` | 8 | evet | log deposu + arka uç; Ayarlar ekranında önbellekli (60 sn) 2 sn Ping |
| `k8s` | 4 | evet | entity katmanı bayrağı |
| `oracle` | 1 | evet | etkin Oracle kaynağı |
| `devops-code` | 2 | evet | DevOps + Code Search / kaynak okuyucu |
| `devops-wiki` | 2 | evet | wiki bilgisi + DevOps |
| `knowledge` | 3 | evet | depo; RAG hazır değil → not |
| `meta` | 5 | hayır | — |

Operatör Ayarlar › CoSRE › **Kaynak araç setleri**'nden (admin; audit
`settings.ai_toolsets.update`, ayar `system_settings` `ai.toolsets`) bir
seti kapatırsa: uygulama içi sohbet o araçları HİÇ görmez; dış MCP
`tools/list` boot anlık görüntüsü olduğu için aracı listelemeye devam
eder ama `tools/call` adlandırılmış bir retle döner (**-32001**,
`toolset "logs" is disabled by the operator …`; rate bütçesini tüketmez,
"yeniden deneme" der). `get_capabilities` sonucunda ek `toolsets` alanı
her setin `enabled` + `health` durumunu verir — kapalı/sağlıksız seti
çağırmadan önce oradan bak.

### "Benim servislerim" MCP'de YOK (bilinçli)

Takım tool'ları takım ADIYLA çalışır. Sebep yukarıdaki sınırın
doğrudan sonucu: `cmk_` token'ı bir ROL taşır, bir kullanıcı değil —
"benim takımım" o yolda tanımsızdır ve kimliğe bağlansaydı sessizce
boş dönerdi. Kimlik-farkındalıklı sorular (`benim servislerim`,
`takımımın problemleri`) uygulama içi sohbetin guided yolunda
çalışmaya devam eder (orada oturum kullanıcısı → `users.team`).
Dış ajan için doğru akış: `list_teams` → operatöre hangi takım
olduğunu sor → `get_team_services`.

## 2. Bağlan

**Birincil — Streamable-HTTP (önerilen; çok-pod kurulumda afinite
GEREKTİRMEZ, stateless):**
```bash
claude mcp add --transport http coremetry \
  https://<coremetry-host>/api/mcp \
  --header "Authorization: Bearer cmk_..."
```

**Legacy — SSE (eski istemciler; çok-pod'da sticky-session ister):**
```bash
claude mcp add --transport sse coremetry \
  https://<coremetry-host>/api/mcp/sse \
  --header "Authorization: Bearer cmk_..."
```

Claude Desktop: Settings → Connectors'a aynı URL + header.
Doğrulama: Claude Code'da `/mcp` → coremetry **connected**; sonra
üç uçtan uca çağrı iste: `list_services` (MV, ucuz) →
`list_problems` → `search_logs` (prod'da ES yolu — gerçek maliyet
yolu da test edilmiş olur).

## 3. Hangi tool / prompt ne zaman

| İhtiyaç | Araç |
|---|---|
| "Şu an ne sağlıksız?" girişi | `list_services`, `list_problems`, `list_anomalies` |
| Servis kazısı | `get_service_health` |
| "Yukarımda/aşağımda ne var" · servis grafiği | `get_topology` (odaklı: upstream/downstream; boş `service` = filo kenarları; 1 hop) |
| "Bu bozulursa kim bozulur" · etki cümlesi | `get_blast_radius` (YUKARI-akış çağıranlar + cascade bayrağı; aşağı-akış get_topology'de) |
| "Hata ne zaman başladı" · log hacmi şekli | `get_log_histogram` (severity bantlı kovalar; satırlar için `search_logs`) |
| "Pod'lar / JVM ne durumda" | `get_pod_health` (servisli: envanter + heap; servissiz: filo heap sıralaması; restart/faz YOK) |
| "Hangi db yavaş" · "kuyruk tarafı nasıl" | `get_db_health`, `get_messaging_health` (filo geneli; consumer-lag ÖLÇÜLMÜYOR) |
| "Dün gece neler oldu" · vardiya devri | `list_problem_window_events` (açılan + ÇÖZÜLEN; `list_problems` yalnız şu anki kümeyi verir) |
| İz → log/metrik pivotları | `get_trace`, `get_logs_for_trace`, `get_metrics_for_span` |
| "Nasıl bakarım / nereden görürüm" · ürün yol tarifi | `product_guide` (sunucu adımları + uygulama bağlantıları; sayfayı açmaz, v0.10.809) |
| Metrik sorgusu / histogram ucu | `query_metric`, `get_exemplar_traces` |
| Async zincir takibi | `get_linked_traces` |
| Olay anlatımı / runbook önerisi | prompt: `explain_problem`, `suggest_runbook` |
| Deploy şüphesi / iz karşılaştırma | prompt: `deploy_impact`, `compare_traces` |

## 4. Limitler ve davranış

- **Rate limit:** kimlik (token) başına **60 çağrı/dk**; aşımda LLM'e
  JSON-RPC **-32000** olarak "rate limited … retry in Ns" döner — model
  bekleyip devam eder (bağlantı kopmaz, 429 yok). v0.9.1136'dan beri
  bütçe `tools/call` + `resources/read` + `prompts/get` toplamıdır
  (üçü de aynı chstore okumalarını yapıyor; eskiden yalnız
  `tools/call` sayılıyordu). `initialize`/`ping`/`*/list` limitsiz —
  veri okumazlar.
- **Rol reddi (-32001) bütçeyi TÜKETMEZ** ve "bekle" demez: metin
  gereken rolü söyler, model istemeden yeniden denemez.
- **Kapalı toolset reddi (-32001, v0.10.1434)** de bütçeyi tüketmez:
  metin aracı ve kapalı seti ADIYLA söyler, kimin/ne zaman kapattığını
  söylemez.
- Streamable-HTTP tamamen stateless: her POST bağımsız — LB hangi
  pod'a düşürürse düşürsün çalışır. SSE yolunda ise session pod-lokal:
  çok-pod'da `service.sessionAffinity: ClientIP` (chart v0.6.21) ya da
  cookie-sticky ingress şart.
- Tüm tool'lar salt-okunur; mutation tool'u yok (eklenirse audit_log
  source alanı tasarım notu: audit §7).

## 5. Sorun giderme

| Belirti | Neden / çözüm |
|---|---|
| 401 | Token süresi/yanlış değer — Settings'ten yeni token |
| JSON-RPC -32001 "requires the … role" | Token'ın rolü o tool/resource/prompt için yetersiz. Token rolü sonradan değişmez: doğru rolle YENİ token üret, eskisini Revoke et |
| -32001 "unrecognized MinRole" | Kayıt defterinde yazım hatası (Coremetry bug'ı) — kapı bilinçli olarak KAPALI yönde davranır; sürümü not edip bildir |
| `/mcp` "failed to connect" (http) | URL `/api/mcp` mi (sse path'i değil)? Proxy POST gövdesini kesiyor mu? |
| SSE bağlanıyor, çağrılar "unknown session" | Çok-pod + afinite yok → Streamable-HTTP'ye geç (kalıcı çözüm) |
| Sık "rate limited" | Ajan tool-loop'ta — sorguyu daraltın; limit kimlik başına, ikinci token ayrı bütçe demektir |
