# Servis sayfası — Details ve Infrastructure sadeleştirme planı (Faz 0, 2026-11-13)

Durum: operatör prod'da iki sekmeye "çok karmaşık / çok detay var" dedi; öneri mockup'ını gördü, "Go" (2026-11-13). Mockup: claude.ai/artifact/1JriioYape7Xw6SwBksReA (özeti brief'te; bu doküman o özeti esas alır).
Emsal biçim: `docs/frontend/traces-shadcn-plan.md` (S0 karakterizasyon testleri, her dilim tek commit geri alınır, her dilim ayrı v0.10.X).
Hedef: hiçbir bölüm silinmez, hiçbir veri kaybolmaz; **varsayılan görünümde** az şey açık, az kolon, iç adlar yok. Operatör-kararı yorumları (§2) aynen korunur; çakışanlar soru olarak §7'de.

## 1. Bugün ne var (kanıt) ve mockup karşılığı

### Details — `frontend/src/pages/Service.tsx:625-747`
Sıra: `DetailsPropsStrip` (#dtl-props, :635) › ?op= kapsam şeridi (:636-653) › Clusters `ServiceClusterBreakdown` (:659-665) › Database `DBQueriesPanel` (:670-687) › `DetailsEndpointsSection` (:690) › Performance `ServiceCharts` (:691-704) › `ServiceAnnotationLane` (:714) › `DetailsMetricsSection` (:721) › Latency `ServiceLatencyHeatmap` (:723-730) › Runtime & rollouts `DeployHistoryPanel` (:731-743) › `DetailsToc` (:745). Sarmal flex + inline style (:626-627).

| Bölüm | Bileşen | Veri (endpoint) | Mount'ta çeker mi | Bugün aç/kapa | Mockup |
|---|---|---|---|---|---|
| Properties | `pages/service/DetailsPropsStrip.tsx` | `/api/services/:svc/runtime`, rollouts 7g, gitops, `instances` (:36-61) | Evet, koşulsuz | — | Düz etiket-değer satırı; veri aynı |
| Clusters | `ServiceClusterBreakdown.tsx` | `/api/services/:svc/clusters` + `/api/clusters/sources` (:38-51) | Evet (LazyMount yalnız görünürlük) | — | AÇIK; 9 → 5 kolon |
| Database | `components/DBQueriesPanel.tsx` | `/api/services/:svc/db-queries?limit=101` (:86-106) | `open` kapılı; `defaultOpen` ile açılışta | DisclosureButton, kalıcı değil (:163) | AÇIK; 12 → 6 kolon |
| Endpoints | `DetailsEndpointsSection.tsx` | `/api/endpoints` (limit 50); kip=cluster'da +N çağrı (:73-83) | Evet, hook LazyMount DIŞINDA | — | KAPALI satır |
| Performance | `components/ServiceCharts.tsx` | `POST /api/spans/metric-batch` veya `/api/metrics/resolve` (:202-305) | Evet, `enabled` kapısı yok | — | KAPALI satır |
| Metrikler | `DetailsMetricsSection.tsx` | `/api/metrics/names?limit=500` (katalog, sekme kapılı, Service.tsx:235) + panel×metrik `metricQueryFull` useQueries (:98-113) | Katalog sekmede; paneller koşulsuz | — | KAPALI satır |
| Gecikme dağılımı | `ServiceLatencyHeatmap.tsx` | `/api/spans/heatmap` (:111-125) + clusters | **Hayır** kapalıyken (`if (collapsed) return`, :112) | localStorage `svc.heatmap.collapsed` (global) | KAPALI satır |
| Dağıtımlar | `components/DeployHistoryPanel.tsx` | `/api/services/:svc/rollouts` 7g (aynı query key: Props şeridiyle paylaşımlı) + copilotConfig probu | Evet | — | KAPALI satır |
| ToC | `DetailsToc.tsx` | — | — | sticky 140 px (:56-60) | KALKAR |

Kaynak rozetleri `SectionHead source=` ile Service.tsx:659 (`service_env_summary_5m · cluster…`), :670 (`spans · db.statement`), :691, :723, :731; ayrıca `ServiceClusterBreakdown.tsx:117-122` ve `DetailsEndpointsSection.tsx:117`.
Tablo türleri: Clusters/Database/Endpoints `DataTable + useDataTable` (storageKey `service-clusters`, `db-queries-panel`, `svc-dtl-endpoints`). Tek emsal bir "collapsible section" primitive'i yok: `components/shadcn/collapsible.tsx` var, `coremetry/SectionHead.tsx` yalnız başlık atomu, `ui/DisclosureButton` DONUK.

### Infrastructure — `pages/service/ServiceInfraTab.tsx:539-549`
Sıra: `thanosBody` (Clusters tablosu :316-415 → 4 KPI karosu :421-455 → Kaynak kullanımı :461-494 → Router/HAProxy :501-529) › `ServicePodInventory excludeClusters=…` › `ServiceRuntimeSection` › `ServiceKafkaClientsPanel`.

| Parça | Dosya | Veri | Mockup |
|---|---|---|---|
| Clusters (Thanos) | SIT `CL_COLS` :56-67: Cluster, Namespace, Pods, CPU, Memory, Restarts, Durum, Eylemler; storageKey `svc-infra-clusters`; seçim `?icluster=`; satır altı `ServicePodInventory cluster=` (:403-409) | `useServicePods` (`/api/clusters/sources`, `deployments`, `pods`) | "Nerede çalışıyor" tablosunun gövdesi |
| "Yanıt vermedi" | SIT:340-345 başlık rozetleri; `clusterStatus` bilinmiyorsa gray "durum bilinmiyor" (`infraClusters.ts:83-87`) | — | Satır içinde yazı + üst cümlede sayı |
| 4 KPI karosu | SIT:421-455 `StatTile` | aynı | KALKAR → tek cümle + "Dikkat isteyenler" |
| Kaynak kullanımı | SIT:461-494 `MetricArea` ×2 | `/api/clusters/deploy-trend` | DEĞİŞMEZ |
| Router/HAProxy | SIT:501-529 `MetricArea` ×3 (`MultiLineChart` varsayılan `maxSeries`) | `/api/clusters/haproxy-trend` | bant + ortanca + en yüksek |
| Diğer cluster'lardaki pod'lar | `ServicePodInventory.tsx`: SegmentedControl ×2 (:197-209, `?psrc=all|entity|thanos`, `?pview=cluster|flat`, `podsMerge.ts:110-121`), `ChainStrip` "runs in:" (:51-81), rozetler (:210-230); `ServicePodsTable.tsx` `POD_COLS` (SPI:34-49, 15 kolon) storageKey `service-pods-v2`, E/T/E+T (SPT:42-46, :202), `?jpod=` | `/api/services/:svc/pods` (entity) + Thanos birleşimi `mergePods` | Clusters tablosuna BİRLEŞİR; kip/şerit/rozet kalkar |
| Pod → JVM | `PodJmxInline.tsx` (fetch-on-expand, `/api/clusters/jmx-metrics`, `jmx-trend`) | — | KORUNUR |
| Runtime | `RuntimeCharts.tsx` (`FANOUT_MAX=40` :87; `mode ?? 'area'` :332; limit = `ref:true` TSSeries, `dash:[5,4]` :262; istemcide `slice(0,40)` :248) + `HeapBaselineCard.tsx` (`LINES_MAX=12`, `/api/services/:svc/heap-baseline`) | `api.metricQuery` çizgi başına, tüm pod serileri istemciye gelir | heap: % sınır, bant + ortanca + en yüksek, kesik limit, "sınıra en yakın 5 pod"; non-heap düzse cümle |
| Kafka client | `ServiceKafkaClientsPanel.tsx` | `/api/services/:svc/kafka-clients` | DEĞİŞMEZ |

## 2. Bağlayıcı kararlar — mockup ile çakışma noktaları

| # | Karar (kod) | Mockup | Çakışma | Öneri |
|---|---|---|---|---|
| K1 | `Service.tsx:611` v0.9.63: RED üçlüsü sekme tepesinde YOK, Details/Performance'ta yaşar | Performance KAPALI satır | Yok (yer aynı, yalnız kapalı) | Uygula; §7 S1 |
| K2 | `Service.tsx:654` v0.10.151: Clusters + Database EN ÜSTTE | İkisi açık, üstte | Yok | Uygula |
| K3 | `Service.tsx:753` v0.9.282: SLO şeridi içeriğin ALTINDA, her sekmede | Mockup değinmez | Yok | Dokunulmaz |
| K4 | `ServiceClusterBreakdown.tsx:98` v0.9.142: yalnız trace'i gelen cluster'lar; metrik-yalnız Thanos cluster'ları Infra'da; başlık/tooltip açıklar | Kaynak rozeti başlıktan kalkar | Açıklama kaybolmasın | Açıklama bölüm altındaki "kaynak" satırına iner |
| K5 | `DetailsPropsStrip.tsx:45` v0.10.1257: Envs/Clusters çıktı; Java sürümü, image, son deploy | Düz etiket-değer | Yok | İçerik aynı, yalnız yerleşim |
| K6 | `ServicePodInventory.tsx:29` v0.10.720: yapışkan şerit YOK; `?psrc`/`?pview` URL kaynak | Kip düğmeleri + `runs in:` kalkar | **Çakışır**: 720 bu kipleri ekledi | Kipler kalkar, "Nerede çalışıyor" tek görünüm = cluster'a göre gruplu; `?psrc` sunucu tarafı veri kaybı değil (birleşim zaten `both`) → §7 S3 |
| K7 | `ServicePodInventory.tsx:83` v0.10.1206: Pods sekmesi kalktı, cluster satırı altında inline | Cluster'a basınca pod'lar | Yok | Korunur |
| K8 | `ServicePodsTable.tsx:178` v0.10.1210 ve `ServiceInfraTab.tsx:367` v0.10.1224: ada/satıra basınca açılır, ↗ ayrı | Aynı | Yok | Korunur, test S0 |
| K9 | `PodJmxInline.tsx:18,66`: başlık keşfedilen metrikten; JMX yoksa CPU/Mem | Pod'a basınca CPU/bellek + JVM | Yok | Korunur |
| K10 | `ServiceInfraTab.tsx:531` v0.9.574: PromQL kartı kaldırıldı | — | Yok | Geri gelmez |
| K11 | `RuntimeCharts.tsx:71` v0.9.532: cluster öneki yalnız çok cluster'da; `groupKey[0..2]` sabit | bant+ortanca; "en yüksek" adı | Etiket sözleşmesi (`podLineLabel`) korunmalı | "En yüksek" etiketi `podLineLabel` ile basılır |
| K12 | **`RuntimeCharts.tsx:82` v0.9.95: "sadece 8 pod gösteriyor" → tavan 40, pod başına çizgi + interaktif lejand** | Seri başına çizgi yerine bant + ortanca + en yüksek | **DOĞRUDAN ÇAKIŞIR**: operatör pod'ları tek tek görmek istemişti | Varsayılan bant; "Pod başına çizgiler" düğmesi eski görünümü (40 tavanlı) açar; "sınıra en yakın 5 pod" tablosu "hangi pod" sorusuna cevap verir → **§7 S4 onay şart** |
| K13 | `RuntimeCharts.tsx:235` v0.9.127: yumuşatma, limit sabit | Bant | Yok | Bant ham veriden, ortanca yumuşatılmış; limit SMA'sız |
| K14 | `bandsParam.ts:1` v0.10.170: anomali bantları VARSAYILAN KAPALI (`?bands=1`) | "Aralık bandı" | Ad çakışması, kavram farklı | Yeni şeye "zarf" (envelope) de; `?bands` ile ilişkilendirilmez |
| K15 | memory: "Ayarda vida değil varsayılan", "Detay sayfaları sade" | Uyumlu | — | Aç/kapa durumu tek localStorage anahtarı, ayar sayfası yok |

## 3. Kapalı bölümlerin tek cümle özetleri — veri nereden

Kural: **kapalı bölüm kendi verisini AÇILANA KADAR çekmez** (`enabled: open` / `useEffect` erken dönüş; `ServiceLatencyHeatmap.tsx:112` emsal). Özet cümlesi yalnız **zaten sayfada olan** veriden türetilir; yoksa sayısız statik cümle. Yeni CH/ES/VM sorgusu YOK.

| Bölüm | Özet cümlesi | Kaynak | Hesap |
|---|---|---|---|
| Endpoints | "HTTP route'ları: çağrı payı, hata, p99." (sayısız) | Zaten yüklü veri yok (`/api/endpoints` bölümün kendi verisi) | Statik. Sayı istenirse (`N endpoint`) yalnız açıldıktan sonra başlıkta kalır. §7 S2 |
| Performance | "Son {range}: {rate}/sn, %{err} hata, p99 {p99}." | Sayfa başındaki KPI (Service.tsx üst KPI'ları, aynı pencere) | İstemci, prop ile geçir |
| Metrikler | "{N} panel hazır ({aile adları})." | `useDetailsMetricPanels` kataloğu zaten sekme kapılı yükleniyor (`/api/metrics/names`, küçük) | İstemci; panel sorguları (`useQueries`) `enabled: open` |
| Gecikme dağılımı | "p50 {p50}, p99 {p99}; dağılım grafiği kapalı." | Üst KPI p50/p99 | İstemci. Mevcut `svc.heatmap.collapsed` anahtarı yeni ortak anahtara taşınır (eski anahtar okunmaz; dolgu yok) |
| Dağıtımlar | "Son 7 günde {N} rollout, sonuncusu {X} önce." | `useServiceRollouts7d` zaten `DetailsPropsStrip` tarafından çekiliyor (aynı React Query anahtarı → ek istek yok) | İstemci; `copilotConfig` probu `enabled: open` |
| Infra üst cümle | "{pods} pod, {clusters} cluster'da çalışıyor. {ok} normal, {unreach} cluster'dan durum alınamıyor." | `useServicePods` + `useEntityServicePods` (zaten mount'ta) | İstemci, saf fonksiyon `infraSummary.ts` (test edilir) |
| Dikkat isteyenler | yalnız sapanlar: restart>0 / OOM / not-running pod, yanıt vermeyen cluster, Thanos'ta görünmeyen entity pod'u | aynı yanıtlar | İstemci, saf fonksiyon; boşsa kutu çizilmez |
| Infra "kaynak" satırı (sayfa altı, kapalı) | E/T/E+T, unmapped, topk(500) kısmi, "entity katmanı okunamadı" | aynı | Bugünkü rozet mantığı buraya taşınır (silinmez) |

Performans kazancı (açılışta): Endpoints 1-N istek, Performance 3-4 istek, Metrikler panel×metrik istek, Deploy copilot probu → **sıfır**. Clusters, Database, Props, Infra pods aynı kalır.

## 4. "Bant + ortanca + en yüksek" grafiği

Mevcut: uPlot `bands` tek yerde (`components/viz/TimeSeriesPanel.tsx:417-420`, yalnız `stacked`); `OverviewChart.tsx:307` aynı. Kesik referans çizgisi var (`TSSeries.dash`, `RuntimeCharts.tsx:262`). `thresholds` yatay çizgi + tint (`lib/chart/thresholdLines.ts`), min-max zarfı değil. Yüzde ekseni: `unit` ile; 0-100 sabitleme yok (`yRange.ts` yalnız headroom).

Öneri — istemci hesabı, saf yardımcı `lib/chart/seriesEnvelope.ts`:
- Girdi: hizalanmış seriler (`alignSeries.ts`), çıktı: `{ lo, hi, median, top: {label, points}, n }`. 40 pod × ~300 nokta → µs düzeyi; sunucu gereksiz. VM'de `quantile(0.5,…)`/`max()` yazılabilir ama "en yüksek serinin ADI" için ikinci sorgu (`topk(1)`) gerekir ve yalnız VM yoluna bağlanır; HAProxy (Thanos) için ayrı. İstemci tek yol.
- Çizim: `TimeSeriesPanel`'e ADDİTİF `envelope?: { lo: TSSeries; hi: TSSeries; fill: string }` prop'u → uPlot `bands: [{ series: [hiIdx, loIdx], fill }]`, lo/hi `width: 0`. Ortanca normal çizgi; "en yüksek" ikinci çizgi, etiketi `podLineLabel` (K11). Limit: mevcut `ref:true` + `dash` yolu, yüzde kipinde sabit 100 çizgisi.
- Yüzde: `RuntimeCharts` heap kartı için `value / limit × 100` istemcide (limit serisi zaten çekiliyor, :96/:103); `unit:'percent'`, `yRange` 0..max(105, hi). Limit yoksa (K8s olmayan JVM) mutlak değer, eski davranış.
- 40 tavanı: zarf **tüm** pod serilerinden hesaplanır (fetch zaten tümünü getiriyor, :248 kesim yalnız çizimde) → tavan zarf kipinde anlamsız; "Pod başına çizgiler" kipinde 40 kalır. Not "N / M pod" yalnız çizgi kipinde.
- "Sınıra en yakın 5 pod": `HeapBaselineCard` zaten pod başına %limit hesaplıyor (`heap-baseline` yanıtı) → kartın tablosu 5 satıra iner (statik `<table>`, ≤10 sabit satır, gerekçe yorumla), `LINES_MAX` çizgileri zarf kipinde çizilmez.
- Non-heap "düz" tanımı: (max-min)/median < %10 pencere boyunca → cümle; aksi grafik. Eşik sabit, ayar değil (K15).

## 5. Dilimler (her biri tek commit, ayrı v0.10.X; `scripts/gate.sh all` + `make audit` + golangci-lint gerekmez — Go yok)

Kurallar: dört tablo türü (DECISIONS "Tablo standardı"); `components/ui` DONUK — yeni primitive `components/coremetry/`; dokunulan dosyada inline style sıfır (Tailwind/tokens); emoji yok; yapışkan şerit yok (ToC kalkar); uPlot dışı grafik yok; URL = seçim kaynağı, `replace:true`, sig-guard.

- **S0 Karakterizasyon testleri** (yalnız test). **GEMİDE v0.10.2131** — `Service.detailsFetchGates.test.tsx` bileşen MONTE edilince okuduğu uçları pinler (açık yarı; S1 kapalı = monte yok, ayrı testte); `detailsSections.pin` kimlik/sıra/açık bölümler; `ServiceInfraTab.pin` K7/K8 + `?icluster`/`?jpod`; `runtimeCharts.limitRef`. Dosyalar: `pages/service/detailsSections.pin.test.ts` genişletme, yeni `Service.detailsFetchGates.test.tsx` (hangi bölüm mount'ta hangi endpoint'i çağırır — bugünkü hâli PİNLER, sonraki dilimler "çağırmaz"a çevirir), `ServiceInfraTab.pin.test.ts` (+K8 satır tıkı, `?icluster`, `?jpod`), `podsMerge.test.ts` (mevcut), yeni `runtimeCharts.limitRef.test.ts` (limit `dash`, 40 kesim, `podLineLabel`), `heapBaselineCard.pin.test.ts` (mevcut). Risk: yok.
- **S1 `CollapsedSection` composite + Details beş bölüm kapalı.** **GEMİDE v0.10.2132** — ana oturum kararları: aç/kapa tarayıcı başına `svc.details.open` (servisten bağımsız, `lib/storage` try/catch); özet yalnız yüklü veriden (Endpoints sabit; Gecikme "ortalama / p99" — sayfada p50 yok); çapa (`#deploys`, `#dtl-*`) kapalı bölümü bu oturumluk açar. Kapalı = gövde monte edilmez (Radix içerik), `enabled: open` ayrı bayrak gerekmedi. Yeni `components/coremetry/CollapsedSection.tsx` (shadcn `collapsible`, props: id, title, summary, source, open, onOpenChange); tek localStorage anahtarı `svc.details.open` (JSON set, servisten bağımsız); `Service.tsx:625-747` sarmal; `SectionHead source` → bölüm altı "kaynak" satırı; bölüm verisi `enabled: open` (`DetailsEndpointsSection`, `ServiceCharts` fetchRed, `DetailsMetricsSection` useQueries, `DeployHistoryPanel`, Heatmap mevcut). Görünen: 5 satır kapalı, özetler §3. Risk: Heatmap eski anahtarı unutulur (kabul; dolgu yok). Test: S0 kapıları "çağırmaz"a döner; özet cümle saf fonksiyon testi `detailsSummaries.test.ts`.
- **S2 ToC kalkar + Properties düz satır.** **GEMİDE v0.10.2133** — `DetailsToc.tsx` + `.dtl-toc` CSS + Details flex sarmalı silindi; Properties `coremetry/KeyValue` (çerçevesiz, satır içi stil yok); `#deploys` bağlantısı adres zaten #deploys iken de kapalı bölümü açar. `DetailsToc.tsx` silinir, `Service.tsx:745`, `globals.css` `dtl-toc` kuralı; `DetailsPropsStrip.tsx` etiket-değer (`KeyValue` primitive'i), inline style sıfır. Bölüm id'leri (`dtl-*`) KALIR (derin link / CoSRE product_guide). Test: pin testinde ToC girdisi kaldırılır, id'ler pinlenir.
- **S3 Clusters 5 kolon + "tüm kolonlar".** `ServiceClusterBreakdown.tsx` COLS'a `columnModel` (`lib/columnModel` `hidden`; `useDataTable` :307 mevcut) — varsayılan gizli: Trend, Avg, P50, P95; `Pods` yalnız Thanos varsa; düğme "Tüm kolonlar" `columnModel.onChange` → localStorage. İlk 5 satır + "{N} daha, hepsi normal · hepsini göster" (normal = err% ve p99 ortanca×1.5 altında; saf fonksiyon). Test: `serviceClusterBreakdown.pin.test.ts` + kolon seti.
- **S4 Database 6 kolon.** `DBQueriesPanel.tsx` (Details'e ait tek sayfa mı doğrula: `grep DBQueriesPanel` başka çağıran varsa prop ile) — görünür: Statement, DB, xN, Total(+pay), P99, Errors; gizli: Avg, P95, Max, Detail/Traces eylemleri satır menüsüne. "Pay" = `totalMs / Σ totalMs` istemcide. `DisclosureButton` yerine S1 composite. Test: yeni `dbQueriesPanel.cols.test.tsx`.
- **S5 Infra üst cümle + Dikkat kutusu, KPI karoları kalkar.** `ServiceInfraTab.tsx:421-455` → `infraSummary.ts` (saf) + küçük bileşen. CPU/Mem karosu grafiğe kaydırma davranışı: cümlede link. Test: `infraSummary.test.ts` (6 durum: tek cluster, yanıt vermeyen, Thanos yok, entity yok…).
- **S6 "Nerede çalışıyor" birleşik tablo.** `ServiceInfraTab.tsx` `CL_COLS` → Cluster, Pod, Çağrı?, Hata?, p99?, CPU, Bellek, Durum (gizli: Namespace, Restarts→Durum'a katılır); entity-yalnız cluster'lar satır olur ("Thanos'ta görünmüyor" yazısı, `useEntityServicePods` zaten var); yanıt vermeyen cluster satırı "durum alınamıyor" hücresi. `ServicePodInventory` yalnız `cluster=` kipinde kalır: SegmentedControl, `ChainStrip`, başlık rozetleri → `InfraSourceNote` (sayfa altı kapalı satır). `parsePodSource/parsePodView/filterBySource` silinir; `?psrc`/`?pview` eski linklerde yok sayılır (react-router okumaz; DOLGU YOK). `?icluster`, `?jpod`, `?cluster` kalır. `POD_COLS`: Node, Workload, Net in/out, OOM `columnModel.hidden`. Risk: en büyük dilim; `ServicePodInventory.tableStates.test.tsx` (15) ve `ServiceInfraTab.tableStates.test.tsx` (12) yeniden yazılır. **Onay: §7 S3.**
- **S7 `seriesEnvelope` + `TimeSeriesPanel.envelope`.** Yalnız kütüphane + test; hiçbir sayfa kullanmaz. `lib/chart/seriesEnvelope.ts(.test.ts)`, `components/viz/TimeSeriesPanel.tsx` additif prop. Risk: sıfır (kullanıcı yok).
- **S8 Router/HAProxy üçlüsü zarf kipi.** `MetricArea.tsx` → `envelope` prop geçişi (yalnız seri sayısı > 3 ise; aksi eski çizgiler). Test: render testi seri sayısı eşiği.
- **S9 JVM heap zarf + % sınır + "sınıra en yakın 5".** `RuntimeCharts.tsx` heap kartı; `?rtview=lines` eski görünüm (K12); `HeapBaselineCard` 5 satır. **Onay: §7 S4.** Test: `runtimeCharts.limitRef.test.ts` güncellenir, `seriesEnvelope` yüzde testi.
- **S10 Non-heap düz → cümle.** `RuntimeCharts.tsx` non-heap kartı; "Grafiği göster" `?rtview`. Test: düzlük fonksiyonu.
- **S11 Temizlik.** Dokunulan dosyalarda kalan `style={{`, ölü CSS (`globals.css` sonuna kural EKLENMEZ), `docs/DECISIONS.md` girişi, `large-files.md` güncelleme gerekirse.

Sıra gerekçesi: S1-S2 en görünür/en ucuz; S6 ve S9 onay bekler, S7 bağımsız olduğundan onay beklerken yapılabilir.

## 6. Kapsam dışı ve doğrulanamayanlar
- Kafka client paneli, Kaynak kullanımı grafikleri, SLO şeridi, Overview/Operations/Logs sekmeleri, Pod sayfası, backend (sıfır Go değişikliği; yeni endpoint yok).
- Tarayıcı yok, Playwright yasak: yerleşim, zarf dolgusunun renk/alfa görünümü, sticky ToC kaldırıldıktan sonra >=1100 px boşluk dağılımı yalnız prod'da operatör gözüyle doğrulanır.
- "0-8 panel" üst sınırı `detailsMetricPanels.ts`'te doğrulanmadı; `useServicePods.ts` `/api/clusters/pods` `enabled` kapısı okunmadı (S0'da pinlenir).
- `DBQueriesPanel`'in Service.tsx dışında çağıranı olup olmadığı S4 başında grep'lenir.
- Performans kazancı (istek sayısı) kodla gösterilir, ms ölçülmez.

## 7. Operatöre sorular (her biri öneriyle)
1. **(S1) Aç/kapa hafızası**: kapalı bölümü açınca sonraki ziyarette açık mı kalsın? Öneri: evet, tarayıcı başına (localStorage), servisten bağımsız; ayar sayfası yok.
2. **(S1) Endpoints kapalı cümlesinde sayı**: "N endpoint" için açılmadan bir istek gerekir. Öneri: sayısız statik cümle, açıldıktan sonra sayı başlıkta kalır.
3. **(S6) `?psrc`/`?pview` ve kip düğmeleri**: v0.10.720'de siz istemiştiniz. Öneri: kaldır; "yalnız Thanos / yalnız entity" ayrımı sayfa altındaki kapalı "kaynak" satırında metin olarak kalır, kaynak rozeti (E/T) "tüm kolonlar"da bir kolon olur.
4. **(S9) JVM heap pod başına çizgi**: v0.9.95'te "sadece 8 pod gösteriyor" demiştiniz. Öneri: varsayılan zarf (bant + ortanca + en yüksek pod adıyla) + "sınıra en yakın 5 pod" tablosu; "Pod başına çizgiler" düğmesi eski 40'lı görünümü açar.
5. **(S3/S4) "Hepsi normal" eşiği**: gizlenen satırlar için normal = hata oranı ve p99 ortancanın 1.5 katı altında. Öneri: bu sabit, ayar değil; "hepsini göster" her zaman var.
6. **(S6) Birleşik tabloda trace metrikleri (Çağrı/Hata/p99)**: Details Clusters'ta zaten var; Infra tablosuna da gelsin mi? Öneri: gelmesin, Infra = kaynak/durum; Details Clusters = trafik (K2/K4 ayrımı korunur).
7. **(S8) HAProxy zarf eşiği**: 3 ve altı seri eski çizgiler. Öneri: 3.
