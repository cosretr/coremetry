# CoSRE v2 etkileşim katmanı — Faz 0 denetimi (2026-11-18)

Denetim belgesi; kod değişmedi. Okunan ağaç `v0.10.2347`. Kısaltmalar `investigation-design.md` ("tasarım") ile aynı: `api/` = `internal/api/`, `cop/` = `internal/copilot/`, `mcp/` = `internal/mcptools/`, `ch/` = `internal/chstore/`, `us/` = `internal/cosre/uistream/`, `fe/` = `frontend/src/`, `chat.go` = `api/copilot_chat.go`. Token: **3 bayt ≈ 1 token**. Adlar nötr (checkout, orders, payments). Tekrar edilmeyen kaynaklar bölümle anılır: `conversation-audit.md` ("konuşma"), `sre-evidence-audit.md` ("kanıt"), `chat-ui-audit.md` ("UI"), `quality-loop-audit.md` ("kalite"), tasarım S1–S6.

## 0. Hüküm

İstenen üç parçanın (A adım yönlendirme, B öneri çipleri, C eylem kartı) iskeleti büyük ölçüde var: adım olayları ve bütçe parçası, istemcinin yapısal alan gönderdiği `context.*` ve `approvals[]` devam yolu, imzalı jeton + Redis tek kullanım deseni, kural türevli en çok 3 takip çipi, `action` bloğu ve iki istemci-POST kartı, konuşma paylaşım bağlantısı. **Eksik olan üç şey:** (1) döngü durumu istek ömürlüdür, adım başına hiçbir şey kalıcı değildir — "bu adımdan devam" için adım özeti Redis'e yazılmalı; (2) çipler bugün metin gönderir, yapısal niyet (`intent`) yürütmez ve reddedileni hatırlamaz; (3) eylem hedeflerinden **Problem oluşturma API'si yok**, **Jira entegrasyonu yok**; paylaşım bağlantısı var. Yeni şema gerekmez (`cosre_signals.kind` açık uçlu), `api.go` büyümez. Önemli düzeltme: mevcut `followup_click` sinyali gece kalite puanına 0,05 ağırlıkla girer; yeni sinyaller 0 ağırlıkla girmeli (§5).

## 1. Döngü bugün nasıl ilerler

| Konu | Durum | Kanıt |
|---|---|---|
| Tur / çağrı tavanı | var | 5 tur, 6 çağrı (`chat.go:63`, `:68`); plan bütçesi ve Derin kip (`chat.go:1260-1267`); `ai.loopBudget` daraltır (`api/ai_loop_budget_settings.go:42`, `:59`) |
| Token bütçesi | var | tur bütçesi bağlam penceresinden, eski sonuçlar kısaltılır (`api/chat_round_budget.go:129`, `:167`; DECISIONS v0.10.2340) |
| Durma nedeni | var | adlandırılmış koşullar `round_cap / call_cap / cancelled / token_budget / tool_called:` (`api/chat_stop.go:17-27`), tek değerlendirme (`chat.go:1715-1720`) |
| İptal | var, özetsiz | iptalde model yeniden çağrılmaz, `error` olayı (`chat.go:1721-1729`); bağlantı kapandığı için sunucu özet gönderemez (konuşma §3.8) |
| Ara durum satırı | var | `data-status` transient (`us/translator.go:89`; `api/chat_status_line.go:83`) |
| Adım parçası | kısmi | araçlı adım AI SDK `tool-input-*` parçasıdır, `data-step` yalnız araçsız etiketli adım; bütçe ayrı `data-budget` (`us/translator.go:133-162`). Başlık / sinyal / tek satır sonuç / kanıt bağı / harcanan bütçe alanları yok; aile haritası var (`api/chat_step_phase.go:20-65`, konuşma §3.8) |
| Adımdan yeniden başlama | yok | aşağıda |

**Döngü durumu nerede.** Bütün döngü durumu (`conv`, `callsLeft`, `round`, sonuçlar) istek yığınındadır (`chat.go:1260-1267`). İstekler arası tek kalıcı yapı `ChatContext`'tir: Redis `copilot:ctx:<kullanıcı>:<konuşma>`, 24 sa; yazım istek sonunda (konuşma §3.2). İstemci geçmişi yalnız metin taşır; araç çağrısı ve sonucu düşer (`api/chat_history_sanitize.go:32-50`, konuşma §3.2). `cosre_interactions` araç çıktısını tutar ama 4 KB kırpılmış ve kuyruklu asenkron yazılır (`api/cosre_interactions.go:37`, `:73-94`; şema `ch/store.go:2986-3028`) — birkaç saniye içinde okunacak durum kaynağı olamaz.

