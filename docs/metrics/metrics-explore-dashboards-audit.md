# Metrics Explorer, Explore ve Dashboards: Faz 0 audit

Tarih: 2026-11-05 · Durum: ONAY BEKLİYOR (uygulama yok, kod değişmedi) · Mockup: `docs/frontend/mockups/metrics-explore.html`

Operatör isteği: "Metrics ve explorer sayfası da VM'e göre değişse daha iyi olur. Daha modern sade tasarım olabilir. Dashboards sekmesi de çok eski kaldı, revize edebiliriz."

Kısaltmalar: `fe/` = `frontend/src/` · `api/` = `internal/api/` · `vm/` = `internal/vmetrics/` · `ch/` = `internal/chstore/` · MQE = `fe/components/viz/MetricQueryEditor.tsx`.

Kanıt kuralı: mevcut durum iddiaları dosya:satır taşır (2026-11-05 çalışma ağacı; başka ajanlar commit attığı için satırlar birkaç satır kayabilir). VM API davranışı VM belgelerine dayanır, prod sürümünde denenmedi; §9'da komutlarıyla listelidir.

## 1. Özet ve önerilen karar

**Karar önerisi: iki sayfa kalır, tek sorgu bileşeni olur.**

| # | Öneri | Kanıt |
|---|---|---|
| 1 | `/metrics` = **Metrics Explorer**: yalnız metrik, VM-first. İki sekme: **Sorgu** (Builder ⇄ MetricsQL) ve **Keşif** (ad, etiket, kardinalite). | Varsayılan görünüm zaten editör (`fe/pages/metricsView.ts:27`); katalog `?editor=0` arkasında. |
| 2 | `/explore` = **span analitiği** olarak kalır. Metrik satırı kalır ama ortak bileşeni kullanır. Eski `source=metrics\|logs` panelleri kalkar. | Explore bugün span + metrik + 2 eski panel; üç üst üste anahtar (`fe/pages/Explore.tsx:809`, `:827`, `fe/pages/explore/model.ts:23`). |
| 3 | Üç ayrı sorgu editörü **tek** `components/coremetry/metrics/QueryRow` olur (Metrics, Explore metrik satırı, panel editörü). | MQE `:520`, `fe/pages/explore/QueryRow.tsx`, `fe/components/dashboard/PanelEditor.tsx:244`: üç model, üç URL biçimi. |
| 4 | Metrik yanıtlarına `meta` zarfı: kaynak, çalışan MetricsQL, süre, seri toplam/gösterilen, kırpıldı, kısmi, adım. | VM'in 1000-seri kesmesi yalnız MCP'ye görünür (`vm/investigate.go:30-47`); HTTP uçları düşürür. |
| 5 | Dashboards yerinde evrilir: etiket süzgeci + şablon galerisi, 12 kolon akış ızgarası, tek ⋯ menü, bölünmüş panel editörü + canlı önizleme, bundle genişlemesi + LazyMount. | Editör ham `position:fixed` kutu, önizleme yok (`PanelEditor.tsx:105-115`); bundle yalnız 2 tip (`fe/pages/Dashboard.tsx:200-205`). |
| 6 | **Yerinde evrim, yan rota yok.** Her dilim tek commit ile geri alınır; eski dosya operatör prod'da "OK" dedikten sonra silinir. | Bugünkü sorun zaten paralel editörler; dördüncü paralel yüzey borcu büyütür. |

**Neden tek "Explorer" değil?** `Explore.tsx` 1441 satır. Span jestleri (örnek trace, BubbleUp, hata diff) metrik modunda zaten kapalı (v0.9.1202). MetricsQL, kardinalite ve etiket keşfi span'e uygulanmaz. Ortak olan sayfa değil, sorgu satırıdır.

**En büyük üç risk**
1. **Metadata.** VM'de tür ve birim adın son ekinden tahmin ediliyor, açıklama yok (`vm/describe.go:64`). "Sayaç → rate" varsayılanı yanlış tahminde yanlış fonksiyon seçer. Önlem: üç basamaklı kaynak, "tahmini" işareti, kullanıcı seçimi her zaman üstün.
2. **Keşif uçlarının maliyeti.** `/api/v1/status/tsdb` ve `/api/v1/series` bugün hiç çağrılmıyor. Korumasız açılırsa VM'i yorar. Önlem: §3.2 tavanları.
3. **Davranış değişiklikleri.** Katalog tıklama hedefi, sunucu tarafı top-N, sayaçta varsayılan rate, ızgara 4→12, eski URL'ler (`?mq=`, `?promql=`). Her biri ayrı "OK" ister (§7).

**Önermediklerim:** tek birleşik Explorer · yeni grafik kütüphanesi, CorePanel'e ikinci eksen, serbest x/y pano yerleşimi · Thanos konsolunu büyütmek · sessiz CH fallback · exemplar'ı VM'e taşımak · builder'da `without` / alt sorgu / `WITH` (kod kipinde kalır) · vmui gömmek · pano sahibi kolonu ve kalıcı sürüm geçmişi (şema ister) · Monaco/CodeMirror (ilk yükleme bütçesi < 347 KB gz).

## 2. Mevcut durum

### 2.1 Yüzey tablosu

