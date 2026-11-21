package chstore

// compare_baseline.go — CoSRE faz D (görseller, operatör onaylı 2026-11-20
// planı): "ne değişti / neden yavaşladı" aracının (mcptools compare_baseline)
// okumaları. YALNIZ ön-agregatlar — ham spans hiçbir dalda okunmaz (prod
// ~2.3 milyar span/gün; araç her "ne değişti" sorusunda koşabilmeli):
//
//	RED (iki pencere)        service_summary_5m | operation_summary_5m (op verilince)
//	grafik serisi            aynı tablo, kova = stepS (≤ ~60 nokta / seri)
//	endpoint değişimi        operation_summary_5m (servis, iki pencere)
//	bağımlılık gecikmesi     topology_edges_5m FINAL (servisin çağırdıkları)
//	deploy'lar               service_version_5m (MV kapsamıyorsa OKUNMAZ, not düşer)
//	                         + deploy olayları (deployEventEntries)
//
// Her sorgu: zaman sınırlı WHERE (iki pencere), LIMIT, max_execution_time.
// Pencereler kova başına hizalanır (alignBucketStart); üst sınır `<`.
// compare_periods'tan (raw yedekli, giriş span'i popülasyonu) farkı: burada
// popülasyon MV'nin popülasyonu (servisin TÜM span'leri; operation_summary_5m
// tüm türler) — araç bunu notta söyler.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Tavanlar.
const (
	baselineOpsLimit   = 1000 // (name × pencere) satırı; servis başına ayrık ad bunun altında
	baselineEdgesLimit = 400  // (child × pencere) satırı
	baselineSeriesMax  = 260  // iki aralık × ≤ ~120 kova
)

// BaselineQuery — iki pencere + grafik aralığı. Ref ⟂ Cur ve Ref.To ≤ Cur.From
// (yalnız previous / day_before / week_before; çağıran garanti eder).
type BaselineQuery struct {
	Service   string
	Operation string // "" = servis geneli
	Cur, Ref  PeriodWindow
	// ViewFrom..Cur.To — grafiğin şimdiki aralığı. Overlay > 0 → aynı aralık
	// Overlay kadar geride de okunur (dün/geçen hafta kesikli çizgi).
	ViewFrom time.Time
	Overlay  time.Duration
	StepS    int // 300'ün katı, 86400'ü böler
}

// BaselineRED — bir pencerenin RED'i (tüm-pencere tdigest birleşimi).
type BaselineRED struct {
	Calls   uint64  `json:"calls"`
	Errors  uint64  `json:"errors"`
	P50Ms   float64 `json:"p50_ms"`
	P95Ms   float64 `json:"p95_ms"`
	P99Ms   float64 `json:"p99_ms"`
	Buckets uint64  `json:"buckets"`
}

// BaselineCell — endpoint satırının bir penceresi.
type BaselineCell struct {
	Calls  uint64
	Errors uint64
	P95Ms  float64
}

// BaselineOp — endpoint (operation_summary_5m `name`) iki pencerede.
type BaselineOp struct {
	Name     string
	Cur, Ref BaselineCell
}

// BaselineEdge — servisin bir aşağı bağımlılığı iki pencerede.
type BaselineEdge struct {
	Child, Kind        string
	CurCalls, RefCalls uint64
	CurErrs, RefErrs   uint64
	CurAvgMs, RefAvgMs float64
}

// BaselinePoint — grafik kovası (unix ms).
type BaselinePoint struct {
	TimeMs int64
	Calls  uint64
	Errors uint64
	P95Ms  float64
}

// BaselineRaw — okumaların ham sonucu (SAF kurucuya girer).
type BaselineRaw struct {
	Cur, Ref       BaselineRED
	Series         []BaselinePoint // [ViewFrom, Cur.To)
	OverlaySeries  []BaselinePoint // [ViewFrom-Overlay, Cur.To-Overlay); zaman kaydırılmamış
	Ops            []BaselineOp
	OpsTruncated   bool
	Edges          []BaselineEdge
	EdgesTruncated bool
	SQL            BaselineSQL // kanıt çipleri için çalışan metinler
}

