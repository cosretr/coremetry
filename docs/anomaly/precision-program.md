# Precision programı — Problem ve anomali tespitlerinin doğruluğunu ölçme denetimi

Tarih 2026-11-27 · Taban v0.10.2482 · Kod, şema, ayar ve test değişikliği yok.
Girdi: `docs/anomaly/d1-olcum.md`, `learning-loop-audit.md`, `anomaly-reassessment-audit.md`, `docs/plans/ml-next.md`, `/aiops`.
Yol kısaltmaları: `chstore/` = `internal/chstore/`, `api/` = `internal/api/`, `ml/` = `internal/ml/`, `FE/` = `frontend/src/`.
Her iddianın sonunda etiketi var:
- **[K]** kodda doğrulandı (dosya:satır).
- **[Y]** yerel geliştirme ClickHouse'unda (26.2, `coremetry` veritabanı) salt okuma ile sözdizimi ve sonuç biçimi denetlendi.
- **[V]** varsayım; doğrulanmadı.

Örnek adlar sentetiktir (`orders-api`, `payments-api`).

## Özet

**Bulgu.**
- Bir tespitin doğru olup olmadığını açıkça söyleyen yalnız iki insan kaydı var:
  - olay oyu (`anomaly_verdicts`),
  - Problem'in onaylı kök neden vakası (`incident_cases`, `status='confirmed'`).
- Problem triyaj işareti ("Gerçek problem / Problem değil") imza düzeyindedir. Operatör kararı D4 gereği değerlendirme etiketi değildir.
- Diğer bütün izler dikkat kanıtıdır, doğruluk kanıtı değildir: onay (ack), elle kapatma, bildirimden susturma, CoSRE kartı ilgili/ilgisiz, 👍/👎, detay açma.
- Bugünkü ML karnesi (`/api/ml/scorecard`) 30 günü 5000 satırla ve sırasız `LIMIT` ile okuyor (`chstore/ml_events.go:552-556`) **[K]**. Prod hacminde sayıları kesik olabilir. Etiket sayısı da karnede güven aralığı olmadan gösteriliyor.

**Operatörün koşacağı sorgular** (prod ClickHouse, Coremetry veritabanı, salt okuma):
1. A0 ön kontrol.
2. A1 dedektör başına 30 günlük precision (tek sorgu).
3. A2 etiketsizlerin örtük sinyali.
4. Ek olarak A1b ve A1c.

ML tabloları Distributed değildir. Hangi düğümde koşulacağını `d1-olcum.md` §4 belirler.

**Kararı ne sağlar.** A1'in `wilson_alt_kural` sütunu ve kapsama oranı, Bölüm C tablosundan sonraki iki işin sırasını mekanik olarak seçer: önce dedektör sıkılaştırma mı, önce karne mi, yoksa önce etiket toplama mı.

## Bölüm A — bugünkü durum

### A.1 Etiket kaynaklarının tam listesi

"Kullanılabilir mi" sütunu üç değer alır:
- **D/Y**: doğru ya da yanlış etiketi olarak kullanılabilir.
- **Kural dışı**: karnede gösterilir ama otomatik kurala girmez.
- **Hiçbiri**: doğruluk bilgisi taşımaz.

| # | Kaynak (UI) | Depo: tablo · kolonlar · değerler | Yazan rol · uç | Birim · ömür · RMT | Kullanılabilir mi |
|---|---|---|---|---|---|
| 1 | ML olay detayı ve Sinyaller satırı: "Gerçek anomali / Sorun değil" (`FE/lib/i18n.tr.ts:628-629`; `FE/components/coremetry/anomalies/MLReplaySheet.tsx:367`; liste `FE/lib/queries/anomalies.ts:196-199`) | `anomaly_verdicts`: `event_id='ml:'+olay id`, `verdict` (`anomaly` ya da `not_anomaly`), `reason` (`no_deviation`, `no_impact`, `expected`, `other`; `chstore/anomaly_verdict.go:28-33`), `note`, `created_by`, `created_at` (`chstore/store.go:2661-2675,4512`) | editor+ · `PUT /api/ml/{id}/verdict` (`api/ml.go:46,341-379`) · audit `ml.verdict` (`api/ml.go:378`) | Olay (epizot) başına son karar · TTL 180 gün, ama olayın kendisi 30 gün yaşar (`chstore/store.go:3301`) · ayrı tablo, olay yeniden yazımından etkilenmez | **D/Y** — olay düzeyinde açık karar. `not_anomaly` nedeni ayrıca raporlanır: `no_deviation` dedektör hatasıdır; `no_impact` ve `expected` "eyleme değmez" demektir **[K]** |
| 2 | Eski olay satırı (servis sayfası, eski anomali tablosu) | Aynı tablo, `event_id` = `anomaly_events.id` | editor+ · `PUT /api/anomalies/{id}/verdict` (`api/anomaly_verdicts.go:29,52`); FE `FE/pages/service/Overview.tsx:590` | Olay başına · 180 gün | **D/Y**. Bugün eski olayı yalnız takvim düşüşü ve özneler yazıyor (`anomaly/anomaly.go:949`, `anomaly/subjects_eval.go:135`) **[K]** |
| 3 | "Gerçek mi? Evet/Hayır" (`ml-next.md` C3) | Kodda bu metin artık yok. Yerini 1 numaralı satır aldı | — | — | 1 ile aynı **[K]** |
| 4 | Problems triyajı: "Gerçek problem / Problem değil" (v0.10.1015). Gerekçeler: `expected`, `too_sensitive`, `known_accepted`, `wrong_attribution`, `other` (`chstore/problem_verdict.go:84-93`) | `saved_views`: `page='problem-verdict'`, `id='pv:'+imza`, `owner_id=''`, `name` (`real` ya da `noise`), `query_string` JSON (`chstore/problem_verdict.go:21-29`). İmza biçimi `p:<rule_id>\|<servis>` (`FE/lib/problemVerdict.ts:38-40`) | editor+; kalıcı gizleme yalnız admin · `PUT /api/problem-verdicts` (`api/problem_verdicts.go:40,203`); FE `FE/components/ProblemVerdictActions.tsx:59` | **İmza** başına, yalnız son karar · TTL yok · geçmiş yalnız `audit_log` içinde (`problem.verdict`) | **Kural dışı**. D4 (DECISIONS:11595) ve `api/problem_rootcause_label.go:21-23` bunun etiket olmadığını söyler. Bugün çalışma zamanında iki kullanımı var: politika açıksa bildirim susturma (`notify/verdict_silence.go:109`) ve vaka taslağı (`cosrelearn/cases.go:287`) **[K]** |
| 5 | Problem detayı: "Kök neden" etiketi ve /ai'daki onay bekleyen vakalar | `incident_cases`: `source='problem'`, `source_id=problem.id`, `status='confirmed'`, `category` (`deploy`, `config`, `capacity`, `dependency`, `code_bug`, `infra`, `data`, `external`, `noise`, `other`; `cosrelearn/cases.go:52`), `final_cause`, `confirmed_by`, `confirmed_at` (`chstore/store.go:3436-3470`) | editor+ · `PUT /api/problems/{id}/root-cause-label` (`api/problem_rootcause_label.go:51,259`) · `POST /api/cases/{caseId}/confirm` (`api/cosre_cases.go:44`) | Problem başına · TTL yok · RMT `case_id`, tam satır yazılır | **D/Y**, en güçlü kaynak: `category='noise'` yanlış, diğerleri doğru sayılır **[K]** |
| 6 | Problem'e terfi | Yalnız otomatik: grup kapısı (`ml/group_gate.go:154`) ve servis düzeyi (`ml/svc_shadow.go:121`). Elle terfi ucu yok (grep boş) | Sistem | — | **Hiçbiri** (kapı kararı, insan etiketi değil) **[K]** |
| 7 | Onay (ack) | `problems.status='acknowledged'` (`chstore/problem.go:1970-1973`) · audit `problem.acknowledge`, `details.ids` (`api/api.go:883,8468`) | editor+ | Problem başına | **Hiçbiri** (dikkat göstergesi) **[K]** |
| 8 | Kapatma | Elle toplu kapatma: `MarkResolved`, gerekçe yazılmaz (`chstore/problem.go:676-682`) · audit `problems.bulk_resolve`, `details.problems` (`api/problems_bulk.go:26,70`). Otomatik kapatma: açıklamaya ` · auto-resolved: <neden>` eklenir (`evaluator/evaluator.go:1773`) ya da ` · kendiliğinden kapandı: …` (`ml/group_live.go:98,433`) | editor+ / sistem | Problem başına · `problems` tablosunda TTL yok | **Hiçbiri**. Elle kapatma belirsizdir; otomatik kapatma etiket değildir, yalnız gürültü vekilidir (< 15 dk) **[K]** |
| 9 | Bildirimden "Sustur" | `notification_log`: `channel_kind='ignore'` + ack + audit `notify.ignore` (`api/notify_actions.go:104-116`; `chstore/notification_ignore.go:22-35`) | Jeton sahibi (oturumsuz) | Problem başına · 90 gün | **Hiçbiri**. Rahatsızlık gösterir, yanlış tespit kanıtı değildir **[K]** |
| 10 | Susturma ya da erteleme | Problem için uç yok. `anomaly_silences` yalnız eski olay parmak izini kapsar (`chstore/anomaly_silence.go`, `store.go:2640`). ML olayı için susturma yok | editor+ | — | **Hiçbiri** **[K]** |
| 11 | CoSRE kanıt kartı ilgili/ilgisiz | `cosre_signals.kind`: `evidence_relevant` ya da `evidence_irrelevant` (`api/cosre_quality.go:5-8`; `store.go:3180`) | Her oturum | Cevap başına · 180 gün | **Hiçbiri**. Cevabın kanıtı hakkındadır, tespit hakkında değildir **[K]** |
| 12 | AI cevabı 👍/👎 | `ai_feedback` (`store.go:3110`). Yazma ucu v0.10.1393'te kaldırıldı (`api/ai_routes.go:92-94`) | — | Cevap başına · 90 gün | **Hiçbiri**. 2026-10-17 kuralı **[K]** |
| 13 | AI SRE hipotezi "Ele al / Onayla" | `saved_views` `sre-investigation` HumanState (`api/sre_hypothesis.go:7`) | editor+ | Hipotez başına | **Hiçbiri** (kök neden adayı hakkında) **[K]** |
| 14 | Problem notu, atama, inceleme başlatma | audit `problem.note.create`, `problem.assign`, `problem.investigation.enqueue` | editor+ | Problem başına | **Hiçbiri** (dikkat) **[K]** |

