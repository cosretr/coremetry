package chstore

// cosre_lookup.go — v0.10.2449 (operatör raporu 2026-11-22: prod CoSRE
// soruları çoğunlukla TRACE ve ENDPOINT hakkında — "bu servisin url'i ne",
// "bu domain hangi podu çözüyor", "timeout hangi poddan", "hangi mq'da
// uzamış", "reponun linki"). mcptools endpoint_lookup / trace_where /
// repo_lookup araçlarının okumaları. SAF telemetri (state okumaz/yazmaz)
// spans + spanmetrics_1m okumaları telemetryReadConn'dan; argocd_sync_events
// (state) ana bağlantıdan.
//
// Sınırlar (clickhouse-schema adım 4; prod ~27k span/s, ~2.3 milyar/gün):
//   - spanmetrics_1m: zaman sınırlı + kind IN (server, consumer) + LIMIT +
//     max_execution_time 8. Ham span YOK.
//   - ham spans: her okumada zaman sınırı (pencere tavanı Go'da kırpılır ve
//     cevapta beyan edilir), PREWHERE ucuz LowCardinality kolonlar, LIMIT,
//     max_execution_time, ve max_rows_to_read + read_overflow_mode='break'
//     (servis öneki olmayan domain araması için tavan; kesilen okuma "kısmi"
//     sayılır — yokluğun kanıtı değil).
//   - servis biliniyorsa service_name = ? / IN ? (ORDER BY ön eki).

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Pencere tavanları — ham okumalar bu kadar geriye bakar (sona yaslanır).
const (
	LookupObservedScopedMax   = 30 * time.Minute // servis(ler) biliniyor
	LookupObservedUnscopedMax = 5 * time.Minute  // yalnız domain / URL parçası
	LookupWhereMax            = 6 * time.Hour    // servis kapsamlı trace_where
	LookupRepoAttrsMax        = time.Hour        // kaynak öznitelikleri
	LookupRoutesMax           = 24 * time.Hour   // spanmetrics_1m rota kataloğu
	// LookupUnscopedRowsMax — servis öneki olmayan domain aramasının okuma
	// tavanı (satır). ~27k span/s.de 5 dk ≈ 8M satır; tavan bunun üstünde
	// → prod pencereyi tam okur; yük artarsa tavan keser ("kısmi" notu).
	LookupUnscopedRowsMax = 10_000_000
	// LookupScopedRowsMax — servis kapsamlı ham okumaların tavanı.
	LookupScopedRowsMax = 50_000_000
)

// ClampLookupWindow — SAF: pencereyi sona yaslayarak max'a kırpar.
func ClampLookupWindow(from, to time.Time, maxWin time.Duration) (time.Time, time.Time, bool) {
	if to.Sub(from) > maxWin {
		return to.Add(-maxWin), to, true
	}
	return from, to, false
}

// EndpointRouteRow — bir servisin giriş (server/consumer) rotası, spanmetrics_1m.
type EndpointRouteRow struct {
	Service string  `json:"service"`
	Name    string  `json:"operation"`
	Route   string  `json:"route,omitempty"`
	Calls   uint64  `json:"calls"`
	Errors  uint64  `json:"errors"`
	P95Ms   float64 `json:"p95_ms"`
}

// endpointRoutesSQL — SAF: SQL + bind sırası (services boşsa kosul yok).
func endpointRoutesSQL(services []string, frag string) (string, []any) {
	var b strings.Builder
	b.WriteString(`SELECT service_name, name, http_route,
       countMerge(calls_state) AS calls,
       countIfMerge(error_state) AS errors,
       arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p95_ms
FROM spanmetrics_1m
WHERE time_bucket >= ? AND time_bucket < ? AND kind IN ('server', 'consumer')`)
	var args []any
	if len(services) > 0 {
		b.WriteString(` AND service_name IN ?`)
		args = append(args, services)
	}
	if frag != "" {
		b.WriteString(` AND (positionCaseInsensitive(http_route, ?) > 0 OR positionCaseInsensitive(name, ?) > 0)`)
		args = append(args, frag, frag)
	}
	b.WriteString(`
GROUP BY service_name, name, http_route
ORDER BY calls DESC
LIMIT ?
SETTINGS max_execution_time = 8`)
	return b.String(), args
}

