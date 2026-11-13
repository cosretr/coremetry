# CoSRE yol haritası durumu — Faz 0 audit

Tarih 2026-11-13. Salt okunur; kod değişmedi. En son etiket `v0.10.2098` (`.git/refs/tags`).
Yöntem sınırı: bu oturumda `git log` koşulamadı. Sürüm kanıtı = etiket dosyaları
(`.git/packed-refs`, `.git/refs/tags`), `docs/frontend/migration-log.md`, `docs/DECISIONS.md`
başlıkları ve yerel reflog. "çıkarım" yazan satırlar koddan okunmadı.
Kısaltma: `api/` = `internal/api/`, `mcp/` = `internal/mcptools/`, `fe/` = `frontend/src/`.

## Onay — 2026-11-13

Operatör bu audit'i beş kararla onayladı (§7 sorularının cevapları):
1. Araç seti dilim 3, React 19, tembel chunk ve shadcn temeli BİTTİ, sıradan çıkar. Uygulama v2
   sohbet arayüzünün kapanışıyla başlar (plan: `docs/cosre/v2-ui-closeout.md`).
2. v1 emekliliğinde mesaj balonu ve composer ortak kalır; yalnız v1 kabuğu, yazıcısı ve rotası
   silinir. AI Elements KURULMAZ.
3. Deneyim (iş 6) ile kısa özet + link kartı (iş 8) TEK ortak audit belgesi alır.
4. Giriş özeti login sayfasına taşınmaz, v2 karşılamasında kalır; ayar satırı + prod ölçümüyle kapanır.
5. LLM hakem gece işinde kalır, kapı değildir; kalite kapısı deterministik rubrikle başlar.

## Kalıcı kurallar (uygulama sırasında geçerli)

- Aynı anda tek iş. Bitti = main'de, testler yeşil, bayrak açılabilir, doküman güncel.
- `internal/api/api.go` büyümez; yeni uç kendi dosyasında `registerXxxRoutes()` ile.
- Sürüm doğrudan main'e; deploy operatörde.
- v1'e yeni davranış eklenmez.
- Kod içeren LLM isteği yalnız kendi barındırılan modele gider.
- PII maskeleme özelliği yok. Gerçek kurum adı yok.
- Audit dokümanı olmayan iş koda başlamadan durur ve sorar.

## 1. Özet

