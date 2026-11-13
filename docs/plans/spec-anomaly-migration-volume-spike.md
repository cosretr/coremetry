# Spec — "servis hacmi sıçradı" Problem → Anomali (ml_events)

Tarih 2026-11-13 · operatör kararı: `self-volume-spike` Problem değil ANOMALİ; taşınınca açılış bildirimi GÖNDERİLMEZ; koşul geçince kendiliğinden kapanma aynı kalır. Kardeş dört self-health kuralı Problem olarak kalır.

**Durum:** Dilim 1 GEMİDE v0.10.2118 (açık sorular ana oturumca cevaplandı: 1 evet not düşülür, 2 evet rozete girer, 3 bu işte YOK). Dilim 2 sırada.

## What
`internal/evaluator/selfhealth_volume.go` kuralı (24 sa / önceki 24 sa, `cur >= prev*factor`, taban 100k, yeni servis ≠ sıçrama, batch süzgeci) **aynen kalır**; yalnız çıktısı değişir: `UpsertProblem` + `SendProblemAlert` yerine `ml_events` satırı (`detector=svc_volume_spike`). Anomalies sayfası ve Inbox `kind=anomaly` bu tabloyu zaten okuduğu için satır ek iş olmadan "Anomali" olarak görünür. Açık Problem satırları ilk ölçülü tikte mevcut auto-resolve yoluyla kapanır.

## Bugün (kanıt)
- **Anomaliler iki tabloya yazılıyor, tek yoldan okunuyor.** `anomaly_events` (store.go:2615; yazıcılar anomaly/recorder.go:117, evaluator/anomaly_subject_sweep.go:115, elasticml/poller.go:246) ve `ml_events` (store.go:3119; tek yazıcı chstore/ml_events.go:84 ← internal/ml/recorder.go:259,310). Anomalies sayfası `GET /api/anomalies/unified` (anomalies_unified.go:45) ve Inbox `kind=anomaly` (inbox.go:747) ikisini `chstore.ListAnomalyFeed` (anomaly_feed.go:219, v0.10.2082) ile birleştirir; menü rozeti `computeInboxCountFor` (inbox.go:1857) aynı feed'den sayar.
- **Tekilleştirme hedefi (audit §6, hafıza 2026-11-12, DECISIONS v0.10.2056/2067/2080-2082):** tek motor `internal/ml`, tek tablo `ml_events`, Problem yalnız grup kararıyla (`anomaly-auto:group:<id>`). Operatör D0-D4 onaylı; `anomaly_events`'e yazım dilim dilim duruyor, veri DROP edilmiyor. Kuyruktaki "svc-slowdown + metric_anomaly error_rate → ML" işi de aynı hedefe (`ml_events`) yazacak; bu spec onunla hizalı, ikinci taşıma yok.
- **Hacim kuralı bugün:** rule-id `self-volume-spike` (selfhealth_volume.go:45), id `self-volume-spike:<service>` (:231), daima warning (:239), prev/cur/katsayı yalnız açıklama metninde (:254, yapısal alan yok). Dedup `snap.ByID` (selfhealth.go:704), tazeleme StartedAt korur (:735-750), kapanış `wanted` dışı + `covered` → `MarkResolved` (:753-768); ölçülemeyen tik yazmaz. `escalationExempt` (:126) yaş eskalasyonunu keser, bildirimi KESMEZ: açılışta `go e.notifier.SendProblemAlert` (selfhealth.go:726) diğer Problem'ler gibi gider. Ayarlar `system_settings["self_health"]` (`volumeSpikeFactor`, `volumeSpikeMinSpans`; chstore/selfhealth.go:31-73), PUT `/api/settings/self-health` (api.go:7644-7689); Settings sekmesi bilinçli YOK (api.go:7637). Sorgu `service_summary_5m` (chstore/selfhealth_volume.go:71).
- **ML tarafında yaşam döngüsü:** `ml_events`'te status yok; `stage` shadow|promoted|closed|chronic (ml_events.go:24-27). Açık = stage≠closed ve `now-last_seen ≤ 4*window_sec` (anomaly_feed.go:50-59). Kimlik `MLEventID(detector,service,part)` + `MLEpisodeID(base,start)` (:72,:79). Evaluator `internal/ml`'i import etmez (main.go:690); ikisi yalnız CH üzerinden buluşur.
- **ML bildirimi:** yalnız grup Problem'i açılışında (group_live.go:220,254); `DecideGroupPromotion` ≥2 farklı sinyal (group_gate.go:48,129-177) ya da tek-sinyal kolu `singleSignalShadow` (:223, v0.10.2092 CANLI: ≥600 sn, gözlenen/taban ≥2 → P2 critical + bildirim). svc_error_rate/svc_breadth bu kola bilerek girmez (:211-214). **Risk: yeni dedektör dışlanmazsa 4× sıçrama tek-sinyal koluyla Problem+bildirim üretir** — "gönderilmez" garantisi bu dışlamadır.

