# CoSRE v2 sohbet arayüzü — kapanış planı (iş 5)

Tarih 2026-11-13. Audit-first belge; kod değişmedi. Üst belge: `docs/cosre/roadmap-status.md`
(onaylı sıra adım 1). Kısaltma: `api/` = `internal/api/`, `fe/` = `frontend/src/`.
Dürüst sonuç: **v2 arayüzünde kodda kapanmamış işlev yok.** Kalan tek kod dilimi bir temizlik
(K1); gerisi operatörün prod doğrulamasıdır.

## 1. Agent-core S1–S6 (`agent-core-ideas.md:216-223`)

| S | Ne | Durum | Kanıt | Bu işe ait mi |
|---|---|---|---|---|
| S1 | Döngü durdurma koşulları | gemide | `api/chat_stop.go:32,49,53`, `chat_stop_test.go`; `data-stop` `api/chat_stop_event.go:36` | backend; kapalı |
| S2 | `chatBudget.StopReason` | gemide (backend) | `api/chat_loop_steps.go:76`, `copilot_chat.go:1630,1665`, `chat_budget_step_test.go`; FE ayrıştırma `fe/lib/cosre/protocol.ts:59,110` | çipte nedenin görünmesi yalnız doğrulama (§6 adım 6) |
| S3 | `prepareStep` zinciri | gemide | `api/chat_prepare_step.go:56-60,158`; çağrı `copilot_chat.go:1231,1279,1677` | backend; kapalı |
| S4 | Araç onayı altyapısı | gemide | `api/chat_approval.go:36`, `uistream/translator.go:84`, `protocol.ts:44,99`, ayar `ai.toolApproval` (`chstore/ai_tool_approval_settings.go:24`) | backend + ayar; kapalı |
| S5 | `add_problem_note` onaylı araç | gemide (UI dahil) | `api/chat_approval_resume.go:44,89,106`; `ApprovalCard.tsx`, `MessageList.tsx:147`, `useCosreChat.ts:113`, `CosreChatV2.tsx:188`, `ApprovalCard.test.tsx` | evet; yalnız prod doğrulaması |
| S6 | Prep'te `enabled=true, tools:["add_problem_note"]` | kodda değil, operatör ayarı | — | evet; operatör adımı (§6 adım 7) |

S1–S5 için yeni kod yok. Agent-core'dan sonraki fikirler bu işe çekilmez (§5).

## 2. P1 / P2 / P3

Tanım `chat-ui-audit.md:286-288` (özet `phase-2b-plan.md:473-478`):
- **P1 Geçiş** = `cosre_v2` varsayılanı AÇIK, `/cosre` + çekmece v2, eski arayüz bir sürüm bayrak
  arkasında. Durum: yapılmadı (`api/cosre_v2_flag.go:19,49-56` anahtar yoksa false).
- **P2 Söküm** = eski bileşenler, ~32 pin, `api.copilotChat`, v1 rotası silinir; `lib/sse.ts`
  kalır (explain akışı); bayrak kalkar. Durum: yapılmadı.
- **P3 Defter** = DECISIONS girdisi + CLAUDE.md "What goes WHERE" (`internal/cosre/`,
  `components/cosre`). "Ledger" adlı kod yok. Durum: yapılmadı.