Sıranın ilk dört kalemi zaten main'de: v2 araç seti dilim 3 (`v0.10.1688`), React 19
(`v0.10.1600`), CoSRE tembel chunk (`v0.10.1599`) ve shadcn Faz 1 (`v0.10.1608–1619`). "React
18.3.1 pini" artık yok; yalnız bayat bir yorum kaldı. v2 sohbet arayüzü ve giriş özeti de gemide
ama ikisi de varsayılan kapalı bayrak arkasında ve prod'da doğrulanmadı. En önemli bulgu: v2
ayrı bir backend değil. `cosreV2Chat` v1 gövdesini aynen çağırır (`api/cosre_v2_chat.go:54`);
niyet, kademe, araç ve akışların tamamı ortaktır. Arayüzde de v2, v1'in balonunu ve
composer'ını kullanır (`fe/components/cosre/conversation/CosreChatV2.tsx:73`,
`MessageList.tsx:25`). Yani "v1'i emekli et" büyük bir silme değil; bir yazıcı, bir rota, bir
kabuk ve bunları pinleyen testlerin kaldırılmasıdır. Gerçekten açık olan işler: deneyim (audit
yok), kısa özet + link kartı (audit yok, kod yok), kalite kapısı (CI'da kapı yok),
`search_knowledge` ADO backend'i (audit onay bekliyor).

## 2. İş akışları

| # | İş | Durum | Main'de ne var | Kalan iş | Neyi bekliyor | Dilim |
|---|---|---|---|---|---|---|
| 1 | v2 araç seti dilim 3 | **kapalı, sıra dışı (onay 2026-11-13)** | `find_traces`, `search_logs`, `explain_trace` yeniden yazıldı: `mcp/v2_traces_logs.go:332,577,730`, testleri var; v2 yolunda kayıtlı `api/copilot_chat.go:780-783`. `v0.10.1688` (`migration-log.md:73`). 3b `endpoint_metrics` `v0.10.1708`, dilim 4 bütçe `main.go:1062` | Yok. `briefing-audit.md:13,184` "dilim 3 yok" notu bayat | — | 0 |
| 2 | React 19 + `@grafana/ui` | **kapalı, sıra dışı (onay 2026-11-13)** | `react` 19.3.1 `frontend/package.json:47-48`, `v0.10.1600` (`shadcn-phase1-plan.md:332`). `@grafana/ui` 13.1.2 `:26`, `overrides` ile `:76-100`; pin `fe/lib/chart/depPins.test.ts:38-47`. Tek ithalatçı `fe/components/chart/CorePanel.tsx:37` | Bayat yorum `depPins.test.ts:8` ("18.3.1 pin"). `@grafana/ui` 13.2+ gelince `overrides` silinir (`depPins.test.ts:37`) | Dış sürüm (13.2) | 1 (yorum) |
| 3 | CoSRE sohbeti tembel chunk | **kapalı, sıra dışı (onay 2026-11-13)** | `CopilotChatGate` statik, v1 ve v2 `React.lazy`: `fe/components/CopilotChatGate.tsx:35-36`; `AppShell.tsx:8,287`. Bütçe kapısı `frontend/scripts/bundle-budget.mjs:43-56`. `v0.10.1599` | Yok | — | 0 |
| 4 | shadcn Faz 1 | **kapalı, sıra dışı (onay 2026-11-13)** | R1–R10 `v0.10.1608–1619` (`migration-log.md:13-22`); `fe/components/shadcn` 25 dosya; 157 dosya tüketiyor | Faz 1 için yok. Sayfa dilimleri ayrı program (Settings 1629–1693, Traces S3b 2093) | — | 0 |
| 5 | v2 sohbet arayüzü, `cosre_v2` | kısmen | B1–B7 `v0.10.1647–1672` (`migration-log.md:41-62`); `/cosre` tam sayfa `v0.10.1785–1788` (`DECISIONS.md:12155`). Kanca `fe/lib/cosre/useCosreChat.ts`; `data-reset`/`textId` haritası uygulanmış ve testli (`useCosreChat.ts:97-101`, `textIdMap.ts`, `resetReducer.ts`). Bayrak varsayılan kapalı `api/cosre_v2_flag.go:19,50-56` | (a) prod/tarayıcı doğrulaması, (b) agent-core S1–S6 durum tespiti (`agent-core-ideas.md:216-223`), (c) P1 bayrak varsayılanı açık, P3 defter (`phase-2b-plan.md:473-478`). AI Elements kurulmayacak (onay 2026-11-13). Kapanış planı: `docs/cosre/v2-ui-closeout.md` | Operatör prep testi | 3 |
| 6 | Deneyim: niyet, seçenekli soru, kaynak çipi | **tamam (gemide; prod doğrulaması operatörde)** | `docs/cosre/experience-audit.md` D1–D3, D5: `data-question` parçası `v0.10.2100`; kaynak sorusu kartı `v0.10.2102`; doküman/varlık + trace (evet/hayır) kartı `v0.10.2103`; arşiv (pasif kart, seçim saklı) `v0.10.2104`. Sunucu `api/chat_question.go` (`clarifyQuestion`, `clarifyOptionClass`), FE `QuestionCard.tsx`. Düz metin soru akışta aynen; v1 değişmez | Prod/tarayıcıda denenmedi (kaynak sorusu tık, serbest metin, Oracle "evet"). Guided takım/servis çipleri karta taşınmadı (kapsam dışı, §8 S1) | Operatör prod doğrulaması | 0 |
| 7 | Giriş özeti | **kod tamam (`v0.10.2105`); prod ölçümü operatörde** | Faz 1 kapandı `v0.10.1690–1719` (`briefing-audit.md:282-291`). Paket `internal/cosre/briefing/`, uçlar `api/cosre_briefing.go:39-40`, üretici `main.go:1638`. Arayüz yalnız v2 karşılamasında (`BriefingFeed.tsx:123`); login sayfasına taşınmaz (Onay 4). Ayar: Settings › CoSRE tek aç/kapa satırı `fe/pages/settings/CosreBriefingLine.tsx` (`v0.10.2105`; PUT bütün blobu aynen taşır, audit `settings.cosre_briefing.update`) | Varsayılan kapalı kalır (`briefing/briefing.go:90`); R9 maliyeti ve 1716–1719 prod'da denenmedi; Faz 2 ertelemeleri (`briefing-audit.md:286-290`). Ölçüm adımları `briefing-audit.md` "Kapanış" | `cosre_v2` açık olmalı; operatör prod ölçümü | 1 |
| 8 | Kısa özet + link kartı | **tamam (gemide; prod doğrulaması operatörde)** | `experience-audit.md` D4 + D5: deterministik `guidedSummaryTR` (`api/chat_summary_card.go`) + `data-links` `title`/`summary` `v0.10.2101`; üç rota (service_health, problems, my_services), ≤3 iç link; FE `SummaryLinkCard.tsx`. Arşiv (kart ve link çipleri kaydedildiği hedeflerle) `v0.10.2104` | Prod/tarayıcıda denenmedi. Diğer rotalar prod ölçümünden sonra (audit §3.3) | Operatör prod doğrulaması | 0 |
| 9 | Kalite döngüsü | kısmen (D1–D4 gemide) | Birleşik audit `docs/cosre/quality-loop-audit.md` (onaylı 2026-11-13). Koşucu v2 yolunda + `ai_eval_runs.path` `v0.10.2106`; rubrik `stream_contract` `v0.10.2107`; modelsiz `cosre-contracts` kapısı (gate + CI) `v0.10.2108`; günlük v2 koşusu worker'da (`cosrelearn/evalset_daily.go`, `api/ai_evalset_daily.go`; kilit `cosre-evalset-daily`, UTC + 2 sa, 30 dk, fren `system_settings` `cosre_evalset_daily`, yalnız yerel profil) `v0.10.2109`. Sinyal `POST /api/ai/signal`, gece hakemi, panel `fe/pages/ai/CosreQualityPanel.tsx` | D5 panel trendi + eşik altı rozeti; D6 aday vakalar (`saved_views`) | Prod: günlük koşu süresi, `ai_calls` yükü, ES sayacı (2 gün; operatörde) | audit D5–D6 |
| 10 | `search_knowledge` ADO + RAG | kısmen | Araç var, yalnız sözcük eşleşmesi: runbook + RAG parçası (`mcp/knowledge_tools.go:328-394`); v1 ve v2 kataloğunda (`mcp/tools.go:535`). ADO wiki ayrı araçlarda: `search_wiki`, `read_wiki_page` (`mcp/wiki_tools.go:80,170`, `DECISIONS.md:2394`). SharePoint kodu yok | ADO backend'lerinin `search_knowledge`'a bağlanması: Phase 1–4 (`search-knowledge-audit.md:407-422`) | Audit onaysız (`search-knowledge-audit.md:3`, D1–D12) | 7 (audit'e göre) |
| 11a | Park: kod RCA | var (gemide) | "Kodu da incele" `api/chat_code_turn.go:108`, `v0.10.1406`; yalnız yerel profil | Yeni kapsam tanımsız | Belge yok | — |
| 11b | Park: kanal kodu çözme | var (gemide) | `resolve_business_code` `mcp/business_code.go:3,34`; v2 sezgisi `mcp/v2_traces_logs.go:69` | Yeni kapsam tanımsız | `tools-audit.md:129-153` | — |
| 11c | Park: görev bazlı model yönlendirme | başlamadı | Yalnız yüzey profili `internal/copilot/profiles.go:287-324` | Tamamı | Evalset puanı; belge yok | — |
| 11d | Park: LLM hakem | var (çalışıyor) | Gece işinde `cosrelearn/grounding.go:21,136`, `main.go:1078-1084` | Kapı değil (onay 2026-11-13); gece işinde kalır | — | — |
| 11e | Park: RAG vektör arama | var, kullanılmıyor | `api/rag.go:381-388`, rotalar `:45-57`, `rag/rag.go:161` | Söküm ya da canlandırma | Ayrı operatör onayı | — |

Sayım (1–10): bitti 4, kısmen 5, başlamadı 1.

## 3. v1 ↔ v2 farkı

Temel: iki uç aynı gövdeyi çağırır. v1 `copilotChat` → `copilotChatWith(…, v1ChatSink)`
(`api/chat_sink.go:40-42`); v2 → `copilotChatWith(…withCosreV2Loop…, v2ChatSink)`
(`api/cosre_v2_chat.go:54`). Gövdede `!cosreV2Marked` dalı yok; v1'e özgü koşul bulunamadı.

**Ortak olanlar (fark yok, v1 silinince kaybolmaz):** LLM niyetleri ve modu
(`api/copilot_intent.go:46-82,382`); deterministik yönlendirici (`api/copilot_guided.go:62-191`);
off_topic + önceki turlar (`api/chat_offtopic_general.go:86-147`); how_to/product_guide
(`api/copilot_howto.go:18-48`); `service_healthcheck` (`api/copilot_service_healthcheck.go:119`);
yapıştırılan hata (`api/copilot_guided.go:1923`); "Kodu da incele" ve yapışkan kod
(`api/chat_code_turn.go:108`, `api/chat_code_sticky.go:83`); Explain turu
(`api/chat_explain_turn.go:319`); operasyon ön-getirme (`api/chat_operation_prefetch.go:627`);
kapsam, hafıza, netleştirme (`api/chat_scope.go:601`, `api/chat_memory.go:368`,
`api/chat_clarify.go:214`); wiki, belge satırları, RAG kademesi (`api/chat_wiki_tier.go:192`,
`api/chat_doc_rows.go:313`, `api/rag.go:355`); Oracle ve deploy akışları
(`mcp/tools.go:465,507-509`); cevap temizliği (`api/copilot_chat.go:1219-1242`); dış MCP ve rol
süzgeci (`api/copilot_chat.go:766-775,811-822`); kullanım, exchange, span, audit, kalıcılık
(`api/copilot_chat.go:332-362`, `api/chat_span.go:59`, `api/ai_conversations.go:359-382`);
tüm ayarlar (`ai_copilot`, `ai.generalQuestions`, `ai.toolsets`, `ai.budget`); istek bağlamı
alanları (`api/copilot_chat.go:98-200`). Balon içi kartların tümü (kanıt, trace listesi,
servis raporu, topoloji, grafik, log sıçraması, not/hipotez/kural düğmeleri, takip çipleri,
kaynak çipleri) v2'de aynı `ChatBubble` ile çizilir (`MessageList.tsx:25`).

**v1'de olup v2'de olmayan ya da farklı olan:**

| # | Tür | v1 davranışı | v1 yeri | v2 |
|---|---|---|---|---|
| 1 | araç | `search_traces` modele açık | `mcp/tools.go:385-561` | gizli (`mcp/v2_tools.go:111-115`); yerine `find_traces` |
| 2 | araç | `query_metric` modele açık | aynı | gizli; yerine `endpoint_metrics` |
| 3 | araç | `list_metric_labels` modele açık | aynı | gizli; karşılığı yok |
| 4 | araç | `search_logs` Lucene sürümü | `mcp/logs_tools.go:419` | aynı adla yeniden yazım (`mcp/v2_traces_logs.go:577`) |
| 5 | araç | `explain_log_spike` `query` argümanı | `mcp/tools.go` | argüman düşer (`mcp/v2_tools.go:118-120`) |
| 6 | davranış | Kapsamsız: araç argümanına takım/servis eklenmez | `api/copilot_chat.go` (işaretsiz yol) | kapsam zorunlu; yalnız `ai.userScope=false` ya da @-kapsamla kapanır (`api/chat_user_scope.go:22-24`) |
| 7 | bütçe | `dm.toolCalls/Rounds` tavanı | `api/copilot_chat.go:1194` | `ai.loopBudget` ile yalnız daralır (`:1198-1203`); araç gövdesi 2 KB tavan (`:1600`) |
| 8 | akış biçimi | `event:`/`data:` SSE | `api/chat_sink.go:30` | UI Message Stream; düşen alan yok (`translator.go:222-259`) |
| 9 | erişim | Uç her zaman açık | `api/ai_routes.go:113` | bayrak kapalıyken 404 (`api/cosre_v2_chat.go:38-46`) |
| 10 | karşılama | `CosreCapabilities` | `fe/components/CopilotChat.tsx:675` | `CosreWelcomeV2` + `BriefingFeed`; eski karşılama içeriğinin eşdeğerliği doğrulanmadı |
| 11 | evalset | Koşucu v1 yazıcısıyla, kapsamsız | `api/ai_evalset_agent.go:458` | yalnız `UserTeam` dolu vaka v2 işaretli (`:444-446`) |
| 12 | netleştirme | Düz metin soru | `api/chat_clarify.go` | aynı + `data-pick` kartı (v1 yazıcısı düşürür) |

**v2'de olup v1'de olmayan:** `data-scope` ve takım seçme kartı (`api/chat_user_scope.go:125`);
sekiz v2 aracı (`mcp/v2_tools.go:56-58`); uç nokta seçme kartı ve rota incelemesi
(`api/chat_endpoint_pick.go:329`, `api/cosre_route_red.go:44`); araç onayı
(`api/chat_approval_resume.go:373`); `data-stop` (`api/chat_stop_event.go:36`); giriş özeti;
adım kartları (`fe/components/cosre/evidence/StepCard.tsx`).

**v1 silinirken kırılacak yerler (silme listesi değil, önce çözülecekler):**
- `CosreChatV2.tsx:73` v1 dosyasından `ChatHistoryPanel`, `CosreComposerForm`,
  `CosreHeaderContext`, `deleteConversation` alır; `MessageList.tsx:25` `ChatBubble` kullanır.
- `api/ai_evalset_agent.go:458` v1 yazıcısına bağlı.
- `blocks.NewEmitter` v1 dışında da kullanılır (`api/copilot_explain_stream.go:154`,
  `api/insight.go:597`); paket silinemez.
- v2 kataloğu v1 kataloğunun üstüne kurulur (`mcp/tools.go:640`, `V2MergeTools`).
- v1 baytını pinleyen testler: `api/chat_stream_golden_test.go:124,137`, `chat_sink_test.go:42`,
  `sse_heartbeat_test.go:203`, `chat_stop_event_test.go:93`, `chat_body_cap_test.go:21` ve v1
  ucuna vuran `chat_scope_test.go:369`, `chat_source_code_test.go:170`.
- Yalnız v1 için olanlar (silinebilir): `v1ChatSink`/`copilotChat` (`api/chat_sink.go:28-42`),
  `v1OnlySink` (`api/chat_stop_event.go:55-68`), `pickChatImpl.ts`, v1 altın `.sse` dosyaları.

## 4. Bağımlılık grafiği

- v2 arayüzü → v1 arayüz bileşenleri: doğrudan ithalat (`CosreChatV2.tsx:73`, `MessageList.tsx:25`).
  Sonuç: v1 silinmeden önce ortak bileşenler v1 dosyasından ayrılmalı.
- Giriş özeti → `cosre_v2` bayrağı: uçlar bayrağa bağlı (`api/cosre_briefing.go:39-40`), arayüz
  yalnız v2 karşılamasında (`CosreWelcomeV2.tsx:133`).
- Deneyim arayüzü (soru kartı) → v2 arayüzü: `data-pick` türünü v1 yazıcısı düşürür
  (`uistream/part.go:18`, `api/chat_stop_event.go:55-68`).
- Kalite kapısı → evalset'in v2 yoluna taşınması: koşucu v1 yazıcısında
  (`api/ai_evalset_agent.go:458`). Taşınmadan kurulan kapı v1 davranışını ölçer.
- v1 emekliliği → evalset taşıması + ortak bileşen ayrımı + bayrak varsayılanı (yukarıdaki üçü).
- Görev bazlı model yönlendirme → evalset puanı: profil yüzeye göre seçilir
  (`api/ai_evalset_runs.go:21-23`); puan olmadan ölçülemez.
- `search_knowledge` ADO → audit onayı (`search-knowledge-audit.md:3`). Başka işe bağlı değil.
- React 19 → `@grafana/ui`: engel değil; `overrides` ile çözülmüş (`depPins.test.ts:38-47`).
- Karşılanmış bağımlılıklar: shadcn → v2 arayüzü; araç seti dilim 3 → giriş özeti
  (`briefing-audit.md:184`); tembel chunk → bütçe kapısı.

### Onaylı sıra (2026-11-13)

İş 1–4 sıradan çıktı (main'de). Her adım kendi audit belgesiyle başlar; belge yoksa durulur.

1. v2 sohbet arayüzü kapanışı — plan `docs/cosre/v2-ui-closeout.md`.
2. Deneyim + kısa özet / link kartları (iş 6 ve 8) — önce ORTAK audit belgesi, onaydan sonra kod.
3. Giriş özeti kapanışı — ayar satırı + prod ölçümü; karşılamada kalır.
4. Kalite döngüsü — ilk dilim evalset koşucusunu v2 yoluna taşımak; kapı deterministik rubrikle
   başlar, LLM hakem kapı değildir.
5. v1 emekliliği, iki sürüm: önce bayrak varsayılanı açık (geri dönüş = bayrağı kapat), sonra
   silme. Silmeden önce ortak balon + composer v1 dosyasından ayrılır; yalnız v1 kabuğu, yazıcısı
   ve rotası silinir.

Park kalemleri sıra dışında; model yönlendirme evalset puanı gelene kadar tek model.

## 5. Yarım kalanlar

| Ne | Yer | Anahtar | Bitirmek / silmek için |
|---|---|---|---|
| Tüm v2 arayüz ağacı | `fe/components/cosre/` | `cosre_v2` varsayılan kapalı (`api/cosre_v2_flag.go:19`) | Prod doğrulaması (adım 1); varsayılan açık adım 5 |
| Giriş özeti üreticisi | `api/cosre_briefing_worker.go:444,453` | `cosre.briefing` `Enabled:false` (`briefing/briefing.go:90`) | Ayar satırı `v0.10.2105`; kalan prod ölçümü (`briefing-audit.md` "Kapanış") |
| Özet ayar istemcisi | `fe/lib/api.ts:1421-1423` | **kapandı `v0.10.2105`**: tüketicisi `fe/pages/settings/CosreBriefingLine.tsx` | — |
| AI Elements | `frontend/scripts/bundle-budget.mjs:55` regex'i var; paket ve `fe/components/ai-elements/` yok | — | Kurulmayacak (onay 2026-11-13): regex ve belge notu temizlenir (`DECISIONS.md:12054`) |
| Evalset kapısı | `Makefile:62-69`, `evalrubric/rubric.go:20-21` | CI dışı, `-tags evalset` | İş 9 |
| RAG vektör yolu ve yönetim rotaları | `api/rag.go:45-57,381-388` | prod'da kullanılmıyor | Söküm yalnız ayrı onayla |
| Eski karşılama ucu | `api/cosre_greeting.go:38` | bayraksız | Özetle çakışma olabilir (çıkarım); v1 emekliliğinde karar |
| v2'de gizli araçlar hâlâ çağrılabilir | `api/copilot_chat.go:874-896` | gizleme yalnız şema düzeyinde | çıkarım; doğrulanıp gerekirse reddedilir |
| `V2ToolList` | `mcp/v2_tools.go:202` | yalnız testte | Bilinçli eşitleme koşumu; dokunma |
| Bayat yorum | `fe/lib/chart/depPins.test.ts:8` | — | Tek satır düzeltme |
| Kaynağı olmayan test | `fe/components/ai/CosreTeamCard.test.tsx` | — | çıkarım; bakılıp yeniden adlandırılır ya da silinir |
| `search_knowledge` audit'i | `docs/cosre/search-knowledge-audit.md:3` | onaysız | D1–D12 kararı |

## 6. Audit dokümanı eksik olanlar

| # | İş | Belge | Durum |
|---|---|---|---|
| 1 | Araç seti dilim 3 | `docs/cosre/tools-audit.md:247-249,289` | var, onaylı |
| 2 | React 19 | yok (not: `shadcn-phase1-plan.md:332`) | iş bitti; gerekmez |
| 3 | Tembel chunk | yok (not: `docs/frontend/interaction-slice-plan.md:22`) | iş bitti; gerekmez |
| 4 | shadcn Faz 1 | `docs/frontend/shadcn-phase1-plan.md` | var, onaylı |
| 5 | v2 arayüzü | `docs/cosre/chat-ui-audit.md`, `phase-2b-plan.md`, kapanış `docs/cosre/v2-ui-closeout.md` | kapanış planı yazıldı, onay bekliyor |
| 6 | Deneyim | **yok** (`experience-audit.md` bulunamadı) | 8 ile ortak belge yazılacak (onay 2026-11-13) |
| 7 | Giriş özeti | `docs/cosre/briefing-audit.md` | var; Faz 2 planı yok |
| 8 | Kısa özet + link kartı | **yok** | 6 ile ortak belge |
| 9 | Kalite döngüsü | **birleşik belge yok**; parça `docs/audit/cosre-evaluation-program-2026-09-10.md` | dur ve sor |
| 10 | `search_knowledge` | `docs/cosre/search-knowledge-audit.md` | var, onaysız |
| 11 | Park | hakem: yukarıdaki program `:60-72`; kanal kodu: `tools-audit.md:129-153`; kod RCA, model yönlendirme, v1 emekliliği: **yok** | başlamadan önce yazılır |

## 7. Emin olmadıklarım ve sorular

Emin olmadıklarım:
- `git log` koşulamadı. `v0.10.2012` ve `v0.10.2048` etiketleri var ama konu satırları
  okunmadı; Traces dilimi 2084–2097 aralığından yalnız 2090 ve 2093 doğrulandı.
- `v0.10.2080` sonrası CoSRE'ye dokunan commit listesi çıkarılamadı.
- agent-core S1–S6: tespit `docs/cosre/v2-ui-closeout.md` §1'de (S1–S5 gemide, S6 operatör ayarı).
- `api/copilot_chat.go:1243-1770` okunmadı; "v1'e özgü dal yok" sonucu `cosreV2Marked` grep'ine dayanır.
- `cosre_v2` prod'da açık mı bilinmiyor.
- Eylül tarihli CoSRE plan ve audit dosyalarının güncelliği okunmadı.
- Dilim sayıları audit'i olmayan işlerde tahmindir.

Sorular (2026-11-13 cevaplandı; kararlar en üstte "Onay" bölümünde):
1. Sıranın ilk iki adımı main'de. Sıradan çıkarıp v2 arayüz kapanışından başlayalım mı? → evet.
2. v1 emekliliği kapsamı: balon + composer ortak kalsın, yalnız v1 kabuğu, yazıcısı ve rotası
   silinsin mi? AI Elements? → evet; AI Elements kurulmaz.
3. Deneyim (6) ve kısa özet + link kartı (8) için tek audit? → evet, tek belge.
4. Giriş özeti login sayfasına mı, v2 karşılamasında mı? → karşılamada; ayar satırı + prod ölçümü.
5. LLM hakem olduğu gibi kalsın mı? → gece işinde kalır, kapı değil; kapı deterministik rubrikle.

## 8. Sonraya

(boş — yeni fikirler buraya eklenir, uygulanmaz)
