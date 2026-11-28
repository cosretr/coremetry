# Arka plan SRE araştırma hattı audit'i (Faz 0, 2026-11-28)

Yalnız belge; kod değişmedi. Okunan ağaç `v0.10.2491`. Kısaltma: `sre/` = `internal/sre/`, `cop/` = `internal/copilot/`, `fe/` = `frontend/src/`. Kapsam yalnız `COREMETRY_MODE=sre` (ve `all`) drain'i; sohbet ajanı `cosre-convergence-audit.md` (`2e704c619`) kapsamında, K1–K18 oradan atıfla anılır. Önce okunanlar: `docs/cosre/investigation-design.md` (§2.2, §4.3), `.claude/skills/aiops/SKILL.md` (satır 35–36, 251, 330), `docs/DECISIONS.md` başlıkları (2026-11-21 v0.10.2412 / 2415 / 2429, 2026-11-28 v0.10.2486). Örnek adlar nötr: checkout, orders, payments, inventory, gece toplu işi `batch-settle`. Token varsayımı denetimlerle aynı: 3 bayt ≈ 1 token. Keşif bu ajan içinde yapıldı (alt ajan açılamaz; `.claude/rules/calisma.md` "Ajan").

## Özet

Hedef metin, araştırmanın modelin araç çağırdığı bir döngü olduğunu varsayıyor. Bu hatta öyle bir döngü yok: drain her P1/P2 problemi için sabit bir okuma planını iki dalgada paralel koşturuyor (tarama, deploy, bağımlılık, olaylar; trace pivotu ve log artışı belirtiye göre), hipotezleri ve bantları saf bir kod skoru (`Score`) hesaplıyor, model yalnız bir kez ve araçsız çağrılıp hazır paketi dört bölümlük düz metne çeviriyor. Yani "araç sonucundan sonra doğrulama çağrısını kod zorlasın" isteğinin karşılığı "planın kod tarafındaki okumaları"dır, tur/çağrı tavanı ve aynı imzayla tekrar sorunu bu hatta yapısal olarak yok. Ölçülen en önemli üç boşluk: (1) hata mesajını okuyan tek adım (`errors`: hatalı operasyon + istisna grubu + mesaj) Context Pack bayrağının arkasında ve varsayılan kapalı, yani hata oranı yüksek bir problemde istisna mesajı bugün varsayılan olarak okunmuyor; (2) mesajın iş kuralı / teknik hata / altyapı diye sınıflandırılması hiçbir yerde yok, hipotez kategorilerinde "iş kuralı" ve "belirsiz" yok, eval de bu vakaları ölçemiyor çünkü `noise` ve `data` kategorili onaylı vakalar "eşlenmedi" diye atlanıyor; (3) drain problems / öncelik / hipotez tablosuna hiç yazmıyor ama kaydı Problem bildirimine giriyor (`notifySummary`, varsayılan açık, operatör onayı 2026-11-21), dolayısıyla kategori ya da neden cümlesi değişikliği canlı bildirim metnini değiştirir. Önerilen yol: önce fikstürlü, LLM'siz bir eval kümesi (dört senaryo, yapısal beklenti), sonra hata ayrıntısı okuması ve mesaj sınıfı gölgede kayda yazılsın, onaylı vakalarla ölçülsün, Problem sayfası ve bildirime ancak operatör onayı ve gölge karşılaştırmasından sonra çıksın. Güven kodda kalsın (dört model kademesi değil).

## Bugünkü durum

### A. On soru

**1. Kuyruk nerede okunuyor.** Üretici (worker) P1/P2 açık servis problemlerini seçer, drain 5 sn'de bir kuyruğu okur ve sıralar. `sre/enqueuer.go:84-116`, `sre/drain.go:224-256`, `:331-361`.
```go
rows, err := dr.d.Store.ListSREInvestigations(ctx, []string{StateQueued, StateRunning}, 0) // drain.go:226
for _, inv := range pickWork(invs, mine, free) { dr.claim(ctx, inv, cfg) }               // :252-254
// rank: manual/verify 0 → P1 1 → P2 2 → diğer 3 → replay 4                               // :338-352
```
Bağlantı `main.go:1725` (üretici) ve `main.go:1883-1890` (`if mode.sre { sreDone = sre.StartDrain(...) }`). Problem sayfası sonucu `GET /api/problems/{id}/investigation` ile okur (`internal/api/sre_status.go:40`, `:175`; `fe/features/anomalies/ProblemDetail.tsx:1356` → `ProblemInvestigationPanel`).