// EndpointEntryRoutes — servis(ler)in giriş rotaları (http.route / span adı),
// spanmetrics_1m'den. frag rota ya da operasyon adında harf duyarsız aranır.
// services ve frag ikisi boşsa okuma koşmaz.
func (s *Store) EndpointEntryRoutes(ctx context.Context, services []string, frag string, from, to time.Time, limit int) ([]EndpointRouteRow, error) {
	if len(services) == 0 && frag == "" {
		return []EndpointRouteRow{}, nil
	}
	from, to, _ = ClampLookupWindow(from, to, LookupRoutesMax)
	sql, extra := endpointRoutesSQL(services, frag)
	args := append([]any{from, to}, extra...)
	args = append(args, limit)
	rows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EndpointRouteRow{}
	for rows.Next() {
		var r EndpointRouteRow
		if err := rows.Scan(&r.Service, &r.Name, &r.Route, &r.Calls, &r.Errors, &r.P95Ms); err != nil {
			return nil, err
		}
		r.P95Ms = finiteOr0(r.P95Ms)
		out = append(out, r)
	}
	return out, rows.Err()
}

// lookupHostSQL — giriş span'inin istemcinin kullandığı host'u, öznitelik
// olarak SAKLANDIĞI gibi (semconv yeni → eski: server.address, http.host,
// url.full / http.url host'u, net.host.name).
const lookupHostSQL = `coalesce(` +
	`nullIf(attr_values[indexOf(attr_keys, 'server.address')], ''), ` +
	`nullIf(attr_values[indexOf(attr_keys, 'http.host')], ''), ` +
	`nullIf(domain(attr_values[indexOf(attr_keys, 'url.full')]), ''), ` +
	`nullIf(domain(attr_values[indexOf(attr_keys, 'http.url')]), ''), ` +
	`nullIf(attr_values[indexOf(attr_keys, 'net.host.name')], ''), '')`

// EndpointObservedQuery — gözlenen URL host/yol okumasının girdisi.
type EndpointObservedQuery struct {
	Services []string // boş = servis öneki yok (yalnız Host/URL ile)
	Host     string   // host parçası (harf duyarsız)
	URL      string   // yol / URL parçası (harf duyarsız; host+yol+rota üstünde)
	From, To time.Time
	Limit    int
}

// EndpointObservedRow — (servis, cluster, host, fiil, yol grubu) satırı.
type EndpointObservedRow struct {
	Service string   `json:"service"`
	Cluster string   `json:"cluster,omitempty"`
	Host    string   `json:"host,omitempty"`
	Method  string   `json:"method,omitempty"`
	Path    string   `json:"path,omitempty"`
	Route   string   `json:"route,omitempty"`
	Calls   uint64   `json:"spans"`
	Errors  uint64   `json:"error_spans"`
	P95Ms   float64  `json:"p95_ms"`
	Pods    []string `json:"pods,omitempty"` // span'lerin host_name'i (≤5, kanıt)
	Sample  string   `json:"sample_url,omitempty"`
}

// EndpointObservedResult — satırlar + okumanın gerçekten kapsadığı pencere.
type EndpointObservedResult struct {
	Rows    []EndpointObservedRow
	From    time.Time
	To      time.Time
	Clamped bool
	MaxRows int // okuma tavanı (satır); tavana dayanmış olabilir
}

