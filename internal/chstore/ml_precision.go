package chstore

// ml_precision.go — v0.10.2484 (precision programı ADIM 1, docs/anomaly/
// precision-program.md B.1 + B.3/1; operatör onayı 2026-11-27). ML dedektör
// karnesi TEK ClickHouse toplamasıdır: GROUP BY dedektör (iç düzeyde
// dedektör × servis — "en kötü servis payı" için), sayım CH'de. Eski okuma
// 30 günü 5000 satırlık sırasız LIMIT ile okuyup Go'da birleştiriyordu; prod
// hacminde sessizce kesikti.
//
// SALT RAPOR. Hiçbir tespit, kapı, öncelik ya da bildirim bu dosyayı okumaz
// (TestAnomalyVotesDoNotReachRuntime `MLScorecard` adını ml / anomaly /
// evaluator / notify paketlerinde yasaklar; ad bilinçli korundu).
//
// Etiket tanımı (belgenin A1 / A1b / A1c ifadeleri; ikinci sözlük yok):
//   - Kurala girebilen (rule-eligible) kaynaklar yalnız iki: olay oyu
//     (anomaly_verdicts 'ml:' öneki; kaynak 1–2) ve Problem'in onaylı kök
//     neden vakası (incident_cases status='confirmed'; kaynak 5).
//   - Öncelik: olay oyu > vaka. Vaka, bağlı Problem'in BÜTÜN üye olaylarını
//     etiketler (B.3/1): category='noise' yanlış, diğerleri doğru.
//   - "Sorun değil" oyunda yalnız no_deviation, other ve nedensiz oy YANLIŞ
//     sayılır; expected ve no_impact "eyleme değmez"dir — ayrı sayılır, n'e
//     girmez (açık karar 3).
//   - Problem triyajı ("Gerçek problem / Problem değil", D4) yalnız ayrı bir
//     alanda ve KURAL DIŞI olarak raporlanır; n / doğru / yanlış'a girmez.
//
// Bilinen sınırlar (audit, UI notunda da yazılı):
//   - Grup Problem'inin vakası grubun HER üye dedektörüne yazılır (çoklu
//     kredi; tek dedektörün payı ayrıştırılamaz).
//   - Servis düzeyi Problem'lerin kimliği sabittir (anomaly-auto:svc:<svc>)
//     ve problems RMT(id) olduğundan yalnız servisin SON Problem'i vakayı
//     taşır; önceki açılışların olayları vaka etiketi alamaz.
//
// Maliyet: ml_events ≤ ~27 dedektör × DailyEventCap(200) × 30 gün ≈ 162 bin
// satır (FINAL küçük); en ağır kolon kanıt JSON'u (Problem bağı). problems
// FINAL 37 gün süzgeciyle bir kez taranır. Sınırlar: zaman aralığı, LIMIT,
// max_execution_time; uç 5 dk serveCached arkasında.

import (
	"context"
	"math"
	"strings"
	"time"
)

// Wilson %95 sabiti (belgenin A1 ifadesindeki 1.96; 1.9208 = z²/2, 3.8416 = z²).
const wilsonZ = 1.96

// WilsonBounds — SAF: k doğru / n etiket için %95 Wilson skor aralığı
// [alt, üst]. n ≤ 0 → (0, 0, false). A1 SQL'iyle aynı formül:
// (p + z²/2n ∓ z·sqrt(p(1−p)/n + z²/4n²)) / (1 + z²/n).
func WilsonBounds(k, n int64) (lo, hi float64, ok bool) {
	if n <= 0 || k < 0 || k > n {
		return 0, 0, false
	}
	nf := float64(n)
	p := float64(k) / nf
	z2 := wilsonZ * wilsonZ
	center := p + z2/(2*nf)
	margin := wilsonZ * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf))
	den := 1 + z2/nf
	lo, hi = (center-margin)/den, (center+margin)/den
	return math.Max(0, lo), math.Min(1, hi), true
}

// Precision programı eşikleri (belgenin B.1 tablosu ve B.2 "Küçük n").
const (
	// MLPrecisionMinLabels — n < 20 → karnede "karar için yetersiz".
	MLPrecisionMinLabels = 20
	// MLPrecisionMinCoverage — kapsama < %5 → "karar için yetersiz" (kırmızı değil).
	MLPrecisionMinCoverage = 0.05
)

// mlPrecisionExemptDetectors — açık karar 5 + B.2 "Muaf olanlar": etki kuralı
// aileleri (impactRuleRun, internal/ml/quality.go) ve metric_kafka (ilk
// sürümde tümüyle muaf). Karnede ölçülür, "muaf" yazılır.
var mlPrecisionExemptDetectors = []string{"svc_error_rate", "svc_response_time", "endpoint_impact", MLDetPodOOM, "metric_kafka"}

