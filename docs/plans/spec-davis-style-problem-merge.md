# Spec — Davis tarzı Problem birleşmesi: eski dedektörler emekli olmadan tek Problem

Tarih 2026-11-14 · Operatör sorusu: "Eski dedektörleri emekli etmeden, Davis gibi mevcut probleme entegre edebilir miyiz?" · Durum: ONAYLANDI (2026-11-14; açık sorular 1–4 "evet"), D1–D5 GEMİDE (v0.10.2155–2157, 2163–2164); Faz 2 ayrı spec — gemi notları dosya sonunda.

## What

Evet, mümkün; en küçük doğru tasarım v0.10.2130'daki "servis başına TEK açık Problem; önce açan sahip, sonra gelen üye" kuralını (`internal/ml/group_live.go:166-256 planMLProblems`) ML ailesinin dışına taşımaktır. Eski dedektörler aynen çalışır, ama servis için açık bir sahip Problem varsa yeni satır açmaz: **olay** olarak sahibe eklenir (ölçü yalnız yükselir, bildirim yok). Üyelik verisi yeni şema gerektirmez: ML'in bugün yaptığı gibi (`svc_shadow.go:307 markLiveSvc`, evidence'a `problem_id`) üye olaylar `ml_events` satırı olur. Varlıklar arası (topoloji) birleşme Faz 2'ye kalır.

### Bugünkü zemin (doğrulandı)
- Sahiplik yalnız ML içinde: `planMLProblems` servis başına `bySvc` haritası, en eski açık satır sahip (`group_live.go:180-200`); üye = sahibin tam-satır kopyasıyla Refresh, yalnız Value/Threshold/Metric/Description değişir (:246-249). Kapanış: üye kalmazsa 3 tik (`:257-273`). Bildirim yalnız `plan.Open` (:333).
- Eski kural aynı serviste açıksa yalnız NOT düşer (`svc_shadow.go:233 legacyTwinNote`) → prod'da bugün çift satır.
- Ortak "Problem aç" katmanı YOK; her yol kendi `chstore.Problem{}` kurar, `UpsertProblem` (`problem.go:2002`) tam satır, `MarkResolved` (:636) pinli, `OpenProblemsSnapshot.ByKey(ruleID, svc)` ile dedupe. `problems` tablosunda evidence/members kolonu yok (`store.go:2743`); öncelik saklanmaz, okuma anında `computePriority` (`problem.go:449`).
- Üyelik verisi için hazır zemin: `ml_events` (`store.go:3119`: detector, signal, stage, service, started_at, last_seen, baseline, observed, direction, evidence, dup_of) + `GET /api/anomalies/unified?event=` üyeleriyle okuyor; `ListAnomalyFeed` (v0.10.2080) iki olay tablosunu birleştiriyor.
- Kök neden `root_cause_hypotheses.anchor_id = Problem.id` (`rootcause_worker.go:296`); sahip satırının id'si sabit kaldığı için hipotez sahibe bağlı kalır.
- Incident kodu emekli (`incidents_settings.go:28`, `AttachProblemToIncident` silindi) — diriltilmez; birleşme Problem düzeyinde.

### Önerilen model (tek paragraf)
Servis = birleşme anahtarı. İki aile sınıfı: **sahip olabilen** (operatörün inbox'ta öncelik korunan ailesi, keep listesi: `svc-slowdown:*`, `anomaly:*:error_rate`, `anomaly-auto:svc/group/subject:*`) ve **yalnız üye olabilen** (`anomaly:<svc>:p99_ms|request_rate`, tekil `anomaly-auto:<fp>:<svc>` terfisi — operatörün P3'e indirdiği aileler). Kural: servis için açık sahip varsa herkes üyedir; sahip yoksa sahip-olabilen ilk açan sahip olur ve kendi rule_id'siyle açılır (id değişmez → verdict imzaları `p:<ruleId>|<svc>`, bildirim türü, keep listesi, P1 kuralları bozulmaz); yalnız-üye aile sahip yokken bugünkü gibi kendi P3 satırını açar, sonra sahip gelirse o satır "merged into <sahip>" ile kapanır ve üyeye döner (bugün `clustering.go:416 mergeIntoCluster` deseni). Başlık/ölçü = en güçlü sinyal (oran yalnız yükselir, `planMLProblems:246` kuralı aynen), başlangıç = ilk olay.

### Aileler

| Aile | Karar | Gerekçe |
|---|---|---|
| `anomaly-auto:svc/group:*` (ML) | sahip/üye (bugün) | değişmez |
| `svc-slowdown:<svc>` | sahip/üye | servis sağlığı, keep + P1 (oran ≥3) korunur; sahipse id aynı |
| `anomaly:<svc>:error_rate` (+ error_ramp) | sahip/üye | keep listesinde; aynı olay |
| `anomaly:<svc>:p99_ms`, `request_rate` | yalnız üye | operatör P3'e indirdi; sahip yokken bugünkü satır |
| `anomaly-auto:<fp>:<svc>` eski terfi, `anomaly-auto:subject:*` | yalnız üye / sahip-üye | servis olayı; subject keep'te |
| Alarm kuralı (`r.ID`) | AYRI satır | operatör sözleşmesi (ForSec, cooldown, bildirim kanalı); Faz 2'de "ilişkili" çipi |
| `exception-storm` | AYRI | filo düzeyi, Service="" , P1 sabit |
| `anomaly:ext*`, Oracle/ext, `db-health:` | AYRI | özne servis değil (Kind external/db), poller sahipli |
| `self-*` | AYRI | Service="" |
| `anomaly-cluster:<src>` | Faz 1 dokunulmaz; Faz 2 varlıklar arası sahip | aşağıda |

### Yaşam döngüsü
- Açılış: ilk sahip-olabilen olay satırı açar → `SendProblemAlert` bir kez (bugünkü gibi).
- Üye ekleme: `ml_events` satırı (detector = `svc_slowdown` / `metric_anomaly` / `anomaly_promotion`, stage=`live`, observed/baseline/direction, evidence `{problem_id, rule_id, value, threshold}`), sahip Refresh (tam satır kopya + `carryProblemOperatorState`; oran büyükse Value/Threshold/Metric/Severity yükselir). Bildirim YOK.
- Öncelik: yalnız yükselir; P1 yapışkanlığı exception grubunda kalır (`inbox.go:2290`), dokunulmaz. Öncelik P2→P1'e çıkınca tek "yükseldi" bildirimi: Açık soru 1.
- Kapanış: her eski ailenin kendi auto-resolve'u artık **üyeden ayrılma**dır (üye `last_seen` tazelenmez); sahip, canlı üyesi kalmayınca ML'in 3-tik kuralıyla kapanır (`svcResolveReason` genelleşir). `sweepStaleProblems` (`evaluator.go:1132`) sahibi görmez çünkü her üye Refresh `updated_at`'i tazeler; yine de `IsMLLiveProblem` muafiyeti `HasLiveMembers` ile genişletilir.
- "Problem değil": Problem imzası sahibe uygulanır (`p:<sahipRuleId>|<svc>`); üye eklenirken üyenin KENDİ imzası `NoiseVerdictSignatures` ile denetlenir — susturulmuş eski aile üye de olmaz (bugünkü susturma korunur). Ack/atama sahipte; çift satır göçünde genç satırın assignee'si sahip boşsa taşınır.

### Faz 2 — varlıklar arası (Davis'in ikinci yarısı)
Bugün `anomaly-cluster:<src>` (`clustering.go`) komşuları `GetServiceAdjacencyWeighted` ile bulup ≥3 üyede kaynak servisi suçluyor, bireysel satırları metinle "merged" kapatıyor. Birleşik modelde küme = suçlanan servisin sahip Problem'i; komşu servislerin sahipleri evidence `problem_id` ile ona bağlanır, "Kimi etkiliyor" etkilenen servisleri listeler. Önce servis içi, sonra varlıklar arası: servis içi birleşme sıfır topoloji riskiyle bugünkü çift satırı kaldırır; varlıklar arası birleşme yanlış suçlamada etkilenen servisin sayfasını "sorunsuz" gösterir, RCA skoru + topoloji güveni eşiği ister; ayrı spec.

### Geçiş
Veri göçü yok. İlk tikte planlayıcı aynı servisteki açık sahip-olabilen ikizleri görür: en eski sahip, gençler "merged into <sahip>" ile `MarkResolved` + üye olayı yazılır. Snapshot durumundan türediği için idempotent; tam-satır replace `UpsertProblem` ile.

### Emeklilikle ilişki
Birleşme çift satırı kaldırdığı için `spec-ml-service-level.md` madde 5 emekliliği acil olmaktan çıkar; gereksiz kılmaz. Üye olay satırları "hangi dedektör önce yakaladı" ölçümünü bedavaya verir (ML üyesi sahipten X dk sonra mı geldi) — emeklilik kanıtı buradan çıkar. Maliyet sürer: svc-slowdown tik başına `service_summary_5m` (MV, ucuz); metric_anomaly servis × 3 ölçü VM sorgusu; ML kendi VM okumaları — yaklaşık iki kat metrik okuma, prod'da ölçülmedi.

## Files (in shipping order)

Her dilim ayrı v0.10.X, tek commit geri alınır.

- **D1 — sahip çözümü (saf):** `internal/chstore/problem_owner.go` (new file): `OwnerEligibleRule(ruleID)`, `MemberEligibleRule(ruleID)`, `(OpenProblems) ServiceOwner(svc) *Problem` (StartedAt, eşitse ID); `problem_owner_test.go` tablo testi (`TestOwnerDedupe` genişletmesi: svc-slowdown sahip + ML gelir → üye; ML sahip + slowdown gelir → üye; p99 P3 açık + ML gelir → genç kapanır; ext/alarm/self hiç katılmaz). `group_live.go`: `bySvc` artık `ServiceOwner`; `legacyTwinNote` silinir.
- **D2 — eski yazarlar katılır:** `internal/anomaly/problem_member.go` (new file): `JoinOrOpen(ctx, svc, member)`; çağıranlar `evaluator/service_slowdown.go:598 applySvcSlow`, `anomaly/anomaly.go:896 applyOutcome`, `evaluator.go:1370 promoteStrongAnomalies` (grep + ±80). ml_events yazımı için mevcut ML writer yeniden kullanılır; `ListAnomalyFeed` bu detector'ları Faz 1'de gizler (feed davranışı değişmesin).
- **D3 — kapanış + göç:** `HasLiveMembers`, 3-tik kapanış genelleşir, `sweepStaleProblems` muafiyeti, ikiz göçü; `svc_live_test.go` yaşam döngüsü tabloları genişler.
- **D4 — okuyucular:** `internal/api/problem_events.go` (new file) `registerProblemEventRoutes`; `frontend/src/lib/types.ts`, `lib/api.ts`, `features/anomalies/ProblemDetail.tsx` (1347 satır; grep `alertDetailBoxes`), `lib/inboxRowText.ts`, `internal/api/inbox.go` `InboxItem.EventCount`; `internal/mcptools/problem_tools.go:49 problemView` `eventCount`.
- **D5 — yükseliş bildirimi** (Açık soru 1 evetse) + `docs/DECISIONS.md`, `.claude/skills/aiops/SKILL.md` §2 satırı, `docs/anomaly/old-detectors-ml-gap.md` durum sütunu.

## API surface

- `GET /api/problems/{id}/events` → `[{detector, ruleId, startedAt, lastSeen, observed, baseline, direction, live}]`, zaman sırası; `RequireAnyRole` viewer dahil; `serveCached` anahtarı `problem:events:v1:<id>`, 15 sn; `ml_events WHERE service = ? AND started_at >= problem.started_at - 30m AND JSONExtractString(evidence,'problem_id') = ?` + LIMIT + max_execution_time.
- `GET /api/inbox`, `/api/problems/{id}`: `eventCount` alanı (snapshot anında sayım, ek sorgu yok: üye Refresh sırasında Description'a değil `InboxItem`'a sayı, kaynağı aynı ml_events sorgusu 15 sn cache).
- Yazma endpoint'i yok; audit değişmez.

## Schema changes

none — `problems` kolon eklenmez; üyelik `ml_events.evidence.problem_id` (bugünkü ML damgası). Alternatifler reddedildi: (A) `problems.members` JSON kolonu — "yeni kolon iki boot" + dağıtık ALTER, gerek yok (DUR işareti kalkar); (B) Description metni — bugünkü cluster yöntemi, sorgulanamaz. system_settings anahtarı yok, yeni vida yok.

## UX surface

- Problems listesi: tek satır; "Ne bozuk" = sahip satırının en güçlü sinyali (bugünkü `inboxHeadline`); alt satıra "· N olay" (N≥2 ise). Tür sütunu sahibin ailesi.
- Detay: "Ne oluyor" kutusunun altına 1-satırlık olay çizelgesi (≤5: `14:02 p99 yavaşlama (svc-slowdown) 3.1×`, `14:07 ML error_rate 2.4×`), fazlası ve ham değerler kapalı "Teknik kanıt"ta. Spinner yok (15 sn cache), boşsa satır gizli. Viewer görür.
- Menü rozeti `/api/problems/count` değişmez; çift satır kalktığı için sayı düşer.
- CoSRE/MCP: `problemView.eventCount` + açıklama cümlesine "N dedektör aynı olayı gördü".

## Risk

Medium — sahibin rule_id'si "kim önce" ile değişir (Tür sütunu, bildirim türü, team-mail tür kapısı sahibin türü olur); yanlış üyelik eski ailenin bağımsız kapanışını geciktirir (3 tik). Alarm/ext/self dışarıda tutulduğu için operatör kuralları etkilenmez. İki motor maliyeti ölçülmedi.

## Estimate

D1 ~1 saat · D2 ~2 saat · D3 ~1 saat · D4 ~2 saat · D5 ~30 dk. Faz 2 ayrı spec.

## Open questions

1. Öncelik P2→P1'e yükselince tek "yükseldi" bildirimi gitsin mi? Öneri: evet, bir kez; ek üye için asla.
2. Sahip svc-slowdown iken ML üye gelirse bildirim türü ve ekip maili sahibin türünde kalır (anomali türü mail gitmez). Kabul? Öneri: kabul; bildirim zaten yalnız açılışta.
3. Alarm kuralı Problem'leri Faz 1'de ayrı satır kalsın mı? Öneri: evet; Faz 2'de yalnız "ilişkili" çipi, birleşme yok.
4. Prod'daki açık ikizler ilk tikte otomatik "merged into" ile kapansın mı (geçmiş sayımlar etkilenir)? Öneri: evet, elle göç yok.

Spec'i onaylıyor musun? Açık soru varsa o ilkönce.

## Gemi notları

**GEMİDE v0.10.2155 — D1 sahip çözümü.** `internal/chstore/problem_owner.go` (SAF): aile sınıfı `ProblemMergeClass` (+ `OwnerEligibleRule` / `MemberEligibleRule`), `ServiceOwnerOf` / `(OpenProblems).ServiceOwner`, `DecideJoin`, `RaiseOwner`, `EscalatedToP1`, `PlanServiceMerges` / `MergeInto`. `ml/group_live.go` `planMLProblemsWith` artık tüm açık satırları görür: sahip eski aileyse ML kararı üye olur (yeni Problem / bildirim yok, olaya sahibin kimliği damgalanır), ML ikizi "merged into" ile kapanır; `legacyTwinNote` silindi. Spec'ten SAPAN / netleşen kararlar:
- **`anomaly-auto:subject:*` yalnız üye** (tabloda "sahip/üye"). Gerekçe: spec sahip ailesini "inbox'ta önceliği korunan aile (keep listesi)" diye tanımlıyor ama subject listede YOK (`chstore/problem_priority_inbox.go:87-107`, /aiops §4 "bilinçli dışarıda"). Sahip olsaydı satır inbox'ta P3'e zorlanır, üyesi olan yaygın yavaşlama P1'i görünmez kalırdı.
- **Sahiplik etkin keep listesine bağlı** (`OwnerPolicy.Keep`): statik aile + listedeki kalıba uyma. Operatör bir aileyi listeden çıkardıysa o aile sahip olamaz / sahibi yükseltemez (aynı gizlenme riski). Boş liste = birleşme yok.
- **Öncelik yükseltme oranla değil ÖNCELİKLE**: üye sahibi yalnız TEK BAŞINA önceliği sahibinkinden yüksekse değiştirir; şiddet + ölçü birlikte üyeninki olur (sahibin şiddeti × üyenin oranı diye çapraz öncelik uydurulmaz). Sonuç: P1 sahip (ör. svc-slowdown) üyeler yüzünden hiç yeniden yazılmaz. Yalnız-üye aileler (p99 / request_rate / eski terfi / subject) sahibi hiç yükseltmez — operatörün P3'e indirdiği sinyal sahibi P1 yapamaz.
- **Sahibin `StartedAt`'i değişmez** ("başlangıç = ilk olay" satıra yazılmaz): geriye çekmek yaş eskalasyonunu ve 4 sa P1 kuralını tetikler, bildirim üretirdi. İlk olayın saati olay çizelgesinde (D4).
- **Zayıf ML grubu eski sahibe üye olmaz**: yalnız "açardı" kararı (WouldOpen) katılır; ML ailesinin kendi içindeki v0.10.2130 davranışı (bitmemiş her grup satırı yaşatır) aynen.
- **Yabancı sahip yalnız değişince yazılır**: iki süreç (sre + worker) aynı satıra tam-satır yazdığı için ML üyesi sahibi her tik tazelemez; yalnız öncelik yükselince ya da ikizin atananı devralınınca.
- **"Problem değil"**: imzalı satır ne sahip ne üye; ML tikinde kararlar okunamazsa plan yalnız ML ailesini görür (v0.10.2130 davranışı).

**GEMİDE v0.10.2156 — D2 eski yazarlar katılır.** `internal/anomaly/problem_member.go` (`Members`: `LoadMembers` / `OpenRow` / `Open` / `Write`), `internal/chstore/problem_member.go` (üye olayı: `NewMemberEvent`, `MemberEvidenceOf`, `MLEventProblemID`, `LiveMemberEvents`, `ProblemEventsOf`). Bağlanan yazarlar: `applySvcSlow`, `applyOutcome`, `promoteStrongAnomalies` + `promoteSubjectsLive` + `resolveClearedAnomalyPromotions`. Kararlar:
- **`JoinOrOpen` yerine "sanal satır"**: üye olayı dedektörün AÇACAĞI satırı taşır (`evidence.row`) ve dedektör üyeliğini kendi açık satırı gibi görür. Böylece dedektör mantığına dokunulmadı — histerezis, tazeleme, "none" touch'ı, kapanış kararı aynen; yalnız yazım yolu değişti (tazeleme → üye olayının `last_seen`'i, kapanış → `stage=closed` = üyeden ayrılma). Spec'in "üye `last_seen` tazelenmez" kapanışı emniyet yolu olarak duruyor: dedektör durursa üyelik 20 dk'da biter (`MLEventOpen`, pencere 300 sn).
- **Üye olayı kimliği deterministik** (dedektör + servis + kural + sahip kimliği + sahibin başlangıcı): yeniden katılma (okuma gecikmesi, lider değişimi, kör tik) ikinci satır üretmez; sahibin yeni bölümü ayrı satır.
- **Özne terfisi de bağlandı** (spec dosya listesinde yoktu): bağlanmasaydı D3 göçü her tikte satırı kapatır, süpürme yeniden açıp bildirirdi.
- **Görünürlük**: üye dedektörleri `ListMLEvents` varsayılanında, gruplama ve karne okumalarında SQL'de dışlanır (`MLEventsFilter.Members`); Anomalies listesi / birleşik akış / sağlık kontrolü / karne değişmez.
- **Küme tespiti korunur**: üye olan metrik anomalisi, satırı olsaydı aday olacağı aynı 30 dk penceresiyle küme adayıdır; "katılacak" satırı yoktur, üyeliği sahibinde sürer.
- **`anomaly:<svc>:error_rate` sahibinin tazelemesi**: satırın ölçüsü bir üyeden geldiyse (öncelik yükseltmesi) `Value` ezilmez (`ownAnomalyMeasure`: kendi ölçüsü ">" / "<", üyeninki ">=" — ML karşılaştırıcısı testle pinli).
- **Yumuşak hata yönleri**: üye olayları okunamadı → sanal satır yok ama katılma sürer (aynı satıra yazar; mevcut üyeler yeni Problem + bildirim olarak geri açılmaz). "Problem değil" kararları okunamadı → yeni üyelik yok, mevcutlar sürer. Anlık görüntü okunamadı → üyelik bilinmiyor sayılır, düşürülmez.
- **Maliyet**: worker'da tik başına +2 küçük `ml_events` okuması (yaygın yavaşlama, terfi), anomali tikinde +1; "problem değil" kararları yalnız üyelik / katılma adayı varken okunur.