**2. Model ne bağlam alıyor.** Model araştırmanın BAŞINDA değil SONUNDA, tek anlatım çağrısında bağlam alır; araç verilmez. Girdi `BuildContextPack`: problem künyesi, kural + tek metrik değeri ve eşiği, başlangıç/pencere, adım özetleri, sentezleyici, kod hipotezleri, öneriler, bulgular; ≤4 KB. `sre/contextpack.go:115-131`, `sre/narrate.go:103`, `:141-142`.
```go
head := []string{firstNonEmpty(inv.DisplayID, inv.ProblemID), "servis=" + inv.Service} // contextpack.go:115
line += fmt.Sprintf(" · %s=%.4g (eşik %.4g)", p.Metric, p.Value, p.Threshold)          // :129
rd.budget.inc(ctx)                                                                      // narrate.go:141
text, err := rd.narrator.Explain(nctx, system, user)                                    // :142
```
"Normal" değer (baseline) yalnız Context Pack açıkken `baseline` bölümüyle gelir (`sre/contextpack_settings.go:7`, `:42`, varsayılan kapalı). Problem açıklaması (`Description`) girmez.

**3. Araç döngüsü ve tavan.** Model araç döngüsü YOK. Döngünün yerine sabit plan: dalga 1 (sweep 17 sn, deploys 3, deps 8, [errors 8]), dalga 2 (pivot 17, log_spike 15, events 5, [baseline 12, anomalies 5]), tembel node (7). İş bütçesi 90 sn. `sre/plan.go:46-61`, `:225-276`, `sre/orchestrator.go:363-390`, `sre/settings.go:79-84`.
```go
for _, wave := range []int{1, 2} {           // orchestrator.go:363
    ... runWave(ctx, rd, inv.Service, batch, now, apply)
if !reuse(stepNode) && ctx.Err() == nil && (pbNode || needsNodeCheck(evidenceFrom(inv), verify)) { // :388
```
LLM tavanı: araştırma başına ≤1 çağrı, saatte `maxLLMCallsPerHour` 30 (`sre/narrate.go:13-16`, `:70`); tekrarda 0 (`orchestrator.go:406-421`).

**4. İkinci doğrulayıcı çağrı.** Modele bırakılan bir şey yok; model araç çağıramaz. Okumaların hepsi planla koddan gelir; "doğrulama" yalnız operatörün elle başlattığı `verify` koşusunda hipotezin zorladığı adımları yeniden okur. `sre/plan.go:132-160`, `sre/orchestrator.go:335-348`.
```go
if verify != "" { forced = verifyForced(verify) }            // orchestrator.go:335-337
if verify != "" { return !forced[name] && prev.State != StepError } // reuse, :345-347
```
Hata oranı ya da p95'e bağlı koşullu ek okuma yok; pivot ve log artışı belirtiye (`error|down|latency`) bağlı (`plan.go:245-262`).

**5. Boş sonuç.** Kod `Step.NoData` işaretler ve özete "veri yok" yazar; erişim satırı `no_data`, kanıt kapısı `blind_source` eksiği, bildirim "kör noktalar" listesi bundan türer. `sre/readers_errors.go:117-121`, `sre/steps.go:200`, `:260`, `sre/reach.go:38-51`, `sre/coverage.go:120-136`.
```go
if len(ef.Ops) == 0 && len(ef.Groups) == 0 && st.State == StepOK {   // readers_errors.go:117
    st.NoData = true
    st.Summary = "veri yok: pencerede hatalı operasyon ya da istisna grubu yok"
case s.NoData: return ReachNoData                                    // reach.go:40-41
```
FE bunu "kör" listesine koyar (`fe/features/anomalies/investigationQualityView.ts:55-59`). Not: "hata yok" (gerçek negatif) ile "kaynak boş" aynı `no_data` olur.

