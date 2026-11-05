# Log Search yeniden tasarımı — Faz 0 audit

Tarih 2026-11-05 · HEAD ≈ v0.10.1810 · Uygulama kodu değişmedi, commit yok.
Mockup: `docs/frontend/mockups/log-search.html` (4 görünüm, 3 tema, sahte veri).
Yollar repo köküne göre. `es.go` = `internal/logstore/elasticsearch.go`, `FE/` = `frontend/src/`.
"doğrulanmadı / ölçülmedi" = koddan teyit edilemedi; operatörün koşacağı sorgu §10'da.

## 1. Özet ve önerilen karar

**Öneri:** `/logs` yerinde evrilir, yan yana ikinci sayfa açılmaz. Bu iş Logs'un shadcn sayfa dilimidir.

- 15 maddenin 4'ü var, 9'u kısmen, 2'si yok (§2). İskelet v0.10.1498–1502'de Discover düzenine geçti.
- URL sözleşmesi altın listeyle kilitli (`docs/DECISIONS.md:10954`). İkinci sayfa ES yükünü ikiye katlar.
- Her davranış değişikliği ayrı sürüm, tek-commit revert, önce operatör OK'i (§8 "OK" sütunu).

| Konu | Karar |
|---|---|
| KQL | Mevcut `internal/logql` ayrıştırıcısı KQL yazımlarıyla genişler. ES'e kanonik Lucene `query_string` gider. ES DSL derleyicisi yazılmaz. |
| Tablo yığını | `useDataTable` üstünde kalınır. TanStack kararı Traces dilimini bekliyor; Logs onu öne çekmez. |
| Trace önizleme | Sayfadaki trace_id'ler TEK sorguda `trace_summary_5m`'den okunur. Pencere log zaman damgalarından türer. |
| Alan istatistiği | `random_sampler` ile örneklenir; yüzdeler "~" ile gösterilir. |
| Maskeleme | Yalnız LLM'e giden log metni maskelenir. Saklanan, gösterilen, dışa aktarılan veri tam kalır. CLAUDE.md kuralıyla çelişir: ilk açık soru (§9-1). |

**Üç büyük risk**
1. **ES maliyeti.** Alan istatistiği bugün örneklemesiz, tam pencere `terms` agg (`es.go:1101`). Milyar dokümanda 10 sn soft timeout'a dayanıyor.
2. **KQL.** Arayüz "KQL" diyor, ES Lucene çalıştırıyor. Küçük harf `and/or` ES'te terim sayılır (ES davranışı; doğrulanmadı). Sözdizimi hatası HTTP 500 döner.
3. **trace_id.** Loglar uygulamalardan doğrudan yazılıyor. Biçim (32-hex / UUID / büyük harf) ve span_id oranı ölçülmedi. Birleşik görünümün değeri bu orana bağlı.

**Önermediklerim**
- ES DSL derleyicisi: prod anahtarı `field_caps` / `_mapping`'de 403 alabiliyor (memory; güncel durum doğrulanmadı). Mapping bilmeden term/match seçilemez.
- Async search (Faz 1): bağlantı kapanınca ES aramayı zaten iptal eder; kazancı yalnız ara sonuç. 24 sa hedefi tutmazsa açılır (§8 4.5).
- ES `collapse` ve ES highlight: ek maliyet. Gruplama ve vurgu yüklenen örnekte, istemcide yapılır.
- Ham `spans` üzerinde toplu `trace_id IN`: bloom süzgeci 100 kimlikte işe yaramaz (§4).
- CoSRE'ye genel ES sorgu aracı ve RAG'e dayanan açıklama.

## 2. Mevcut durum — 15 madde

| # | Madde | Durum | Kanıt | Hedefe uzaklık | Faz |
|---|---|---|---|---|---|
| 1 | Sorgu çubuğu | kısmen | `FE/pages/logs/LogsQueryBar.tsx:100`, `FE/components/KqlSearchInput.tsx:218,324` | Alan ve değer tamamlama var. Geçmiş yok. ES hatası 500 ve ham metin. KQL yazımları çevrilmiyor. | 1 |
| 2 | Zaman + histogram | var | `FE/lib/utils.ts:5`, `FE/lib/rangePicker.ts:22`, `FE/components/LogsHistogram.tsx:76,295` | Çizim `TimeChart` ile; `CorePanel`'e taşınacak. | 1 |
| 3 | Alan kenar çubuğu | var | `FE/components/LogFieldsPanel.tsx:63-176,342-388` | İş alanları bölümü yok. İstatistik örneklemesiz. | 2 |
| 4 | Filtre hapları | kısmen | `FE/pages/logs/LogsFilterRow.tsx:115-148`, `FE/pages/logs/LogPillEditor.tsx:76` | Aç/kapa, ters çevir, düzenle var. Sabitleme yok. | 2 |
| 5 | Doküman tablosu | kısmen | `FE/components/LogTable.tsx:302-316,482,509,620`, `FE/pages/Logs.tsx:78,545,1332` | Kolon seçimi, vurgu, Tablo/JSON, "Load more" var. Yükseklik, yoğunluk, örnek boyutu, sayfa boyu, çoklu sıralama, sürükleme, stack katlama yok. | 1, 3 |
| 6 | Çevre loglar | var | `FE/components/LogContextModal.tsx:168`, `internal/api/api_logs.go:842` | Modal; satır detayına taşınacak. | 3 |
| 7 | Kaydedilmiş aramalar | var | `FE/lib/logsUrl.ts:52-87`, `FE/components/SavedViewsBar.tsx:145` | Yeni görünüm ayarları URL'ye eklenecek. | 5 |
| 8 | Servis odaklı başlangıç | kısmen | `FE/pages/Logs.tsx:211-215`, `FE/components/ServicePicker.tsx:77-95`, `FE/components/LogTable.tsx:46` | Varsayılan: tüm servisler + "Takımım" (ES takım alanı). Sağlık, akıllı kolon, iş alanı süzgeci yok. | 2 |
| 9 | trace_id deneyimi | kısmen | `FE/components/LogTable.tsx:579-596`, `FE/pages/logs/logsQuery.ts:26`, `FE/components/SpanDetail.tsx:501` | Link, kopyala, peek, yapıştırma çipi, ters yön var. Önizleme, menü, gruplama, birleşik görünüm, örneklenmiş durumu yok. | 3 |
| 10 | Bağlamlı histogram | yok | `FE/components/charts/TimeChart.tsx:114`, `internal/api/annotation_routes.go:51` | Veri ucu ve çizim desteği hazır; bağlanmamış. | 4 |
| 11 | Desenler | kısmen | `FE/components/LogPatternsPanel.tsx:78-94,124,182` | Sayı, Δ/YENİ, tıkla-süz var. İlk görülme ve Log-ML ortak kimliği yok. | 4 |
| 12 | İki aralık | kısmen | `internal/api/api_logs_patterns.go:140-157` | Yalnız "önceki eşit pencere". Kullanıcı seçimi yok. | 4 |
| 13 | Exception farkındalığı | yok | `FE/components/LogTable.tsx:482,651`; Go tarafı hazır: `internal/stackparse/appframes.go:212`, `internal/stackparse/fromlog.go:82` | Rozet, kaynak tükenmesi, çerçeve ayrımı, hızlı süzgeç yok. | 3 |
| 14 | Hız hissi | kısmen | `FE/lib/queries/logs.ts:32`, `FE/components/LogsHistogram.tsx:177`, `internal/api/api_logs.go:289` | Ayrı istekler ve 15–60 sn cache var. Histogram ve alan listesi iptal edilmiyor. | 1 |
| 15 | Klavye | kısmen | `FE/lib/useTableNav.ts:158-211`, `FE/components/GlobalShortcuts.tsx:93` | `/`, `j/k`, `Enter` var. `f`, `e`, `t` yok. | 2 |