**GEMİDE v0.10.2157 — D3 kapanış + göç.** `internal/evaluator/problem_merge.go` (`mergeServiceTwins` + SAF `planMergeWrites` / `splitLiveOwners`), `anomaly.HasLiveMembers` / `Members.HoldResolve`, `ml.splitHeldResolves`. Kararlar:
- **Kapanış = üyeden ayrılma**: sahibin kendi dedektörü kapatmak istediğinde (yaygın yavaşlama: ClearBuckets; hata oranı anomalisi: çözülme kararı; ML: 3 tik + sinyal bitti) canlı üyesi varsa satır kapanmaz, AYNEN tazelenir (onay / atanan korunur); dedektör kendi ritminde yeniden dener, son üye de bitince bugünkü gerekçesiyle kapatır. Canlı üye okuması yalnız kapanış anında (nokta okuması), tik başına değil. Okuma hatası = kapatma.
- **Bayat süpürme muafiyeti**: bayat listedeki SAHİP olabilen satır, canlı üyesi varsa süpürülmez (`dropLiveOwners`; ML muafiyetinden sonra). Sahibin dedektörü susmuş olsa da Problem üyeleriyle yaşar; üyeler de susunca 20 dk'da süpürülür.
- **İkiz göçü evaluator'da, tikin SON pası** (ML kaydedicisinde değil): ML kapalı kurulumda da çalışsın. Üç durumu karşılar: prod'daki mevcut ikizler (ilk tik), iki sürecin yarışı, sonradan gelen sahip (yalnız-üye satır sahipten önce açılmışsa). ML ikizini ML planı da kapatır (D1); hangisi önce koşarsa.
- **Yazım sırası sahip → üye olayı → kapanış**: sahip yazılamazsa o servisin ikizi KAPANMAZ (yüksek öncelik taşınmadan gizlenmez). Kapanan eski aile satırı üye olayına dönüşür (dedektör kaldığı yerden sürer); ML ikizi için üye olayı yok.
- **Göç bildirim üretmez**: kapanan ikiz açılırken kendi bildirimini göndermişti.
- **D1 düzeltmesi — `RaiseRule`**: sahibi yükseltebilme ölçütü "statik sahip ailesi" değil "kaynak önceliği inbox'ta korunan aile" oldu. Varsayılan listede aynı küme; operatör yalnız-üye bir aileyi (ör. `anomaly:*:p99_ms`) listeye eklediyse onun P1'i de sahibe taşınır (aksi halde göç korunan bir önceliği gizlerdi).
- **İlk tikte gözlem**: worker logunda `[evaluator] PROBLEM MERGED <servis> · <id> (<kural>) → <sahip>` satırları + özet `problem merge: N satır M serviste sahibine katıldı`; sre logunda `[ml] PROBLEM MERGED …`. Kapanan satırın açıklaması ` · merged into <sahip kimliği>` ile biter.

