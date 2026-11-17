# CoSRE kanıt gösteren SRE — Faz 0 denetimi (2026-11-18)

Audit-first belge; kod değişmedi. Okunan ağaç `v0.10.2323`. Kısaltma: `api/` = `internal/api/`, `cop/` = `internal/copilot/`, `uistream/` = `internal/cosre/uistream/`, `mcp/` = `internal/mcptools/`, `fe/` = `frontend/src/`; `chat.go` = `api/copilot_chat.go`. Girdi: statik okumadan çıkmış bir bilgi föyü; tasarımın dayandığı iddialar bu ağaçta yeniden okundu, föyden sapan her madde **[DÜZELTME]** ile işaretli (toplu liste §8). Token varsayımı konuşma denetimiyle aynı: **3 bayt ≈ 1 token**. Paket boyutları tahmin değil ölçümdür (§4). Örnek adlar nötr: checkout, orders, payments.

## 1. Hüküm

Hedefin taşıyıcı altyapısı büyük ölçüde var: sunucu araç sonucundan deterministik blok kuruyor, bloklar `data-block-<tip>` olarak akıyor, kendiliğinden arşivleniyor ve v2 sohbetinde çiziliyor; blok kırpılmamış sonuçtan kurulup modele kırpılmış metin gidiyor (`chat.go:1620-1639`), yani "parça arayüze, özet modele" ayrımı için dikiş hazır. Eksik olan dört yapısal şey var. (1) Gösterilebilir kanıt alanları dağınık: istisna grubu + örnek trace + yığın tek sonuçta yok, `explain_trace` hatalı span'in kimliğini vermiyor, `endpoint_metrics` çip üretip grafik üretmiyor, v2'ye özel sekiz aracın hiçbiri blok üretmiyor. (2) "Metrik yalnız VictoriaMetrics" bugün doğru değil: varsayılan grafik tarifi ClickHouse span metriğinden çiziliyor, `query_metrics` ClickHouse'a düşebiliyor, pod doygunluğu Thanos'tan geliyor. (3) Kimlik veri katmanında servis adıdır: RED rollup'ı, `exception_groups` ve `ml_events` cluster / namespace taşımıyor; kullanıcı kapsamı 72 aracın 6'sına iniyor ve modelin verdiği değer kazanıyor. (4) Bulgudan bilgiye köprü yok; yüklenen belgeler `search_knowledge`'ın değil wiki indeksinin içinde; döngü isteminde hipotez / kanıt ayrımı yok, tur bazında profil ayrımı yok. Öneri: altı parça yeni üst düzey parça değil blok tipi olsun; grafiğin kataloğu mevcut 11 şablon olsun; kanıtı model değil sunucu eklesin (doğrulayıcının sayı eşleşmesi üzerinden); istem değişikliği konuşma programının C dilimiyle tek sürüm olsun. Ölçüm: dört bileşen tembel eklenince v2 sohbet chunk'ı +0,9 KB gz, ilk yük +0,1 KB (293,5 / 347 KB).

## 2. Mevcut denetimlerle ve uçuştaki konuşma programıyla ilişki

Tekrarlanmayan konular: akış parçaları ve `data-reset` → `chat-ui-audit.md` §0, §3; kademe zinciri, istem boyutu, cevap dikişi → `conversation-audit.md` §3; kaynak çipi, özet kartı → `experience-audit.md` §3; v2 adaptörü, kapsam zinciri → `tools-audit.md` §1, §3; ADO tasarımı → `search-knowledge-audit.md` §3; ölçüm kapısı → `quality-loop-audit.md` §3.

