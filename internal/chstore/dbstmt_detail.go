package chstore

// DB statement DETAIL reads — v0.8.378, Stage-2 slice D2
// (docs/pages-enhancement-audit.md §2 Faz D). The /slow-queries catalog row
// drills into ONE statement class, keyed by the persistent identity D1
// introduced (spans.db_stmt_hash / db_statement_summary_5m.stmt_hash —
// dbstmt.go parity contract). Four readers, all statement-scoped:
//
//   • GetDBStmtSummary — window totals for the drawer header strip
//     (calls / errors / avg / p95 / p99 / max) + the bucket sample the
//     display form is re-derived from. Also the compare=prior source:
//     the API layer runs it twice (shifted window) and fills the Prior*
//     fields — the Endpoints v0.5.404 / M1 v0.8.364 merge pattern.
//   • GetDBStmtTrend — 5m-grain latency/error TREND series (coarsened
//     to ≤ ~288 points on wide windows so the payload stays bounded).
//   • GetDBStmtCallers — per-service breakdown; service_name is a real
//     MV dimension, so "who issues this query" is one GROUP BY away.
//   • DBStmtExemplars — the TRUE exemplar pivot the audit wanted:
//     spans.db_stmt_hash = ? over a time-bounded raw read resolves the
//     slowest (+ worst-error) trace_id for the exact statement class —
//     replacing the lossy `db.statement LIKE prefix%` deep-link.
//
// MV-first: the three aggregate readers touch ONLY db_statement_summary_5m
// (bare name — the MV is a highVolumeTables member, so cluster mode reads
// its Distributed wrapper). Raw spans appear ONLY in the exemplar lookup,
// which returns two ids, not an aggregate. Same alias rule as
// slowQueriesGlobalMVSQL throughout: with the optional db_system/db_name
// WHERE filters present, a SELECT alias named after either column would
// make ClickHouse resolve the WHERE identifier to the alias and reject the
// query with code 184 (the v0.8.362 incident class) — pinned by
// dbstmt_detail_test.go.

import (
	"context"
	"log"
	"strings"
	"time"
)

// DBStmtDetailQuery bundles the statement-detail inputs. Hash is the
// REQUIRED identity (already parsed from the API's decimal string —
// 0 is the "no statement" sentinel and never reaches here); DBSystem /
// DBName optionally narrow a hash class that shows up under more than
// one engine/database (the MV keeps them as dimensions; the catalog
// folds across them).
type DBStmtDetailQuery struct {
	Hash     uint64
	DBSystem string
	DBName   string
	From, To time.Time
}

// dbStmtDetailWhere builds the shared MV predicate. The window start
// snaps DOWN to the MV's 5-minute grid (the getSlowQueriesGlobalMV /
// GetDBTrends trick) so a rolling window covers whole buckets instead of
// half-clipping the first one; the end is DIŞLAYICI.
//
// v0.9.1167 — üst sınır `<=` idi (v0.9.823/1156 sınıfı). Üç okuma da bu
// yüklemi paylaşıyor (summary/trend/callers), dolayısıyla tek satır üç
// yüzeyi birden kirletiyordu. En sinsi zararı ?compare=prior'da veriyordu:
// API önceki pencereyi (From-dur, From) olarak kuruyor ve `<= From` ile
// ŞİMDİKİ pencerenin İLK kovası önceki pencereye de giriyordu — iki
// pencere bir kova örtüşüyor, yani delta'nın "önce" tarafı "şimdi"nin
// trafiğini sayıyordu. Örtüşme testi: dbstmt_bucket_bound_test.go.
func dbStmtDetailWhere(q DBStmtDetailQuery) whereClause {
	return dbStmtDetailWhereGrain(q, 5*time.Minute)
}

// dbStmtDetailWhereGrain — aynı yüklem, alt sınır kaynağın kova ızgarasına
// hizalı (v0.10.1525: 1 dk trend kaynağı db_statement_summary_1m için 1 dk).
func dbStmtDetailWhereGrain(q DBStmtDetailQuery, grain time.Duration) whereClause {
	var wc whereClause
	wc.add("stmt_hash = ?", q.Hash)
	wc.add("time_bucket >= ?", q.From.Truncate(grain))
	wc.add("time_bucket < ?", q.To)
	if q.DBSystem != "" {
		wc.add("db_system = ?", q.DBSystem)
	}
	if q.DBName != "" {
		wc.add("db_name = ?", q.DBName)
	}
	return wc
}