**GEMİDE v0.10.2163 — D4 okuyucular.** `internal/chstore/problem_events.go` (`ProblemEvents`, `EnrichProblemsWithEventCounts`, SAF `countProblemEvents` / `ProblemOwnDetector` / `ProblemMeasureIsOwn`), `internal/api/problem_events.go` (`GET /api/problems/{id}/events`, `problem:events:v1:<id>` 15 sn, viewer dahil), FE `features/anomalies/ProblemEventTimeline.tsx` + SAF `problemEvents.ts`, `inboxRowText.inboxOccurrencesLabel` "N olay", MCP `problemView.eventCount`. Kararlar:
- **Olay = Problem'e bağlı `ml_events` satırı + (eski aile sahibinde) sahibin KENDİ sinyali**: eski sahibin sinyali `ml_events`'te değildir; sayılmasaydı "ML üyesi geldi" 1 olay görünürdü. ML ailesi sahibinde kendi satırı yok (sinyali zaten bağlı ML olaylarıdır). Üyesi olmayan Problem'de sayı 0, çizelge boş (kendi satırı tek başına çizilmez).
- **Çizelge okuması `ListMLEvents(Members)` + Go'da `ProblemEventsOf`** (spec'teki tek SQL yerine): üyelik kuralı (üye `problem_id`, ML canlı svcShadow / groupShadow damgası) tek yerde kalır. Sayım (liste, 15 sn cache) ayrı hafif SQL: kanıt gövdesi taşınmaz, bağ ClickHouse'ta çıkarılır (`mlEventProblemIDExpr`, Go ikiziyle testte pinli), pencere en çok 24 sa — çok eski Problem'in rozeti son 24 sa'i sayar, tam geçmiş detayda.
- **Sahibin ölçüsü bir üyeden geldiyse** (öncelik yükseltmesi) sahibin kendi çizelge satırı oransız: üyenin eşiğiyle uydurma oran yazılmaz.
- **"N olay" yeri**: "Ne bozuk" alt satırındaki oluşum yuvası (`occLine`, exception "N oluşum" ile aynı), N ≥ 2.
- **Tam liste** "Teknik kanıt" içinde "Birleşen olaylar" bölümü (ham gözlenen / olağan); kısa çizelge ile aynı sorgu anahtarı, ikinci istek yok. ML olay satırı `/anomalies/ml/<id>`'ye linkli.
- **MCP cümlesi yalnız ≥ 2 FARKLI dedektörde** ("N dedektör aynı olayı gördü."); aynı dedektörün iki olayı için cümle yok, `eventCount` yine döner.
- **"merged into" eki** satır cümlesinden kırpılır (BE `inboxResolveSuffixes` + FE `LIFECYCLE_SUFFIXES`); `ML_DETECTOR_TR` `lib/mlDetectorLabels.ts`'e taşındı (ML sekmesi Problems parçasına girmesin).