**Emsal: onay devamı.** İstemci aynı sohbeti `approvals[]` ile yeniden yollar (`chat.go:211-214`, `fe/lib/cosre/useCosreChat.ts:78-85`); jeton exchange, adım, araç, argüman özeti, kullanıcı ve **kalan bütçeyi** imzalar (`internal/auth/approval_token.go:28-43`); Redis SETNX tek kullanım, Redis yoksa ret (`api/chat_approval.go:129-137`); devam konuşması sentetik araç çifti olarak kurulur (`api/chat_approval_resume.go:302-319`); akış aynı asistan mesajına yazılır (`useCosreChat.ts:92-96`). Adım yönlendirmesi bu desenin genellemesidir.

**"Bu adımdan devam" için gerekenler.**
1. Adım tamamlandıkça (istek sonunu beklemeden) Redis'e özet: `copilot:steps:<uid>:<konuşma>:<exchange>`, TTL 2 sa (durum TTL'iyle aynı, `api/chat_state_resolve.go:51`). Adım başına ≤1 KB: `{id, order, tool, args ≤512 B, signal, ok, ms, view ≤600 B (S3 model görünümü), evidenceRef?, spent {calls, rounds}}`; ≤12 adım ⇒ ≤12 KB. Alışveriş düzeyi: sınıf, `ServiceRef` (S1), pencere, profil.
2. `data-step` jetonu: onay jetonunun ayrı `Kind`'ı (`step`), exchange + adım + kullanıcı + son kullanma; bütçe jetonda değil Redis'te.
3. İstek alanı `stepRedirect {exchangeId, stepId, directive, token}`; `directive` kapalı küme `continue | metric | trace | log | skip | summarize`.
4. Sunucu: jeton + kullanıcı + Redis kaydı → konuşma = metin geçmişi + adım ≤k özetleri sentetik araç çifti olarak + tek satır yönerge; adım k+1 ve sonrası modele gitmez. `metric/trace/log` yönergesi araç ailesini o tur için daraltır (mevcut `prepareStep` dikişi, tasarım §2.3). Yeni exchange, yeni dal; önceki adımlar istemcide kalır.
5. `summarize`: iptalden sonra ayrı istek; model döngüsü yok, anlatım profili adım özetlerinden ≤3 cümle yazar.
6. Redis yoksa (Noop önbellek) yönlendirme kapalı: çip gösterilmez (onay yolunun "Redis yok = ret" kuralı).

## 2. İstemciden sunucuya yapısal olay

| Kanal | Durum | Kanıt |
|---|---|---|
| `context.*` alanları | var | `userTeam`, `unscoped`, `skipEndpointPick`, `clarifyOption`, `state`, `statePatch` (`chat.go:190-210`); yama doğrulanır, geçersizse bütünüyle red (`api/chat_state.go:201-202`, `:749-761`) |
| Üst düzey devam alanı | var | `approvals[]` (`chat.go:211-214`); FE `prepareSendMessagesRequest` yalnız `context` ve `approvals` geçirir (`useCosreChat.ts:78-85`) |
| Gizli mesaj parçası / `metadata` | yok | `toRequestMessages` mesajları metne indirir; sunucu `sanitizeClientHistory` rol dışını düşürür (konuşma §3.2). Gizli parça ile olay taşımak sanitizer'ı delmek demektir; önerilmez |
| Yapısal çip niyeti | kısmi | `explainAsk` çipi `context.explainAsk` gönderir (`api/chat_followup_chips.go` `chatFollowup.ExplainAsk`); genel `intent {tool,args}` yok |

**Öneri.** `step_redirect`, `suggestion` ve eylem için `approvals` gibi **üst düzey** alan: `stepRedirect`, `suggestion {id, token}`. Böylece "devam isteği" ayrımı (`isResume`, `useCosreChat.ts:55-58`) tek yerde genişler. Eylem onayı sohbet akışından geçmez, ayrı uca gider (§4).

