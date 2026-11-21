package chstore

// operation_prefetch.go — CoSRE operasyon tespiti Faz 2: eşleşen operasyonun
// sohbet öncesi okumaları. RED MV'den (span adı → operation_summary_5m;
// fonksiyon kodu → rollup_spans_wide, kurulmamışsa ölçülmedi), Oracle
// teknik hataları TRACEID ile (oracle_error_log bloom index'i).

import (
	"context"
	"time"
)

// OpRED — bir pencerenin istek sayısı / hata / p99.
type OpRED struct {
	Calls  uint64  `json:"calls"`
	Errors uint64  `json:"errors"`
	P99Ms  float64 `json:"p99Ms"`
	// P50Ms — v0.10.1327: CoSRE trace drill'inin taban çizgisi ("bu
	// operasyonun 24 sa p50'si 13 ms (×3825)"). Aynı MV, aynı tdigest durumu;
	// omitempty: eski yük (op-prefetch bloğu) bayt bayt aynı kalır.
	P50Ms float64 `json:"p50Ms,omitempty"`
	// P95Ms — v0.10.1374: küme kıyası (RouteREDByEnv / PathREDByEnv) satırı;
	// diğer okumalar doldurmaz, omitempty ile eski yük bayt bayt aynı.
	P95Ms    float64 `json:"p95Ms,omitempty"`
	Measured bool    `json:"measured"`
}

// ErrorRate — SAF: 0..1.
func (r OpRED) ErrorRate() float64 {
	if r.Calls == 0 {
		return 0
	}
	return float64(r.Errors) / float64(r.Calls)
}

// SpanOperationRED — operation_summary_5m'den tek pencere (servis boşsa tüm servisler).
func (s *Store) SpanOperationRED(ctx context.Context, service, name string, from, to time.Time) (OpRED, error) {
	q, args := spanOperationREDQuery(service, name, from, to)
	q = s.weighSQL(q, from, "operation_summary_5m") // v0.10.2435 — sampling-weight Phase 2
	var r OpRED
	var p99, p50 float64
	if err := s.conn.QueryRow(ctx, q, args...).Scan(&r.Calls, &r.Errors, &p99, &p50); err != nil {
		return OpRED{}, err
	}
	if p99 == p99 { // NaN (boş pencere) korunmaz
		r.P99Ms = p99
	}
	if p50 == p50 {
		r.P50Ms = p50
	}
	r.Measured = true
	return r, nil
}

// spanOperationREDQuery — SAF (v0.10.1327): SpanOperationRED'in SQL'i +
// bind'leri. p50 aynı tdigest birleşiminin 1. elemanı (CH aynı aggregate
// ifadesini bir kez hesaplar); MV-first (operation_summary_5m), zaman-sınırlı,
// tek satır (aggregate), max_execution_time'lı. SQL pini testte.
func spanOperationREDQuery(service, name string, from, to time.Time) (string, []any) {
	q := `SELECT countMerge(span_count_state), countMerge(error_count_state),
	             arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6,
	             arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 1) / 1e6
	      FROM operation_summary_5m
	      WHERE name = ? AND time_bucket >= ? AND time_bucket < ?`
	args := []any{name, from, to}
	if service != "" {
		q += " AND service_name = ?"
		args = append(args, service)
	}
	q += " SETTINGS max_execution_time = 5"
	return q, args
}

// RouteRED — v0.10.1343: (servis, http_route) penceresi spanmetrics_1m'den —
// CoSRE endpoint ön-getirmesinde katalogda yalnız `endpoint` (http_route)
// satırı olan hedefin RED'i. Servis boşsa tüm servisler. Scan sırası
// SpanOperationRED ile aynı (calls, errors, p99, p50).
func (s *Store) RouteRED(ctx context.Context, service, route string, from, to time.Time) (OpRED, error) {
	q, args := routeREDQuery(service, route, from, to)
	q = s.weighSQL(q, from, "spanmetrics_1m") // v0.10.2435 — sampling-weight Phase 2
	var r OpRED
	var p99, p50 float64
	if err := s.conn.QueryRow(ctx, q, args...).Scan(&r.Calls, &r.Errors, &p99, &p50); err != nil {
		return OpRED{}, err
	}
	if p99 == p99 { // NaN (boş pencere) korunmaz
		r.P99Ms = p99
	}
	if p50 == p50 {
		r.P50Ms = p50
	}
	r.Measured = true
	return r, nil
}