**Etiket kullanımını kısıtlayan kurallar:**
- 2026-10-17 operatör kuralı: "Oylara çok güvenmeyelim". 👍/👎 ve kalite puanları çalışma zamanı kararına girmez. Pin: `cosrelearn/votes_weak_test.go:57` `TestVotesNeverReachRuntimeDecisions`.
- Problem standardı D4: triyaj işaretleri değerlendirme etiketi değildir (`docs/DECISIONS.md:11595`).
- Anomali oyları yalnız karne etiketidir. Tespit, taban, eşik ve bildirim onları okumaz; tek istisna backtest puanlamasıdır. Pin: `ml/vote_runtime_guard_test.go:36` `TestAnomalyVotesDoNotReachRuntime` (v0.10.2269).
- UI metni: "Your label feeds this detector's precision score (ML scorecard) only; it does not change thresholds or decisions." (`FE/lib/i18n.en.ts:600`) **[K]**

**Bölüm B'deki otomatik gölge kuralı bu kuralı kısmen TERSİNE ÇEVİRİR.** Operatörün açık isteğidir. Kural, 1 ve 2 numaralı olay oylarını bir dedektörün Problem ve bildirim üretip üretmeyeceğine bağlar.

Uygulandığında üç şey değişmelidir:
- `TestAnomalyVotesDoNotReachRuntime` tek bir izinli okuyucuyla genişletilir (gölge kararı işlevi).
- UI notu güncellenir: "etiketiniz bu dedektörün otomatik gölgeye düşmesine katkı verebilir".
- DECISIONS'a yeni kayıt girer.

Kurala girebilecek ve giremeyecek kaynaklar:
- **Girer:** 1, 2 ve 5. Bunlar tek bir tespit hakkında açık doğruluk kararıdır ve kişi kayıtlıdır.
- **Girmez:** 4 (D4 operatör kararı; tersine çevirmek ayrı onay ister, bkz. Açık kararlar), 11, 12, 13 (tespit hakkında değil) ve 6–10, 14 ile bütün örtük sinyaller (dikkat kanıtı, doğruluk kanıtı değil). `cosrelearn` pini aynen kalır.

### A.2 Dedektör başına 30 günlük precision — tek sorgu

Birimler ve eşleme:
- **Olay katmanı.**
  - `olay:ml`: ML dedektörü başına olay.
  - `olay:eski`: eski olayın `kind` değeri başına olay.
  - Etiket önceliği: olay oyu > olayın bağlı olduğu Problem'in onaylı vakası > o Problem'in triyaj işareti.
- **Problem katmanı.**
  - `problem`: Döküm aile adlarıyla Problem başına (`chstore/problem_source_breakdown.go:83-110`).
  - Etiket önceliği: vaka > triyaj.

Problem bağı:
- ML olayı: kanıttaki `problem_id`, `svcShadow.problemId` ya da `groupShadow.problemId` (`chstore/problem_events.go:134-138`) **[K]**.
- Eski olay: yalnız `rule_id = 'anomaly-auto:' + olay id`.
- Bağ yok: özne Problem'leri (`anomaly-auto:subject:`), `anomaly-cluster:` ve kural aileleri. Bunlar yalnız Problem katmanında görünür.
- Grup Problem'inin vaka etiketi grubun **her** üye dedektörüne yazılır. Bu çoklu kredidir; tek bir dedektörün payı ayrıştırılamaz.

Sütunlar:
- `*_kural`: yalnız kurala girebilen kaynakları (olay oyu, vaka) sayar.
- `wilson_*`: %95 Wilson alt sınırı.
- `oto_kapanan_15dk`: gürültü vekili.

**[Y]** Yerelde koştu: `ml_events` 3 satır, `problems` 1, `anomaly_verdicts` 0. Sonuç 2 satır döndü, hata yok. Wilson formülü 8/10 → 0,490 ile kontrol edildi. Sayılar bulgu değildir.

**Amaç:** dedektör ve aile başına üretilen, etiketli, doğru, yanlış ve etiketsiz sayıları; kapsama; precision ve Wilson alt sınırı.
**Çıktı sütunları:** `katman, detector, uretilen, etiketli, dogru, yanlis, etiketsiz, kapsama_pct, kaynak_olay, kaynak_vaka, kaynak_triyaj, precision_nokta, wilson_alt, n_kural, dogru_kural, wilson_alt_kural, wilson_ust_kural, oto_kapanan_15dk`.