**Altın dosyalar.** Go: `api/testdata/chat_stream/{v1,v2}_{help,loop}.sse`, `v2_question.sse` (`api/chat_stream_golden_test.go:34`); paylaşılan çiftler `testdata/cosre/ui-stream-v1*.sse|events.jsonl` (FE `fe/lib/cosre/uiStreamFixture.test.ts`). Ayar kapalıyken hiçbiri değişmez; her dilim kendi yeni çiftini ekler (`-steps`, `-redirect`, `-suggestions`, `-action`). İstek gövdesi alanları altına girmez. FE ayrıştırıcıları `fe/lib/cosre/protocol.ts` (`data-approval` `:109`, `data-status` `:200`, `data-state` `:276`) — her yeni parça için bir ayrıştırıcı + test.

## 3. Çip adayları için veri

| Kaynak | Durum | Kanıt | Not |
|---|---|---|---|
| Deploy / rollout | var | `list_deployments` kaynak `inferred | event | rollout` (`mcp/list_deployments.go:41`, `:164`); `get_correlated_changes` (`mcp/correlate_deploy.go:29`); çip kuralı yalnız tarama bulgusundan (`chat_followup_chips.go:12`) | S4 paketi P3 doğrudan aday verir |
| Kanal / fonksiyon kodu dağılımı | kısmi | `find_traces.channel_code/function_code` süzgeci (`mcp/v2_traces_logs.go:158-159`), `trace_stats group_by attr` (`mcp/trace_stats.go:39`), `resolve_business_code` (`mcp/business_code.go:268`) | dağılım ayrı okuma ister; paket dışında |
| `search_knowledge` | kısmi, veri yok | araç var (`mcp/knowledge_tools.go:330`), RAG kapalı olduğu için satır dönmez (kanıt §3.4) | aday kaynağı değil |
| Wiki | kısmi | `search_wiki` (`mcp/wiki_tools.go:39`); bulgudan sorgu türeten kod yok (kanıt §3.4) | K3'e kadar aday değil |
| `ml_events` | var | `get_log_anomalies`, `explain_ml_event` (`mcp/v2_ml.go:40-41`); S4 P5 | aday: "anomaliyi aç" |
| Mevcut takip çipleri | var | §3.1 | B ile ≈ %70 örtüşür |

### 3.1 `chat_followup_chips.go` bugün

Kurallar araç sonucundan olgu çıkarır, modelden asla (`:1-23`); olgu türleri ve öncelik `hypothesis > exception_group > op_topology > pod > trace > slow > errors > deploy` (`:41-58`); eşikler 1,5× / 2× gecikme, %5 hata, %85 pod sınırı (`:58-65`); en çok 3, sorulmuş mesajla Jaccard ≥0,8 benzeri düşer (`:478-503`); rol ve sayfa problemine göre kapı (`:413`, `:586`); çip türleri metin, aksiyon (`sre_hypothesis`), link, seçici, açıklama (`chatFollowup`, `:96-117`). Tel: `answer.followups` → `data-followups` (`us/translator.go:271-279`). Tık sinyali `followup_click` (`internal/cosrelearn/signals.go:30`).

**B'ye göre fark:** (a) metin çipi router'dan geçer, `intent {tool,args}` doğrudan yürütülmez; (b) yalnız sorulmuş metin elenir, gösterilip yok sayılan / reddedilen hatırlanmaz (emsal: `ChatContext.Corrections[].proposed`, `api/chat_context.go:84-87`); (c) sıra sabittir, model sıralamaz; (d) S4 paket olguları (istisna grubu, deploy, anomali, bağımlılık) kurala bağlı değil; (e) yalnız döngü ve guided yolunda.

**Doğrudan yürütme güvenliği.** İstemciden gelen `{tool,args}` yürütülmez; sunucu çip başına jeton basar (onay jetonu deseni, `Kind: chip`, argüman özeti bağlı) ve tıkta jeton + kullanıcı kapsamı doğrulanır, araç yine v2 kataloğu ve kapsam dikişinden geçer (tasarım karar 10).

## 4. Eylem hedefleri