**Yan bulgular** (okurken görüldü; bu audit düzeltmedi, hepsi koddan çıkarım):
- `hasTraceUnapplied` ve `unappliedFilters` yanıta yazılmıyor (`internal/api/api_logs.go:529-551`); arayüz okuyor (`FE/pages/logs/LogsNotices.tsx:37,44`). Canlı yanıtla teyit edilmedi.
- ES sorgu hatası 400 yerine 500 döner (`internal/api/api_logs.go:518` → `internal/api/cache.go:224`). `ErrBadQuery` yalnız `mcptools`'ta tüketiliyor (`internal/logstore/logstore.go:743`).
- ES'te `limit > 1000` 1000'e kırpılmaz, 100'e düşer (`es.go:1369`).
- Küçük harf `and/or`: CH yolunda operatör (`internal/logql/logql.go:267`), ES yolunda ham dize. İki backend farklı sonuç verebilir.
- `internal/copilot/prompts.go:2761` "telemetri collector'da maskelenir" diyor; loglar Collector'dan geçmiyor.
- CLAUDE.md'deki `fnvDigest` / `sortedSlice` adları kodda yok. Gerçek adlar `fnvStr` (`internal/api/entity_keys.go:14`) ve `excludeKeyDigest` (`internal/api/topology.go:371`).

## 3. ES sorgu katmanı

### 3.1 Bugün
| Konu | Durum | Kanıt |
|---|---|---|
| Serbest metin | `query_string`, `default_operator: AND`, `lenient`, baştan joker kapalı; `case_insensitive` bilinçli yok | `es.go:2919-2938` |
| Kısayol | 17 sabit anahtar (level, service, trace…) çok alanlı OR'a açılır; diğerleri ham Lucene | `es.go:2973-3032` |
| Süzgeçler | Hepsi skorsuz `bool.filter`; tam eşleşme `.keyword` + çıplak alan | `es.go:2582-2614` |
| Sayfalama | PIT + `search_after`, sıra `[zaman, _shard_doc]`; PIT 401/403'te `from` + `_doc` | `es.go:1484-1532,1588` |
| Sınırlar | `track_total_hits: 10000`, soft timeout 10 sn, API bütçesi 30 sn | `es.go:1512,185`, `internal/api/api_logs.go:42` |
| `_source` | Tam doküman; daraltma yalnız desen örnekleminde | `internal/logstore/es_terms_field.go:177` |
| Histogram | 5 seviye `filters` + `date_histogram`, `min_doc_count: 1`, ≤5000 kova | `es.go:1989-2044`, `internal/api/api_logs.go:261` |
| Alan istatistiği | `terms` size 5/20, `shard_size` ×10, örnekleme yok | `es.go:1101-1120` |
| Otomatik tamamlama | `_terms_enum` + `index_filter`; alan listesi mapping'den, 60 sn | `es.go:1031-1090`, `internal/api/api_logs.go:563` |
| Yok | `random_sampler`, `_async_search`, `collapse`, `highlight`, `runtime_mappings` | grep boş |
| İndeks | Varsayılan `app-*`; servis başına şablon `app-{service}.{namespace}`; `_cat/indices` ile güne daraltma | `es.go:291`, `internal/config/config.go:145`, `internal/logstore/es_indices.go:273` |
| Alan adları | Ayarlanabilir: `@timestamp`, `trace.id`, `span.id`, `service.name`, `message`, `log.level` | `es.go:254-291`, `internal/chstore/logstore_es_settings.go:12` |