// DBStmtSummary is the drawer-header rollup for one statement class.
// Prior* fields are additive — filled by the API layer's second
// (shifted-window) read when ?compare=prior, zero otherwise (the
// EndpointRow Prior* convention: prior-absent renders as "NEW").
type DBStmtSummary struct {
	// One real bucket sample — the API re-derives the normalized display
	// form via NormalizeDBStatement (hash-consistent by construction).
	SampleStatement string  `json:"sampleStatement"`
	DBSystem        string  `json:"dbSystem"`
	DBName          string  `json:"dbName"`
	Calls           uint64  `json:"calls"`
	Errors          uint64  `json:"errors"`
	TotalMs         float64 `json:"totalMs"`
	AvgMs           float64 `json:"avgMs"`
	P50Ms           float64 `json:"p50Ms"` // v0.10.1958 — medyan
	P95Ms           float64 `json:"p95Ms"`
	P99Ms           float64 `json:"p99Ms"`
	MaxMs           float64 `json:"maxMs"`
	PriorCalls      uint64  `json:"priorCalls,omitempty"`
	PriorErrors     uint64  `json:"priorErrors,omitempty"`
	PriorAvgMs      float64 `json:"priorAvgMs,omitempty"`
	PriorP95Ms      float64 `json:"priorP95Ms,omitempty"`
	PriorP50Ms      float64 `json:"priorP50Ms,omitempty"`
}

// dbStmtSummarySQL — pure builder, pinned by dbstmt_detail_test.go.
// db_sys / db_nm aliases, NEVER `AS db_system` / `AS db_name` (code-184
// alias-shadow class, v0.8.362).
func dbStmtSummarySQL(where string) string {
	return `
		SELECT
			anyMerge(sample_stmt_state)                 AS sample_stmt,
			any(db_system)                              AS db_sys,
			any(db_name)                                AS db_nm,
			countMerge(span_count_state)                AS cnt,
			countIfMerge(error_count_state)             AS err_cnt,
			sumMerge(duration_sum_state) / 1e6          AS total_ms,
			arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 1) / 1e6 AS p50_ms,
			arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms,
			arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99_ms,
			maxMerge(duration_max_state) / 1e6          AS max_ms
		FROM db_statement_summary_5m ` + where + `
		LIMIT 1
		SETTINGS max_execution_time = 10`
}

// GetDBStmtSummary returns the statement's window rollup, or (nil, nil)
// when the class has no rows in the window (a GROUP-BY-less aggregate
// always yields one row — cnt==0 IS the "not found" signal). safeF on
// every merged float: TDigest merges over edge-case states can yield NaN
// and encoding/json rejects NaN (the v0.5.301 500-class).
func (s *Store) GetDBStmtSummary(ctx context.Context, q DBStmtDetailQuery) (*DBStmtSummary, error) {
	wc := dbStmtDetailWhere(q)
	row := s.telemetryReadConn().QueryRow(ctx, dbStmtSummarySQL(wc.sql()), wc.args...)
	var out DBStmtSummary
	var totalMs, p50, p95, p99, maxMs float64
	if err := row.Scan(&out.SampleStatement, &out.DBSystem, &out.DBName,
		&out.Calls, &out.Errors, &totalMs, &p50, &p95, &p99, &maxMs); err != nil {
		return nil, err
	}
	if out.Calls == 0 {
		return nil, nil
	}
	out.TotalMs = safeF(&totalMs)
	out.AvgMs = out.TotalMs / float64(out.Calls)
	out.P50Ms = safeF(&p50)
	out.P95Ms = safeF(&p95)
	out.P99Ms = safeF(&p99)
	out.MaxMs = safeF(&maxMs)
	return &out, nil
}

