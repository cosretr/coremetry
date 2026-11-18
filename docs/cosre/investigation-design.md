# CoSRE araştırma ve arama tasarımı (2026-11-18)

Tasarım belgesi; kod değişmedi. Okunan ağaç `v0.10.2343`. Kısaltma: `api/` = `internal/api/`, `cop/` = `internal/copilot/`, `mcp/` = `internal/mcptools/`, `ch/` = `internal/chstore/`, `fe/` = `frontend/src/`; `chat.go` = `api/copilot_chat.go`. Token varsayımı denetimlerle aynı: **3 bayt ≈ 1 token**. Örnek adlar nötr (checkout, orders, payments, inventory); IP'ler belge aralıklarından (192.0.2.0/24, 198.51.100.0/24). Buradaki maddeler karardır, soru değildir; uygulama dilim dilim, ara onay beklemeden yapılır. `sre-evidence-audit.md` ("kanıt denetimi") ve `conversation-audit.md` tekrar edilmez, bölümle anılır.

## 1. Hüküm

Operatör üç şeyi altı sürümde alır. (1) **Arama:** attribute, uç nokta ve istemci IP'siyle trace araması `find_traces`'in üç yeni argümanı olur, log tarafında `search_logs` istemci IP'sini alır; soru tek araç turunda cevaplanır, devam sorusu ("ve 198.51.100.7'den gelenler?", "aynı endpoint için dün") konuşma durumundan devralır. İkinci sürümde, `cosre_v2` açık olan herkese gelir. (2) **Araştırma:** "checkout'taki hatayı incele" dendiğinde sunucu servis ve pencere için sınırlı bir kanıt paketini paralel okur (hata oranı, hatalı operasyonlar, istisna grupları, deploy, bağımlılık, anomali, örnek trace ve o trace'in logları; en çok 8 sn), model bu paketle başlar, gerekirse araçla derine iner, kısa ve bulguyla başlayan bir cevap yazar; cevabın altına en çok iki kanıt kartı eklenir. Dördüncü sürümde hazır, beşincide kartlar çizilir; `cosre_conversation.evidence` alanı `on` yapılana kadar etkisizdir. (3) **İki profil:** araç turları araştırma profilinde, görünen cevap anlatım profilinde koşar; altıncı sürümde gelir ve operatör `chat-research` yüzeyine profil eşleyene kadar bugünkü tek profilli davranış bayt bayt aynıdır. Yeni araç yok, yeni anahtar yok (mevcut bloba tek alan, mevcut yüzey haritasına tek yüzey), genel SQL / PromQL yok, v1 değişmez. Açılmadan değişen tek şey arama argümanlarının katalog maliyetidir: v2 model çağrısı başına ≈ +470 token.

## 2. Bugün ne var

### 2.1 Arama — Durum: kısmi

| İstek | Bugün v2 yolunda | Tur | Durum |
|---|---|---|---|
| Attribute ile trace listesi | `find_traces` yalnız kapalı süzgeç alır: operation, channel_code, function_code, status, min_duration_ms (`mcp/v2_traces_logs.go:340-356`). Serbest süzgeçli `search_traces` v2'de gizli (`mcp/v2_tools.go:111-115`). `query_traces` tek servis + operation, attribute yok (`mcp/query_traces.go:121-133`). | liste: yok | yok |
| Attribute ile sayım | `trace_stats.filters[]` + `errors_only`: grup başına sayı, hata oranı, p95; trace_id vermez (`mcp/trace_stats.go:30`, `:117`, `:150`). Anahtar keşfi `describe_attributes` (`mcp/attr_discovery.go:231`). Kapı: kapsam zorunlu, indeksli ≤6 sa, indekssiz ≤1 sa (`mcp/search_filters.go:36-38`, `:125-150`). | 2 (keşif + sayım) | var |
| Uç nokta ile | Guided ön-getirme soruyu operasyona çözer: `endpointMention` + `matchEndpoint` (`api/chat_endpoint_prefetch.go:162`, `:438`; `chat.go:619`). Döngüde `find_traces.operation` tam span adı ister. RED: `endpoint_metrics.route`, VictoriaMetrics (`mcp/v2_endpoint_metrics.go:361`). | 1 (ad tam ise) | kısmi |
| İstemci IP'si ile trace | Argüman yok. `find_attribute_by_value` IP'yi "host" şekli sayar; host sözlüğünde `client.address` yoktur (`mcp/attr_discovery.go:66`, `:72-80`), anahtar ancak 5.000 span'lik örneklemde görünürse bulunur. Sonra `trace_stats.filters` ile sayım. | 2–3; liste yok | kısmi |
| İstemci IP'si ile log | `search_logs.words` düz kelime: gövdede tek tırnaklı ifade (`mcp/query_logs.go:54-70`; ES `query_string`, `default_field` gövde, `internal/logstore/elasticsearch.go:2976-2984`). Yapısal alan yok: `logstore.Filter` ve `ESFieldMap` istemci adresi taşımaz (`internal/logstore/logstore.go:44-110`, `elasticsearch.go:260-291`). | 1; IP gövdede yazılıysa | kısmi |
| Metrik | Uç nokta: `endpoint_metrics` (var). Attribute ve IP: metrik etiketi değil; yok ve olmayacak (karar 8). | — | — |

İki örnek soru bugün: "192.0.2.10'dan /api/orders'a son 1 saatte gelen istekleri göster" → liste verilemez; sayı 2–3 turda (`find_attribute_by_value` → `trace_stats` + `client.address` ve `http.route` süzgeci). "tenant.tier = gold olan ve hata alan trace'ler" → sayı ve oran 1–2 turda, trace kimliği yok; döngü istemi modeli v2'de bulunmayan `search_traces`'a yönlendirir (`cop/prompts.go:2930`).

**İstemci adresinin durduğu yer.** Kodun okuduğu tek anahtar `client.address`'tir: çağıran kırılımı sunucu span'inden okur (`ch/backtrace.go:34`, `:221`), yüksek kardinalite listesindedir (`internal/acache/acache.go:167`), tipli kolonu yoktur, attribute dizisinde durur (`internal/otlp/semconv.go:32-35`). Attribute'lar aynen saklandığı için eski SDK adları veride olabilir; kod hiçbirini anmaz (`net.peer.ip` yalnız bir yorumda, `ch/repo.go:1762`). Log tarafında eşlenmiş alan yoktur.

### 2.2 Araştırma — Durum: kısmi