// MLPrecisionExempt — SAF: dedektör precision kuralından muaf mı.
func MLPrecisionExempt(det string) bool {
	for _, d := range mlPrecisionExemptDetectors {
		if d == det {
			return true
		}
	}
	return false
}

// Etiket kaynağının sorgu dışı değerleri.
const (
	// VerdictOriginQueue — haftalık etiket kuyruğundan verilen oy (B.3/2;
	// anomaly_verdicts.origin). '' = organik.
	VerdictOriginQueue = "queue"
)

// NormalizeVerdictOrigin — SAF: kapalı küme (” | queue); başka değer ”.
func NormalizeVerdictOrigin(s string) string {
	if strings.TrimSpace(s) == VerdictOriginQueue {
		return VerdictOriginQueue
	}
	return ""
}

// mlScorecardSQL — SAF: karnenin tek sorgusu. withReason / withOrigin =
// anomaly_verdicts.reason / .origin kolonları görüldü mü (iki-boot; yoksa
// sabit ”). Argümanlar: (olay alt sınırı, Problem alt sınırı).
//
// İç düzey dedektör × servis, dış düzey dedektör: medyan ve farklı
// etiketleyen sayısı State/Merge ile birleşir (Go'da birleştirilemez).
func mlScorecardSQL(withReason, withOrigin bool) string {
	reason, origin := "''", "''"
	if withReason {
		reason = "toString(reason)"
	}
	if withOrigin {
		origin = "toString(origin)"
	}
	return `
		WITH
		  ev AS (
		    SELECT substring(event_id, 4) AS eid, toString(verdict) AS verdict, ` + reason + ` AS reason,
		           created_by, created_at, ` + origin + ` AS origin
		    FROM anomaly_verdicts FINAL
		    WHERE startsWith(event_id, 'ml:') AND created_at >= ?
		  ),
		  cs AS (` + problemCaseLabelSQL + `),
		  pl AS (
		    SELECT p.id AS problem_id, c.source_id != '' AS has_case, c.case_cat AS case_cat, v.name AS triage,
		      toUInt8(p.resolved_at IS NOT NULL
		        AND dateDiff('second', p.started_at, assumeNotNull(p.resolved_at)) < 900
		        AND (position(p.description, 'auto-resolved:') > 0 OR position(p.description, 'kendiliğinden kapandı') > 0)) AS short_auto
		    FROM (SELECT id, rule_id, service, started_at, resolved_at, description
		          FROM problems FINAL WHERE started_at >= ?) AS p
		    LEFT JOIN cs AS c ON c.source_id = p.id
		    LEFT JOIN (SELECT substring(id, 4) AS sig, name FROM saved_views FINAL
		               WHERE page = 'problem-verdict' AND owner_id = '' AND startsWith(id, 'pv:')
		                 AND name IN ('real', 'noise')) AS v
		           ON v.sig = concat('p:', p.rule_id, '|', toString(p.service))
		  ),
		  lab AS (
		    SELECT toString(m.detector) AS detector, toString(m.service) AS service, m.problem_ref AS problem_ref,
		           e.verdict AS ev_verdict, e.reason AS ev_reason, e.origin AS ev_origin, e.created_by AS ev_by,
		           dateDiff('second', m.started_at, e.created_at) AS label_s,
		           l.has_case AS has_case, l.triage AS triage, l.short_auto AS short_auto,
		           multiIf(e.verdict != '', 'olay', l.has_case, 'vaka', l.triage != '', 'triyaj', '') AS src,
		           toInt8(multiIf(
		             e.verdict = 'anomaly', 1,
		             e.verdict = 'not_anomaly' AND e.reason IN ('expected', 'no_impact'), 2,
		             e.verdict = 'not_anomaly', 0,
		             l.has_case AND l.case_cat != 'noise', 1, l.has_case, 0,
		             l.triage = 'real', 1, l.triage = 'noise', 0, -1)) AS ok
		    FROM (SELECT id, detector, service, started_at, ` + mlEventProblemIDExpr + ` AS problem_ref
		          FROM ml_events FINAL
		          WHERE started_at >= ? AND detector NOT IN (` + mlMemberDetectorsSQL() + `)) AS m
		    LEFT JOIN ev AS e ON e.eid = m.id
		    LEFT JOIN pl AS l ON l.problem_id = m.problem_ref
		  ),
		  per_svc AS (
		    SELECT detector, service,
		      count()                                                  AS produced,
		      countIf(problem_ref != '')                               AS linked,
		      countIf(problem_ref != '' AND short_auto = 1)            AS linked_short_auto,
		      countIf(src IN ('olay', 'vaka') AND ok IN (0, 1))        AS n,
		      countIf(src IN ('olay', 'vaka') AND ok = 1)              AS correct,
		      countIf(src IN ('olay', 'vaka') AND ok = 0)              AS wrong,
		      countIf(src = 'olay' AND ok IN (0, 1))                   AS by_vote,
		      countIf(src = 'vaka')                                    AS by_case,
		      countIf(src = 'vaka' AND ok = 1)                         AS case_correct,
		      countIf(src = 'olay' AND ok = 2)                         AS not_actionable,
		      countIf(ev_verdict = 'not_anomaly' AND ev_reason = 'no_deviation') AS r_no_deviation,
		      countIf(ev_verdict = 'not_anomaly' AND ev_reason = 'no_impact')    AS r_no_impact,
		      countIf(ev_verdict = 'not_anomaly' AND ev_reason = 'expected')     AS r_expected,
		      countIf(ev_verdict = 'not_anomaly' AND ev_reason = 'other')        AS r_other,
		      countIf(ev_verdict = 'not_anomaly' AND ev_reason = '')             AS r_none,
		      countIf(src = 'triyaj')                                  AS triage_n,
		      countIf(src = 'triyaj' AND ok = 1)                       AS triage_real,
		      countIf(src = 'olay' AND ok IN (0, 1) AND ev_origin = 'queue') AS queue_n,
		      countIf(src = 'olay' AND ok = 1 AND ev_origin = 'queue')       AS queue_correct,
		      quantileExactIfState(0.5)(toFloat64(label_s), ev_verdict != '') AS label_q,
		      uniqExactIfState(ev_by, ev_verdict != '' AND ev_by != '')       AS labellers_u
		    FROM lab
		    GROUP BY detector, service
		  )
		SELECT detector,
		  sum(produced), sum(linked), sum(linked_short_auto), sum(n), sum(correct), sum(wrong),
		  sum(by_vote), sum(by_case), sum(case_correct), sum(not_actionable),
		  sum(r_no_deviation), sum(r_no_impact), sum(r_expected), sum(r_other), sum(r_none),
		  sum(triage_n), sum(triage_real), sum(queue_n), sum(queue_correct),
		  toFloat64(quantileExactIfMerge(0.5)(label_q)) AS median_label_s,
		  toUInt64(uniqExactIfMerge(labellers_u)) AS labellers,
		  max(wrong) AS worst_svc_wrong, argMax(service, wrong) AS worst_svc
		FROM per_svc
		GROUP BY detector
		ORDER BY detector
		LIMIT 500
		SETTINGS max_execution_time = 20, join_use_nulls = 0`
}