// dbStmtSummaryFineSQL — v0.10.1957: özetin sayı + kuantil yarısı 1 dk tablodan
// (db_statement_summary_1m; örnek metin ve max kolonu bu tabloda YOK, onlar 5m'den kalır).
func dbStmtSummaryFineSQL(where string) string {
	return `
		SELECT
			countMerge(span_count_state)                AS cnt,
			countIfMerge(error_count_state)             AS err_cnt,
			sumMerge(duration_sum_state) / 1e6          AS total_ms,
			arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 1) / 1e6 AS p50_ms,
			arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms,
			arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99_ms
		FROM ` + dbStmtTrendTableFine + ` ` + where + `
		LIMIT 1
		SETTINGS max_execution_time = 10`
}

// mergeFineSummary — SAF: 1 dk tablonun sayıları/kuantilleri 5m özetin üstüne yazılır;
// örnek metin ve max 5m'den kalır. cnt == 0 da yazılır (hassas pencerede çağrı yok).
func mergeFineSummary(out *DBStmtSummary, cnt, errs uint64, totalMs, p50, p95, p99 float64) {
	out.Calls, out.Errors = cnt, errs
	out.TotalMs = safeF(&totalMs)
	out.AvgMs = 0
	if cnt > 0 {
		out.AvgMs = out.TotalMs / float64(cnt)
	}
	out.P50Ms = safeF(&p50)
	out.P95Ms = safeF(&p95)
	out.P99Ms = safeF(&p99)
}

// GetDBStmtSummaryGrain — v0.10.1957 (operatör: "db summary 1 dakika yok muydu"): kısa ve
// 1 dk tablonun kapsadığı pencerede (g = DBStmtTrendGrain, trendle aynı seçim) sayı ve
// kuantiller 1 dk ızgarasından — 5 dk kova kenarı pencereyi kaydırmaz. 1 dk okuması hata
// verirse 5m değerleri kalır (yumuşak).
func (s *Store) GetDBStmtSummaryGrain(ctx context.Context, q DBStmtDetailQuery, g DBStmtTrendGrain) (*DBStmtSummary, error) {
	out, err := s.GetDBStmtSummary(ctx, q)
	if err != nil || out == nil || g.Table != dbStmtTrendTableFine {
		return out, err
	}
	wc := dbStmtDetailWhereGrain(q, time.Minute)
	var cnt, errs uint64
	var totalMs, p50, p95, p99 float64
	if ferr := s.telemetryReadConn().QueryRow(ctx, dbStmtSummaryFineSQL(wc.sql()), wc.args...).Scan(&cnt, &errs, &totalMs, &p50, &p95, &p99); ferr != nil {
		return out, nil
	}
	mergeFineSummary(out, cnt, errs, totalMs, p50, p95, p99)
	return out, nil
}

// DBStmtTrendPoint is one trend bucket: bucket-start ns + RED-shaped
// values for the statement class.
type DBStmtTrendPoint struct {
	TsNs   int64   `json:"tsNs"`
	Calls  uint64  `json:"calls"`
	Errors uint64  `json:"errors"`
	AvgMs  float64 `json:"avgMs"`
	P95Ms  float64 `json:"p95Ms"`
	P50Ms  float64 `json:"p50Ms"` // v0.10.1958 — medyan
}

// dbStmtTrendMaxPoints bounds the trend payload — ~288 points covers a
// 24h window at the MV's native 5m grain; wider windows coarsen instead
// of shipping tens of thousands of buckets (90d at 5m = 25 920 rows).
const dbStmtTrendMaxPoints = 288

// dbStmtTrendBucketSec picks the trend bucket width: the MV's native
// 300s up to 24h, then the smallest 5m multiple keeping the series at
// ≤ dbStmtTrendMaxPoints. Pure — table-pinned (v0.8.378).
func dbStmtTrendBucketSec(from, to time.Time) int64 {
	windowSec := to.Unix() - from.Unix()
	if windowSec <= 0 {
		return 300
	}
	b := (windowSec + dbStmtTrendMaxPoints - 1) / dbStmtTrendMaxPoints
	b = ((b + 299) / 300) * 300 // round UP to the 5m MV grain
	if b < 300 {
		b = 300
	}
	return b
}

