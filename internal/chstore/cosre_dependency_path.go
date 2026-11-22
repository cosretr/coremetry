package chstore

// cosre_dependency_path.go — v0.10.2453 (operatör onayı 2026-11-22): CoSRE
// dependency_path aracının okumaları. "neden yavaş, zincir", "kim çağırıyor /
// etki alanı", "hangi downstream yüzünden" sorularına çok-atlamalı kenar
// yürüyüşü.
//
//	atlama (hop)   topology_edges_5m FINAL (ana bağlantı) — İKİ pencere tek
//	               okumada (şimdiki + taban, sumIf/maxIf), bilinen uç IN ?
//	               (≤20 düğüm), düğüm başına ≤20 kenar (LIMIT n BY), toplam
//	               LIMIT, max_execution_time 5. Ham span YOK.
//	deploy         service_version_5m (telemetryReadConn): servisler IN ?,
//	               first_seen ∈ [from, to) (24 sa geriye bakış, MV kapsamıyorsa
//	               OKUNMAZ — covered=false), LIMIT, max_execution_time 5.
//	örnek trace    TEK sınırlı ham okuma (yalnız "olası kaynak" kenarı): çocuk
//	               uç servis öneki (PREWHERE service_name), ≤15 dk, satır tavanı
//	               + read_overflow_mode='break'; servis→servis kenarında üst
//	               ucun trace kümesi GLOBAL IN alt sorgusuyla (aynı sınırlar).

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Tavanlar.
const (
	DepHopPerNode      = 20               // düğüm başına kenar (trafiğe göre)
	DepHopFrontierMax  = 20               // bir atlamada genişletilen düğüm
	DepPathMaxWindow   = 24 * time.Hour   // şimdiki pencere tavanı
	DepSampleWindowMax = 15 * time.Minute // örnek trace ham okuma penceresi
	depDeploysLimit    = 200
	depSampleSubLimit  = 200_000 // üst ucun trace_id alt kümesi
)

// DepPathCell — bir kenarın bir penceredeki MV değeri (örneklenmiş span).
type DepPathCell struct {
	Calls  uint64  `json:"calls"`
	Errors uint64  `json:"errors"`
	AvgMs  float64 `json:"avg_ms"`
	// P99Ms — 5 dk kovalarının EN KÖTÜSÜ (MV kova-yerel p99; birleşik değil).
	P99Ms float64 `json:"p99_worst_bucket_ms"`
}

// DepHopEdge — ham düğüm kimlikleriyle (öneklerle: db:, queue:, ext:) kenar.
type DepHopEdge struct {
	Parent string
	Child  string
	Kind   string // child'ın node_kind'ı (MV)
	Cur    DepPathCell
	Ref    DepPathCell
}

// DepHopQuery — tek atlama: bilinen uç(lar)dan bir yön.
type DepHopQuery struct {
	Up    bool     // true: child_node IN nodes (çağıranlar); false: parent_service IN nodes
	Nodes []string // ≤ DepHopFrontierMax
	Env   string   // "" = tümü; doluysa bilinen ucun env'i ∈ (env, '')
	Cur   PeriodWindow
	Ref   PeriodWindow // Ref.To ≤ Cur.From (çağıran garanti eder)
}

// DependencyHopSQL — SAF. Bind sırası DependencyHopArgs ile aynı.
func DependencyHopSQL(up, env bool) string {
	known, envCol := "parent_service", "parent_env"
	if up {
		known, envCol = "child_node", "child_env"
	}
	envCond := ""
	if env {
		envCond = "\n  AND " + envCol + " IN (?, '')"
	}
	return fmt.Sprintf(`WITH ? AS cur_from
SELECT parent_service, child_node, any(node_kind) AS kind,
       sumIf(calls, time_bucket >= cur_from) AS cc, sumIf(errors, time_bucket >= cur_from) AS ce,
       sumIf(sum_duration_ns, time_bucket >= cur_from) AS cd, maxIf(p99_ms, time_bucket >= cur_from) AS cp,
       sumIf(calls, time_bucket < cur_from) AS rc, sumIf(errors, time_bucket < cur_from) AS re,
       sumIf(sum_duration_ns, time_bucket < cur_from) AS rd, maxIf(p99_ms, time_bucket < cur_from) AS rp
FROM topology_edges_5m FINAL
WHERE ((time_bucket >= ? AND time_bucket < ?) OR (time_bucket >= ? AND time_bucket < ?))
  AND %s IN ?
  AND %s%s
GROUP BY parent_service, child_node
HAVING cc > 0 OR rc > 0
ORDER BY cc DESC, rc DESC, parent_service, child_node
LIMIT %d BY %s
LIMIT ?
SETTINGS max_execution_time = 5`, known, TopoCallEdgeFilterSQL, envCond, DepHopPerNode, known)
}