| Yüzey | Ne var (dosya:satır) | VM uyumu / CH kalıntısı | Görsel / stack | Sorun |
|---|---|---|---|---|
| **Metrics kabuk + katalog** | `fe/pages/Metrics.tsx` 492 satır. Varsayılan editör (`metricsView.ts:27`). Katalog DataTable `metric-catalog` (`:199`), kolonlar `:53-65`. Satır tıklaması Explore'a gider (`:274`). | VM rozeti "birim/tür/son-görülme boş" diyor (`:301`). Yorum hâlâ `metric_catalog` (`:161`). `?metricsrc` elle yazılır (`fe/lib/metricSource.ts:65`). | `components/ui/*`; shadcn 0; 12 inline style. | Başlık yorumu "katalog ana yüzey" (`:33-41`), davranış tersi. Katalog 200→1000 tavan (`:480`). |
| **Metrics editörü (MQE)** | 1342 satır. Builder / Code (DSL) / PromQL (`:1090`). Model `:120-144`, `?mq=` JSON (`:162`). Formül istemcide, yalnız ilk seri (`:884-889`). Top-N istemcide (`:929-934`). Adım Auto/15s/1m/5m (`:68`). Dashboard'a ekle (`:1042`, `:1310`). | PromQL sekmesinde kaynak seçimi (`:1151`). Builder seçicileri kaynağı göndermez (`GroupedMetricPicker.tsx:47`, MQE `:315`). 13 sabit noktalı OTel etiketi (`:75`). Süzgeç yalnız `=` (`:322`). Varsayılan agg `avg` (`:154`). Metin "PromQL over the OTel metric store" (`:1214`). | `ui/*` + ham `<select>/<input>/<textarea>`; `.mqe-*` 62 CSS kuralı; 15 inline style. | `note` ve `rowsCapped` istemcide düşüyor (`fe/lib/api.ts:3095`). Süre yalnız Thanos'ta (`:1217-1224`). Türkçe/İngilizce karışık. |
| **Explore** | `Explore.tsx` 1441 + `explore/` 31 dosya (~5,2k satır). Model en çok 4 sorgu (`model.ts:82-91`, `:127`). 9 görsel (`model.ts:28`). Metrik satırı `/api/metrics/query` (`useExploreQueries.ts:173`), ısı haritası `/api/metrics/histogram` (`Explore.tsx:553`). Eski paneller `:1071`, `:1078`. Geçmiş localStorage, 4 kayıt (`useQueryHistory.ts:21`). | FE'de backend dalı yok. `note` gösteriliyor (`PanelStack.tsx:231`). Etiket önerisi metrikten (`SplitByPicker.tsx:21`). MetricsQL kipi yok. | `ui/*`; shadcn 0; 146 inline style; ham `<select>` (`Explore.tsx:838`, `:844`, `:1061`). | İki veri katmanı: react-query + elle effect (`:522-645`). URL mount'ta bir kez okunur (`:99-102`). Elle yenileme (`fe/lib/autoRefresh.ts:112-125`), Metrics otomatik. |
| **Dashboards listesi** | `fe/pages/Dashboards.tsx` 401. DataTable `dashboards` (`:179`), kolonlar `:41-49`. Arama istemcide (`:96-104`). Yıldız `saved_views` `dashboard-star` (`:29`). Yeni + Import (`:223`). | Yok. | `ui/*` + `coremetry/PageShell`; 11 inline style. | Etiket süzgeci, satır eylemi, şablon, "son görüntülenen" yok. Pano dışı "Ingest last 1h" şeridi (`:296-314`). |
| **Dashboard görünümü** | `fe/pages/Dashboard.tsx` 965. `.grid-4` (`fe/styles/globals.css:3527`), genişlik 1-4 (`:870`). HTML5 sürükle-sırala (`:844-868`), boyutlama yok. Taslak + Kaydet/Vazgeç (`:345-376`). Kiosk (`:467`). | Panel başına `source vm\|ch`. | 21 inline style. | İki ⋯ menü: PanelMenu (`:555-654`) + CorePanel menüsü. LazyMount yok. `?edit=1` yalnız admin'de açar (`:132`). Değişken URL'i ham `replaceState` (`:157-166`). |
| **Panel editörü** | `fe/components/dashboard/PanelEditor.tsx` 737. 520 px `position:fixed` kutu (`:105-115`). Kendi alanları (`:244`, `:414`). | PromQL düz `<textarea>`, öneri/doğrulama yok. | 35 inline style; ham `<input>/<select>`. | `role=dialog`, Esc, odak tuzağı yok. Önizleme yok. Y ekseni, lejant, takma ad seçeneği yok. |
| **Değişkenler** | `VariablesBar.tsx` 100. Tipler: service (sunucu aramalı), database (eager liste `:9-33`), custom (statik). | Etiket-değeri tabanlı değişken yok. | 5 + 8 inline style. | Çoklu seçim, bağımlı değişken yok. Eager liste picker kuralına aykırı. |
| **Import / export** | `fe/lib/dashboardIO.ts` 93. Şema `coremetry.dashboard/v1` (`:14-35`). | Yok. | Yok. | Export etiketleri atlıyor (`:26-33`). Panel doğrulaması yok. |
| **Grafana köprüsü** | Pano içe aktarma / datasource proxy **yok**. "Köprü" sözcüğü grafik birim köprüsüdür (`fe/lib/chart/dataFrame.ts:1`). `docs/grafana/` yalnız öz-izleme envanteri. | Yok. | Yok. | Grafana JSON içe aktarma sıfırdan yazılır (§5.5). |
| **Şablonlar** | `ch/dashboard_presets.go:122` 10 hazır pano, sürüm `apm-v2` (`:47`), boot'ta tohum (`:49-118`). | Hepsi span-metric + metric paneli; PromQL paneli 0. Ad karışık (noktalı OTel + `kafka_consumergroup_lag` `:658`). | Yok. | FE galerisi yok; yeni pano boş açılır (`Dashboards.tsx:348`). |
| **Grafik katmanı** | `fe/components/chart/CorePanel.tsx` 1393 kanonik. Görseller `:316`. Lejant tablosu + tıkla-izole (`PanelLegend.tsx:64-70`, `:91-94`). Palet `--series-1..8` (`fe/styles/tokens.css:62-69`). Top-8 + "others" (`fe/lib/chart/foldTopN.ts:41`). | Yok. | Dokunulmaz (chart-layer denetimi). | `yMin/yMax` ve ikinci eksen yok (`:154-317`). Isı haritası canvas (`fe/components/LatencyHeatmap.tsx:20-24`). |
| **Backend seam** | `api/metricsource.go:156-299`. Seçim `:701`. VM→CH fallback yok (`:62-67`). | VM'de çağrılan: `query_range`, `query`, `labels`, `label/<k>/values`. **Çağrılmayan:** `series`, `status/tsdb`, `metadata`, `status/top_queries`, `trace=1`, `extra_filters`, `limit`. | Yok. | Ad araması tüm adları çekip Go'da süzer (`vm/promql.go:1338`). Seri kesmesi sırasız ilk 1000 (`internal/promapi/meta.go:58-61`). |

### 2.2 Kapalı işlerle ilişki