// BaselineSQL — kanıt çipinin gösterdiği sorgular (bind'li).
type BaselineSQL struct {
	RED, Series, Ops, Edges string
}

// baselineTable — SAF: okuma tablosu + ek süzgeç.
func baselineTable(op string) (table, extra string) {
	if op != "" {
		return "operation_summary_5m", " AND name = ?"
	}
	return "service_summary_5m", ""
}

// BaselineREDSQL — SAF. Bind: [service, (op), curFrom(is_cur), refFrom, refTo, curFrom, curTo].
func BaselineREDSQL(op string) string {
	table, extra := baselineTable(op)
	return `
		SELECT time_bucket >= ? AS is_cur,
		       countMerge(span_count_state)                                         AS calls,
		       countIfMerge(error_count_state)                                      AS errs,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state) AS q, 1) / 1e6 AS p50_ms,
		       arrayElement(q, 2) / 1e6                                             AS p95_ms,
		       arrayElement(q, 3) / 1e6                                             AS p99_ms,
		       uniqExact(time_bucket)                                               AS buckets
		FROM ` + table + `
		WHERE service_name = ?` + extra + `
		  AND ((time_bucket >= ? AND time_bucket < ?) OR (time_bucket >= ? AND time_bucket < ?))
		GROUP BY is_cur
		LIMIT 2
		SETTINGS max_execution_time = 10, ` + mvQuantileMemSettings
}

// BaselineSeriesSQL — SAF. stepS kod sabiti (biçimlenir, kullanıcı girdisi
// değil). Bind: [service, (op), aFrom, aTo, bFrom, bTo].
func BaselineSeriesSQL(op string, stepS int) string {
	table, extra := baselineTable(op)
	return fmt.Sprintf(`
		SELECT toUnixTimestamp(toStartOfInterval(time_bucket, INTERVAL %d SECOND)) AS b,
		       countMerge(span_count_state)                                         AS calls,
		       countIfMerge(error_count_state)                                      AS errs,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms
		FROM %s
		WHERE service_name = ?%s
		  AND ((time_bucket >= ? AND time_bucket < ?) OR (time_bucket >= ? AND time_bucket < ?))
		GROUP BY b
		ORDER BY b
		LIMIT %d
		SETTINGS max_execution_time = 10, %s`, stepS, table, extra, baselineSeriesMax, mvQuantileMemSettings)
}

// BaselineOpsSQL — SAF. Bind: [curFrom(is_cur), service, refFrom, refTo, curFrom, curTo].
func BaselineOpsSQL() string {
	return fmt.Sprintf(`
		SELECT name, time_bucket >= ? AS is_cur,
		       countMerge(span_count_state)    AS calls,
		       countIfMerge(error_count_state) AS errs,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms
		FROM operation_summary_5m
		WHERE service_name = ?
		  AND ((time_bucket >= ? AND time_bucket < ?) OR (time_bucket >= ? AND time_bucket < ?))
		GROUP BY name, is_cur
		ORDER BY calls DESC
		LIMIT %d
		SETTINGS max_execution_time = 10, %s`, baselineOpsLimit+1, mvQuantileMemSettings)
}

// BaselineEdgesSQL — SAF. Bind: [curFrom(is_cur), refFrom, refTo, curFrom, curTo, service].
func BaselineEdgesSQL() string {
	return fmt.Sprintf(`
		SELECT child_node, any(node_kind) AS kind, time_bucket >= ? AS is_cur,
		       sum(calls)  AS c,
		       sum(errors) AS e,
		       sum(sum_duration_ns) / nullIf(sum(calls), 0) / 1e6 AS avg_ms
		FROM topology_edges_5m FINAL
		WHERE ((time_bucket >= ? AND time_bucket < ?) OR (time_bucket >= ? AND time_bucket < ?))
		  AND parent_service = ?
		  AND %s
		GROUP BY child_node, is_cur
		ORDER BY c DESC
		LIMIT %d
		SETTINGS max_execution_time = 5`, TopoCallEdgeFilterSQL, baselineEdgesLimit+1)
}