**6. Hipotez kartı yapısı.** `sre/hypotheses.go:75-108`.
```go
type HypothesisCheck struct { Name string; Outcome int; Weight float64; Reason string; EvidenceRef string }
type Hypothesis struct { Kind, Category, Label string; Score float64; Band string; Prior float64
    Checks []HypothesisCheck; HumanState *HumanState; State string; StateRefs []string; StateReason string }
```
Kayıt `saved_views(page='sre-investigation')` (aiops SKILL:35); kartta en çok 3 (`HypothesesShown`, `:72`). `State` = supported / ruled_out / tested / untested (`sre/hypothesis_state.go:20-25`).

**7. Güven kimin.** Tamamen kod. `Score` = `clamp(prior + Σ ağırlık × sonuç)`, bant yüksek ≥0.70 / orta ≥0.40 / düşük; tablo 8 tür. Model skoru görür, değiştiremez; yalnız "Muhtemel neden" cümlesinde en olası hipotezi adı ve bandıyla anabilir. `sre/hypotheses.go:4-7`, `:242-265`, `cop/prompts.go:2016-2019`.
```go
sum += c.Weight * float64(o)                                  // hypotheses.go:257
h.Score = math.Round(math.Max(0, math.Min(1, sum))*100) / 100 // :260
h.Band = BandOf(h.Score)                                      // :261
// prompt: "AJAN HİPOTEZİ satırı KODLA hesaplanmış skordur ... skoru, bandı ya da sırayı DEĞİŞTİRME, yeni hipotez uydurma"
```
Modelin yazdığı tek şey `Narration.Text` (≤4000 rune, `narrate.go:153`); hipotez kaydına, duruma, kapıya girmez. Ekip kararı (HumanState) skoru değiştirmez, görünüm sırasını, kapıyı ve bildirim satırını değiştirir (`sre/hypothesis_ruling.go:8-15`, DECISIONS v0.10.2429).

**8. Eval.** Admin `POST /api/sre/eval/runs` → yalnız onaylı `incident_cases` (status=confirmed) → `replay-<run>-<problem>` kuyruk işi, çıpa problem başlangıcı, aynı okuyucular, anlatım yok; top-1/top-3 = hipotez türü → vaka kategorisi. `sre/eval.go:3-36`, `:380`, `sre/queue.go:232`, `sre/eval.go:1014`. Notlayıcı: yerel hakem modeli "Kök neden" etiketiyle sonucu karşılaştırır (`sre/replay_grade.go:3-17`, `main.go:1891-1894`).
```go
cases, err := st.ListIncidentCases(ctx, chstore.IncidentCaseQuery{Status: chstore.CaseConfirmed, Source: "problem", // eval.go:380
var evalUnmappedCategories = map[string]string{ "infra": ..., "data": ..., "noise": ..., "other": ... }       // :117-122
```
Tekrar canlı CH/ES okur; fikstürlü (çevrimdışı) bir SRE vaka kümesi yok.

**9. Araç kataloğu.** Katalog yok, model araç görmez. Okuyucular sabit bir alt küme: `ReadServiceSweepCached`, `ReadDeployCheck`, topoloji/değişiklik/blast, exemplar + `RunTracePivotCached`, `RunLogSpike`, `ReadProblemWindowEvents`, `ReadNodeCheck`, Argo, (paketle) hata/istisna, baseline, anomali, (known) Problem grafı / etki / benzer vaka. `sre/orchestrator.go:44-105`, `:106-190`. Salt-okur korkuluk: `sre/guardrails_test.go:62` (`TestSRENoMutatingCalls`), `:173` (`TestSRENeverUpsertsProblemsOrPriority`).