| Hedef | Durum | Kanıt |
|---|---|---|
| Problem oluşturma | **yok** | `/api/problems` altında yalnız acknowledge (`api/api.go:883`), resolve / delete (`api/problems_bulk.go:26-27`), not (`api/problem_notes.go:35`), araştırma (`api/sre_status.go:7`). Yazım yalnız dedektör yolunda `UpsertProblem` (`ch/problem.go:2031`); kullanıcı ucu, rol, idempotency yok |
| Probleme not | var | `POST /api/problems/{id}/notes`, editor+, imzalı jeton (problem + metin özeti + çağıran), aynı jeton = aynı satır, audit `problem.note.create` (`api/problem_notes.go:8-14`, `:35`); onay kapılı araç yolu (`api/chat_approval_resume.go:1-18`) |
| Jira | **yok** | Kodda tek anış izleyici içe aktarmasında "desteklenmeyen eylem" (`internal/watcher/watcher.go:228-230`) ve bir yorum (`fe/pages/PublicTrace.tsx:17`). İstemci, ayar, sır alanı yok |
| Sohbet paylaşım bağlantısı | var | `POST/DELETE /api/ai/conversations/{id}/share`, `GET /api/ai/shared/{id}`, üç rol (`api/ai_conversation_share.go:28-37`); var olan jeton korunur, yani doğal idempotent (`:94-101`); audit `ai_chat.share` (`:122`); jeton blobda (`api/ai_conversations.go:264-266`). Paylaşılan şey konuşmanın tamamıdır; "özet" yok. Konuşma önce istemci upsert'iyle kaydedilmiş olmalı (`api/ai_routes.go:166-169`) |
| İstemci-POST kart emsali | var | `cosre_alias` ve operatör kuralı kartı: sunucu yalnız önerir, "Kaydet" tıkı kullanıcının kendi oturumuyla POST (`api/chat_alias_proposal.go:3-8`); `action` bloğu yalnız araç sonucundan türer, model metninden asla (`api/chat_actions.go:16-21`) |
| Idempotency başlığı | yok | kodda `Idempotency-Key` yok; emsal imzalı jeton + Redis SETNX (`api/chat_approval.go:129-137`) ya da jetondan türeyen satır kimliği (`problem_notes.go`) |
| `registerCosreActionRoutes` | yok | yeni dosya `api/cosre_actions.go`, `registerRoutesExtra` ile; `api.go` 9.862 satır = taban (`.claude/baselines/api_go_lines`) |

Eksikler §6 açık sorularındadır (Problem oluşturma, Jira, özet bağlantısı).

## 5. `cosre_signals`

Şema: `exchange_id, created_at, user_email, kind LowCardinality, detail String` — append-only, 180 gün (`ch/store.go:3030-3045`). Uç `POST /api/ai/signal`, kimlikli her kullanıcı, audit yok, kullanıcı başına 60/dk (`api/cosre_quality.go:3-12`, `api/cosre_signal_rate.go:4`). İstemci kapalı kümesi `ClientSignalKinds` (`internal/cosrelearn/signals.go:52-56`); FE tipi `fe/lib/types.ts:9217`.

**Dikkat:** `followup_click` gece kalite puanına +0,05 ile girer (`signals.go:110`) ve "yeniden sorma" türetmesini değiştirir (`:198-205`); gece puanı en kötü cevapları evalset adayı yapar (kalite §3.5). Yani çip tıkı bugün dolaylı olarak aday seçimine dokunur. Kalıcı kural (örtük sinyal ne çalışma zamanı kararı ne değerlendirme etiketi) için yeni türler **ağırlık 0** ve aday seçiminde okunmaz; pin testi `note` emsaliyle.

Şema değişmez. Yeni türler (`detail` JSON, ≤512 B):

| Tür | Kaynak | `detail` |
|---|---|---|
| `suggestion_click` | istemci | `{chip, rule, tool, rank}` |
| `suggestion_ignored` | istemci, exchange başına bir kez: çipler görünürken kullanıcı yazdı ya da yeni konuşma açtı | `{chips[], rules[]}` |
| `step_redirect` | sunucu (istek geldiğinde) | `{step, directive, kept}` |
| `step_summarize` | sunucu | `{steps}` |
| `action_confirm` / `action_cancel` | sunucu (confirm ucu) / istemci (iptal) | `{kind, result}` |

`followup_click` geriye dönük kalır; `data-suggestions` açıkken çip tıkı yalnız `suggestion_click` yazar (çift sayım yok). Kolon eklenmez: `conversation_id` gerekirse `cosre_interactions` ile `exchange_id` üzerinden birleşir.

## 6. Tasarımın yeniden kesimi

**Ayar.** `cosre_v2` bu kurulumlarda zaten açık (`api/cosre_v2_flag.go:40`); "v1 değişmez" yetmez, v2 davranışı da açılana kadar aynı kalmalı. Tasarım karar 25 emsaliyle `cosre_conversation` bloba tek alan: `interaction = off | on` (varsayılan off). Kapalıyken bütün `.sse` altınları bayt aynı.

