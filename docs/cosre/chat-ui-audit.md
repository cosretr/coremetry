# CoSRE sohbet UI yeniden kurulumu — Faz 0 denetimi (2026-10-29)

Girdi: üç salt-okunur Opus taraması (frontend / backend / araç kataloğu), CLAUDE.md,
docs/DECISIONS.md, docs/frontend/shadcn-audit.md; yük taşıyan her iddia repoda yeniden
okundu (file:line). Operatör brief'i: Vercel AI SDK UI (`useChat`, pinli `ai`) + AI
Elements **yapısı referans**; Go "UI Message Stream" SSE adaptörü; `/api/cosre/v2/chat`;
Faz 3 eski UI/uç söküm. **Değişmeyecek:** tool döngüsü mantığı, araç gerçeklemeleri,
prompt'lar, model seçimi, veri sorguları. §0 kararları 2026-10-29'da kesinleşti; §3/§5/§6/§7
onlara göre yeniden yazıldı. Faz 1 uygulama planı: scratchpad `cosre-phase1-plan.md`.

## 0. Kararlar (operatör, 2026-10-29 — kesin)

1. **Seçenek B tersine ÇEVRİLMEDİ.** "Yalnız sohbet için Tailwind" tam da kaçındığımız
   çift stil sistemidir: ya küresel geçiş ya hiç. Bugün erken verilen geniş shadcn geçiş
   kararı da **geri çekildi**; geniş bir frontend/shadcn denetimi koşulunca yeniden ele
   alınacak. O denetime not: tema argümanı gerçek engel değil (shadcn CSS değişkeni
   kullanır; `.dark` yalnız Tailwind `darkMode: ['selector','[data-theme="dark"]']`, üç
   `[data-theme]` bloğuna eşlenir); gerçek maliyet 45 atomu yeniden yazmak + bir süre iki
   sistemi paralel taşımak. CoSRE v2 **MEVCUT `ui/` atomlarıyla** yazılır; AI Elements'ın
   yapısı/davranışı referanstır (parça → bileşen eşlemesi, Conversation / Message /
   PromptInput / Tool ayrımı). Tailwind yok, shadcn yok, AI Elements kopyası yok, yeni UI
   bağımlılığı yok.
   > **2026-10-29: global shadcn geçişiyle geçersiz.** CoSRE v2 frontend'i Faz 1 merge'ünden
   > sonra `components/shadcn` + `components/ai-elements` üzerine kurulur. CoSRE Faz 1 (Go akış
   > adaptörü) bağımsız, devam eder. (Brief: `docs/frontend/shadcn-audit.md`, DECISIONS 2026-10-29.)
2. **👍/👎 yok.** Kart başına "ilgili / ilgisiz" + mesaj düzeyinde serbest metin not;
   ikisi de `cosre_signals`'a (mevcut `POST /api/ai/signal`, yeni türler) yazılır. Mevcut
   söküm ratchet'i (aiVoteRemoved.ratchet.test.ts, TestAIFeedbackWriteRouteRemoved) aynen.
3. **Dizinler:** `components/shadcn` yok, `components/ai-elements` yok. Sohbet bileşenleri
   `frontend/src/components/cosre/` altında, alt klasörler `conversation/`, `evidence/`,
   `prompt/`. Mevcut `components/ui` ve `components/ai` dokunulmaz (macOS APFS büyük/küçük
   harf çakışması notu geçerli).
   > **2026-10-29: global shadcn geçişiyle geçersiz.** CoSRE v2 frontend'i Faz 1 merge'ünden
   > sonra `components/shadcn` + `components/ai-elements` üzerine kurulur. CoSRE Faz 1 (Go akış
   > adaptörü) bağımsız, devam eder. (Brief: `docs/frontend/shadcn-audit.md`, DECISIONS 2026-10-29.)