**GEMİDE v0.10.2164 — D5 yükseliş bildirimi** (Açık soru 1 "evet"). `anomaly.EscalationAlert` (tek gövde), bağlantı: `evaluator.members` (yaygın yavaşlama, terfi, özne terfisi), `anomaly.go` metrik anomalisi taraması (`Members.OnEscalate`), ML kaydedicisi (`ownerEscalated`, yabancı sahip yükseltmesi). Kararlar:
- **Hat = yaş eskalasyonununki**: `SendProblemAlert` yeniden ateşlenir (yeni bildirim türü / metni / ayarı yok); P1'e abone kanal da duyar, onaylanmış sahip kanal turuna çıkmaz, bakım penceresi susturur.
- **"Bir kez" = P1'e GEÇİŞ** (`EscalatedToP1`, yazım öncesi / sonrası aynı ayarla): zaten P1 sahibe gelen ek üye, her tazeleme ve atanan taşıma sessiz. Ayrı "bildirildi" durumu saklanmaz: sahip satırı üyeler yüzünden P1'den inmez (D1 kuralı: ölçü yalnız yükselir), dolayısıyla ikinci geçiş yoktur.
- **İkiz göçü bildirmez** (D3 kararı aynen): kapanan ikiz açılırken kendi bildirimini göndermişti.