// ── v0.10.1525 — 1 dk trend kaynağı ───────────────────────────────────────
//
// Operatör (prod, 2026-10-24): Statement detayının Calls/s · Errors · P95
// grafikleri 5 dk kovaydı — "düzgün/doğru değil". db_summary_1m'in
// (v0.10.1095) /database detayına yaptığını burada db_statement_summary_1m
// yapar: kısa pencerede (≤ 6 sa = 360 kova, LIMIT 400'ün altında) ve 1m
// tablo pencere başını kapsıyorsa 1 dk kova; aksi hâlde bugünkü 5m yolu
// (24 sa'e kadar 5 dk, üstü kabalaşır). Yalnız TREND taşınır: özet,
// çağıranlar ve exemplar 5m'de (1m ikiz o state'leri taşımaz).

// dbStmtTrendFineMaxWindow — 1 dk kova bu pencereye kadar (dahil).
// Servis grafiklerinin metricAutoStep merdiveni de 6 sa'e kadar 60 sn verir.
const dbStmtTrendFineMaxWindow = 6 * time.Hour

// Trend kaynakları — SQL'e YALNIZ bu sabitlerden girer.
const (
	dbStmtTrendTableFine   = "db_statement_summary_1m"
	dbStmtTrendTableCoarse = "db_statement_summary_5m"
)

// DBStmtTrendGrain — trend kaynağı: tablo, tablonun kova ızgarası (sn) ve
// çizilecek kova genişliği (sn; kaba yolda ızgaranın katı).
type DBStmtTrendGrain struct {
	Table     string
	GrainSec  int64
	BucketSec int64
}

// dbStmtTrendGrainFor — SAF seçici (tablo testi dbstmt_trend_grain_test.go).
//
//	pencere ≤ 6 sa VE 1m tablo pencere başını kapsıyor → 1m tablo, 60 sn kova
//	aksi hâlde → 5m tablo, dbStmtTrendBucketSec (≥ 300 sn)
//
// fineCovers=false: tablo yok (kolon kapısı / ertelenmiş DDL), boş ya da
// ilk kovası pencere başından sonra (geriye dolmaz, TTL 7 gün). Hepsinde
// SESSİZCE 5m — eksik bir 1m serisi çizmektense doğru bir 5m serisi.
func dbStmtTrendGrainFor(from, to time.Time, fineCovers bool) DBStmtTrendGrain {
	if w := to.Sub(from); w > 0 && w <= dbStmtTrendFineMaxWindow && fineCovers {
		return DBStmtTrendGrain{Table: dbStmtTrendTableFine, GrainSec: 60, BucketSec: 60}
	}
	return DBStmtTrendGrain{Table: dbStmtTrendTableCoarse, GrainSec: 300, BucketSec: dbStmtTrendBucketSec(from, to)}
}

// DBStmtTrendGrain — pencere için kaynağı seçer; kapsama probu yalnız kısa
// pencerede ve kolon kapısı açıkken koşar (60 sn önbellekli, hata →
// kapsamıyor). Önbellek anahtarı seçimi taşır (api/dbstmt_detail.go):
// 1m tablo yeni dolduğunda eski 5m yükü 1m anahtarında servis edilmez.
func (s *Store) DBStmtTrendGrain(ctx context.Context, from, to time.Time) DBStmtTrendGrain {
	fine := false
	if w := to.Sub(from); s != nil && s.hasDBStmtHashCol && w > 0 && w <= dbStmtTrendFineMaxWindow {
		fine = s.sourceCovers(ctx, priorSrcDBStmtSummary1m, from.Truncate(time.Minute))
	}
	return dbStmtTrendGrainFor(from, to, fine)
}

// dbStmtTrendSQL — pure builder. Buckets via intDiv over the MV's
// time_bucket (the GetEndpointsMV sparkline shape); the two intDiv args
// (bucket start unix, bucket width) bind BEFORE the WHERE args. LIMIT
// carries headroom over dbStmtTrendMaxPoints for rounding at the edges.
// v0.10.1525 — kaynak tablo parametreli (bilinmeyen ad → 5m); 1m yolu ≤ 361 kova.
func dbStmtTrendSQL(where string) string { return dbStmtTrendSQLFrom(dbStmtTrendTableCoarse, where) }