- Araçlar var (kanıt denetimi §3.1). Hata sorularının tek okumalı guided rotaları var (`api/chat_answer_class.go:95-98`); serbest döngü hata avı sınıfını tanır (`:129-147`).
- Çok adımlı deterministik araştırma var ama sohbet koşmaz: `internal/sre` dalgaları Problem kaydından plan kurar, 90 sn iş bütçesi, tek anlatım (`internal/sre/plan.go:3-14`, `:46-48`, `:214`).
- Döngü isteminde hipotez / kanıt ayrımı ve inceleme sırası yok (kanıt denetimi §3.5). Tavanlar: 5 tur, 6 çağrı (`chat.go:63`, `:68`); Derin 8 / 12 (`api/chat_deep.go:37-38`); alışveriş tavanı ≥180 sn (`api/chat_deadline.go:55`).
- Sunucunun döngüye hazır araç sonucu tohumlaması var: operasyon ön-getirmesi sentetik araç çifti ekler (`api/chat_operation_prefetch.go:308-317`, `chat.go:988`).

### 2.3 İki profil — Durum: yok

Alışverişin bütün turları tek profildedir: yüzey `chat` (`chat.go:342`), çözüm sırası açık profil > yüzey haritası > grup kardeşi > varsayılan (`cop/profiles.go:316-343`). Tur başına bağlam seçeneği dikişi hazır (`api/chat_prepare_step.go:53`, `:158-163`), profil değiştiren adım yok. Ayarlar ekranı yalnız iki yüzey grubu eşler: intent ve background (`cop/profiles.go:785-790`, `fe/pages/settings/AiProfilesPanel.tsx:65`); `chat` için seçici yoktur, yani sohbet varsayılan profilde ya da kullanıcının sohbette seçtiği profilde koşar (`chat.go:353-356`). Tur bütçesi pencereyi alışveriş başına bir kez çözer (`api/chat_round_budget.go:134-137`); araç seti profile göre seçilir (`cop/profiles.go:578-586`, `chat.go:807`); `ai_calls` satırının modeli alışveriş bağlamının profilidir (`cop/chat.go:155`). Konuşma kipi evalset'i iki profili yan yana koşar (`api/ai_evalset_conversation.go:66-67`, `:316`).

### 2.4 K1–K6 ile ilişki

K dilimlerinin hiçbiri gemide değil (`out_of_scope` ve `evidence` alanı ağaçta yok; `ch/cosre_conversation_settings.go:50-57`). Kanıt denetiminin on yedi önerisi kabul edilmiştir; iki sapma §3'te işaretli.

| K | Bu tasarımda |
|---|---|
| K1 kimlik ve kapsam | **Öne çekilir, dar** (S1): `ServiceRef` çözümü, tek dikişte `out_of_scope`, sınıflandırma pini. Yaklaşık 25 işleyiciye bağlam süzgeci K1'de kalır. |
| K2 kanıt parçaları | **Öne çekilir, dar** (S3): `trace`, `exception`, `logs`, `anomaly` blokları, model görünümü, `explain_trace` span kimliği, `get_exception_samples` grup satırı + çerçeveler. `chart` katalog biçimi, `metric_chart`, katalog taşıması, `render_chart` gizleme, `query_metrics` VM kapısı K2'de kalır. |
| K3 bulgudan bilgi | Dokunulmaz. Araştırma akışında son adım olarak yeri ayrılır (§4.3). |
| K4a ekleme kuralı + ölçütler | **Öne çekilir** (S3), önerildiği gibi. |
| K4b istem | Yalnız araştırma bölümü öne çekilir; taban isteme değil tura ek olarak (S4). Gizli araç adının v2 isteminden çıkması S2'de. |
| K5 profil ayrımı | **Öne çekilir, yüzey adı değişir** (S6; karar 21). |
| K6 çiziciler | **Öne çekilir, dört çizici** (S5). `chart` ve `source` çizicileri K6'da kalır. |

## 3. Kararlar

**Arama**
1. Yeni araç yok; `find_traces` ve v2 `search_logs` genişler. Neden: ikisi yalnız v2 kataloğundadır (`mcp/v2_tools.go:56-58`), v1 baytı değişmez; yeni araç ≈ 870 B katalog ve trace takibi sınıflandırması isterdi.
2. `trace_stats` değişmez; sayım bugünkü `filters[]` ile yapılır. Neden: v1 kataloğunda da vardır.
3. Süzgeç modeli kapalıdır: en çok 3 süzgeç, AND; işleç `eq | ne | in | exists`. Regex, içerir, serbest metin yok. Neden: `search_traces` tam bu yüzden gizlendi (`mcp/v2_tools.go:112`).
4. Anahtarı sunucu doğrular: tipli / terfi kolon (`ch/attr_discovery.go:145`) ya da kapsamın keşfedilmiş kataloğunda ya da kesin prob > 0 ise kabul; değilse `unknown_attribute` + en çok 5 yakın anahtar. Neden: keşif turu modelden sunucuya geçer (2 tur → 1), uydurma anahtar sorguya inmez.
5. Uç nokta `endpoint` argümanıdır; sunucu mevcut `matchEndpoint` ile tam operasyona çözer, belirsizse en çok 4 aday döner. Neden: çözüm tek kural olarak kalır, model span adı tahmin etmez.
6. İstemci IP'si trace'te kapalı aday anahtar listesinde tek OR grubuyla aranır: `client.address`, `http.client_ip`, `net.sock.peer.addr`, `network.peer.address`, `net.peer.ip`. Neden: kod yalnız ilkini bilir, veri eski adları taşıyabilir; liste kapalıdır, model anahtar seçmez. `x-forwarded-for` başlık attribute'u listede yoktur: değeri listedir, tam eşleşme tutmaz, içerir araması indekssiz taramadır.
7. CIDR yalnız trace'te ve yalnız sekizli sınırında (/8, /16, /24 ya da "192.0.2." öneki), tek servis, ≤1 saat. Logda ve IPv6'da yalnız tam adres. Neden: önek `=~` ile derlenir (`mcp/search_filters.go:83-87`) ve indekssiz yolun mevcut kapısı 1 saattir; ES'te önek joker sorgudur.
8. Logda attribute süzgeci, metrikte attribute / IP araması yok; loga trace üzerinden gidilir (`search_logs.trace_id`). Neden: `logstore.Filter`'a alan eklemek iki arka uçta her metodun uygulamasını ve ES'e yeni sorgu şekli ister; metrik etiketi sınırsız kardinalite olur.
9. İstemci IP'siyle arama ayrı rol istemez; viewer dahil herkes, kendi kapsamında. Neden: REST eşleri viewer'a açıktır (`api/api.go:730`, `:773`), araç rolü araç başınadır, argüman başına değil (`internal/mcp/mcp.go:256-267`), ürün kuralı tam sadakattir. Sonuç kapsam kuralının dışına çıkmaz (karar 10); servis adı vermeyen kapsamsız kullanıcıda arama koşmaz (`mcp/v2_traces_logs.go:168`, `:207`).
10. Kapsam K1 kuralıyla, tek dikişte (`chat.go:1484`): model `service` yazdıysa ve servis kullanıcının kapsamında, bu turda anılan ya da durumdaki varlık değilse araç koşmaz, `out_of_scope` döner. `evidence: shadow` yalnız sayar. Neden: işleyicilere dokunmadan sessiz genişlemeyi kapatır; yanlış red önce ölçülür.
11. Sonuçlar `entity {service, cluster, namespace}` etiketini varlık modelinden taşır; ad birden çok çiftte koşuyorsa `ambiguous: n`. Neden: kanıt denetimi §5.5; ad eşlemesi yapılmaz.
12. Konuşma durumuna tek yeni alan: `clientIp`. Uç nokta mevcut `operation`, attribute süzgeçleri mevcut `filters` alanında durur (`api/chat_context.go:46`, `:69`). Neden: v1 yazmaz, JSON baytı aynı kalır.