// endpointObservedSQL — SAF.
func endpointObservedSQL(clusterExpr string, q EndpointObservedQuery) (string, []any) {
	var where strings.Builder
	var args []any
	if len(q.Services) > 0 {
		where.WriteString(` AND service_name IN ?`)
		args = append(args, q.Services)
	}
	if q.Host != "" {
		where.WriteString(` AND positionCaseInsensitive(` + lookupHostSQL + `, ?) > 0`)
		args = append(args, q.Host)
	}
	if q.URL != "" {
		where.WriteString(` AND (positionCaseInsensitive(` + externalPathURLSQL + `, ?) > 0 OR positionCaseInsensitive(http_route, ?) > 0)`)
		args = append(args, q.URL, q.URL)
	}
	maxRows := LookupScopedRowsMax
	if len(q.Services) == 0 {
		maxRows = LookupUnscopedRowsMax
	}
	sql := `SELECT service_name, cl, host, http_method, ` +
		`if(length(g) > 1, substring(g, 1, length(g) - 1), '/') AS path, any(http_route) AS route,
       count() AS calls, countIf(status_code = 'error') AS errors,
       quantileTDigest(0.95)(duration) / 1e6 AS p95_ms,
       groupUniqArray(5)(host_name) AS pods, any(u) AS sample
FROM (
  SELECT service_name, ` + clusterExpr + ` AS cl, ` + lookupHostSQL + ` AS host, http_method, http_route,
         ` + externalPathURLSQL + ` AS u,
         ` + externalPathNormalizeSQL("if(position(u, '://') > 0, path(u), cutQueryStringAndFragment(u))") + ` AS g,
         status_code, duration, host_name
  FROM spans
  PREWHERE kind IN ('server', 'consumer')
  WHERE time >= ? AND time < ?` + where.String() + `
)
GROUP BY service_name, cl, host, http_method, path
ORDER BY calls DESC
LIMIT ?
SETTINGS max_execution_time = 8, max_rows_to_read = ` + fmt.Sprint(maxRows) + `, read_overflow_mode = 'break'`
	return sql, args
}

// EndpointObserved — giriş span'lerinde GÖZLENEN host / yol / fiil + hangi
// pod'lar (host_name) ve cluster. Servis yoksa Host ya da URL zorunlu.
func (s *Store) EndpointObserved(ctx context.Context, q EndpointObservedQuery) (EndpointObservedResult, error) {
	res := EndpointObservedResult{Rows: []EndpointObservedRow{}}
	if len(q.Services) == 0 && q.Host == "" && q.URL == "" {
		return res, nil
	}
	winMax := LookupObservedScopedMax
	res.MaxRows = LookupScopedRowsMax
	if len(q.Services) == 0 {
		winMax, res.MaxRows = LookupObservedUnscopedMax, LookupUnscopedRowsMax
	}
	res.From, res.To, res.Clamped = ClampLookupWindow(q.From, q.To, winMax)
	if q.Limit <= 0 {
		q.Limit = 20
	}
	sql, extra := endpointObservedSQL(s.clusterExpr(), q)
	args := append([]any{res.From, res.To}, extra...)
	args = append(args, q.Limit)
	rows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var r EndpointObservedRow
		if err := rows.Scan(&r.Service, &r.Cluster, &r.Host, &r.Method, &r.Path, &r.Route, &r.Calls, &r.Errors, &r.P95Ms, &r.Pods, &r.Sample); err != nil {
			return res, err
		}
		r.P95Ms = finiteOr0(r.P95Ms)
		r.Path = normalizeExternalPath(r.Path)
		res.Rows = append(res.Rows, r)
	}
	return res, rows.Err()
}

// lookupPeerSQL — span'in KARŞI tarafı: mesaj hedefi (kuyruk/topic), db,
// peer.service, server.address / net.peer.name. Boş = iç span.
const lookupPeerSQL = `coalesce(` +
	`nullIf(attr_values[indexOf(attr_keys, 'messaging.destination.name')], ''), ` +
	`nullIf(attr_values[indexOf(attr_keys, 'messaging.destination')], ''), ` +
	`nullIf(if(db_system != '', concat(db_system, ':', attr_values[indexOf(attr_keys, 'db.name')]), ''), ''), ` +
	`nullIf(peer_service, ''), ` +
	`nullIf(attr_values[indexOf(attr_keys, 'server.address')], ''), ` +
	`nullIf(attr_values[indexOf(attr_keys, 'net.peer.name')], ''), '')`

// lookupTimeoutSQL — zaman aşımı işareti (durum mesajı / exception tipi).
const lookupTimeoutSQL = `(status_code = 'error' AND (positionCaseInsensitive(status_msg, 'timeout') > 0 ` +
	`OR positionCaseInsensitive(status_msg, 'timed out') > 0 ` +
	`OR positionCaseInsensitive(attr_values[indexOf(attr_keys, 'exception.type')], 'timeout') > 0))`

// Trace-where kipleri.
const (
	WhereModeErrors    = "errors"    // hatalı span'ler (zaman aşımı işaretli)
	WhereModeSlow      = "slow"      // süre ≥ eşik
	WhereModeMessaging = "messaging" // mesaj hedefi başına gecikme
)

