# CoSRE kalite döngüsü audit'i — sinyal, gece hakemi, evalset koşucusu, deterministik kapı (iş 9)

Tarih 2026-11-13. Audit-first belge; kod değişmedi. Üst belge: `docs/cosre/roadmap-status.md` (Onay karar 5; §2 satır 9; §3 satır 11; §4 Onaylı sıra madde 4; §5 "Evalset kapısı"; §6 satır 9). Önceki parça: `docs/audit/cosre-evaluation-program-2026-09-10.md` (v0.10.646; Faz A gemide, B/C/D kısmen — aşağıda). Kısaltma: `api/` = `internal/api/`, `fe/` = `frontend/src/`, `learn/` = `internal/cosrelearn/`, `uistream/` = `internal/cosre/uistream/`.

Bağlayıcı kararlar: LLM hakem gece işinde KALIR, kapı DEĞİLDİR; kapı deterministik rubrikle başlar; ilk dilim koşucuyu v2 yoluna taşımak. v1'e yeni davranış yok.

Dürüst sonuç: döngünün dört parçası (sinyal, gece hakemi, evalset koşucusu, panel) ayrı ayrı çalışıyor ama birbirine bağlı değil. Koşucu v1 sohbet yolunu ölçüyor, kapı yok (CI'da model yok), sinyalden evalset vakasına giden yol bir "indir" düğmesinden ibaret. Modelsiz CI'da cevap kalitesi ölçülemez; ölçülebilen şey sözleşmedir (akış, kart/link, araç şeması, evalset geçerliliği). Tasarım bu sınırı kabul eder.

## 1. Bugünkü durum (kanıt)

| Parça | Durum | Kanıt |
|---|---|---|
| Sinyal toplama | çalışır | `POST /api/ai/signal` `api/cosre_quality.go:47,84`; rol sarmalayıcı YOK (giriş yapmış herkes), audit yok (:11-13), gövde ≤4 KiB; tür kümesi `learn/signals.go:38-57` (`ClientSignalKinds`: copy, link_click, evidence_click, problem_note, followup_click, evidence_relevant/irrelevant, note; sistem: applied/corrected/briefing_ask); türetilmiş sinyaller `DeriveSignals` :147 (yeniden sorma, iptal, tool hatası, kanıtsız cevap) |
| Sinyal deposu | CH tablosu | `cosre_signals` — `internal/chstore/cosre_quality.go:15,23` (`InsertCosreSignal`); DDL `internal/chstore/store.go:3018` (MergeTree, TTL 180 gün, ORDER BY created_at, kind). `saved_views`/`system_settings` DEĞİL; ağırlıklar `system_settings` (`learn/signal_weights.go`, `api/cosre_signal_weights.go`) |
| Gece işi | çalışır, "gece" değil saatlik | `main.go:1077-1085` `mode.worker` → `cosrelearn.StartQualityJob(ctx, store, lock, time.Hour, judge, ActiveModel)`; kapanmış UTC gün + 2 saat (`learn/job.go:36-47`), ≤3 gün geri yakalama, lider kilidi `cosre-quality-nightly` (:122), koşu tavanı 75 dk. Puanlar `learn/quality.go` (`ScoreAnswers`, `ScoreTools`) |
| LLM hakem | çalışır, kapı değil | `learn/grounding.go`: iddia başına grounded/ungrounded/no_evidence (:3-7), günde ≤40 çağrı (:24), 90 sn zaman aşımı, ≤12 iddia (:68-96), `SummarizeGrounding` :99; prompt `copilot.SystemPromptGroundingJudge()` (:145); model = `copilotSvc.Explain`, Surface `cosre-selfeval` (`main.go:1078-1084`); sonuç `cosre_answer_grounding` (`chstore/purge.go:79`). Self-hosted olduğu kodda ZORLANMIYOR, yorumda yazıyor |
| Vaka üretici | çalışır, sinyalden DEĞİL | `main.go:872` `StartCaseBuilder` 5 dk, kilit `cosre-case-builder` (`learn/cases.go:39-40,346`); kaynak kapanmış problem / çözülmüş exception (`DraftFromProblem` :242, `DraftFromException` :257) → `IncidentCase`; onay `POST /api/cases/{id}/confirm` (`api/cosre_cases.go:44`), `fe/pages/ai/PendingCasesPanel.tsx`. Evalset'le ilgisi yok |
| Evalset | 15 dosya, ~78 vaka | `internal/copilot/evalset/*.json` gömülü (`embed.go:21`); zarf `{schema, cases[]}` (`api/ai_evalset_core.go:233`); `evalCase` :92-140 (ID, Surface, Prompt, Expect, Mode, Deep, BindService, UserTeam…); `evalExpect` :47-89 (MustContain/NotContain, Intent*, ExpectedTools/AnyOfTools/ForbiddenTools, MaxToolCalls, KeyFacts, MaxLatencyMs, MaxAnswerRunes…). Sayım `"id":` grep'i: 78 (intent 24, agent_loop 11, problem 6, …). v2/ui-stream işareti alanı YOK; `mode:"agent"` 11 vaka |
| Koşucu (sunucu) | v1 yolunda | Rotalar `api/ai_evalset_runs.go:64-70` (admin; POST runs `requireCopilot` :67); arka plan `runEvalJob` :558, bellek içi `evalJobBook`, aynı anda tek koşu; kayıt CH `chstore.EvalRun` (`internal/chstore/ai_eval_runs.go`). Ajan: `evalsetServerAgent` `api/ai_evalset_agent.go:409` sentetik `POST /api/copilot/chat` → `s.copilotChat` (:453-458; v1 = `api/chat_sink.go:40`, gövde `copilot_chat.go`), çıktı discard writer (:248); `s.copilot` yoksa koşu yok (:413). v2 bağı yalnız `UserTeam != ""` → `withCosreV2Loop` (:444-446); `cosreV2Chat` (`api/cosre_v2_chat.go:53`) ve `uistream` HİÇ çağrılmıyor |
| Koşucu ölçümleri | var | tool/token istatistiği (OnTurn/OnTool), `scoreEvalAgentCase` :543 (expected/anyOf/forbidden tools, maxToolCalls, tier, keyFacts), `tool_calls_valid` :514, rubrik :507, trail :652, latency |
| Deterministik rubrik | var, LLM'siz | `internal/ai/evalrubric/rubric.go`: `Score(Input) Result` :75, `Threshold=0.8` :31; boyutlar grounded 0.40 (:80), answers_question 0.30 (:89), language_tr 0.10 (:121), length_ok 0.10 (:133), tool_calls_valid 0.10 (:145); n/a boyutlar yeniden normalize (:160). Girdi DÜZ METİN cevap + sayaçlar; kart/link/araç yapısı girdi değil. "KAPI DEĞİL" :20-21. Çağrı: `ai_evalset_core.go:744`, `ai_evalset_agent.go:507`, `ai_evalset_runs.go:199-202`, `cmd/evalsetdiff`. Testler var |
| `make evalset` | CI DIŞI | `Makefile:62-69`: `go test -tags evalset ./internal/api/ -run TestEvalsetReplay`; `evalset_test.go:52` (`//go:build evalset`) `COREMETRY_EVAL_BASE_URL` + `_MODEL` yoksa `t.Skip`; `make evalset-diff` → `cmd/evalsetdiff` (modelsiz, iki artefakt diff) |
| CI | sözleşme testleri var, evalset yok | `.github/workflows/ci.yml:110` `go test ./...`, :159 `-race` (cosre dahil); `-tags evalset` yok; model/COPILOT/OLLAMA env hiçbir workflow'da yok. `scripts/gate.sh` ve `scripts/audit.sh`: evalset/rubrik yok |
| Kalite paneli | küçük, düğme tek | `fe/pages/ai/CosreQualityPanel.tsx` (~120 satır): `api.cosreQuality(7)` :40, `api.cosreQualityEvalset(7)` :62; "En kötü cevaplar" :58, prefix istatistiği :97, "En kötü tool'lar" :102; tek düğme "evalset indir" :61-63 → `GET /api/ai/quality/evalset` (`api/cosre_quality_evalset.go:82`, admin; en kötü cevaplardan `coremetry.evalset/1` dosyası). Değerlendirme (koşu) paneli ayrı: Ayarlar > CoSRE > Değerlendirme `?tab=eval` (DECISIONS.md:757, v0.10.940) |
| v2 fikstürleri | 11 çift | `testdata/cosre/ui-stream-v1{,-error,-closed,-reset,-reset2,-approval,-approval-resume,-stops,-scope,-question,-linkcard}.{events.jsonl,sse}`; Go `uistream/golden_test.go:60,95` (`TestGoldenFixtures`, `-update` :25); TS `fe/lib/cosre/uiStreamFixture.test.ts:16,51`, `fe/components/cosre/conversation/MessageList.test.tsx:331,341`. Ayrı: `testdata/chat_stream/{v1_help,v1_loop,v2_help,v2_loop,v2_question}.sse` (`api/chat_stream_golden_test.go:102`; v2 test fonksiyonu görülmedi — doğrulanmadı) |

Eval programı (2026-09-10) karşısında: Faz A (skor geçmişi + rubrik) GEMİDE; Faz B (pairwise LLM hakem, prompt A/B) YAPILMADI — bugünkü hakem grounding etiketleyici, pairwise değil; Faz C (yansıma döngüsü) YAPILMADI; Faz D kısmen (vaka 78, panel Ayarlar'da; "CI'da LLM'siz alt küme" ve "haftalık replay" YOK).

## 2. Boşluklar

1. Sinyal → aday vaka bağı yok. Sinyal `cosre_signals`'a düşer, gece işi puanlar, panel en kötü 7 günü gösterir, "indir" bir JSON verir; repoya girip girmediği, hangi vakanın hangi sinyalden doğduğu izlenmez. Vaka üretici (problem/exception → IncidentCase) farklı bir döngü; adı karışıyor.
2. Koşucu v1 davranışını ölçüyor (`s.copilotChat`). v2'nin kart/link/soru sözleşmesi, kapsam (team), araç parçaları ölçülmez; v1 emekli olunca koşucu ölür. Rubrik girdisi düz metin → v2 parçaları skorlanamaz.
3. Kapı yok: `make evalset` CI dışı ve modele muhtaç; rubrik "kapı değil" diye işaretli; CI'da yalnız `go test ./...` içindeki sözleşme testleri (evalset geçerliliği, uistream golden) örtük kapı. Hangi adım neyi koruyor, hiçbir yerde adlandırılmamış.
4. Model gereksinimi: sunucu koşusu (`requireCopilot`), replay (`_BASE_URL`+`_MODEL`) ve hakem modele muhtaç; CI'da model YOK ve olmayacak (air-gapped lokal model yalnız prod/lokal kümede). Sonuç: cevap kalitesi ancak prod worker'da ölçülür; CI kapısı sözleşmeyle sınırlı kalmalı.
5. Gece işi "gece" değil (saatlik, gün kapanışından 2 saat sonra); evalset koşusu zamanlanmış DEĞİL, yalnız admin düğmesiyle. Trend yok, gerileme uyarısı yok.
6. `POST /api/ai/signal` rol sarmalayıcısız ve audit'siz — tasarım gereği (viewer de sinyal verir), ama 4 KiB tavan dışında hız sınırı yok (risk, §5).

## 3. Öneri tasarım

### 3.1 Koşucu v2 yoluna (dilim 1)
`evalsetServerAgent` sentetik isteği `cosreV2Chat`'e verir; `uistream` yazıcısının altına bellek içi bir toplayıcı ("part sink") konur: metin parçaları → `Answer` (rubrik girdisi aynen), araç parçaları → tool trail / `tool_calls_valid`, kart/link/soru parçaları → yeni sözleşme kontrolü (3.2). Koşu kaydına `path:"v2"` yazılır (`EvalRun` alanı; CH `ai_eval_runs` kolon eklemesi dağıtık-güvenli: varsayılan `'v1'`). Eski koşular `v1` kalır, `evalset-diff` yolu farklı iki koşuyu karşılaştırmaz (uyarı yazar). v1'e dokunulmaz; `UserTeam` → `withCosreV2Loop` dalı gereksizleşir (v2 yolunda zaten işaretli), kaldırılır. Elenen: iki yolu yan yana koşturan "A/B kipi" — v1 emekli olacak, iki kat model yükü ve iki kat kod.

### 3.2 Deterministik kapı: modelsiz neyi ölçeriz
Modelsiz ölçülebilen üç sözleşme, hepsi bugün `go test ./...` içinde ama adsız:
- Evalset geçerliliği (`evalset_fixture_test.go`): şema, benzersiz ID, ≥N vaka, her `expect` alanı tutarlı.
- Akış sözleşmesi (`uistream/golden_test.go`, FE `uiStreamFixture.test.ts`): 11 fikstür; soru kartı ve link kartı dahil.
- Rubrik birimi (`evalrubric/*_test.go`).
Eklenecek tek deterministik parça: kayıtlı v2 koşu artefaktı üzerinde rubrik + sözleşme kontrolü ("replay-from-artefact"): prod worker koşusunun part kaydı (`events.jsonl`) repoya altın olarak değil, `COREMETRY_EVAL_OUT` artefaktı olarak düşer; `cmd/evalsetdiff` bunu okur, rubrik eşiğini ve kart/link sözleşmesini modelsiz yeniden hesaplar. Böylece "kapı" = sözleşmeler (CI) + son prod koşusunun eşik altı olmaması (yayın öncesi `make evalset-check`, modelsiz, saniyeler). Elenen: model cevaplarını golden dosya yapmak (her prompt değişiminde kırılır, altın güncelleme refleksi kaliteyi gizler); CI'da küçük model çalıştırmak (operatör: CI'da model yok). Cevap kalitesi ölçümü (model gerektiren) yeri: prod worker, günlük zamanlanmış koşu (3.4); kapı değil, trend.

### 3.3 Kapının yeri ve bütçe
`scripts/gate.sh` go kademesine adlı adım `cosre-contracts`: `go test ./internal/cosre/uistream/ ./internal/ai/evalrubric/ ./internal/api/ -run 'TestEvalsetFixtures|TestGoldenFixtures|TestChatStreamGolden'` + `make evalset-check` (artefakt yoksa "atlandı" yazar, kırmızı değil). Bütçe ≤60 sn (bugünkü `go test ./...` zaten kapsıyor; adımın değeri adlandırma ve çıktı özeti). CI: `ci.yml` aynı adım ayrı iş adı olarak (süre aynı, yeni bağımlılık yok). `evalset-check` CI'da artefakt bulamaz → atlanır; yalnız geliştirici makinesinde ve yayın öncesi anlamlı. Elenen: CI'da prod CH'den son koşuyu çekmek (gizli, ağ, flakiness).

### 3.4 Günlük v2 koşusu (model, kapı değil)
`learn/job.go` kalite gününden sonra aynı worker ve lider kilidi altında (`cosre-evalset-daily`) `runEvalJob` çağrısı; vaka kümesi = gömülü evalset + kabul edilmiş adaylar (3.5). Zaman aşımı ≤30 dk, günde 1, `system_settings` anahtarıyla kapatılabilir (varsayılan AÇIK; vida değil, acil fren). Panelde trend + "son koşu eşik altı" rozeti. Elenen: haftalık (gerilemeyi 7 gün geç görür) ve her yayında koşmak (prod yükü, deploy sırası).

### 3.5 Sinyal → aday vaka: sistem önerir, tek düğme
Gece işi en kötü K cevabı (bugün `exportCosreQualityEvalset` mantığı) aday vaka olarak `saved_views(page='cosre_evalset_candidate')`'e yazar (mevcut kalıp; yeni tablo YOK); Değerlendirme panelinde "Aday vakalar" listesi, satır başına tek düğme "Kabul" (admin, audit `evalset.candidate.accept`). Kabul edilen aday günlük koşuya girer (3.4) ve `GET /api/ai/quality/evalset` indirme dosyasına eklenir; repoya gömme operatörün kararı (CI sözleşmesi gömülü kümeyle çalışır). Provenance: `exchange_id` + sinyal türleri. İnsan onayı ZORUNLU (otomatik kabul yok: en kötü cevap ≠ iyi vaka; soru PII içerebilir, maskeleme önerilmez, bu yüzden görüp onaylamak gerekir). "indir" düğmesi kalır. Elenen: adayı doğrudan repoya PR açan otomasyon (air-gapped, git erişimi yok).

## 4. Dilim planı (her dilim tek sürüm, tek commit geri alınır)

| # | Dilim | Dosyalar | Test | Prod doğrulama | Operatör onayı |
|---|---|---|---|---|---|
| D1 | **GEMİDE v0.10.2106** — Koşucu v2 yoluna; `path` alanı; `UserTeam` dalı kalkar | `api/ai_evalset_agent.go`, yeni `api/ai_evalset_v2sink.go`, `internal/chstore/ai_eval_runs.go` (+`path` kolon, varsayılan v1), `api/ai_evalset_runs.go` (kayıt) | `ai_evalset_agent_test.go` v2 sink birimi (fikstür `ui-stream-v1-linkcard` parçalarıyla); mevcut testler yeşil | Ayarlar > Değerlendirme'den bir koşu; `path=v2`, pass sayısı önceki koşuyla karşılaştırılır | Hayır (onaylı sıra madde 4) |
| D2 | Rubrik girdisine akış sözleşmesi: `evalrubric.Input.Parts` → yeni boyut `stream_contract` (v1'de n/a) | `internal/ai/evalrubric/rubric.go`, `rubric_test.go`, `api/ai_evalset_agent.go` (:507) | birim: link kartı eksik `href` → 0; soru kartı >3 seçenek → 0 | D1 koşusu tekrar; rubrik ortalaması değişimi not | Hayır |
| D3 | `make evalset-check` (artefakttan modelsiz) + `gate.sh` `cosre-contracts` adımı + `ci.yml` aynı adım | `Makefile`, `scripts/gate.sh`, `cmd/evalsetdiff/main.go`, `.github/workflows/ci.yml` | `cmd/evalsetdiff` birim: eşik altı artefakt → çıkış 1; artefakt yok → 0 + "atlandı" | CI süresi ölçülür (≤60 sn hedef) | Hayır |
| D4 | Günlük v2 koşusu worker'da (lider kilidi, 30 dk tavan, acil fren anahtarı) | `learn/job.go`, `main.go` (worker dalı), `api/ai_evalset_runs.go` (`runEvalJob` dış çağrı) | `job_test.go` zamanlama; tek koşu kilidi | 2 gün prod: koşu süresi, model yükü (`ai_calls`), `ES istek sayacı` değişmez | EVET (prod modele günlük ek yük) |
| D5 | Panel: trend + "son koşu" + eşik altı rozeti; vida yok | `fe/pages/ai/CosreQualityPanel.tsx` veya Değerlendirme sekmesi; `api/ai_evalset_runs.go` (GET runs zaten var) | vitest render; sayfa `DataTable` kuralı | Tarayıcıda | Hayır |
| D6 | Aday vaka: gece yazım `saved_views`, "Kabul" düğmesi, audit, günlük koşuya katılım | `learn/job.go`, yeni `api/ai_evalset_candidates.go` (`registerXxxRoutes`, `requireCopilot`), `fe/...` liste | Go: kabul → koşu kümesine girer; audit satırı | Prod: ilk hafta adaylar operatörle gözden geçirilir | EVET (saved_views kullanımı + K değeri) |
| D7 | v1 koşucu dalının kaldırılması | `api/ai_evalset_agent.go` | — | — | v1 emekliliğiyle birlikte; bu belgenin dışı |

`api/api.go` hiçbir dilimde büyümez (yeni rota yalnız D6, kendi dosyasında). Hiçbir dilim v1'e davranış eklemez.

## 5. Kapsam DIŞI
- Pairwise LLM hakem, prompt A/B, yansıma döngüsü (eval programı Faz B/C): operatör kararı 5 ile hakem kapı değil; yeni hakem işi önerilmez.
- RAG metrikleri (Recall@5/MRR): RAG kullanılmıyor.
- PII maskeleme adaylarda: önerilmez; insan onayı bunun yerine.
- Vaka üretici (IncidentCase) ve PendingCasesPanel: ayrı döngü, dokunulmaz.
- Hakem self-hosted zorlaması kodda: ayrı küçük iş; buradaki dilimlere bağlı değil (soru 5).

## 6. Riskler ve doğrulanamayanlar
- v2 koşusu v1'den farklı pass sayısı verir (kapsam, araç kümesi); ilk D1 koşusu "taban" sayılır, kırmızı değil.
- D4 prod modele günde ~78+ istek ekler; 40/gün hakem tavanının 2 katı. Ölçülmeden açılmaz (onay).
- `POST /api/ai/signal` hız sınırı yok; kötü niyetli olmasa da bir sekme döngüsü tabloyu şişirebilir (TTL 180 gün). Bu belgede dilim değil; not.
- Doğrulanmadı: `chat_stream_golden_test.go` içinde v2 `.sse` dosyalarını okuyan test (dosyalar var, fonksiyon görülmedi); `mode.worker`'ın `all` kipinde de true olduğu (`parseRunModeValue` okunmadı); hakem profilinin self-hosted olduğu (yorum var, kod zorlamıyor); vaka sayısı 78 (`"id":` grep'i, iç içe alan sapması olabilir); `git log` koşulamadı, dilimlerin sürüm tarihleri koddan okunmadı.
- roadmap-status'ta yanlış/bayat: "evalset 16 vaka" → 15 dosya, ~78 vaka; başlık "en son etiket v0.10.2098" → 2105. Diğer kalite iddiaları (cosre_quality.go:47, signals.go:38-57, main.go:872 ve 1078-1084, ai_evalset_runs.go:65-70, ai_evalset_agent.go:444-458, rubric.go:20-21, Makefile:62-69, grounding.go:21,136, CosreQualityPanel.tsx:34) koddan DOĞRULANDI.

## 7. Bitti tanımı
D1-D3 gemide: koşucu v2, rubrik parçaları görür, gate'te adlı sözleşme adımı, `make evalset-check` modelsiz. D4-D6 operatör onayıyla. Roadmap §2 satır 9 "kısmen" → "gemide (hakem kapı değil, günlük)".

## 8. Operatöre sorular

**Onay, 2026-11-13:** operatör belgeyi altı sorunun önerileriyle onayladı: 1 → `path` kolonu kabul;
2 → günlük v2 koşusu açık, UTC gün kapanışı + 2 sa, acil fren anahtarıyla; 3 → K=5, `saved_views`,
tek "Kabul"; 4 → adaylar CI kapısına girmez, repoya gömme elle; 5 → hakem guard'ı evet, ayrı sürüm;
6 → sinyal ucu kullanıcı başına 60/dk, evet.

1. D1'de koşu kaydına `path` kolonu (`ai_eval_runs`, varsayılan `'v1'`) eklenir; eski koşularla diff kapanır. Kabul mü? Öneri: kabul; alternatif (ayrı tablo) gereksiz.
2. Günlük v2 koşusu (D4) prod self-hosted modele günde ~80 istek ekler, 30 dk tavan. Açılsın mı, hangi saatte? Öneri: kalite gününden hemen sonra (UTC gün kapanışı + 2 sa), acil fren anahtarı varsayılan açık.
3. Aday vakalar `saved_views(page='cosre_evalset_candidate')` kalıbıyla, gecede en kötü K=5, "Kabul" tek düğme (D6). K ve kabul akışı uygun mu? Öneri: K=5, kabul edilen aday repoya gömme olmadan günlük koşuya girer; repo gömme ayrı karar.
4. Kabul edilen adaylar CI sözleşme kapısına girmez (gömülü değil). Böyle kalsın mı? Öneri: kalsın; ayda bir "indir → repoya göm" el işi, otomasyon yok.
5. Hakem profilinin self-hosted olduğunu kod zorlamıyor (yorum var). Kod içeren istek kuralı için küçük bir guard (profil `local` değilse hakem atlanır) eklensin mi? Öneri: evet, ayrı sürüm, bu belgenin dilimleri dışında.
6. `POST /api/ai/signal` için kullanıcı başına dakikada 60 tavanı (bellek içi) eklensin mi? Öneri: evet, viewer davranışı değişmez, yalnız taşma 429.