4. **Markdown:** mevcut chatMarkdown motoru. Streamdown yok, Shiki yok. Mevcut ```chart
   çiti `coremetry-chart` adıyla da uyumlu kalır (istemci ikisini de kabul eder).
5. **Testler:** vitest + axe kapıdır; Playwright yalnız elle. Ek şart: Go altın SSE fixture
   testi ile frontend parça→bileşen testi **AYNI fixture dosyasını** okur
   (`testdata/cosre/ui-stream-v1.sse`, repo kökü) — protokol kayması iki tarafı birden
   kırar. Yol ve vitest erişimi: §6 F-fixture satırı + Faz 1 planı R3/R8.
6. **Görünürlük:** admin bayrağı. Bayrak HEM rotayı HEM lazy chunk'ı kapatır; v2 aynı
   kalıcılığı kullandığından bayrağı çevirmek thread kaybettirmez. Bayrak
   `system_settings`'te (mevcut ayar deseni, ai_budget.go emsali).
7. **Kalıcılık:** bugünkü gibi istemci güdümlü. `finish`ten ÖNCE asla kaydetme; metni
   `data-reset` uygulandıktan SONRA kaydet; sunucu kimlikleri varsa onları kullan.

Diğer onaylar: akış geri alma = TEK özel `data-reset` parçası (hedef metin parçası id'sini
taşır; istemci indirgeyicisi o parçayı boşaltır). Final cevap için sunucu reset + tam
otoriter metni tek delta olarak yollar — ikinci mekanizma yok. Reasoning = model düşüncesi
kapsam dışı; bileşen yalnız araç adımları + bütçe çizer. Bundle: v2 lazy chunk. Defter:
CLAUDE.md ve DECISIONS.md'ye kısa not — "CoSRE v2 Seçenek B'ye uyar; shadcn sorusu
frontend audit'ine ertelendi" — ANA oturum yazar (bu doküman yalnız atıf yapar).

## 1. Özet

1. Sohbet bugün kendi SSE protokolü (`event:`/`data:` çerçeve, blocks/emitter.go:76) + kendi
   akan markdown ayrıştırıcısıyla (ai/chatMarkdown.ts) çalışıyor; AI SDK, Tailwind, shadcn,
   Radix, react-markdown, Shiki, testing-library, Playwright **yok** (package.json).
2. Tek uç `POST /api/copilot/chat` (ai_routes.go:120 → copilot_chat.go:193); 16 kademeli
   sıra kaynak-pinli (chat_tier_order_test.go); tool döngüsü copilot_chat.go:1040-1507.
3. Olay kümesi 8 tür: step / step-result / delta{reset} / block / answer / error /
   code-lock / done (types.ts:8392-8421); `answer` otoriter, delta'ları ezer. Sarım zinciri
   (withStepIDs → turnBlockCapture → withBlockSeq → xch.tap → armDisambigRescue) hep bu
   sözlükte konuşur; dikiş en altta `em.Emit` (copilot_chat.go:244-258, :321, :518).
4. Düşünce blokları sağlayıcıda soyulur (provider/think_stream.go) → reasoning olayı yok.
5. Bütçe yalnız TR etiket dizesi (chat_loop_steps.go:53,71); yapısal sayaç yayınlanmıyor.
6. Kalıcılık istemci güdümlü debounce upsert (useChatThread.ts:159-197 → `POST
   /api/ai/conversations`, saved_views page='ai-chat'); bloklar cosre_interactions'tan
   birleştirilir (ai_conversations.go:104-117).
7. 👍/👎 v0.10.1393'te kaldırıldı; ratchet + Go pin testi geri sızmayı yasaklıyor (§0-2 korur).
8. 74 araç tek kayıtta (mcptools/tools.go:373-550); link çipi 10 araçta
   (chat_tool_links.go:88-127), kanıt zarfı 3+3 araçta (evidence_link.go:82-89).
9. Sohbet ana index chunk'ında eager (AppShell.tsx:8; ~134 KB gz'nin ~%54'ü sohbet).
10. Yeni UI'yi sınırlayan kapılar: ui/no-raw-button (eslint.config.js:97-104),
    buttonUnityRatchet MAX_RAW_BUTTONS=0, @grafana/ui chart primitifi yalnız CorePanel.tsx
    (corePanelContracts.test.ts:787), ~32 kaynak-metin pini. §0-1 ile bunlar v2'nin de
    kapısıdır — muafiyet açılmaz.

**Riskler (karar sonrası durum)**

- R1 (kayıtlı kararlarla çelişki) — **kapandı**: §0-1 B'yi korur; shadcn sorusu ertelendi.
- R2 (protokol empedansı) — **tasarımla kapandı**: `data-reset` + otoriter metin tek delta
  (§3.2). Reasoning uydurulmaz (§3.3).
- R3 (dizin/kapı çakışması) — **kapandı**: `components/cosre/{conversation,evidence,prompt}`;
  ratchet/eslint muafiyeti YOK (atomlar zaten uyumlu).
- R4 (gözlemlenebilirlik kör noktaları) — **Faz 1'de zorunlu iş**: aiSurfaceFromPath dalı,
  TestRequireCopilotRouteCoverage tüm *.go, v2'de MaxBytesReader (plan R5).

## 2. Denetim kalemleri

### 2.1 Stack
Vite 8.3.4 (rolldown advancedChunks vite.config.ts:64-83), React 18.3.1 tam pin, TS ~5.9,
Vitest 4 + jsdom opt-in, ESLint 9 flat, react-router 6.30, react-query 5, uplot 1.6.32 +
@grafana/ui|data 13.1.2 tam pin. CSS: tek globals.css (~6.4k satır; token'lar --bg0..3,
--text/2/3, --brand, --accent*, --ok/--err/--warn); 33 stil kapısı testi. Tema `<html
data-theme=dark|light|redhat>`; theme.ts:39 `currentTheme()` eksik öznitelikte 'redhat'
döner, CSS ise eksik = dark (tutarsızlık, küçük ön-düzeltme). Dev-only `/design` rotası var.

### 2.2 Mevcut sohbet UI
components/CopilotChat.tsx (1.098 satır, variant drawer|page), ai/useChatThread.ts (467),
ai/ChatBubble.tsx (1.276), ai/ChatComposer.tsx (423). Mount: AppShell.tsx:281-285 global FAB
+ Drawer (statik import :8); pages/CoSRE.tsx:21; TraceKiosk.tsx:225; `?ai=<subject>` kodeği
(lib/aiSubject.ts:70-77, 13 tür) → explainAsk turu; `coremetry:ai-ask` / `ai-prefill`
pencere olayları; metrik paneli "Ask CoSRE". Akış: api.copilotChat (api.ts:2419-2541) →
lib/sse.ts readSSE (:55 yalnız `event:`/`data:`; yorum satırı düşer) → useChatThread.ts:
255-303. Meşgulken abort + kuyruk (:202-206). Geçmiş yalnız {role,text} (:218-235);
durdurulan tur "[YARIDA DURDURULDU]" (:232). Örtük sinyal `POST /api/ai/signal`
(cosre_quality.go:36; türler cosrelearn/signals.go:47: copy, link_click, evidence_click,
problem_note, followup_click; detail ≤512 B :34).

### 2.3 Render
Markdown: ai/chatMarkdown.ts (text/heading/list/quote/hr/callout/details/code/table) +
chatInline.ts; ChatBubble.tsx:431,462. CodeBlock ChatBubble.tsx:210. autoHighlight.ts
çipleri. Grafik: ```chart çiti ChatBubble.tsx:391 (`b.lang === 'chart'`) → CosreChart →
lazy CorePanelMulti (uPlot); `block{type:chart}` (lib/chatBlocks.ts:13). `coremetry-chart`
dizesi frontend/src'de HİÇ yok → §0-4 "ikisini de kabul" = ChatBubble.tsx:391'e takma ad.
Link politikası chatLinks.ts, isSafeAppPath (lib/chatBlocks.ts:22-24, lib/openHref.ts:27).

