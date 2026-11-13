# CoSRE deneyim audit'i — seçenekli soru, kaynak çipi, kısa özet + link kartı (iş 6 + 8)

Tarih 2026-11-13. Audit-first belge; kod değişmedi. Üst belge: `docs/cosre/roadmap-status.md`
(Onay, karar 3; §2 satır 6 ve 8). Kısaltma: `api/` = `internal/api/`, `fe/` = `frontend/src/`,
`uistream/` = `internal/cosre/uistream/`, `mcp/` = `internal/mcptools/`. roadmap-status'taki
dosya:satır iddiaları koda karşı yeniden okundu; sapmalar §1 sonunda. Dürüst sonuç: altyapı
(niyet, netleştirme, `data-pick`, `data-links`, kaynak çipi) var ve ortak; eksik olan iki şey
genel bir soru parçası ile deterministik bir özet+link kartı. İkisi de v2'ye özel, küçük,
test edilebilir. Asıl risk teknik değil: operatörün 2026-10-29 "çipler çok kullanılmıyor" kararı
ile seçenekli soru kartı çelişebilir; §8 S2 bunu sorar.

## 1. Bugünkü durum (kanıt)

Kademe sırası `api/copilot_chat.go`: clarify-reply :487-504 → wiki/doc/entity :506-604 → v2
route-turn :617-627 → endpoint-pick :628-642 → guided :643-653 → source-ask :692 → drawer :700
→ RAG :711 → LLM niyet :727-733 → serbest araç döngüsü. Sıra `chat_tier_order_test.go:60` ile
pinli; her yeni kademe bu testi günceller.