## Önerilen hedef
- **Tablo:** `ml_events`, yeni şema YOK. **Dedektör:** `svc_volume_spike`, signal `trace` (`MLDetectorSignal` haritasına satır). Stage `shadow` (feed stage süzmez, "Sürüyor"/"Bitti" ön ekiyle listelenir).
- **Alanlar:** Service; Baseline=prev; Observed=cur; Score=cur/prev; Direction `up`; WindowSec=3600 (tarama kadansı; emniyet kapanışı 4 sa); Evidence JSON `{prev,cur,factor,threshold,measure_window_sec:86400,min_spans}`. ID = `MLEpisodeID(MLEventID("svc_volume_spike",svc,""), startedAt)`.
- **Yaşam döngüsü (bugünkü reconcile birebir):** açılış = ilk görüldüğü ölçülü tikte upsert, `startedAt` evaluator içi `volOpen map[service]start`'ta tutulur, boot'ta `ListMLEvents(detector)` ile açık epizotlardan tohumlanır; güncelleme = aynı epizot id, `last_seen/observed/score` tazelenir, `started_at` korunur (RMT whole-row: tüm alanlar taşınır); kapanış = ölçülü tikte servis `wanted` dışına çıkınca aynı satır `stage=closed`, `last_seen=now`; aile kapalıysa (`!SelfHealthOn`) hepsi closed; ölçülemeyen tik yazmaz (aiops kuralı). Dedup epizot id'siyle doğal.
- **Öncelik:** anomalide severity yok; Inbox `active|cleared`. Önceki "daima warning / P3" bilgisi düşer (operatör zaten Problem istemiyor).
- **Evaluator'da kalır, yalnız hedef değişir.** Gerekçe: 24 sa/24 sa gün penceresi ML motorunda yok (`throughput_drop` kova+dwell tabanlı, trace_red.go:211-214), taşımak davranışı değiştirir; operatör davranışı aynı istedi; kural saf ve tablo-testli. Alternatif (bir cümle): `internal/ml`'e dedektör olarak taşımak (recorder/tracker epizot yönetimini bedava alır) tekilleştirme programının "tek motor" dilimi olarak sonra yapılabilir; veri aynı tabloda olduğu için ikinci veri taşıması gerekmez.
- **Bildirim garantisi:** (1) `reconcileSelfHealth`'e hacim kuralı artık `wanted` üretmez → `SendProblemAlert` yolu kapanır; (2) `group_gate.go` üye süzgecine `svc_volume_spike` dışlaması (svc_error_rate/svc_breadth yanına) + saf pin testi "bu dedektör hiçbir kolda Problem açmaz"; (3) ML tarafında başka bildirim yolu yok (group_live tek yol).
- **Geçiş:** `selfVolumeRuleID` `selfHealthRules`'ta ve `covered` işaretlemesi kalır, `wanted` boş → prod'daki açık `self-volume-spike:*` satırları ilk ölçülü tikte `MarkResolved` ile kendiliğinden kapanır (whole-row replace mevcut yolla, operatör alanları `carryProblemOperatorState`). Elle kapatma gerekmez. Başlık/runbook/kategori eşlemeleri (problem_standard.go:831, problem.go:784, problem_category.go:58) geçmiş satırlar okunabilsin diye kalır; şim değil, okuma eşlemesi. Öğrenmeler: bu kural için `anomaly_silences` kaydı yok (anahtar fingerprint, yalnız anomaly_events okuyucularına uygulanıyor — anomaly_extra.go:588, ml tarafında grep sıfır); "yok say" Problem durumu kapanan satırla biter, taşınacak bir şey yok. Batch listesi (`anomaly_sensitivity`) `dropBatchVolumeRows` ile aynen çalışır.