func dbStmtTrendSQLFrom(table, where string) string {
	if table != dbStmtTrendTableFine {
		table = dbStmtTrendTableCoarse
	}
	return `
		SELECT
			intDiv(toUnixTimestamp(time_bucket) - ?, ?)     AS b,
			countMerge(span_count_state)                    AS cnt,
			countIfMerge(error_count_state)                 AS err_cnt,
			sumMerge(duration_sum_state) / 1e6              AS total_ms,
			arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms,
			arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 1) / 1e6 AS p50_ms
		FROM ` + table + ` ` + where + `
		GROUP BY b
		ORDER BY b
		LIMIT 400
		SETTINGS max_execution_time = 10`
}

// GetDBStmtTrend returns the statement's trend series — one MV scan,
// GROUP BY coarsened bucket. Buckets with no data are absent (sparse);
// the returned bucketSec lets the frontend densify against the window
// exactly (gaps render as zeros) without re-deriving the coarsening.
//
// v0.10.1525 — g kaynağı seçer (DBStmtTrendGrain); ızgara başı g.GrainSec'e
// hizalı, böylece istemci aynı ızgarayı yanıttaki trendGrainSec ile kurar.
func (s *Store) GetDBStmtTrend(ctx context.Context, q DBStmtDetailQuery, g DBStmtTrendGrain) (points []DBStmtTrendPoint, bucketSec int64, err error) {
	if g.Table != dbStmtTrendTableFine || g.GrainSec <= 0 || g.BucketSec <= 0 {
		g = dbStmtTrendGrainFor(q.From, q.To, false)
	}
	grain := time.Duration(g.GrainSec) * time.Second
	bucketStart := q.From.Truncate(grain)
	bucketSec = g.BucketSec
	wc := dbStmtDetailWhereGrain(q, grain)
	args := append([]any{bucketStart.Unix(), bucketSec}, wc.args...)
	rows, err := s.telemetryReadConn().Query(ctx, dbStmtTrendSQLFrom(g.Table, wc.sql()), args...)
	if err != nil {
		return nil, bucketSec, err
	}
	defer rows.Close()
	out := []DBStmtTrendPoint{}
	for rows.Next() {
		var b int64
		var p DBStmtTrendPoint
		var totalMs, p95, p50 float64
		if err := rows.Scan(&b, &p.Calls, &p.Errors, &totalMs, &p95, &p50); err != nil {
			return nil, bucketSec, err
		}
		p.TsNs = (bucketStart.Unix() + b*bucketSec) * int64(time.Second)
		if p.Calls > 0 {
			p.AvgMs = safeF(&totalMs) / float64(p.Calls)
		}
		p.P95Ms = safeF(&p95)
		p.P50Ms = safeF(&p50)
		out = append(out, p)
	}
	return out, bucketSec, rows.Err()
}

// DBStmtCaller is one service's slice of the statement class. Prior*
// additive, same contract as DBStmtSummary.
type DBStmtCaller struct {
	Service     string  `json:"service"`
	Calls       uint64  `json:"calls"`
	Errors      uint64  `json:"errors"`
	AvgMs       float64 `json:"avgMs"`
	P95Ms       float64 `json:"p95Ms"`
	TotalMs     float64 `json:"totalMs"`
	PriorCalls  uint64  `json:"priorCalls,omitempty"`
	PriorErrors uint64  `json:"priorErrors,omitempty"`
	PriorAvgMs  float64 `json:"priorAvgMs,omitempty"`
	PriorP95Ms  float64 `json:"priorP95Ms,omitempty"`
}