| Belge, bölüm | Bu işle ilişki | Tür |
|---|---|---|
| `tools-audit.md` §10 Q5 (onaylı): "tek `data-tool-result`, `kind` içeride; parça adı kümesi küçük ve kapalı kalsın" | Altı yeni üst düzey parça adı bu kararın tersidir; blok tipi önerisi kararla uyumlu (§5.1) | **çatışma** (§7-1) |
| `tools-audit.md` §3, Q1–Q2 (kapsam = varsayılan daraltma, kaçış var, sessiz genişleme yok) | "Her araçta kapsam" bu anlamı genişletir; yetki sınırı değildir (rol modeli admin / editor / viewer) | genişletme (§5.5) |
| `tools-audit.md` §2 (v2'de serbest PromQL / SQL yok) | `data-chart` kataloğu aynı ilkenin grafik tarafı | uyumlu |
| `chat-ui-audit.md` §4 (aile başına kanıt kartı, `components/cosre/evidence/`) | K6 çizicileri aynı dizine iner; v1 `ChatBubble`'a eklenmez | yeniden kullanım |
| `experience-audit.md` §3.3 (özet kartı sunucuda deterministik) | Kanıt ekleme kararı da sunucuda (§5.4) | uyumlu |
| `experience-audit.md` §1 "Kaynak çipi" (`source-url`: başlık + bağlantı) | `data-source` bölüm ve skor ister; mevcut parça bunları düşürür | boşluk (§5.7) |
| `search-knowledge-audit.md` §3 (ADO arka uçları; uygulanmadı), operatör kararı "RAG kullanılmayacak" | Bulgudan bilgi araması bunlara yaslanmaz; ADO gelirse aynı arayüzün arkasına takılır | sınır |
| `quality-loop-audit.md` karar 5 (hakem kapı değil) | Üç yeni ölçüt hakemsiz ölçülür (§5.4) | uyumlu |
| `roadmap-status.md` satır 11c (görev bazlı model yönlendirme başlamadı, evalset puanını bekliyor) | Profil ayrımı (K5) bu satırın işidir; iki profilli koşu ölçümüdür | örtüşme |
| `conversation-audit.md` §6.0 durum nesnesi + kanıt defteri (A, sürüyor) | Parça özeti ile defter olgusu aynı kayıttır; iki ayrı olgu çıkarıcı kurulmaz | **bağımlılık** (K1, K2 A'yı bekler) |
| `conversation-audit.md` E1 / E2 (sayı ve saat araç çıktısından) | Buradaki "sayı modelden gelmez" kuralının kendisi; kanıt eşleme E1'in eşleşme sonucunu kullanır | aynı kural, tek uygulama |
| `conversation-audit.md` C (v2 istem varyantları, örnek diyaloglar) | Bu programın istem dilimi ayrı sürüm olursa istem iki kez değişir | **birleştirme** (K4b = C'nin içinde) |
| `conversation-audit.md` B (cevap sınıfı) | "Genel soruya grafik yok" sınıfın kanıt tavanıdır | bağımlılık (K4a B'yi bekler) |
| `conversation-audit.md` F1 (ara cümle) | Profil ayrımında araştırma taslağı akmaz; sessizliği F1 kapatır | bağımlılık (K5 F1'i bekler) |
| `conversation-audit.md` H1 (konuşma kipi, gemide) | Üç yeni ölçütün yeri: `api/ai_evalset_conversation.go` `expect` alanları | yeniden kullanım |
| `conversation-audit.md` §7-5 (b) (tek blob `cosre_conversation`) | Bu program yeni anahtar açmaz; aynı bloba tek alan ekler (§7-15) | uyumlu |

Tek sıra §6.2'de.

## 3. Beş Faz 0 cevabı

### 3.1 v2 araç envanteri — Durum: var (72 araç, koşullularla 77); gösterilebilir kanıt kısmi

- Katalog: `mcp.V2ToolList` (`mcp/v2_tools.go:202-214`); altın `mcp/testdata/toolset_catalog_golden.json` `zero/v2` 72, `conditional/v2` 77. Gizli: `search_traces`, `query_metric`, `list_metric_labels` (`mcp/v2_tools.go:111-115`).
- Kanıt zarfı (`cosreEvidence{source, query, queryKind, window, env, cluster, rows, series}`, `mcp/cosre_query.go:83-93`) yalnız dört atomik ve üç bileşik araçta: `query_metrics`, `query_traces`, `query_logs`, `endpoint_metrics` (`mcp/evidence_link.go:83`); `pivot_from_trace`, `service_health_sweep`, `explain_log_spike` (`:90`). Zarfın `source` alanı kapalı kümedir: VictoriaMetrics / ClickHouse / Thanos / Elasticsearch (`mcp/cosre_query.go:43-46`).
- Blok üreten yol: `ToolResultBlock`'un `switch`'inde yalnız `query_traces`, `query_logs`, `query_metrics` var (`mcp/evidence_blocks.go:140`). `endpoint_metrics` zarf taşır ama `case`'i yoktur: çip çıkar, grafik çıkmaz.
- Model araç sonucunu ham JSON olarak, 2.048 baytta kırpılmış görür (`api/ai_loop_budget_settings.go:39`, `:91-96`; `ai.loopBudget.toolTextBytes` daraltabilir).
- Katalog maliyeti **ölçüldü** (atılabilir test, commit edilmedi): 72 aracın ad + sohbet açıklaması + şeması **62.916 B ≈ 20.970 token**, her model çağrısında. Ortalama araç 874 B ≈ 290 token; `render_chart` 1.946 B, `build_link` 3.446 B. Sabit istem çekirdeği 8.991 B ile kıyaslanınca çağrının asıl ağırlığı katalogdur.

Tablo işaretleri: `+` var, `~` kısmi, `–` yok. Sütunlar: **T** trace_id · **Z** zaman penceresi (`+` mutlak baş / son, `~` yalnız aralık ya da başlangıç) · **M** metrik adı + etiket ya da sorgu · **L** log örneği · **X** istisna grubu · **E** `ml_events` kaydı · **K** servis adının ötesinde kimlik (cluster / namespace / pod). Kapsam: `kul` kullanıcı kapsamı enjekte, `ekr` yalnız ekran kapsamı, `–` hiçbiri. İşaretsiz satırlar bu ağaçta okundu; `(f)` föyden alındı, yeniden okunmadı.

| Araç(lar) | Kaynak | T | Z | M | L | X | E | K | Kapsam | Bugün ürettiği |
|---|---|---|---|---|---|---|---|---|---|---|
| `list_my_services` | CH rollup | – | ~ | – | – | – | – | – | kul | `page_url` |
| `open_problems` | CH gelen kutusu | – | ~ | – | – | ~ kimlik | – | – | kul | `page_url` |
| `find_traces` | CH | + | ~ | – | – | – | – | – | kul | `page_url` |
| `explain_trace` | CH span | + | – | – | – | ~ tür + mesaj; span_id yok | – | – | kul (işaret) | `page_url` |
| `search_logs` (v2) | logstore | + | + | – | + ≤10 | – | – | – | kul | `page_url`; blok yok |
| `endpoint_metrics` | **yalnız VM** | – | + | + PromQL | – | – | – | – | kul (tek servis) | zarf → çip; grafik yok |
| `get_log_anomalies` | CH `ml_events` | – | ~ | – | – | – | + | ~ pod | – | `page_url` |
| `explain_ml_event` | CH | + ≤3 | + | – | – | ~ referans | + | – | – | `page_url` |
| `query_traces` | CH | + | + | – | – | – | – | – | ekr | zarf + `trace_list` |
| `query_logs` | logstore | + | + | – | + ≤50 | – | – | + pod | ekr | zarf + `table` |
| `query_metrics` | VM / CH / Thanos | – | + | ~ yalnız VM'de | – | – | – | ~ | ekr | zarf + `chart` (veri) ya da `table` |
| `explain_log_spike` | logstore | + | + | – | + | – | – | – | ekr | bileşik bloklar ≤6 |
| `pivot_from_trace` | CH + Thanos + VM/CH + logstore | + | + | + | + | – | – | + | ekr | bileşik bloklar ≤6 |
| `service_health_sweep` | CH + logstore + k8s | – | + | – | ~ | – | – | + | ekr | bileşik bloklar ≤6 |
| `get_trace`, `get_logs_for_trace` (f) | CH, logstore | + | ~ / + | – | – / + | – | – | – / + | ekr | metin |
| `list_exception_groups` | CH | – | + ilk / son | – | – | + | – | – | – | metin; örnek trace ve yığın yok |
| `get_exception_samples` | CH span | + | + | – | – | + | – | – | – | metin; yığın kırpılmış düz metin |
| `list_anomalies` (f) | CH | – | ~ | – | – | – | ~ doğrulanmadı | – | ekr | metin |
| `get_exemplar_traces`, `get_metrics_for_span` (f) | CH | + / – | + | – | – | – | – | – | ekr | metin |
| `render_chart` | CH (yalnız doğrulama) | – | ~ | ~ agg adı | – | – | – | – | ekr | `chart` (tarif; varsayılan ClickHouse) |
| `get_service_health`, `get_operation_health`, `get_topology`, `get_blast_radius`, `get_db_health`, `get_messaging_health`, `compare_periods`, `list_slo_status` (f) | CH rollup | – | ~ | – | – | – | – | – | ekr | metin |
| `list_services`, `get_team_services`, `list_teams` (f) | CH | – | ~ | – | – | – | – | – | ekr / – | metin |
| `get_pod_health`, `cluster_metric`, `list_metric_names` (f) | VM ya da CH; Thanos; CH ya da VM | – | ~ | ~ ad | – | – | – | + / + / – | ekr / – | metin |
| `log_patterns`, `list_log_fields`, `get_log_histogram` (f) | logstore | – | ~ | – | ~ | – | – | – | ekr | metin |
| `find_trace_by_span`, `find_trace_by_request_id`, `get_linked_traces`, `trace_stats` (f) | CH / logstore | + (ilk üçü) | ~ | – | – | – | – | – | ekr / – | metin |
| `list_problems`, `list_problem_window_events`, `get_problem_root_cause`, `get_problem`, `get_correlation_evidence`, `similar_problems`, `similar_incidents` (f) | CH | ~ | ~ | – | – | – | – | – | ekr | metin |
| `get_correlated_changes`, `get_deploy_diff`, `list_deploys`, `list_deployments` (f) | CH + rollout | – | ~ | – | – | – | – | – | ekr | metin |
| `list_namespaces`, `list_workloads`, `list_pods`, `resolve_entity` | varlık kataloğu | – | – | – | – | – | – | + tam | – | metin |
| `list_environments`, `list_clusters`, `list_operations`, `describe_attributes`, `find_attribute_by_value`, `resolve_business_code` (f) | CH | – | – | – | – | – | – | ~ | – | metin |
| `get_runbook`, `search_knowledge`, `product_guide` | CH runbook, RAG, sabit | – | – | – | – | – | – | – | – | §3.4 |
| `get_capabilities`, `build_link`, `set_context`, `get_context`, `clear_context` | meta | – | – | – | – | – | – | – | – | link / durum |

Hiçbir araçta bulunmayanlar: istisna grubu + örnek trace_id + ilk yığın çerçeveleri tek kayıtta; hatalı span'in `span_id`'si (`mcp/v2_traces_logs.go:666-670`); bir grafik parçasının doğrudan alabileceği "katalog kimliği + kimlik + mutlak pencere" üçlüsü; `ml_events` satırında bitiş anı (liste satırı yalnız `started` verir, `mcp/v2_ml.go:69-71`; `last_seen` yalnız `explain_ml_event`'te, `:176`).

### 3.2 `data-*` parça desteği — Durum: var; blok yolu hazır, üst düzey parça yolu elle

- Bloklar: 13 tip (`internal/ai/agent/blocks/block.go:17-45`, `fe/lib/cosre/protocol.ts:34-37`). Çevirici tipi bilmez: `"block"` olayı `data-block-<tip>` olur (`uistream/translator.go:58`, `:294-301`). Yeni blok tipi çeviricide değişiklik istemez; `chat_turn_blocks.go` hepsini değerle arşivler (tavan 256 KiB, aşarsa seriler 60 noktaya iner, `:44`, `:49`) ve geri yüklemede `data-block-*` olarak döner. İstemci bilmediği tipi atlar, çökmez (`fe/lib/cosre/toChatTurn.ts:141-143`).
- Grafik bloğunun iki biçimi var ve ikisi de `chart` adını taşır. (a) **Sorgu tarifi** `{service, agg, rangeS, operation?, groupBy?, compare?, source?, fromNs/toNs?}`: `render_chart` sonucundan (`chat.go:1499-1506`) ya da rota incelemesinden (`:1144`) çıkar; veriyi istemci çeker, varsayılan kaynak ClickHouse span metriğidir, yalnız `source:'metric'` VM'e gider (`fe/components/CosreChart.tsx:60-76`). (b) **Veri taşıyan** `EvChartPayload{title, evidenceRef, deepLink, window, series[], regions[], truncated}` (`mcp/evidence_blocks.go:82-90`): sunucu araç sonucundan kurar, ≤20 seri × ≤120 nokta; `EvidenceChart` tembel `CorePanelMulti` ile çizer ve bölge işaretini destekler (`fe/components/ai/EvidenceChips.tsx:143-156`). Model hiçbir biçimde seri ya da çit üretmez; ürettiği çit silinir (`stripModelChartFences`, `chat.go:958`).
- Yeni **üst düzey** `data-*` parça şunları ister: `KnownKinds` (`uistream/part.go:10-22`), `Translate`'te `case`, `v2OnlyEventKinds` (`api/chat_stop_event.go:56`), `protocol.ts` `DATA_PART_NAMES` + ayrıştırıcı, `toChatTurn` eşlemesi, ayrı bir arşiv yolu ve geri yükleme (bloklarınki ona uygulanmaz).
- Kaynak parçaları: `source-url` / `source-document` yalnız `answer.sources`'tan çıkar (`uistream/translator.go:244-246`, `:277-292`) ve yalnız `sourceId`, `url`, `title` taşır; istemci skoru 0 yazar (`fe/lib/cosre/toChatTurn.ts:162-165`). Serbest döngünün `answer` gövdesinde `sources` yoktur (`api/chat_tool_links.go:232-238`).
- Altın dosyalar: `uistream/golden_test.go:60` listesindeki 11 çift ve `api/testdata/chat_stream/` beşlisi. Blok tipi eklemek mevcut dosyaların baytını değiştirmez; yalnız yeni blok üreten senaryonun dosyası eklenir ya da yenilenir. `"block"` olayı v1 yazıcısından da geçer; v1 baytı değişmesin diye yeni bloklar yalnız v2 planı varken yayınlanmalıdır.

### 3.3 Sohbette yeniden kullanılabilir bileşenler — Durum: kısmi (grafik ve trace listesi var; şelale, log tablosu, istisna satırı, ML kartı sohbette yok)

- Bugün v2 sohbetinde çizilenler: `CosreChart` (tarif), `EvidenceChart` ve kanıt tabloları (veri), `ChatTraceList`, `LogSpikeCard`, `MetricPanelCard`, `ChartsSignalsCard`, `ChatTopologyCard`, `OperationCard`, `ServiceReportCard`; hepsi v1 `ChatBubble` üzerinden (`fe/components/cosre/conversation/MessageList.tsx:30`).
- Sohbette olmayanlar, hepsi veriyi dışarıdan alır: `TraceWaterfall` (`fe/components/TraceWaterfall.tsx:201`; `spans`, `selectedId`, `criticalPathIds`, `revealSpanId`), `LogTable` (`fe/components/LogTable.tsx:200`), `ExceptionSampleRow` (`fe/features/anomalies/ExceptionSampleRow.tsx:78`), `MLRecordCard` (`fe/components/coremetry/anomalies/MLRecordCard.tsx:80`; grafiği tembel `TimeSeriesPanel`, `MLRecordChart.tsx:18`).
- **[DÜZELTME]** `ChatBubble` v2 chunk'ının içinde değil, v1 ile paylaşılan ayrı chunk'tır; `EvidenceChips` ile `ChatTraceList` başka bir paylaşılan chunk'ta (`oracleGroup`); v2 sohbeti v1 `CopilotChat` chunk'ını statik olarak çeker (§4).

### 3.4 `search_knowledge` ve doküman araması — Durum: kısmi; bulgudan sorgu türeten kod yok

- `search_knowledge` (`mcp/knowledge_tools.go:328-378`): girdi `{query, source: all|runbook|document, limit ≤10}`; çıktı `{source, title, snippet ≤500, href, id, score}`. Runbook'lar Go'da sözcük puanlanır; `document` `TopKRagChunksByContent`'e gider. **[DÜZELTME]** Kaynak kod olarak atıl değil, veri olarak atıl: operatör kararıyla RAG kullanılmadığı için dönen satır olmaz.
- Asıl bulgu: yüklenen belgeler RAG'de değil **wiki indeksinde** ayrı bir kaynaktır (`internal/wiki/localdocs.go:3-13`), kurum wiki'siyle aynı motorla aranır. Yani "wiki / yüklenen belge" için tek arka uç `wiki.Service.SearchWith`'tir (`internal/wiki/search.go:156-168`); `search_knowledge` yüklenen belgeleri hiç görmez, `search_wiki` görür.
- `search_wiki` satırı `{title, project, wiki, path, url, heading, snippet ≤400, score, live}` (`mcp/wiki_tools.go:67-75`): eşleşen bölüm ve skor araç çıktısında var, ama sohbete yalnız link çipi olarak iner. Wiki araçları yalnız oturum kullanıcısına ve yalnız dış MCP aracı yapılandırılmamışken sunulur (`chat.go:845-852`); katalogda wiki ya da kod aracı varken alışveriş yerel profile sabitlenir (`chat.go:857-860`).
- `get_runbook` Problem kuralı ya da servisten runbook türetir (deterministik, ama metin sorgusu değil).
- Bulgudan sorgu türetme: **yok**. `SearchWith`'in bütün çağıranları kullanıcının sorusunu ya da sayfa başlığını geçirir (`api/chat_wiki.go:118`, `chat_wiki_tier.go:154`, `chat_wiki_probe.go:229`, `chat_wiki_followup.go:372`, `:603`). İstisna türü, mesaj şablonu, operasyon ya da hata kodundan sorgu kuran kod bulunamadı.

### 3.5 Sistem istemi ve inceleme döngüsü — Durum: kısmi; kanıt disiplini kural olarak var, hipotez / kanıt ayrımı yok

- Döngü çekirdeği ÇÖZ → KAPSAMLA → ATTRIBUTE KEŞFET → SORGULA → CEVAPLA'dır (`cop/prompts.go:2916-2940`); "hipotez" sözcüğü döngü isteminde geçmez, yalnız RCA hakemi ve guided kök neden istemlerinde geçer (`:380-427`, `:1206-1237`).
- Tahmin ile veri arasındaki ayrım bugün iki kuralla ve hedefin tersine kurulu: "Her olgusal iddia … bir tool çıktısına dayanmalı; kaynağını göster … UYDURMA" (`:2299-2301`) genel bilgiden konuşmayı da kapatır; "Emin değilsen güven seviyeni ve nedenini belirt" (`:2326`) doğal sözcük yerine etiket ister.
- Ne zaman grafik: "Operatör grafik GÖRMEK isterse … render_chart çağır" (`:2321-2323`). Trace göstermeye dair kural yok. İstem v2'de gizli olan `search_traces`'ı adıyla anıyor (`:2930`) **[föye ek]**.
- Sabit pivot sırası istemde yok; yalnız `pivot_from_trace` kendi içinde sabit zincir yürütür.
- Sayı denetimi (`ungroundedNumbers`, `api/numeric_claims.go:66`) canlıda yalnız trace takibinde; **[DÜZELTME]** bu ağaçta konuşma kipi evalset'i de çağırıyor (`api/ai_evalset_conversation.go:504`).
- Profil: bir alışverişin bütün döngü çağrıları tek profildedir. Çözüm sırası açık profil > yüzey haritası > grup kardeşi > varsayılan (`cop/profiles.go:316-343`). `stepPlan.Ctx` tur başına bağlam seçeneği koyabilir (`api/chat_prepare_step.go:46-54`), ama hiçbir adım `WithProfile` çağırmaz (çağıranlar: `chat.go:350`, `api/chat_deep.go:183`, evalset). Profilde bağlam penceresi alanı yok.

## 4. Ölçülen paket tablosu

Yöntem `bundle:check` ile aynı: `vite build` + `scripts/bundle-budget.mjs`; gz = Node `zlib.gzipSync` varsayılan seviye. Modül → chunk eşlemesi üretilen `.js.map` dosyalarının `sources[]` alanından; "statik kapanış" chunk'ın `from "./x.js"` zincirinden, `index.html` ön yüklemeleri çıkarılarak hesaplandı. Deney: `MessageList.tsx`'e dört `lazy()` ithalatı (`TraceWaterfall`, `LogTable`, `ExceptionSampleRow`, `MLRecordCard`) ve parça tipine göre bunları çizen küçük bir bileşen eklendi, yeniden derlendi, sonra tamamen geri alındı.

```
cd frontend && npm run bundle:check                  # taban
cp -R dist <geçici>/dist-base
# deney düzenlemesi: src/components/cosre/conversation/MessageList.tsx
npm run bundle:check                                 # deney
node <geçici>/diff.mjs <geçici>/dist-base dist       # chunk başına gzipSync + sourcemap sources[]
git checkout -- frontend && git status --short       # boş çıktı
```

| Ölçüm (KB gz) | Taban | Deney | Fark |
|---|---|---|---|
| İlk yük (`bundle:check`, bütçe 347) | 293,4 | 293,5 | +0,1 |
| Ana giriş chunk'ı `index` | 82,1 | 82,2 | +0,1 |
| v2 sohbet chunk'ı `CosreChatV2` | 26,1 | 27,0 | +0,9 |
| v2 sohbetin ilk yük dışı statik kapanışı (63 chunk) | 267,7 | 268,6 | +0,9 |
| Toplam JS (332 → 336 chunk) | 1.979,1 | 1.982,5 | +3,4 |
| `TraceWaterfall` chunk'ı | 7,1 | 7,2 | +0,1 |
| `LogTable` chunk'ı | 6,1 | 6,1 | 0 |
| `ExceptionSampleRow`'un bulunduğu chunk | `spread` 33,0 | kendi chunk'ı 1,0; `spread` 32,6 | ayrıldı |
| `MLRecordCard` + `MLRecordChart`'ın bulunduğu chunk | `MLReplayPage` 15,5 | `MLRecordCard` 4,4 + `mlRecordText` 3,2 + `mlDetectorLabels` 0,6; `MLReplayPage` 12,4 | ayrıldı |
| Küçülen diğer paylaşılan chunk'lar | `unifiedRows` 11,1; `problemGraph` 3,7 | 8,2; 3,3 | −2,8; −0,5 |

v2 sohbet kapanışının büyük kalemleri (taban): `radix` 43,7 · `zod` 38,6 · `CopilotChat` (v1) 34,5 · `ChatBubble` 26,2 · `CosreChatV2` 26,1 · `FocusedNeighborhood` 17,3 · `ai` 16,5 · `oracleGroup` (`EvidenceChips`, `ChatTraceList`) 12,5 · `Markdown` 7,2.

İlk kullanımda inen ek yük (ilk yük ve sohbet kapanışı dışında kalan chunk'lar):

| Kart | Ek indirme (KB gz) | İçinde |
|---|---|---|
| Grafik, `corePanelEntry` yolu (`EvidenceChart`, `CosreChart`) | 185,3 (7 chunk) | `grafana` 149,5 · `charts` (uPlot) 21,4 · `corePanelEntry` 8,7 · `chartTooltip` 3,8 · üç küçük |
| Grafik, `TimeSeriesPanel` yolu | 32,8 (5 chunk) | `charts` 21,4 · `TimeSeriesPanel` 5,9 · `chartTooltip` 3,8 · iki küçük |
| Trace şelalesi | 7,8 (2 chunk) | `TraceWaterfall` 7,2 |
| Log tablosu | 7,8 (4 chunk) | `LogTable` 6,1 |
| İstisna örnek satırı | 1,0 (1 chunk) | — |
| ML kartı | 8,2 (3 chunk); grafiği çizilince + `TimeSeriesPanel` yolu | `MLRecordCard` 4,4 |

Sonuç: dört bileşeni tembel eklemek bütçeyi zorlamaz (ilk yük +0,1 KB, bütçeye 53,5 KB pay var). Asıl maliyet grafiğin hangi sarmalayıcıyla çizildiğidir: `corePanelEntry` yolu ilk grafikte 185,3 KB indirir, `TimeSeriesPanel` yolu 32,8 KB (§7-13). `CosreChart` (2,0) zaten sohbet kapanışındadır.

## 5. Önerilen tasarım

### 5.1 Karar: yeni üst düzey parça mı, blok tipi mi

**Öneri: blok tipi.** Altı parça `data-block-<tip>` olarak akar: `chart` (mevcut tip, üçüncü ve açıkça işaretli biçim), yeni `trace`, `logs`, `exception`, `anomaly`, `source`. Gerekçe: çevirici değişmez, arşiv ve geri yükleme hazır, bilinmeyen tipi eski istemci atlar, `tools-audit.md` Q5'in "kapalı küçük parça kümesi" kararı korunur, mevcut altın dosyaların baytı değişmez. Üst düzey parça seçilirse altı ad için `KnownKinds`, altı `case`, `v2OnlyEventKinds`, altı ayrıştırıcı ve yeni bir arşiv yolu gerekir; kazanç yalnız adın kısalığıdır.

| İstenen | Bugün var olan | Yükünde eksik | Yeniden kullanılmaması gereken |
|---|---|---|---|
| `data-chart` | `chart` (a) sorgu tarifi, (b) veri taşıyan; `metric_panel` | katalog kimliği, kimlik (cluster / namespace), mutlak pencere, anomali penceresi, araç anındaki özet değerler | (a) biçimi: varsayılanı ClickHouse; (b) biçimi: seri taşır ve kaynağı VM olmayabilir |
| `data-trace` | `trace_list` (liste), `topology` | tek trace özeti, hatalı span kimliği, kritik yol | `trace_list`: liste anlamını bozmamak için |
| `data-logs` | `table` (`rowSubjects` ile), `log_spike` | sorgu özeti, histogram, satırda cluster / namespace / pod | `log_spike`: artış anlatımına özgü; genel log örneği değil |
| `data-exception` | yok (`service_report` içinde gömülü) | grup + sayı + ilk / son + örnek trace + ilk çerçeveler | `service_report` |
| `data-anomaly` | yok (`charts_signals` yalnız sinyal listesi) | `ml_events` kaydı, pencere, örnek trace, grafik tarifi | `charts_signals` |
| `data-source` | `source-url` / `source-document`, `link`, `evidence` | eşleşen bölüm, alıntı, skor, hangi bulguya bağlı | `answer.sources` yolu: parça numarası anlamı taşır, yalnız kademelerde dolar |

### 5.2 Parça sözleşmesi

Ortak alanlar: `ref` (defter olgusuyla ortak kimlik), `entity {service, cluster?, namespace?}`, `window {fromMs, toMs}` (mutlak), `href` (tam sayfa). Grafik seri taşımaz (tarif); diğerleri küçük ve tavanlı örnek taşır. Hiçbir alan modelden gelmez.

| Parça | Yük alanları | Kim üretir | Model ne görür (özet) | Arşiv |
|---|---|---|---|---|
| `chart` (`recipe:"metric"`) | `metric` (katalog kimliği), `entity`, `window`, `groupBy?`, `anomaly? {fromMs, toMs, label}`, `stats {last, peak, peakAtMs, min, avg, unit}`, `title` | yeni `metric_chart` aracı; `endpoint_metrics`; `explain_ml_event` (metrik sinyali kataloğa oturuyorsa) | `stats` + pencere + anomali penceresi, ≤300 B ≈ 100 token | değerle, ≈0,4 KB; geri yüklemede istemci yeniden çeker, `stats` araç anındaki değerdir |
| `trace` | `traceId`, giriş servisi ve operasyonu, `startMs`, `durationMs`, `spanCount`, `errorSpan {spanId, service, name, exceptionType, message ≤160}`, `criticalPath ≤6` | `explain_trace` | süre, hatalı span, kritik yolun başı, ≤400 B ≈ 135 token | değerle, ≤1,5 KB; şelale açılınca span'ler mevcut trace ucundan |
| `logs` | görünen sorgu, `total`, `histogram ≤12`, `rows ≤10 {ts, sev, service, body ≤160, traceId?, pod?, cluster?, namespace?}` | `search_logs` (v2), `get_logs_for_trace` | toplam, seviye dağılımı, 3 örnek satır, ≤600 B ≈ 200 token | değerle, ≤2,5 KB |
| `exception` | `fingerprint`, `type`, `message ≤160`, `count`, `firstSeenMs`, `lastSeenMs`, `state`, `sampleTraceId`, `sampleSpanId`, `frames ≤5` | `get_exception_samples` (grup satırı ve çerçevelerle genişletilmiş) | ad, sayı, ilk / son, örnek trace, ilk 3 çerçeve, ≤550 B ≈ 185 token | değerle, ≤1,5 KB |
| `anomaly` | `id`, `detector`, `signal`, `part`, `startedMs`, `lastSeenMs`, `windowSec`, `observed`, `baseline`, `score`, `direction`, `verdict`, `sampleTraceIds ≤3`, `chart?` (tarif) | `explain_ml_event` | dedektör, gözlenen / taban, pencere, örnek trace, ≤350 B ≈ 115 token | değerle, ≤1 KB; kart açılınca kayıt mevcut ML ucundan |
| `source` | `kind` (wiki / upload / runbook), `title`, `href`, `heading`, `snippet ≤240`, `score`, `findingRef`, türetilen sorgu | sunucu adımı (§5.7); araç değil | başlık, bölüm, alıntı; kaynak başına ≤400 B ≈ 135 token, en çok 2 | değerle, ≤0,8 KB |

Model görünümü: parça üreten araçta modele giden metin sonucun `summary` nesnesidir; `parts` alanı `chat.go:1620-1639` dikişinde blok kurulduktan sonra modele gitmeden çıkarılır. Parça üretmeyen araçlar bugünkü gibi 2.048 B ile kırpılır.

### 5.3 Metrik kataloğu ve "yalnız VictoriaMetrics"

- **Katalog:** `metricChartCatalogue` (`api/metric_chart_templates.go:98`), 11 kimlik: `jvm_heap`, `jvm_gc`, `cpu`, `memory`, `threads`, `db_pool`, `kafka_lag`, `http_5xx`, `http_rps`, `http_p99`, `http_p95` (`:36-46`). Bu iş için uygun: PromQL yer tutuculu şablondan kurulur, varyant metriğin etiket kümesine göre seçilir, ≤20 seri × ≤120 nokta, sorgu metni önbellek anahtarına girer. Eksikleri: `api` paketinde durduğu için araç erişemez (nötr bir pakete taşınır, guided rota aynı paketi kullanır); seçici servis + ortam ile kurulur, cluster / namespace eşleştiricisi yok (VM etiket haritasında bu roller tanımlı, `internal/vmetrics/labelmap.go:44-51`); rota kırılımı yok (`endpoint_metrics` için `http_*` şablonlarına rota seçicisi eklenir); kimlik listesi modele ve istemciye açık değil (yeni aracın `metric` enum'u olur).
- **`data-chart` ne kullanabilir:** yalnız katalog kimliği + VM. Şablonun VM yokken span grafiğine düşen dalı (`api/copilot_metric_chart.go:625-627`) v2 parça yolunda kullanılmaz; VM yoksa ya da servis o metriği basmıyorsa parça çıkmaz, araç bunu yapısal olarak söyler ve Service sayfasına link verir.
- **ClickHouse tarifi:** `render_chart` v2 model kataloğundan gizlenir (`V2Hidden`); kayıt defterinde ve dış MCP sunucusunda kalır, v1 aynen. Rota incelemesinin `source:'route'` grafiği bu işin dışındadır.
- **`query_metrics`:** v2'de veri taşıyan grafik bloğu yalnız zarfın `source` alanı VictoriaMetrics iken çıkar; ClickHouse'a düşen RED ve Thanos aileleri çip + metin olarak kalır, cevap kaynağı adıyla söyler.
- **Pod CPU / bellek (Thanos): `data-chart` dışında.** Kataloğun `cpu` ve `memory` şablonları sürecin kendi metriğini VM'den çizer ve Infrastructure sekmesine yönlendirir (`api/metric_chart_templates.go:23-26`); cAdvisor serisi için grafik yok, `cluster_metric` ve `query_metrics` pod aileleri okunmaya devam eder (§7-3).

### 5.4 Kanıt ne zaman eklenir

Parça yalnız araç sonucundan doğar; model parça seçmez, yazmaz. Sunucu turun aday parçalarını toplar, cevaba hangilerinin ekleneceğine `answer` yayınından önce karar verir.

| Kural | Sunucunun deterministik kararı | Modele kalan |
|---|---|---|
| (a) canlı veriye dayanan iddia | Cevaptaki sayı, saat ve kimlikler defter olgularıyla eşleştirilir (E1'in yaptığı iş); eşleşen olgunun parçası eklenir. E1 gelmeden: tür başına son başarılı adayın parçası | hangi aracı çağıracağı |
| (b) "göster", "nereden biliyorsun", "emin misin" | Kullanıcı turunda kapalı ipucu listesi → tavan 2'den 4'e çıkar; bu turda araç çağrılmadıysa önceki turun defterindeki parça tarifleri eklenir | yok |
| (c) eyleme yönlendiren cevap | Cevap metninde kapalı eylem listesi (rollback, restart, geri al, yeniden başlat, eskalasyon, ölçekle); aday varsa en ilgilisi eklenir, yoksa `evidence_missing_action` sinyali (önce gölge) | öneriyi yazıp yazmamak |
| genel soruya grafik yok | Cevap sınıfı bilgi / sohbet ise tavan 0 ve `metric_chart` o turda sunulmaz; araç çağrılmayan turda aday zaten yoktur | yok |
| yığılma | Cevap başına ≤2 parça (ipuçlu turda ≤4), tür başına 1; eşleşmeyen aday eklenmez, adım kartında kalır | yok |
| tahmin | Parça yok, etiket yok, uyarı kutusu yok; ayrım doğal sözcükle | "muhtemelen", "genellikle" demek |

Üç ölçüt, hakemsiz, konuşma kipinde (`api/ai_evalset_conversation.go` `expect`, kalıp listeleri `internal/ai/evalrubric/`):
1. **Canlı veri iddiasının kanıtı var:** sahte veriyle eşleşen her sayı için o olgunun parçası cevapta mı (`claims_with_evidence_pct`); vaka `expect.evidence.require` ile tür isteyebilir.
2. **Gereksiz kanıt yığını yok:** `expect.evidence.max` (bilgi / sohbet turunda 0); cevap başına parça ortalaması, tavan aşımı payı, hiçbir iddiayla eşleşmeyen parça sayısı.
3. **Tahmin veri gibi sunulmadı:** mevcut `ungroundedNumbers` (sahte veride olmayan sayı); araçsız ya da boş sonuçlu turda sayı / saat taşıyan durum cümlesi yok; kaynak bulunamayan turda "wiki'ye göre" kalıbı ve `source` parçası yok; neden öne sürülen araçsız turda kapalı listeden bir ihtiyat sözcüğü var.

### 5.5 Kimlik ve kapsam

Bugün: kapsam yalnız şemasında `services` dizisi olan altı araca iner (`mcp/v2_traces_logs.go:343`, `:589`, `:743`; `v2_endpoint_metrics.go:358`; `v2_tools.go:368`; `v2_problems.go:270`); model `services` verdiyse ya da başka bir takım adı yazdıysa modelin değeri kalır (`api/chat_user_scope.go:267-272`); kimlik çıplak servis adıdır. Sahiplik de ada bağlıdır (`service_metadata`, tek anahtar `service`).

Veri katmanının sınırı: `service_summary_5m` servis × 5 dk'dır (`internal/chstore/store.go:1223-1233`), `exception_groups` parmak izini tür + mesaj + servisten üretir (`:2405-2424`), `ml_events` servis ve pod taşır (`:3134-3151`). Bu üç depoda cluster / namespace ile **süzmek mümkün değil**. Ayırabilenler: VM (etiket haritası), log deposu (`logstore.Filter` cluster ve namespace alır, `internal/logstore/logstore.go:49`, `:62-67`), trace (kimlik zaten trace_id), Thanos.

Öneri (K1):
- Kimlik tipi `ServiceRef{service, clusterId, cluster, namespace}`. Kapsam alışveriş başına bir kez, servis adlarından varlık modeliyle çözülür: `svc:<ad>` → `serves` kenarı → iş yükleri → (cluster, namespace) çiftleri (`internal/entity/identity.go:38-48`, `entity/service_pods.go:63`). Ad eşlemesi yapılmaz.
- Kapsam argümana değil bağlama konur; şema büyümez (çağrı başına token 0). Her v2 aracı bir sınıfa girer ve test bunu pinler (sınıfsız yeni araç testi kırar): *argümanla kapsamlı* (bugünkü altı), *bağlamla kapsamlı*, *yalnız kimlik* (katalog araçları), *muaf* (meta, bilgi).
- "Modelin değeri kazanır" daralır: modelin verdiği servis kapsamdaysa, kullanıcının bu turda andığı ya da durum nesnesindeki varlıksa, ya da kapsam kaçışı açıksa kabul edilir; değilse araç `out_of_scope` yapısal hatası döner. Sessiz genişleme ve sessiz daraltma yok.
- Ayıramayan depolarda kimlik **etikettir**: parça `entity` alanını varlık modelinden taşır; ad birden çok (cluster, namespace) çiftinde koşuyorsa sonuç bunu söyler ve cevap ya çifti adlandırır ya da mevcut `entity_pick` soru kartını açar.
- Maliyet: alışveriş başına bir varlık çözümü (60 sn'lik süreç içi katalog önbelleği var), yaklaşık 25 araç işleyicisinde süzgeç, bir sınıflandırma testi. Şema işi yok (§7-6).

Araçlar risk sırasıyla:
- **Yüksek** (boş argüman = bütün servisler, ya da serbest servis adı ve parça üretecek): `get_log_anomalies`, `list_anomalies`, `list_exception_groups`, `get_exception_samples`, `explain_ml_event`, `list_problems`, `trace_stats`, `query_traces`, `query_logs`, `query_metrics`, `list_services`.
- **Orta** (tek servis / trace / Problem kimliği zorunlu, ekran kapsamı doldurur): sağlık ve topoloji ailesi, `service_health_sweep`, `pivot_from_trace`, `explain_log_spike`, log deseni ailesi, `get_trace`, `get_logs_for_trace`, trace bulma ailesi, Problem ailesi, değişiklik ailesi, `get_pod_health`, `cluster_metric`, `get_team_services`.
- **Düşük / muaf:** katalog ve varlık araçları, öznitelik keşfi, bilgi araçları, meta araçlar.
- **Erişilemeyen:** dış MCP araçları v2 süzgecinden geçmez; kapsam uygulanamaz, kanıt parçası üretemezler (§7-8).

### 5.6 Araştırma profili / anlatım profili

En küçük mekanizma: yeni yüzey `chat-narrate` ve yeni adım türü. Araştırma turları bugünkü gibi `chat` yüzeyinde koşar. Araştırma modeli araç çağırmadan dönünce ve bu turda en az bir araç çağrılmışsa, sunucu araçsız tek bir anlatım çağrısı yapar: `prepareStep` zincirine eklenen plan bağlama `chat-narrate` yüzeyini, `WithNoToolCalls`'u ve sınıfın token tavanını koyar. Anlatıcıya araç konuşması gitmez; guided anlatım deseniyle soru + bu turun model görünümü özetleri (≤6 × ≤600 B) + araştırmacının kısa bulgu notu gider.

- **Yapılandırma:** yeni anahtar yok; mevcut yüzey → profil haritası. Ayrım yalnız `chat-narrate` haritada açıkça eşlenmişse ve çözülen profil `chat`'inkinden farklıysa etkinleşir; aksi hâlde bugünkü tek çağrı. Açık profil seçimi ve Derin kip çözüm sırasında yüzeyi ezdiği için ayrımı kendiliğinden kapatır.
- **Anlatıcı yalnız özetleri görür.** İstenen de bu (sayı yalnız araç çıktısından), ama araştırmacının gördüğü tablo ayrıntısı kaybolur; bu yüzden girdi beş olguluk defter değil turun bütün özetleridir.
- **Bağlam penceresi farkı.** Anlatıcı girdisi yapısı gereği ≤ yaklaşık 6 KB ≈ 2.000 token'dır, küçük pencereli model de taşır. 21 bin token'lık kataloğu araştırma profili taşır; profilde pencere alanı olmadığından yanlış boyutlu profil ancak geçit hatasıyla görünür, iki profilli evalset koşusu bunu canlıdan önce yakalar.
- **Bir profil yok ya da hata veriyor.** Anlatım çağrısı başarısız olursa araştırmacının bulgu notu cevap olarak yayınlanır, `narrate_fallback` sinyali yazılır; cevap sessizce düşmez.
- **Yerel profil sabitlemesi.** Wiki ya da kod metni bağlama girmişse iki profil de yerel olmalıdır; değilse anlatım araştırma profilinde kalır.
- **Gecikme.** Araştırma taslağı akmaz (yoksa kullanıcı taslağı görür, sonra silinip yeniden yazılır); ilk token anlatım çağrısına kadar gecikir. Sessizliği F1 ara cümlesi kapatır.

### 5.7 Bulgudan bilgi sorgusu

- **Girdiler** (hepsi araç çıktısında var): istisna türü, mesaj, servis, span adı (`get_exception_samples`); operasyon ve istisna (`explain_trace`); dedektör, bölüm, kod (`ml_events`); log deseni şablonu; iş kodu / HTTP durum kodu; yığının ilk çerçevesi.
- **Türetme** (saf, tablo testli): istisnanın kısa sınıf adı; mesajdan sayı, kimlik ve onaltılık değerler atılarak şablon, şablondan 2–4 ayırt edici sözcük; varsa hata kodu; servis adı yalnız sıralamada. Önce dar sorgu (sınıf adı + kod), boşsa gevşek sorgu. Model sorgu yazmaz.
- **Arka uçlar, sırayla:** runbook (kural / servis türetmesi + mevcut sözcük puanlayıcı) → `wiki.SearchWith` (wiki ve yüklenen belgeler tek indekste; canlı yedek kapalı ya da yalnız bayatken, semantik tarama kapalı; bulgu başına bir arama, tur başına en çok iki, 1,5 sn bütçe). RAG `document` kaynağı kullanılmaz. ADO tasarımı uygulanmadı; gelirse aynı arayüzün arkasına girer, bu iş onu beklemez.
- **Eşik:** `source` parçası yalnız skor eşiği geçilmiş ve eşleşen bölümde güçlü terim (sınıf adı ya da kod) varken çıkar. Değilse modele "bilgi kaynağında eşleşme yok" olgusu gider; cevap bunu doğal cümleyle söyler.
- **Gizlilik ve kapsam:** wiki indeksi kurulum geneli tek servis hesabıyla okunur, kullanıcı başına izin yok; bugünkü `search_wiki` ile aynı sınır. Aynı üç kapı geçerli: yalnız oturum kullanıcısı; dış MCP yapılandırılmışsa wiki metni araç turlarına girmemeli, bu yüzden arama araç turları bittikten sonra, cevaptan hemen önce koşar; alışveriş yerel profile sabitlenir.
- **`data-source` ile `source-url` farkı:** mevcut parça yalnız kimlik, bağlantı, başlık taşır; yenisi bölümü, alıntıyı, skoru ve hangi bulguya bağlı olduğunu taşır. Mevcut kademeler `source-url` üretmeye devam eder.

### 5.8 Token bütçesi

- Bugün: model çağrısı ≈ istem 3.250–3.300 token + katalog ≈ 20.970 token + konuşma; araç sonucu başına tavan 2.048 B ≈ 683 token ve sonuç sonraki her turda yeniden gider.
- Parça üreten beş araçta tavan 683'ten 100–200 token'a iner (§5.2). Gerçek düşüş sonuçların bugünkü ortalama boyuna bağlıdır (§8).
- Katalog: `render_chart` çıkar (−1.946 B), `metric_chart` girer (hedef ≤1.000 B) → çağrı başına ≈ −315 token. Kimlik için şema büyümez.
- Bilgi araması araç değildir: şema 0; eşleşme olan turda ≤270 token.
- Profil ayrımı tek kalemde artırır: araç kullanan turda bir anlatım çağrısı ≈ +1.900 giriş token'ı (anlatım istemi ≈ 620 + özetler ≤1.200 + soru); katalog bu çağrıda gitmez.

## 6. Dilim planı

Operatörün A–D'si altı dilime yeniden kesildi; harf çakışmasın diye K1–K6. Karşılık: A → K1 + K2; B → K3; C → K4a + K4b + K5; D → K6. Her dilim kendi sürümü, doğrudan main, `docs/frontend/migration-log.md` girişi, `go test -race` + vitest yeşil, hepsi `cosre_v2` arkasında, v1 baytı değişmez.

### 6.1 Dilimler

| Dilim | Kapsam | Dosyalar | Test | Etkilenen altınlar | Risk | Tur başına token | Bekledikleri |
|---|---|---|---|---|---|---|---|
| **K1** kimlik ve kapsam | `ServiceRef`; kapsam varlık modelinden çözülür, bağlamla her v2 aracına iner; araç sınıflandırması; `out_of_scope`; ayıramayan depolarda kimlik etiketi | `api/chat_user_scope.go`, yeni `mcp/scope_ctx.go`, yüksek riskli araç dosyaları (`v2_ml.go`, `exception_samples.go`, `trace_stats.go`, `query_*.go`, `tools.go` liste araçları), `api/mcp_deps.go` | sınıflandırma pini; kapsam karar tablosu; kapsam dışı servis reddi; aynı ad iki yerde senaryosu | `ui-stream-v1-scope` (yük genişlerse); katalog altını değişmez | orta-yüksek: yanlış daraltma "veri yok" diye okunur; iki sürüme bölünebilir (tip + sınıflandırma, sonra süzgeçler) | 0 | konuşma A (durum nesnesindeki varlık) |
| **K2** kanıt parçaları | Beş yeni blok tipi + `chart`'ın katalog biçimi; yük kurucuları; model görünümü; `metric_chart` aracı; katalog nötr pakete; tarif ucu; `explain_trace`'e span kimliği, `get_exception_samples`'a grup satırı + çerçeveler; `query_metrics` VM kapısı; `render_chart` v2'de gizli | `internal/ai/agent/blocks/block.go`, yeni `mcp/evidence_parts.go`, `mcp/evidence_blocks.go`, yeni `internal/ai/metriccatalog/`, yeni `mcp/v2_metric_chart.go`, `mcp/v2_tools.go`, `mcp/v2_traces_logs.go`, `mcp/exception_samples.go`, `chat.go` (sonuç dikişi), yeni `api/cosre_evidence_routes.go` (`registerCosreEvidenceRoutes`), `api/ai_evalset_stream_contract.go`, `fe/lib/cosre/protocol.ts` (yalnız tip listesi) | yük kurucu tablo testleri; "model görünümünde seri ve satır yok" pini; VM dışı kaynakta grafik yok; v1 akış baytı değişmedi pini; tarif ucu önbellek anahtarı bütün girdileri özetler | yeni `ui-stream-v1-evidence` çifti; `chat_stream/v2_loop.sse` yenilenir; `toolset_catalog_golden.json` `zero/v2` ve `conditional/v2`; mevcut `ui-stream-v1*` ve `v1_*.sse` değişmez | orta: MCP dış sözleşmesine alan eklenir (yalnız ek); katalog taşıması guided rotayı etkiler | çağrı başına ≈ −315; parça üreten sonuç başına tavan 683 → 100–200 | konuşma A (defter biçimi), K1 |
| **K3** bulgudan bilgi | Sorgu türetme; runbook + wiki araması; eşik; `source` bloğu; "eşleşme yok" olgusu | yeni `api/chat_finding_knowledge.go`, `mcp/evidence_parts.go`, `mcp/knowledge_tools.go` (puanlayıcı dışa açılır) | türetme tablo testi; eşik; dış MCP kapısı; yerel profil kapısı | `ui-stream-v1-evidence` genişler | orta: alakasız eşleşme güveni düşürür; eşik gölgede ayarlanır | eşleşme olan turda ≤ +270; yoksa ≈ +15 | K2 |
| **K4a** ekleme kuralları + ölçütler | Aday toplama, ekleme kararı (§5.4), ipucu ve eylem listeleri; konuşma kipine üç ölçüt ve 8–10 kanıt vakası; istem değişmez | yeni `api/chat_evidence_policy.go`, `api/ai_evalset_conversation.go`, yeni `internal/ai/evalrubric/evidence.go`, `cop/evalset/conversation/` | karar tablo testi; ölçüt fonksiyonları; sahte modelle çok turlu koşu | yok (blok sırası değişirse `ui-stream-v1-evidence`) | düşük; taban ölçümü istemden önce alınır | 0 | K2, konuşma B (sınıf), konuşma E1 (eşleşme; yoksa yedek kural) |
| **K4b** istem ve döngü | v2 varyantında inceleme bölümü (belirti → hipotez → kanıt → sonuç, sabit pivot sırası yok), ihtiyat sözcüğü kuralı, örnekle konuşma, cevap ölçüsü; 2–3 kanıt diyaloğu; "her olgusal iddia araç çıktısına dayanmalı" ve "güven seviyeni belirt" v2'de değişir; gizli araç adları istemden çıkar | konuşma C'nin dosyaları (`cop/prompts.go` v2 accessor'ları, `cop/styledialogs/`) | C'nin bayt bütçesi pini; v1 istem baytı değişmedi pini | — | **yüksek** (C'nin riski; ayrı sürüm olursa istem iki kez değişir) | C'nin bütçesi içinde, ek 0 | konuşma C ile **tek sürüm**; K2, K4a |
| **K5** profil ayrımı | `chat-narrate` yüzeyi, anlatım adımı, taslağın akmaması, bulgu notu, geri düşüş, evalset'te profil çifti | `api/chat_prepare_step.go`, `chat.go` (döngü çıkışı), `cop/profiles.go`, `cop/prompts.go`, `api/ai_evalset_conversation.go` | etkinleşme tablosu (eşleme yok / aynı profil / açık profil / Derin); geri düşüş; akışta araştırma taslağı yok | `chat_stream/v2_loop.sse` eşleme yokken değişmez | orta: ilk token gecikir; anlatıcı ayrıntı kaybeder | araç kullanan turda ≈ +1.900 giriş; eşleme yoksa 0 | K4b, konuşma F1 |
| **K6** parça çizicileri | Altı çizici mevcut bileşenlerle, hepsi tembel; `MessageList`'ten çizilir, v1 `ChatBubble`'a dokunulmaz; tarif ucundan grafik; şelale ve ML kaydı açılınca mevcut uçlardan | yeni `fe/components/cosre/evidence/parts/`, `MessageList.tsx`, yeni `fe/lib/cosre/evidenceParts.ts`, `fe/lib/api.ts` | vitest bileşen testleri; `uiStreamFixture.test.ts`; `cosrePersist.test.ts` (geri yükleme); `bundle:check` | Go altını yok | düşük-orta; paket etkisi §4'te ölçüldü | 0 | v2 sohbet arayüzü fazı main'de; K2 yükleri donmuş |

### 6.2 Konuşma programıyla tek sıra

1. **H1** — gemide.
2. **Konuşma A** (durum nesnesi + defter) — sürüyor. K1 ve K2 bunu bekler: kimlik durumdan, parça özeti defterden.
3. **K1**; ayrık dosya kümesiyle yanında **konuşma E1** (gölge doğrulayıcı).
4. **K2**.
5. **Konuşma B** (cevap sınıfı).
6. **K4a** — ölçütlerle taban ölçümü; istem henüz değişmedi.
7. **K3**; yanında **K6** (yalnız frontend, K2 yükleri donunca).
8. **Konuşma C + K4b** — tek istem sürümü.
9. **Konuşma D + E2** — cevap dikişi; (c) kuralının uygulaması aynı dikişe girer.
10. **Konuşma F1**.
11. **K5**.
12. **Konuşma G**, **H2**.

"Yanında" = ayrı ajan, ayrık dosya listesi, ayrı sürüm; sürümler yine tek tek main'e iner ("aynı anda tek iş" kuralı sürüm sırası için geçerli kalır). Beklemesi zorunlu olanlar: K1 ve K2 → A; K4a → B; K4b → C (aynı sürüm); K5 → F1; K6 → v2 arayüz fazı.

## 7. Operatöre açık kararlar

1. **Parça mekanizması.** Altı yeni üst düzey `data-*` parça mı, `data-block-<tip>` altında blok tipi mi? Öneri: blok tipi (§5.1); arşiv, geri yükleme ve çevirici hazır, `tools-audit.md` Q5 korunur.
2. **Grafik adı.** Katalog tarifi mevcut `chart` tipinin açıkça işaretli üçüncü biçimi mi, yeni bir tip mi (`metric_chart`)? Öneri: üçüncü biçim (`recipe:"metric"`); eski iki biçime dokunulmaz.
3. **"Yalnız VM"nin kapsamı.** Yalnız grafik mi VM'den, yoksa model ClickHouse / Thanos metriğini hiç okumasın mı? Öneri: `data-chart` yalnız VM; araçlar okumaya devam eder, cevap kaynağı adıyla söyler. Pod CPU / bellek grafiği dışarıda, süreç düzeyi `cpu` / `memory` içeride.
4. **VM'de metrik yoksa.** Öneri: parça yok, dürüst cümle + Service sayfası linki; span grafiğine düşülmez. Sıklığı `endpoint_metrics` atlama oranından ölçülür.
5. **Tarif mi, veri mi.** Tarif geri yüklemede yeniden sorgular; saklama süresi dolmuşsa grafik boş gelir. Öneri: tarif + araç anındaki `stats` parçada; kart atıf yapılan sayıları her zaman gösterir.
6. **Rollup'larda cluster / namespace.** RED rollup'ı, `exception_groups`, `ml_events` bu boyutları taşımıyor. Şema işi istenir mi? Öneri: bu programda hayır; kimlik varlık modelinden etiket, ayırabilen depolarda süzgeç.
7. **Kapsamın anlamı.** Öneri: varsayılan daraltma olarak kalır (yetki değil); modelin kendi çıkardığı kapsam dışı servis `out_of_scope` ile reddedilir, kullanıcının andığı varlık ve kaçış aynen çalışır.
8. **Dış MCP araçları.** Kapsam uygulanamaz, kanıt parçası üretemezler. Öneri: kabul ve belgelenir.
9. **Profil ayrımı.** Yeri yüzey haritası, yeni anahtar yok; araştırma taslağının akmaması kabul mü? Öneri: evet, F1'den sonra.
10. **Bilgi arka uçları.** Öneri: runbook + wiki indeksi (yüklenen belgeler dahil); RAG `document` ve ADO tasarımı bu programa girmez.
11. **Dış MCP varken wiki araması.** Öneri: araç turları bittikten sonra, cevaptan hemen önce koşar.
12. **Kanıt tavanı.** Öneri: cevap başına ≤2, "göster" turunda ≤4, bilgi / sohbet sınıfında 0.
13. **Grafik çizicisi.** Mevcut `EvidenceChart` (ilk grafikte 185,3 KB gz) mi, `TimeSeriesPanel` (32,8 KB) mi? Öneri: `EvidenceChart`; sohbette zaten var, tarif ucu aynı yük biçimini döner. Fark ilk yükü etkilemez.
14. **Eylem öneren cevapta kanıt yoksa.** Öneri: önce gölge sayaç; uygulama konuşma E2'nin kipine bağlanır.
15. **Anahtar.** Öneri: yeni anahtar yok; `cosre_conversation` blobuna tek alan "kanıt" (kapalı / gölge / açık).
16. **İstem.** K4b konuşma C'siyle tek sürüm mü? Öneri: evet. İki mevcut kural v2 varyantında değişir (§3.5).
17. **Araç çıktısına alan eklemek.** `explain_trace` span kimliği, `get_exception_samples` grup satırı ve çerçeveler dış MCP sözleşmesini büyütür. Öneri: kabul, yalnız ek alan.

## 8. Koddan doğrulanamayanlar ve bilgi föyü düzeltmeleri

| Doğrulanamayan | Hangi prod değeri kapatır |
|---|---|
| VM etiket haritasında cluster ve namespace rollerinin eşlenmiş olması | VM ayar blobu; eşleme raporu |
| Servislerin ne kadarının VM'de HTTP metriği bastığı | `endpoint_metrics` atlanan sonuç oranı |
| Aynı servis adının birden çok (cluster, namespace) çiftinde koşma sıklığı | varlık ilişkilerinde servis başına `serves` çifti sayısı |
| Parça üreten araçların bugünkü ortalama sonuç boyu (token düşüşünün gerçek değeri) | adım olaylarındaki `bytes`; `ai_calls.input_tokens` |
| Gerçek bayt / token oranı ve geçidin araç şemasına eklediği çerçeve | araçlı ve araçsız çağrı farkı |
| Prod'da wiki'nin ve dış MCP'nin yapılandırılmış olması; logların Elasticsearch'ten okunduğu | ayar blobları |
| `chat`, `chat-guided` yüzeylerinin profili ve modellerin bağlam penceresi | profil ayarı; geçit model kartı |
| VM saklama süresi (tarifin geri yüklemede boş dönme eşiği) | VM yapılandırması |
| `list_anomalies` ML satırlarının `ml_events` kimliği taşıması; `(f)` işaretli yaklaşık 40 aracın tavan ve alanları | okunmadı; K1 sınıflandırması sırasında okunur |
| `endpoint_metrics` gövdesinin zarf biçimi | okunmadı; `case` yokluğu grafik çıkmaması için yeterli |
| Konuşma A'nın defter kaydının son biçimi | o dilim main'e inince; K2 ona uyar |
| "Beş uPlot sarmalayıcısı"nın tam listesi | sayılmadı; tasarım hiçbirine dokunmuyor |

Föy düzeltmeleri:
1. Konuşma kipi evalset koşucusu bu ağaçta **var** (`api/ai_evalset_conversation.go`, `api/ai_evalset_conv_seam.go`, `internal/ai/evalrubric/fluency.go`); föy "yok" diyordu.
2. `ungroundedNumbers` evalset'te de çağrılıyor (`api/ai_evalset_conversation.go:504`); föy "yalnız trace takibi" diyordu.
3. `ChatBubble` ve statik ithalatları v2 chunk'ında değil: `ChatBubble` ayrı paylaşılan chunk (26,2 KB gz), `EvidenceChips` + `ChatTraceList` `oracleGroup` chunk'ında (12,5), v2 sohbeti v1 `CopilotChat` chunk'ını (34,5) statik çekiyor.
4. `search_knowledge`'ın `document` kaynağı kod olarak çalışır, veri olarak boştur; föyün atladığı asıl nokta yüklenen belgelerin wiki indeksinde durmasıdır (`internal/wiki/localdocs.go:3-13`).
5. Kullanıcı kapsamı: model başka bir takım adı yazdığında da enjeksiyon yapılmaz (`api/chat_user_scope.go:270-272`); föy yalnız `services` durumunu yazmıştı.
6. Föyde olmayanlar: v2 araç kataloğunun ölçülen boyutu (62.916 B); döngü isteminin gizli `search_traces` aracını anması (`cop/prompts.go:2930`); RED rollup'ı, `exception_groups` ve `ml_events`'in cluster / namespace taşımaması; `explain_trace` hata span'lerinde `span_id` olmaması.

Doğrulanıp aynen kalanlar: grafik bloğunun iki biçimi ve çizenleri; `endpoint_metrics`'in çip üretip grafik üretmemesi; kanıt zarfını taşıyan yedi araç; kapsamın yalnız `services` dizili altı araca inmesi, modelin değerinin kalması, çıplak servis adı; serbest döngü `answer`'ında `sources` olmaması; yeni blok tipinin çevirici değişikliği istememesi ve kendiliğinden arşivlenmesi, üst düzey parçanın istediği değişiklik listesi; VM dışı okuyan metrik araçları; bulgudan bilgi sorgusu türeten kod olmaması; bir alışverişin bütün turlarında tek profil.
