package chstore

// business_code.go — CoSRE iş kodu çözümleme (resolve_business_code).
//
// business_code_summary_5m: (tür, kod, servis, 5 dk) → span / hata sayısı.
// ORDER BY kodla başlar: sohbette SERVİS KAPSAMI OLMADAN "BQRZ001 hangi
// servis" okuması birkaç granül. Kod ifadeleri promoted kolonlarla AYNI
// (promotedAttrExpr — iki yazım), ama MATERIALIZED kolona değil diziye
// dayanır: kolon her kurulumda yok ve Distributed iletimde düşer.
//
// FUNCTION_CODE biçim kapısı (prod ölçümü 2026-10-15): günde 17 M farklı
// değerin ~%98'i GUID / sayısal ID / "LOGIN-43930337" gibi kod+ID karışımı;
// gerçek kodlar 7 karakter, büyük harf+rakam (BQRZ001, IQV0181) ve 1 saatte
// span'lerin >%90'ı. Kapı ^[A-Z][A-Z0-9]{4,9}$ dışını MV'ye hiç almaz
// (kardinalite sınırlı). CHANNEL_CODE (günde ~2,3 K değer) kapısız.

import (
	"context"
	"fmt"
	"time"
)

// FunctionCodeShape — MV'nin function_code biçim kapısı (SAF testler pinler).
const FunctionCodeShape = `^[A-Z][A-Z0-9]{4,9}$`

func businessCodeMVDDL() string {
	fc := promotedAttrExpr([]string{"FUNCTION_CODE", "function_code"})
	ch := promotedAttrExpr([]string{"CHANNEL_CODE", "channel_code"})
	return fmt.Sprintf(`CREATE MATERIALIZED VIEW IF NOT EXISTS business_code_summary_5m
		 ENGINE = AggregatingMergeTree
		 PARTITION BY toDate(time_bucket)
		 ORDER BY (kind, code, service_name, time_bucket)
		 TTL toDate(time_bucket) + INTERVAL 30 DAY
		 SETTINGS index_granularity = 8192
		 AS SELECT
		   kc.1                                        AS kind,
		   kc.2                                        AS code,
		   service_name,
		   toStartOfInterval(time, INTERVAL 5 MINUTE)  AS time_bucket,
		   countState()                                AS span_count_state,
		   countIfState(status_code = 'error')         AS error_count_state
		 FROM spans
		 ARRAY JOIN arrayFilter(x -> x.2 != '', [
		   ('function_code', if(match(%s, '%s'), %s, '')),
		   ('channel_code', %s)
		 ]) AS kc
		 GROUP BY kind, code, service_name, time_bucket`, fc, FunctionCodeShape, fc, ch)
}

// BusinessCodeIndex — penceredeki tekil (tür, kod) çiftleri: çözümleyicinin
// aday listesi (merdiven Go'da). ≤200 K satır (prod: ~17 K).
func (s *Store) BusinessCodeIndex(ctx context.Context, from, to time.Time) ([][2]string, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT DISTINCT kind, code FROM business_code_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ?
		LIMIT 200000
		SETTINGS max_execution_time = 10`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var k, c string
		if err := rows.Scan(&k, &c); err != nil {
			return nil, err
		}
		out = append(out, [2]string{k, c})
	}
	return out, rows.Err()
}

// CodeServiceStat — bir kodun bir servisteki span / hata sayısı.
type CodeServiceStat struct {
	Service string `json:"service"`
	Calls   uint64 `json:"calls"`
	Errors  uint64 `json:"errors"`
}

// BusinessCodeServices — verilen kodların servis kırılımı (kod başına ≤20 servis).
func (s *Store) BusinessCodeServices(ctx context.Context, kind string, codes []string, from, to time.Time) (map[string][]CodeServiceStat, error) {
	out := map[string][]CodeServiceStat{}
	if len(codes) == 0 {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT code, service_name, countMerge(span_count_state) AS c, countMerge(error_count_state) AS e
		FROM business_code_summary_5m
		WHERE kind = ? AND code IN (?) AND time_bucket >= ? AND time_bucket < ?
		GROUP BY code, service_name
		ORDER BY code, c DESC
		LIMIT 20 BY code
		SETTINGS max_execution_time = 10`, kind, codes, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var st CodeServiceStat
		if err := rows.Scan(&code, &st.Service, &st.Calls, &st.Errors); err != nil {
			return nil, err
		}
		out[code] = append(out[code], st)
	}
	return out, rows.Err()
}