// WhereQuery — servis + pencere kipindeki trace_where okuması.
type WhereQuery struct {
	Service  string
	Mode     string
	Peer     string // karşı taraf parçası (ör. "osb"); harf duyarsız
	MinMs    int64  // slow kipinde eşik
	From, To time.Time
	Limit    int
}

// WhereRow — (pod, cluster, span adı, tür, karşı taraf) grubu.
type WhereRow struct {
	Pod      string  `json:"pod,omitempty"`
	Cluster  string  `json:"cluster,omitempty"`
	Span     string  `json:"span"`
	Kind     string  `json:"kind"`
	Peer     string  `json:"peer,omitempty"`
	Spans    uint64  `json:"spans"`
	Errors   uint64  `json:"error_spans"`
	Timeouts uint64  `json:"timeout_spans"`
	P50Ms    float64 `json:"p50_ms"`
	P95Ms    float64 `json:"p95_ms"`
	MaxMs    float64 `json:"max_ms"`
	Status   string  `json:"status_message,omitempty"`
	TraceID  string  `json:"sample_trace_id,omitempty"`
}

// WhereResult — satırlar + kapsanan pencere.
type WhereResult struct {
	Rows    []WhereRow
	From    time.Time
	To      time.Time
	Clamped bool
}

// whereSQL — SAF.
func whereSQL(clusterExpr string, q WhereQuery) (string, []any) {
	cond := `status_code = 'error'`
	group := `host_name, cl, name, kind, peer`
	switch q.Mode {
	case WhereModeSlow:
		cond = `duration >= ?`
	case WhereModeMessaging:
		cond = `msg_system != ''`
		group = `cl, name, kind, peer`
	}
	podSel, orderBy := `host_name`, `spans`
	if q.Mode != WhereModeErrors {
		orderBy = `p95_ms`
	}
	if q.Mode == WhereModeMessaging {
		podSel = `arrayStringConcat(groupUniqArray(3)(host_name), ',')`
	}
	var args []any
	if q.Mode == WhereModeSlow {
		args = append(args, q.MinMs*1_000_000)
	}
	peerCond := ""
	if q.Peer != "" {
		peerCond = ` AND (positionCaseInsensitive(` + lookupPeerSQL + `, ?) > 0 OR positionCaseInsensitive(name, ?) > 0 OR positionCaseInsensitive(status_msg, ?) > 0)`
		args = append(args, q.Peer, q.Peer, q.Peer)
	}
	sql := `SELECT ` + podSel + ` AS pod, cl, name, kind, peer,
       count() AS spans, countIf(status_code = 'error') AS errors, countIf(to_flag) AS timeouts,
       quantileTDigest(0.5)(duration) / 1e6 AS p50_ms, quantileTDigest(0.95)(duration) / 1e6 AS p95_ms,
       max(duration) / 1e6 AS max_ms,
       substring(anyIf(status_msg, status_msg != ''), 1, 160) AS msg,
       argMax(trace_id, duration) AS sample
FROM (
  SELECT host_name, ` + clusterExpr + ` AS cl, name, kind, ` + lookupPeerSQL + ` AS peer,
         status_code, status_msg, duration, trace_id, ` + lookupTimeoutSQL + ` AS to_flag
  FROM spans
  PREWHERE service_name = ? AND ` + cond + `
  WHERE time >= ? AND time < ?` + peerCond + `
)
GROUP BY ` + group + `
ORDER BY ` + orderBy + ` DESC
LIMIT ?
SETTINGS max_execution_time = 8, max_rows_to_read = ` + fmt.Sprint(LookupScopedRowsMax) + `, read_overflow_mode = 'break'`
	// bind sırası: [slow eşiği], servis, from, to, [peer×3], limit — PREWHERE
	// metinde WHERE'den önce, slow eşiği cond içinde servisin ARDINDA.
	return sql, args
}

