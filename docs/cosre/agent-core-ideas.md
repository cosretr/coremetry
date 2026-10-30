# CoSRE agent loop — three AI SDK Core ideas, Go-native (design note, 2026-10-30)

Durum: **TASARIM — uygulama yok, commit yok.** Kaynak fikirler Vercel AI SDK Core v5
(`Agent`, `stopWhen`, `prepareStep`, tool approval). Her iddia `frontend/node_modules/ai`
5.0.276 tip tanımlarından ve repodaki `file:line`'dan doğrulandı. Node çalışma zamanı ve
AI SDK Core bağımlılığı YOK: fikirler Go döngüsüne taşınır, AI SDK yalnız UI protokolü kalır.

## 0. SDK gerçekleri (ai 5.0.276, salt-okur doğrulama)

| Kavram | 5.0.276'da | Kaynak (`node_modules/ai/dist/index.d.ts`) |
|---|---|---|
| `stopWhen` | `StopCondition = ({steps}) => bool`; dizi = **OR**; varsayılan `stepCountIs(1)`; hazır: `stepCountIs(n)`, `hasToolCall(name)` | :957-961, :1129-1131 |
| `prepareStep` | `({steps, stepNumber, model, messages}) → {model?, toolChoice?, activeTools?, system?, messages?} \| undefined` (undefined = dış ayar) | :943-955 |
| `Agent` | yalnız `Experimental_Agent` (sarıcı: model+tools+stopWhen+prepareStep) | :2348 |
| `needsApproval` / `approval-requested` / `output-denied` / `addToolApprovalResponse` | **YOK** (tek geçiş `pruneMessages` doc yorumu) — bunlar ai@6 | grep boş |
| Araç parçası durumları | `input-streaming → input-available → output-available \| output-error` (dinamik araç aynı) | :1640-1668, :1704-1729 |
| v5 insan-onayı deseni | araç sunucuda yürütülmez → parça `input-available`'da kalır; istemci `onToolCall` / `addToolOutput` (`addToolResult` deprecated) ile çıktıyı yazar; `sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithToolCalls` yeni POST atar | :4117, :4181, :4262-4285, :4445 |

