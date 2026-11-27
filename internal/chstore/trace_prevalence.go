package chstore

// trace_prevalence.go — v0.10.2483 (operatör onaylı): trace açıklamasının
// YAYGINLIK satırı ("bu hata tekil mi, yaygın mı?") için TEK sınırlı okuma.
//
// Anahtar: (servis, span adı) — Errors görünümünün (TopErrorOperations)
// gruplama anahtarı. MV-first: operation_summary_5m (ham spans'ta aggregate
// YOK); zaman-sınırlı (çağıran 1 saatlik pencere verir), tek satır aggregate +
// LIMIT 1, max_execution_time. "Eşiği aşan" sayısı MV'nin apdex durumlarından
// birebir: span_count − satisfied − tolerating = süresi 4T'yi aşan istek
// (OpFrustratedMs; T store.go canonicalMVs apdexT ile aynı — test pinler).

import (
	"context"
	"time"
)

// OpFrustratedMs — operation_summary_5m'nin apdex "frustrated" sınırı (4T, ms).
const OpFrustratedMs = 800

// OpPrevalence — bir operasyonun penceredeki çağrı / hata / eşik-üstü sayımı.
type OpPrevalence struct {
	Calls  uint64
	Errors uint64
	Over   uint64 // süresi OpFrustratedMs'i aşan
	P95Ms  float64
}

// opPrevalenceQuery — SAF: SQL + bind'ler (pin testte).
func opPrevalenceQuery(service, name string, from, to time.Time) (string, []any) {
	q := `SELECT countMerge(span_count_state),
	             countIfMerge(error_count_state),
	             countIfMerge(apdex_satisfied_state),
	             countIfMerge(apdex_tolerating_state),
	             arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6
	      FROM operation_summary_5m
	      WHERE service_name = ? AND name = ? AND time_bucket >= ? AND time_bucket < ?
	      LIMIT 1
	      SETTINGS max_execution_time = 3`
	return q, []any{service, name, alignBucketStart(from), to}
}

// OperationPrevalence — operation_summary_5m'den tek pencere.
func (s *Store) OperationPrevalence(ctx context.Context, service, name string, from, to time.Time) (OpPrevalence, error) {
	q, args := opPrevalenceQuery(service, name, from, to)
	q = s.weighSQL(q, from, "operation_summary_5m")
	var calls, errs, sat, tol uint64
	var p95 float64
	if err := s.conn.QueryRow(ctx, q, args...).Scan(&calls, &errs, &sat, &tol, &p95); err != nil {
		return OpPrevalence{}, err
	}
	return opPrevalenceOf(calls, errs, sat, tol, p95), nil
}

// opPrevalenceOf — SAF: ham sayımlar → sonuç (NaN p95 düşer; eksi taşma yok).
func opPrevalenceOf(calls, errs, sat, tol uint64, p95 float64) OpPrevalence {
	r := OpPrevalence{Calls: calls, Errors: errs}
	if sat+tol < calls {
		r.Over = calls - sat - tol
	}
	if p95 == p95 && p95 > 0 {
		r.P95Ms = p95
	}
	return r
}