// routeREDQuery — SAF (v0.10.1343): RouteRED'in SQL'i + bind'leri. MV-first
// (spanmetrics_1m), giriş-noktası kind yüklemi GetEndpointsMV'nin HTTP
// sekmesiyle AYNI (client/producer hariç — aynı route'u çağıran istemci
// span'leri sayılmaz), yarı-açık pencere, tek satır, max_execution_time.
func routeREDQuery(service, route string, from, to time.Time) (string, []any) {
	where := "http_route = ?"
	args := []any{route}
	if service != "" {
		where = "service_name = ? AND http_route = ?"
		args = []any{service, route}
	}
	q := `SELECT countMerge(calls_state), countMerge(error_state),
	             arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state), 4) / 1e6,
	             arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state), 1) / 1e6
	      FROM spanmetrics_1m
	      WHERE ` + where + `
	        AND kind NOT IN ('client', 'producer')
	        AND time_bucket >= ? AND time_bucket < ?
	      SETTINGS max_execution_time = 5`
	return q, append(args, from, to)
}

// FunctionCodeRED — rollup ailesinden fonksiyon kodu toplamı; p99 = kova
// p99'larının en yükseği (yaklaşık, üst sınır). Rollup yoksa Measured=false.
func (s *Store) FunctionCodeRED(ctx context.Context, codes []string, from, to time.Time) (OpRED, error) {
	var out OpRED
	plan, err := PickRollup(RollupQuery{From: from, To: to, MaxDataPoints: 60, Dims: []string{"function_code"}}, time.Now())
	if err != nil {
		return out, nil
	}
	if ready, rerr := s.RollupTableReady(ctx, plan.Table); rerr != nil || !ready {
		return out, nil
	}
	for _, c := range codes {
		series, _, err := s.QueryRollupRED(ctx, plan, RollupSeriesFilter{Function: c}, from, to)
		if err != nil {
			return OpRED{}, err
		}
		for _, se := range series {
			for _, p := range se.Points {
				out.Calls += p.Calls
				out.Errors += p.Errors
				if p.P99Ms > out.P99Ms {
					out.P99Ms = p.P99Ms
				}
			}
		}
	}
	out.Measured = true
	return out, nil
}

// OracleErrorCount — TRACEID birleşiminden kod başına sayı + örnek mesaj.
type OracleErrorCount struct {
	Code    string `json:"code"`
	Count   uint64 `json:"count"`
	Sample  string `json:"sample"`
	Channel string `json:"channel,omitempty"`
}

// OracleErrorsForTraces — verilen trace'lere bağlı Oracle hata satırları,
// kod başına (error_code boşsa external_code). ≤10 kod.
func (s *Store) OracleErrorsForTraces(ctx context.Context, traceIDs []string, from, to time.Time) ([]OracleErrorCount, error) {
	if len(traceIDs) == 0 {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT if(error_code != '', error_code, external_code) AS code, count() AS n,
		       substring(any(body), 1, 240), anyHeavy(channel_code)
		FROM oracle_error_log
		WHERE time >= ? AND time < ? AND trace_id IN (?)
		GROUP BY code
		ORDER BY n DESC
		LIMIT 10
		SETTINGS max_execution_time = 5`, from, to, traceIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OracleErrorCount
	for rows.Next() {
		var c OracleErrorCount
		if err := rows.Scan(&c.Code, &c.Count, &c.Sample, &c.Channel); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