// BaselineArgs — SAF: dört sorgunun bind dizileri (çip metni ile okuma AYNI).
func BaselineArgs(q BaselineQuery) (red, series, ops, edges []any) {
	cf, ct := alignBucketStart(q.Cur.From), q.Cur.To
	rf, rt := alignBucketStart(q.Ref.From), q.Ref.To
	red = []any{cf, q.Service}
	if q.Operation != "" {
		red = append(red, q.Operation)
	}
	red = append(red, rf, rt, cf, ct)
	vf := alignBucketStart(q.ViewFrom)
	series = []any{q.Service}
	if q.Operation != "" {
		series = append(series, q.Operation)
	}
	if q.Overlay > 0 {
		series = append(series, vf.Add(-q.Overlay), ct.Add(-q.Overlay), vf, ct)
	} else {
		// İkinci aralık boş (from = to): tek aralık, SQL şekli aynı.
		series = append(series, vf, ct, ct, ct)
	}
	ops = []any{cf, q.Service, rf, rt, cf, ct}
	edges = []any{cf, rf, rt, cf, ct, q.Service}
	return
}

// ReadBaseline — dört MV okuması (sıralı; her biri kendi zaman tavanında).
// RED okunamazsa hata; ikincil okumalar (seri / endpoint / bağımlılık)
// başarısızsa ilgili alan boş kalır ve errs'e yazılır (kısmi sonuç).
func (s *Store) ReadBaseline(ctx context.Context, q BaselineQuery) (BaselineRaw, map[string]error, error) {
	if q.StepS < 300 {
		q.StepS = 300
	}
	var out BaselineRaw
	errs := map[string]error{}
	redA, serA, opsA, edgA := BaselineArgs(q)
	out.SQL = BaselineSQL{RED: BaselineREDSQL(q.Operation), Series: BaselineSeriesSQL(q.Operation, q.StepS), Ops: BaselineOpsSQL(), Edges: BaselineEdgesSQL()}
	conn := s.telemetryReadConn()

	rows, err := conn.Query(ctx, out.SQL.RED, redA...)
	if err != nil {
		return out, errs, fmt.Errorf("baseline red: %w", err)
	}
	for rows.Next() {
		var isCur uint8
		var r BaselineRED
		var p50, p95, p99 *float64
		if err := rows.Scan(&isCur, &r.Calls, &r.Errors, &p50, &p95, &p99, &r.Buckets); err != nil {
			rows.Close()
			return out, errs, fmt.Errorf("baseline red scan: %w", err)
		}
		r.P50Ms, r.P95Ms, r.P99Ms = safeF(p50), safeF(p95), safeF(p99)
		if isCur == 1 {
			out.Cur = r
		} else {
			out.Ref = r
		}
	}
	rows.Close()

	if pts, err := s.readBaselineSeries(ctx, out.SQL.Series, serA); err != nil {
		errs["series"] = err
	} else {
		out.Series, out.OverlaySeries = SplitBaselineSeries(pts, alignBucketStart(q.ViewFrom), q.Overlay)
	}
	if q.Operation == "" {
		if ops, trunc, err := s.readBaselineOps(ctx, out.SQL.Ops, opsA); err != nil {
			errs["ops"] = err
		} else {
			out.Ops, out.OpsTruncated = ops, trunc
		}
	}
	if edges, trunc, err := s.readBaselineEdges(ctx, out.SQL.Edges, edgA); err != nil {
		errs["edges"] = err
	} else {
		out.Edges, out.EdgesTruncated = edges, trunc
	}
	return out, errs, nil
}

// SplitBaselineSeries — SAF: tek sorgunun kovaları → şimdiki aralık + overlay
// aralığı (viewFrom'dan önceki kovalar overlay'dir). Overlay yoksa hepsi şimdiki.
func SplitBaselineSeries(pts []BaselinePoint, viewFrom time.Time, overlay time.Duration) (cur, ov []BaselinePoint) {
	vf := viewFrom.UnixMilli()
	for _, p := range pts {
		if overlay > 0 && p.TimeMs < vf {
			ov = append(ov, p)
		} else {
			cur = append(cur, p)
		}
	}
	return cur, ov
}