```sql
WITH
  pl AS (
    SELECT p.id AS problem_id, p.rule_id AS rule_id, toString(p.service) AS service, toString(p.metric) AS metric,
           p.started_at AS started_at, p.resolved_at AS resolved_at, p.description AS description,
           c.case_id != '' AS has_case, toString(c.category) AS case_cat, v.name AS triage
    FROM (SELECT id, rule_id, service, metric, started_at, resolved_at, description
          FROM problems FINAL WHERE started_at >= now64(9) - INTERVAL 37 DAY) AS p
    LEFT JOIN (SELECT source_id, case_id, category FROM incident_cases FINAL
               WHERE source = 'problem' AND status = 'confirmed') AS c ON c.source_id = p.id
    LEFT JOIN (SELECT substring(id, 4) AS sig, name FROM saved_views FINAL
               WHERE page = 'problem-verdict' AND owner_id = '' AND startsWith(id, 'pv:')
                 AND name IN ('real', 'noise')) AS v
           ON v.sig = concat('p:', p.rule_id, '|', toString(p.service))
  ),
  ev AS (
    SELECT substring(event_id, 4) AS eid, toString(verdict) AS verdict FROM anomaly_verdicts FINAL
    WHERE startsWith(event_id, 'ml:') AND created_at >= now64(9) - INTERVAL 30 DAY
  ),
  lv AS (
    SELECT event_id AS eid, toString(verdict) AS verdict FROM anomaly_verdicts FINAL
    WHERE NOT startsWith(event_id, 'ml:') AND created_at >= now64(9) - INTERVAL 30 DAY
  ),
  rows AS (
    SELECT 'olay:ml' AS katman, toString(m.detector) AS detector, e.verdict AS ev_verdict,
           l.has_case AS has_case, l.case_cat AS case_cat, l.triage AS triage, toUInt8(0) AS short_auto
    FROM (SELECT id, detector, multiIf(
            JSONExtractString(evidence, 'problem_id') != '', JSONExtractString(evidence, 'problem_id'),
            JSONExtractBool(evidence, 'svcShadow', 'live') AND JSONExtractString(evidence, 'svcShadow', 'problemId') != '',
              JSONExtractString(evidence, 'svcShadow', 'problemId'),
            JSONExtractBool(evidence, 'groupShadow', 'live'), JSONExtractString(evidence, 'groupShadow', 'problemId'),
            '') AS problem_ref
          FROM ml_events FINAL
          WHERE started_at >= now64(9) - INTERVAL 30 DAY
            AND detector NOT IN ('svc_slowdown', 'metric_anomaly', 'anomaly_promotion')) AS m
    LEFT JOIN ev AS e ON e.eid = m.id
    LEFT JOIN pl AS l ON l.problem_id = m.problem_ref
    UNION ALL
    SELECT 'olay:eski', toString(a.kind), lv2.verdict, l.has_case, l.case_cat, l.triage, toUInt8(0)
    FROM (SELECT id, kind FROM anomaly_events FINAL WHERE started_at >= now64(9) - INTERVAL 30 DAY) AS a
    LEFT JOIN lv AS lv2 ON lv2.eid = a.id
    LEFT JOIN pl AS l ON l.rule_id = concat('anomaly-auto:', a.id)
    UNION ALL
    SELECT 'problem', multiIf(
        startsWith(rule_id, 'incident:'), 'incident',
        startsWith(rule_id, 'anomaly-auto:') AND metric = 'oom_kills', 'ml_oom',
        startsWith(rule_id, 'anomaly-auto:') AND metric IN ('ml_group_lag', 'ml_group_lag_pattern'), 'ml_lag',
        startsWith(rule_id, 'anomaly-auto:group:'), 'ml_group',
        startsWith(rule_id, 'anomaly-auto:svc:'), 'ml_svc',
        startsWith(rule_id, 'anomaly-auto:subject:'), 'ml_subject',
        startsWith(rule_id, 'anomaly-auto:'), 'ml_single',
        startsWith(rule_id, 'anomaly:ext-down:') OR startsWith(rule_id, 'anomaly:ext-cap:')
          OR startsWith(rule_id, 'anomaly-cluster:ext:') OR startsWith(rule_id, 'anomaly:ext:'), 'external',
        startsWith(rule_id, 'anomaly-cluster:'), 'anomaly_cluster',
        startsWith(rule_id, 'anomaly:'), 'anomaly',
        startsWith(rule_id, 'svc-slowdown:'), 'svc_slowdown',
        rule_id = 'exception-storm' OR startsWith(rule_id, 'exception:') OR startsWith(rule_id, 'exception-group:'), 'exception',
        startsWith(rule_id, 'db-health:') OR startsWith(rule_id, 'db-capacity:') OR startsWith(rule_id, 'db-slow-stmt'), 'db',
        startsWith(rule_id, 'runtime:'), 'runtime',
        startsWith(rule_id, 'monitor:'), 'monitor',
        startsWith(rule_id, 'self-'), 'self',
        startsWith(rule_id, 'slo:'), 'slo',
        startsWith(rule_id, 'builtin-'), 'builtin',
        match(rule_id, '^([0-9a-f]{16}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$'), 'user_rule',
        'other'),
      '', has_case, case_cat, triage,
      toUInt8(resolved_at IS NOT NULL
        AND dateDiff('second', started_at, assumeNotNull(resolved_at)) < 900
        AND (position(description, 'auto-resolved:') > 0 OR position(description, 'kendiliğinden kapandı') > 0))
    FROM pl
    WHERE started_at >= now64(9) - INTERVAL 30 DAY
  ),
  lab AS (
    SELECT katman, detector, short_auto,
      multiIf(ev_verdict != '', 'olay', has_case, 'vaka', triage != '', 'triyaj', '') AS src,
      toInt8(multiIf(ev_verdict = 'anomaly', 1, ev_verdict = 'not_anomaly', 0,
                     has_case AND case_cat != 'noise', 1, has_case, 0,
                     triage = 'real', 1, triage = 'noise', 0, -1)) AS ok
    FROM rows
  )
SELECT
  katman, detector,
  count()                                      AS uretilen,
  countIf(ok >= 0)                             AS etiketli,
  countIf(ok = 1)                              AS dogru,
  countIf(ok = 0)                              AS yanlis,
  countIf(ok < 0)                              AS etiketsiz,
  round(100 * etiketli / uretilen, 1)          AS kapsama_pct,
  countIf(src = 'olay')                        AS kaynak_olay,
  countIf(src = 'vaka')                        AS kaynak_vaka,
  countIf(src = 'triyaj')                      AS kaynak_triyaj,
  if(etiketli = 0, NULL, round(dogru / etiketli, 3)) AS precision_nokta,
  if(etiketli = 0, NULL, round((dogru / etiketli + 1.9208 / etiketli
     - 1.96 * sqrt((dogru / etiketli) * (1 - dogru / etiketli) / etiketli + 0.9604 / (etiketli * etiketli)))
     / (1 + 3.8416 / etiketli), 3))           AS wilson_alt,
  countIf(src IN ('olay', 'vaka'))             AS n_kural,
  countIf(src IN ('olay', 'vaka') AND ok = 1)  AS dogru_kural,
  if(n_kural = 0, NULL, round((dogru_kural / n_kural + 1.9208 / n_kural
     - 1.96 * sqrt((dogru_kural / n_kural) * (1 - dogru_kural / n_kural) / n_kural + 0.9604 / (n_kural * n_kural)))
     / (1 + 3.8416 / n_kural), 3))             AS wilson_alt_kural,
  if(n_kural = 0, NULL, round((dogru_kural / n_kural + 1.9208 / n_kural
     + 1.96 * sqrt((dogru_kural / n_kural) * (1 - dogru_kural / n_kural) / n_kural + 0.9604 / (n_kural * n_kural)))
     / (1 + 3.8416 / n_kural), 3))             AS wilson_ust_kural,
  sum(short_auto)                              AS oto_kapanan_15dk
FROM lab
GROUP BY katman, detector
ORDER BY katman, uretilen DESC
LIMIT 500
SETTINGS max_execution_time = 60, join_use_nulls = 0;
```