**Dilimler** (her biri kendi sürümü, doğrudan main, `docs/frontend/migration-log.md` girdisi, `scripts/gate.sh all`, `go test -race`):

| Dilim | Kapsam | S ile ilişki | Risk | Varsayılan |
|---|---|---|---|---|
| **I1** adım sözleşmesi | `interaction` alanı; adım olayına `title ≤60 rune`, `signal` (aile haritasından), `state`, `result ≤100 rune`, `evidenceRef`, `spent {calls, rounds}`; araçlı adımda da eşli `data-step` (`id` = `s<i>`, tool parçası değişmez); adım tamamlandıkça Redis özeti; `data-step` jetonu | S3 sonrası (`evidenceRef` = blok kimliği; S3 yoksa boş); S4 paket adımları aynı sözleşmeyle | düşük-orta: adım başına Redis yazımı (≤1 KB) | off |
| **I2** yönlendirme + durdur-özetle | `stepRedirect` alanı, jeton + Redis + kullanıcı doğrulaması, devam konuşması kurucu (onay devamı deseni), aile daraltma, yeni dal; `summarize` anlatım çağrısı; Redis yoksa kapalı | S6'dan sonra (anlatım profili); S6 yoksa `chat` profili — davranış aynı, yalnız tek aile | orta-yüksek: dal ve geçmiş tutarlılığı, iptal sonrası ikinci istek | off |
| **I3** `data-suggestions` | olgu kurallarına S4 paket olguları; çip `{id, label ≤40 rune, intent {tool,args}, token}`; tık = etiket kullanıcı mesajı + `suggestion` alanı, sunucu niyeti router'sız yürütür; oturum içi gösterilen / reddedilen kaydı `ChatContext`'te; açıkken `data-followups` yerine geçer | S4 ile aynı olgu kaynağı; sıralama / ifade S6 anlatım profili (açık soru 3) | orta | off |
| **I4** sinyaller | §5 türleri, ağırlık 0, aday seçiminden hariç; FE tip | I3/I2/I5 ile birlikte iner | düşük | — |
| **I5** `data-action` | `api/cosre_actions.go` `registerCosreActionRoutes` (`requireCopilot` sarılı); kurallar yalnız araç sonucundan öneri (model eylem aracı görmez); form kartı Onayla/İptal; `POST /api/cosre/actions/confirm {token, kind, fields, idemKey}`: kullanıcının kendi rolü, kapsam (`api/chat_user_scope.go:125`), jeton + Redis SETNX, audit `cosre.action.<kind>`; sonuç `data-action-result` (sonraki akışta değil, uç cevabından istemcide eklenir). İlk türler: paylaşım bağlantısı, probleme not (mevcut uçları sarar). Problem oluşturma ve Jira açık sorulara bağlı | S1 kapsam kuralı | orta: yazma yolu; yetkisiz kullanıcı ve yeniden gönderim testleri zorunlu | off |

Sıra: S1 → S2 → S3 → **I1** → S4 → **I3+I4** → **I5** → S5 → S6 → **I2**. I5 S4'ü beklemez; yalnız S1.

**Testler (dilim başına özet).** I1: kapalıyken akış bayt aynı; adım metin tavanları; Redis kaydı ≤1 KB ve TTL. I2: başka kullanıcının jetonu, süresi geçmiş jeton, ikinci kullanım, Redis yok → red; adım k+1 modele gitmez (istek gövdesi pin); `summarize` model döngüsü çağırmaz. I3: aday yalnız olgudan (modelin uydurduğu aday atılır — sıralama çıktısı kimlik kümesine süzülür); reddedilen çip aynı konuşmada çıkmaz; sahte niyet / değiştirilmiş argüman → red; kapsam dışı servis → `out_of_scope`. I5: viewer'ın editör eylemi 403; başka kullanıcının kapsamı 403; aynı `idemKey` ikinci kez = aynı sonuç, ikinci yazım yok; audit satırı; prod'a dokunan tür (restart / rollback / scale) kapalı kümede yok (pin).