### 3.2 Önerilen istek şekilleri
| Özellik | İstek | Maliyet notu | Cache anahtarı girdileri |
|---|---|---|---|
| Satırlar | `_search`, `size = min(örnek, 500)`, sıra `[alanlar…, zaman, _shard_doc]`, PIT + `search_after`, `track_total_hits: false`, `_source.includes` = görünen kolonlar + gövde + trace/span | Toplam histogramdan alınır. Sayım kapalıyken zaman sıralı sorgu erken keser (ES davranışı; ölçülmedi). | Bugünkü `logsSearchKey` + kolon özeti + sıra + örnek + kapsam özeti (`fnvStr(sıralı servisler)`) |
| Tam doküman | `ids` sorgusu, `_index` + `_id`, `size: 1`; yalnız detay açılınca | Liste yükü küçülür; detay başına 1 istek | indeks + id |
| Histogram | Bugünkü gövde (`es.go:1989`) | Değişmez; `request_cache` açık | Bugünkü anahtar + kapsam özeti |
| Alan en sık değerler | `random_sampler{probability: p}` altında `terms`; `p = min(0.5, 200.000 / toplam)`; toplam < 200.000 ise örneklemesiz | ES < 8.2 veya hata: `sampler{shard_size: 20000}`. Yanıt `sampled`, `probability` taşır. | Bugünkü `logsFieldStatsKeyLift` + kapsam özeti + örnekleme kipi |
| Alan listesi | `field_caps`, yalnız kapsamdaki indeksler, `index_filter` = zaman | 403'te konvansiyonel listeye düşer (bugünkü davranış) | indeks özeti + saate yuvarlı pencere; 10 dk |
| Değer tamamlama | `_terms_enum` (bugünkü), ≤20 değer, 180 ms debounce | Terim sözlüğü okur, doküman taramaz | alan + önek + limit + saate yuvarlı pencere + kapsam özeti; 30 sn |
| Desenler | Bugünkü `GroupBySignatureN`: en yeni 500/2000 satır, 10 sn | Agregasyon yok; yalnız sekme açıkken | Bugünkü `logs-patterns:v3` anahtarı |
| İki aralık | İki örneklem + `CountPatterns` ile ilk 20 desene gerçek sayım (tek `_msearch`, `es.go:2286`) | 3 ES isteği; yalnız düğmeyle | iki pencere + süzgeçler + limit |
| "İlk kez görüldü" | Tek `_msearch`, ≤20 kol, `size: 0`, `terminate_after: 1`, pencereden geriye 7 gün | Yalnız "önceki pencerede yok" desenler için | desen özetleri + pencere + kapsam özeti |
| Çevre loglar | Bugünkü iki paralel arama, ±30 dk, n ≤ 200 (`internal/api/logs_context_halves.go:26`) | Değişmez | Bugünkü `logsContextKey` |
| Trace önizleme | ClickHouse, §4 | ES isteği yok | sıralı kimlik özeti + 5 dk'ya yuvarlı pencere |

### 3.3 KQL kararı
| Seçenek | Artı | Eksi |
|---|---|---|
| A. Bugünkü: ham dize → `query_string` | Sıfır iş | KQL yazımları sessizce yanlış sonuç verir. Hata ES'e gidince anlaşılır. CH ile ayrışır. |
| **B. `logql.Parse` → kanonik Lucene → `query_string` (öneri)** | Tek gramer iki backend'de. Hata ES'e gitmeden, konumlu ve Türkçe. Mapping bilgisi gerekmez; analiz ES'te kalır. | Ayrıştırıcının tanımadığı Lucene (regex, fuzzy, boost) için geçiş kipi gerekir. |
| C. `logql` → ES DSL derleyicisi | Tam denetim, süzgeç bağlamı | Her alan için text/keyword bilgisi şart; binlerce indekste `field_caps` pahalı ve prod'da 403 olabilir. Anlam kayması riski yüksek. |

Bugün hazır olan: `Parse` + AST + konumlu `ParseError` (`internal/logql/logql.go:66,82,297`), CH derleyicisi (`internal/logql/compile_ch.go:61`), kanonik Lucene çıktısı (`(*Expr).String()`), ~55 tablo testi (`internal/logql/parse_test.go:17`). ES derleyicisi yok.

B'nin kapsamı:
- Ayrıştırıcıya eklenecek KQL yazımları: `alan: değer` (boşluklu), `alan >= 5` (boşluklu). Küçük harf `and/or/not` zaten operatör (`logql.go:267`).
- ES yolu: parse başarılıysa kanonik Lucene gider. Kısayol açılımı (`es.go:2984`) aynen kalır.
- Parse başarısızsa: dize bugünkü gibi ham gider ve "Lucene" rozeti görünür. ES de reddederse hata döner.
- Hata mesajı: HTTP 400, `{error, pos, detail}`. `error` = `ParseError` metni ("sorgu sözdizimi (konum N): …"). `detail` = ES `reason`, kapalı gösterilir.
- İstemci `kqlLint` (`FE/lib/kqlLint.ts:16`) kalır; yalnız tırnak, parantez, asılı operatör bakar.
- Test: her yeni yazım için tablo satırı (parse + kanonik çıktı); ES'e giden dize için altın dosya.