## Files (in shipping order)
Dilim 1 — backend (vX):
- `internal/evaluator/selfhealth_volume.go` (+~70): `volumeEventFor(ServiceVolume, cfg, start) chstore.MLEvent` saf; `volOpen` haritası + boot tohumu; `wanted` yerine `[]MLEvent` döner.
- `internal/evaluator/selfhealth.go` (+~25/−10): hacim dalı `reconcileVolumeEvents(ctx, events, covered)` (open/refresh/close upsert); Problem dalından çıkar; `covered[selfVolumeRuleID]=true` kalır (eski satırların kapanışı).
- `internal/chstore/ml_events.go` (+2): `MLDetectorSignal` → `svc_volume_spike: trace`.
- `internal/ml/group_gate.go` (+~6): üye süzgecinde dedektör dışlaması; `internal/ml/group_gate_volume_pin_test.go` (new file).
- `internal/ml/explain_card.go` (+~3): spec satırı: label "Servis hacmi sıçradı", fact "{svc} servisi önceki 24 saatte {baseline} span, son 24 saatte {observed} span yazdı ({score} kat)", unit span.
- `internal/evaluator/selfhealth_volume_test.go` (değişir): tablo sürümlü yaşam döngüsü (açılış / tazeleme start korunur / koşul geçince closed / ölçülemeyen tik yazmaz / aile kapalı → closed / batch düşer) sahte store ile; `volumeSpikeDecision` testleri dokunulmaz.
- `docs/DECISIONS.md` (+~8), `.claude/skills/aiops/reference/detectors.md:243` (+1).
Dilim 2 — frontend (vX+1):
- `frontend/src/components/coremetry/anomalies/MLTab.tsx:16-26` (+1) `ML_DETECTOR_TR.svc_volume_spike`; `frontend/src/lib/signalKind.test.ts:14-20` (+1) `want` → trace.
- ML detay sayfası (`/anomalies/ml/<id>`): `ExplainCard.Summary` düz cümleyi zaten besler; dedektöre özel dal gerekmez (doğrulanacak).
Geri alma: her dilim tek commit revert. Dilim 1 geri alınırsa kural eski yoldan yeni Problem açar; yazılmış ml_events satırları 4 sa içinde `MLEventOpen` ile kendiliğinden "Bitti" olur, veri silinmez.

## API surface
none (yeni yol yok). `/api/anomalies/unified`, `/api/inbox?kind=anomaly`, `/api/inbox/count` mevcut `ListAnomalyFeed` üzerinden otomatik kapsar. `/api/settings/self-health` aynen kalır (ayar vidası yer değiştirmez; Settings sekmesi bugün de yok).

## Schema changes
none. `ml_events` mevcut kolonlar yeter; yeni CH/ES/VM sorgusu yok (`ReadServiceVolumeWindows` aynen). DUR işareti gerekmedi.

## UX surface
- Problems listesi: kural artık görünmez (Problem değil). Inbox birleşik görünümde `kind=anomaly`, durum active/cleared.
- Anomalies sayfası: satır "Servis hacmi sıçradı · <servis>", cümle yukarıdaki fact; teknik kanıt (evidence JSON) kapalı. Viewer okur.
- CoSRE/MCP: rule-id'yi arayan araç yok (copilot/mcp grep sıfır). `mcptools/service_healthcheck.go:700,722` ml_events okuduğu için servis sağlık kontrolü hacim sıçramasını kendiliğinden görür (olumlu yan etki).
- Raporlar / shift summary / alert_tuning: kurala özel kod yok; Problem sayımlarından düşer.

## Risk
Medium. (a) Tek-sinyal kolu dışlaması unutulursa bildirim gider — pin testi zorunlu. (b) `mlFeedFilter` (anomaly_feed.go:188) varsayılan son 24 sa'yi `started_at`'e göre süzüyorsa günlerce süren epizot listeden düşer — Dilim 1'de doğrulanır, gerekirse `last_seen`. (c) Evaluator ve ML lideri farklı pod'da ml_events'e yazar; id uzayı dedektörle ayrık, `closeStale` yalnız kendi koşularını kapatır — çakışma yok. (d) Eski Problem satırları kapanırken geçmiş ekranında "çözüldü" görünür; bu beklenen.

## Estimate
Dilim 1 ~2 saat · Dilim 2 ~30 dk.

## Open questions (operatöre)
1. Geçiş anında prod'daki açık "servis hacmi sıçradı" Problem satırları ilk ölçülü tikte kendiliğinden "çözüldü" olarak kapanır ve geçmişte kalır; açıklamaya "anomali tarafına taşındı" notu eklensin mi? Öneri: evet, elle kapatma yok.
2. Hacim anomalisi menü rozetine (Inbox sayısı) girer; aktif olduğu sürece sayılsın mı? Öneri: evet, diğer ML anomalileriyle aynı kural, özel durum yok.
3. Kuralın `internal/ml` motoruna taşınması (alternatif B) tekilleştirme programına "sonraki dilim" olarak kuyruğa alınsın mı? Öneri: evet ama bu spec'ten ayrı; şimdi yalnız hedef tablo değişir.

Spec'i onaylıyor musun? Açık soru varsa o ilkönce.