| Kapalı iş | Bu öneri ne yapar |
|---|---|
| Explore v2 P5 (v0.8.123): "/metrics yalnız katalog, satır → Explore" | **AŞILIR.** Katalog satırı Metrics Explorer'da açılır (dilim 2.5, OK gerekir). v0.9.561 varsayılan editör kararı korunur. |
| Dashboard FAZ 2 (772-790): serbest boyutlandırma ve repeat-by-variable kapsam dışı; "çift menü yasak" | Korunur. 12 kolon yalnız genişlik basamağını artırır, serbest yerleşim değildir. "Tek ⋯" kuralı bugün delinmiş; onarılır. |
| Chart consolidation, grafik audit, chart-layer yükseltme | Dokunulmaz. `yMin/yMax`, ikinci eksen, yeni motor istenmez. |
| VM Explore adaptasyonu (1199-1213) | Üzerine kurulur. Exemplar muafiyeti ve "CH ön-parse / VM ham geçiş" asimetrisi korunur; UI rozetle söyler. |
| PromQL konsolu (Thanos) ertelendi | Yeniden önerilmez. MQE'deki Thanos kaynak seçimi (`:1151`, `:846`) olduğu gibi taşınır. Memory "UI yok" diyor, kod kullanıyor: çelişki, doğrulanmadı. |
| Doorway metrics "DO NOT BUILD", Endpoints/Overview kaynak varsayılanları | Dokunulmaz. |
| v0.10.1809 ad gizleme | Korunur. UI gizli adı ilk kez görünür kılar (§3.1 satır 15). |

## 3. "VM-first" somut olarak ne demek

### 3.1 Özellik tablosu

Yeni uçların hepsi **`api/metrics_discovery.go`** içinde (`registerMetricsDiscoveryRoutes`, `registerRoutesExtra`); `api.go` büyümez. Yeni cache anahtarları `fnvDigest(sortedSlice(girdiler))` ile kurulur.

| # | Özellik | Bugün | Öneri | Uç | Cache anahtarı girdileri | Sınır / koruma |
|---|---|---|---|---|---|---|
| 1 | Ad arama + tür/birim/açıklama | `/api/metrics/names` (`api/api.go:4691`). VM'de tür/birim son ekten, açıklama yok (`vm/describe.go:64`). | Aynı uç. Alanlar eklenir: `help`, `typeSource` (`catalog` \| `vm` \| `suffix`). Kaynak merdiveni: `metric_catalog` eşlemesi → VM `/api/v1/metadata` → son ek. | Mevcut + alan | mevcut (`src, svc, q, limit, offset, dropTag`) + `cluster` | Picker `limit ≤ 50` (bugün tuş başına 200, `MetricNamePicker.tsx:48-49`). |
| 2 | Etiketle keşif (etiket → değer → seri) | Değerler `/api/metrics/labels` (`api.go:4953`), adlar `/api/metrics/attr-keys` (`:4998`, 100 tavan). İkisi de metrik ister. | Metrik seçmeden etiket listesi. Değerler için mevcut uç yeniden kullanılır. | **Yeni** `GET /api/metrics/discover/labels?match&q&limit` | `src, match, q, limit, since, cluster, nd` | `limit ≤ 200`, pencere ≤ 24 sa, 60 sn TTL. |
| 3 | Kardinalite | VM için yok. CH için `/api/admin/cardinality` (`api.go:2223`). | VM `status/tsdb`: ada, etikete, etiket-değer çiftine göre seri sayısı. | **Yeni** `GET /api/metrics/cardinality?topN&focusLabel&match&date` | `src, topN, focusLabel, match, date, cluster` | `topN ≤ 50`, 10 dk TTL, tek uçuş, 15 sn zaman aşımı (`internal/promapi/promapi.go:169`). |
| 4 | Seri örnekleri | Yok. | "Bu seçiciye uyan 20 seri" listesi. | **Yeni** `GET /api/metrics/series-sample?match&limit` | `src, match, limit, pencere, cluster` | `__name__` zorunlu, `limit ≤ 20`, pencere ≤ 1 sa. |
| 5 | Sayaç → rate varsayılanı | Backend otomatik uygulamaz (`vm/promql.go:238`). MQE varsayılanı `avg` (`:154`). Explore'da sayaç şablonu rate (v0.9.1201). | Builder türe göre seçer: sayaç → `rate`, histogram → `p95`, gösterge → `avg`. Satırda "sayaç → rate" notu. | Yok (FE) | Yok | Tür "tahmini" ise not bunu söyler. |
| 6 | Histogram → yüzdelik / ısı haritası | Var: `histogram_quantile` (`vm/promql.go:735`), ısı haritası (`vm/histogram.go:89-105`, `api.go:4895`). | Histogram seçilince p50/p95/p99 ve "ısı haritası" hazır düğmeleri. Filtresiz kova taraması 400'ünün nedeni satırda gösterilir. | Mevcut; `/histogram`'a `maxDataPoints` eklenir | mevcut + `mdp` | Mevcut `guardBucketScan`. |
| 7 | `by` / `without` | `groupBy` = `by`. | Builder yalnız `by`. `without` kod kipinde. | Mevcut | Mevcut | Etiket önerisi sunucudan (bugün 8 sabit anahtar, MQE `:370`). |
| 8 | MetricsQL özellikleri | VM'e ham geçiş (`api/metricsource.go:571`). | Kod kipinde parçacık menüsü: `WITH`, `default`, `topk_avg`, `rollup_rate`, `keep_metric_names`. Builder'a girmez. | Mevcut `/api/metrics/promql` | Mevcut | Sorgu ≤ 8192 karakter (`api/promql.go:16`). |
| 9 | Anlık / aralık | `?instant=1` yalnız VM (`api/promql.go:99`). | Tablo ve tek değer görselleri anlık sorgu kullanır. Builder'da ilk sürümde aralığın son değeri. | Mevcut | Mevcut | Yok. |
| 10 | Adım / çözünürlük | VM: `ceil(aralık / (maxDataPoints veya 300))`, en az 1 sn (`vm/promql.go:942`). Bundle `maxDataPoints` göndermez (`api/dashboards_data.go:284`). | Tek sözleşme: FE panel genişliğinden `maxDataPoints` yollar (`fe/lib/chartStep.ts`), yanıt `stepSec` döner, UI "adım 1 dk (otomatik)" yazar. Kod kipinde `$__interval`. | Mevcut + `meta.stepSec` | mevcut + `mdp` | Bundle'a `maxDataPoints` eklenir. |
| 11 | Seri sınırı + "N seri kırpıldı" | VM: sırasız ilk 1000 (`promapi/meta.go:58-61`), bayrak HTTP'de düşer. FE top-N istemcide. | Sunucu tarafı deterministik kesme: `topk_avg(N, ifade)`. Sınıra değilince tek `count(ifade)` anlık sorgusu ile toplam. Bildirim: "12 seriden 8'i gösteriliyor". | Mevcut + `limit`, `rank` + `meta` | mevcut + `limit, rank` | Varsayılan 8, seçenek 8/20/50, tavan 50. |
| 12 | Sorgu maliyeti / süresi | Hiçbir metrik yanıtında yok. | `meta{backend, query, durationMs, seriesTotal, seriesReturned, truncated, partial, stepSec}`. Ek alan; var olan alanlar değişmez. | `/query`, `/promql`, `/histogram`, bundle slot | Değişmez | `durationMs` önbellek isabetinde "önbellekten" yazar (`X-Cache`). |
| 13 | En pahalı sorgular / sorgu izi | Yok. | Keşif altında admin bölümü: VM `status/top_queries`. Kod kipinde isteğe bağlı `trace=1`. | **Yeni** `GET /api/admin/metrics/top-queries?topN&maxLifetime`; `/promql?trace=1` | `topN, maxLifetime` (30 sn); iz önbelleksiz | Admin. İz yalnız editor+, elle tetiklenir. |
| 14 | Çok-cluster kapsamı | Tek VM adresi. Cluster sıradan etiket süzgeci (`vm/promql.go:393`). Seçiciler kapsamsız (`api.go:4693-4696`). | Topbar cluster seçimi tek kaynak. Builder'da kilitli "kapsam" çipi. Kod kipinde ve seçicilerde VM `extra_filters[]` (sorgu metni değişmez). Etiket adı `LabelMap RoleCluster` (`vm/labelmap.go:57`). | Mevcut uçlara `cluster` | hepsine `cluster` | Kapsam etiketi olmayan seri gizlenir; çip bunu söyler ve tek tıkla kapanır. |
| 15 | Kuralla gizli metrik (v0.10.1809) | Adlar sessizce düşer; yanıt "gizli" demez (`api/metric_name_drop.go:101-108`). | Zarfına `hiddenByRule` sayısı. Admin Keşif'te gizli adı soluk satırda görür: "listeden gizli, sorgu çalışır". | Mevcut + alan | Mevcut (`:nd=`) | Viewer yalnız sayıyı görür. |
| 16 | Kod kipi hata konumu | CH: `promql: … (at position N)` metin içinde (`internal/promql/parse.go:86-88`). VM: 502 + VM metni. | Gövdeye `pos` alanı. CH'de var olan sayıdan, VM'de hata metnindeki artık kuyruktan türetilir; yoksa konumsuz. | Mevcut `/promql` | Değişmez | Hata yanıtı önbelleğe girmez. |
| 17 | Sorgu geçmişi | Explore: localStorage, 4 kayıt. Metrics: yok. | Kişisel, 50 kayıt, `saved_views page='metrics-history'`. Desen hazır (`api/promql_console_history.go:58-88`). | Mevcut `/api/views` | Yok | Şema değişmez. |
| 18 | Exemplar | Yalnız CH; kalıcı muafiyet (`fe/lib/api.ts:814-820`). | Metrics Explorer'da exemplar yok. Explore span satırında olduğu gibi kalır. | Yok | Yok | Yeniden açılmaz. |