// dbStmtCallersSQL — pure builder. Ordered by total wall-clock time
// (the catalog's own "worth optimising" metric) so the top caller is
// the service actually burning DB time, not just the chattiest.
func dbStmtCallersSQL(where string) string {
	return `
		SELECT
			service_name,
			countMerge(span_count_state)                AS cnt,
			countIfMerge(error_count_state)             AS err_cnt,
			sumMerge(duration_sum_state) / 1e6          AS total_ms,
			arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms
		FROM db_statement_summary_5m ` + where + `
		GROUP BY service_name
		ORDER BY total_ms DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// GetDBStmtCallers returns the per-service breakdown of the statement
// class, top `limit` by total time.
func (s *Store) GetDBStmtCallers(ctx context.Context, q DBStmtDetailQuery, limit int) ([]DBStmtCaller, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	wc := dbStmtDetailWhere(q)
	args := append(append([]any{}, wc.args...), limit)
	rows, err := s.telemetryReadConn().Query(ctx, dbStmtCallersSQL(wc.sql()), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DBStmtCaller{}
	for rows.Next() {
		var c DBStmtCaller
		var totalMs, p95 float64
		if err := rows.Scan(&c.Service, &c.Calls, &c.Errors, &totalMs, &p95); err != nil {
			return nil, err
		}
		c.TotalMs = safeF(&totalMs)
		if c.Calls > 0 {
			c.AvgMs = c.TotalMs / float64(c.Calls)
		}
		c.P95Ms = safeF(&p95)
		out = append(out, c)
	}
	return out, rows.Err()
}

// dbStmtExemplarWhere builds the raw-spans exemplar predicate: the
// (…, time) PK time bound leads, then the stored-column identity match.
// db_name is deliberately NOT filtered here — on spans it lives in the
// attr arrays (the MV derives it at insert), and an attr lookup per row
// would defeat the bounded-scan posture; hash + optional db_system is
// already exact for the class.
func dbStmtExemplarWhere(q DBStmtDetailQuery, errorOnly bool) whereClause {
	var wc whereClause
	wc.add("time >= ?", q.From)
	wc.add("time <= ?", q.To)
	wc.add("db_stmt_hash = ?", q.Hash)
	if q.DBSystem != "" {
		wc.add("db_system = ?", q.DBSystem)
	}
	if errorOnly {
		wc.add("status_code = 'error'")
	}
	return wc
}

// dbStmtExemplarSQL — pure builder for the true-exemplar raw read:
// slowest matching span's trace_id (ORDER BY duration DESC LIMIT 1),
// time-bounded, 10s capped.
func dbStmtExemplarSQL(where, shardSetting string) string {
	return `
		SELECT trace_id
		FROM spans ` + where + `
		ORDER BY duration DESC
		LIMIT 1
		SETTINGS max_execution_time = 10,
		         ` + shardSetting
}

// DBStmtExemplars resolves the statement class's slow + error exemplar
// trace_ids — the TRUE pivot D2 replaces the lossy `db.statement LIKE
// prefix%` deep-link with (v0.8.378). Two bounded raw-spans point reads
// keyed on the stored db_stmt_hash column; empty ids mean "no exemplar"
// (no traffic / all-healthy window) and surface as a soft-missing
// section, never an error. Guarded on the D1 boot probe: installs where
// the column couldn't land (external Distributed, cluster_name unset)
// skip the raw read entirely.
func (s *Store) DBStmtExemplars(ctx context.Context, q DBStmtDetailQuery) (slowTraceID, errorTraceID string, err error) {
	if !s.hasDBStmtHashCol {
		return "", "", nil
	}
	// v0.9.1097 (0.5 Alternatif-B) — MV-önce: statement class'ı servisler
	// arası olduğundan ham tarama service_name predicate'i koyamıyor (PK
	// prefix yok; audit §5.3: 1B span/gün'de 24h ≈ 22s). MV iki soruyu
	// tek küçük sorguda cevaplar. Boş cevap ham fallback'e DÜŞER: upgrade
	// öncesi kovalar state taşımıyor olabilir — "MV'de yok"u "hiç yok"
	// sanmak yanlış negatif üretirdi.
	if s.hasDBStmtExemplarCols {
		if slow, errTid, mvErr := s.dbStmtExemplarsFromMV(ctx, q); mvErr == nil && (slow != "" || errTid != "") {
			return slow, errTid, nil
		} else if mvErr != nil {
			log.Printf("[chstore] dbstmt exemplar MV okuması düştü, ham fallback: %v", mvErr)
		}
	}
	// Ham fallback pencere KELEPÇELİ (v0.9.1097): exemplar süstür, trend
	// 90 güne dek pencere taşıyabilir — kelepçesiz ham tarama audit'in
	// timeout-"veri yok" sınıfının ta kendisiydi.
	q.From, q.To = clampExemplarWindow(q.From, q.To)
	for _, target := range []struct {
		errorOnly bool
		dst       *string
	}{
		{false, &slowTraceID},
		{true, &errorTraceID},
	} {
		if ctx.Err() != nil {
			return slowTraceID, errorTraceID, ctx.Err()
		}
		wc := dbStmtExemplarWhere(q, target.errorOnly)
		row := s.telemetryReadConn().QueryRow(ctx, dbStmtExemplarSQL(wc.sql(), s.shardSkipSetting()), wc.args...)
		// v0.8.564 — no-rows is the legitimate "no exemplar in window"
		// and stays silent; every OTHER scan error used to be swallowed
		// by a bare `_ =`, which at prod scale meant a 10s
		// max_execution_time timeout's ONLY symptom was a drawer with no
		// exemplar link. Still soft (the exemplar is decoration, the
		// drawer's stats must ship), but the failure is now visible.
		if err := row.Scan(target.dst); err != nil && !isNoRows(err) {
			log.Printf("[chstore] dbstmt exemplar read (errorOnly=%v) failed — drawer renders without the link: %v", target.errorOnly, err)
		}
	}
	return slowTraceID, errorTraceID, nil
}

// dbStmtExemplarMVSQL — MV okuma metni (saf, shape-testli). Finalizer
// ÇİFTLERİ sözleşmenin parçası: argMaxState↔argMaxMerge,
// argMaxIfState↔argMaxIfMerge (endpoints_detail emsali; yanlış eş
// sessizce boş döner).
//
// v0.9.1167 — üst sınır `<=`den `<`e döndü. Burada zarar sayı değil
// KİMLİK: argMax pencere dışındaki bir kovadan gelen trace'i "en yavaş"
// seçebiliyordu, yani çekmecedeki derin link operatörün baktığı pencereye
// AİT OLMAYAN bir trace'i açıyordu — istatistikler bir pencereyi,
// örnek trace başka bir pencereyi anlatıyordu.
const dbStmtExemplarMVSQL = `
		SELECT argMaxMerge(slow_exemplar_state)    AS slow_tid,
		       argMaxIfMerge(error_exemplar_state) AS err_tid
		FROM db_statement_summary_5m
		WHERE stmt_hash = ?
		  AND time_bucket >= toStartOfInterval(?, INTERVAL 5 MINUTE)
		  AND time_bucket < ?
		SETTINGS max_execution_time = 10`

func (s *Store) dbStmtExemplarsFromMV(ctx context.Context, q DBStmtDetailQuery) (slowTraceID, errorTraceID string, err error) {
	sql := dbStmtExemplarMVSQL
	args := []any{q.Hash, q.From, q.To}
	if q.DBSystem != "" {
		sql = strings.Replace(sql, "WHERE stmt_hash = ?", "WHERE db_system = ? AND stmt_hash = ?", 1)
		args = append([]any{q.DBSystem}, args...)
	}
	if err := s.telemetryReadConn().QueryRow(ctx, sql, args...).Scan(&slowTraceID, &errorTraceID); err != nil && !isNoRows(err) {
		return "", "", err
	}
	return slowTraceID, errorTraceID, nil
}

// clampExemplarWindow — ham exemplar taramasının pencere tavanı (saf,
// tablo testli): 24 saatten geniş istekte From, To-24h'a çekilir.
// Trend/istatistik pencereleri ETKİLENMEZ — yalnız ham exemplar okuması.
const exemplarRawWindowMax = 24 * time.Hour

func clampExemplarWindow(from, to time.Time) (time.Time, time.Time) {
	if to.Sub(from) > exemplarRawWindowMax {
		return to.Add(-exemplarRawWindowMax), to
	}
	return from, to
}

// ── v0.10.1960 — taban çizgisi (normal aralık) ──────────────────────────────
//
// Operatör (Dynatrace benzeri süre gösterimi, öneri 2): sorgunun süresinin arkasında
// "normal aralık". Kaynak 5m MV (90 gün TTL): pencereden ÖNCEKİ 7 günün AYNI SAAT
// dilimleri; her 5 dk dilim için 7 günün birleşik TDigest'inden p50 ve p95. Dilim
// kimliği pencere başına göre gün-içi uzaklık (offsetSec, 300'ün katı). Pencere
// 24 saati aşarsa bant yok; az çağrılı dilim (< dbStmtBaselineMinCalls) atlanır.

// DBStmtBaselinePoint — pencere başından offsetSec saniye sonraki 5 dk dilimin normal aralığı.
type DBStmtBaselinePoint struct {
	OffsetSec int64   `json:"offsetSec"`
	P50Ms     float64 `json:"p50Ms"`
	P95Ms     float64 `json:"p95Ms"`
}

const (
	dbStmtBaselineDays      = 7
	dbStmtBaselineMaxWindow = 24 * time.Hour
	dbStmtBaselineMinCalls  = 20
	dbStmtBaselineStepSec   = 300
)

// dbStmtBaselineSQL — saf kurucu. İlk iki argüman: pencere başı (unix, 5 dk hizalı) —
// gün-içi uzaklığı GROUP BY için; WHERE argümanları ardından.
func dbStmtBaselineSQL(where string) string {
	return `
		SELECT
			intDiv(modulo(toUnixTimestamp(time_bucket) - ? + 604800, 86400), 300) * 300 AS off,
			countMerge(span_count_state)                                              AS cnt,
			arrayElement(quantilesTDigestMerge(0.5, 0.95)(duration_q_state), 1) / 1e6 AS p50_ms,
			arrayElement(quantilesTDigestMerge(0.5, 0.95)(duration_q_state), 2) / 1e6 AS p95_ms
		FROM db_statement_summary_5m ` + where + `
		GROUP BY off
		ORDER BY off
		LIMIT 300
		SETTINGS max_execution_time = 10`
}

// dbStmtBaselineWhere — saf: önceki 7 gün, pencereyle aynı gün-içi dilimler.
func dbStmtBaselineWhere(q DBStmtDetailQuery, from5 time.Time, windowSec int64) whereClause {
	var wc whereClause
	wc.add("stmt_hash = ?", q.Hash)
	wc.add("time_bucket >= ?", from5.Add(-dbStmtBaselineDays*24*time.Hour))
	wc.add("time_bucket < ?", from5)
	if q.DBSystem != "" {
		wc.add("db_system = ?", q.DBSystem)
	}
	if q.DBName != "" {
		wc.add("db_name = ?", q.DBName)
	}
	wc.add("modulo(toUnixTimestamp(time_bucket) - ? + 604800, 86400) < ?", from5.Unix(), windowSec)
	return wc
}

// GetDBStmtBaseline — pencere ≤ 24 sa ise normal aralık dilimleri; aksi nil.
func (s *Store) GetDBStmtBaseline(ctx context.Context, q DBStmtDetailQuery) ([]DBStmtBaselinePoint, error) {
	w := q.To.Sub(q.From)
	if w <= 0 || w > dbStmtBaselineMaxWindow {
		return nil, nil
	}
	from5 := q.From.Truncate(dbStmtBaselineStepSec * time.Second)
	wc := dbStmtBaselineWhere(q, from5, int64(q.To.Sub(from5)/time.Second))
	args := append([]any{from5.Unix()}, wc.args...)
	rows, err := s.telemetryReadConn().Query(ctx, dbStmtBaselineSQL(wc.sql()), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DBStmtBaselinePoint
	for rows.Next() {
		var off int64
		var cnt uint64
		var p50, p95 float64
		if err := rows.Scan(&off, &cnt, &p50, &p95); err != nil {
			return nil, err
		}
		if cnt < dbStmtBaselineMinCalls {
			continue
		}
		out = append(out, DBStmtBaselinePoint{OffsetSec: off, P50Ms: safeF(&p50), P95Ms: safeF(&p95)})
	}
	return out, rows.Err()
}
