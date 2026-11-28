package chstore

// problem_source_breakdown.go — v0.10.2478 (operatör: "Çok fazla problem
// üretiyor, gerçek problemler olsun isterim"). Problems KAYNAK DÖKÜMÜ: hangi
// kural ailesi kaç Problem açıyor. SALT OKUMA — hiçbir dedektör, kapı,
// öncelik ya da bildirim davranışı bu dosyaya bağlı değil; eşikleri körlemesine
// sıkmak yerine gürültünün KAYNAĞINI tek ekranda göstermek için.
//
// Aile sınıflaması: classifyProblem'in (problem_standard.go) rule_id önek
// sözlüğünün OPERATÖRÜN TANIDIĞI kaynak düzeyine indirgenmiş hali. Neden
// classifyProblem'in kendisi DEĞİL: onun `family` alanı sinyal türüdür
// (error/latency/traffic) — "ML servis anomalisi" ile "kullanıcı kuralı"
// aynı error kovasına düşer ve soru ("hangi KURAL") cevapsız kalır. Önekler
// aynı sabitlerden okunur (RuleExt*, RuleDB*, MLGroupRulePrefix, …) ki iki
// sözlük ayrışmasın.
//
// TEK TABLO, İKİ DERLEME: problemSourceRules hem saf Go sınıflayıcısını
// (ProblemSourceFamily, tablo testi) hem de SQL multiIf ifadesini
// (problemSourceFamilySQL) üretir. Aile toplama CH'de yapılır (medyan/uniq
// Go'da birleştirilemez); ifade aynı tablodan türediği için Go testi SQL'in
// kuralını da pinler. Bilinmeyen → "other" (UI: "diğer"), ham metrik ikinci
// düzeyde görünür — hiçbir şey gizlenmez.
//
// Sorgu maliyeti: `problems` ORDER BY id (partition yok, problem_frequent.go
// başlığı) — started_at alt sınırı satırları BUDAMAZ, yalnız süzer; FINAL tüm
// tabloyu okur. Sınırlar: started_at aralığı (≤ 30 gün), LIMIT
// ProblemSourceBreakdownLimit, max_execution_time 10, max_rows_to_read 20M +
// read_overflow_mode 'break' (tablo bundan büyükse okuma KESİLİR → Partial).
// Tek tarama; GROUPING SETS dört kümeyi aynı geçişte üretir. Uç 5 dk
// serveCached arkasında.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// Kaynak aileleri (API anahtarı; etiket FE i18n'de).
const (
	SrcFamilyIncident       = "incident"
	SrcFamilyMLOOM          = "ml_oom"
	SrcFamilyMLLag          = "ml_lag"
	SrcFamilyMLGroup        = "ml_group"
	SrcFamilyMLSvc          = "ml_svc"
	SrcFamilyMLSubject      = "ml_subject"
	SrcFamilyMLSingle       = "ml_single"
	SrcFamilyExternal       = "external"
	SrcFamilyAnomalyCluster = "anomaly_cluster"
	SrcFamilyAnomaly        = "anomaly"
	SrcFamilySvcSlowdown    = "svc_slowdown"
	SrcFamilyException      = "exception"
	SrcFamilyDB             = "db"
	SrcFamilyRuntime        = "runtime"
	SrcFamilyMonitor        = "monitor"
	SrcFamilySelf           = "self"
	SrcFamilySLO            = "slo"
	SrcFamilyBuiltin        = "builtin"
	SrcFamilyUserRule       = "user_rule"
	SrcFamilyOther          = "other"
)

// srcRule — tablonun bir satırı. Prefix/Exact/UserID'den TAM BİRİ dolu;
// Metrics boş değilse metrik de eşleşmeli (VE).
type srcRule struct {
	Family  string
	Prefix  string
	Exact   string
	UserID  bool     // kullanıcı alarm kuralı kimliği (newID(8) hex ya da UUID)
	Metrics []string // boş = her metrik
}

// userRuleIDPattern — POST /api/alert-rules kimliği newID(8) = 16 hex; elle
// verilmiş UUID de kabul. RE2: Go regexp ve CH match() aynı sözdizimi.
const userRuleIDPattern = `^([0-9a-f]{16}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`