### 3.2 VM API'leri: maliyet ve koruma

| VM API | Maliyet | Koruma |
|---|---|---|
| `/api/v1/status/tsdb` | Günlük ters indeks taraması. Seri sayısı arttıkça pahalı. | `topN ≤ 50`, tek gün, 10 dk paylaşımlı önbellek, tek uçuş. Viewer önbellekten okur. |
| `/api/v1/labels`, `/label/<k>/values` | Orta. `match[]` yoksa tüm indeks. | `limit`, pencere ≤ 24 sa, `q` en az 2 karakter, 60 sn TTL. |
| `/api/v1/series` | Yüksek. Geniş seçicide milyonlarca satır. | Yalnız `__name__` + `limit ≤ 20` + pencere ≤ 1 sa. Kardinalite için kullanılmaz. |
| `/api/v1/metadata` | Düşük. | `limit`, 5 dk TTL. Boş dönerse sessizce atlanır. |
| `/api/v1/status/top_queries` | Çok düşük (bellek içi sayaç). | Admin, `topN ≤ 20`. |
| `query` / `query_range` + `trace=1` | Sorgunun kendisi + iz yükü. | Elle, editor+, önbelleksiz, UI'da ayrı düğme. |

### 3.3 Metrik backend'i ClickHouse iken (dev / yerel)

| Konu | CH'de ne olur | UI nasıl söyler |
|---|---|---|
| Kaynak | `GET /api/metrics/capabilities` (yeni, 30 sn, anahtar `src`) `backend`, dil ve özellik bayraklarını döner. | Durum satırında "Kaynak: ClickHouse (yerel)". |
| Metadata | Tam: açıklama, birim, tür, son görülme (`ch/repo.go:5838`). | Tür rozeti "katalog". VM'den iyi. |
| Kod kipi | PromQL alt kümesi; `WITH`, alt sorgu, `rollup_rate` 400 verir (`internal/promql/eval.go:130`, `:224`). | Rozet "PromQL (alt küme)"; parçacık menüsünde MetricsQL öğeleri kapalı ve nedeni yazılı. |
| Kardinalite, top queries, iz | Yok. | Boş durum: "Yalnız VictoriaMetrics". CH kardinalitesi için mevcut admin sayfasına link. |
| Anlık sorgu | 400 (`api/promql.go:99`). | Tablo/tek değer aralığın son değerinden hesaplanır; not düşülür. |
| Seri kesme | 50.000 satır, alfabetik (`ch/spanmetric.go:1534`). | Aynı "N seri kırpıldı" bildirimi; sıralama "alfabetik" yazar. |

### 3.4 Ne alınır, ne bırakılır