func (s *Store) readBaselineSeries(ctx context.Context, sql string, args []any) ([]BaselinePoint, error) {
	rows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BaselinePoint
	for rows.Next() {
		var b uint32
		var p BaselinePoint
		var p95 *float64
		if err := rows.Scan(&b, &p.Calls, &p.Errors, &p95); err != nil {
			return nil, err
		}
		p.TimeMs, p.P95Ms = int64(b)*1000, safeF(p95)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) readBaselineOps(ctx context.Context, sql string, args []any) ([]BaselineOp, bool, error) {
	rows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	byName := map[string]*BaselineOp{}
	n := 0
	for rows.Next() {
		var name string
		var isCur uint8
		var c BaselineCell
		var p95 *float64
		if err := rows.Scan(&name, &isCur, &c.Calls, &c.Errors, &p95); err != nil {
			return nil, false, err
		}
		n++
		if n > baselineOpsLimit {
			break
		}
		c.P95Ms = safeF(p95)
		o := byName[name]
		if o == nil {
			o = &BaselineOp{Name: name}
			byName[name] = o
		}
		if isCur == 1 {
			o.Cur = c
		} else {
			o.Ref = c
		}
	}
	out := make([]BaselineOp, 0, len(byName))
	for _, o := range byName {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, n > baselineOpsLimit, rows.Err()
}

func (s *Store) readBaselineEdges(ctx context.Context, sql string, args []any) ([]BaselineEdge, bool, error) {
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	byChild := map[string]*BaselineEdge{}
	n := 0
	for rows.Next() {
		var child, kind string
		var isCur uint8
		var c, e uint64
		var avg *float64
		if err := rows.Scan(&child, &kind, &isCur, &c, &e, &avg); err != nil {
			return nil, false, err
		}
		n++
		if n > baselineEdgesLimit {
			break
		}
		ed := byChild[child]
		if ed == nil {
			ed = &BaselineEdge{Child: child, Kind: kind}
			byChild[child] = ed
		}
		if isCur == 1 {
			ed.CurCalls, ed.CurErrs, ed.CurAvgMs = c, e, safeF(avg)
		} else {
			ed.RefCalls, ed.RefErrs, ed.RefAvgMs = c, e, safeF(avg)
		}
	}
	out := make([]BaselineEdge, 0, len(byChild))
	for _, ed := range byChild {
		out = append(out, *ed)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Child < out[j].Child })
	return out, n > baselineEdgesLimit, rows.Err()
}

// BaselineDeploys — deploy'lar [from, to): YALNIZ service_version_5m (MV 48 sa
// geriye bakışı kapsamıyorsa okunmaz → covered=false; ham spans yedeği YOK)
// + deploy olay tablosu. Sonuç zamana göre artan.
func (s *Store) BaselineDeploys(ctx context.Context, service string, from, to time.Time) (out []Deploy, covered bool, err error) {
	var inferred []Deploy
	if covered = s.deployMVCovers(ctx, from.Add(-deployLookback)); covered {
		if inferred, err = s.serviceDeploysFromMV(ctx, service, from, to); err != nil {
			return nil, covered, err
		}
	}
	entries := make([]RecentDeployEntry, 0, len(inferred))
	for _, d := range inferred {
		entries = append(entries, RecentDeployEntry{Service: d.Service, Version: d.Version, FirstSeenNs: d.TimeUnixNs, SpanCount: uint64(d.SpanCount), Source: d.Source})
	}
	if evs := s.deployEventEntries(ctx, service, from, to); len(evs) > 0 {
		entries = mergeDeployEntries(entries, evs, 0)
	}
	for _, e := range entries {
		if strings.TrimSpace(e.Version) == "" {
			continue
		}
		out = append(out, Deploy{Service: service, Version: e.Version, TimeUnixNs: e.FirstSeenNs, SpanCount: int(e.SpanCount), Source: e.Source})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TimeUnixNs < out[j].TimeUnixNs })
	return out, covered, nil
}