**10. Geçerli kısıtlar.** Oy/triyaj çalışma zamanı ve eval etiketi sürmez, tek istisna ML gölge kuralı (`.claude/rules/backend.md`; DECISIONS 2026-11-28 v0.10.2486: istisna yalnız ML olay oyu + onaylı vaka → `MLScorecard`, CoSRE 👍/👎 hariç). Prod LLM yalnız yerel geçit, tek modele bağlı kod yolu yok (`backend.md`; `cop/copilot.go:745` `localOnlyGate`). Hipotez skoru değişikliği = `hypothesis_*_test.go` tablo satırı + DECISIONS notu (`sre/hypotheses.go:143-144`, aiops SKILL:257, :376). Drain problems / root_cause_hypotheses / öncelik yazmaz (`sre/orchestrator.go:18-20`, aiops SKILL:35). Canlı bildirim metni, prod davranışı operatör kararı (`calisma.md`). Fikstürde gerçek ad yok. Yeni Problem davranışı önce gölge (Problem standardı R1, aiops SKILL:341).

### Ölçümler (fikstür ve koddan; prod yok)

| Ölçü | Değer | Nasıl sayıldı |
|---|---|---|
| Model çağrısı / araştırma | canlı ≤1, tekrar 0 | `narrate.go:141-142` tek `Explain`; `orchestrator.go:406-421` |
| Okuma adımı / araştırma (varsayılan ayar, hata belirtili problem) | ≈10: sweep, deploys, deps, events, pivot, log_spike, graph_origin, impact, similar + 1 Argo | `plan.go:225-266` sabit + koşullu adımlar; `known.go:116-126` üç bayrak nil = açık (`settings.go:63-69`); `orchestrator.go:394` Argo |
| Okuma adımı üst sınırı | ≈14 (+ errors, baseline, anomalies paketle; + node) | `plan.go:269-276`, `:388` |
| Anlatım girdisi boyutu | 112–4.096 B (≈37–1.365 token), tavan 4.096 B | `wc -c sre/testdata/narration/*.narration.txt` (7 altın); `narrate.go:44` |
| Paket kipi tavanı | 2.000 token × 4 B = 8.000 B, varsayılan kapalı | `contextpack_settings.go:44`, `:55` |
| Hipotez türü → kategori | deploy 1, config 1, capacity 2, dependency 2, external 1, code_bug 1; iş kuralı 0, belirsiz 0 | `hypotheses.go:139-197` tablo satırları |
| Hedef sınıflara eşleme | teknik hata ≈ code_bug (1), altyapı ≈ capacity+dependency+external (5), deploy/config ayrı (2), iş kuralı 0, belirsiz 0 | aynı tablo; eşleme bu belgenin önerisi |
| Etiketli kök neden payı (eval) | kuyruğa giren vakaların %100'ü (yalnız onaylı); 10 vaka kategorisinin 4'ü (`infra`, `data`, `noise`, `other`) eşlenmez → "unmapped", puanlanmaz | `eval.go:380`, `:117-122`; `cosrelearn/cases.go:52` |
| Tetik eşikleri (%10 hata, p95 ≥2×) | kodda yok; p99 artışı yalnız trafik kartının 0.20 kontrolü | `plan.go:216-276` plan girdisi yalnız belirti ailesi (`symptomOf`), oran/p95 okunmaz; `hypotheses.go:188` |

### B. Hedefe göre boşluk tablosu