// DependencyHopArgs — SAF: bind değerleri (pencereler 5 dk kovasına hizalı).
func DependencyHopArgs(q DepHopQuery) []any {
	cf := alignBucketStart(q.Cur.From)
	args := []any{cf, alignBucketStart(q.Ref.From), q.Ref.To, cf, q.Cur.To, q.Nodes}
	if strings.TrimSpace(q.Env) != "" {
		args = append(args, q.Env)
	}
	return append(args, DepHopPerNode*len(q.Nodes))
}

// DependencyHop — bir atlamanın kenarları (iki pencere). nodes boşsa boş.
func (s *Store) DependencyHop(ctx context.Context, q DepHopQuery) ([]DepHopEdge, error) {
	out := []DepHopEdge{}
	if len(q.Nodes) == 0 {
		return out, nil
	}
	if len(q.Nodes) > DepHopFrontierMax {
		q.Nodes = q.Nodes[:DepHopFrontierMax]
	}
	q.Cur.From, q.Cur.To, _ = ClampLookupWindow(q.Cur.From, q.Cur.To, DepPathMaxWindow)
	rows, err := s.conn.Query(ctx, DependencyHopSQL(q.Up, strings.TrimSpace(q.Env) != ""), DependencyHopArgs(q)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e DepHopEdge
		var cd, rd uint64
		if err := rows.Scan(&e.Parent, &e.Child, &e.Kind, &e.Cur.Calls, &e.Cur.Errors, &cd, &e.Cur.P99Ms,
			&e.Ref.Calls, &e.Ref.Errors, &rd, &e.Ref.P99Ms); err != nil {
			return nil, err
		}
		e.Cur.AvgMs, e.Ref.AvgMs = depAvgMs(cd, e.Cur.Calls), depAvgMs(rd, e.Ref.Calls)
		e.Cur.P99Ms, e.Ref.P99Ms = finiteOr0(e.Cur.P99Ms), finiteOr0(e.Ref.P99Ms)
		out = append(out, e)
	}
	return out, rows.Err()
}

func depAvgMs(sumNs, calls uint64) float64 {
	if calls == 0 {
		return 0
	}
	return float64(sumNs) / float64(calls) / 1e6
}

// DepDeploy — pencere içinde İLK görülen sürüm (service_version_5m).
type DepDeploy struct {
	Service     string `json:"service"`
	Version     string `json:"version"`
	FirstSeenNs int64  `json:"-"`
}

// dependencyDeploysSQL — SAF. Bind: services, scanFrom, to, fromNs, toNs, limit.
func dependencyDeploysSQL() string {
	return `SELECT service_name, version, toUnixTimestamp64Nano(minMerge(first_seen_state)) AS first_seen
FROM service_version_5m
WHERE service_name IN ? AND time_bucket >= ? AND time_bucket < ?
GROUP BY service_name, version
HAVING version != '' AND first_seen >= ? AND first_seen < ?
ORDER BY service_name, first_seen
LIMIT ?
SETTINGS max_execution_time = 5, `
}

// DependencyDeploys — servis kümesinde [from, to) içinde doğan sürümler.
// covered=false: MV 24 sa geriye bakışı kapsamıyor → okunmadı (≠ deploy yok).
func (s *Store) DependencyDeploys(ctx context.Context, services []string, from, to time.Time) ([]DepDeploy, bool, error) {
	out := []DepDeploy{}
	if len(services) == 0 {
		return out, true, nil
	}
	scanFrom := alignBucketStart(from.Add(-deployJudgmentLookback))
	if !s.deployMVCovers(ctx, scanFrom) {
		return out, false, nil
	}
	rows, err := s.telemetryReadConn().Query(ctx, dependencyDeploysSQL()+s.shardSkipSetting(),
		services, scanFrom, to, from.UnixNano(), to.UnixNano(), depDeploysLimit)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	for rows.Next() {
		var d DepDeploy
		if err := rows.Scan(&d.Service, &d.Version, &d.FirstSeenNs); err != nil {
			return nil, true, err
		}
		out = append(out, d)
	}
	return out, true, rows.Err()
}

// EdgeSampleQuery — "olası kaynak" kenarından geçen tek örnek trace.
type EdgeSampleQuery struct {
	Parent, Child string // ham düğüm kimlikleri
	From, To      time.Time
	PreferError   bool
}