// CatalogNames — katalogdaki tekil adlar (source: span | endpoint).
func (s *Store) CatalogNames(ctx context.Context, source string) ([]string, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT DISTINCT operation FROM cosre_operation_catalog
		WHERE source = ?
		LIMIT 300000
		SETTINGS max_execution_time = 10`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// CatalogServices — katalog adlarının servis kırılımı (son 24 sa toplamları).
func (s *Store) CatalogServices(ctx context.Context, source string, names []string) (map[string][]CodeServiceStat, error) {
	out := map[string][]CodeServiceStat{}
	if len(names) == 0 {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT operation, service, call_count, error_count
		FROM cosre_operation_catalog FINAL
		WHERE source = ? AND operation IN (?)
		ORDER BY operation, call_count DESC
		LIMIT 20 BY operation
		SETTINGS max_execution_time = 5`, source, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var st CodeServiceStat
		if err := rows.Scan(&name, &st.Service, &st.Calls, &st.Errors); err != nil {
			return nil, err
		}
		out[name] = append(out[name], st)
	}
	return out, rows.Err()
}

// BusinessCodeSeenRow — v0.10.2197 (CoSRE router D3-F): bir iş kodunun bir
// servisteki görülme özeti (tür, sayılar, ilk/son 5 dk kovası).
type BusinessCodeSeenRow struct {
	Kind      string    `json:"kind"` // function_code | channel_code
	Service   string    `json:"service"`
	Calls     uint64    `json:"calls"`
	Errors    uint64    `json:"errors"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

// BusinessCodeSeen — v0.10.2197: tek kodun iki türde servis kırılımı +
// ilk/son görülme. business_code_summary_5m nokta okuması (ORDER BY kind,
// code ile başlar); ham span YOK. ≤20 satır.
func (s *Store) BusinessCodeSeen(ctx context.Context, code string, from, to time.Time) ([]BusinessCodeSeenRow, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT kind, service_name, countMerge(span_count_state) AS c, countMerge(error_count_state) AS e,
		       min(time_bucket) AS f, max(time_bucket) AS l
		FROM business_code_summary_5m
		WHERE kind IN ('function_code', 'channel_code') AND code = ? AND time_bucket >= ? AND time_bucket < ?
		GROUP BY kind, service_name
		ORDER BY c DESC
		LIMIT 20
		SETTINGS max_execution_time = 5`, code, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BusinessCodeSeenRow
	for rows.Next() {
		var r BusinessCodeSeenRow
		if err := rows.Scan(&r.Kind, &r.Service, &r.Calls, &r.Errors, &r.FirstSeen, &r.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CatalogOperationsByFunctionCode — v0.10.2197: function_codes dizisinde
// kodu taşıyan katalog operasyonları (son 24 sa toplamları, ≤limit).
// cosre_operation_catalog küçük bir meta tablo; ham span YOK.
func (s *Store) CatalogOperationsByFunctionCode(ctx context.Context, code string, limit int) ([]CatalogOperation, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT operation, source, service, function_codes, last_seen, call_count, error_count
		FROM cosre_operation_catalog FINAL
		WHERE has(function_codes, ?)
		ORDER BY call_count DESC
		LIMIT ?
		SETTINGS max_execution_time = 3`, code, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatalogOperation
	for rows.Next() {
		var c CatalogOperation
		if err := rows.Scan(&c.Operation, &c.Source, &c.Service, &c.FunctionCodes, &c.LastSeen, &c.CallCount, &c.ErrorCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
