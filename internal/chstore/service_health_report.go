package chstore

// service_health_report.go — v0.10.2454 (operatör 2026-11-22: "X servisi
// nasıl" cevabı metrik, trace hatası, hatalı operasyonlar, teknik hatalar,
// exception ve messaging bölümlerini kaynağıyla taşımalı). mcptools
// get_service_health raporunun iki CH okuması; geri kalan bölümler mevcut
// okuyuculardan (ListExceptionGroups, MessagingFlow, DownstreamEdges,
// BaselineDeploys, service_summary_5m). SAF telemetri, telemetryReadConn.
//
// Sınırlar (prod ~27k span/s, ~2,3 milyar/gün; upstream tüm HATALARI + %5'i
// saklar → hatalı span'ler tam):
//   - ServiceErrorProfile: ham spans, YALNIZ hatalı span'ler —
//     PREWHERE service_name (ORDER BY öneki) + status_code = 'error'
//     [+ deploy_env], pencere ≤ 1 sa (sona yaslanır, cevapta beyan),
//     GROUPING SETS (operasyon × sınıf, toplam), LIMIT, max_execution_time 8,
//     max_rows_to_read 50M + read_overflow_mode='break' (kesilen okuma kısmi).
//   - ServiceOpTotals: operation_summary_5m (MV), ad listesi LİTERAL (alt
//     sorgu yok → GLOBAL IN gerekmez), LIMIT, max_execution_time 5.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	// HealthErrProfileMax — hata profili ham okumasının pencere tavanı.
	HealthErrProfileMax = time.Hour
	// HealthErrProfileGroups — (operasyon × sınıf) grup tavanı (+1 toplam satırı).
	HealthErrProfileGroups = 200
	healthOpTotalsMax      = 50
)

// ErrorProfileRow — bir (operasyon × hata sınıfı) grubu.
type ErrorProfileRow struct {
	Operation  string
	ErrorType  string // error.type özniteliği
	HTTPStatus uint16 // 0 = yok
	GRPCCode   string // rpc.grpc.status_code
	DBSystem   string
	Errors     uint64
	Traces     uint64
	Sample     string // örnek trace id
	Message    string // ilk dolu status_msg (≤120)
}

// ErrorProfile — servisin pencere içi hatalı span profili.
type ErrorProfile struct {
	From, To    time.Time
	Clamped     bool // pencere HealthErrProfileMax'a kırpıldı
	TotalErrors uint64
	ErrorTraces uint64 // ayrık hatalı trace (uniq, ±%2)
	Rows        []ErrorProfileRow
	Truncated   bool // grup tavanı doldu
}

// ServiceErrorProfileSQL — SAF. Bind: service, [env], from, to, limit.
func ServiceErrorProfileSQL(env bool) string {
	envCond := ""
	if env {
		envCond = " AND deploy_env = ?"
	}
	return `SELECT grouping(name) AS is_total, name, err_type, http_status, grpc_code, db_system,
       count() AS errors, uniq(trace_id) AS traces, any(trace_id) AS sample,
       substring(anyIf(status_msg, status_msg != ''), 1, 120) AS msg
FROM (
  SELECT name, attr_values[indexOf(attr_keys, 'error.type')] AS err_type, http_status,
         attr_values[indexOf(attr_keys, 'rpc.grpc.status_code')] AS grpc_code, db_system, trace_id, status_msg
  FROM spans
  PREWHERE service_name = ? AND status_code = 'error'` + envCond + `
  WHERE time >= ? AND time < ?
)
GROUP BY GROUPING SETS ((name, err_type, http_status, grpc_code, db_system), ())
ORDER BY is_total DESC, errors DESC
LIMIT ?
SETTINGS max_execution_time = 8, max_rows_to_read = ` + fmt.Sprint(LookupScopedRowsMax) + `, read_overflow_mode = 'break'`
}

// ServiceErrorProfileArgs — SAF: bind sırası ServiceErrorProfileSQL ile aynı.
func ServiceErrorProfileArgs(service, env string, from, to time.Time) []any {
	args := []any{service}
	if env != "" {
		args = append(args, env)
	}
	// +1 toplam satırı, +1 kesilme sezimi.
	return append(args, from, to, HealthErrProfileGroups+2)
}

// ServiceErrorProfile — servisin hatalı span'lerinin operasyon × sınıf
// dağılımı + toplam hata / ayrık hatalı trace.
func (s *Store) ServiceErrorProfile(ctx context.Context, service, env string, from, to time.Time) (ErrorProfile, error) {
	var p ErrorProfile
	service, env = strings.TrimSpace(service), strings.TrimSpace(env)
	if service == "" {
		return p, fmt.Errorf("service required")
	}
	p.From, p.To, p.Clamped = ClampLookupWindow(from, to, HealthErrProfileMax)
	rows, err := s.telemetryReadConn().Query(ctx, ServiceErrorProfileSQL(env != ""), ServiceErrorProfileArgs(service, env, p.From, p.To)...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var isTotal uint64
		var r ErrorProfileRow
		if err := rows.Scan(&isTotal, &r.Operation, &r.ErrorType, &r.HTTPStatus, &r.GRPCCode, &r.DBSystem,
			&r.Errors, &r.Traces, &r.Sample, &r.Message); err != nil {
			return p, err
		}
		if isTotal == 1 {
			p.TotalErrors, p.ErrorTraces = r.Errors, r.Traces
			continue
		}
		p.Rows = append(p.Rows, r)
	}
	if len(p.Rows) > HealthErrProfileGroups {
		p.Rows, p.Truncated = p.Rows[:HealthErrProfileGroups], true
	}
	return p, rows.Err()
}

// OpTotal — operasyonun pencere içi saklanan span / hata sayısı (MV).
type OpTotal struct {
	Calls  uint64
	Errors uint64
}

// ServiceOpTotalsSQL — SAF. Bind: service, names, from, to, limit.
const ServiceOpTotalsSQL = `SELECT name, countMerge(span_count_state) AS calls, countIfMerge(error_count_state) AS errs
FROM operation_summary_5m
WHERE service_name = ? AND name IN ? AND time_bucket >= ? AND time_bucket < ?
GROUP BY name
LIMIT ?
SETTINGS max_execution_time = 5`

// ServiceOpTotals — verilen operasyonların toplamları (operation_summary_5m;
// ortam boyutu YOK — çağıran env'li soruda bunu beyan eder).
func (s *Store) ServiceOpTotals(ctx context.Context, service string, names []string, from, to time.Time) (map[string]OpTotal, error) {
	out := map[string]OpTotal{}
	if strings.TrimSpace(service) == "" || len(names) == 0 {
		return out, nil
	}
	if len(names) > healthOpTotalsMax {
		names = names[:healthOpTotalsMax]
	}
	rows, err := s.telemetryReadConn().Query(ctx, ServiceOpTotalsSQL, service, names, from.Truncate(5*time.Minute), to, healthOpTotalsMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		var t OpTotal
		if err := rows.Scan(&n, &t.Calls, &t.Errors); err != nil {
			return nil, err
		}
		out[n] = t
	}
	return out, rows.Err()
}