**Araştırma**
13. Yönlendirme (c): deterministik ilk dalga + istem bölümü. Neden: katalog her turda ≈ 21.000 token; paket üç-dört araç turunu sıfır model turuna indirir ve küçük modelin ilk araç seçimi hatasını ortadan kaldırır; sonrası serbesttir, sıra sabit değildir.
14. Paket yalnız şu koşulda koşar: v2, `evidence: on`, sınıf hata avı, tek çözülmüş servis, serbest döngü. Trace takibi, rapor sınıfı ve Derin kipte koşmaz. Açık araştırma ipucu ("incele", "araştır", "kök neden", "sorunu bul", "ne oldu") varsa tur guided orta kademelerini atlar. Neden: "istendiğinde" koşulu; ipucu yoksa bugünkü rota aynen.
15. Paket `internal/sre` okuyucularını kullanır, planını ve skorunu değil. Neden: `planSteps` Problem ister (`internal/sre/plan.go:214`), skor araştırma kaydından okur (`internal/sre/hypotheses.go:269`); okuyucular servis + pencere alır.
16. Paket Elasticsearch'e alışveriş başına en çok bir sorgu atar: örnek trace'in logları (trace_id terimi, bugünkü `search_logs trace_id` sorgusu). Servis geneli hata logu araması pakette yoktur. Neden: ES yükü artmaz.
17. Paket ≤8 sn duvar süresi, model turu ve araç çağrısı sayılmaz; döngü tavanları aynen (5 tur, 6 çağrı, alışveriş tavanı). Neden: yeni bütçe anahtarı yok; `ai.loopBudget` daraltmaya devam eder.
18. Bu tasarımın ilk ihtiyaç duyduğu bloklar `exception` ve `trace`, sonra `logs` ve `anomaly`. `chart` katalog biçimi K2'de kalır; o gelene kadar metrik sayıları metin + çip olarak verilir. Neden: `metric_chart` aracı ve katalog taşıması ayrı bir iştir, araştırmayı bekletmez.
19. Cevap ölçüsü mevcut hata avı sınıfıdır (768 token tavan, `api/chat_answer_class.go:75`). Elenen hipotez tek yan cümle, bakılamayan tek cümle. Neden: rapor değil konuşma.
20. Trace sohbet kuralı aynen: trace takibinde paket koşmaz, profil ayrılmaz, log ve trace yorum araçları daraltılmaz (`api/chat_trace_followup_tools.go`), tur bütçesi o aileleri en son kısaltır.

**İki profil**
21. Yeni yüzey `chat-research`; anlatım `chat`'te kalır. **Sapma** (kanıt denetimi §5.6 yeni yüzeyi `chat-narrate` yapıyordu). Neden: ayarlarda `chat` seçicisi yoktur; o tasarımda operatör hem `chat`'i hem yeni yüzeyi eşlemek zorunda kalır ve araçsız turlar da model değiştirirdi. Burada tek eşleme yeter, görünen metni yazan model bugünküyle aynı kalır.
22. Anlatıcı konuşmanın araç mesajlarını değil özet paketini alır (≤6 KB) ve ek olarak son dört turun metnini. **Sapma (ek)**: kanıt denetiminde geçmiş yoktu. Neden: hitap ve devam göndermesi geçmiş olmadan tutmaz; araç mesajsız girdi her model ailesinin sohbet şablonunda güvenlidir.
23. Tek `ai_calls` satırı kalır; `model` görünen metni yazan anlatım profilidir, araştırma modeli iki yeni kolona yazılır. Neden: satır ↔ cevap eşleşmesi bozulmaz.
24. Hangi sınıf modelin hangi işte iyi olduğuna evalset karar verir; belgede model ailesi iddiası yoktur (§4.6).

**Ayar**
25. Tek yeni alan: `cosre_conversation.evidence` = `off | shadow | on`, varsayılan `off`. Arama argümanları `cosre_v2` ile açık gelir; durum yuvaları mevcut `state`, araştırma ara cümlesi mevcut `style` arkasındadır. Neden: arama operatörün açık isteği ve yalnız çağrıldığında okur; paket her hata sorusunda okuma ekler, o yüzden açılana kadar etkisizdir.

## 4. Sözleşmeler

### 4.1 Arama süzgeci

```
find_traces  (+ mevcut: service, services, operation, status, min_duration_ms, sort_by, env, range_s, limit)
  endpoint   string   "POST /api/orders" | "/api/orders/{id}"; operation ile birlikte verilmez
  client_ip  string   IPv4 / IPv6 tam adres; IPv4 öneki "192.0.2." ya da CIDR /8 /16 /24
  filters    [≤3]     {key, op: eq|ne|in|exists, value ≤160 rune | values ≤10}; kontrol karakteri reddedilir
search_logs  (+ mevcut: words, service, services, severity, trace_id, env, range_s, limit)
  client_ip  string   yalnız tam adres

find_traces →  window {from_ms, to_ms} · entity {service, cluster?, namespace?, ambiguous?}
               applied {endpoint? {operation, route}, client_ip? {value, match: exact|prefix, keys[]}, filters[]}
               traces[≤10] {id, root_op, entry_svc, start_ms, ms, spans, error} · count · has_more · narrowed? · page_url
search_logs →  mevcut gövde + window, entity, applied.client_ip {value, match: "body"}
hatalar     →  unknown_attribute {key, near[≤5]} · endpoint_ambiguous {candidates[≤4]} · endpoint_not_found {near[≤3]}
               bad_client_ip · out_of_scope {service}
```