Sonuç: onay **akış içinde duraklatma değil, yeni istek turudur** (v5'te tek yol; 180 s alışveriş tavanıyla da uyumlu tek seçenek).

## 1. Bugünkü döngü

### 1.1 Sınırlar ve durma kararları (hepsi örtük, dört ayrı `break`)

| Karar | Değer | file:line |
|---|---|---|
| Tur tavanı | 5 (Derin: ×1.5, mutlak 8) | `internal/api/copilot_chat.go:62`; `chat_deep.go:37,51-56` |
| Çağrı tavanı (alışveriş) | 6 (Derin: ×1.5, mutlak 12) | `copilot_chat.go:67`; `chat_deep.go:38,58-63` |
| Uçtan uca tavan | clamp(çağrı-başı×3, 180 s, 900 s); ctx tüm araçlara iner | `chat_deadline.go:52-58,65`; uygulanır `copilot_chat.go:298-300` |
| Araç başına süre | 20 s (`mcp.ToolCallBudget`) | `internal/mcp/toolerr.go:220`; `ai/agent/tools/executor.go:150,168` |
| Modele giden sonuç | 6000 rune, kırpma söylenir | `chat_tool_budget.go:41-66` |
| Bütçe adımı (yapısal) | `chatBudget{maxCalls,maxRounds,slotsUsed,executed,skipped,roundsUsed,exhausted}` | `chat_loop_steps.go:55-69`; emit `copilot_chat.go:1056,1434` |
| Döngü başı | `for round := 0; round < maxRounds; round++` | `copilot_chat.go:1100` |
| Çıkış 1: sağlayıcı hatası | `break` (taşma: 1 kez küçült + `round--`) | `:1122-1146` |
| Çıkış 2: araç çağrısı yok → cevap | `break` | `:1149-1175` |
| Çıkış 3: iptal / deadline | tur kalanı skipped, model yeniden çağrılmaz | `:1412-1420` |
| Çıkış 4: tavan turu | `round == maxRounds-1 \|\| callsLeft <= 0` → `WithNoToolCalls` + `ChatRoundCapAddendum` → `break` | `:1431-1500` |
| Tur içi çağrı bölme | `splitByCallBudget` (çalışacak / `call_cap` skipped) | `:81, :1183, :1399-1411` |

### 1.2 Adım başına el yapımı ayarlar (= gizli `prepareStep`)

| Nerede | Ne değişir | Mekanizma |
|---|---|---|
| Tavan turu `:1444-1446` | sistem eki + araç çağrısı yasak (tanımlar kalır — Anthropic 400 / OpenAI boş dizi notu `:1439-1443`) | `copilot.ChatRoundCapAddendum()`, `copilot.WithNoToolCalls` (`copilot/chat.go:74`) |
| Yineleme düşüşü `:1072-1088` | araçsız + token tavanı + ek | `WithNoToolCalls`, `WithMaxTokensCap` (`chat.go:87`), `ChatDegenRetryAddendum()` |
| Araç seti yeniden çözümü `:751-757` | `simple` ↔ `full` katalog (profil/model) | `copilot.ToolsetFor` (`profiles.go:578`), `mcptools.ChatToolListFor` (`tools.go:628`) |
| Model çağrı noktası | tek yer | `chatLoopTurn` (`chat_step_phase.go:99`) → `ChatWithToolsStream(ctx, system, msgs, specs, onDelta)` (`copilot/chat_stream.go:38`) |
| Spec'ler | döngüden ÖNCE bir kez kurulur, her turda aynı dizi | `copilot_chat.go:791-808` |

### 1.3 Araç kataloğu ve yan etki envanteri

| Katman | file:line | Not |
|---|---|---|
| Kayıt defteri | `mcptools/tools.go:373` `ToolList`; sohbet görünümü `:589` `ChatToolList` → `:628` `ChatToolListFor` | 74 araç; `externalOnlyTools` (`bubble_up`) `:582`; `simpleHiddenTools` `:617` |
| Rol süzgeci | `api/mcp_gate.go:96` `toolsForRole` (MinRole) | viewer tabanı |
| Yürütme | `ai/agent/tools/executor.go:118-165` `Call`: iptal → bilinmeyen → tekrar → kapsam/şema → span → 20 s → audit | iki site: `copilot_chat.go:1039`, `chat_exception_followup.go:299` |
| Audit | `Hooks.Audit` → `s.audit(r,"mcp.tool.call","mcp_tool",…)` `copilot_chat.go:1026-1031`; `s.audit` `anomaly_extra.go:317` | yürütülen her çağrı |
| Dış MCP köprüsü | `chat_mcp_bridge.go` (`<sunucu>__<tool>`, allow/deny, audit `mcp.call`) | **yan etkisi bilinmez**; `readOnlyHint` okunmuyor (grep boş) |

**Durum-değiştiren araç bugün YOK.** `mcptools/no_write_tool_test.go:30` her aracın `MinRole==""`
olmasını zorlar, yazma fiilli adı reddeder. Tek istisna `ConversationStateTools` (`set/get/clear_context`,
`context_tools.go:29`): konuşma başına Redis bağlamı, operatör durumu değil. Yazan tek yol **model-dışı**
tıkla-onayla aksiyonlar: `chat_actions.go` `problemNoteAction` (v0.10.1169; `auth.SignAction` jetonu, tür
`cosre-note`, TTL 2 h; uç `problem_notes.go:34` editor+, audit `problem.note.create` `:99`) ve
`sreHypothesisAction`. Onay bu yüzden **gelecek** araçları kapılar (ilk aday §4.6). Arka plan SRE
orkestratörü (`internal/sre/orchestrator.go`) deterministik okuyucudur, model araç döngüsü değil.

## 2. Fikir A — `stopWhen`: açık, birleşebilir durma koşulları

İlk dilim: diğer ikisi (tavan turu planı, onayın "bekliyor" durması) birer durma nedenidir.
Hedef: `:1431`'deki örtük `||` zinciri adlandırılmış koşullara dönüşür; tel ve etiket **bayt bayt aynı**.

### 2.1 Go tasarımı (yeni dosya `internal/api/chat_stop.go`, SAF)

| Parça | Şekil |
|---|---|
| Durum | `type loopState struct{ Round, MaxRounds, CallsLeft, MaxCalls, Executed, Skipped int; TokensIn, TokensOut uint32; LastCalls []string; CtxErr error }` |
| Koşul | `type stopCond func(loopState) stopReason`; `type stopReason string` ∈ `""`, `round_cap`, `call_cap`, `cancelled`, `tool_called:<ad>`, `token_budget`, `approval_pending` (§4) |
| Birleştirici | `stopAny(conds ...stopCond) stopCond` — ilk boş-olmayan neden kazanır (AI SDK dizisi = OR ile aynı) |
| Hazır koşullar | `stopAtRound(n)` (= `round == n-1`), `stopWhenCallsExhausted()` (= `callsLeft <= 0`), `stopOnCtxDone()`, `stopOnToolCall(name)`, `stopAtTokens(n)` |
| Varsayılan | `defaultStop(maxRounds) = stopAny(stopOnCtxDone(), stopAtRound(maxRounds), stopWhenCallsExhausted())` — sıra bugünkü dal sırası |

**Takılma:** `:1412` → `cancelled`, `:1431` → `stop(state) != ""`; neden S1'de yerel değişken (tele çıkmaz).
`:1149` cevap yolu koşul değil, doğal son (`finishReason:"stop"`). Derin kip sayıları aynı (`deepMode`).

### 2.2 Tel / UI

| Dilim | Değişiklik |
|---|---|
| S1 | **yok** — `chatBudget` alanları, TR etiketler, `data-budget` aynı; `testdata/cosre/*.sse` ve `chat_stream_golden_test.go` altınları dokunulmaz |
| S2 (ayrı, isteğe bağlı) | `chatBudget.stopReason` eklenir → v1 SSE baytı değişir → altınlar + fixture'lar yenilenir; v2 bütçe çipi nedeni gösterir ("tur tavanı" / "çağrı tavanı") |

### 2.3 Denetim / güvenlik / test

| Konu | Karar |
|---|---|
| Audit | yok — admin yazması değil; `cspan.turn` span'ına `ai.loop.stop_reason` özniteliği (gözlem) |
| Güvenlik | koşullar yalnız **daraltır**: hiçbir koşul `maxRounds/maxCalls`'ı büyütemez (tip düzeyinde: `stopAny` yalnız durdurur, devam ettiremez) |
| Test | `chat_stop_test.go` tablo: her hazır koşul × sınır değerleri; `stopAny` sırası; `defaultStop` ≡ eski ifade (tüm `round×callsLeft` ızgarası). Mevcut `chat_round_cap_test.go`, `chat_call_budget_test.go`, `chat_budget_step_test.go`, `chat_roundcap_parity_test.go` (kaynak pini) ve altınlar değişmeden yeşil = sıfır davranış kanıtı |

## 3. Fikir B — `prepareStep`: model çağrısından önce adım ayarı

§1.2'deki üç el yapımı ayar zaten birer prepareStep'tir; tasarım onları **tek kapıdan** geçirir. Kimlik planı = bugünkü bayt.

### 3.1 Go tasarımı (yeni dosya `internal/api/chat_prepare_step.go`)

| Parça | Şekil |
|---|---|
| Girdi | `type stepInput struct{ State loopState; Kind stepKind /*normal\|cap\|degen_retry*/; Conv []copilot.ChatMessage; Specs []copilot.ToolSpec; Role string; Deep bool; Sink string /*v1\|v2*/ }` |
| Çıktı | `type stepPlan struct{ Specs []copilot.ToolSpec /*nil = aynen*/; SystemAddendum string; Ctx func(context.Context) context.Context /*nil = aynen*/ }` |
| Fonksiyon | `type prepareStep func(stepInput) stepPlan`; `prepareChain(fs ...prepareStep)` sırayla uygular (sonraki öncekinin çıktısını görür — AI SDK'nın "undefined = dış ayar" kuralı) |
| Varsayılan | `defaultPrepare = prepareChain(capTurnPlan, degenRetryPlan)`: `cap` → `{Addendum: ChatRoundCapAddendum(), Ctx: WithNoToolCalls}`; `degen_retry` → `{Addendum: ChatDegenRetryAddendum(), Ctx: WithNoToolCalls∘WithMaxTokensCap(chatDegenRetryMaxTokens)}`; `normal` → boş plan |
| Takılma | `:1103`, `:1076`, `:1446` üç çağrı tek yardımcıya iner: `s.loopTurn(ctx, in)` → `plan := prepare(in); chatLoopTurn(plan.ctx(ctx), loopPrompt+plan.SystemAddendum, in.Conv, plan.specsOr(in.Specs), emit)` |
| Model/profil | `Ctx` ile `copilot.WithProfile` (`profiles.go:185`) — Derin profil kuralı bugünkü yerinde kalır (istek başına), adım başına profil **kapalı** (§7) |

**Alt küme kuralı (AI SDK `activeTools`'tan farkı):** sağlayıcı geçmişte geçen aracın tanımını ister
(`copilot_chat.go:1439-1443`) → `plan.Specs` `keepReferenced(conv, subset)` ile zorlanır (çağrılmış araç
düşürülemez); üst sınır `toolsForRole` çıktısı — plan yalnız **daraltır**, araç EKLEYEMEZ.

### 3.2 İlk gerçek kullanıcılar (varsayılan dışı, her biri ayrı karar)

| Plan | Ne yapar | Bağımlılık |
|---|---|---|
| `approvalGatePlan` | `Sink==v1` ya da rol onaylayamazsa onay-kapılı araçları spec'ten düşürür | §4 |
| `discoveryFirstPlan` | tur 0'da yalnız keşif araçları (`list_*`, `resolve_entity`, `get_context`), sonra tam | evalset ölçümü (`chat_eval_mode.go`) — ölçüm yoksa açılmaz |
| `tokenAwarePlan` | `TokensIn` eşiği aşınca `WithMaxTokensCap` | S2 `token_budget` koşuluyla birlikte |

### 3.3 Tel / denetim / güvenlik / test

| Konu | Karar |
|---|---|
| Tel | S3'te **yok**; plan ileride görünür olmalıysa `data-step{label}` (etiket-yalnız çip, `chat_step_phase.go` deseni) |
| Audit | yok (yazma değil); span `ai.turn.tools_n`, `ai.turn.addendum` (ad, metin değil) |
| Güvenlik | plan `toolsForRole` çıktısını aşamaz (test: viewer için `MinRole:"editor"` araç hiçbir planla geri gelmez); `SystemAddendum` yalnız `copilot/prompts.go` accessor'larından (dil kapısı `prompt_language_test.go` kapsar — serbest metin eklenmez) |
| Test | `chat_prepare_step_test.go`: `defaultPrepare(cap)` ≡ eski (`WithNoToolCalls` + aynı ek), `degen_retry` ≡ eski; `keepReferenced` tablo (çağrılmış araç düşmez, çağrılmamış düşer, boş conv); `prepareChain` sırası. Altınlar + `chat_degen_retry_test.go`, `chat_loop_degen_test.go`, `chat_trace_followup_loop_test.go` (seam `copilot_chat.go:77`) değişmeden yeşil |

## 4. Fikir C — tool approval (insan onayı)

Bugün kapılanacak araç yok (§1.3): tasarım **gelecek yazma araçlarının** tek kapısıdır; ilk araç (§4.6)
ayarla, varsayılan kapalı, yalnız v2. Model-dışı tıkla-onayla aksiyonlar (`chat_actions.go`) aynen kalır.

### 4.1 Model: akış durur, yeni istek devam eder (§0 sonucu)

| Adım | Nerede | Ne olur |
|---|---|---|
| 1 | `mcp.Tool.Approval bool` (yeni alan) + `MinRole:"editor"` | katalogda işaret; `TestMCPToolsNoWriteTool` kuralı "MinRole boş" → "MinRole boş **ya da** Approval:true" |
| 2 | `Executor.Call` iptal→bilinmeyen→tekrar→**onay**→kapsam… (`executor.go:118`) | `Hooks.Approve func(ctx,name,cargs) approvalDecision` (nil = bugünkü yol); `ask` → `Outcome{Executed:false, Kind:KindApproval}`; **jeton digest'i kapsam/şema sonrası `cargs` üzerinden** (operatör onayladığı argüman çalışır) |
| 3 | Döngü `copilot_chat.go:1230` dalı | `step-result{skipped:true, reason:"approval_required", approval:{token, expiresAt}}`; kalan çağrılar `call_cap` gibi skipped; `stopReason=approval_pending` → model **yeniden çağrılmaz**, `done{ok:true}` |
| 4 | `uistream/translator.go:128` | `reason=="approval_required"` → `tool-output-available` **yazılmaz** (parça `input-available`'da kalır) + `data-approval{id:"a<i>", data:{toolCallId:"s<i>", tool, argsDigest, token, expiresAt}}`; `done` → `finish{finishReason:"tool-calls"}` |
| 5 | v2 istemci (B3 "Araştırma adımları" üstüne) | `dynamic-tool` parçası + eşli `data-approval` → Onayla / Reddet; tık → `addToolOutput({toolCallId:"s<i>", output:{approved, token}})`; `sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithToolCalls`; `prepareSendMessagesRequest` v1 gövdeye `approvals:[{stepId, tool, args, token, approved}]` ekler (`lib/cosre/useCosreChat.ts`, `cosrePersist.ts toRequestMessages`) |
| 6 | Sunucu devam (`chatRequest.Approvals`, yeni `chat_approval.go`) | jeton doğrula → onay: `exec.Call` (audit `mcp.tool.call` aynen) → sonuç konuşmaya `user/ToolResults` olarak; ret / süre dolmuş: `ToolErrorJSON{error:"operator_denied\|approval_expired", retryable:false}`; döngü **kalan bütçeyle** sürer (jeton `callsLeft/roundsUsed` taşır) |
| 7 | v1 SSE | çip + `skipped` rozeti ("yürütülmedi · onay gerekir"); düğme yok — `approvalGatePlan` (§3.2) v1'de aracı zaten sunmaz |

Pod bağımsız: jeton kendini taşır, sunucu durum tutmaz (`copilot_chat.go:55-57`). Akış içi bekleme
**reddedildi**: 180 s tavan insan gecikmesiyle yarışır, SSE dakikalarca açık kalır, pod yakınlığı ister.

### 4.2 Jeton (`internal/auth/action_token.go` ailesi, yeni tür)

| Alan | Değer | Kontrol (devam isteğinde) |
|---|---|---|
| `k` | `cosre-approval` | notify ignore ucu bu türü REDDEDER (`cosre-note` emsali) |
| `x`,`s` | exchangeId, stepId | istekteki `approvals[].stepId` ile eşit |
| `t`,`h` | tool, sha256(cargs) | istekteki `tool/args` digest'i eşit — argüman değiştirilemez |
| `u` | onay isteyen userID | çağıran = aynı kullanıcı **ve** rol ≥ `approverMinRole` (yeniden okunur; viewer'a düşmüş hesap onaylayamaz) |
| `b` | `{callsLeft, roundsUsed}` | bütçe devri; "onayla da 6 çağrı daha kazan" kapalı |
| `e` | now + `timeoutS` | geçmiş → **ret** (zaman aşımı = ret) |
| tek-kullanım | Redis `SETNX cosre:approval:<sha256(token)> EX timeoutS` | ikinci kullanım → ret + audit `replayed:true` (Redis zorunlu altyapı; yoksa ret) |

### 4.3 Ayar (`system_settings` `ai.toolApproval`, şablon `ai_toolarg_settings.go`)

`{enabled:false, tools:[], timeoutS:300, approverMinRole:"editor", externalAll:false}` —
GET her rol, PUT admin + audit `settings.ai_tool_approval.update` (before/after).
`enabled=false` → onay-kapılı araçlar katalogda **yok** (bayt aynı); `externalAll=true` →
dış MCP araçlarının hepsi `ask` (yan etkisi bilinmediği için; §7).

### 4.4 Denetim

| Olay | `s.audit(action, kind, id, details)` |
|---|---|
| Onay istendi | `copilot.tool_approval.request`, `mcp_tool`, tool, `{exchangeId, stepId, argsDigest, expiresAt}` |
| Karar | `copilot.tool_approval.decision`, `mcp_tool`, tool, `{decision: approved\|denied\|expired\|replayed\|forbidden, latencyMs, exchangeId, stepId}` |
| Yürütme | mevcut `mcp.tool.call` (`copilot_chat.go:1029`) + aracın kendi yazma audit'i (`problem.note.create`) |

### 4.5 Güvenlik özeti

| Tehdit | Karşılık |
|---|---|
| viewer yazdırır | `MinRole:"editor"` → katalogda yok; jeton rolü yeniden okur; `approverMinRole` admin'e çekilebilir |
| injection araç argümanı değiştirir | kart **çalışacak `cargs`** gösterir, model metni değil (`chat_actions.go` doktrini); digest jetonda |
| jeton başka alışverişe / adıma taşınır | `x,s,t,h,u` bağlı; herhangi biri tutmazsa ret |
| sekme kapandı / geç kaldı | `e` → ret; audit `expired` |
| çift tık / tekrar oynatma | Redis tek-kullanım; ilk araç idempotent (aynı digest → aynı satır, `problem_notes.go` sözleşmesi) |
| v1 akışında sahte düğme | v1 araç setinde yok (`approvalGatePlan`); jeton gelse de v1 handler `Approvals` okumaz |

### 4.6 İlk araç: `add_problem_note` (öneri)

| Neden bu | Karşı aday |
|---|---|
| yalnız ekler, durum geçişi yok; `createProblemNote` yolu + audit + idempotent kimlik hazır (`problem_notes.go`); tıkla-onayla emsali var, model-başlatan hâli doğal uzantı; sayfa bağlamı `pageCtx.ProblemID` zaten döngüde | `mute_problem_notifications` (v0.10.1016, bildirimi keser — etki büyük), `silence_log_pattern` (`ActiveSilencedFingerprints`, dedektörü körler), incident/ayar yazmaları (ikinci dalga) |

### 4.7 Testler

| Katman | Test |
|---|---|
| Saf | `chat_approval_test.go`: imzala/doğrula tablosu (süre, yanlış kullanıcı, digest, exchange, adım, rol düşmesi); bütçe devri |
| Executor | `executor_test.go`: `Approve=nil` ≡ eski; `ask` → Executed=false, handler çağrılmadı, audit yok |
| Döngü | seam (`chatLoopHandlerSeam`): kapılı araç → kalan çağrılar skipped, model ikinci kez çağrılmadı, `done{ok:true}`; devam isteği → tam bir yürütme; tekrar → ret |
| Tel | `testdata/cosre/ui-stream-v1-approval.sse` + `.events.jsonl`; `protocol.ts` `DATA_PART_NAMES += 'approval'`, `finishReason` 'tool-calls'; `uiStreamFixture.test.ts`, `TestUIStreamCoversEmittedKinds` |
| Katalog | `toolset_catalog_golden_test.go`: ayar kapalı → bayt aynı; viewer → araç yok |

## 5. Sürüm dilimleri

Her dilim kendi `v0.10.X`'i (numara kesimde; son etiket v0.10.1649; 2b B2–B7 ile serpiştirilebilir).
Kapılar: CLAUDE.md release zinciri + `make audit` + `~/go/bin/golangci-lint run ./...`; tele dokunan dilimde fe üçlüsü.

| # | İçerik | Tel | Kanıt |
|---|---|---|---|
| S1 | `chat_stop.go`: `loopState/stopCond/stopAny` + varsayılan; `:1412/:1431` koşula bağlanır | yok | altınlar + fixture'lar dokunulmaz, `chat_stop_test.go` |
| S2 (ops.) | `chatBudget.stopReason`; v2 bütçe çipi nedeni gösterir | **v1 bayt değişir** → altınlar + fixture yenilenir, B3 çipi | `chat_budget_step_test.go` |
| S3 | `chat_prepare_step.go`: kimlik + üç el yapımı ayar plana taşınır; `keepReferenced`; tek `s.loopTurn` | yok | §3.3 testleri, altınlar |
| S4 | `mcp.Tool.Approval`, `Hooks.Approve`, `KindApproval`, `stopReason=approval_pending`, translator `data-approval` + `finish tool-calls`, `protocol.ts`, fixture, ayar `ai.toolApproval` (kapalı), `approvalGatePlan` | yeni parça adı; araç yok → prod'da görünmez | §4.7 Saf+Executor+Tel |
| S5 | `add_problem_note` (Approval, editor) + `chat_approval.go` devam yolu + audit çifti; **B3 sonrası** v2 Onayla/Reddet kartı + `approvals[]` gövdesi | v2 yalnız; ayar kapalı | §4.7 Döngü + Katalog; v2 UI testi (approve/deny/expired) |
| S6 | operatör: prep'te `enabled=true, tools:["add_problem_note"]`; `/ai` + audit izlenir | — | bir hafta sonra ikinci araç kararı |

## 6. Kapsam dışı

| Dışarıda | Neden |
|---|---|
| Node çalışma zamanı, AI SDK Core / `Experimental_Agent` sunucuda | tek Go binary; AI SDK yalnız UI protokolü (5.0.276 pinli, `aiSdkPin.test.ts`) |
| ai@6 yükseltmesi (`needsApproval`, `approval-requested`) | pinli protokolü değiştirir; ayrı karar — tasarım v5 deseniyle çalışır, v6'da `data-approval` yerine yerli parça kullanılabilir |
| Model / prompt değişikliği | ekler mevcut accessor'lar; onay/ret metni araç sonucu (`RepeatedCallJSON` sınıfı), sistem prompt'u dokunulmaz |
| v1 UI değişikliği | v1 donuk; onay araçları v1'e sunulmaz |
| Akış içi bekleme / pod yakınlığı / yeni CH şeması | jeton kendini taşır; kararlar audit tablosunda |
| Arka plan SRE orkestratörü, exception takip döngüsü (`chat_exception_followup.go:299`) | ilki model araç döngüsü değil; ikincisi salt-okur — Executor kancası nil kalır |
| Adım başına model değişimi | profil kuralı istek başına (`chat_deep.go`); ölçüm yok |

## 7. Operatöre sorular (önerilen varsayılanla)

| # | Soru | Öneri |
|---|---|---|
| Q1 | İlk onay-kapılı yazma aracı? | `add_problem_note` (§4.6); `mute_problem_notifications` ikinci dalga |
| Q2 | Onay zaman aşımı? | 300 s; dolunca **ret** (audit `expired`) |
| Q3 | Kim onaylar? | isteyenin kendisi + rol ≥ editor (REST yazma ile aynı); admin-only ayarla seçilebilir |
| Q4 | Devamda bütçe? | **devredilir** (`callsLeft/roundsUsed` jetonda); sıfırlanmaz |
| Q5 | Dış MCP araçları onay ister mi? | `externalAll=false` (sunucu allow/deny listesi yeter); yan etkili sunucu bağlanınca `true` |
| Q6 | `stopReason` tele çıksın mı (S2)? | evet, B3 çipi nedeni gösterebilir; v1 altınları yenilenir — tek commit |
| Q7 | Tekrar oynatma koruması Redis tek-kullanım mı? | evet; Redis yoksa ret (idempotent ilk araç ikinci emniyet) |
| Q8 | `discoveryFirstPlan` (tur 0 keşif alt kümesi) açılsın mı? | hayır — evalset ölçümü olmadan davranış değişikliği yok |
| Q9 | Dilimler 2b'nin neresine? | S1/S3 hemen (tel yok); S4 B3 ile; S5 B3 sonrası |
| Q10 | Onay kartı kapanınca (Sheet/sekme) ne olur? | hiçbir şey gönderilmez → zaman aşımı reddi; kalıcı mesajda parça `input-available` kalır, "süresi doldu" rozeti |

## Operator approval (2026-10-30)

Approved with all recommended defaults (§ questions). Lean execution: no security scans / CI watching.