var userRuleIDRe = regexp.MustCompile(userRuleIDPattern)

// problemSourceRules — SIRA ÖNEMLİ (ilk eşleşen kazanır): metrik koşullu ML
// satırları (OOM, lag) önekli ML alt ailelerinden önce; dış kaynak önekleri
// genel `anomaly:` / `anomaly-cluster:`dan önce.
var problemSourceRules = []srcRule{
	{Family: SrcFamilyIncident, Prefix: "incident:"},
	{Family: SrcFamilyMLOOM, Prefix: PromotedAnomalyRulePrefix, Metrics: []string{MLOOMMetric}},
	{Family: SrcFamilyMLLag, Prefix: PromotedAnomalyRulePrefix, Metrics: []string{"ml_group_lag", MLGroupLagPatternMetric}},
	{Family: SrcFamilyMLGroup, Prefix: MLGroupRulePrefix},
	{Family: SrcFamilyMLSvc, Prefix: MLSvcRulePrefix},
	{Family: SrcFamilyMLSubject, Prefix: PromotedAnomalyRulePrefix + "subject:"},
	{Family: SrcFamilyMLSingle, Prefix: PromotedAnomalyRulePrefix},
	{Family: SrcFamilyExternal, Prefix: RuleExtDownPrefix},
	{Family: SrcFamilyExternal, Prefix: RuleExtCapPrefix},
	{Family: SrcFamilyExternal, Prefix: RuleExtClusterPrefix},
	{Family: SrcFamilyExternal, Prefix: RuleExtSeriesPrefix},
	{Family: SrcFamilyAnomalyCluster, Prefix: "anomaly-cluster:"},
	{Family: SrcFamilyAnomaly, Prefix: "anomaly:"},
	{Family: SrcFamilySvcSlowdown, Prefix: RuleSvcSlowdownPrefix},
	{Family: SrcFamilyException, Exact: "exception-storm"},
	{Family: SrcFamilyException, Prefix: "exception:"},
	{Family: SrcFamilyException, Prefix: ExceptionGroupRulePrefix},
	{Family: SrcFamilyDB, Prefix: RuleDBHealthPrefix},
	{Family: SrcFamilyDB, Prefix: RuleDBCapacityPrefix},
	{Family: SrcFamilyDB, Prefix: "db-slow-stmt"},
	{Family: SrcFamilyRuntime, Prefix: "runtime:"},
	{Family: SrcFamilyMonitor, Prefix: "monitor:"},
	{Family: SrcFamilySelf, Prefix: "self-"},
	{Family: SrcFamilySLO, Prefix: "slo:"},
	{Family: SrcFamilyBuiltin, Prefix: "builtin-"},
	{Family: SrcFamilyUserRule, UserID: true},
}

func (r srcRule) match(ruleID, metric string) bool {
	switch {
	case r.Prefix != "":
		if !strings.HasPrefix(ruleID, r.Prefix) {
			return false
		}
	case r.Exact != "":
		if ruleID != r.Exact {
			return false
		}
	case r.UserID:
		if !userRuleIDRe.MatchString(ruleID) {
			return false
		}
	}
	if len(r.Metrics) == 0 {
		return true
	}
	for _, m := range r.Metrics {
		if metric == m {
			return true
		}
	}
	return false
}

// ProblemSourceFamily — SAF: (rule_id, metric) → kaynak ailesi; eşleşmeyen
// "other".
func ProblemSourceFamily(ruleID, metric string) string {
	for _, r := range problemSourceRules {
		if r.match(ruleID, metric) {
			return r.Family
		}
	}
	return SrcFamilyOther
}