### 2.4 Sohbet ucu + akış
Handler copilot_chat.go:193; gövde decode :195 (**MaxBytesReader yok**); sanitizeClientHistory
:204; chatProfileGate (400/403 SSE öncesi, :215); emitter blocks.NewEmitter Eager + 15 s
`: ping` (:244; Close :249 terminal olay yoksa `event: error` basar, emitter.go:108).
Sarım zinciri: withStepIDs (chat_step_ids.go:46; `step` → `i`) :253 → turnBlockCapture
:257 → withBlockSeq (chat_step_ids.go:179; evidence/trace_list/service_report/topology/
charts_signals/log_spike/metric_panel/ev_block → `block{id:"bN",seq,final:true}`) :258 →
xch.tap (chat_exchange.go:372; answer'a callId/turnId ekler) :321 → armDisambigRescue
(chat_disambig_rescue.go:75) :518. Yayınlanan tür sayımı (grep): answer 82, step 41, block
26, error 21, delta 18, done 17, step-result 14, ev_block 6, trace_list 5, evidence 4;
code-lock chat_code_sticky.go:50. delta{reset}: :1065, :1082. answer: :1163, :1486 +
kademe çıkışları. error metinleri: chat_deadline.go:87, chat_overflow.go:99,
chat_loop_steps.go:83; ham: EmptyAnswerError (provider/salvage.go:154), 503/429 sohbete
ham gider. Bütçe chatMaxToolRounds=5 / chatMaxToolCalls=6, derin ×1.5. Test altyapısı:
newLoopTestServer + scriptli loopLLM (chat_trace_followup_loop_test.go:39,47,156),
postChatRaw (chat_scope_test.go:366), exchangeId pini withChatEvalMode (chat_eval_mode.go:97).

### 2.5 Kalıcılık
Thread = saved_views page='ai-chat' blobu (ai_conversations.go: messages[], subject,
context, shareToken, link, firstAt); mesaj şekli :104-117 (role, text, evidence, explain,
exchangeId, blocks, codeRead); tavanlar 40 mesaj / 64 KB / 256 KB istek (:65-74; :617
MaxBytesReader). Rotalar ai_routes.go:162-165 (requireCopilot'suz — kişisel). Kimlik SUNUCU
basar (:640-660). cosre_interactions (exchange başına), ai_calls, cosre_signals, ai_feedback
(yalnız okuma). FE istemci: api.ts:1396-1411 (aiConversations/aiConversation/
saveAiConversation) — v2 bunları AYNEN kullanır (§0-6/7).

### 2.6 Araç kataloğu
Tek kayıt internal/mcptools (74; sohbet 73 altın liste); 12 toolset; "simple" (≤12B) bazı
araçları gizler. Dış MCP köprüsü `<server>__<tool>`. Zarf (cosre_query.go:83-125): {tool,
ok, empty, error{code,need,candidates}, request, evidence{source,query,queryKind,window,
env,cluster,rows,series,durationMs}, evidences[]{hop,as}, data}. Blok üretimi
ToolResultBlock (evidence_blocks.go:140). URL sözleşmesi buildLink (build_link.go:73-82) +
EvidenceHref (evidence_link.go:169) + toolCallLinkTarget (chat_tool_links.go:88-127).

## 3. Eşleme: mevcut SSE olayları → AI SDK UI Message Stream parçaları

### 3.1 Pinlenecek protokol varsayımları (kurulumda doğrulanır)
`ai@5.x` varsayıldı. Yanıt başlığı `x-vercel-ai-ui-message-stream: v1`, `Content-Type:
text/event-stream`; her çerçeve `data: {"type":…}\n\n`; sonlandırıcı `data: [DONE]\n\n`;
SSE yorum satırı (`: ping`) ayrıştırıcıda yok sayılır (heartbeat kalır — doğrulanacak;
düşerse `data-heartbeat{transient:true}`). Parça türleri: `start{messageId?}`,
`start-step`/`finish-step`, `text-start{id}` / `text-delta{id,delta}` / `text-end{id}`,
`tool-input-start{toolCallId,toolName,dynamic?}` / `tool-input-available{toolCallId,
toolName,input,dynamic?}` / `tool-output-available{toolCallId,output,dynamic?}` /
`tool-output-error{toolCallId,errorText}`, `source-url{sourceId,url,title?}`,
`source-document{sourceId,mediaType,title}`, `data-<name>{id?,data,transient?}` (aynı `id`
→ yerine geçer), `message-metadata{messageMetadata}`, `error{errorText}`,
`finish{finishReason?}`, `abort`. İstemci: `useChat({transport: new DefaultChatTransport
({api, prepareSendMessagesRequest})})`; `dynamic:true` araç parçası `dynamic-tool` olarak
düşer (74 araç + dış MCP adları derleme zamanında kapalı küme değil → dynamic şart).
Go tarafı sabiti `uistream.ProtocolVersion = "v1"`; **pinli `ai` sürümü Faz 2'de
`node_modules/ai/package.json`'dan doğrulanır ve vitest ile çivilenir; fixture o sürüme
bağlanır.**

### 3.2 Tablo (adaptör sarım zincirinin ALTINDA: `i`, `bN`, callId/turnId damgalı çerçeveyi görür)

| v1 olay / alan | Hedef parça | id | Kayıp / karar |
|---|---|---|---|
| `step{i,label,phase?}` (tool'suz) | `data-step{id:"s<i>",data:{i,label,phase?,origin?}}` | `s<i>` | Araç parçası DEĞİL; şeffaflık paneli bugün de saymıyor |
| `step{i,tool,args,origin?,phase?}` | `tool-input-start{toolCallId:"s<i>",toolName,dynamic:true}` + `tool-input-available{…,input:{argsText,args?}}` | `s<i>` | `args` JSON'u emit sitesinde var; eklemek payload alanı, döngü mantığı değil (plan R6) |
| `step-result{i,tool,ok,…}` / `{skipped,reason}` | `tool-output-available{toolCallId:"s<i>",output:{…i/tool hariç tümü…},dynamic:true}` | `s<i>` | `ok=false`'ta `tool-output-error` KULLANILMAZ (yapısal alanlar kalır); "yürütülmedi" rozeti `skipped`tan |
| `step` bütçe etiketi (:1047, :1425) | `data-step` + `data-budget{id:"budget",data:{maxCalls,maxRounds,slotsUsed,executed,skipped,roundsUsed,exhausted}}` | sabit | Sayaçlar emit sitesinde; step payload'ına `budget{}` alanı (plan R6); eski FE yok sayar |
| `delta{text}` | `text-delta{id:"t<k>",delta}` (`text-start` segmentin ilkinde) | `t<k>` tur içi sayaç | Boş `text` parça açmaz |
| `delta{reset:true}` | `text-end{t<k>}` (açıksa) + **`{"type":"data-reset","data":{"textId":"t<k>"}}`** (v0.10.1606: tam şekil bu; parçanın kendi `id`'si yok — uzlaştıran tüketici yok, her reset tekil; providerMetadata yok); sonraki delta `text-start{t<k+1>}` | yok | TEK mekanizma (§0): istemci indirgeyicisi `textId` parçasını boşaltır; data-reset parçaları mesajda kalır → `applyResets(parts)` saf ve kalıcılıkta da doğru. Deterministik senaryo: `testdata/cosre/ui-stream-v1-reset.sse` |
| `answer.text` (otoriter) | Açık/dolu segment varsa `text-end` + `data-reset{data:{textId:t<k>}}`; sonra HER ZAMAN `text-start{t<k+1>}` + `text-delta{delta:text}` + `text-end` | aynı | Önek karşılaştırması YOK (ikinci mekanizma yok); metin v1'deki gibi iki kez gider |
| `answer.exchangeId, turnId, callId, cached, evidenceSpanIds, explain, allowedLinks, open` | `message-metadata{…}` | — | `open` → `onFinish`'te isSafeAppPath kapısıyla SPA gezinme (v1 D7b aynen); `start{}` messageId TAŞIMAZ (exchangeId dikişten sonra basılıyor, :275) — v0.10.1603 (operatör kararı 2026-10-29 §3): `start` artık sunucu üretimi ULID `messageId` taşır (uistream/ulid.go); exchangeId yine `message-metadata`'da |
| `answer.sources[]` (RagSource) | `kind:'url'|'wiki'` → `source-url{sourceId:doc+"#"+chunk,url:ref,title:label}`; `upload` → `source-document` | doc+chunk | `score/sections` → metadata.sources |
| `answer.links[]`, `followups[]`, `suggestions[]` | `data-links{id:"links"}`, `data-followups{id:"followups"}` | sabit | suggestions → followups'a katlanır |
| `answer.evidence[]` | `data-evidence-summary{id:"evidence"}` | sabit | Özet şerit için kopya |
| `block{id:"bN",type,seq,final,payload}` (13 tür) | `data-block-<type>{id:"bN",data:{seq,final,payload}}` | `bN` | appendChatBlock yerine-geçme semantiği birebir |
| ```chart çiti (metin içinde, :1247-1259) | metin parçası olarak akar; istemci çit ayrıştırıcısı `chart` VE `coremetry-chart` (§0-4) | — | Sunucu çit adı değişmez |
| `code-lock{locked}` | `data-code-lock{id:"code-lock",data:{locked}}` | sabit | Arşivde `codeRead` ile buluşur |
| `error{error}` | `data-error{id:"error",data:{class,raw}}` + `error{errorText:raw}` | sabit | class ∈ deadline\|overflow\|cancelled\|max_tokens\|provider_down\|quota\|closed\|unknown; sınıflama saf `ClassifyError(raw)`; ham metin KORUNUR |
| Emitter Close terminal olaysız | `error{closed}` + `finish-step` + `finish{error}` + `[DONE]` | — | v1 Close semantiğinin aynası |
| `done{ok}` | `finish-step` + `finish{finishReason: ok?"stop":"error"}` + `[DONE]` | — | İlk çerçeve ne olursa olsun önce `start{}` + `start-step{}` (tembel) |
| `: ping` | SSE yorumu aynen | — | Parser yok sayar (varsayım, 3.1) |
| bilinmeyen tür | `data-unknown{data:{kind,payload}}` | — | Kaynak taraması testi (plan R3) yayınlanan her türün kapsandığını pinler |

### 3.3 Açık noktalar
- **Reasoning yok, uydurulmaz.** think_stream.go soyar. Bileşen `tool-*` + `data-step` +
  `data-budget`tan beslenir; adı "Araştırma adımları".
- **Clarify düz metin kalır** (chat_clarify.go:228). Operatör 1571/1572: "çip değil düz
  soru-cevap". Yapısal `data-clarify` yok.
- **Geçmiş gövdesi.** `prepareSendMessagesRequest` UIMessage[]'ı v1 şekline indirger
  (`{messages:[{role,text}], context}`) → v2 handler chatRequest decode + sanitize +
  ClampHistory'yi AYNEN kullanır (paylaşılan gövde); durdurulmuş tur notu istemcide.
- **Kuyruk davranışı** (meşgulken abort + kuyruk) useChat'te yok → composite'te `stop()`
  + bekleyen soru; vitest ile pinlenir.
- **Kalıcılık sırası (§0-7):** `finish` → `applyResets` → projeksiyon → saveAiConversation;
  `exchangeId`/`turnId` metadata'dan; sunucu thread id'si yanıttan devralınır.

## 4. Kanıt kartı tipleri (aile başına bir kart; `components/cosre/evidence/`)

Ortak gövde: başlık (araç adı TR, phase), `request` özeti, pencere, kaynak rozeti, süre,
durum rozeti, kompakt sonuç, "<sayfa>'da aç" pivotu, kart başına "ilgili / ilgisiz"
(§0-2; sinyal `evidence_relevant|evidence_irrelevant`, detail = `s<i>`). Pivot href'i HER
ZAMAN sunucudan; FE yardımcısı yalnız boşlukta ve isSafeAppPath kapısından.

| Aile | Araçlar | Kompakt sonuç | Pivot + mevcut kurucu | Boşluk |
|---|---|---|---|---|
| Metrics | query_metrics, query_metric, get_pod_health, list_metric_*, cluster_metric, compare_periods, render_chart | sparkline (EvChartPayload) / satır tablosu | promqlEditorHref evidence_link.go:276; FE metricExploreHref lib/metricQuery.ts:130 | list_*, cluster_metric, get_pod_health, compare_periods çipsiz |
| Traces | search_traces, query_traces, trace_stats, get_trace, find_trace_by_*, get_exemplar_traces, get_linked_traces, get_metrics_for_span, pivot_from_trace, describe_attributes, find_attribute_by_value | trace_list satırları / stats; hop zinciri | TracesDeepLink, aggregateDeepLink, EvidenceHref; FE tracesPivotHref, traceHref, attrTracesHref | get_trace / find_* / exemplar / linked çipsiz |
| Logs | query_logs, search_logs, list_log_fields, get_log_histogram, explain_log_spike, log_patterns, get_logs_for_trace | tablo (Zaman/Seviye/Pod/Mesaj/Trace) | EvidenceHref queryLogs; toolCallLinkTarget search_logs; FE logsHref | histogram bloğu yok |
| Exceptions | list_exception_groups, get_exception_samples | grup satırları | `/inbox?kind=exception&service=`; FE inboxItemHref | grup href'i sunucuda yok |
| Problems / incidents | list_problems, get_problem, get_problem_root_cause, list_problem_window_events, list_anomalies, get_correlation_evidence, similar_* | problem satırları + hipotez; `action` bloğu aynen | `/problems?service=`; build_link problem/incident; FE anomalyDetailHref | aile büyük ölçüde çipsiz |
| Services / topology | list_services, get_service_health, get_operation_health, get_topology, get_blast_radius, list_environments/clusters/operations/slo_status/teams, get_team_services, service_health_sweep, resolve_business_code | RED tablosu; topoloji → mevcut ChatTopologyCard | `/service?name=`, `/endpoints?service=`, `/service-map`; FE serviceHref, endpointDetailHref, nodeDetailHref | service-map çipi focus/range taşımıyor; SLO linksiz |
| DB / messaging | get_db_health, get_messaging_health | statement/topic satırları | `/databases`, `/messaging` (penceresiz); FE databaseDetailHref, stmtDetailHref, messagingTopicHref | sunucu çipi kimlik+pencere taşımıyor |
| Deploys / rollouts | get_correlated_changes, get_deploy_diff, list_deploys, list_deployments | satırlar (zaman, workload, tag, köken) | build_link rollouts; FE rolloutEvidenceHref | hiç çip yok |
| K8s | list_namespaces, list_workloads, list_pods, resolve_entity | varlık satırları + canlılık | build_link pod/clusters; FE podDetailPath, entityHref | list_* çipsiz |
| Oracle | resolve_oracle_operation | code/count/sample satırları | query_traces kod süzgeci; FE functionCodeTracesHref | sunucu çipi yok |
| Wiki / code / knowledge | search_wiki, read_wiki_page, search_code, read_source_code, get_runbook, search_knowledge, product_guide | isabet listesi; kod yalnız referans satırı | `source-url` (allowedLinks kapısı); product_guide link çipi (otomatik geçiş YOK, 809) | runbook href'i yok |
| Meta / build_link | get_capabilities, build_link, set/get/clear_context | href + page + range | "Coremetry'de aç" (chat_tool_links.go:184-195) | context araçları kart çizmez |

## 5. Repo kurallarıyla çatışmalar — ÇÖZÜLDÜ (§0)

| # | Konu | Karar |
|---|---|---|
| K-1 | Tailwind + shadcn vs "ui atoms only" | **B korunur** (§0-1). v2 = `ui/` atomlarıyla AI-Elements-biçimli composite; shadcn sorusu frontend audit'ine ertelendi; DECISIONS/CLAUDE.md notunu ana oturum yazar |
| K-2 | Kapılar + dizin adları | `components/cosre/{conversation,evidence,prompt}` (§0-3); ratchet/eslint muafiyeti YOK; `components/ui` ve `components/ai` dokunulmaz; `<input>/<textarea>` yasağı eklenmez (`Field.tsx` zaten var) |
| K-3 | Üç tema vs `.dark` | Düştü (shadcn yok). Gelecek audit notu §0-1'de. theme.ts:39 'redhat' varsayılanı ↔ CSS dark ön-düzeltmesi küçük ayrı sürüm (operatör onayı) |
| K-4 | uPlot sarmalayıcı | Mevcut CosreChart (lazy CorePanelMulti) + EvidenceChips; kompakt sparkline satır-içi SVG; tek motor korunur |
| K-5 | Streamdown vs chatMarkdown | **chatMarkdown** (§0-4); CodeBlock mevcut bileşene çıkarılır; çit `chart` + `coremetry-chart` |
| K-6 | Oy rayı | **Oy yok** (§0-2); `evidence_relevant` / `evidence_irrelevant` / `note` sinyal türleri + Weight; ratchet aynen |
| K-7 | Bundle | v2 lazy chunk (`cosre` advancedChunks grubu); FAB eager; bayrak kapalıyken chunk yüklenmez (§0-6); "AppShell `ai`/`@ai-sdk` statik import etmez" pini |
| K-8 | Kaynak-metin pinleri | v2 yeni dizinlerde; eski ağaç Faz 2 boyunca dokunulmaz; Faz 3'te pin testleriyle birlikte silinir |
| K-9 | Playwright | **vitest + axe kapı**; Playwright yalnız elle (§0-5) |
| K-10 | "PR" → sürüm serisi | Her birim ayrı `v0.10.X` (Faz 1: plan R1-R8) |
| K-11 | Kalıcılık | **İstemci güdümlü** (§0-7); mevcut `/api/ai/conversations` + `lib/cosre/uiMessages.ts` projeksiyonu; finish-sonrası, reset-sonrası, sunucu id'li |
| K-12 | Dış sağlayıcı yolları | v2 YENİ dış çağrı eklemez; chat dizinlerinde yeni `https://` host yok grep-kapısı |
| K-13 | MaxBytesReader | v2'de `http.MaxBytesReader(w, r.Body, 256<<10)` (aiChatMaxRequestBytes); v1'e eklemek ayrı sürüm, operatör onayı (§7 açık) |
| K-14 | Kapı kör noktaları | Zorunlu (plan R5): coverage testi tüm *.go + `/api/cosre/v2/` deseni; aiSurfaceFromPath `api/cosre/v2/chat → "chat"`; `registerRoutesExtra(` yorumda yazılmaz; kayıt adı `"cosre-v2-chat"` |
| K-15 | Görünürlük | **Admin bayrağı** `system_settings("cosre_v2")` (§0-6); rota kapalıyken 404; `/api/copilot/config.cosreV2` FE'ye |

## 6. Faz dilimleme — Coremetry sürümleri olarak

Her dilim ayrı `v0.10.X`, tek mantıksal birim, geri alınabilir. Ortak kapı: `go build
./... && go vet ./...`, `go test ./...` (+ `go test -race ./internal/cosre/...`; CI race
listesi ci.yml:151'e uistream eklenir), `~/go/bin/golangci-lint run ./...`, `make audit`,
FE: `npx tsc --noEmit && npx eslint src && TZ=UTC npx vitest run`.

**Faz 1 (Go adaptörü) — ayrıntı scratchpad `cosre-phase1-plan.md`:** R1 v1 handler bayt
altını (taban) · R2 sink dikişi `copilotChatWith` · R3 `internal/cosre/uistream` + paylaşılan
fixture · R4 admin bayrağı · R5 `POST /api/cosre/v2/chat` + kapı genişletmeleri · R6 yapısal
alanlar (budget, argsJson) · R7 sinyal türleri · R8 FE fixture pini (yalnız test).

**Paylaşılan fixture (§0-5):** `testdata/cosre/ui-stream-v1.events.jsonl` (girdi: v1 çerçeve
senaryosu) → `testdata/cosre/ui-stream-v1.sse` (çıktı: v2 baytları), repo kökünde. Go:
`internal/cosre/uistream/golden_test.go` paket dizininden `../../../testdata/cosre/…` okur
(`-update` bayrağı yeniden üretir). FE: `frontend/src/lib/cosre/uiStreamFixture.test.ts`
`resolve(__dirname, '../../../../testdata/cosre/ui-stream-v1.sse')` ile okur — vitest env
node, düz `node:fs`; `test.include` yalnız test dosyalarını seçer, Vite `server.fs.allow` dev
sunucusunun HTTP servisini kısıtlar, test-zamanı fs okumasını değil (emsal
aiVoteRemoved.ratchet.test.ts:17 `resolve(__dirname,'..','..')`). Kök `go.mod` varlığı test
başında doğrulanır ki taşınan test sessizce yanlış dosya okumasın.

| # | Dilim (Faz 2-3) | İçerik | Özel kapılar |
|---|---|---|---|
| F1 | Eksik atomlar | Chat için `ui/`de olmayan parçalar (ör. Conversation scroll-to-bottom, PromptInput textarea büyümesi) `ui/`ye ATOM olarak eklenir; stil token CSS | buttonUnityRatchet, colorLeaks, undefinedCssRefs, contrastTokens, noFixedPxWidth |
| F2 | Transport + tipler | `ai`/`@ai-sdk/react` tam pin + `node_modules/ai/package.json` vitest pini, `lib/cosre/transport.ts` (prepareSendMessagesRequest → v1 gövde + context), `lib/cosre/parts.ts` + dataPartSchemas + `applyResets`, parça→bileşen eşleme tablosu testi **paylaşılan fixture'ı okuyarak** (R8 dosyası genişler) | vitest; K-7 lazy pin |
| F3 | Kabuk | `components/cosre/conversation/` (Conversation, Message, Steps, Sources, CodeBlock), `components/cosre/prompt/` (PromptInput Enter/Shift+Enter/stop/regenerate), `CosreChat.tsx`; gizli rota `/cosre/v2` bayrak arkasında (`copilotConfig.cosreV2`); `/design`e chat durumları | a11y vitest+axe |
| F4a/b/c | Kanıt kartları | `components/cosre/evidence/`: Metrics/Traces/Logs → Services-topology/Problems/Exceptions/DB-messaging → Deploys/K8s/Oracle/Wiki-code/Meta + action blokları | kart→pivot href testi |
| F5 | Grafik + tipli bloklar | ```chart (+`coremetry-chart`) + data-block-* → CosreChart / EvidenceChips sarmalanır | corePanelContracts |
| F6 | Bağlam + mount | pageContext/pinned/scope paritesi, `?ai=` kodeği, Problem sayfası Drawer ön-tohumlu | CopilotChat pin testleri dokunulmadan |
| F7 | Geri bildirim | kart "ilgili/ilgisiz" + mesaj notu → `POST /api/ai/signal` (R7 türleri), optimistik | aiVoteRemoved ratchet yeşil |
| F8 | Hata kartları + durdur/yeniden | data-error sınıfları, stop/regenerate (`attempt`), meşgul-kuyruk paritesi | vitest |
| F9 | Kalıcılık | onFinish → applyResets → saveAiConversation projeksiyonu; Geçmiş → UIMessage | 40 mesaj/64 KB tavan testi |
| P1 | Geçiş | bayrak varsayılanı açık; `/cosre` + çekmece v2; eski UI bir sürüm bayrak arkasında | — |
| P2 | Söküm | Eski bileşenler + ~32 pin + `api.copilotChat` + v1 rotası; `lib/sse.ts` KALIR (explain akışı); bayrak kalkar | TestApiGoDoesNotGrow, registry sayımı |
| P3 | Defter | DECISIONS girdisi, CLAUDE.md "What goes WHERE" (`internal/cosre/`, `components/cosre`) | — |

## 7. Operatöre sorular

**Cevaplananlar (2026-10-29):** 1 Seçenek B korunur, shadcn ertelendi (§0-1) · 2 oy yok,
sinyal türleri (§0-2) · 3 dizinler `components/cosre/*` (§0-3) · 4 chatMarkdown, çit
`coremetry-chart` takma adı (§0-4) · 5 clarify düz metin (§3.3) · 6 vitest+axe, Playwright
elle (§0-5) · 7 admin bayrağı, system_settings (§0-6) · 8 çit adı sunucuda `chart` kalır,
istemci ikisini kabul eder (§0-4) · 9 kalıcılık istemci güdümlü (§0-7) · 11 `<input>`
lint yasağı YOK (K-2) · 12 Faz 3 geri dönüş penceresi: eski UI bir sürüm bayrak arkasında
(P1/P2, "geriye-uyum şimi yok" kuralıyla uyumlu kısa pencere).

**Hâlâ açık (karar istiyor):**
1. v1 ucuna da MaxBytesReader (413 davranış değişikliği, ayrı sürüm) — eklensin mi?
2. theme.ts:39 'redhat' varsayılanı ↔ CSS dark uyumsuzluğu ön-düzeltmesi (küçük, ayrı sürüm).
3. Sinyal ağırlıkları önerisi: `evidence_relevant` +0.10, `evidence_irrelevant` −0.10, `note`
   0 (kaydedilir, puanlanmaz) — kabul mü?
4. `/ai` kırılımında v2 ayrı görünsün mü (bugün ikisi de yüzey `chat`; ayrım isteniyorsa
   cosre_interactions'a taşıma kolonu gerekir — Faz 1 kapsamı dışı)?