// edgeSampleSQL — SAF: kenar türüne göre sınırlı ham okuma. ok=false →
// bu kenar türü için güvenli bir okuma yok (ör. node:).
func edgeSampleSQL(q EdgeSampleQuery) (sql string, args []any, ok bool) {
	pk, pname := TopologyNodeIdentity(q.Parent)
	ck, cname := TopologyNodeIdentity(q.Child)
	order := "duration DESC"
	if q.PreferError {
		order = "status_code = 'error' DESC, duration DESC"
	}
	tail := fmt.Sprintf(`
ORDER BY %s
LIMIT 1
SETTINGS max_execution_time = 5, max_rows_to_read = %d, read_overflow_mode = 'break'`, order, LookupScopedRowsMax)
	switch {
	case pk == NodeKindQueue && ck == "":
		// kuyruk → tüketici: tüketicinin consumer span'i, aynı msg_system.
		sys := strings.SplitN(strings.SplitN(pname, ":", 2)[0], "@", 2)[0]
		return `SELECT trace_id FROM spans
PREWHERE service_name = ?
WHERE time >= ? AND time < ? AND kind = 'consumer' AND msg_system = ?` + tail,
			[]any{cname, q.From, q.To, sys}, true
	case pk == "" && ck == "":
		// servis → servis: çocuğun giriş span'i, trace'i üst uçta da var.
		return `SELECT trace_id FROM spans
PREWHERE service_name = ?
WHERE time >= ? AND time < ? AND kind IN ('server', 'consumer')
  AND trace_id GLOBAL IN (SELECT trace_id FROM spans
    PREWHERE service_name = ?
    WHERE time >= ? AND time < ? AND kind IN ('client', 'producer')
    LIMIT ` + fmt.Sprint(depSampleSubLimit) + `)` + tail,
			[]any{cname, q.From, q.To, pname, q.From, q.To}, true
	case pk == "" && ck == NodeKindDB:
		sys := strings.SplitN(cname, "@", 2)[0]
		return `SELECT trace_id FROM spans
PREWHERE service_name = ?
WHERE time >= ? AND time < ? AND db_system = ?` + tail,
			[]any{pname, q.From, q.To, sys}, true
	case pk == "" && ck == NodeKindQueue:
		sys := strings.SplitN(strings.SplitN(cname, ":", 2)[0], "@", 2)[0]
		return `SELECT trace_id FROM spans
PREWHERE service_name = ?
WHERE time >= ? AND time < ? AND kind = 'producer' AND msg_system = ?` + tail,
			[]any{pname, q.From, q.To, sys}, true
	case pk == "" && ck == NodeKindExternal:
		return `SELECT trace_id FROM spans
PREWHERE service_name = ?
WHERE time >= ? AND time < ? AND kind = 'client' AND (peer_service = ? OR has(attr_values, ?))` + tail,
			[]any{pname, q.From, q.To, cname, cname}, true
	}
	return "", nil, false
}

// EdgeSampleTrace — kenardan geçen tek trace id ("" = bulunamadı / okunmadı).
// Pencere sona yaslanıp DepSampleWindowMax'a kırpılır.
func (s *Store) EdgeSampleTrace(ctx context.Context, q EdgeSampleQuery) (string, error) {
	q.From, q.To, _ = ClampLookupWindow(q.From, q.To, DepSampleWindowMax)
	sql, args, ok := edgeSampleSQL(q)
	if !ok {
		return "", nil
	}
	rows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var id string
	if rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
	}
	return id, rows.Err()
}

// DepServiceRED — servisin KENDİ RED'i iki pencerede (service_summary_5m;
// etki alanında "çağıran da kötüleşti mi").
type DepServiceRED struct {
	Service            string
	CurCalls, RefCalls uint64
	CurErrs, RefErrs   uint64
	CurP95Ms, RefP95Ms float64
}

// dependencyServiceREDSQL — SAF. Bind: curFrom(is_cur), services, refFrom, refTo, curFrom, curTo, limit.
func dependencyServiceREDSQL() string {
	return `SELECT service_name, time_bucket >= ? AS is_cur,
       countMerge(span_count_state) AS calls, countIfMerge(error_count_state) AS errs,
       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms
FROM service_summary_5m
WHERE service_name IN ?
  AND ((time_bucket >= ? AND time_bucket < ?) OR (time_bucket >= ? AND time_bucket < ?))
GROUP BY service_name, is_cur
LIMIT ?
SETTINGS max_execution_time = 5, ` + mvQuantileMemSettings
}

// DependencyServiceRED — ≤DepHopFrontierMax servisin iki pencere RED'i.
func (s *Store) DependencyServiceRED(ctx context.Context, services []string, cur, ref PeriodWindow) ([]DepServiceRED, error) {
	out := []DepServiceRED{}
	if len(services) == 0 {
		return out, nil
	}
	if len(services) > DepHopFrontierMax {
		services = services[:DepHopFrontierMax]
	}
	cf := alignBucketStart(cur.From)
	rows, err := s.telemetryReadConn().Query(ctx, dependencyServiceREDSQL(),
		cf, services, alignBucketStart(ref.From), ref.To, cf, cur.To, 2*len(services))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]*DepServiceRED{}
	var order []string
	for rows.Next() {
		var svc string
		var isCur uint8
		var c, e uint64
		var p95 float64
		if err := rows.Scan(&svc, &isCur, &c, &e, &p95); err != nil {
			return nil, err
		}
		r := by[svc]
		if r == nil {
			r = &DepServiceRED{Service: svc}
			by[svc] = r
			order = append(order, svc)
		}
		if isCur == 1 {
			r.CurCalls, r.CurErrs, r.CurP95Ms = c, e, finiteOr0(p95)
		} else {
			r.RefCalls, r.RefErrs, r.RefP95Ms = c, e, finiteOr0(p95)
		}
	}
	for _, svc := range order {
		out = append(out, *by[svc])
	}
	return out, rows.Err()
}