Karar: **P1, onaylı sıradaki "bayrak varsayılanı açık" ile aynı şeydir → adım 5'in ilk sürümüne
taşınır.** P2 adım 5'in ikinci sürümü, P3 onunla birlikte. Üçü de bu işin kapsamı DEĞİL.
`phase-2b-plan.md:469` riski (v2 arşivi `evidence[]/explain` yazmıyor, v1'de açılan v2
thread'inde kanıt çipi yok) yalnız v1 yaşadıkça anlamlı; P1 ön koşulu olarak adım 5'e gider.

## 3. v2 yüzeyinin bugünkü hâli (yalnız v2 sohbetinin kendisi)

| Konu | Durum | Kanıt |
|---|---|---|
| Akış durumları | var | `MessageList.tsx:45,70`; `role="log"`, `aria-live`, `aria-busy` `CosreChatV2.tsx:411` |
| `data-reset` / `textId` | var, testli | `useCosreChat.ts:97-101`; `useCosreChat.test.tsx:36-60`, `resetReducer.test.ts`, `textIdMap.test.ts`, `uiStreamFixture.test.ts` |
| Durdur | var | stop prop `CosreChatV2.tsx:429`, akarken yeni soru önce durdurur `:171`; `isAbort` → `metadata.partial` `useCosreChat.ts:104-107` |
| Hata + yeniden dene / regenerate | var | `CosreChatV2.tsx:176`; hata satırı `MessageList.tsx:166-171`, balonda `onRetry` `:84` |
| Geçmiş / konuşma listesi | var | `ChatHistoryPanel` (sheet), sayfada `CosrePageLayout` kenarı `CosreChatV2.tsx:296,406,436`; `?chat=` `:265-290`; /ai bağı `/cosre?chat=<id>` (`fe/pages/ai/exchangeView.ts:216`) |
| Ek / yapıştırılan kod | dosya eki yok (tasarım gereği); kod kilidi + Kod hapı v1 composer'ından | `cosrePersist.ts:26`, `CosreChatV2.tsx:429` |
| Kartlar | paylaşılan `ChatBubble` + `ApprovalCard`, `TeamPickCard`, `EndpointPickCard`, adım kartları | `MessageList.tsx:25,144-154`, `fe/components/cosre/evidence/` |
| Klavye | Enter / Shift+Enter / Esc, `@` tamamlama ortak composer'dan | `fe/components/CopilotChat.tsx:731-789` |
| Karşılama | `CosreWelcomeV2` + `BriefingFeed` (kendi poll'u yok) | `CosreChatV2.tsx:121-127` |
| Dar ekran | sheet genişliği `fe/lib/cosre/sheetWidth.ts` (+test); sayfa düzeni koddan doğrulanmadı | §7 |
| SSE / poll | v2 EventSource ya da interval açmıyor; sohbet = POST `/api/cosre/v2/chat` UI Message Stream (`useCosreChat.ts:7,24`, `reconnectToStream` yok `:87`); FAB rozeti RQ, yalnız kapalıyken (`CosreChatV2.tsx:388`). Kural ihlali yok | — |
| Tembel chunk + bütçe | `/cosre` lazy (`fe/pages/CoSRE.tsx:8`), çekmece `CopilotChatGate` dinamik; giriş chunk'ında ai-sdk ve `components/cosre` yasak `frontend/scripts/bundle-budget.mjs:46-55`; ayrı bayt tavanı yok | — |
| TODO / stub | `fe/components/cosre/` içinde yok | — |
| Testler / pinler | FE: kanca, pin, conversation/*, evidence/*, `cosreLookA/B.pin`, `cosrePageLook.pin`, `bareBorder.ratchet`; Go: `cosre_v2_flag_test.go`, `cosre_v2_chat_test.go`, `uistream/golden_test.go`, `chat_approval*_test.go`, `chat_stop*_test.go`, `chat_prepare_step_test.go` | eksik test bulunmadı |

`roadmap-status.md` §5'ten bu işe ait tek kalem: **AI Elements regex'i** (`bundle-budget.mjs:55`)
— onay 2026-11-13 "kurulmaz" → K1. `chat-ui-audit.md:300-306` açıkları: (1) v1 ucuna 413 sınırı
= v1'e yeni davranış, yapılmaz; (2) `theme.ts:39` varsayılan tema uyumsuzluğu v2'ye özgü değil,
kapsam dışı.

## 4. Bayrak

- Sunucu: `system_settings["cosre_v2"] = {"enabled":bool}`, süreç-yerel 30 sn önbellek, env yok
  (`api/cosre_v2_flag.go:40,41,63-102`). GET/PUT `/api/admin/cosre-v2` yalnız admin (`:35-36`);
  PUT audit yazar `settings.cosre_v2.update` (`:146`), pin `cosre_v2_flag_test.go:81-94`.
- Sohbet ucu bayrak kapalıyken 404; istek başında okunur, açık akış kesilmez (`cosre_v2_chat.go:38-46`).
  Rota `registerRoutesExtra` ile, `requireCopilot` sarılı (`:25,28`); `api.go` büyümedi.
- İstemci: `/api/copilot/config` `cosreV2` alanı (`api/copilot_config.go:84,149`;
  `fe/.../useCopilotEnabled.ts:34,61`), seçim `pickChatImpl.ts:12`, `CopilotChatGate.tsx:83`,
  `pages/CoSRE.tsx:34`. Viewer dahil herkes okur; viewer v2'yi admin açınca görür. Değişiklik
  yeni sayfa yüklemesinde görünür (modül önbelleği).
- Ayar arayüzü: Settings → AI, "CoSRE v2 sohbeti (önizleme)" kutusu + "Şimdi yenile"
  (`fe/pages/settings/CosreV2StatusLine.tsx`, `AiTab.tsx:74`).
- **"Bayrak açılabilir" bugün sağlanıyor.** Eksik olan açıldığında prod'da çalıştığının kanıtı.

## 5. Kapsam DIŞI (sıranın sonraki adımlarına)

- Seçenekli soru kartı, policy katmanı, kısa özet, link kartları → adım 2 (ortak audit).
- Giriş özeti ayar satırı, R9 maliyet ölçümü, `fe/lib/api.ts:1421-1423` tüketicisi → adım 3.
- Evalset'i v2 yoluna taşıma, kapı, rubrik, S2 nedeninin kalite paneline girmesi → adım 4.
- P1 varsayılan açık, P2 söküm, P3 defter, ortak bileşenlerin v1 dosyasından ayrılması,
  `phase-2b-plan.md:469` kanıt arşivi paritesi, eski karşılama ucu (`api/cosre_greeting.go:38`) → adım 5.
- v1'e herhangi bir düzeltme (413 sınırı dahil); AI Elements kurulumu; yeni bayt tavanı.

## 6. Bitti tanımı ve dilimler

Bitti: [x] K1 main'de, gate yeşil (v0.10.2099) · [ ] operatör prod'da aşağıdaki 1–9'u geçti · [ ] bu belge ve
`roadmap-status.md` iş 5 satırı "bitti" · [ ] bayrak prod'da açık bırakılabilir ya da kapatılma
nedeni yazılı.

**K1 — AI Elements kalıntı temizliği (tek sürüm).** Dosyalar: `frontend/scripts/bundle-budget.mjs:55`
(regex'ten `ai-elements` düşer, `cosre` kalır), `docs/DECISIONS.md:12054` civarı nota "kurulmaz,
2026-11-13" eki. Test: `bundle:check` yeşil; varsa betiğin regex pini güncellenir. Prod doğrulaması
gerekmez (yalnız derleme kapısı).

**Operatör prod doğrulaması** (Playwright yok; elle, admin + bir viewer hesabıyla):
1. Settings → AI → "CoSRE v2 sohbeti" aç; Admin → Audit'te `settings.cosre_v2.update` satırı görünür.
2. Sayfayı yenile; `/cosre` ve sağ alt çekmece v2 karşılamasını (öneriler + özet kartı) gösterir.
   Viewer hesabı aynı yüzeyi görür, boş ekran yok.
3. Bir servis sorusu sor: adım kartları akarken dolar, cevap tek balonda biter, metin çift yazılmaz
   (`data-reset` yolu).
4. Uzun bir soruda "Durdur": akış hemen kesilir, balon "yarım" kalır; aynı soruyu "Yeniden dene"
   baştan üretir.
5. Ağ kesik / model kapalıyken sor: hata satırı + "Yeniden dene" görünür, sayfa donmaz.
6. Bütçe dolduracak geniş bir soru: durma çipi nedeni (bütçe / tur) yazar.
7. Ayarda `ai.toolApproval` `enabled=true, tools:["add_problem_note"]` (S6); açık bir Problem
   için "not ekle" iste: onay kartı çıkar, Onayla → not Problem'de görünür, Reddet → yazılmaz.
8. Geçmiş: konuşma kenar listesinde görünür; /ai satırındaki bağ `/cosre?chat=<id>` aynı konuşmayı
   açar; sil çalışır.
9. Tarayıcı 400 px genişlik: çekmece ve `/cosre` taşmadan okunur; sekme arka plandayken ağ
   sekmesinde v2 kaynaklı periyodik istek yoktur.
Herhangi biri düşerse: bayrağı kapat (geri dönüş), bulgu operatör hatası olarak yeni sürümle düzelir.

## 7. Riskler ve doğrulanamayanlar

Riskler: 30 sn önbellek çok pod'da açılışı en çok 30 sn kaydırır; açık akış bayrak kapanınca
kesilmez (bilinçli). Viewer'ın v2'yi görmesi bayrağın tüm kullanıcılara açılması demektir.

Doğrulanamayanlar: "Konuşma —" dizgesi `fe/` içinde bulunamadı; /ai'deki alanın v2 karşılığı
yalnız `/cosre?chat=` bağıyla eşlendi. S2 çipinin nedeni gösterdiği, dar ekran düzeni, aria
kapsamı ve v1 ile kart türü tam paritesi koddan doğrulanmadı (adım 6 ve 9 bunları prod'da yakalar).
`fe/components/ai/CosreTeamCard.test.tsx` kaynaksız test iddiası (çıkarım) bakılmadı. `cosre_v2`
prod'da şu an açık mı bilinmiyor.

## 8. Operatöre sorular

1. K1 tek sürümle çıksın mı, yoksa adım 5 söküm sürümüne mi katılsın? Öneri: şimdi, ayrı sürüm.
2. Doğrulama için bayrak prod'da tüm kullanıcılara mı açılsın (viewer dahil görür)? Öneri: evet,
   mesai içinde; sorun olursa kapat.
3. /ai'deki "Konuşma —" alanı hangi sayfa/sütun? Öneri: `/cosre?chat=` bağı kastediliyorsa ek iş yok.