**Riskler.** (1) Yönlendirme dalında eski adımların sonuçları özet olarak gider; model ayrıntıyı yeniden okumak isteyebilir → memo zaten 30 sn (tasarım §4.1), aksi hâlde ek tur. (2) Çipin doğrudan yürütülmesi router'ı atlar; guided kademenin sağladığı netleştirme kartları çıkmaz. (3) İptal sonrası özet ikinci istek ister; kullanıcı beklemeyebilir. (4) `suggestion_ignored` gürültülüdür; yalnız gözlem için. (5) Problem oluşturma Problem standardı (D1–D13, kural kimliği, parmak izi) ile çakışabilir.

**Tur başına token etkisi.**

| Parça | Model girdisi | Akış |
|---|---|---|
| I1 adım alanları | 0 | adım başına ≈ +180 B (≤6 adım ≈ +1 KB) |
| I2 yönlendirme | taban (katalog ≈ 21.000) aynı; adım özetleri k × ≤600 B ≈ k × 200; yeniden koşulmayan k araç turu ≈ k × 25.000 tasarruf | — |
| I2 özet | ≈ 2.000–2.500 (≤6 KB özet + 500 B istem), çıktı ≤150 | ≤500 B |
| I3 kural + şablon | 0 | ≤3 × 150 B |
| I3 model sıralama | ≈ 450 giriş (olgu ≤600 B + ≤6 aday ≈ 400 B + istem 300 B), ≈ 60 çıktı | aynı |
| I5 eylem | 0 (araç yok, katalog değişmez) | kart ≈ 400 B |

## 7. Açık sorular (her biri öneriyle)

1. **Problem oluşturma** ucu yok. Öneri: I5'in ilk sürümünde yok; Problem standardı programına "elle Problem" kararı olarak gitsin (kural kimliği `manual.cosre`, parmak izi = servis kimliği + başlık + gün, editor+, audit `problem.create`). O gelene kadar kart "probleme not" ve "bağlantı paylaş" sunar.
2. **Jira** entegrasyonu yok. Öneri: bu programda yapılmaz; kart Jira seçeneğini göstermez. İstenirse ayrı Faz 0 (Ayarlar'da sır alanı, `system_settings` blobu, kimlik eşleme). Ara çözüm: paylaşım bağlantısı + "Jira için metni kopyala" düğmesi (istemci, sunucu yazımı yok).
3. **Çip sıralama ve ifadesi modelle mi.** Öneri: varsayılan deterministik şablon (0 token); model sıralaması `interaction` altında ikinci anahtar değil, S6 eşlemesi varken anlatım profiliyle açılır; çıktı aday kimlik kümesine süzülür.
4. **Paylaşım = konuşmanın tamamı** mı, özet mi. Öneri: mevcut tam konuşma bağlantısı; "özet bağlantısı" yeni yüzey ister, şimdilik yok. Konuşma kaydedilmemişse onay ucu önce sunucu tarafında upsert etmez, kart "önce kaydedilmeli" der (sahiplik istemci upsert'inde).
5. **Yönlendirme dalının bütçesi.** Öneri: yeni exchange, kendi tavanı (5 tur / 6 çağrı); korunan adımlar bağlamdır, tavandan düşmez. Alternatif (kalan bütçe) kullanıcıyı hızla tavana iter.
6. **Redis yokken yönlendirme.** Öneri: kapalı; adım kartı yönlendirme düğmesi göstermez (onay yolu emsali). İstemci yankısı (`context.state` gibi) adım özetleri için çok büyük olur.
7. **`step_redirect` adım tıkı tek mesaj mı.** Öneri: yönerge kullanıcı mesajı olarak görünür ("payments için loglara bak"), yanında yapısal alan; geçmiş metni tutarlı kalır.
8. **`followup_click` ağırlığı.** Öneri: yeni türler 0; mevcut 0,05 de 0'a çekilsin mi — operatör kararı (kural "örtük sinyal değerlendirme etiketi olmaz" ile çelişiyor).
9. **Eylem önerisi kimden.** Öneri: yalnız kural (araç sonucu + sayfa bağlamı), model eylem aracı görmez; "model önerir" isteği model metninin eylem doğurması değil, kuralın kartı ancak ilgili bulgu cevapta varken eklemesi olarak okunur.
10. **`data-step` ile tool parçası çiftliği.** Öneri: araçlı adımda tool parçası kalır (AI SDK sözleşmesi, altınlar), `data-step` eşli ek parçadır; FE adım listesini `data-step`'ten çizer.
11. **`interaction` tek alan mı üç alan mı.** Öneri: tek alan; I1–I5 aynı kapı, dilimler kodu kapalı iner, operatör tek seferde açar.