| # | Hedef | Durum | Kanıt |
|---|---|---|---|
| B1 | Yüksek hata / başarısızlık / 4xx-5xx / p95 ≥2× → kod en az bir ayrıntı okuması zorlar (salt-okur) | **kısmen var** | Okumalar zaten kodda ve salt-okur (`guardrails_test.go:62`). Pivot (temsilci trace → pod → JVM → log) ve log artışı belirtiye bağlı (`plan.go:245-262`). İstisna grubu + mesaj okuyan `errors` adımı yalnız paketle (`plan.go:269-276`, oyun kitabı eklemez `playbook.go:162-182`). Eşik tabanlı tetik yok; durum kodu kırılımı yok. |
| B2 | Mesaj sınıfı: iş kuralı / teknik / altyapı / belirsiz + sınıfa göre çıktı kuralı | **yok** | `ErrorGroup.Message` okunuyor ama yalnız pakette metin olarak (`contextpack_sections.go:255-256`); sınıflandıran kod, alan ya da istem kuralı yok. İstem "kanıt yetersizse kanıt yetersiz de" (`prompts.go:2019`) tek yakın kural. |
| B3 | Boş sonuç kodla işaretlenir, kartta "izleme kör noktası" | **büyük ölçüde var** | `NoData` 8 okuyucuda (`readers_errors.go:119`, `steps.go:81/200/260`, `deps.go:268`, `events.go:156`, `readers_anomalies.go:185`, `readers_baseline.go:315`) → `reach` `no_data` → FE "kör" listesi, bildirim `Blind`. Eksik: gerçek negatif ile boş kaynak ayrımı; kör nokta bir bulgu kartı değil, kalite satırı. |
| B4 | Çoklu hipotez; iddia (mekanizma), kanıt zinciri {araç, alan, değer, künye}, 4 güven, 4 kategori, elenenler; doğrulama okuması yoksa en çok orta; zaman çizelgesi | **kısmen var** | 8 hipotez, 3 gösterilir; `Label` sabit tür adı (mekanizma değil); zincir `{Name, Outcome, Weight, Reason, EvidenceRef}`, künye adım düzeyi (`hypotheses.go:75-81`); 3 bant; kategori 6 vaka kategorisi; `ruled_out` var (`hypothesis_state.go:20-25`). Kanıt kapısı tek kaynakta "yetersiz" der ama UI'da gölge, bildirimde uygulanır (`coverage.go:15-19`, `notify_digest.go:8-10`); "doğrulama okuması yoksa orta" kuralı yok. Adımlar okuma kaydıdır (durum, süre, özet, kanıt); `timeline.go` olay çizelgesidir, güven gerekçesi tutmaz. |
| B5 | Tüketim: problem başına çağrı tavanı + zarif kart; tekrar imza; 3 ardışık boş → kör kart; araştırma başına token AI kullanım sayfasında | **kısmen var** | Plan sabit, adım tavanları ve 90 sn bütçe var (`plan.go:46-61`); tekrar imza yapısal olarak yok (memo, `orchestrator.go:15-18`). 3 ardışık boş kuralı yok (kapı `blind_source` ≤3 listeler, `coverage.go:33-34`). LLM çağrısı `ai_calls` yüzey `sre-investigation`, `CallID` kayıtta (`narrate.go:114-120`, `:152-153`); okuma adımı sayısı ve süresi /ai'de yok (yalnız selfobs RED, `drain.go:489`). |
| B6 | Dört eval vakası | **yok** | Fikstürlü SRE vaka kümesi yok; canlı tekrar yalnız onaylı vakalar, `noise`/`data` eşlenmez (`eval.go:117-122`) → iş kuralı ve uyum beklemesi vakaları ölçülemez. Sohbet tarafı K16 aynı üç senaryoyu sohbet için ister. |

### Riskler

1. **Canlı bildirim.** Kayıt `srenotify.Lookup` → bildirim özeti (`internal/srenotify/srenotify.go:15`, `sre/notify_digest.go:126`); `notifySummary` nil = açık (`settings.go:59-62`). Yeni kategori ya da "metrik kirli, altyapı arızası değil" cümlesi bildirime girerse Zoom metni değişir.
2. **Yanlış sınıf, gerçek arızayı gizler.** "not found", "unauthorized", "rate limit", "ORA-" iki tarafa da düşer; hedef metin ORA-'yı teknik sayıp örneği ORA-20012'yi iş kuralı yapıyor. Kesin anahtar kelime sınıfı bir P1'i "iş kuralı" diye susturabilir.
3. **`no_data` karışıklığı.** `errors` adımında "hatalı operasyon yok" gerçek negatiftir; kör nokta sayılırsa kart yanlış alarm verir.
4. **Eval körlüğü.** İş kuralı vakaları bugün "unmapped" atlanıyor; yeni davranış ölçülemeden gönderilirse isabet düşüşü görünmez.
5. **Tek modele kayma.** Sınıflama istemle yapılırsa Qwen3.8 Flash ve gemma4 farklı davranır; kod yolu modele göre dallanamaz.
6. **Ölçek.** Ek `errors` okuması MV'den (`operation_summary_5m` + istisna grupları), 8 sn tavan; ≤20 araştırma/saat (`settings.go:81`) → saatte ≤20 ek okuma. İstisna grubu okumasının prod maliyeti (≈27k span/s) ölçülmedi [V].