| Sınır | Değer | Dayanak |
|---|---|---|
| Gövde | ≤2.048 B, satırlar sığana dek düşer | `mcp/v2_traces_logs.go:46`, `v2Fit` |
| Satır | trace ≤10, log ≤10 | `:50`, `:55` |
| Pencere, trace | varsayılan 1 sa; indeksli anahtar ≤6 sa; indekssiz anahtar, önek ya da en yavaş sıralaması ≤1 sa; daraltma `narrowed` ile söylenir | `:49`, `mcp/search_filters.go:36-37`, `:125-150` |
| Pencere, log | varsayılan 1 sa, ≤24 sa | `mcp/v2_traces_logs.go:54` |
| ClickHouse | servis (ya da kullanıcının servis kümesi) + zaman sınırı + `LIMIT 11` + sayım yok (`CountMode: "skip"`, `:244`) + 10 sn bağlam (`mcp/cosre_query.go:66`) + okuyucunun `max_execution_time`'ı; sonuç 30 sn memo, anahtar bütün girdileri özetler | mevcut `find_traces` yolu |
| Derleme | IP: aday anahtarlar üzerinde `=` yapraklı tek OR grubu (`ch/filterexpr.go:541-545`, `ch/repo.go:1986-1991`), attribute hash indeksi varken bloom yolunda; önek: aynı anahtarlarda çıpalı `=~`. `endpoint`: çözülen ad `name =` (tipli kolon). `filters`: `toFilterExpr` + `ValidateFilters` | `mcp/search_filters.go:43`, `ch/filterexpr.go:745` |
| Anahtar kataloğu | `GetScopedAttrs` (5.000 span örneklemi), kapsam başına 60 sn memo; katalogda yoksa `AttrValueProbe` (3 sn) | `ch/attr_discovery.go:105`, `:197` |
| Elasticsearch | `client_ip` yeni sorgu şekli üretmez: `words` ile aynı tek `query_string` yan tümcesi (`"192.0.2.10"`, gövde alanı, `default_operator: AND`, baştaki joker kapalı); `words` de varsa aynı yan tümcede `"<kelimeler>" AND "<ip>"`. Servis ve zaman süzgeci, `size ≤10`, 8 sn tavan, ≤12 kovalı histogram bugünkü gibi. Joker, toplama, yeni alan yok | `internal/logstore/elasticsearch.go:2976-2984`, `mcp/logs_tools.go:66-70`, `mcp/v2_traces_logs.go:619`, `:630` |