// mlScorecardProblemSlack — Problem penceresi olay penceresinden bu kadar
// geniş (belgenin A1'i: 30 g olay, 37 g Problem — olaydan önce açılmış
// grup Problem'i de bağlanır).
const mlScorecardProblemSlack = 7 * 24 * time.Hour

// MLScorecard — son `since`'den beri dedektör başına karne (tek sorgu). Ad
// TestAnomalyVotesDoNotReachRuntime'ın oy okuyucu listesinde: çalışma
// zamanı paketleri bunu çağıramaz.
func (s *Store) MLScorecard(ctx context.Context, since time.Time) ([]MLDetectorScore, error) {
	sql := mlScorecardSQL(s.hasVerdictReasonCol.Load(), s.hasVerdictOriginCol.Load())
	rows, err := s.conn.Query(ctx, sql, chDateTime64Arg(since), chDateTime64Arg(since.Add(-mlScorecardProblemSlack)), chDateTime64Arg(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLDetectorScore
	for rows.Next() {
		var r MLDetectorScore
		var c mlScoreCounts
		if err := rows.Scan(&r.Detector,
			&c.Produced, &c.Linked, &c.LinkedShortAuto, &c.N, &c.Correct, &c.Wrong,
			&c.ByVote, &c.ByCase, &c.CaseCorrect, &c.NotActionable,
			&c.NoDeviation, &c.NoImpact, &c.Expected, &c.Other, &c.NoReason,
			&c.TriageN, &c.TriageReal, &c.QueueN, &c.QueueCorrect,
			&c.MedianLabelSec, &c.Labellers, &c.WorstSvcWrong, &c.WorstSvc); err != nil {
			return nil, err
		}
		out = append(out, FinishMLDetectorScore(r.Detector, c))
	}
	return out, rows.Err()
}

// mlScoreCounts — sorgunun ham sayıları (UInt64 → Scan; FinishMLDetectorScore
// int64'e çevirir).
type mlScoreCounts struct {
	Produced, Linked, LinkedShortAuto, N, Correct, Wrong uint64
	ByVote, ByCase, CaseCorrect, NotActionable           uint64
	NoDeviation, NoImpact, Expected, Other, NoReason     uint64
	TriageN, TriageReal, QueueN, QueueCorrect            uint64
	MedianLabelSec                                       float64
	Labellers, WorstSvcWrong                             uint64
	WorstSvc                                             string
}

// FinishMLDetectorScore — SAF: ham sayılar → karne satırı (oranlar, Wilson,
// yetersiz / muaf işaretleri). Eski alanlar (Events / Samples / Hits /
// HitRate) AYNI tanımla doldurulur: samples = kurala girebilen n, hits =
// doğru — iki ayrı isabet tanımı yok.
func FinishMLDetectorScore(det string, c mlScoreCounts) MLDetectorScore {
	i := func(v uint64) int64 { return int64(v) }
	r := MLDetectorScore{
		Detector: det, Signal: MLDetectorSignal(det),
		Events: i(c.Produced), Samples: i(c.N), Hits: i(c.Correct), HitRate: MLScorecardRate(i(c.Correct), i(c.N)),
		Linked: i(c.Linked), Wrong: i(c.Wrong),
		BySource:      MLLabelSources{Vote: i(c.ByVote), Case: i(c.ByCase), CaseCorrect: i(c.CaseCorrect)},
		NotActionable: i(c.NotActionable),
		NotAnomalyReasons: MLNotAnomalyReasons{NoDeviation: i(c.NoDeviation), NoImpact: i(c.NoImpact),
			Expected: i(c.Expected), Other: i(c.Other), None: i(c.NoReason)},
		Triage:    MLTriageCounts{N: i(c.TriageN), Real: i(c.TriageReal), Eligible: false},
		Queue:     MLQueueCounts{N: i(c.QueueN), Correct: i(c.QueueCorrect)},
		Labellers: i(c.Labellers),
		Exempt:    MLPrecisionExempt(det),
	}
	if c.N > 0 {
		r.Precision = float64(c.Correct) / float64(c.N)
		r.WilsonLo, r.WilsonHi, _ = WilsonBounds(i(c.Correct), i(c.N))
	}
	if c.Produced > 0 {
		r.Coverage = float64(c.N) / float64(c.Produced)
	}
	if c.Linked > 0 {
		r.ShortAutoShare = float64(c.LinkedShortAuto) / float64(c.Linked)
		r.LinkedShortAuto = i(c.LinkedShortAuto)
	}
	if c.ByVote+c.NotActionable > 0 && !math.IsNaN(c.MedianLabelSec) {
		r.MedianLabelSec = c.MedianLabelSec
	}
	if c.Wrong > 0 && c.WorstSvcWrong > 0 {
		r.WorstSvc, r.WorstSvcShare = c.WorstSvc, float64(c.WorstSvcWrong)/float64(c.Wrong)
	}
	if c.QueueN > 0 {
		r.Queue.WilsonLo, r.Queue.WilsonHi, _ = WilsonBounds(i(c.QueueCorrect), i(c.QueueN))
	}
	r.Insufficient = c.N < MLPrecisionMinLabels || r.Coverage < MLPrecisionMinCoverage
	return r
}

// MLLabelSources — kurala girebilen etiketlerin kaynağa göre dökümü.
type MLLabelSources struct {
	Vote        int64 `json:"vote"`        // olay oyu (doğru + yanlış; eyleme değmez hariç)
	Case        int64 `json:"case"`        // onaylı vaka (oy yoksa)
	CaseCorrect int64 `json:"caseCorrect"` // vaka category != noise
}

// MLNotAnomalyReasons — "Sorun değil" oylarının nedene göre dökümü.
type MLNotAnomalyReasons struct {
	NoDeviation int64 `json:"noDeviation"` // yanlış sayılır
	NoImpact    int64 `json:"noImpact"`    // eyleme değmez (n'e girmez)
	Expected    int64 `json:"expected"`    // eyleme değmez (n'e girmez)
	Other       int64 `json:"other"`       // yanlış sayılır
	None        int64 `json:"none"`        // nedensiz: yanlış sayılır
}

// MLTriageCounts — Problem triyajından türeyen sayılar (D4: KURAL DIŞI;
// yalnız oy ve vaka yokken). Eligible her zaman false.
type MLTriageCounts struct {
	N        int64 `json:"n"`
	Real     int64 `json:"real"`
	Eligible bool  `json:"eligible"`
}

// MLQueueCounts — haftalık etiket kuyruğundan verilen oylar (B.3/2; yanlılık
// önlemi: organik etiketlerden ayrı okunur).
type MLQueueCounts struct {
	N        int64   `json:"n"`
	Correct  int64   `json:"correct"`
	WilsonLo float64 `json:"wilsonLo"`
	WilsonHi float64 `json:"wilsonHi"`
}