| Ürün | Al | Bırak |
|---|---|---|
| Grafana Explore | Builder ⇄ kod geçişi, sorgu geçmişi, "çalışan sorguyu göster", `$__interval`. | Bölünmüş çift pano, datasource seçici, karışık kaynak. |
| Datadog Metrics Explorer | Tek satır anatomi (metrik → from → fonksiyon → by), formül satırı, tür/birim rozetli arama. | "Space/time aggregation" çifti (MetricsQL'de karşılığı karmaşık), not defteri. |
| Honeycomb | Sakin tek sütun, sonuç altında tablo, tıkla-izole. Span tarafı Explore'da kalır. | Metrik için BubbleUp (span jesti). |
| vmui | Kardinalite gezgini (ad / etiket / odak etiket), top queries, sorgu izi. | Ham JSON sekmesi, vmui'nin kendisini gömmek, relabel hata ayıklayıcı. |

## 4. Tasarım dili: "modern ve sade" kurallara çevrildi

Üç mockup (`log-search.html`, `anomaly-detail.html`, `metrics-explore.html`) aynı token bloklarını, aynı `.ctx` bağlam çubuğunu ve aynı taban sınıfları kullanır.

| Kural | Uygulama |
|---|---|
| Bölge başına tek birincil eylem | Sorgu bölgesi: yok (yazdıkça çalışır, 300 ms). Kod kipi: "Çalıştır". Liste: "Yeni pano". Editör: "Kaydet". |
| Sorgu satırı anatomisi (soldan sağa, sabit) | Harf · metrik (tür rozeti) · süzgeç çipleri · fonksiyon · toplama + `by` · lejant · göster/gizle · ⋯ |
| Aşamalı açılım | Adım, seri sınırı, üretilen MetricsQL, lejant biçimi ikinci düzeyde. Varsayılan görünümde yalnız satır + grafik + lejant tablosu. |
| Bağlam çubuğu | Başlık → Env → Cluster → (pano değişkenleri) → boşluk → yenile → zaman seçici en sağda. İkinci cluster seçici çizilmez. |
| Yüzen şerit yok | Sorgu çubuğu, pano düzenleme çubuğu ve sayfalayıcı akışta durur; `position:sticky` yok. |
| Yoğunluk | 13 px gövde, 12 px tablo, 30 px kontrol, 8 px ızgara. Kart içinde kart yok. Çizgi yerine boşluk. |
| Tipografi | Inter; metrik adı, etiket, sorgu ve sayı JetBrains Mono + `tabular-nums`. Tek başlık boyutu (18 px). |
| Renk | Yalnız sapan değerde. Seri rengi tek başına sinyal değil: lejant metni + çizgi deseni. Palet `--series-1..8`. |
| Boş durum | Ne eksik + tek eylem. Örnek: "Isı haritası histogram ister. Bu metrik sayaç." + "…_bucket metriğine geç". |
| Yükleniyor | Grafik alanında iskelet; önceki veri soluk kalır. Durum satırında dönen işaret. |
| Hata | Grafik yerinde tek cümle + konum (kod kipi) + "yeniden dene". 400 (sorgu hatası) ile 502 (VM erişilemedi) ayrı metin. |
| Dürüstlük satırı | Her sonuçta: "8 / 12 seri · adım 1 dk (otomatik) · 142 ms · VictoriaMetrics". |
| Dil | Tek dil (Türkçe arayüz); "PromQL" yerine "MetricsQL" (VM açıkken). |
| Stack | Yeni bileşenler `components/coremetry/metrics/` ve `components/coremetry/dashboard/`; `components/ui` importu eklenmez; inline style 0; ham `<select>/<input>` yok. |

**Kalkanlar:** Code (DSL) sekmesi (MQE `:231-243`; MetricsQL kipi yerini alır) · 13 sabit etiket listesi (`:75`) · birim elle yazma kutusu (birim metadata'dan, panel seçeneğinde) · renk seçici satır içi · Explore'un "Source" anahtarı (`Explore.tsx:809`) · Dashboards listesindeki ingest şeridi · ikinci ⋯ menü.

## 5. Dashboards revizyonu

### 5.1 Tutulan / değişen / kalkan

| | Öğe |
|---|---|
| **Tutulur** | `dashboards` tablosu (`ch/store.go:2432`), CRUD + audit (`api.go:9576-9658`), yıldız (`dashboard-star`), kiosk (`?kiosk=1`), satır (row) panelleri, sürükle-sırala, global saat + zoom (`usePageZoomRange`), 9 panel tipi, `coremetry.dashboard/v1` dosya biçimi, sohbetten panel ekleme. |
| **Değişir** | Liste (süzgeç + satır eylemleri), ızgara 4 → 12 kolon, panel başlığı (tek ⋯), panel editörü (bölünmüş görünüm), değişkenler (etiket-değeri tipi), bundle (5 tip daha), şablonlar (galeri + VM sürümleri). |
| **Kalkar** | Ingest şeridi (`Dashboards.tsx:296-314`), 520 px editör kutusu, panel editörünün kendi sorgu alanları, `database` değişkeninin eager listesi, elle tutulan panel tipi listesi (`Dashboard.tsx:684`). |

### 5.2 Liste

| İstek | Karşılık | Şema? |
|---|---|---|
| Arama | Ad + açıklama + etiket (var). 100 panoyu geçerse sunucu araması. | Hayır |
| Etiket süzgeci | Etiket çipleri (var olan `tags` kolonu). **Klasör önerilmez**: etiket aynı işi şemasız yapar. | Hayır |
| Favori | Var. "Yalnız favoriler" anahtarı eklenir. | Hayır |
| Son görüntülemeniz | Kişisel `saved_views page='dashboard-recent'`, tek satır JSON (son 50 pano id + zaman). | Hayır |
| Sahip | Kolon yok; panolar paylaşılan (`ch/dashboard.go:18-37`). **Öneri:** "Son düzenleyen" `audit_log`'dan türetilir (`dashboard.create/update` kayıtları var, `api.go:9614`, `:9646`). | Hayır (audit saklama süresi doğrulanmadı) |
| Satır eylemleri | Aç · Çoğalt · Dışa aktar · Sil (bugün yalnız pano sayfasında). | Hayır |
| Şablon şeridi | Tek satır, 4 öğe; tablo birincil kalır. | Hayır |

### 5.3 Pano görünümü

| Konu | Öneri |
|---|---|
| Izgara | 12 kolon, akış sıralı (x/y yok). Panel JSON'una isteğe bağlı `span` (2-12); yoksa `width × 3`. Göç gerekmez (panel dizisi opak JSON). Yükseklik s/m/l kalır. |
| Bağlam çubuğu | Cluster = Topbar'ın tek seçicisi. Pano değişkenleri onun sağında. Yenile ve zaman en sağda. |
| Değişkenler | Yeni tip "etiket değeri": `/api/metrics/labels` üzerinden sunucu aramalı. `database` aynı desene geçer. Çoklu seçim `=~"a\|b"` olarak açılır. URL aynası router üzerinden; yalnız panoda tanımlı değişken adları yazılır/silinir. |
| Panel başlığı | Tek ⋯: Düzenle · Tam ekran · Explorer'da aç · Kopyala · CSV. Grafik panellerinde CorePanel `menuExtra` (`CorePanel.tsx:154-317`); grafik olmayanlarda ortak `PanelFrame`. |
| Tam ekran | Tek panel URL'i `?panel=<id>` (rezerve paramlara eklenir, `fe/lib/dashboardUrl.ts:19`). |
| Paylaş | Var olan `ShareButton` (`Explore.tsx:816-819`). Link değişken + aralık + kiosk taşır. |
| TV / kiosk | Var. Eklenen: düzenleme düğmeleri gizli, başlıkta saat. Ayrı rota yok. |
| Yükleme | Panel başına `LazyMount` (`fe/components/LazyMount.tsx:19-72`). Bundle: metric + spanmetric + promql + stat + gauge + topn + heatmap. |
| Düzenleme kipi | Akışta duran çubuk: + Panel · Değişkenler · Geri al · Vazgeç · Kaydet. `?edit=1` editor için de açar. |

### 5.4 Panel editörü

| Konu | Öneri |
|---|---|
| Yerleşim | Tam sayfa bölünmüş görünüm (`?edit=<panelId>`): solda önizleme + sorgu, sağda seçenekler. Dar ekranda alt alta. Esc = Vazgeç, odak yönetimi `coremetry/overlay` üzerinden. |
| Sorgu | Metrics Explorer ile **aynı** `QueryRow`. Builder ⇄ MetricsQL aynı anahtar. Span panelleri için mevcut span alanları kalır. |
| Önizleme | Canlı, 300 ms gecikmeli; gerçek panel bileşeni çizer. Durum satırı aynı. |
| Seçenekler | Katlanır gruplar: Görselleştirme · Birim ve ondalık · Eşikler · Lejant · Yığma · Y ekseni · Boyut ve zaman. |
| Kısıt | Y ekseni yalnız "0'dan başlat" ve "log ölçek" (CorePanel'de var). `yMin/yMax` yok: chart-layer işi, bu programın dışında. Birim listesi 6 değerle sınırlı (`fe/lib/units.ts:9-16`); `µs`, `ns`, `reqps` eklenir. |
| Yeni tip yok | Tablo ve sıralı liste yeni panel tipi değil, `promql` paneline `viz: 'table' \| 'toplist'` (skill kuralı). Stat ve gauge'a MetricsQL kaynağı eklenir. |

### 5.5 Şablonlar, Grafana içe aktarma, sürüm

| Konu | Bugün | Öneri |
|---|---|---|
| Şablon galerisi | 10 hazır pano yalnız boot tohumu. | **Yeni** `GET /api/dashboards/templates` (`api/dashboard_templates.go`): ad, etiket, panel sayısı, gereken metrikler. "Şablondan oluştur" `custom-*` kopya yaratır. |
| VM şablonları | PromQL paneli 0. | Dört MetricsQL şablonu: Service RED (`http_server_request_duration_seconds_*`), JVM, Kafka (sorgular hazır: `fe/lib/kafkaMetrics.ts`), Kubernetes. Ön kontrol: `MetricExists` (`api/metricsource.go:156-299`) eksik metriği galeride söyler. |
| Grafana JSON | Yok. | İstemcide saf dönüştürücü + golden test + kuru çalıştırma raporu ("9 panel alındı, 3 atlandı: neden"). Alınan: Prometheus kaynaklı `timeseries`/`graph`, `stat`, `gauge`, `heatmap`, `text`, `row`; `targets[].expr` → `promql` paneli; `gridPos.w/2` → `span`; `label_values()` → etiket-değeri değişkeni. **Alınmayan:** transformations, overrides, karışık kaynak, Loki/Tempo panelleri, repeat, alarm, annotation sorguları. |
| Sürüm / geri al | Yok; son yazan kazanır. | Ucuz iki şey: (a) düzenleme kipinde oturum içi geri al; (b) PUT'ta `updatedAt` eşleşmesi, uyuşmazsa 409 "başkası kaydetti". Kalıcı geçmiş şema ister: önerilmez. |
| Export | Etiketleri atlıyor. | Etiketler eklenir (hata düzeltme). |

## 6. Performans ve ölçüm planı

| Hedef | Değer | Nasıl ölçülür | Bugün |
|---|---|---|---|
| `/api/metrics/names` | p99 < 200 ms ılık, < 1 sn soğuk | Coremetry'nin kendi HTTP span'leri (`internal/selfobs`); perfcheck | ölçülmedi |
| `/api/metrics/query`, `/promql` (≤ 6 sa) | p99 < 200 ms ılık, < 1 sn soğuk | aynı + `meta.durationMs` | ölçülmedi |
| `/api/metrics/cardinality` | < 3 sn soğuk, ılık < 50 ms | aynı; §9 komut 2 | ölçülmedi |
| Metrics Explorer TTFI | < 1,5 sn taze sekme | perfcheck; CorePanel ayrı chunk kalır (`fe/components/chart/corePanelEntry.tsx:1-6`) | ölçülmedi |
| İlk yükleme paketi | < 347 KB gz, Radix giriş paketinde yok | `bundle:check` kapısı | geçiyor (shadcn Faz 1) |
| 20 panelli pano, ilk boya | ≤ 2 veri isteği (bundle) + görünür panel kadar çizim | tarayıcı ağ sayımı; bundle slot sayısı | N panel = N istek (`fe/components/dashboard/PanelRenderer.tsx:404-993`) |
| Yenileme | ≥ 10 sn, tek saat, `document.hidden`'da durur | `pollingBudget.pin.test.ts` | Metrics otomatik, Explore elle |
| Seçici tuş başına yük | ≤ 50 satır | ağ yanıt boyutu | 200 satır |
| Seri kesme dürüstlüğü | kesilen her sonuçta bildirim | birim test: `truncated=true` → bildirim çizilir | VM kesmesi görünmez |

Her dilim kendi ölçümünü commit gövdesine yazar: önce / sonra, aynı pencere, aynı pano.

## 7. Faz planı

Tür: **[T]** = tasarım kararı içerir (ana oturum) · **[M]** = mekanik → `implementer`. **OK** = var olan davranışı değiştirir, operatör onayı gerekir.

### Faz 1: Backend sözleşmesi (görünmez, eklemeli)

| Dilim | Tür | Ne | Kabul ölçütü | OK |
|---|---|---|---|---|
| 1.1 | T | `meta` zarfı: `/query`, `/promql`, `/histogram`, bundle slot. VM `Truncated/Partial` taşınır. | Eski alanlar birebir; golden test; VM 1000 kesmesinde `truncated=true`. | yok |
| 1.2 | M | FE `api.metricQuery` zarfı açmayı bırakır; MQE ve panoda `note` + kesme bildirimi görünür. | Kesilen sorguda bildirim çizilir (birim test). | yok |
| 1.3 | T | `/api/metrics/capabilities` + ad zarfına `help`, `typeSource`, `hiddenByRule`. Metadata merdiveni. | VM'de `typeSource` doğru; CH'de `catalog`. | yok |
| 1.4 | T | Sunucu top-N: `limit` + `rank` → `topk_avg`; toplam için koşullu `count`. | Aynı sorgu iki kez aynı N seriyi döner; cache anahtarında `limit, rank`. | **OK** |
| 1.5 | T | Cluster kapsamı: `cluster` paramı, `extra_filters[]`, seçiciler kapsamlı. | İki cluster'da ad listesi farklı; anahtar `cluster` taşır. | **OK** |
| 1.6 | T | Keşif uçları: `discover/labels`, `cardinality`, `series-sample`, admin `top-queries`. | Tavanlar test edilir; CH'de `supported:false`. | yok |
| 1.7 | M | `/promql` hata gövdesine `pos`. | CH parse hatasında `pos` sayı. | yok |

### Faz 2: Tek sorgu bileşeni + Metrics Explorer

| Dilim | Tür | Ne | Kabul ölçütü | OK |
|---|---|---|---|---|
| 2.1 | T | Tek model + tek URL biçimi; eski `?mq=`, `?promql=`, Explore `?q=` bir kez içe okunur. | Eski yer imi aynı grafiği açar (tablo testi). | yok |
| 2.2 | T | `coremetry/metrics/QueryRow` + parçaları (metrik seçici, süzgeç, fonksiyon, `by`); henüz sayfa kullanmaz. | Birim testler; inline style 0; ham element 0. | yok |
| 2.3 | T | `/metrics` Sorgu sekmesi yeni builder'a geçer (kesme dilimi, tek commit). | Mockup görünüm 1 ile eşleşir; TTFI ölçülür. | **OK** |
| 2.4 | T | MetricsQL kipi: renklendirme, tamamlama, hata konumu, "builder'a çevir", geçmiş. | Builder → kod her zaman; kod → builder yalnız builder biçiminde. | **OK** (Code DSL sekmesi kalkar) |
| 2.5 | T | Keşif sekmesi: katalog + etiket gezgini + kardinalite. Satır tıklaması Metrics Explorer'da açar. | Mockup görünüm 3; viewer salt okur. | **OK** (Explore v2 P5 aşılır) |
| 2.6 | M | Explore metrik satırı ortak `QueryRow`'u kullanır. | Var olan Explore `?q=` linkleri aynı sonucu verir. | yok |
| 2.7 | T | Explore'un eski `source=metrics\|logs` panelleri kalkar; `/metrics` ve `/logs`'a yönlenir. | `MetricsExplorer.tsx`, `LogsExplorer.tsx`, `ExploreViz.tsx` silinir. | **OK** |
| 2.8 | M | MQE ve `.mqe-*` CSS silinir. | Operatör 2.3-2.5'e prod'da OK dedikten sonra. | yok |

### Faz 3: Dashboards

| Dilim | Tür | Ne | Kabul ölçütü | OK |
|---|---|---|---|---|
| 3.1 | M | Liste: etiket çipleri, "yalnız favoriler", satır eylemleri, export'a etiket. | DataTable + `useDataTable` kalır. | yok |
| 3.2 | T | Ingest şeridi kalkar; "Son görüntülemeniz" + "Son düzenleyen" kolonları. | Şema değişmez. | **OK** |
| 3.3 | T | Şablon galerisi + 4 VM şablonu + ön kontrol. | Eksik metrik galeride yazılır. | yok |
| 3.4 | T | `PanelFrame`: tek ⋯, LazyMount, Paylaş, `?panel=`. | Panel başına tek menü; görünmeyen panel istek atmaz. | **OK** (menü yeri) |
| 3.5 | T | 12 kolon ızgara, `span` alanı. | Var olan panolar piksel yakın aynı dizilir (`width × 3`). | **OK** |
| 3.6 | T | Bundle 5 tip daha + `maxDataPoints`. | 20 panelli panoda ≤ 2 veri isteği. | yok |
| 3.7 | T | Panel editörü bölünmüş görünüm + ortak `QueryRow` + önizleme. | Mockup görünüm 6; Esc ve odak testleri. | **OK** |
| 3.8 | T | Değişkenler: etiket-değeri tipi, çoklu seçim, URL aynası router'dan. | Eager liste kalmaz. | **OK** (`database` davranışı) |
| 3.9 | T | Grafana JSON içe aktarma (kuru çalıştırma raporu). | Golden test; atlanan her panel nedeniyle listelenir. | yok |
| 3.10 | M | 409 çakışma koruması + oturum içi geri al. | İki sekmede kaydetme testi. | yok |

**Bu programın dışında (ayrı Faz 0 ister):** metrik sorgusundan alarm kuralı. `AlertRule.Metric` sabit enum, MetricsQL alanı yok (`internal/evaluator/evaluator.go:1808`, `ch/problem.go:24`). Mockup düğmeyi gösterir; ürüne o audit'ten önce girmez.

**Operatörden beklenen girdiler:** mockup onayı (6 görünüm) · §8 sorularına cevap · §9 komutlarının prod çıktısı (özellikle 1, 2, 4, 6).

## 8. Açık sorular (tasarımı değiştirenler)

| # | Soru | Önerilen varsayılan |
|---|---|---|
| S1 | İki sayfa + tek bileşen mi, tek birleşik Explorer mı? | İki sayfa. |
| S2 | `/metrics` hangi sekmeyle açılır? | Sorgu (bugünkü davranış). Boş sorguda Keşif'e götüren tek öneri satırı. |
| S3 | Katalog satırı Explore yerine Metrics Explorer'da mı açılsın? | Evet. |
| S4 | Sayaçta varsayılan `rate`, histogramda `p95`? | Evet, satırda notuyla. |
| S5 | Varsayılan seri sınırı? (bugün MQE 12, pano 8, Explore 50'ye kadar) | 8 (palet 8 renk); seçenek 8/20/50. |
| S6 | Cluster kapsamı kod kipinde de uygulansın mı? | Evet; çip görünür, tek tıkla "tüm cluster'lar". |
| S7 | Pano listesinde "sahip" ne olsun? | "Son düzenleyen", `audit_log`'dan; kolon eklenmez. |
| S8 | 12 kolon akış ızgarası (serbest x/y yok)? | Evet. |
| S9 | Sorgu geçmişi sunucuda mı (kişisel 50), tarayıcıda mı? | Sunucuda (`saved_views`). |
| S10 | Kardinalite sekmesini kim görür? | Herkes okur (önbellekten); top queries ve sorgu izi admin / editor+. |
| S11 | Grafana içe aktarma kapsamı §5.5'teki dar liste mi? | Evet; gerisi raporda "atlandı". |

## 9. Doğrulanmadı / ölçülmedi

| # | Konu | Operatörün prod'da çalıştıracağı |
|---|---|---|
| 1 | VM sürümü ve metadata desteği (tür/birim/açıklama geliyor mu) | `curl -s "$VM/api/v1/status/buildinfo"` ; `curl -s "$VM/api/v1/metadata?limit=5"` |
| 2 | `status/tsdb` süresi | `time curl -s "$VM/api/v1/status/tsdb?topN=20" -o /dev/null` ; aynı komut `&focusLabel=service_name` ile |
| 3 | Top queries açık mı | `curl -s "$VM/api/v1/status/top_queries?topN=10&maxLifetime=1h"` |
| 4 | `extra_filters[]` ad listesini daraltıyor mu | `curl -s "$VM/api/v1/label/__name__/values" --data-urlencode 'extra_filters[]={k8s_cluster_name="<cluster>"}' \| jq '.data\|length'` ; filtresiz sayıyla karşılaştır |
| 5 | `$__interval` kabul ediliyor mu | `curl -s "$VM/api/v1/query_range" --data-urlencode 'query=rate(up[$__interval])' -d start=-1h -d step=60s` |
| 6 | Cluster etiketinin adı | `curl -s "$VM/api/v1/labels" \| jq -r '.data[]' \| grep -i cluster` |
| 7 | Ad listesinin boyutu ve süresi | `time curl -s "$VM/api/v1/label/__name__/values" \| jq '.data\|length'` |
| 8 | Kubernetes şablonu metrikleri var mı | `curl -s "$VM/api/v1/label/__name__/values" --data-urlencode 'match[]={__name__=~"kube_.*\|container_.*"}' \| jq '.data\|length'` |
| 9 | VM parse hatasının metni (konum türetilebilir mi) | `curl -s "$VM/api/v1/query" --data-urlencode 'query=sum(rate(up[5m])'` |
| 10 | `topk_avg` üçüncü argümanı ("kalanlar" serisi) | `curl -s "$VM/api/v1/query_range" --data-urlencode 'query=topk_avg(3, sum by (job)(up), "job=diğer")' -d start=-1h -d step=60s` |
| 11 | BaseURL kiracı yolu içeriyor mu (`/select/0/prometheus`) | Ayarlar → VictoriaMetrics adresi (kod yol eklemez, `internal/promapi/promapi.go:211`) |
| 12 | Bugünkü `/api/metrics/query` p99 | Traces: Coremetry'nin kendi servisi, `http.route = /api/metrics/query`, son 24 sa p99 |

Koddan doğrulanamayanlar: `/api/metrics/labels` metriksiz çağrılabilir mi · `metric_catalog` CH metrik yazımı kapanınca (VM Dilim 5) beslenmeye devam eder mi · global yenileme saati MQE sorgularını gerçekten yeniliyor mu (MQE `:796-808` canlı işareti yok) · `audit_log` saklama süresi · `dashPanelTTL` değeri · ≤ 640 px'te `.grid-4` + `span 2..4` taşması · Thanos konsolu için memory "UI yok" derken MQE'nin onu kullanması · CH PromQL yaprak okumalarının metrik dışlamadan geçip geçmediği.

## 11. Operatör kararı (2026-11-06)
Operatör mockup ve öneri özetini gördü: "Tamamdır." Audit ve mockup ONAYLI; S1–S11 önerilen varsayılanlarla karara bağlandı: iki sayfa + tek sorgu bileşeni · `/metrics` Sorgu sekmesiyle açılır · katalog satırı Metrics Explorer'da açılır · sayaçta varsayılan `rate`, histogramda `p95` (satırda notuyla) · varsayılan seri sınırı 8 (8/20/50) · cluster kapsamı kod kipinde de uygulanır (çip görünür, tek tıkla kapanır) · pano listesinde "Son düzenleyen" (kolon eklenmez) · 12 kolon akış ızgarası · sorgu geçmişi sunucuda kişisel 50 · kardinaliteyi herkes okur, pahalı sorgular ve sorgu izi admin/editor · Grafana içe aktarma dar kapsam + kuru rapor. "OK gerekir" işaretli dilimler bu onayla kapsanır; her biri yine tek commit ile geri alınabilir.