### 3.4 Mapping, indeks, trace_id / span_id varlığı
- Mapping varsayımı: dinamik text + keyword. Kod iki şekli de dener (`es.go:2582-2590`). Zaman damgası ECS dışı olabilir; `docvalue_fields` epoch_millis okunur (`es.go:1501`).
- trace alanı adayları: `trace.id`, `trace_id`, `traceId`, `TraceId` (`internal/logstore/es_trace_context.go:75`). Alan text ise pivot çalışmaz (`pivotReady` yalnız keyword'de).
- Okuma tarafı biçim toleransı var: tireli UUID, Datadog decimal, 32 haneye tamamlama (`internal/logstore/trace_fallback.go:71,185,207`). Sorguda yalnız küçük harfe çevirme var (`es.go:2657`).
- **Varlık oranı ürün içinde ölçülüyor:** son 24 saat, toplam + servis başına ilk 50 (`internal/logstore/es_trace_context.go:191-221`). Uç `GET /api/logstore/trace-context`; ekran `/admin/elastic` (`FE/pages/AdminElastic.tsx:174`).
- **Ölçülmeyen:** kimlik biçimi (32-hex mi) ve span_id oranı. Prod değeri bu audit'te yok: **ölçülmedi**. Operatör Dev Tools'ta koşar (alan adlarını Settings › Elasticsearch'teki değerlerle değiştirin):

```
POST app-*/_search
{ "size": 0, "track_total_hits": false, "timeout": "10s",
  "query": { "range": { "@timestamp": { "gte": "now-1h" } } },
  "aggs": { "s": { "random_sampler": { "probability": 0.01 }, "aggs": {
    "with_trace": { "filter": { "exists": { "field": "trace.id" } } },
    "with_span":  { "filter": { "exists": { "field": "span.id" } } },
    "hex32":      { "filter": { "regexp": { "trace.id": "[0-9a-f]{32}" } } },
    "upper_or_uuid": { "filter": { "regexp": { "trace.id": ".*[A-F-].*" } } },
    "by_service": { "terms": { "field": "service.name", "size": 50 },
      "aggs": { "with_span": { "filter": { "exists": { "field": "span.id" } } } } } } } } }
```
Okuma: `with_trace / s.doc_count` = trace_id oranı; `with_span / with_trace` = span'e bağlanabilen log oranı; `hex32 / with_trace` < 1 ise biçim normalizasyonu sorgu tarafına da eklenir.

## 4. trace_id deneyimi

| Alt madde | Bugün | Öneri | Sınır |
|---|---|---|---|
| Çip + önizleme | 8 karakter link, kopyala, peek (`FE/components/LogTable.tsx:581-596`) | `POST /api/logs/trace-preview {ids, fromNs, toNs}`. Satırlardan sonra akar. Hover kartı aynı yanıtı kullanır; ek istek yok. | ≤200 kimlik, 3 sn |
| Çip menüsü | 3 ayrı düğme | Filtrele (`traceId=`), trace görünümünde aç (`panel=trace`), kopyala | — |
| Trace'e göre grupla | Yok | İstemcide, yüklenen örnek içinde (`group=trace`). Başlık önizleme yanıtından. | Etiket: "yüklenen N satır içinde" |
| Birleşik görünüm | Trace sayfasında ayrı Logs sekmesi (`FE/pages/trace/TraceLogsPanel.tsx:94`) | `GET /api/traces/{id}/bundle` (`internal/api/trace_bundle.go:50`) + `TraceWaterfall`'a `logRows` prop'u (`FE/components/TraceWaterfall.tsx:201`). span_id'siz loglar ayrı grup. | log ≤500 (`internal/logstore/logstore.go:815`), span ≤50.000 (`internal/chstore/repo.go:5043`) |
| Servisler arası takip | ES, trace_id varken From/To'yu yok sayar (`es.go:1382`) | Kapsam = trace'in servisleri (CH'den) → o servislerin indeksleri. Pencere = trace başı −1 dk … sonu +5 dk. Kapsam dışı satır etiketlenir. | ≤50 servis, 3 sn, `terminate_after` |
| Yapıştırılan trace_id | Çip önerir (`FE/pages/logs/logsQuery.ts:26`) | 32-hex, tireli UUID tanınır; `panel=trace` doğrudan açılır | Davranış değişikliği |
| Örneklenmemiş trace | Düz "Trace not found" (`FE/pages/Trace.tsx:752`); sunucuda ayrı durum yok (`internal/api/trace_routes.go:102-128`) | Aşağıdaki durum tablosu | — |
| Ters yön | Var, ama `q=traceId` gönderir (`FE/components/SpanDetail.tsx:501`) | `traceId` + `spanId` paramları; "bu span'in logları" | — |

**Toplu önizleme okuması (tek sorgu):**
```
SELECT trace_id, <root_service, root_name, başlangıç, bitiş, span sayısı, hata sayısı -Merge sonlandırıcıları>
FROM trace_summary_5m
WHERE time_bucket >= {lo} AND time_bucket <= {hi} AND trace_id IN ({ids})
GROUP BY trace_id LIMIT 200 SETTINGS max_execution_time = 3
```
- `lo/hi` = sayfadaki trace'li logların en erken / en geç zamanı ∓ 15 dk, 5 dk kovaya yuvarlı.
- Neden MV: sıralama anahtarı `(time_bucket, trace_id)` (`internal/chstore/store.go:1842`). Dar pencerede anahtar aralığı okunur. Okuma deseni hazır (`internal/chstore/trace_stub.go:50`).
- Neden ham `spans` değil: `idx_trace bloom_filter(0.01)` (`internal/chstore/store.go:2257`). 100 kimlikte bir bloğun süzgeçten geçme olasılığı 1 − 0,99^100 ≈ %63 (hesap; ölçülmedi).
- Eksik: MV'de servis sayısı yok. İlk sürümde çip "span sayısı" gösterir; servis sayısı ayrı şema dilimi (§8 3.9).
- Tekil trace okuması `GetTrace` kalır (`internal/chstore/repo.go:4990`). Bundle'a `hint=<log zamanı>` eklenir; `TraceWindow` 24 sa → 7 g → 90 g taramasından önce ±1 sa denenir. İpucu ıskalarsa eski yol çalışır; sonuç ipucundan bağımsızdır.

**"Örneklenmiş" tespiti:**
| Durum | Koşul | Çip metni |
|---|---|---|
| Bulundu | MV satırı var | ✓ / ✕ + süre |
| Henüz gelmedi | Satır yok, log 2 dk'dan yeni | "trace henüz yazılmadı" |
| Örneklenmiş | Satır yok, log 2 dk'dan eski, sorgu başarılı | "trace örneklenmiş (sampling Collector'da)" |
| Tanınmadı | Kimlik hex/UUID değil | Düz metin, çip yok |
| Okunamadı | CH hata veya timeout | "önizleme alınamadı"; asla "örneklenmiş" denmez |

Örneklenmiş trace'te kapsam genişlemez (servis listesi bilinmez). "Tüm servislerde ara (±5 dk)" ayrı düğmedir; tek istek, 3 sn. Tempo yapılandırılmışsa "Tempo'da ara" yalnız tıklayınca çalışır (`internal/api/trace_resolve.go:66-80`).

## 5. Performans

| Hedef | Uç | Ölçüm yöntemi |
|---|---|---|
| Son 15 dk, tek servis, ilk satırlar < 1 sn | `POST /api/logs/search` | İstemci: `performance.mark('logs:submit')` → ilk satır boyanınca `performance.measure`. Sunucu: `Server-Timing: es;dur=` + `X-Cache`. |
| Son 24 sa, ilk satırlar < 3 sn | aynı | Aynı; soğuk yol `?refresh=1` (BYPASS) ile, 5 koşu medyanı |
| trace_id ile birleşik görünüm < 2 sn | `GET /api/traces/{id}/bundle?hint=` | `Server-Timing: ch;dur=, es;dur=`; CH tarafı `system.query_log` medyanı |

- `Server-Timing` bugün hiçbir uçta yok; `X-Cache` var (`internal/api/cache.go:320`). İlk dilimde eklenir.
- Sürekli izleme: `http_server_request_duration_seconds`, rota etiketli (`internal/api/req_tag.go:48`).
- Kapı: `scripts/perf/budget.json`'da log noktası yok (`:10-40`). Üç nokta eklenir. perfcheck lokal CH log backend'inde koşar; ES süresi prod'da ölçülür (§10-P).
- CH sorguları: `log_comment = 'route:<uç>'` ile `system.query_log`, ≥3 koşu medyanı (`/perf-triage` kuralı).

| # | Risk | Bugün | Koruma |
|---|---|---|---|
| 1 | Alan istatistiği tam pencere tarar | Örnekleme yok (`es.go:1101`) | `random_sampler`; yalnız alan açılınca; 60 sn cache |
| 2 | Kapsamsız `app-*` sorgusu | Servissiz sorgu `app-*` + gün daraltma (`internal/logstore/es_indices.go:273`) | Varsayılan kapsam kullanıcının servisleri (≤100, `internal/mcptools/team_ownership.go:86`). Kapsam yoksa servis seçilmeden sorgu atılmaz. |
| 3 | Örnek 500 × tam `_source` | Tam doküman döner | Görünen kolonlar; detayda tek doküman. Önce prod ölçümü (§10-P). |
| 4 | Çoklu alan sıralama | Yalnız zaman | ≤3 anahtar, yalnız doc_values alanı, soft timeout + `partial` rozeti |
| 5 | Uzun aralık histogram | ≤5000 kova, 10 sn | Değişmez; hedef tutmazsa async search dilimi |
| 6 | Otomatik tamamlama yükü | `_terms_enum`, 30 sn cache | 180 ms debounce, ≤20 değer; alan listesi istemcide süzülür |
| 7 | `field_caps` binlerce indekste | 10 dk cache (`internal/logstore/es_terms_field.go:139`) | Yalnız kapsam indeksleri + `index_filter` |
| 8 | Trace önizleme | — | Tek sorgu, ≤200 kimlik, 3 sn, dar pencere |
| 9 | Desen çıkarımı | 500/2000 örnek, 10 sn (`internal/logstore/patterns.go:34`) | Değişmez; yalnız sekme açıkken; `/api/logs/patterns` < 2 sn bütçesi |
| 10 | İptal edilmeyen istekler | Histogram ve alan listesi sinyalsiz (`FE/components/LogsHistogram.tsx:177`) | Hepsi `signal` → `r.Context()`; bağlantı kapanınca ES aramayı keser (ES davranışı) |

Akış sırası: satırlar ∥ histogram ∥ işaretler → trace önizleme → alan istatistiği (yalnız açılınca). Otomatik yenileme yok (bugünkü kural). Cache TTL'leri değişmez: liste 15 sn, histogram 30 sn, istatistik 60 sn.

## 6. Ekran yerleşimi (mockup ile 1:1)

| Bölge (mockup) | Dolduran | Durum |
|---|---|---|
| Bağlam çubuğu: Env → kapsam → zaman seçici en sağda | `ContextBar` + `coremetry/TimeRangePicker` | Mevcut |
| Servis seçici + sağlık | `ServicePicker` (`preferred`, `optionMeta`) | Mevcut; veri yeni uçtan |
| Sorgu çubuğu + tamamlama + geçmiş | `coremetry/FilterBar` + shadcn `command` popover | FilterBar gövdesi genişler (bugün 64 satır, öneri prop'ları kullanılmıyor) |
| Filtre hapları (sabit / ters / kapalı) | `FilterBar` çip satırı + düzenleme popover'ı | Yeni; plan "Logs diliminde gelir" diyor (`docs/frontend/shadcn-phase1-plan.md:360`) |
| Görünüm anahtarı `?panel=` | `coremetry/SegmentedControl` | Mevcut; `trace` değeri eklenir |
| Histogram + işaretler + sürükleme | `CorePanel` `viz="stacked-bars"`, `regions`, `onZoom`, `onRegionClick` (`FE/components/chart/CorePanel.tsx:183,205,316`) | Mevcut; Logs'a bağlanır |
| Alan kenar çubuğu | Yeni `coremetry/logs/FieldSidebar` | Yeni (bugünkü `LogFieldsPanel` yerine) |
| Doküman tablosu | `coremetry/DataTable` + `useDataTable`; yeni `coremetry/logs/LogDocTable` | Yeni; DataTable'a satır yüksekliği ve çoklu sıralama eklenir |
| Görünüm seçenekleri | shadcn `popover` + `toggle-group` | Yeni `coremetry/logs/DisplayOptions` |
| Trace çipi + önizleme | Yeni `coremetry/TraceChip`; shadcn `hover-card` üretilir (radix-ui paketinde var) | Yeni |
| Stack trace bloğu, exception rozeti | Yeni `coremetry/logs/StackBlock`; `coremetry/StatusBadge` | Yeni |
| Satır detayı | `coremetry/DetailSheet` + `useSheetParam('doc')` + `KeyValue` | Mevcut bileşenler |
| Trace + log | `TraceWaterfall` + `logRows` | Mevcut bileşen, yeni prop |
| Desenler + karşılaştırma | `coremetry/DataTable`; yeni `coremetry/logs/PatternCompare` | Kısmen yeni |
| Klavye ipuçları | Tablo kartının alt satırı; `useShortcuts` (`FE/lib/keyboard.ts:297`) | Yüzen şerit değil |

- Sayfa düzeyinde yapışkan şerit yok. Açılır katmanlar yalnız kendi kapları içinde.
- Tablo kuralı: 100 satır/sayfa + `content-visibility`; sanal liste gerekmez. Böylece "sanal + satır içi detay" boşluğuna girilmez (`docs/frontend/shadcn-phase1-plan.md:366`).
- Tablo standardı "hücre tek satır" der (`docs/DECISIONS.md:727`). Satır yüksekliği 2/3/otomatik için Logs istisnası gerekir (§9-5).

## 7. CoSRE ve maskeleme (Faz 6 tasarımı)

**Bugün:** LLM'e giden metne maskeleme uygulanmıyor; bu bilinçli (`internal/copilot/prompts.go:1953-1971`). Log araçları ham satır gönderiyor: `search_logs` ≤200 satır (`internal/mcptools/logs_tools.go:419`), `get_logs_for_trace` ≤500 (`internal/mcptools/pivots.go:95`). Genel ES aracı yok (`internal/mcptools/toolset.go:100`).

**Maskeleme tasarımı**
- Yeni saf paket `internal/llmmask`: `Mask(text) (masked, stats)`. Tablo testi + altın dosya.
- Türler: TCKN (11 hane + sağlama), kart (13–19 hane + Luhn), IBAN (mod-97), telefon, e-posta, token/şifre (JWT, Bearer, `password=`). Token kalıpları için emsal: `internal/templater/masker.go:29-93`.
- Çıktı: tipli yer tutucu, istek içinde tutarlı numara (`<TCKN#1>`). Model "aynı müşteri 3 satırda" ilişkisini kurabilir.
- Uygulama noktası: log metninin prompt'a girdiği yer. Önerilen kapsam: yeni Log Search yüzeyleri + mevcut log araçları ve `explain-log*` uçları. Trace, SQL, Oracle yükü bugünkü gibi kalır (operatör kararı 2026-09-25).
- Değişmeyen: ES'teki veri, arayüz, CSV/NDJSON dışa aktarım. `ai_calls` kaydı modele gideni, yani maskeli metni saklar.

| Özellik | Bugün | Öneri |
|---|---|---|
| "Bu logları açıkla" | `explain-log-patterns`, `explain-log-spike` var (`internal/api/ai_routes.go:132-134`) | Aynı yüzey genişler: desen özeti + seçili satırlar (≤12). Cevap şeması: ne oluyor, baskın hata, başlangıç, yeni desenler, ilgili trace linkleri. |
| Tek satır açıklama | `explain-log` var | Exception özeti `stackparse` ile deterministik; LLM sade Türkçe anlatır. Runbook: `search_knowledge` sözcük araması; RAG'e dayanmaz. |
| Trace hikâyesi | "Explain this trace" var; logları okur (`internal/api/explain_trace_input.go:329`) | Birleşik görünüme düğme; loglar maskeli. |
| Doğal dilden sorgu | Yok | Yeni `POST /api/copilot/logs-nl-query`. Girdi: cümle + alan adları. Çıktı: `{kql, filters, range}`. Sunucu `logql.Parse` ile doğrular. Arayüz sorguyu gösterir; çalıştırmaz. |
| Servis hızlı özeti | Briefing yalnız takım ERROR sayısı okur (`internal/api/copilot_team_logs.go:207`) | Ortak deterministik katman: desen özeti + önceki pencere farkı. Servis özeti ve briefing aynı fonksiyonu çağırır. LLM yalnız anlatır. |

Doğal dil sorgusunda kullanıcı kimlik yazabilir. Cümle maskelenir; dönen KQL'de yer tutucu sunucuda geri doldurulur. Geri doldurma yalnız bu uçta yapılır.

## 8. Faz planı

Ortak kabul (her dilim): `scripts/gate.sh all` + `make audit` + `golangci-lint` yeşil; `migration-log.md` satırı (`design:` dahil); geçirilen dosyada inline style 0 ve `ui/` import 0; tek-commit revert; mockup ile 1:1. Tür: **T** = tasarım, **M** = mekanik → `implementer`. **OK** = mevcut davranış değişir, operatör onayı şart.

**Faz 1 — sorgu çubuğu, zaman/histogram, doküman tablosu, URL, akış**
| Dilim | Tür | İçerik | Kabul | OK |
|---|---|---|---|---|
| 1.1 | M | Sözleşme çivisi: `legacyUrls.test.ts`, `Logs.firstLoad.test.tsx` genişler; `Server-Timing`; 3 perfcheck noktası | Davranış aynı; noktalar ölçüm üretir | — |
| 1.2 | M | ES sorgu hatası 400 + `{error, pos, detail}`; `unappliedFilters` yanıta | Bozuk sorgu 400; uyarı rozetleri görünür | — |
| 1.3 | T | `logql` KQL yazımları + ES yolunda kanonik Lucene + "Lucene" geçiş kipi | ≥40 yeni tablo satırı; altın dosya | OK |
| 1.4 | M | Sorgu geçmişi (yerel, son 20; emsal Explore `RecentQueries.tsx`) | Geçmiş tamamlama listesinde | — |
| 1.5 | T | Sorgu çubuğu `FilterBar` + `command` popover; konumlu hata satırı | Alan/değer/geçmiş tek listede; a11y testi | — |
| 1.6 | M | Histogram `CorePanel`'e; tüm isteklere iptal sinyali | Sürükleme ve çift tık aynı çalışır | — |
| 1.7 | T | Doküman tablosu: satır yüksekliği (gövde/başlık), yoğunluk, sayfa başına 100 | Dört yükseklik + üç yoğunluk; kolon genişliği korunur | OK |
| 1.8 | T | Örnek boyutu: `smp` (10..üst sınır), `system_settings['logs_search'].maxSample`, Settings alanı + audit; dar `_source`; sayım kapalı; "daha fazla yükle" | Varsayılan 500; üst sınır Settings'ten; admin yazımı audit'li | OK |
| 1.9 | M | Görünüm tercihleri: `GET/PUT /api/preferences/{key}/display` (ayrı dosya; `saved_views`, `page='table:<key>'`); URL paramları `rh, hh, den, smp, pp` | Tercih yeniden girişte korunur; link aynı görünümü açar | — |
| 1.10 | T | Çoklu alan sıralama (≤3) + sürükleyerek kolon sırası | Sıra URL'de; yalnız doc_values alanları | — |
| 1.11 | M | Akış göstergesi: satır / histogram / istatistik ayrı iskelet + süre etiketi | İlk satırlar histogramı beklemez | — |
Operatör girdisi: 1.8 öncesi `_source` ölçümü (§10-P).

**Faz 2 — alan kenar çubuğu, filtre hapları, servis odaklı başlangıç, klavye**
| Dilim | Tür | İçerik | Kabul | OK |
|---|---|---|---|---|
| 2.1 | M | Alan istatistiğinde `random_sampler` + "~" etiketi | Yanıt `sampled` taşır; ES < 8.2'de geri düşer | — |
| 2.2 | T | Kenar çubuğu shadcn'e; "İş alanları" bölümü (`logs_search.businessFields`: etiket → ES alan adayları) | CHANNEL_CODE, FUNCTION_CODE, operasyon en üstte | — |
| 2.3 | T | Haplar: sabitle + `FilterBar` çip düzenleyici | Sabit hap servis değişince kalır; URL'de | — |
| 2.4 | T | Kapsam: `GET /api/logs/scope` (ayrı dosya; `resolveUserScope`, `internal/api/chat_user_scope.go:125`) + `scope=mine` + seçicide sağlık | Kapsam ≤100 servis; takımsız kullanıcıda servis seçimi istenir | OK |
| 2.5 | M | Akıllı varsayılan kolonlar: yüklenen örnekte doluluk ≥ %80 olan iş alanları | Yalnız kayıtlı kolon tercihi yoksa uygulanır | — |
| 2.6 | M | Klavye `f`, `e`, `t`; yardım ekranı kaydı (`FE/lib/shortcutRegistry.test.ts`) | Kısayollar yalnız Logs kapsamında | — |
Operatör girdisi: iş alanlarının ES'teki adları (§10-F).

**Faz 3 — satır detayı, çevre loglar, trace_id deneyimi, exception farkındalığı**
| Dilim | Tür | İçerik | Kabul | OK |
|---|---|---|---|---|
| 3.1 | T | Satır detayı `DetailSheet`: düz cümle, Tablo/JSON, tam doküman isteği | `?doc=` linki aynı çalışır | OK |
| 3.2 | M | Çevre loglar sheet içinde (uç değişmez) | ±50 → ±200; pod/servis kapsamı | — |
| 3.3 | T | Exception: sunucuda `stackparse.FromLog` ile tür + sınıf (kaynak tükenmesi) + uygulama çerçeveleri; satır içi katlı stack; "yalnız kritik hatalar" (emsal kurallar `internal/anomaly/log_patterns.go:144-187`) | Java ve .NET fikstürleri; rozet + metin birlikte | — |
| 3.4 | T | `POST /api/logs/trace-preview` (ayrı dosya) + `TraceChip` + hover kartı + menü | Sayfa başına tek CH sorgusu; 5 durum | — |
| 3.5 | M | "Trace'e göre grupla" (istemci) | Başlıkta süre + hata; "yüklenen N satır" etiketi | — |
| 3.6 | T | Birleşik görünüm: `logRows` prop'u + bundle `hint` + `panel=trace` | < 2 sn hedefi ölçülür | — |
| 3.7 | T | Kapsam genişletme + "kapsam dışı" etiketi + örneklenmiş durumu | ≤50 servis; örneklenmişte genişleme yok | — |
| 3.8 | M | Yapıştırılan trace_id → birleşik görünüm; span → logs `spanId` | Geri tuşu önceki aramaya döner | OK |
| 3.9 | T (architect) | `trace_summary_5m`'ye servis sayısı state'i (`/clickhouse-schema`, dağıtık-güvenli) | Çip "N servis" gösterir | OK |

**Faz 4 — desenler, iki aralık, bağlamlı histogram**
| Dilim | Tür | İçerik | Kabul | OK |
|---|---|---|---|---|
| 4.1 | M | Histogram işaretleri: `/api/annotations` → `regions`; tıkla = daral | Rollout, anomali, Problem ayrı simge + metin | — |
| 4.2 | T | Desenler: "ilk kez görüldü", oran ifadesi kuralı, eğilim çizgisi (ilk 20, tek `_msearch`) | "Önceki pencerede yok" ile "ilk kez" ayrı etiket | — |
| 4.3 | T (architect) | Ortak kimlik: `SignatureHash` tek kimlik; Log-ML aile ifadesi imzadan aynı fonksiyonla türer | Aynı satır iki yüzeyde aynı desene düşer (test) | — |
| 4.4 | T | İki aralık: `GET /api/logs/patterns/compare` (ayrı dosya) + yeni / artan / kaybolan | "Yeni" ve "kaybolan" gerçek sayımla doğrulanır | — |
| 4.5 | T | Async search (koşullu: 24 sa ölçümü hedefi tutmazsa) | Histogram ara sonucu gösterir | — |
Not 4.3: bugün üç ayrı kimlik var. Desenler xxhash imza (`internal/logstore/signature.go:385`), Şablonlar Drain sha1 (`internal/templater/drain.go:335`, kararsız), Log-ML sabit ifade (`internal/ml/log_templates.go:37-72`). Taban tanımı `JoinPatternBaseline` (`internal/logstore/patterns.go:78-101`); oran dili `docs/anomaly/learning-loop-audit.md:336`.

**Faz 5 — kaydedilmiş aramalar**
| Dilim | Tür | İçerik | Kabul | OK |
|---|---|---|---|---|
| 5.1 | M | Yeni paramlar (`scope, group, sort, rh, hh, den, smp, pp, pins`) altın listeye; `SavedViewsBar` aynen | Kayıtlı arama görünümü birebir açar; 8 KiB sınırı testte | — |
| 5.2 | M | Paylaş linki: göreli pencere mutlak zamana sabitlenir (mevcut) + görünüm ayarları | Link başka kullanıcıda aynı tabloyu açar | — |
Yeni tablo yok: `saved_views(page='logs')` (`internal/chstore/saved_view.go:18`).

**Faz 6 — maskeleme + CoSRE**
| Dilim | Tür | İçerik | Kabul | OK |
|---|---|---|---|---|
| 6.1 | T | `internal/llmmask` + tablo testleri | Her tür için pozitif / negatif örnek; sağlama testleri | OK (§9-1) |
| 6.2 | M | Maskeyi log taşıyan yüzeylere bağla; CLAUDE.md, memory, `prompts.go` metinleri | Prompt altın testlerinde ham kimlik yok | OK |
| 6.3 | T | "Bu logları açıkla" (desen özeti + seçili satırlar) | Cevap 5 başlıklı; linkler süzgeçli | — |
| 6.4 | T | Tek satır açıklama + runbook | RAG kapalıyken çalışır | — |
| 6.5 | M | Trace hikâyesi düğmesi (birleşik görünüm) | Mevcut yüzey; loglar maskeli | — |
| 6.6 | T | Doğal dilden sorgu | Sorgu gösterilir; kullanıcı "Ara"ya basmadan çalışmaz | — |
| 6.7 | T | Servis hızlı özeti + briefing ile ortak katman | Aynı fonksiyon iki yerde (test) | — |

## 9. Açık sorular

| # | Soru | Önerilen varsayılan |
|---|---|---|
| 1 | **Maskeleme kapsamı.** CLAUDE.md "No PII redaction features" diyor; bu görev LLM öncesi maskelemeyi zorunlu kılıyor. Kapsam ne? | Yalnız LLM'e giden **log** metni. Saklanan, gösterilen, dışa aktarılan veri tam kalır. Onayla birlikte değişecek metinler: CLAUDE.md satırı ("stored / displayed / exported veri maskelenmez; LLM'e giden log metni Go'da maskelenir"), `feedback-no-redaction.md`, `internal/copilot/prompts.go:1953-1971` ve `:2761`. |
| 2 | Mevcut log araçları (`search_logs` vb.) da maskelensin mi, yoksa yalnız yeni yüzeyler mi? | Hepsi. Aynı veri iki yoldan farklı gitmesin. |
| 3 | Yerinde mi, yan yana mı? | Yerinde. Dilim dilim, tek-commit revert. |
| 4 | Varsayılan kapsam `scope=mine` olsun mu? Bugünkü otomatik "Takımım" (ES takım alanı) yerine geçer. İki mekanizma farklı sonuç verebilir. | Evet. `team=` elle süzgeç olarak kalır. Takımsız kullanıcıda servis seçilmeden sorgu atılmaz. |
| 5 | Tablo: çok satırlı hücre için Logs istisnası ve `coremetry/DataTable`'a ek prop (API dondurulmuş). | İstisna DECISIONS'a yazılır. TanStack beklenmez. |
| 6 | Örnek boyutu varsayılanı 500 (bugün 100/istek). | Dar `_source` ölçümü olumluysa 500; değilse 200. |
| 7 | KQL: küçük harf `and/or` ES'te de operatör olsun mu? Tanınmayan Lucene "geçiş kipi"nde çalışsın mı? | İkisi de evet. |
| 8 | Yapıştırılan trace_id birleşik görünümü otomatik açsın mı (bugün çip önerir)? | Evet; Geri tuşu döner. |
| 9 | Servis sayısı için `trace_summary_5m` şema değişikliği. | Faz 3 sonunda ayrı onay. O zamana kadar çip span sayısı gösterir. |
| 10 | "İlk kez görüldü" geriye bakışı. | 7 gün, ES varlık yoklaması. Log-ML açılınca `ml_series` kaynağına geçer. |
| 11 | Çoklu sıralama sunucuda mı? | Evet, ≤3 anahtar. İstemci sıralaması yok (tablo kuralı). |
| 12 | Sabit hapların kalıcılığı: URL mi, kullanıcı tercihi mi? | URL (`pins`); paylaşılan link aynı görünür. |
| 13 | Satır detayı satır içi genişleme yerine sheet olsun mu? | Sheet. Stack trace satır içinde katlı kalır. |

## 10. Doğrulanmadı / ölçülmedi

| Kod | Konu | Operatörün koşacağı |
|---|---|---|
| T | trace_id biçimi, span_id oranı | §3.4'teki Dev Tools isteği. trace_id oranı için ayrıca `/admin/elastic` kartı. |
| V | ES sürümü (`random_sampler` ≥ 8.2) | `GET /` → `version.number` |
| Y | Coremetry API anahtarının yetkileri | Anahtarla: `GET app-*/_field_caps?fields=trace.id`, `POST app-*/_pit?keep_alive=1m`, `POST app-*/_async_search?wait_for_completion_timeout=1s {"size":0}`. 403 dönenler not edilir. |
| F | İş alanlarının ES adları | `GET app-*/_field_caps?fields=*CHANNEL_CODE*,*channel_code*,*FUNCTION_CODE*,*function_code*,*operation*&filter_path=fields` |
| K | Küçük harf `and` ES'te terim mi | `POST app-<servis>*/_validate/query?explain=true {"query":{"query_string":{"query":"level:error and level:warn","default_field":"message","default_operator":"AND"}}}` |
| P | Dar `_source` kazancı ve hedef süreler | Giriş yapılmış çerez kavanozuyla, 5 koşu: `curl -s -o /dev/null -b jar -w '%{time_starttransfer} %{size_download}\n' '<host>/api/logs?service=<servis>&from=<ns>&to=<ns>&limit=500&refresh=1'`. Aynısı 15 dk ve 24 sa pencereyle. |
| B | Toplu önizleme sorgusunun maliyeti; bloom hesabı | Dilim 3.4 sonrası: `SELECT query_duration_ms, read_rows, result_rows FROM clusterAllReplicas('<küme>', system.query_log) WHERE type='QueryFinish' AND is_initial_query AND log_comment LIKE 'route:POST /api/logs/trace-preview%' ORDER BY event_time DESC LIMIT 20` |
| U | `unappliedFilters` yanıtta yok | CH backend'li ortamda `team=` ile `/api/logs` yanıtına bakılır |

Koddan çıkarılamayan diğerleri:
- Prod'da `field_caps`, PIT, `_mapping` 403 bilgisi memory'den geliyor (125–154 gün eski).
- TanStack kararının sonucu kayıtlı değil. Bağımlılık yok (`frontend/package.json`); karar Traces dilimine bırakılmış (`docs/frontend/shadcn-audit.md:398-401`).
- "Bağlantı kapanınca ES aramayı iptal eder", "sayım kapalıyken erken keser", `random_sampler` lisansı: ES davranışı; bu repoda ölçüm yok.
- `/api/preferences` gövdesi yalnız kolon modelini kabul ediyor (`internal/api/preferences_routes.go:67-105`); bu yüzden 1.9 ayrı uç öneriyor.
- `docs/frontend/mockups/` dizini bu audit'ten önce yoktu; eski mockup'lar `scratchpad/dt-parity/` altında.
- Mockup, çipte "5 servis" gösterir; bu, dilim 3.9 sonrası hedef durumdur.