Uç nokta çözümü `Deps.ResolveEndpoint` kancasıyla iner (`api/mcp_deps.go` bağlar; `mcp` paketi `api`'yi göremez). Örnek: "192.0.2.10'dan /api/orders'a son 1 saatte gelenler" → `find_traces {service: orders, endpoint: "/api/orders", client_ip: "192.0.2.10"}`, tek tur. "tenant.tier = gold ve hatalı" → `find_traces {filters: [{key: "tenant.tier", value: "gold"}], status: error}`, tek tur. "Kaç tane" sorusu `trace_stats`'ta kalır.

### 4.2 Konuşma durumu ve çözücü

| Yuva | Alan | Çözücü ne zaman yazar | Devralma | Silinme |
|---|---|---|---|---|
| İstemci IP'si | yeni `clientIp` | soruda IPv4 / IPv6 ya da önek biçimi (saf regex) | kısa devam sorusunda, servis / ortam / pencere ile aynı kural (`api/chat_state_resolve.go:169`); yeni IP eskisinin yerine geçer | "hepsi", "tümü", "filtresiz"; durum TTL'i 2 sa (`:51`) |
| Uç nokta | mevcut `operation` | `endpointMention` eşleşmesi çözülünce; "aynı endpoint", "bu endpoint" → devral | aynı kural | yeni açık servis (`:145`, `:155`) |
| Attribute süzgeçleri | mevcut `filters` | araç başarıyla koşunca argümandan (`noteTool`, `api/chat_state.go:286`) | aynı kural | yeni açık servis; "filtresiz" |

Devralınan yuva, model argümanı boş bıraktıysa `find_traces` / `search_logs` çağrısına sunucuda eklenir (`toolScreenScope` deseni, `api/chat_tool_scope.go:64-69`) ve adım çipinde söylenir. `data-state` parçası ve istemci yankısı `clientIp`'yi taşır; yankı IP biçimine doğrulanır. Durum satırına en çok ≈ 60 B eklenir. "ve 198.51.100.7'den gelenler?" → uç nokta ve pencere devralınır, IP değişir. "aynı endpoint için dün" → uç nokta ve IP devralınır, pencere dünün yerel günü olur.

### 4.3 Araştırma akışı

Belirti → hipotez → kanıt → sonuç. P = paket (sunucu, paralel, model turu yok), A = model isterse araç. Sıra sabit değildir: paket hepsini birlikte getirir, model kanıtın desteklediği hipoteze göre A adımlarından birini seçer.

| Adım | Okuma (eş araç) | Giriş | Blok | Durma / dallanma |
|---|---|---|---|---|
| P1 belirti | pencere ve önceki eş pencere RED'i, `service_summary_5m` (`get_service_health`, `compare_periods`) | teknik, istisna | — | iki pencere aynıysa "bu pencerede bozulma görünmüyor" de, pencereyi genişletmeyi öner, dur |
| P2 nerede | ≤3 hatalı operasyon + ≤3 istisna grubu + örnek trace: `internal/sre/readers_errors.go:82` (`get_operation_health`, `list_exception_groups`) | teknik | `exception` | tek grup hataların çoğunu taşıyorsa istisna yoluna geç |
| P2' grup | adı verilen istisna: grup satırı + örnek + ≤5 çerçeve (`list_exception_groups`, `get_exception_samples`) | istisna | `exception` | grup pencerede yoksa söyle, pencereyi genişletmeyi öner, dur |
| P3 ne değişti | pencere başına yakın deploy / değişiklik: `internal/sre/orchestrator.go:49` (`list_deployments`, `get_correlated_changes`) | ikisi | — | deploy hata başlangıcından (ya da grubun ilk görülmesinden) hemen önceyse hipotez "değişiklik" |
| P4 bağımlılık | aşağı akış kenarları (DB, mesajlaşma, servis): `internal/sre/deps.go:84` (`get_topology`) | teknik; istisnada yalnız tür zaman aşımı / bağlantı ise | — | en kötü kenar belirtiyle birlikte bozulduysa hipotez "bağımlılık"; A: `get_db_health` / `get_messaging_health` / o servisin sağlığı |
| P5 anomali | ≤3 ML olayı: `internal/sre/readers_anomalies.go:140` (`get_log_anomalies`, `explain_ml_event`) | ikisi | `anomaly` | başlangıç anını doğrular ya da düzeltir |
| P6 örnek trace | P2 / P2'nin örnek trace'i (`explain_trace`), dalga 2 | ikisi | `trace` | hatalı span başka servisteyse bulgu o servisi adlandırır; zincir izlenmez, sonraki adım olarak önerilir |
| P7 trace logları | `search_logs trace_id`, dalga 2, ES'e tek sorgu | ikisi | `logs` | — |
| A8 doygunluk | `get_pod_health`, `cluster_metric` | teknik, belirti gecikme ve P3–P4 temizse | — | — |
| A9 dağılım | `endpoint_metrics`; `find_traces` (endpoint / client_ip / filters) | hata tek rotada ya da tek istemcide mi | `trace_list` | — |
| A10 log deseni | `explain_log_spike`, `log_patterns` | P7 boşsa | mevcut `log_spike` | — |
| K3 bilgi | bulgudan runbook / wiki (sunucu adımı) | K3 gelince | `source` | bu tasarımda yok |

- **Paket bütçesi:** dalga 1 (P1–P5) ≤4 sn, dalga 2 (P6–P7) ≤4 sn, adım başına 3 sn; süresi dolan ya da hata veren adım `unchecked[] {step, reason}` olur, paket yine teslim edilir. Okumalar mevcut, ön-toplam öncelikli okuyuculardır (`service_summary_5m`, `operation_summary_5m`, `topology_edges_5m`, istisna grubu ve `ml_events` okuyucuları) + kimlikle tek trace + tek ES sorgusu; yeni sorgu yazılmaz, hepsi kendi `LIMIT` ve `max_execution_time` sınırıyla koşar.
- **Modele giden:** tek sentetik araç çifti `evidence_pack`, ≤2.048 B: `{service, entity, window, red {err_pct, prev_err_pct, p95_ms, prev_p95_ms, rps}, error_ops[≤3], exceptions[≤3] {type, message ≤120, count, first_seen_ms, last_seen_ms, sample_trace}, deploys[≤3] {version, at_ms}, deps[≤3] {target, kind, err_pct, p95_ms}, anomalies[≤3], trace {id, error_span, top_path[≤3]}, logs {total, samples[≤3]}, unchecked[]}`. Ham satır, yığın ve seri gitmez; bunlar blok adayıdır.
- **Döngü:** tavanlar aynen. Beklenen: 1–3 model turu. Model pakette olan bir okumayı yeniden çağırırsa sonuç memodan gelir.
- **Kullanıcının gördüğü:** paket başlarken tek ara cümle ("checkout için hata izlerini topluyorum."; `style` arkasında, mevcut `data-status`), paket adımı başına bir adım çipi ("hata özeti: 3 operasyon, 2 istisna grubu", "atlandı: bağımlılık (zaman aşımı)"), sonra bugünkü araç çipleri.
- **Durma:** (1) hipotez iki bağımsız kanıtla tutuyor (örnek: grup sayısı + örnek trace'in hatalı span'i; deploy saati + hata başlangıcı) → cevap. (2) Tavan turu → eldekiyle cevap + bakılamayanlar. (3) Paket bozulma göstermiyor → bunu söyle, tek öneri.
- **Tura eklenen istem bölümü** (yalnız araştırma turunda, istemin sonuna; ≤650 B, test pinler):

```
ARAŞTIRMA: evidence_pack bu servisin bu penceredeki ölçülmüş hâlidir; aynı okumayı yeniden çağırma.
Belirtiyi söyle, pakete uyan en olası tek nedeni seç, onu doğrulayacak ya da çürütecek TEK okumayı çağır;
kanıt iki bağımsız kaynakta tutuyorsa dur ve cevapla.
Cevap: ilk cümle bulgu; istisna adı ya da operasyon, sayı, başlangıç saati ve bir örnek trace; emin değilsen
"muhtemelen" de; elenen nedenleri tek yan cümleyle an; unchecked'te olanı "bakamadım" diye söyle; tek sonraki adım.
```

- **Cevap örneği** (biçim; sayılar araç çıktısından, doğrulayıcı sayar): "checkout'ta hatalar 14:05'te başladı; neredeyse hepsi `SocketTimeoutException`, son bir saatte 412 kez, payments çağrısında. Örnek trace'te hatalı span payments'ın `POST /api/payments` çağrısı. Deploy yok, veritabanı tarafı temiz; pod durumuna bakamadım. İstersen payments'ın aynı penceresine bakayım."

### 4.4 Kanıt blokları ve ekleme

Mekanizma, ortak alanlar ve tavanlar kanıt denetimi §5.1–§5.2'deki gibidir. En az yük:

| Blok | En az yük | Üreten |
|---|---|---|
| `exception` | `type`, `message ≤160`, `count`, `firstSeenMs`, `lastSeenMs`, `sampleTraceId`, `frames ≤5`, `entity`, `window`, `href` | paket P2 / P2'; `get_exception_samples` |
| `trace` | `traceId`, giriş servisi + operasyon, `startMs`, `durationMs`, `errorSpan {spanId, service, name, exceptionType}`, `entity`, `href` | paket P6; `explain_trace` |
| `logs` | görünen sorgu, `total`, `rows ≤10 {ts, sev, service, body ≤160, traceId?}`, `window`, `href` | paket P7; `search_logs` |
| `anomaly` | `id`, `detector`, `signal`, `startedMs`, `observed`, `baseline`, `sampleTraceIds ≤3`, `href` | paket P5; `explain_ml_event` |
| `chart` (`recipe: "metric"`) | `metric` (katalog kimliği), `entity`, `window`, `stats`; seri taşımaz | K2; bu tasarımda üretilmez |
| `trace_list` (mevcut tip) | bugünkü yük | `find_traces` (S3'te eklenir) |

Ekleme kuralı kanıt denetimi §5.4: canlı veriye dayanan iddia, "göster" isteği ya da eyleme yönlendirme; cevap başına ≤2, "göster" turunda ≤4, tür başına 1; bilgi ve sohbet sınıfında 0. Araştırma cevabında öncelik `exception` > `trace` > `logs` > `anomaly`. Eşleme doğrulayıcının eşleşen değerinden; kaynak bağı alınamıyorsa yedek kural: tür başına son başarılı aday. Blok üreten sonucun modele giden hâli özetidir (≤600 B); ham veri modele dönmez. `shadow`'da karar yalnız ekranda çizilmeyen `evidence_policy` adımına yazılır (`answer_class` emsali).

### 4.5 Profil ayrımı

| Konu | Karar |
|---|---|
| Etkinleşme | `chat-research` haritada eşli **ve** profili `chat`'in çözüldüğü profilden farklı **ve** açık profil seçimi yok, Derin değil, trace takibi değil, alışveriş yerel profile sabitlenmemiş **ve** sınıf durum / hata avı / karşılaştırma / açıklama. Aksi hâlde plan adımı boştur: istek baytı bugünküyle aynı. |
| Eşleme | Yüzey gruplarına üçüncü grup `research` = {`chat-research`}; Ayarlar › AI profillerinde üçüncü seçici. Yeni blob, yeni uç yok (`cop/profiles.go:785-790`, `api/ai_settings_profiles.go:333`). |
| Araştırma turları | Normal araçlı turlar (`stepNormal`) `chat-research` yüzeyiyle çağrılır (`prepareStep` adımı + `copilot.WithSurface`). Metinleri kullanıcıya akmaz; ara cümle ve çipler akar. |
| Cevap | Araştırma modeli araçsız dönünce metni "bulgu notu" olur (≤600 B, gösterilmez) ve sunucu `chat` yüzeyinde araçsız, akan tek anlatım çağrısı yapar. Tavan turunda araştırma taslağı atlanır, doğrudan anlatım çağrılır. Bozuk-cevap yeniden denemesi anlatım profilinde, bugünkü tavanla. |
| Anlatıcının girdisi | Sistem: üslup çekirdeği + durum bloğu + örnekler + sınıf satırı + ≈ 300 B'lik ek ("sayı ve saat yalnız ÖZETLER'den; BULGU NOTU kanıt değildir"). Kullanıcı mesajı: son dört turun metni (≤1,5 KB) + soru + ÖZETLER (bu alışverişin araç sonuçlarının model görünümü, yeniden eskiye, sonuç başına ≤900 B, paket dahil toplam ≤6 KB) + BULGU NOTU. Katalog ve araç mesajı gitmez. Doğrulayıcı anlatıcının metnini bugünkü gibi araçların tam çıktısıyla sayar. |
| İlk turda araç çağrılmazsa | Alışveriş `chat` profilinde, araçlı ve akan normal tura düşer (bugünkü davranış); sinyal `research_no_tools`. |
| Bağlam penceresi | Tur bütçesi pencereyi ve bayt / token oranını profil başına çözer (bugün alışveriş başına bir kez). Anlatım girdisi yapısı gereği ≈ 3.400 token'dır. Araç seti araştırma profiline göre seçilir, çünkü kataloğu o görür. |
| Hata | Araştırma çağrısı (mevcut tek ağ geçidi yeniden denemesinden sonra) başarısız → kalan turlar `chat` profilinde, çip "araştırma modeli yanıt vermedi", sinyal `research_fallback`. Anlatım başarısız → bulgu notu cevap dikişinden geçip yayınlanır, sinyal `narrate_fallback`. |
| Kayıt | Tek `ai_calls` satırı; `model` = anlatım profili, token'lar toplam. Yeni kolonlar `loop_research_model String DEFAULT ''`, `loop_research_tokens UInt32 DEFAULT 0` (`loop_compacted_results` emsaliyle ayrı probe). İlk token süresi anlatım çağrısınındır. |

### 4.6 Hızlı sınıf model için beklenti ve ölçüm

Depodan doğrulanamayan genel beklentilerdir, karar değildir. Hızlı ve küçük bir model araç turlarında muhtemelen daha kısa tur süresi verir, yani aynı duvar süresine daha çok okuma sığar; araç çağrısı biçimine uyumu iyi olabilir. Muhtemelen daha zayıf kalacağı yerler: uzun bağlamda (21 bin token'lık katalog + sonuçlar) doğru aracı ve argümanı seçmek, Türkçe anlatımda doğal ihtiyat ve kısalık, küçük pencerede eski sonuçların kısaltılmasına dayanmak. Karar verecek ölçüm:
- **Tek profil, iki koşu:** ajan kipi (`cop/evalset/agent_loop.json`) araç seçimini, konuşma kipi iki profili yan yana akıcılığı ve doğrulayıcı oranını ölçer; bugün mümkündür, S6'yı beklemez.
- **Ayrık koşu (S6):** koşu isteğine yalnız değerlendirmede okunan `researchProfile` alanı; rapor üç sütun olur (A tek, B tek, B araştırır → A anlatır). Bakılan: beklenen araç çağrısı tutma oranı, tur sayısı, duvar süresi, giriş token'ı, doğrulayıcıdan geçen tur payı, akıcılık ölçütleri, `narrate_fallback` ve `research_no_tools` sayısı.
- **Eklenecek vakalar:** §5'teki arama ve araştırma vakalarının hepsi ayrık koşuda da koşar; ek olarak `conv-split-digest-detail` (cevabın özetlerdeki üç değeri taşıması: istisna adı, sayı, saat) ve `conv-split-followup-address` (siz hitabı ve "peki dün?" devamı anlatıcıda korunur).

### 4.7 Ayar tablosu

| Anahtar | Varsayılan | Bu tasarımda neyi açar |
|---|---|---|
| `cosre_v2` | kurulumdaki değer | arama argümanları (S2) |
| `cosre_conversation.state` | açık | arama yuvalarının devralınması (S2) |
| `cosre_conversation.style` | kapalı | araştırma ara cümlesi; mevcut ölçü, örnekle üslup, kalıp süzgeci |
| `cosre_conversation.verification` | shadow | değişmez; araştırma cevabı ve anlatıcı metni de sayılır |
| `cosre_conversation.evidence` (yeni alan) | off | `shadow`: kapsam reddi ve ekleme kararı yalnız sayılır. `on`: `out_of_scope`, bloklar ve model görünümü, kanıt paketi + istem bölümü |
| Yüzey haritası `chat-research` | eşlenmemiş | profil ayrımı (S6) |

## 5. Dilim planı

Her dilim kendi sürümü, doğrudan main, `scripts/gate.sh all`, hepsi `cosre_v2` arkasında, v1 baytı değişmez. S5, S4 ile ayrık dosya kümesinde yan yana yürüyebilir; sürümler yine tek tek iner.

| Dilim | Kapsam | Dosyalar | Test ve evalset | Altınlar | Risk | Tur başına token | Varsayılan |
|---|---|---|---|---|---|---|---|
| **S1** kimlik, kapsam, `evidence` alanı (K1 dar) | Bloba `evidence`; PUT doğrular; Settings › CoSRE'de üçlü seçim. `ServiceRef` alışveriş başına bir kez varlık modelinden. Saf `scopeDecision` `chat.go:1484` dikişinde: shadow → `scope_would_refuse` sinyali, on → `out_of_scope`. v2 araç sınıflandırma pini | `ch/cosre_conversation_settings.go`, `api/cosre_conversation_settings.go`, `api/chat_user_scope.go`, yeni `api/chat_scope_decision.go`, yeni `mcp/scope_ctx.go`, `api/mcp_deps.go`, Settings › CoSRE satırı | karar tablosu (kapsamda / bu turda anılan / durum varlığı / kaçış / kapsam dışı); off ve shadow'da akış bayt aynı; sınıfsız yeni araç testi kırar; aynı ad iki (cluster, namespace) çiftinde. Vaka `conv-scope-inferred-service`: kapsam checkout + orders, model payments'a giderse red, cevap payments sayısı taşımaz | yok | orta: `on`'da yanlış red "veri yok" diye okunur; önce shadow sayacı | 0 | etkisiz (`off`) |
| **S2** arama | §4.1 ve §4.2: `find_traces` + `search_logs` argümanları, saf süzgeç planı, anahtar doğrulama, uç nokta kancası, IP anahtar grubu, `clientIp` yuvası, devralma ve enjeksiyon; v2 istem çeşidinde `search_traces` → `find_traces` + tek cümle | `mcp/v2_traces_logs.go`, yeni `mcp/v2_search_filter.go`, `mcp/tools.go` (kanca), `api/mcp_deps.go`, `api/chat_endpoint_prefetch.go` (sarmalayıcı), `api/chat_context.go`, `api/chat_state_resolve.go`, `api/chat_state.go`, `api/chat_tool_scope.go`, `cop/prompts.go` (yalnız v2 accessor), `fe/lib/cosre/protocol.ts` (durum tipi) | plan tablosu (tam v4 / v6, önek, /24 kabul, /20 ve regex işleci red, 4. süzgeç red); anahtar doğrulama üç yol; uç nokta üç sonuç; gövde ≤2.048 B; önbellek anahtarı bütün girdiler; ES gövdesi yalnız `query_string` (joker / toplama yok); çözücü tablosu; doğrulayıcı IP'yi sayı saymaz; v1 katalog özeti aynı. Vakalar `conversation/search.json`: `conv-search-ip-endpoint` (orders, 192.0.2.10, /api/orders → devam 198.51.100.7 → "aynı endpoint için dün"), `conv-search-attr-failed` (payments, tenant.tier = gold), `conv-search-unknown-key`; ajan kipi `agent-search-client-ip` | `mcp/testdata/toolset_catalog_golden.json` `zero/v2` ve `conditional/v2` özeti (ad listesi aynı); v2 istem SHA pini yenilenir, v1 pini aynen; `.sse` altınları değişmez | orta: OR grubu ve attribute süzgeci trace ön-toplamının hızlı yolunu kapatır (bugünkü her attribute süzgeci gibi); v2 istem satırı değişir | ≈ +1.250 B katalog + 150 B istem ≈ **+470 / v2 model çağrısı**; IP ya da attribute listesi sorusunda 2–3 tur yerine 1 (eksilen tur başına ≈ −25.000) | `cosre_v2` ile açık |
| **S3** kanıt blokları + ekleme kuralı (K2 dar + K4a) | Dört yeni blok tipi + `find_traces` için `trace_list`; yük kurucuları; model görünümü; `explain_trace`'e hatalı span kimliği, `get_exception_samples`'a grup satırı + çerçeveler (yalnız ek alan); ekleme kararı; üç ölçüt | `internal/ai/agent/blocks/block.go`, yeni `mcp/evidence_parts.go`, `mcp/v2_traces_logs.go`, `mcp/exception_samples.go`, `mcp/v2_ml.go`, `chat.go` (sonuç dikişi), yeni `api/chat_evidence_policy.go`, `api/ai_evalset_conversation.go`, yeni `internal/ai/evalrubric/evidence.go`, `api/ai_evalset_stream_contract.go`, `fe/lib/cosre/protocol.ts` (tip listesi) | kurucu tablo testleri; model görünümünde satır ve yığın yok; karar tablosu (tavan 2 / 4 / 0, tür başına 1, yedek kural); off'ta akış ve araç metni bayt aynı; ölçüt fonksiyonları. Vakalar `conversation/evidence.json`: `conv-evidence-show-me`, `conv-evidence-none-on-knowledge`, `conv-evidence-action-needs-proof` | yeni `ui-stream-v1-evidence` çifti; `v2_loop.sse` ve diğerleri `off`'ta aynen (kanıt denetiminin K2 satırı `v2_loop.sse`'nin yenileneceğini yazıyordu; alan arkasında olduğu için gerekmez) | orta: dış MCP sözleşmesine ek alan | `on`'da blok üreten sonuç 683 → 100–200; kapalıyken 0 | etkisiz |
| **S4** araştırma (K4b'nin araştırma bölümü) | Tetik (karar 14); `internal/sre` okuyucularına dışa açık ince sarmalayıcı; iki dalgalı paket; özet; sentetik araç çifti; adım çipleri ve ara cümle ailesi; tura eklenen istem bölümü; paket blokları ekleme kuralına aday | yeni `internal/sre/chatpack.go`, yeni `api/chat_investigate.go`, `chat.go` (tohumlama ve kademe atlama koşulu), `api/chat_prepare_step.go`, `api/chat_status_line.go`, `cop/prompts.go` (ek sabiti), `api/chat_evidence_policy.go` | tetik tablosu; sahte okuyucularla paralellik, adım tavanı, zaman aşımı → `unchecked`; özet ≤2.048 B; ES sorgu sayısı ≤1; tur ve çağrı tavanı değişmedi; trace takibi / Derin / rapor sınıfında koşmaz; kapalıyken akış bayt aynı. Vakalar `conversation/investigate.json`: `conv-investigate-5xx-after-deploy` (checkout), `conv-investigate-exception-downstream` (orders → payments), `conv-investigate-nothing-wrong` (inventory), `conv-investigate-partial-pack`; ajan kipi `agent-investigate-exception` | yeni `ui-stream-v1-investigate` çifti; diğerleri aynen | orta-yüksek: ipucu listesi rotayı değiştirir; hata sorusu başına en çok sekiz sınırlı okuma eklenir | araştırma turunda özet ≤ +683 / tur + bölüm ≈ +215; karşılığında ≈ 3 araç turu eksilir (≈ **−75.000 / alışveriş**) | etkisiz (`evidence: on` ister) |
| **S5** çiziciler (K6 dar) | `exception`, `trace`, `logs`, `anomaly` çizicileri mevcut bileşenlerle (`ExceptionSampleRow`, `TraceWaterfall`, `LogTable`, `MLRecordCard`), hepsi tembel; `MessageList`'ten; v1 `ChatBubble`'a ve uPlot sarmalayıcılarına dokunulmaz | yeni `fe/components/cosre/evidence/parts/`, `fe/components/cosre/conversation/MessageList.tsx`, yeni `fe/lib/cosre/evidenceParts.ts` | vitest bileşen testleri; `uiStreamFixture.test.ts`; geri yükleme testi; `bundle:check` (ölçülen +0,9 KB gz, kanıt denetimi §4) | Go altını yok | düşük-orta | 0 | blok gelince çizer |
| **S6** profil ayrımı (K5) | §4.5; `research` grubu ve seçici; plan adımı; anlatım çağrısı; profil başına pencere; iki kolon; evalset `researchProfile` | `cop/profiles.go`, `api/ai_settings_profiles.go`, `fe/pages/settings/AiProfilesPanel.tsx`, `api/chat_prepare_step.go`, yeni `api/chat_profile_split.go`, `chat.go` (döngü çıkışı), `api/chat_round_budget.go`, `cop/prompts.go`, `cop/loop_usage.go`, `ch/` `ai_calls` kolonları, `api/ai_evalset_conversation.go` | etkinleşme tablosu (eşleme yok / aynı profil / açık profil / Derin / trace takibi / sohbet sınıfı); eşleme yokken istek baytı aynı; araştırma metni hiçbir `text-delta`'da yok; üç geri düşüş; `researchProfile` üretimde okunmaz; §4.6 vakaları | yeni `ui-stream-v1-split` çifti; eşleme yokken `v2_loop.sse` aynen | orta: ilk görünen metin gecikir; anlatıcı ayrıntı kaybedebilir | eşliyken araç kullanan turda ≈ **+3.400 giriş** (tek anlatım çağrısı; katalog gitmez); eşleme yoksa 0 | etkisiz (eşleme ister) |

**Bilerek dışarıda:** `chart` katalog biçimi ve `metric_chart` (K2); bulgudan bilgi ve `source` (K3); ~25 işleyicide bağlam süzgeci (K1'in kalanı); logda attribute süzgeci; sekizli dışı CIDR ve `x-forwarded-for`; `trace_stats` değişikliği; Derin kipte paket; birden çok servisi aynı anda araştırma; araştırma taslağının akması; dış MCP araçları; v1.

## 6. Operatörün açma sırası

1. **S2 sonrası, anahtar yok.** İki soruyu dene: "orders'a son 1 saatte 192.0.2.10'dan gelen istekleri göster", ardından "aynı endpoint için dün". Bak: AI çağrı kaydında `find_traces` argümanları, `narrowed` ve `unknown_attribute` sıklığı.
2. **`style` aç.** Önce konuşma kipi evalset'ini `style: true` ile koş. Bak: durum turlarının cümle sayısı, "Kalıp — model yazdı" oranı.
3. **`verification`.** Bir hafta shadow; `verify_mismatch` azsa `enforce`.
4. **`evidence: shadow`** (S1 ve S3 sonrası). Bak: `scope_would_refuse` sayısı ve `evidence_policy` adımları. Yanlış red görünmüyorsa **`on`** (S4 sonrası): "checkout'taki hatayı incele" de; ilk baktığın yer paket çipleri, `unchecked` ve cevabın altındaki iki kart.
5. **İkinci profil.** Önce bugünkü yolla tek profil: sohbette profili seç ya da evalset'i iki profille koş. Sonra S6 ile `chat-research`'e eşle. Bak: tur süresi, `narrate_fallback`, doğrulayıcıdan geçen tur payı; kötüyse eşlemeyi sil, davranış aynen geri gelir.

**Sohbet hissinde kapanan boşluklar:** arama devam soruları (IP, uç nokta, süzgeç devralınır); araştırmada beş sessiz tur yerine birkaç saniyelik paket + ara cümle; cevabın altında kanıt; araç turlarının hızlı modelde koşması. **Kalanlar:** araştırma taslağının canlı akması, İngilizce ara cümle, 2 saatten eski bağlam, grafik kartı (K2), bilgi kaynağı kartı (K3).

## 7. Koddan doğrulanamayanlar

| Doğrulanamayan | Hangi prod değeri kapatır |
|---|---|
| Span'lerde hangi istemci adresi anahtarlarının dolu olduğu | bir giriş servisinde `describe_attributes`; aday anahtar başına sayım |
| Attribute hash indeksinin hazır olması (6 sa mı 1 sa mı) | attribute indeksi yönetim durumu |
| Log gövdesinde istemci IP'sinin yazılması; ES'te ayrı alan bulunması | örnek belge ve indeks eşlemesi |
| İki profilin bağlam penceresi ve araç çağrısı biçimi | ağ geçidi model kartı; pencere yoklamasının kaynağı; "metinden ayrıştırıldı" çipinin sıklığı |
| Hata avı alışverişinin bugünkü tur sayısı ve giriş token'ı (−75.000 tahmini) | `ai_calls` tur sayısı ve en büyük giriş token'ı, sınıf hata avı |
| Paket okumalarının süresi (8 sn bütçesi) | arka plan araştırmasının aynı okuyuculardaki adım süreleri |
| Doğrulayıcının eşleşen değeri kaynağına bağlayabilmesi | okunmadı; S3 yedek kuralla başlar |
| Hata sorularının ne kadarını guided rotaların sahiplendiği | etkileşim kaydında kademe dağılımı |
| Kapsamlı (takımı olan) kullanıcı payı ve yanlış red riski | `scope_would_refuse` shadow sayacı |
| Model ailesi başına gerçek bayt / token oranı | tur bütçesinin ilk turdaki kalibrasyonu |