// sqlStr — SAF: sabit metni CH tek tırnaklı literaline çevirir.
func sqlStr(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func (r srcRule) sqlCond() string {
	var c string
	switch {
	case r.Prefix != "":
		c = "startsWith(rule_id, " + sqlStr(r.Prefix) + ")"
	case r.Exact != "":
		c = "rule_id = " + sqlStr(r.Exact)
	case r.UserID:
		c = "match(rule_id, " + sqlStr(userRuleIDPattern) + ")"
	}
	if len(r.Metrics) > 0 {
		q := make([]string, len(r.Metrics))
		for i, m := range r.Metrics {
			q[i] = sqlStr(m)
		}
		c += " AND metric IN (" + strings.Join(q, ", ") + ")"
	}
	return c
}

// problemSourceFamilySQL — SAF: problemSourceRules'un multiIf derlemesi
// (ProblemSourceFamily'nin SQL ikizi; aynı sıra, aynı varsayılan).
func problemSourceFamilySQL() string {
	parts := make([]string, 0, 2*len(problemSourceRules)+1)
	for _, r := range problemSourceRules {
		parts = append(parts, r.sqlCond(), sqlStr(r.Family))
	}
	parts = append(parts, sqlStr(SrcFamilyOther))
	return "multiIf(" + strings.Join(parts, ", ") + ")"
}

// Sorgu sınırları.
const (
	ProblemSourceBreakdownLimit   = 100_000
	ProblemSourceBreakdownMaxRead = 20_000_000
	ProblemSourceBreakdownTag     = "problem_source_breakdown"
	// ProblemSourceMetricEmpty — boş metrik değeri; ikinci düzeyde görünür
	// kalsın ve GROUPING SETS kümesi ayırt edilebilsin diye.
	ProblemSourceMetricEmpty = "-"
)

// problemSourceBreakdownSQL — SAF. Tek tarama, dört küme:
//
//	(fam)            aile toplamı
//	(fam, m)         aile × metrik
//	(fam, svc)       aile × servis (ilk 5 servis + gün başına açılış → churn)
//	(bkt)            zaman kovası serisi (fam = '')
//
// Kümede olmayan kolon CH varsayılanını taşır (”/0); m ve svc iç sorguda
// boşsa '-' yapılır, böylece küme kolonların doluluğundan okunur.
// Süre: kapanmışsa resolved_at − started_at, değilse now − started_at.
// Gün = UTC günü (churn: aynı servis+aile bir günde ≥ 3 açılış).
func problemSourceBreakdownSQL(bucketSec int64) string {
	return `
		SELECT
			fam, m, svc, bkt,
			toUInt64(count())                           AS n,
			toUInt64(countIf(is_open))                  AS open_now,
			toUInt64(countIf(sev = 'critical'))         AS sev_critical,
			toUInt64(countIf(sev = 'warning'))          AS sev_warning,
			toFloat64(quantileExact(0.5)(dur_s))        AS median_s,
			toUInt64(countIf(closed AND dur_s < 900))   AS short_n,
			toUInt64(countIf(dur_s >= 3600))            AS long_n,
			toUInt64(uniqExactIf(svc, svc != '-'))      AS services,
			(sumMap([day], [toUInt64(1)])).2            AS day_counts,
			toUInt64(countIf(has_case))                 AS case_n,
			toUInt64(countIf(has_case AND case_ok))     AS case_correct
		FROM (
			SELECT
				` + problemSourceFamilySQL() + ` AS fam,
				if(metric = '', '` + ProblemSourceMetricEmpty + `', toString(metric)) AS m,
				if(service = '', '-', toString(service)) AS svc,
				toString(severity) AS sev,
				intDiv(toUnixTimestamp64Nano(started_at), 1000000000) AS start_s,
				intDiv(start_s, ` + fmt.Sprint(bucketSec) + `) * ` + fmt.Sprint(bucketSec) + ` AS bkt,
				toUInt32(intDiv(start_s, 86400)) AS day,
				status IN ('open', 'acknowledged') AS is_open,
				isNotNull(resolved_at) AS closed,
				greatest(toFloat64(toUnixTimestamp64Nano(coalesce(resolved_at, now64(9))) - toUnixTimestamp64Nano(started_at)) / 1e9, 0) AS dur_s,
				c.source_id != '' AS has_case,
				c.case_cat != 'noise' AS case_ok
			FROM problems AS p FINAL
			LEFT JOIN (` + problemCaseLabelSQL + `) AS c ON c.source_id = p.id
			WHERE started_at >= ? AND started_at < ?
		)
		GROUP BY GROUPING SETS ((fam), (fam, m), (fam, svc), (bkt))
		ORDER BY n DESC
		LIMIT ` + fmt.Sprint(ProblemSourceBreakdownLimit) + `
		SETTINGS max_execution_time = 10, max_rows_to_read = ` + fmt.Sprint(ProblemSourceBreakdownMaxRead) + `, read_overflow_mode = 'break', join_use_nulls = 0`
}

// problemCaseLabelSQL — v0.10.2484 (precision programı B.1, Problem katmanı):
// Problem başına onaylı kök neden vakası (incident_cases status='confirmed',
// kaynak 5). Problem başına TEK satır (en son onaylanan) — birden çok vaka
// Problem satırını çoğaltıp aile sayısını şişirmesin. category='noise' =
// yanlış alarm, diğerleri doğru. ML karnesi (ml_precision.go) aynı ifadeyi
// kullanır. Sınır: servis düzeyi Problem'in kimliği sabittir
// (anomaly-auto:svc:<svc>) → vakayı yalnız servisin son Problem'i taşır.
const problemCaseLabelSQL = `SELECT source_id, argMax(toString(category), confirmed_at) AS case_cat
		    FROM incident_cases FINAL
		    WHERE source = 'problem' AND status = 'confirmed'
		    GROUP BY source_id`

// ProblemSourceAggRow — GROUPING SETS çıktısının bir satırı (ham; birleştirme
// api tarafında, saf).
type ProblemSourceAggRow struct {
	Family    string
	Metric    string
	Service   string
	Bucket    int64 // unix sn (yalnız seri kümesinde dolu)
	N         uint64
	OpenNow   uint64
	Critical  uint64
	Warning   uint64
	MedianSec float64
	Short15   uint64 // < 15 dk'da kapanan
	Long1h    uint64 // ≥ 1 sa süren (açık olanlar dahil)
	Services  uint64
	DayCounts []uint64 // gün başına açılış (yalnız aile × servis kümesinde anlamlı)
	// v0.10.2484 — onaylı vakası olan / vakası doğru (category != noise) Problem.
	CaseN       uint64
	CaseCorrect uint64
}

// Set — satırın hangi GROUPING SETS kümesinden geldiği.
func (r ProblemSourceAggRow) Set() string {
	switch {
	case r.Family == "":
		return "series"
	case r.Metric != "":
		return "metric"
	case r.Service != "":
		return "service"
	}
	return "family"
}

// ProblemSourceAgg — okuma sonucu.
type ProblemSourceAgg struct {
	Rows     []ProblemSourceAggRow
	RowsRead int64
	Capped   bool // LIMIT'e değdi
	Partial  bool // max_rows_to_read'e değdi (okuma kesildi)
}

// ProblemSourceBreakdown — [from, to) aralığında açılan Problem'lerin kaynak
// dökümü (tek sınırlı sorgu).
func (s *Store) ProblemSourceBreakdown(ctx context.Context, from, to time.Time, bucketSec int64) (ProblemSourceAgg, error) {
	var out ProblemSourceAgg
	if bucketSec <= 0 {
		bucketSec = 86400
	}
	var scanned atomic.Int64
	qctx := clickhouse.Context(WithQueryTag(ctx, ProblemSourceBreakdownTag),
		clickhouse.WithProgress(func(p *clickhouse.Progress) { scanned.Add(int64(p.Rows)) }))
	rows, err := s.conn.Query(qctx, problemSourceBreakdownSQL(bucketSec), chDateTime64Arg(from), chDateTime64Arg(to))
	if err != nil {
		return out, fmt.Errorf("problem source breakdown: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r ProblemSourceAggRow
		if err := rows.Scan(&r.Family, &r.Metric, &r.Service, &r.Bucket, &r.N, &r.OpenNow, &r.Critical, &r.Warning,
			&r.MedianSec, &r.Short15, &r.Long1h, &r.Services, &r.DayCounts, &r.CaseN, &r.CaseCorrect); err != nil {
			return out, fmt.Errorf("problem source breakdown scan: %w", err)
		}
		out.Rows = append(out.Rows, r)
	}
	out.RowsRead = scanned.Load()
	out.Capped = len(out.Rows) >= ProblemSourceBreakdownLimit
	out.Partial = out.RowsRead >= ProblemSourceBreakdownMaxRead
	return out, rows.Err()
}