| Konu | Durum | Kanıt |
|---|---|---|
| Niyet, deterministik | Regex/jeton yönlendirici `routeGuidedIntentExt`; sabitler `guidedIntent*` | `api/copilot_guided.go:1223`, `:61-192` |
| Niyet, LLM | Tek katı-JSON çağrı; beyaz liste 26 niyet + `none`; güven eşiği YOK (sunucu doğrular); mod `off/on_no_loop/on` | `api/copilot_intent.go:46-82`, `:91`, `:381-382`, `:456-500` |
| `anchored_ask` / `product_ask` | Kodda YOK (`internal/` grep boş); bellek notuyla uyumlu | — |
| `off_topic` | LLM'siz sınır + `offTopicVeto` (servis/takım/telemetri sözcüğü varsa iptal) | `copilot_intent.go:433`, `chat_offtopic_general.go` |
| Guided cevap şekli | `delta` markdown + `emit("answer",{text,suggestions,links,followups})`; sonuna `"\n\nKaynak: "` eki | `copilot_guided.go:2442`, `:2468-2472` |
| Servis/takım belirsizliği | `guidedAskService` + `ServiceOptions` / `teamAskOptions` (≤8) → yalnız `suggestions` METİN çipi | `copilot_guided.go:186`, `:263`, `:2568`, `:3320`, `:3338` |
| Netleştirme | DÜZ METİN soru; durum Redis `ChatContext.Pending`, TTL 10 dk, `takePending` okur-siler; türler `source`, `trace_synth`, `doc_or_entity`; cevap `clarifyReplyClass`, tanınmazsa `dropped` | `api/chat_clarify.go:3-12`, `:37-40`, `:101`, `:228`, `:248`, `:301`; `chat_clarify_entity.go:56` |
| Uç nokta kartı | `needsEndpointPick` SAF; seçenekler spanmetrics_1m'den deterministik, görünür 4 / havuz 200; yalnız v2; `emit("pick")` → `data-pick {kind:"endpoint",service,env?,rangeS,total,capped?,items[{route,rps,p95Ms,errPct}]}` | `api/chat_endpoint_pick.go:40-44`, `:118`, `:140-148`, `:349-350` |
| Uç nokta seçimi dönüşü | "İncele" = yeni kullanıcı mesajı `"<servis> <route>"` + `context.skipEndpointPick`; "Hepsini özetle" = aynı soru `regenerate` | `fe/components/cosre/conversation/endpointPick.ts:6-9`, `:74-81`; `CosreChatV2.tsx:218-231` |
| Takım kartı | Sunucu `data-scope {unscoped,reason,team?,services?,count,teamPick?}`; FE yalnız `reason==='no_team' && teamPick===true` ve son mesajda; seçim `regenerate` + `context.userTeam` | `api/chat_user_scope.go:66`, `:125`; `fe/.../teamPick.ts:10`, `MessageList.tsx:85`, `:150`; `protocol.ts:139` |
| Kaynak çipi | `sourcePart`: `ref` varsa `source-url {sourceId:"doc#chunk",url,title}`, yoksa `source-document`; FE `sourceChips` + `SourceList` | `uistream/translator.go:263-278`; `fe/components/ai/ChatBubble.tsx:1012`, `:867-891`, `:1218` |
| Link çipleri | `answer.links` → `data-links {links:[{href,label}]}`; kaynak `toolCallLink`/`guidedAnswerLink` | `translator.go:240-241`; `api/chat_tool_links.go:62-92` |
| Araç `page_url` | Beyaz liste `v2PageURLTools`; `V2PageURL`; `data-tool-result.data.page_url` (1024 B çekirdek); `DeepLink` | `mcp/v2_tools.go:85-95`, `:312`; `uistream/tool_result.go:52,84-86,135`; `chat_tool_links.go:154-175` |
| how_to | LLM'siz `GuideFor`+`RenderGuideTR`; `links` tek tık, `open` ASLA; konu yoksa ≤6 konu çipi + 2 sabit link | `api/copilot_howto.go:11-14`, `:18-45`, `:64` |
| Cevap uzunluğu | Yalnız prompt kuralı (`systemGuidedChat` "1-2 cümle", `ChatAnswerStyle` ilk satır `**Özet:**`) + token tavanı `cappedMaxTokens`; son işlem yalnız `filterModelChartFences`. Sunucu tarafı özet/kart kararı YOK | `internal/copilot/prompts.go:2139-2152`, `:2205-2213`; `provider_calls.go:67`; `copilot_guided.go:2443` |
| v1 yazıcı | `v1OnlySink.Emit` `v2OnlyEventKinds={stop,scope,pick}` düşürür; `code-lock` listede DEĞİL (v1'e gidiyor) | `api/chat_stop_event.go:56-66`; `chat_sink.go:34` |
| Bilinmeyen parça | Translator fallback `data-unknown` | `translator.go:290` |
| FE akış ayrıştırma | `useChat` + `onData` yalnız `data-reset`; parça eşleme `toChatTurn.ts channelsOf`; kart ayrıştırıcı `protocol.ts`; render `MessageList.tsx` | `fe/lib/cosre/useCosreChat.ts:7-17`, `toChatTurn.ts:134-179`, `protocol.ts:188` |
| Altın `.sse` | v1_help/v1_loop/v2_help/v2_loop; pick ve links yalnız satır içi JSON testinde | `api/testdata/chat_stream/`; `translator_test.go:200` |

roadmap-status sapmaları: `chat_user_scope.go:125` bir "kart" değil `resolveUserScope`
imzası (kart kararı `chat_team_scope_need.go`'da; içeriği okunmadı). `ChatBubble.tsx:1012`
çipi KURAN memo; render :867-891/:1218. `translator.go:275-277` doğru (sourcePart içi).
`TeamPickCard`, `chat_user_scope.go` değil `data-scope.teamPick` ile tetiklenir.

## 2. Boşluklar

1. Genel `data-question` parçası yok (`internal/` ve `fe/` grep boş). Kapalı cevap kümeli
   sorular (`source`: "Coremetry verisi mi, wiki mi?"; `doc_or_entity`; `trace_synth`
   "trace'iyle de inceleyeyim mi?") düz metin; kullanıcı yazmak zorunda, yanlış anlaşılırsa
   `dropped` çipi görünür (`project-cosre-clarify-2026-10-29`: canlıda denenmedi).
2. "Kısa özet + link kartı" tipi yok. `**Özet:**` yalnız prompt kuralı; gemma sınıfı modelde
   garanti değil. Linkler çip olarak gidiyor, kartla birleşmiyor (`LinkCard|SummaryCard`
   grep boş).
3. Karar dağınık: `needsEndpointPick` SAF, ama "soru mu, kart mı, metin mi" tek yerde değil.
4. Takım/servis seçenekleri (`guidedSuggestions`) metin çipi; tıklama = soru metni gönderir;
   `data-pick`/`data-scope` ile üç ayrı "seçtirme" mekanizması var.
5. `code-lock` v1'e sızıyor (`v2OnlyEventKinds` dışı) — bu işin kapsamı dışı, not.

## 3. Öneri tasarım

### 3.1 Akış parçası sözleşmesi (uistream)
- Yeni olay `question` → `data-question` (id `"question"`), payload:
  `{kind:"source"|"doc_or_entity"|"trace_synth", text, options:[{id,label}], allowFree:true}`.
  Seçenek ≤3; `text` düz cümle (soru metni bugünkü `sourceAskTextTR` vb. ile AYNI kalır).
- `v2OnlyEventKinds`'e `question` eklenir → v1 yazıcı düşürür; v1 bugünkü düz metni aynen alır
  (v1'e yeni davranış yok). `uistream/part.go:13-17` yorumu güncellenir.
- Özet kartı için YENİ parça yok: `data-links` payload'ı opsiyonel `title`, `summary` alır
  (`omitempty`). Alan yoksa FE bugünkü çipleri çizer; varsa kart. v1 `toChatTurn` ek alanı
  yok sayar → v1 değişmez; mevcut altın `v2_loop.sse` baytı etkilenmez.
- Elenen: `data-pick`'i `kind:"question"` ile genişletmek — `parseDataPick` ve
  `EndpointPickCard` uç nokta şemasına (rps/p95/err, arama kutusu) bağlı; karışır. Ayrı
  `data-summary-card` parçası — yeni parça + yeni ayrıştırıcı + yeni kalıcılık sanitasyonu
  (`ai_conversations_v2parts.go:66`) gerektirir, kazanç yok.

### 3.2 Soru kartı ile `data-pick` ilişkisi: YAN YANA, birleştirme yok
- `data-pick` = katalogdan veriyle seçim (çok öğe, arama, metrik sütunu). `data-question` =
  kapalı küçük soru (≤3 cevap). Ortak noktaları yalnız "son mesajda etkin, eskisinde pasif"
  kuralı (`MessageList.tsx:85` deseni).
- Cevap dönüşü uç nokta deseniyle aynı: tıklama = seçenek `label`'ı yeni kullanıcı mesajı +
  `context.clarifyOption:<id>`. Sunucuda `takePending` sonrası `clarifyOption` varsa
  birebir eşleme, yoksa bugünkü `clarifyReplyClass`. Serbest metin yolu AYNEN kalır
  (`allowFree`). Böylece `ChatContext.Pending` / TTL / `dropped` davranışı değişmez.
- Guided `suggestions` çipleri (takım/servis) BU İŞTE dokunulmaz (§8 S1 kapsam).

### 3.3 Kısa özet + link kartı: deterministik, modelden DEĞİL
- Özet cümlesi sunucuda SAF `guidedSummaryTR(route, bundle) (string, bool)`: bundle'daki
  sayılardan tek düz cümle (emsal: `endpointDeltaTR`, v0.10.1783). Sayı yoksa `false` → kart
  yok, çip yok değişikliği yok. Model çıktısı (`**Özet:**`) OKUNMAZ.
- Linkler `answer.links`'ten (`toolCallLink`/`guidedAnswerLink`, kaynağı `page_url`), href
  ile tekilleşir, ≤3. Yeni ES/CH sorgusu YOK (bundle zaten okunmuş).
- Başlangıç rotaları: `service_health`, `problems`, `my_services` (bundle sayıları hazır,
  en sık). Diğer rotalar prod ölçümünden sonra.
- Kart: başlık (servis/kapsam adı), bir cümle, altında link düğmeleri; teknik kanıt yok
  (operatör: sade). Yer: cevap metninin ALTINDA, `MessageList` içinde (ChatBubble'a dokunulmaz).

### 3.4 Sunucu karar dikişi
- `api/chat_question.go` (yeni dosya, `api.go` büyümez): `clarifyQuestion(kind, ctx)
  (questionPayload, bool)` SAF; `chat_clarify.go` her üç tier'da metni emit ettikten sonra
  `if q, ok := clarifyQuestion(...); ok && cosreV2Marked(req) { emit("question", q) }`.
- `api/chat_summary_card.go` (yeni): `guidedSummaryTR` SAF + `withSummaryCard(answer, route,
  bundle)` yalnız `copilot_guided.go:2468` emit noktasında çağrılır.
- İkisi de v2 kapılı (`cosreV2Marked`, `copilot_chat.go:628` deseni).

## 4. Dilim planı (her dilim tek sürüm, tek commit geri alınır)

| D | Ne | Dosyalar | Test | Prod doğrulama | Onay |
|---|---|---|---|---|---|
| D1 (v0.10.2100) | `question` olayı → `data-question`; `v2OnlyEventKinds`+=question; part.go yorumu | `uistream/translator.go`, `part.go`, `api/chat_stop_event.go` | `translator_test.go` satır içi JSON; `chat_sink_test.go` v1 düşürme; yeni altın `v2_question.sse` | yok (ölü yol) | gerekmez |
| D2 (v0.10.2102) | `source` netleştirmesi `data-question` yayar; FE `QuestionCard` (`components/cosre/conversation/`, shadcn Button); `protocol.ts parseDataQuestion`; `toChatTurn` yok sayar; tık → mesaj + `context.clarifyOption`; sunucu birebir eşleme | `api/chat_question.go`, `chat_clarify.go`, `copilot_chat.go` (clarify-reply :487); FE `QuestionCard.tsx`, `protocol.ts`, `MessageList.tsx`, `CosreChatV2.tsx`, `types` | Go table-driven `chat_question_test.go` (3 tür × v1/v2); `chat_clarify_flow_test.go` option yolu; vitest `QuestionCard.test.tsx` + `CosreChatV2.test.tsx` gönderim; `chat_tier_order_test` değişmez | /cosre'de rotasız soru + wiki açık → kart; tıkla → doğru dal; serbest metin hâlâ çalışır | GEREKİR (2026-10-29 kararına dokunur) |
| D3 (v0.10.2103) | `doc_or_entity` + `trace_synth` (evet/hayır) soru kartı | `chat_clarify_entity.go`, `chat_clarify_synth.go`, `chat_question.go` | aynı tablolara satır; `chat_routing_regression_test.go:356-365` korunur | Oracle açıklaması sonrası "evet" tıkla → trace açıklaması | GEREKİR |
| D4 (v0.10.2101) | `guidedSummaryTR` + `data-links {title,summary}`; FE `SummaryLinkCard` (çip ↔ kart seçimi `title` varlığıyla); 3 rota | `api/chat_summary_card.go`, `copilot_guided.go:2468`, `uistream/translator.go:240`; FE `SummaryLinkCard.tsx`, `MessageList.tsx`, `toChatTurn.ts` (alan yok say) | Go table-driven (sayı yok → false, ≤3 link, dedup); translator JSON `omitempty` pini; mevcut `v2_loop.sse` değişmemeli; vitest kart/çip dallanması | "orders-api sağlığı" → kart tek cümle + ≤3 link; link hedefi doğru sayfa | GEREKİR (yeni görünür öğe) |
| D5 | Kapanış: kart kalıcılığı (D4 notu: arşiv mesajında links alanı YOK, yeniden açılan konuşmada kart da çipler gibi görünmez; karar gerekir); evalset v2 fixture'larına question/summary vakası; roadmap-status §2 satır 6/8 güncelle | `ai_conversations_v2parts.go`, `testdata/cosre/`, `docs/cosre/roadmap-status.md` | mevcut testler | konuşma geçmişi yeniden açılınca kart pasif ama görünür | gerekmez |

D2 ve D4 bağımsız; D1→D2→D3 sıralı. Aynı anda tek dilim (kalıcı kural).

## 5. Kapsam DIŞI
- `anchored_ask` / `product_ask` niyetleri (2026-09-19, uygulanmadı; ayrı iş).
- "policy katmanı" / tek merkezi karar motoru — iki saf fonksiyon yeter; motor erken soyutlama.
- Guided `suggestions` çiplerini karta taşıma (S1'e bağlı).
- Modelden özet çıkarma, `**Özet:**` ayrıştırma; AI Elements; v1 yüzeyi; otomatik sayfa geçişi;
  RAG/wiki genişletme; PII maskeleme; yeni ES/CH sorgusu; klasik kanıt toplayıcı ve "Kodu da
  incele" (dokunulmaz); `code-lock` v1 sızıntısı (§2 madde 5, ayrı not).

## 6. Riskler ve doğrulanamayanlar
- Operatör çip şüphesi (2026-10-29): kart "çip" olarak algılanırsa D2/D3 geri alınır; bu
  yüzden soru metni düz cümle kalır, düğme ≤3, serbest metin korunur.
- `Pending` TTL (10 dk) + araya giren tur: kart görünür ama sunucu bekleyen soruyu
  unutmuş olabilir → `clarifyOption` eşleşmez, bugünkü `dropped` yolu; ek metin önerilmez.
- `data-links` alan ekleme (D4'te doğrulandı): `ai_conversations_v2parts.go` yalnız data-tool-result
  ve data-stop'u arşivler; `aiChatMessage`'da links alanı yok — yeniden açılan konuşmada kart,
  bugünkü link çipleri gibi görünmez. D4'te düzeltme yok; D5 notu.
- `guidedSummaryTR` bundle alanları (D4'te doğrulandı): bundle'lar yapısal değil `(evidence,
  sources string, err)` döndürüyordu; D4 kararı (a): üç bundle (+ my_services'in gövdesi
  `guidedTeamServicesBundle`) zaten hesapladığı sayıları dışa aktarılmayan
  `guidedSummaryFacts` olarak da döndürür; kanıt metni bayt bayt aynı.
- `chat_team_scope_need.go` (`needsTeamScope`) içeriği okunmadı; S1 (b) seçilirse okunur.
- `cosre_route_red.go` route-turn ile `trace_synth` etkileşimi okunmadı.
- Hiçbir şey tarayıcı/prod'da denenmedi (netleştirme v0.10.1571 de denenmemiş).

## 7. Bitti tanımı
D1–D5 gemide; `chat_tier_order_test` değişmedi; `v2_loop.sse` baytı değişmedi; v1 altın
dosyaları değişmedi; operatör prod'da üç senaryoyu (kaynak sorusu tık, serbest metin, özet
kartı linki) doğruladı; roadmap-status §2 satır 6 ve 8 "tamam".

## 8. Operatöre sorular

**Onay, 2026-11-13:** operatör belgeyi altı sorunun önerileriyle onayladı: 1 → (a); 2 → soru kartı
kabul (beğenilmezse D2/D3 geri alınır); 3 → deterministik; 4 → üç rota, link tavanı 3; 5 → pasif
görünür; 6 → sıra D1 → D4 → D2 → D3 → D5.

1. Kapsam: (a) yalnız netleştirme soru kartı (D2–D3) + özet/link kartı (D4); (b) + guided
   takım/servis çiplerinin aynı karta taşınması; (c) + `anchored_ask`. Öneri: (a).
2. 2026-10-29 "çip yok" kararı: soru kartı = düz cümle + ≤3 düğme, serbest metin yolu aynen
   kalır, yalnız kapalı cevap kümesinde. Kabul mü, yoksa netleştirme düz metin kalsın ve
   yalnız D4 yapılsın mı? Öneri: kabul; beğenilmezse D2/D3 tek commit geri alınır.
3. Özet cümlesi: sunucuda deterministik (öneri) mi, modelin `**Özet:**` satırı mı?
4. D4 başlangıç rotaları `service_health`, `problems`, `my_services`; link tavanı 3. Uygun mu?
5. Eski mesajlarda soru kartı: pasif-görünür (seçilen işaretli, öneri) mi, gizli mi?
6. D2 ve D4 sırası: önce D4 (davranış değiştirmeyen, görünür kazanç) mı, önce D2 mi?
   Öneri: D1 → D4 → D2 → D3 → D5.