Okuma notları:
- `FINAL` yalnız RMT tablolarında var (`ml_events`, `anomaly_events`, `anomaly_verdicts`, `problems`, `incident_cases`, `saved_views`). Ham `spans` okunmaz.
- `ml_events` dedektör başına günlük tavanla sınırlıdır (`DailyEventCap`, varsayılan 200; `chstore/ml_settings.go`) **[K]**. Bu yüzden 30 gün en çok 27 dedektör × 6000 ≈ 162 bin satırdır.
- Bir Problem'in birden çok onaylı vakası varsa `incident_cases` birleşimi o Problem'in satırlarını çoğaltır ve sayıları şişirir **[V]**. Beklenen durum Problem başına tek vakadır; `kaynak_vaka` beklenenden büyükse önce `SELECT source_id, count() FROM incident_cases FINAL WHERE source='problem' AND status='confirmed' GROUP BY source_id HAVING count() > 1` ile denetleyin.
- `pl` CTE'si üç kez açılır; `problems FINAL` 37 gün süzgeciyle üç kez taranır. `problems` partition'sız, ORDER BY `id` (`chstore/problem_source_breakdown.go:24-27`). Süre 60 sn'yi aşarsa önce 3. kolu (Problem katmanı) ayrı koşun **[V]**.
- Toplu silinen Problem'ler (`api/problems_bulk.go:27`) ölçümden düşer.
- **Servis kolu Problem'leri eksik sayılır.** Kimlikleri servis başına sabittir (`anomaly-auto:svc:<servis>`, `ml/svc_shadow.go:373-375`) ve `problems` tablosu `id` üzerinde ReplacingMergeTree'dir; aynı servisin Problem'i yeniden açılınca önceki satırın üstüne yazılır **[K]**. Bu yüzden `problem` katmanında `ml_svc`, `ml_oom` ve servis düzeyi `ml_lag` aileleri 30 günde servis başına en çok 1 satır görünür; `uretilen` gerçek açılış sayısının altındadır ve bir vaka etiketi o servisin yalnız son Problem'ini anlatır. Ayrıntı ve çözüm seçenekleri: `detector-roles-audit.md` §1.6 (E2) ve "Açık kararlar" 8. Olay katmanı (`olay:ml`) bundan etkilenmez.
- Triyaj imza düzeyindedir. `anomaly-auto:svc:<servis>` gibi tekrar eden imzada tek karar o servisin bütün Problem'lerine yazılır. Bu sütun kural dışı kalır.

**A1b — ek: etiketleme süresi ve etiketleyen çeşitliliği.**
**Amaç:** "medyan etiketleme süresi" ve tek kişinin baskın olup olmadığı. **Çıktı sütunları:** `detector, etiket, medyan_etiket_saat, etiketleyen_dolu, farkli_etiketleyen`. **[Y]** Yerelde 0 satır döndü, hata yok.

```sql
SELECT toString(m.detector) AS detector, count() AS etiket,
  round(quantileExact(0.5)(dateDiff('second', m.started_at, v.created_at)) / 3600, 1) AS medyan_etiket_saat,
  countIf(v.created_by != '') AS etiketleyen_dolu, uniqExact(v.created_by) AS farkli_etiketleyen
FROM (SELECT id, detector, started_at FROM ml_events FINAL WHERE started_at >= now64(9) - INTERVAL 30 DAY) AS m
INNER JOIN (SELECT substring(event_id, 4) AS eid, created_at, created_by FROM anomaly_verdicts FINAL
            WHERE startsWith(event_id, 'ml:') AND created_at >= now64(9) - INTERVAL 30 DAY) AS v ON v.eid = m.id
GROUP BY detector ORDER BY etiket DESC LIMIT 100
SETTINGS max_execution_time = 30;
```

**A1c — ek: yanlışların tek serviste toplanıp toplanmadığı.**
**Amaç:** tek bir gürültülü servisin dedektörü aşağı çekip çekmediğini görmek. **Çıktı sütunları:** `detector, yanlis, en_cok_servis, o_servis, pay`. **[Y]** Yerelde 0 satır döndü, hata yok.

```sql
SELECT detector, sum(n) AS yanlis, argMax(service, n) AS en_cok_servis, max(n) AS o_servis, round(max(n) / sum(n), 2) AS pay
FROM (SELECT toString(m.detector) AS detector, toString(m.service) AS service, count() AS n
      FROM (SELECT id, detector, service FROM ml_events FINAL WHERE started_at >= now64(9) - INTERVAL 30 DAY) AS m
      INNER JOIN (SELECT substring(event_id, 4) AS eid FROM anomaly_verdicts FINAL
                  WHERE startsWith(event_id, 'ml:') AND verdict = 'not_anomaly'
                    AND created_at >= now64(9) - INTERVAL 30 DAY) AS v ON v.eid = m.id
      GROUP BY detector, service)
GROUP BY detector ORDER BY yanlis DESC LIMIT 100
SETTINGS max_execution_time = 30;
```

### A.3 Etiketsizler için örtük sinyal

**Bugün gerçekten kaydedilenler:**
- **Tarayıcı RUM span'leri.** `service_name='coremetry-frontend'`, fetch span'inde `url.full` öznitelik olarak gelir (`FE/lib/browserOtel.ts:63`; varsayılan açık, `FE/main.tsx:52-68`) **[K]**.
  - Yerelde 2 günde 1828 span vardı.
  - Anahtarlar `http.request.method, url.full, http.response.status_code` **[Y]**.
  - URL'deki kimlik çözülebiliyor: Sinyaller'den açılan 3 ML olayının üçü de `/api/ml/<id>/replay` olarak eşleşti **[Y]**.
  - Detay açılışının izi: ML olayı için `/api/ml/{id}/replay|evidence`, Problem için `/api/problems/{id}/insight|events|…` **[K]** `api/ml.go`, `FE/lib/api.ts:1521-1548`.
- **RUM örneklemesi (dikkat).** Tarayıcı span'leri collector'dan geçmez; aynı kökendeki `/v1/traces` ucuna doğrudan gider (`FE/lib/browserOtel.ts:59-60`) **[K]**. Bu yüzden collector'ın "hatalıların tümü + başarılıların %5'i" örneklemesi onlara uygulanmaz. İki istisna vardır: derlemede `VITE_OTEL_ENDPOINT` collector'a çevrilmişse, ya da prod'da `ingest.sampling.mode` açıksa. Bu durumlarda başarılı istekler eksik sayılır ve "detay açıldı" oranı gerçeğin çok altında çıkar **[V]**. A0'ın `rum_span_24sa` sayısı günlük aktif kullanıcıya göre beklenenden çok küçükse A2'nin "açıldı" sütunlarını alt sınır olarak okuyun.
- **CoSRE'ye sorulması.**
  - `cosre_interactions.problem_id`: sayfa bağlamından gelir (`api/chat_exchange.go:610-611`).
  - RUM'da `POST /api/copilot/explain-problem/{id}` (`api/ai_routes.go:123`) **[K]**.
- **Bildirim:** `notification_log`, `related_kind='problem'`, `ok=1` (`store.go:4134`).
- **Bildirim sonrası eylem:** `audit_log` eylemleri `problem.acknowledge` (`details.ids`), `problems.bulk_resolve` (`details.problems`), `notify.ignore`, `problem.assign`, `problem.note.create`, `problem.rootcause.set`, `problem.investigation.enqueue` **[K]**.

**Kaydedilmeyenler ve en küçük ölçüm eki:**