// TraceWhere — servisin pencere içi hatalı / yavaş / mesajlaşma span'leri
// pod · cluster · karşı taraf kırılımıyla.
func (s *Store) TraceWhere(ctx context.Context, q WhereQuery) (WhereResult, error) {
	res := WhereResult{Rows: []WhereRow{}}
	if strings.TrimSpace(q.Service) == "" {
		return res, fmt.Errorf("service required")
	}
	res.From, res.To, res.Clamped = ClampLookupWindow(q.From, q.To, LookupWhereMax)
	if q.Limit <= 0 {
		q.Limit = 20
	}
	sql, extra := whereSQL(s.clusterExpr(), q)
	args := whereArgs(q, extra, res.From, res.To)
	rows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var r WhereRow
		if err := rows.Scan(&r.Pod, &r.Cluster, &r.Span, &r.Kind, &r.Peer, &r.Spans, &r.Errors, &r.Timeouts,
			&r.P50Ms, &r.P95Ms, &r.MaxMs, &r.Status, &r.TraceID); err != nil {
			return res, err
		}
		r.P50Ms, r.P95Ms, r.MaxMs = finiteOr0(r.P50Ms), finiteOr0(r.P95Ms), finiteOr0(r.MaxMs)
		res.Rows = append(res.Rows, r)
	}
	return res, rows.Err()
}

// whereArgs — SAF: bind sırası metindeki ? sırasıyla aynı (PREWHERE servis,
// [eşik], WHERE from/to, [peer×3], LIMIT).
func whereArgs(q WhereQuery, extra []any, from, to time.Time) []any {
	args := []any{q.Service}
	rest := extra
	if q.Mode == WhereModeSlow {
		args = append(args, extra[0])
		rest = extra[1:]
	}
	args = append(args, from, to)
	args = append(args, rest...)
	return append(args, q.Limit)
}

// RepoAttrKeys — kaynak deposunu taşıyabilen resource öznitelikleri (OTel
// vcs.* semconv + yaygın kurum anahtarları), öncelik sırasıyla.
var RepoAttrKeys = []string{
	"vcs.repository.url.full", "vcs.repository.url", "vcs.repository.name",
	"git.repository_url", "git.repository.url", "git.remote.url",
	"service.repository", "scm.repository.url", "repository.url",
	"vcs.ref.head.name", "vcs.ref.head.revision", "git.commit.sha", "git.branch",
}

// ServiceRepoAttrs — servisin span resource'larında RepoAttrKeys'ten DOLU
// olanlar (ilk eşleşen span; yoksa pencerenin servis span'leri taranır,
// satır tavanlı). Boş harita = bu pencerede öznitelik yok.
func (s *Store) ServiceRepoAttrs(ctx context.Context, service string, from, to time.Time) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(service) == "" {
		return out, nil
	}
	from, to, _ = ClampLookupWindow(from, to, LookupRepoAttrsMax)
	rows, err := s.telemetryReadConn().Query(ctx, `SELECT res_keys, res_values FROM spans
PREWHERE service_name = ?
WHERE time >= ? AND time < ? AND hasAny(res_keys, ?)
LIMIT 1
SETTINGS max_execution_time = 5, max_rows_to_read = `+fmt.Sprint(LookupScopedRowsMax)+`, read_overflow_mode = 'break'`,
		service, from, to, RepoAttrKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	want := map[string]bool{}
	for _, k := range RepoAttrKeys {
		want[k] = true
	}
	for rows.Next() {
		var ks, vs []string
		if err := rows.Scan(&ks, &vs); err != nil {
			return nil, err
		}
		for i, k := range ks {
			if want[k] && i < len(vs) && strings.TrimSpace(vs[i]) != "" {
				out[k] = vs[i]
			}
		}
	}
	return out, rows.Err()
}

// ArgoAppRepoURLs — Argo CD uygulamalarının son senkronundaki kaynak depo
// (argocd_sync_events.repo_url; yalnız Rollouts v2 / Argo bağlıysa dolu).
// Tablo yoksa hata döner — çağıran "kaynak yok" sayar.
func (s *Store) ArgoAppRepoURLs(ctx context.Context, apps []string) (map[string]string, error) {
	out := map[string]string{}
	if len(apps) == 0 {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, `SELECT app_name, argMax(repo_url, op_started_at)
FROM argocd_sync_events FINAL
WHERE app_name IN ? AND repo_url != '' AND op_started_at >= now() - INTERVAL 90 DAY
GROUP BY app_name
LIMIT 20
SETTINGS max_execution_time = 3`, apps)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a, u string
		if err := rows.Scan(&a, &u); err != nil {
			return nil, err
		}
		out[a] = u
	}
	return out, rows.Err()
}