## Seçenekler ve önerilen karar

| Seçenek | Ne | Maliyet | Risk |
|---|---|---|---|
| **1 (önerilen) Ölç → gölge → aç** | Önce fikstür eval (LLM'siz, `Score` + kapı + sınıf üzerinde); `errors` adımı paketten ayrılıp kendi ayarıyla; sınıf ve "en çok orta" kuralı kayda gölge alan; Problem sayfası ve bildirim değişmez; onaylı vakalarla ölçüm, sonra operatör onayıyla görünür | 6 dilim | düşük; her adım geri alınabilir |
| 2 Sınıfı anlatım istemine koy | Modele "iş kuralı mı teknik mi" sordur, metinde söylesin | 1 dilim | iki modelde farklı; kayda yapı girmez, eval ölçemez, bildirim metnini dolaylı değiştirir |
| 3 Sohbetteki model döngüsünü drain'e taşı | Hedef metni harfiyen uygula: araçlı döngü, tavanlar, tekrar imza | çok dilim + K1–K11 bağımlılığı | maliyet ve belirsizlik artar; bugünkü deterministik, ölçülebilir hattın yerini alır |

Öneri 1: hedefin istediği "kod zorlar, model uydurmaz" ilkesi bu hatta zaten kurulu; eksik olan okuma (mesaj), sınıf ve ölçüm. Seçenek 3 bu belgede önerilmez.

## Kapsam dışı

- Sohbet ajanı, katalog, K-dilimleri (`cosre-convergence-audit.md`); K14 aynı kuralı sohbet için ister, burada drain eşi tasarlanır.
- Kök neden sentezleyicisi (`root_cause_hypotheses`, ayrı motor; yalnız `SynthRef` olarak okunur).
- Bildirim kanalı biçimi ve takip mesajı (DECISIONS v0.10.2415); yalnız içeriğe etkisi Risk 1'de.
- Prod ölçümü, prod model pencereleri, prod istisna grubu sorgu maliyeti (buradan erişim yok).
- Örnekleme ağırlığının hata oranına etkisi (DECISIONS v0.10.2430; `%10` eşiği şişik orandan tetiklenebilir; Faz 2 tasarımına bağlı).

## Dilim planı

| # | Kapsam | Giriş | Çıkış | Koruma | Risk |
|---|---|---|---|---|---|
| R1 | Fikstürlü SRE eval kümesi: kayıtlı `Facts` + adım durumlarından dört vaka (B6: 12% iş kuralı reddi `orders`; günlük `batch-settle` %100 uyum beklemesi; kalite kapısı düşer ama toplu iş sürer, aşağı akış raporları `inventory`; boş loglar), beklenti yapısal (hangi adım okundu, hangi sınıf, kapı kararı, uydurma neden yok). Bugünkü sonuç taban olarak yazılır. Davranış yok. | S6, S7 | 4 vaka koşar, taban raporu belgede | yeni `sre_fixture_eval_test.go`; ad korkuluğu | düşük |
| R2 | `errors` adımı paketten ayrılır: `sre_agent.errorDetail` off/shadow/on (varsayılan **off**). Tetik: belirti error/down ya da hata ≥ eşik ya da p95 ≥ k×baseline. Gölgede gerçek kayda yazılır, skora / anlatıma / bildirime girmez. | S8, S9 | R1'de vaka 1–3 `errors` okur; altın anlatım bayt bayt aynı | `TestNarrationGoldenByteIdentical`, `TestSRENoMutatingCalls` | düşük |
| R3 | Mesaj sınıfı gölgede: saf `classifyErrors(Facts)` → `{business_rule, technical_fault, infra, unclear}` + gerekçe künyesi; kayıtta ayrı alan; skor/kapı/bildirim görmez. Belirsizde `unclear`, tek neden yazılmaz. | R2, S2 | R1 vaka 1–2 `business_rule`, 4 `unclear`; onaylı vakalarla karışıklık matrisi /ai eval satırında | `TestRulesNeverReachScoring` eşi: sınıf → `Score` girmez | orta |
| R4 | Güven kuralı gölgede: "ayrıntı okunmadıysa bant en çok orta" ve kör nokta ayrımı (`no_data` → `empty_source` / `true_negative`), "3 ardışık boş → kör kart" kayıtta ayrı alan (`BandCap`, `BlindCard`); görünen bant değişmez. | R2, S1 | R1 vaka 4 kör kart; tekrar koşusunda gölge/gerçek fark sayısı | `TestHypothesisMarkNeverChangesScore`, yeni tablo testi | orta |
| R5 | Görünürlük: Problem sayfasında sınıf + kör kart + bant tavanı (yalnız kalite satırında, ayarla), /ai'de araştırma başına okuma sayısı ve süre. Bildirim değişmez. İki modelle anlatım eval (Qwen3.8 Flash, gemma4). | R3, R4, S3 onayı | ekran görüntüsü her sekme; iki model eval satırı | FE testi, `tableUnityRatchet` | orta |
| R6 | Açma: sınıf ve bant tavanı anlatıma ve bildirim özetine (`notifySummary`), ayar `show`; gölge karşılaştırma raporu ekli. | R5 + S3 ve S4 açık onayı | operatörün imzalı kararı; geri dönüş ayarı | bildirim altın testi | yüksek (canlı metin) |

Sıra: R1 → R2 → R3 → R4 → R5 → R6; R3 ve R4 R2'den sonra bağımsız. Her dilim tek sürüm; davranış değiştiren R2–R4 varsayılan kapalı ya da gölge, R6 yalnız onayla.

## Operatör kararları

**S1 — Güven: kod skoru mu, dört model kademesi mi.** Bugün güven tamamen kod (`hypotheses.go:242-265`), model değiştiremez (`prompts.go:2016-2018`), korkuluk `TestHypothesisMarkNeverChangesScore`. Hedef metin model atamalı Kritik/Yüksek/Orta/Spekülatif istiyor; sohbet audit'i K7 öneri (b) üç kademe. (a) üç kod bandı kalır, "ayrıntı okunmadıysa en çok orta" kodla eklenir · (b) dört kademe, KODLA hesaplanır (kritik = yüksek + kapı yeterli + ayrıntı okundu; spekülatif = `untested`) · (c) model atar. **Öneri: (a)**, dört kademe istenirse (b); (c) önerilmez (eval ve bildirim modele bağlanır). Bekleyen: R4.

**S2 — Anahtar kelime ile kesin sınıf.** "not found", "unauthorized", "rate limit", "ORA-" iki yöne düşer (ORA-20012 kullanıcı tanımlı aralıkta, iş kuralı adayı; ORA-00060 teknik). (a) sınıflayıcı yalnız gölgede etiket yazar, onaylı vakalarla ölçülür; liste ayarda veri, kodda sabit değil · (b) doğrudan kesin sınıf. **Öneri: (a)** (K14 ile aynı yön). Bekleyen: R3.

**S3 — Canlı çıktı.** Doğrulandı: drain problems / öncelik / `root_cause_hypotheses` yazmaz (`orchestrator.go:18-20`, `guardrails_test.go:173`); ama kayıt üç yere akar: Problem sayfası paneli, Problem bildirimi özeti (`notifySummary` varsayılan açık), sohbetin kayıtlı araştırma bloğu (`contextpack.go:8-9`). (a) yeni alanlar R2–R4'te yalnız kayıtta, sayfa ve bildirim değişmez · (b) R5'te sayfada görünür, bildirim değişmez · (c) R6'da bildirime de girer. **Öneri: (a) → ölçümden sonra (b); (c) ayrı açık onay.** Bekleyen: R5, R6.

**S4 — Düğmeler ve oylar.** Bugün: ekip kararı (Doğrulandı/Reddedildi) skoru değiştirmez, görünüm sırasını, kanıt kapısını ve bildirim satırını değiştirir (onaylı, DECISIONS v0.10.2429); eval etiketi yalnız onaylı `incident_cases`; 👍/👎 hiçbir şey sürmez; ML gölge istisnası (v0.10.2486) bu hatta uzanmaz. Soru: yeni sınıf ve bant tavanı ekip kararından etkilensin mi? (a) hayır, yalnız kod gerçekleri · (b) "Reddedildi" sınıfı da gizlesin. **Öneri: (a).** Bekleyen: R3, R4.

**S5 — Model ve profil.** Drain tek çağrısı yüzey `sre-investigation`, grup `background` (`cop/profiles.go:798`); profil sırası açık profil > yüzey haritası > grup kardeşi > varsayılan (`cop/profiles.go:323`), yerel geçit kapısı (`cop/copilot.go:745`). Hedefin `qwen3-235b-a22b-fp8` / `qwen3-8b-flash-fp8` + API geçidi adları repoda doğrulanamaz (K9, K12). (a) kod değişmez; operatör yüzeyi Ayarlar'da bir profile eşler; R5'ten itibaren eval iki modelle · (b) model adı koda. **Öneri: (a).** Bekleyen: R5.

**S6 — Fikstür ve beklenti biçimi.** (a) nötr adlar (checkout, orders, payments, inventory, `batch-settle`), yapısal beklenti (okunan adım, sınıf, kapı, uydurma neden yok) · (b) hedef metindeki "iddia X içerir". **Öneri: (a)** (K16 a, mevcut eval ilkesi). Bekleyen: R1.

**S7 — Vaka kategorisi sözlüğü.** `incident_cases` kategorilerinde iş kuralı yok; `noise` ve `data` eşlenmiyor (`eval.go:117-122`). (a) `business_rule` kategorisi eklenir (etiket sözlüğü değişir; eski vakalar yeniden etiketlenmez) · (b) `noise`/`data` iş kuralına eşlenir · (c) değişmez, yalnız fikstür ölçer. **Öneri: (c) R1'de, (a) R3 ölçümünden sonra.** Bekleyen: R3 karışıklık matrisi.

**S8 — Prod ölçek ve bütçe.** Ek okuma saatte ≤20 (`maxPerHour` 20, `maxConcurrent` 2, iş 90 sn). (a) varsayılanlar aynı, R2 gölgede adım süresi p95 selfobs'tan izlenir · (b) P1'de tavan yükseltilir. **Öneri: (a).** Bekleyen: R2.

**S9 — Tetik eşikleri.** Hedef: hata >%10, p95 ≥2× normal. "Normal" yalnız baseline okuyucusundan gelir (paketle). (a) eşikler ayarda (varsayılan %10 / 2×), baseline yoksa belirti error/down ise yine okunur, latency'de `unclear` · (b) eşiksiz, belirti error/down/latency her zaman okur. **Öneri: (b) R2'de**, eşik R3 ölçümünden sonra; örnekleme şişirmesi (v0.10.2430) eşiği güvenilmez kılar. Bekleyen: R2.

## R1 taban raporu (v0.10.2492)

`go test -v -run TestSREFixtureEval ./internal/sre/` — fikstür `internal/sre/testdata/fixture_eval/`, LLM yok, okuyucular donmuş. Hedef 5/20 tuttu.

| Vaka | errors | En olası (kategori, bant) | Kapı | Kör | Sınıf | Hedef |
|---|---|---|---|---|---|---|
| 1 `orders` %12 iş kuralı | not_run | external (external, düşük) | insufficient | errors:not_run | — | errors checked, business_rule, ≤orta |
| 2 `batch-settle` %100 uyum beklemesi | not_run | code_regression (code_bug, düşük) | insufficient | errors:not_run | — | errors checked, business_rule, ≤orta |
| 3 `inventory` kalite kapısı, iş sürer | not_run | code_regression (code_bug, düşük) | insufficient | errors:not_run | — | errors checked, data, ≤orta |
| 4 `payments` boş log | not_run | external (external, düşük) | insufficient | errors:not_run, log_spike:no_data, pivot:no_data | — | errors checked, empty_source ayrımı, unclear |

Düzeltme (uygularken): Özet ve B4 "en olası hipotez" ile kod skorunu kanıt gibi anlatıyor; bu dört vakada hiçbir hipotez `supported` değil, kapı zaten `no_supported` diyor ve en üst satır yalnız öncülden kalan skor. "Bant en çok orta" hedefi (R4) bu vakalarda bugün kendiliğinden tutuyor; R4'ün ölçülebilir etkisi için yüksek bant üreten bir vaka gerekir.