| Eksik | Neden | En küçük ek |
|---|---|---|
| CoSRE'nin bir **ML olayı** hakkında sorulduğu | `cosre_interactions`'ta olay kimliği yok; yalnız `service`, `page` ve `problem_id` var (`chstore/cosre_interactions.go:30-50`) **[K]** | Sayfa bağlamına olay kimliği eklenir: `cosre_interactions.anomaly_id String DEFAULT ''` (append-only MergeTree, ADD COLUMN) |
| Bildirimdeki bağlantıya tıklanması | Bağlantılarda kaynak işareti yok (`internal/notify` içinde `src=`/`utm` grep'i boş) **[K]** | Bağlantıya `?src=notify` eklenir; RUM `documentLoad` `url.full` onu taşır |
| Detayı kimin açtığı | RUM span'inde kullanıcı özniteliği yok **[Y]** | Gerek yok; dikkat ölçüsü kişi istemez |
| Sunucu tarafı istek izi | `coremetry-api` span'i isteğe bağlıdır ve %10 örneklenir (`internal/selfobs/selfobs.go:13-16,35-41`). Span adında kimlik `:id`'ye indirgenir (`api/http_root.go:19-29`). `url.path` özniteliği yerelde doğrulanamadı (0 span) **[V]** | RUM yeterli; prod'da RUM yoksa A0 bunu gösterir |

**Örtük sinyal doğruluk kanıtı değildir, dikkat kanıtıdır.** Açılmış bir detay yanlış alarmı da gösterebilir. Bu yüzden Bölüm B'deki kurala girmez. Kullanım alanı yalnız ikidir: etiket kuyruğunun önceliği ve "kimsenin bakmadığı" hacim.

**A0 — ön kontrol.**
**Amaç:** RUM ve tabloların prod'da dolu olup olmadığı. **Çıktı sütunları:** `rum_span_24sa, api_span_24sa, ml_events_30g_ham, verdict_satir, problems_30g_ham, onayli_vaka, triyaj_imza`. **[Y]** Yerelde 136, 0, 3, 0, 1, 0, 0 döndü.

```sql
SELECT
  (SELECT count() FROM spans WHERE service_name = 'coremetry-frontend' AND time >= now64(9) - INTERVAL 1 DAY) AS rum_span_24sa,
  (SELECT count() FROM spans WHERE service_name LIKE 'coremetry-%' AND service_name != 'coremetry-frontend'
     AND time >= now64(9) - INTERVAL 1 DAY) AS api_span_24sa,
  (SELECT count() FROM ml_events WHERE started_at >= now64(9) - INTERVAL 30 DAY) AS ml_events_30g_ham,
  (SELECT count() FROM anomaly_verdicts) AS verdict_satir,
  (SELECT count() FROM problems WHERE started_at >= now64(9) - INTERVAL 30 DAY) AS problems_30g_ham,
  (SELECT count() FROM incident_cases WHERE status = 'confirmed') AS onayli_vaka,
  (SELECT count() FROM saved_views WHERE page = 'problem-verdict') AS triyaj_imza
SETTINGS max_execution_time = 30;
```

**A2 — etiketsiz ML olaylarının örtük sinyali.**
**Amaç:** etiketsiz olaylardan hangilerinin açıldığı, CoSRE'ye sorulduğu, bildirildiği ve bildirimden sonraki 24 saatte bir eylem gördüğü.
**Çıktı sütunları:** `detector, etiketsiz, problem_bagli, olay_detayi_acildi, problem_detayi_acildi, cosre_soruldu, bildirildi, bildirim_sonrasi_24sa_aksiyon, hic_ilgi_yok`.
**[Y]** Yerelde 1 satır döndü (`svc_volume_spike`, 3 etiketsiz, 3 açılmış).
`spans` okuması `service_name` + `time` ile ORDER BY öneğinde kalır ve iç `LIMIT 5M` taşır.
**Maliyet uyarısı:** `rum` CTE'si sorguda beş kez geçer ve ClickHouse her seferinde yeniden hesaplar; yani `coremetry-frontend` span'lerinin 30 günü beş kez taranır **[V]**. Önce pencereyi 7 güne indirip (`INTERVAL 30 DAY` → `7 DAY`, altı yerde) süreye bakın; yoğun saat dışında koşun. 120 sn'de bitmezse yalnız `olay_detayi_acildi` sütununu bırakıp diğer `rum` kullanımlarını çıkarın.

```sql
WITH
  rum AS (
    SELECT
      decodeURLComponent(extract(u, '^/api/ml/([^/]+)/(?:replay|evidence)')) AS ml_id,
      decodeURLComponent(extract(u, '^/api/problems/([^/]+)/(?:insight|affected|events|graph|breakdown|source-event|root-cause-label|rootcause|notes|investigation|log-templates|notifications|conversations)')) AS p_id,
      decodeURLComponent(extract(u, '^/api/copilot/explain-problem/([^/]+)')) AS ex_id
    FROM (SELECT path(attr_values[indexOf(attr_keys, 'url.full')]) AS u FROM spans
          WHERE service_name = 'coremetry-frontend' AND time >= now64(9) - INTERVAL 30 DAY AND name IN ('GET', 'POST')
            AND (position(attr_values[indexOf(attr_keys, 'url.full')], '/api/ml/') > 0
              OR position(attr_values[indexOf(attr_keys, 'url.full')], '/api/problems/') > 0
              OR position(attr_values[indexOf(attr_keys, 'url.full')], '/api/copilot/explain-problem/') > 0)
          LIMIT 5000000)
  ),
  labelled AS (SELECT substring(event_id, 4) AS eid FROM anomaly_verdicts FINAL
               WHERE startsWith(event_id, 'ml:') AND created_at >= now64(9) - INTERVAL 30 DAY),
  asked AS (SELECT problem_id AS pid FROM cosre_interactions WHERE created_at >= now64(3) - INTERVAL 30 DAY AND problem_id != ''
            UNION DISTINCT SELECT ex_id FROM rum WHERE ex_id != ''),
  sent AS (SELECT related_id AS pid, sent_at FROM notification_log
           WHERE sent_at >= now64(9) - INTERVAL 31 DAY AND related_kind = 'problem' AND ok = 1 AND channel_kind != 'ignore'),
  acts AS (
    SELECT arrayJoin(JSONExtract(details, 'ids', 'Array(String)')) AS pid, time FROM audit_log
    WHERE time >= now64(9) - INTERVAL 31 DAY AND action = 'problem.acknowledge'
    UNION ALL
    SELECT arrayJoin(JSONExtract(details, 'problems', 'Array(String)')), time FROM audit_log
    WHERE time >= now64(9) - INTERVAL 31 DAY AND action = 'problems.bulk_resolve'
    UNION ALL
    SELECT target_id, time FROM audit_log WHERE time >= now64(9) - INTERVAL 31 DAY
      AND action IN ('notify.ignore', 'problem.assign', 'problem.note.create', 'problem.rootcause.set', 'problem.investigation.enqueue')),
  acted AS (SELECT DISTINCT s.pid AS pid FROM sent AS s INNER JOIN acts AS a ON a.pid = s.pid
            WHERE a.time >= s.sent_at AND a.time < s.sent_at + INTERVAL 24 HOUR),
  ml AS (
    SELECT id, toString(detector) AS detector, multiIf(
        JSONExtractString(evidence, 'problem_id') != '', JSONExtractString(evidence, 'problem_id'),
        JSONExtractBool(evidence, 'svcShadow', 'live') AND JSONExtractString(evidence, 'svcShadow', 'problemId') != '',
          JSONExtractString(evidence, 'svcShadow', 'problemId'),
        JSONExtractBool(evidence, 'groupShadow', 'live'), JSONExtractString(evidence, 'groupShadow', 'problemId'),
        '') AS problem_ref
    FROM ml_events FINAL
    WHERE started_at >= now64(9) - INTERVAL 30 DAY
      AND detector NOT IN ('svc_slowdown', 'metric_anomaly', 'anomaly_promotion')
      AND id NOT IN (SELECT eid FROM labelled))
SELECT detector,
  count() AS etiketsiz,
  countIf(problem_ref != '') AS problem_bagli,
  countIf(id IN (SELECT ml_id FROM rum)) AS olay_detayi_acildi,
  countIf(problem_ref != '' AND problem_ref IN (SELECT p_id FROM rum)) AS problem_detayi_acildi,
  countIf(problem_ref != '' AND problem_ref IN (SELECT pid FROM asked)) AS cosre_soruldu,
  countIf(problem_ref != '' AND problem_ref IN (SELECT pid FROM sent)) AS bildirildi,
  countIf(problem_ref != '' AND problem_ref IN (SELECT pid FROM acted)) AS bildirim_sonrasi_24sa_aksiyon,
  countIf(id NOT IN (SELECT ml_id FROM rum) AND (problem_ref = '' OR (problem_ref NOT IN (SELECT p_id FROM rum)
          AND problem_ref NOT IN (SELECT pid FROM asked) AND problem_ref NOT IN (SELECT pid FROM acted)))) AS hic_ilgi_yok
FROM ml GROUP BY detector ORDER BY etiketsiz DESC LIMIT 200
SETTINGS max_execution_time = 120;
```

### A.4 Bugün ölçülemeyenler

- CoSRE'ye bir ML olayının sorulduğu ve bildirim bağlantısına tıklandığı ölçülemez (A.3 tablosu).
- Problem bağı olmayan aileler (özne, `anomaly-cluster:`) olay düzeyinde dedektöre geri bağlanamaz. Yalnız Problem katmanında görünürler.
- 30 günden eski olayların etiketi dedektöre bağlanamaz. Oy 180 gün yaşar ama olay 30 günde düşer (`store.go:2674,3301`). Pencere bu yüzden 30 gündür.
- Elle kapatmanın gerekçesi yoktur (`chstore/problem.go:676`). Bu yüzden ne doğru ne yanlış sayılabilir.
- Grup Problem'inde etiket, üye dedektörlerden hangisinin "suçlu" olduğunu söylemez (çoklu kredi).
- Triyaj kararının geçmişi `saved_views`'te tutulmaz; yalnız `audit_log` `problem.verdict` içinde vardır.

## Bölüm B — sürekli ölçüm (tasarım)

### B.1 Dedektör karnesi

**Bugün olan, tekrar yazılmayacak şeyler:**
- `MLScorecard`: dedektör ve sinyal başına olay, karar ve isabet sayısı (`chstore/ml_events.go:478-601`).
- Bu karne Sinyaller sekmesinin kapalı "Teknik ayrıntı / Dedektör durumu" bölümündeki `MLTab` içinde çizilir (`FE/components/coremetry/anomalies/MLTab.tsx:297-301`, `FE/features/anomalies/AnomalyStreamsPage.tsx:36`).
- Problems "Döküm" ekranı aile başına sayı, açık Problem, < 15 dk kapanan ve churn gösterir (`chstore/problem_source_breakdown.go:209-238`; `FE/components/coremetry/problems/ProblemSourceBreakdown.tsx`) **[K]**.

**Eklenecek metrikler.** Hepsi A1, A1b ve A1c'nin aynı ifadeleridir; ikinci bir sözlük yazılmaz.

| Metrik | Tanım | Yer |
|---|---|---|
| precision + Wilson alt ve üst sınırı | Yalnız kurala girebilen kaynaklar (olay oyu ve vaka). Triyaj ayrı sütunda ve "kural dışı" yazısıyla | ML karnesi satırı (MLTab) |
| hacim | 30 günde olay sayısı ve bunların kaçının Problem'e bağlandığı | MLTab |
| etiket kapsaması | `n_kural / olay`; < %5 ise kırmızı değil, gri "karar için yetersiz" | MLTab |
| medyan etiketleme süresi | A1b | MLTab teknik detay |
| < 15 dk otomatik kapanan payı | Gürültü vekili; Döküm'deki `short_n` ile aynı ifade | Döküm aile satırı + MLTab |
| tek servis payı | A1c `pay` | MLTab teknik detay |
| gölge durumu | açık / gölgede (otomatik, tarih, gerekçe) / sabitlenmiş | MLTab, Settings › ML dedektör satırı, Sinyaller durum çizgisi |

**Nerede ve ne sıklıkla hesaplanır:**
- `MLScorecard` tek bir CH toplamasına geçer: `GROUP BY detector` ve sayım CH'de yapılır. Bugünkü 5000 satırlık Go birleştirmesi prod'da kesik sonuç verir (`chstore/ml_events.go:552-556`) **[K]**.
- `ml_events` küçük bir state tablosudur (≤ ~162 bin satır / 30 gün). MV gerekmez **[V]**.
- Okuma mevcut `serveCached` arkasında kalır; TTL 60 sn yerine 5 dk olur (`api/ml.go:39`).
- Problem katmanı Döküm ucuna iki sütunla eklenir: vaka etiketli ve vaka doğru. Bu, aynı tek tarama içinde `incident_cases` ile bir LEFT JOIN demektir.
- Yeni sayfa açılmaz.

### B.2 Otomatik gölge kuralı

**Tanım (öneri):**
- **Kapsam:** yalnız ML dedektörleri (`chstore.MLDetectorNames`) ve dedektör başına uygulanır. Dedektör × servis yalnız teşhis için gösterilir.
- **Etiket:** son 30 günün olay oyları ve onaylı vakaları (A1 `n_kural`).
- **Düşme şartlarının hepsi** aynı anda tutmalı:
  - `n_kural ≥ 20`,
  - Wilson **üst** sınırı < 0,50,
  - etiketler ≥ 2 farklı kişiden ve ≥ 5 farklı günden,
  - A1c `pay ≤ 0,6`.
- Düşmede neden üst sınır kullanılıyor: dedektör ancak "iyimser tahminle bile yarıdan azı doğru" olduğunda düşer. Alt sınırla düşürmek az etiketle her dedektörü düşürür.
- **Geri dönüş:** gölgedeyken toplanan ≥ 10 etiketle Wilson **alt** sınırı ≥ 0,60, ya da operatörün sabitlemesi. Aradaki 0,50–0,60 boşluğu histerezistir.
- **Bekleme süreleri:** gölgede en az 7 gün kalınır. Geri döndükten sonra 14 gün yeniden düşme yoktur.
- **Hız sınırı:** günde en çok 1 düşüş; aynı anda açık ML dedektörlerinin en çok %25'i gölgede olabilir. Fazlası "düşerdi" listesinde bekler.
- **Olay sırasında düşüş yok:** dedektörün son 2 saatte açık bir P1 Problem'e bağlı olayı varsa o günün değerlendirmesi atlanır.
- **Değerlendirme zamanı:** günde bir, ML liderinin tikinde (`ml/recorder.go`, lider kilidi).

**Bugünkü kodda "gölge" ne demek:**
- ML olayı her zaman `ml_events`'e yazılır (`stage` varsayılanı `shadow`, `chstore/store.go:3284`).
- Problem ve bildirim olaydan değil, grup ve servis kararından doğar. `DecideGroupPromotion` üyeleri `groupExcluded` ve `problemCandidate` süzgeçlerinden geçirir (`ml/group_gate.go:163,244`).
- Gürültü kapısı `held` olayı aday saymaz (`ml/publish_gate_enforce.go:14-19`).
- Servis düzeyi kararı `isSvcDetector` ile seçer (`ml/svc_shadow.go:132`) **[K]**.
- Önerilen gölge, bu süzgeçlere tek bir yüklem eklemektir: `demoted(detector)`. Böylece:
  - Olay yazılır, kanıta `detShadow` damgası basılır (şemasız; `groupShadow` emsali, `ml/group_gate.go:29-31`).
  - Olay Sinyaller'in "Elenenler" görünümünde "dedektör gölgede (karne)" nedeniyle görünür.
  - Olay hiçbir grup ya da servis kolunu beslemez, açık Problem'e üye olup önceliği yükseltmez.
- Problem açmayan dedektörlerin otomatik kurala girmesi anlamsızdır; bunlar karnede yalnız hacimle görünür:
  - `MLOffFeedDetector`: telemetri hacmi, `throughput_drop`, `service_silent`, `log_volume`, `trace_pod_outlier`, `new_template` (`chstore/ml_events.go:263-272`) **[K]**.
- Eski ve kural aileleri (svc-slowdown, db-*, runtime, exception, external, özne) Problem'i doğrudan açar. Bu aileler ilk sürümde yalnız karnede ölçülür, otomatik gölgeye düşmez. Neden: her birinin ayrı yazma yolu var ve ortak bir kapı yok (`/aiops` §3).

**Muaf olanlar (operatörün açık kararları):**
- Etki kuralı aileleri (`impactRuleRun`, `ml/quality.go:60-70`):
  - `svc_error_rate`, `svc_response_time`, `endpoint_impact`, `pod_oom`,
  - etki kuralıyla açılmış consumer lag (`metric_kafka`).
- Kullanıcı alarm kuralları, `monitor:`, `builtin-*`, `slo:`, `exception-storm` ve `exception:*` (zaten ML değil).

`metric_kafka` aynı dedektörde hem etki olayı hem diğer serileri taşır. İlk sürümde tümüyle muaf tutulması önerilir (bkz. Açık kararlar).

**Ayar ve kayıt:**
- **Ayar:** `ml` blobu altında `precisionShadow`: `{mode: off|shadow|on, minLabels: 20, demoteUpper: 0.50, restoreLower: 0.60, windowDays: 30, maxPerDay: 1, maxShare: 0.25, pins: {dedektör: "on"|"shadow"}}`. Yer: Settings › Anomaly › ML, gürültü kapılarının yanında (`ml/publish_gate_enforce.go:51`). Varsayılan `off`; kod gelince `shadow`.
- **Audit:**
  - Elle değişiklik mevcut `ml.config` kaydıyla düşer (`api/ml.go:393-401`).
  - Otomatik düşüş ve geri dönüş `AppendAudit` ile doğrudan yazılır, aktör `system:ml-precision`, `details` içinde n, alt ve üst sınır ve pencere. Neden doğrudan: `s.audit` claims yokken sessizce yazmaz (`api/anomaly_extra.go:317-321`); emsal `api/notify_actions.go:110-116`.
- **Durum:** `system_settings` içinde ayrı bir durum blobu (`ml_precision_state`; `ml_tick_status` emsali). Kayıtlı ayar otomatik olarak yeniden yazılmaz.
- **Görünürlük:** gölgedeki dedektör üç yerde rozet taşır (MLTab, Settings › ML, Sinyaller durum satırı). Rozetin yanında gerekçe cümlesi olur, örneğin: "30 günde 24 etiket, 6'sı doğru; üst sınır %45".

**Devreye alma (R programı deseni):**
1. `shadow` kipi: kural yalnız "düşerdi" kaydı ve günlük rapor yazar, davranış değişmez.
2. En az 14 gün izlenir. Operatör raporu görür: hangi dedektör, hangi gün, kaç Problem kesilirdi, bunların kaçı vaka etiketiyle gerçekti.
3. Kesilecek gerçek Problem sayısı 0 değilse `on` önerilmez. Bu, R programındaki "gölgede %100 recall" kuralıdır.
4. `on` yalnız operatör onayıyla açılır.

**Hata kipleri ve önlemler:**

| Hata kipi | Önlem |
|---|---|
| Etiket yanlılığı (insanlar yalnız rahatsız edeni etiketler → precision düşük görünür) | Haftalık rastgele kuyruk (B.3/1) ayrı sayılır. Kuyruk etiketlerinin alt sınırı ≥ 0,60 ise organik etiketler düşüşü tetiklemez |
| Küçük n | n ≥ 20 + üst sınır şartı; altında karnede "yetersiz" |
| Tek gürültülü servis | A1c `pay > 0,6` → düşüş yok, karnede "tek servis baskın" uyarısı |
| Gerçek olay sırasında düşüş | Açık P1 bağı varsa gün atlanır; günde 1 ve en çok %25 tavanı |
| Kaynak kalitesi eşit değil | Vaka (kök neden) > olay oyu; triyaj kurala hiç girmez. Vaka ve oy çelişirse vaka kazanır |
| `not_anomaly` nedeni karışık (`expected`, `no_impact` dedektör hatası değil) | Karnede nedene göre kırılım; kuralda sayılıp sayılmayacağı açık karar |
| Dedektör kodu değişti, eski etiketler eskidi | `ConfigHash` değişince pencere o tarihten başlar (`chstore/ml_settings.go:438`) **[V]** |

### B.3 Etiket kapsamasını artırmanın en ucuz iki yolu

Sıralama operatörün harcayacağı emeğe göredir. Hiçbiri oy kuralını delmez.

1. **Mevcut açık eylemden etiket türetmek (ek tık yok).** Onaylı kök neden vakası (`incident_cases`, `status='confirmed'`) bağlı Problem'in bütün üye olaylarına doğru ya da yanlış etiketi olarak yayılır. A1 bunu zaten yapar; karne ve kural da aynısını kullanır.
   - Maliyet: karne toplamasına bir LEFT JOIN.
   - Verim: onaylanan her vaka 1 Problem ve ortalama 2–3 üye olayı etiketler **[V]**.
   - Sınır: vakalar çoğunlukla gerçek olaylardır. Yanlış alarm tarafını (`noise`) az besler, bu yüzden precision'ı iyimser gösterir.
   - Triyaj "Problem değil / too_sensitive" de yanlış etiketi olarak türetilebilirdi; ancak D4 bunu yasaklıyor (Açık kararlar).
2. **Haftalık 10 örneklik etiketleme kuyruğu (mevcut yüzey, mevcut düğmeler).**
   - Yer: Sinyaller sekmesinde "Bu hafta etiketle (10)" süzgeç çipi.
   - Seçim: son 7 günün etiketsiz ML olaylarından deterministik ve tabakalı örnek: `fnv(id | ISO hafta) mod`. Önce n < 20 olan en hacimli dedektörlere, en az birer örnek.
   - Etiket detay sayfasındaki mevcut "Gerçek anomali / Sorun değil" düğmesiyle verilir.
   - Maliyet: bir liste süzgeci ve bir saf örnekleyici (tablo testi).
   - Emek: haftada ~10 dk, tek kişi.
   - Verim: ayda ~40 rastgele etiket. Yanlılığı düzeltir. En hacimli 4 dedektör 8–10 haftada n = 20'ye ulaşır **[V]**.
3. **(En son, önerilmez) Kapatma ya da onay anında "Gerçek miydi?" sorusu.** Her kapatmaya ek tık ister ve aceleyle verilen cevap düşük kalitelidir.

## Bölüm C — karar tablosu

Girdi A1'in `olay:ml` satırlarıdır. "Ana dedektörler" = 30 günde ML Problem'lerinin (`ml_group`, `ml_svc`, `ml_single`) %80'ini besleyen dedektörler; bunlar A2 `problem_bagli` ile ve A1 Problem katmanıyla bulunur. Kural nokta tahmine değil `wilson_alt_kural`'a ve kapsamaya bakar.

| # | Ölçülen durum | Sonraki iş 1 | Sonraki iş 2 |
|---|---|---|---|
| C0 | Ana dedektörlerin çoğunda `n_kural < 20` ya da kapsama < %5 (**etiket yetersiz, karar yok**) | B.3/1 + B.3/2'yi 4 hafta çalıştır, A1'i yeniden koş. Tahminle sıkılaştırma yapılmaz | Karnenin yalnız ölçüm kısmı (B.1, kuralsız); kural `off` kalır |
| C1 | En az bir ana dedektörde `n_kural ≥ 20` ve Wilson **üst** < 0,50 | O dedektörü sıkılaştır (aşağıdaki kol tablosu); ölçüm 4 hafta sonra tekrarlanır | B.1 + B.2 `shadow` kipte |
| C2 | Ana dedektörlerde alt sınır 0,50–0,70 arası, hiçbiri C1'e girmiyor | B.1 + B.2 `shadow` (karne önce, çünkü sıkılaştırılacak açık bir dedektör yok) | Alt sınırı en düşük iki dedektöre hedefli sıkılaştırma |
| C3 | Bütün ana dedektörlerde alt sınır ≥ 0,70 ve kapsama ≥ %10 | Rollout karnesi (B.1 + B.2 → 14 gün `shadow` → onayla `on`) | Sıkılaştırma yok; etiket kuyruğu sürer |
| C4 | Olay katmanı iyi (C2/C3), ama `problem` katmanında `ml_group` ya da `ml_svc` vaka alt sınırı < 0,50 | Dedektör değil, **kapı** sıkılaştırılır: tek sinyal kolu eşikleri (600 sn, 2×; `ml/group_gate.go:222-226`) ya da ≥ 2 sinyal kuralı | B.1 + B.2 `shadow` |

**Sıkılaştırma kolları** (mevcut ayar alanları; yeni dedektör ya da sinyal yok):

| Dedektör | Kol (Settings › ML) |
|---|---|
| `op_latency` | `SlowRequestSec`, `MinAffectedRequests`, `ChronicEpisodes` (`chstore/ml_settings.go:215-219`) |
| log ailesi (`error_rate`, `template_spike`, `rare_template`, `change_point`) | `CriticalZ`, `DwellBuckets`, `MinBaselineRate`, `MinBaselineSamples` |
| `dependency_latency`, `dependency_error_rate` | `CriticalZ`, `DwellBuckets`, `Quality.ImpactMinRel/ImpactMinAbs` |
| hepsi | Gürültü kapısı kipi `on` (`PublishGates`; varsayılan zaten `on`, `chstore/ml_settings.go:282-287`) |

**`docs/plans/ml-next.md` ile bağ.** O plandaki maddeler (4–7, B2, B3, C1, C2) gemide. Açık kalanlar ikidir: eski anomali sayfasının C3 düzeni ve heap bandı ile Thanos kaynağı. Hiçbiri precision işi değildir.

Bu belge oraya iki yeni madde ekler:
- **P1 "dedektör sıkılaştırma"**: C1 ya da C4 satırından seçilen kol.
- **P2 "rollout karnesi"**: B.1 + B.2.

Sıra C tablosundan mekanik çıkar:
- C0: önce etiket, sonra ölçüm.
- C1 ve C4: P1 sonra P2.
- C2 ve C3: P2 sonra P1 (ya da P1 yok).

Sayı gelince operatörün seçtiği satır `ml-next.md`'ye yazılır.

## Girdi belgeleri ile bugünkü kod arasındaki çelişkiler

- `anomaly-reassessment-audit.md` N2 "≥ 20 etiket ve ≥ 0,8 isabet karne terfi kapısı" diyor. Bu kapı v0.10.2067'de kalktı (`api/ml.go:252-253`, `chstore/ml_settings.go:184-186`). Grup kapısı "Karne kapı DEĞİL" diyor (`ml/group_gate.go:17`) **[K]**.
- `learning-loop-audit.md`:
  - §2 "`feedbackZ` (≥3 'değil' → +0,5 z)" diyor. Bugün kodda `feedbackZ` yok ve oylar çalışma zamanına giremez (`ml/vote_runtime_guard_test.go:3-10`) **[K]**.
  - §2 "ML dedektörleri (19), hepsi kapalı" diyor. Bugün 27 ad var ve 10 tanesi varsayılan açık (`chstore/ml_settings.go:28-41,324-331`) **[K]**.
  - §2 ML etiket UI'ını `FE/features/anomalies/MLReplaySheet.tsx:103` olarak gösteriyor. Bileşen `FE/components/coremetry/anomalies/MLReplaySheet.tsx:367`'ye taşınmış **[K]**.
- `ml-next.md` C3 "Gerçek mi? Evet/Hayır" diyor. UI bugün "Gerçek anomali / Sorun değil" ve neden seçimi gösteriyor (`FE/lib/i18n.tr.ts:628-629`) **[K]**.
- `chstore/ml_labels.go:3-9` yorumu etiketin "taban hijyeni ve geri bildirim ayarı"nda kullanıldığını söylüyor. Bekçi test yalnız backtest'e izin veriyor (`ml/vote_runtime_guard_test.go:32-34`); yorum eskimiş **[K]**.

## Açık kararlar (operatör)

1. **Oy kuralının kısmi tersine çevrilmesi.** Olay oyları (kaynak 1–2) ve onaylı vakalar (kaynak 5) otomatik gölge kuralına girsin mi? Öneri: evet. `TestAnomalyVotesDoNotReachRuntime` tek okuyucuyla genişler, UI notu değişir, DECISIONS'a kayıt girer.
2. **D4.** Triyaj "Problem değil" (gerekçe `too_sensitive` ya da `wrong_attribution`) yanlış etiketi olarak kurala girsin mi? Öneri: hayır. Karnede ayrı sütun yeter.
3. **`not_anomaly` nedenleri.** `expected` ve `no_impact` kuralda "yanlış" sayılsın mı? Öneri: yalnız `no_deviation`, `other` ve nedensiz oylar yanlış sayılsın. Diğer ikisi karnede ayrı gösterilsin.
4. **Eşikler.** n ≥ 20, düşme üst sınır < 0,50, geri dönüş alt sınır ≥ 0,60, 30 gün, günde 1, %25 tavan, bekleme 7/14 gün. Kabul mü?
5. **`metric_kafka`.** İlk sürümde tümüyle muaf mı (öneri), yoksa yalnız etki kuralıyla açılan olayları mı muaf?
6. **Eski ve kural aileleri.** İlk sürümde yalnız ölçülsün mü (öneri), yoksa aile düzeyinde gölge kapısı mı tasarlansın?
7. **"Rollout karnesi" ne demek?** Repoda tanımı yok (grep boş). Bu belge onu "B.1 + B.2'nin devreye alınması" olarak okudu. Deploy (rollout) kalitesi karnesi kastediliyorsa C tablosunun 2. sütunu değişir.
8. **Ölçüm ekleri.** `cosre_interactions.anomaly_id` ve bildirim bağlantısında `?src=notify` eklensin mi? Öneri: evet, ikisi küçük ve tek sürüm.
9. **A0/A1/A2 prod koşusu.** Hangi düğümde koşulacağı `d1-olcum.md` §4 sonucuna bağlı.

## Kararlar (operatör, 2026-11-27)

Operatörün cevabı: "Precision onay." Açık kararlardaki öneriler karar oldu.

| # | Konu | Karar |
|---|---|---|
| 1 | Oy kuralının kısmi tersine çevrilmesi | **Onaylandı.** Olay oyları (kaynak 1–2) ve onaylı vakalar (kaynak 5) otomatik gölge kuralına girer. Bu, 2026-10-17 kuralını yalnız bu iki kaynak ve yalnız bu kural için tersine çevirir; 👍/👎, triyaj ve örtük sinyaller için kural aynen geçerlidir (`TestVotesNeverReachRuntimeDecisions` değişmez). |
| 2 | Triyaj "Problem değil" (D4) | Kurala girmez; karnede ayrı sütun. |
| 3 | `not_anomaly` nedenleri | Yalnız `no_deviation`, `other` ve nedensiz oylar "yanlış" sayılır; `expected` ve `no_impact` karnede ayrı gösterilir. |
| 4 | Eşikler | n ≥ 20; düşme: Wilson üst sınırı < 0,50; geri dönüş: alt sınır ≥ 0,60; pencere 30 gün; günde en çok 1 düşüş; en çok %25; bekleme 7 / 14 gün. |
| 5 | `metric_kafka` | İlk sürümde tümüyle muaf. |
| 6 | Eski ve kural aileleri | İlk sürümde yalnız ölçülür. |
| 7 | "Rollout karnesi" | Bu belgenin okuması geçerli sayıldı: B.1 karnesi + B.2 kuralının devreye alınması. Operatör başka bir şey kastettiyse C tablosunun 2. sütunu düzeltilir. |
| 8 | Ölçüm ekleri | `cosre_interactions.anomaly_id` ve bildirim bağlantısında `?src=notify` eklenir. |
| 9 | Prod koşusu | A0 / A1 / A2 operatörde; düğüm seçimi `d1-olcum.md` §4. |

**Uygulama sırası.** Prod sayıları henüz yok; Bölüm C'nin her satırında gereken işler önce yapılır:
1. B.1 karnesi (kesik okuma düzeltmesi, Wilson sınırları, kapsama), B.3/1 vaka etiketinin üye olaylara yayılması, B.3/2 haftalık etiketleme kuyruğu ve 8 numaralı ölçüm ekleri.
2. B.2 otomatik gölge kuralı, yalnız `shadow` ("düşerdi" kaydı) kipinde. `on` kipi ayrı operatör onayı ister ve en az 14 günlük gölge raporundan sonra önerilir.

